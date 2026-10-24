// Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
// SPDX-License-Identifier: MIT

package hub

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"errors"
	"flag"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/RhythmShahriar/echo/catcher"
	"github.com/RhythmShahriar/rhythm/kit/cli"
)

// Version is reported by the hub's API and stored runs. The CLI sets it
// to its own version before calling Main or ScheduleMain.
var Version = "dev"

// Usage is the help for rhythm echo hub.
const Usage = `usage: rhythm echo hub --store DIR --sites FILE [flags]

One Echo for many staging sites. Each site sends with its own write token
(captures only); the read token sees every site. The inbox is kept in the
store across restarts.

  --store <dir>        where the inbox, TLS files and results are kept (required)
  --sites <file>       sites.yaml: id, token_env, config and schedule per site (required)
  --listen <addr>      the API (default 127.0.0.1:8025)
  --smtp <addr>        the SMTP sink (default 127.0.0.1:1025; off turns it off);
                       log in with any username and the site's write token
  --seal | --observe   seal (default): nothing leaves; observe: sent for real
  --allow <host>       a host reachable while sealed (repeat, or a comma list)
  --keep <n>           messages kept (default 10000)
  --tls-cert <file> --tls-key <file>
                       serve the API over HTTPS and offer STARTTLS on SMTP
  --tls-self-signed    the same with a certificate the hub makes and keeps in the store
  --tls-host <names>   more names or IPs for that certificate (comma list)

sites.yaml:
  read_token_env: ECHO_HUB_READ_TOKEN   # default RHYTHM_ECHO_TOKEN
  sites:
    - id: client-a
      token_env: ECHO_TOKEN_CLIENT_A
      config: /srv/sites/client-a/.echo/config.yaml
      schedule: "Tue 02:00"

Reading: GET /v1/messages?site=client-a, GET /v1/sites and the rest of
Echo's API, with the read token in X-Echo-Token.
`

// Main runs the hub until interrupted, for rhythm echo hub. It returns 0
// when stopped, 2 for a wrong command line or sites file and 3 when it
// can't listen or keep the inbox.
func Main(args []string, _ io.Reader, _ io.Writer, stderr io.Writer) int {
	fs := flag.NewFlagSet("hub", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	storeDir := fs.String("store", "", "")
	sitesFile := fs.String("sites", "", "")
	listen := fs.String("listen", "127.0.0.1:8025", "")
	smtpAddr := fs.String("smtp", "127.0.0.1:1025", "")
	seal := fs.Bool("seal", false, "")
	observe := fs.Bool("observe", false, "")
	var allow, tlsHosts cli.ListFlag
	fs.Var(&allow, "allow", "")
	keep := fs.Int("keep", 10000, "")
	certFile := fs.String("tls-cert", "", "")
	keyFile := fs.String("tls-key", "", "")
	selfSigned := fs.Bool("tls-self-signed", false, "")
	fs.Var(&tlsHosts, "tls-host", "")
	usage := func(format string, a ...any) int {
		fmt.Fprintf(stderr, "rhythm echo hub: "+format+"\n\n%s", append(a, Usage)...)
		return 2
	}
	if err := fs.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			fmt.Fprint(stderr, Usage)
			return 0
		}
		return usage("%v", err)
	}
	switch {
	case fs.NArg() > 0:
		return usage("unexpected argument %q", fs.Arg(0))
	case *storeDir == "" || *sitesFile == "":
		return usage("--store and --sites are required")
	case *seal && *observe:
		return usage("pick --seal or --observe, not both")
	case (*certFile == "") != (*keyFile == ""):
		return usage("--tls-cert and --tls-key go together")
	case *selfSigned && *certFile != "":
		return usage("pick --tls-self-signed or --tls-cert and --tls-key, not both")
	case *keep <= 0:
		return usage("--keep must be at least 1")
	}
	sites, err := LoadSites(*sitesFile, os.Getenv)
	if err != nil {
		return usage("%v", err)
	}
	mode := "seal"
	if *observe {
		mode = "observe"
	}
	var tlsCfg *tls.Config
	switch {
	case *selfSigned:
		tlsCfg, err = SelfSigned(*storeDir, tlsHosts, time.Now())
	case *certFile != "":
		tlsCfg, err = LoadTLS(*certFile, *keyFile)
	}
	if err != nil {
		fmt.Fprintln(stderr, "rhythm echo hub: TLS:", err)
		return 3
	}
	h, err := New(Options{Store: *storeDir, Sites: sites, Mode: mode, Allow: allow, Keep: *keep, Version: Version})
	if err != nil {
		fmt.Fprintln(stderr, "rhythm echo hub:", err)
		return 3
	}
	defer func() {
		if err := h.Close(); err != nil {
			fmt.Fprintln(stderr, "rhythm echo hub: keeping the inbox:", err)
		}
	}()
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	return serve(ctx, h, *listen, *smtpAddr, tlsCfg, *storeDir, stderr)
}

