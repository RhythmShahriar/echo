// Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
// SPDX-License-Identifier: MIT

package conformance

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"os/signal"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/RhythmShahriar/echo/catcher"
	"github.com/RhythmShahriar/echo/wire"
)

// Usage is the help for `rhythm echo conformance`.
var Usage = `Usage: rhythm echo conformance [flags]

Runs a strict stand-in Echo for a connector's end-to-end test. Point the
connector at it (RHYTHM_ECHO_URL), run the test, then stop it (Ctrl-C or
SIGTERM): it prints a pass or fail table and exits 1 on any fail. Every
capture is checked against Echo's wire schema; a bad one is answered 400
with what is wrong. GET /v1/conformance returns the report so far as JSON.

Flags:
  --listen <addr>    API address (default 127.0.0.1:8025)
  --smtp <addr>      also run Echo's SMTP sink there (default off)
  --mode <m>         seal (default) or observe
  --token <t>        require this X-Echo-Token
  --allow <hosts>    hosts reachable when sealed, comma separated
  --wire <n>         speak this wire version, to check a connector falls back (default ` + strconv.Itoa(wire.Version) + `)
  --expect <list>    scenarios the run must include, comma separated:
                     email, email-after, http-before, http-after, run, site,
                     attachments, wire-1, wire-2
  --json             print the report as JSON instead of a table
`

// Main runs the stand-in until SIGINT or SIGTERM, then prints its report.
// The exit code is 0 when every check passed, 1 when one failed and 2 for
// bad flags.
func Main(args []string, _ io.Reader, stdout, stderr io.Writer) int {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	return serve(ctx, args, stdout, stderr, nil)
}

// serve is Main until ctx ends. ready, when set, gets the API's address
// once it listens.
func serve(ctx context.Context, args []string, stdout, stderr io.Writer, ready func(addr string)) int {
	fs := flag.NewFlagSet("conformance", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	listen := fs.String("listen", "127.0.0.1:8025", "")
	smtp := fs.String("smtp", "off", "")
	mode := fs.String("mode", catcher.Seal, "")
	token := fs.String("token", "", "")
	allow := fs.String("allow", "", "")
	version := fs.Int("wire", wire.Version, "")
	expect := fs.String("expect", "", "")
	asJSON := fs.Bool("json", false, "")
	if err := fs.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			fmt.Fprint(stdout, Usage)
			return 0
		}
		fmt.Fprintf(stderr, "rhythm echo conformance: %v\n\n%s", err, Usage)
		return 2
	}
	if fs.NArg() > 0 {
		fmt.Fprintf(stderr, "rhythm echo conformance: takes no arguments, got %q\n\n%s", fs.Arg(0), Usage)
		return 2
	}
	s, err := New(Options{Mode: *mode, Token: *token, Allow: list(*allow), Wire: *version, Expect: list(*expect)})
	if err != nil {
		fmt.Fprintf(stderr, "rhythm echo conformance: %v\n", err)
		return 2
	}

	ln, err := net.Listen("tcp", *listen)
	if err != nil {
		fmt.Fprintf(stderr, "rhythm echo conformance: %v\n", err)
		return 1
	}
	api := &http.Server{Handler: s, ReadHeaderTimeout: 10 * time.Second}
	errs := make(chan error, 2)
	go func() { errs <- api.Serve(ln) }()
	var sink interface {
		ListenAndServe() error
		Close() error
	}
	if *smtp != "" && *smtp != "off" {
		sink = catcher.NewSMTP(s.Catcher(), *smtp)
		go func() { errs <- sink.ListenAndServe() }()
	}
	fmt.Fprintf(stderr, "rhythm echo conformance: stand-in Echo on http://%s (%s, wire %d)", ln.Addr(), *mode, *version)
	if sink != nil {
		fmt.Fprintf(stderr, ", SMTP %s", *smtp)
	}
	fmt.Fprintln(stderr)
	if ready != nil {
		ready(ln.Addr().String())
	}

	code := 0
	select {
	case <-ctx.Done():
	case err := <-errs:
		if !errors.Is(err, http.ErrServerClosed) {
			fmt.Fprintf(stderr, "rhythm echo conformance: %v\n", err)
			code = 1
		}
	}
	shut, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if sink != nil {
		_ = sink.Close()
	}
	_ = api.Shutdown(shut)

	r := s.Report()
	if *asJSON {
		enc := json.NewEncoder(stdout)
		enc.SetIndent("", "  ")
		_ = enc.Encode(r)
	} else {
		r.Table(stdout)
	}
	if !r.OK {
		code = 1
	}
	return code
}

func list(s string) []string {
	var out []string
	for _, p := range strings.Split(s, ",") {
		if p = strings.TrimSpace(p); p != "" {
			out = append(out, p)
		}
	}
	return out
}