// serve listens and serves until ctx ends.
func serve(ctx context.Context, h *Hub, listen, smtpAddr string, tlsCfg *tls.Config, storeDir string, stderr io.Writer) int {
	ln, err := net.Listen("tcp", listen)
	if err != nil {
		fmt.Fprintf(stderr, "rhythm echo hub: listening on %s: %v\n", listen, err)
		return 3
	}
	api := &http.Server{Handler: h, ReadHeaderTimeout: 10 * time.Second, TLSConfig: tlsCfg}
	errs := make(chan error, 2)
	scheme := "http"
	if tlsCfg != nil {
		scheme = "https"
		go func() { errs <- api.ServeTLS(ln, "", "") }()
	} else {
		go func() { errs <- api.Serve(ln) }()
	}
	fmt.Fprintf(stderr, "Echo hub (%s mode): %d sites, %d messages kept in %s\n", h.Mode(), len(h.o.Sites.Sites),
		len(h.Inbox.List(catcher.Filter{})), storeDir)
	fmt.Fprintf(stderr, "  API   %s://%s  (read token: %s)\n", scheme, ln.Addr(), h.o.Sites.ReadTokenEnv)
	stopSMTP := func() {}
	if smtpAddr != "" && smtpAddr != "off" {
		sl, err := net.Listen("tcp", smtpAddr)
		if err != nil {
			_ = api.Close()
			fmt.Fprintf(stderr, "rhythm echo hub: SMTP on %s: %v\n", smtpAddr, err)
			return 3
		}
		sink := h.SMTP(smtpAddr, tlsCfg)
		go func() { errs <- sink.Serve(sl) }()
		stopSMTP = func() { _ = sink.Close() }
		how := "log in with any username and the site's write token"
		if tlsCfg != nil {
			how += ", after STARTTLS"
		}
		fmt.Fprintf(stderr, "  SMTP  %s  (%s)\n", sl.Addr(), how)
	}
	if tlsCfg != nil {
		if fp, err := Fingerprint(tlsCfg); err == nil {
			fmt.Fprintf(stderr, "  TLS   certificate SHA-256 %s\n", fp)
		}
	} else if !loopback(listen) {
		fmt.Fprintln(stderr, "  Warning: tokens cross the network in clear; add --tls-self-signed, or --tls-cert and --tls-key.")
	}
	for _, s := range h.o.Sites.Sites {
		fmt.Fprintf(stderr, "  Site  %s  (write token: %s)\n", s.ID, s.TokenEnv)
	}
	code := 0
	select {
	case <-ctx.Done():
	case err := <-errs:
		if err != nil && !errors.Is(err, http.ErrServerClosed) {
			fmt.Fprintln(stderr, "rhythm echo hub:", err)
			code = 3
		}
	}
	sctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	_ = api.Shutdown(sctx)
	stopSMTP()
	return code
}

func loopback(addr string) bool {
	host, _, err := net.SplitHostPort(addr)
	if err != nil {
		return false
	}
	ip := net.ParseIP(host)
	return host == "localhost" || ip != nil && ip.IsLoopback()
}

// ScheduleUsage is the help for rhythm echo schedule.
const ScheduleUsage = `usage: rhythm echo schedule "<when>" --store DIR --after "<command>" [flags]

Runs an update window for the hub's sites: for each site in turn, a
snapshot of what its actions send, the update command in the site's folder,
then a compare. Results are kept in the store, per site.

  <when>              "Tue 02:00", "Mon,Thu 03:30", "daily 02:00", "weekdays 22:00"
                      (local time); not needed with --now
  --store <dir>       the hub's store (required)
  --file <file>       sites.yaml (default sites.yaml)
  --sites <ids>       all (default) or a comma list of site IDs
  --after <command>   the update, run with sh -c and RHYTHM_ECHO_SITE set (required)
  --after-timeout <d> stop the command after this long (default 30m)
  --snapshot <name>   compare with this snapshot instead of taking one first
  --url <url>         the hub's API (default http://127.0.0.1:8025)
  --ca <file>         trust this certificate for an HTTPS hub (default: the
                      hub's self-signed one in the store, when there is one)
  --now               run one window now and exit
  --once              wait for the next window, run it and exit

Exit codes with --now and --once: 0 nothing broke, 1 a site broke or its
update failed, 2 wrong command line or sites file, 3 couldn't run.
`

// ScheduleMain runs update windows, for rhythm echo schedule.
func ScheduleMain(args []string, _ io.Reader, stdout, stderr io.Writer) int {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	return scheduleMain(ctx, args, stdout, stderr, time.Now, sleep)
}

func sleep(ctx context.Context, d time.Duration) error {
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-t.C:
		return nil
	}
}

// scheduleMain is ScheduleMain with the clock and the wait injected.
func scheduleMain(ctx context.Context, args []string, stdout, stderr io.Writer, now func() time.Time,
	wait func(context.Context, time.Duration) error) int {
	fs := flag.NewFlagSet("schedule", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	storeDir := fs.String("store", "", "")
	file := fs.String("file", "sites.yaml", "")
	list := fs.String("sites", "all", "")
	afterCmd := fs.String("after", "", "")
	afterTimeout := fs.Duration("after-timeout", 30*time.Minute, "")
	snap := fs.String("snapshot", "", "")
	url := fs.String("url", "http://127.0.0.1:8025", "")
	ca := fs.String("ca", "", "")
	runNow := fs.Bool("now", false, "")
	once := fs.Bool("once", false, "")
	usage := func(format string, a ...any) int {
		fmt.Fprintf(stderr, "rhythm echo schedule: "+format+"\n\n%s", append(a, ScheduleUsage)...)
		return 2
	}
	pos, err := cli.ParseInterspersed(fs, args)
	if err != nil {
		if errors.Is(err, flag.ErrHelp) {
			fmt.Fprint(stderr, ScheduleUsage)
			return 0
		}
		return usage("%v", err)
	}
	var sched Schedule
	switch {
	case *storeDir == "":
		return usage("--store is required")
	case *afterCmd == "":
		return usage(`--after is required: the update to run, like "wp plugin update --all"`)
	case *runNow && *once:
		return usage("pick --now or --once, not both")
	case len(pos) > 1:
		return usage("one schedule, in quotes: %q", strings.Join(pos, " "))
	case len(pos) == 0 && !*runNow:
		return usage(`say when, like "Tue 02:00", or run one window now with --now`)
	case len(pos) == 1:
		if sched, err = ParseSchedule(pos[0]); err != nil {
			return usage("%v", err)
		}
	}
	sites, err := LoadSites(*file, os.Getenv)
	if err != nil {
		return usage("%v", err)
	}
	chosen, err := sites.Select(*list)
	if err != nil {
		return usage("--sites: %v", err)
	}
	httpClient, err := hubClient(*url, *ca, *storeDir)
	if err != nil {
		fmt.Fprintln(stderr, "rhythm echo schedule:", err)
		return 3
	}
	o := WindowOptions{Store: *storeDir, Sites: chosen, URL: *url, Token: sites.readToken, HTTP: httpClient, After: *afterCmd, Hide: sites.SecretEnv(),
		AfterTimeout: *afterTimeout, Snapshot: *snap, Version: Version, Now: now, Log: stdout}
	for {
		if !*runNow {
			next := sched.Next(now())
			fmt.Fprintf(stderr, "Next window: %s (in %s)\n", next.Format("Mon 2 Jan 15:04 MST"), next.Sub(now()).Round(time.Minute))
			if err := wait(ctx, next.Sub(now())); err != nil {
				return 0 // stopped while waiting
			}
		}
		results := RunWindow(ctx, o)
		if ctx.Err() != nil {
			fmt.Fprintln(stderr, "rhythm echo schedule: canceled; the window is partial")
			return 130
		}
		if *runNow || *once {
			code := 0
			for _, r := range results {
				if r.Broke() {
					code = 1
				}
			}
			return code
		}
	}
}

// hubClient returns an HTTP client that trusts ca, or the hub's own
// self-signed certificate in the store, for an https URL.
func hubClient(url, ca, storeDir string) (*http.Client, error) {
	if !strings.HasPrefix(url, "https://") {
		return nil, nil
	}
	if ca == "" {
		cert, _ := TLSFiles(storeDir)
		if _, err := os.Stat(cert); err != nil {
			return nil, nil // a certificate the system trusts
		}
		ca = cert
	}
	pem, err := os.ReadFile(ca) //nolint:gosec // the operator's own file
	if err != nil {
		return nil, err
	}
	pool := x509.NewCertPool()
	if !pool.AppendCertsFromPEM(pem) {
		return nil, fmt.Errorf("%s holds no PEM certificate", ca)
	}
	return &http.Client{Timeout: 70 * time.Second, Transport: &http.Transport{TLSClientConfig: &tls.Config{RootCAs: pool, MinVersion: tls.VersionTLS12}}}, nil
}
