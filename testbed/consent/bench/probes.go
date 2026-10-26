// Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
// SPDX-License-Identifier: MIT

package main

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"math/big"
	"net"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/RhythmShahriar/echo/consent"
	"github.com/RhythmShahriar/echo/testbed/consent/judge"
)

// The environment probes, e01 to e18 of PRD 18.1. Each runs on its own
// testbed server and fixtures on 127.0.0.1:23410-23439 (ProbeEnv.Port),
// except e16's broken state, which only the sealed Docker testbed may
// run: its page asks for the cloud metadata address.
func init() {
	probeFuncs["site-down"] = siteDown
	probeFuncs["no-chrome"] = noChrome
	probeFuncs["bot-wall"] = botWall
	probeFuncs["cmp-absent"] = cmpAbsent
	probeFuncs["interrupt"] = interruptProbe
	probeFuncs["cpu-busy"] = cpuBusy
	probeFuncs["parallel"] = parallelProbe
	probeFuncs["held-everywhere"] = heldEverywhere
	probeFuncs["send-hits-production"] = sendHitsProduction
	probeFuncs["send-hits-complete"] = sendHitsComplete
	probeFuncs["old-baseline"] = oldBaseline
	probeFuncs["self-signed"] = selfSigned
	probeFuncs["no-banner-cmp"] = noBannerCMP
	probeFuncs["request-storm"] = requestStorm
	probeFuncs["huge-datalayer"] = hugeDatalayer
	probeFuncs["ssrf"] = ssrfProbe
	probeFuncs["hostile-text"] = hostileText
	probeFuncs["secrets"] = secretsProbe
}

// withSite runs f with the probe's testbed server up and stops it after.
func withSite(p *ProbeEnv, f func(*localSite) ([]Step, error)) ([]Step, error) {
	site, err := p.site()
	if err != nil {
		return nil, err
	}
	defer site.stop()
	return f(site)
}

// fixtureState sets scenario or fixture id on the probe's site.
func fixtureState(site *localSite, id string, broken bool) error {
	return site.srv.SetBroken(id, broken)
}

// siteDown (e01): the site doesn't answer. Echo must exit 3 and never
// say all is clear.
func siteDown(ctx context.Context, p *ProbeEnv, broken bool) ([]Step, error) {
	return withSite(p, func(site *localSite) ([]Step, error) {
		if broken {
			site.stop()
		}
		return []Step{p.consent(ctx, "check", site, nil, siteURL("g12"))}, nil
	})
}

// noChrome (e02): no Chrome anywhere Echo looks and downloads refused.
// The first step hides every Chrome it can (an empty PATH and cache);
// an installed Chrome at a fixed path is still found on some machines,
// so the checks are on the second step, whose --browser names nothing.
func noChrome(ctx context.Context, p *ProbeEnv, broken bool) ([]Step, error) {
	return withSite(p, func(site *localSite) ([]Step, error) {
		if !broken {
			return []Step{p.consent(ctx, "missing browser", site, nil, siteURL("g12"), "--no-download")}, nil
		}
		empty := filepath.Join(p.Work, "empty")
		if err := os.MkdirAll(empty, 0o750); err != nil {
			return nil, err
		}
		env := p.env("PATH="+empty, "XDG_CACHE_HOME="+empty, "LOCALAPPDATA="+empty, "HOME="+empty)
		hidden := p.consent(ctx, "no chrome", site, env, siteURL("g12"), "--no-download")
		missing := p.consent(ctx, "missing browser", site, nil, siteURL("g12"), "--no-download",
			"--browser", filepath.Join(empty, "no-chrome"))
		return []Step{hidden, missing}, nil
	})
}

// botWallPage is what a Cloudflare managed challenge answers.
const botWallPage = `<!doctype html><html><head><title>Just a moment...</title>` +
	`<script src="/cdn-cgi/challenge-platform/h/b/orchestrate/chl_page/v1"></script></head>` +
	`<body><div id="challenge-running">Checking if the site connection is secure</div></body></html>`

// botWall (e03): the site answers a bot check (a 403 challenge page)
// instead of the shop. Echo must exit 3 and name it, never report a
// site with nothing on it.
func botWall(ctx context.Context, p *ProbeEnv, broken bool) ([]Step, error) {
	h := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		if broken {
			w.Header().Set("Server", "cloudflare")
			w.Header().Set("Cf-Mitigated", "challenge")
			w.WriteHeader(http.StatusForbidden)
			_, _ = w.Write([]byte(botWallPage))
			return
		}
		_, _ = w.Write([]byte(`<!doctype html><html><head><title>Bot wall shop</title></head><body><h1>Shop</h1><a href="/page-2/">Products</a></body></html>`))
	})
	stop, err := serve(p.addr(portBotWall), h, nil)
	if err != nil {
		return nil, err
	}
	defer stop()
	host := "botwall.shop.test"
	return []Step{p.consent(ctx, "check", nil, nil, "https://"+host+"/",
		"--map-host", host+"="+p.addr(portBotWall), "--map-only")}, nil
}

// serve serves h on addr (with TLS when cfg is set) until stop.
func serve(addr string, h http.Handler, cfg *tls.Config) (stop func(), err error) {
	l, err := net.Listen("tcp", addr)
	if err != nil {
		return nil, err
	}
	if cfg != nil {
		l = tls.NewListener(l, cfg)
	}
	srv := &http.Server{Handler: h, ReadHeaderTimeout: 10 * time.Second, ErrorLog: nil}
	go func() { _ = srv.Serve(l) }()
	return func() { _ = srv.Close() }, nil
}

// cmpAbsent (e04): --cmp onetrust names a CMP the page doesn't have.
// Echo must report cmp_not_found (inconclusive, exit 3), not guess.
func cmpAbsent(ctx context.Context, p *ProbeEnv, broken bool) ([]Step, error) {
	return withSite(p, func(site *localSite) ([]Step, error) {
		id := "g04" // OneTrust
		if broken {
			id = "g03" // Cookiebot
		}
		return []Step{p.consent(ctx, "check", site, nil, siteURL(id), "--cmp", "onetrust", "--fail-on", "none")}, nil
	})
}

// interruptProbe (e05): Ctrl-C (SIGINT, or a console Ctrl-Break on
// Windows) while the Reject run is going. Echo must exit 130, keep the
// runs that finished with complete: false and leave no Chrome and no
// temporary profile behind. The child's temporary folder is the probe's
// own (a fresh short folder), so every profile Chrome makes is under it.
func interruptProbe(ctx context.Context, p *ProbeEnv, broken bool) ([]Step, error) {
	return withSite(p, func(site *localSite) ([]Step, error) {
		// A short folder of the system's, not one under the work
		// folder: Chrome puts its SingletonSocket there; a Unix
		// socket path over about 107 bytes makes Chrome fail to start.
		tmp, err := os.MkdirTemp("", "echo-e05-")
		if err != nil {
			return nil, err
		}
		defer func() { _ = os.RemoveAll(tmp) }()
		env := p.env("TMPDIR="+tmp, "TMP="+tmp, "TEMP="+tmp)
		ctx, cancel := context.WithTimeout(ctx, p.Timeout)
		defer cancel()
		cmd := p.command(ctx, "check", env, append([]string{"consent", siteURL("g01"), "--fail-on", "none", "--parallel", "1"}, site.mapArgs()...)...)
		newGroup(cmd)
		var s Step
		if !broken {
			s = p.finish("check", run(cmd))
		} else {
			done := make(chan Exec, 1)
			cmd.Cancel = func() error { return cmd.Process.Kill() }
			if err := startRun(cmd, done); err != nil {
				return nil, err
			}
			// Ctrl-C only once the Reject run's events file is there: a
			// press in another phase proves nothing about this one, so
			// the probe then lets the run end on its own and the "seen"
			// line it asks for is false.
			sawReject, ended := waitForReject(filepath.Join(p.Work, ".echo", "consent"), done, p.Timeout/2)
			if ended == nil && sawReject && cmd.Process != nil {
				_ = interrupt(cmd)
			}
			if ended == nil {
				e := <-done
				ended = &e
			}
			s = p.finish("check", *ended)
			s.note("reject run seen before the interrupt: %v", sawReject)
			if s.Complete == nil {
				s.Complete = completeFromStore(filepath.Join(p.Work, ".echo", "consent"))
			}
		}
		left, err := processesWith(ctx, tmp)
		if err != nil {
			s.note("chrome left: unknown (%v)", err)
		} else {
			s.note("chrome left: %d", left)
		}
		s.note("profile files left: %d", entriesUnder(tmp))
		return []Step{s}, nil
	})
}

// startRun starts cmd and sends how it went on done when it ends.
func startRun(cmd *exec.Cmd, done chan<- Exec) error {
	var out, errOut strings.Builder
	cmd.Stdout, cmd.Stderr = &out, &errOut
	start := time.Now()
	if err := cmd.Start(); err != nil {
		return err
	}
	go func() {
		_ = cmd.Wait()
		e := Exec{Exit: -1, Stdout: out.String(), Stderr: errOut.String(), Wall: time.Since(start)}
		if cmd.ProcessState != nil {
			e.Exit = cmd.ProcessState.ExitCode()
		}
		done <- e
	}()
	return nil
}

// waitForReject polls store for the Reject run's events file until it
// appears, the run ends (its Exec is returned) or limit passes.
func waitForReject(store string, done <-chan Exec, limit time.Duration) (bool, *Exec) {
	tick := time.NewTicker(100 * time.Millisecond)
	defer tick.Stop()
	deadline := time.After(limit)
	for {
		if rejectStarted(store) {
			return true, nil
		}
		select {
		case e := <-done:
			return rejectStarted(store), &e
		case <-deadline:
			return false, nil
		case <-tick.C:
		}
	}
}

// rejectStarted reports whether a result folder under store has the
// Reject run's events file yet (events/page-1-reject.ndjson).
func rejectStarted(store string) bool {
	found := false
	_ = filepath.WalkDir(store, func(path string, d fs.DirEntry, err error) error {
		if err == nil && !d.IsDir() && strings.Contains(d.Name(), "reject") && strings.HasSuffix(d.Name(), ".ndjson") {
			found = true
			return fs.SkipAll
		}
		return nil
	})
	return found
}

// completeFromStore reads completeness from the newest consent.json in
// store when no JSON report was written. consent.json is the bare
// result, which has no complete field (the envelope carries it), so it
// is decided the way consent decides it: an inconclusive entry, or a
// run that is inconclusive, truncated or canceled, makes it incomplete.
// A file that isn't a result says nothing (nil).
func completeFromStore(store string) *bool {
	var newest string
	var at time.Time
	_ = filepath.WalkDir(store, func(path string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() || d.Name() != "consent.json" {
			return nil
		}
		if fi, err := d.Info(); err == nil && fi.ModTime().After(at) {
			newest, at = path, fi.ModTime()
		}
		return nil
	})
	if newest == "" {
		return nil
	}
	data, err := os.ReadFile(newest) //nolint:gosec // the probe's own store
	if err != nil {
		return nil
	}
	var r consent.Result
	if err := json.Unmarshal(data, &r); err != nil || len(r.Pages) == 0 {
		return nil
	}
	c := resultComplete(&r)
	return &c
}

// resultComplete mirrors consent's own rule (Result.incomplete, PRD 9.3
// and 11.2): a no-banner run on a page that needs no consent is
// complete; any other inconclusive, truncated or canceled run is not.
func resultComplete(r *consent.Result) bool {
	if len(r.Inconclusive) > 0 {
		return false
	}
	for _, p := range r.Pages {
		for _, run := range p.Runs {
			if p.NeedsNone && run.Status == consent.StatusNoBanner && (run.Reason == consent.ReasonNone || run.Reason == consent.ReasonNoBanner) {
				continue
			}
			if run.Inconclusive() || run.Truncated || run.Status == consent.StatusCanceled {
				return false
			}
		}
	}
	return true
}

// cpuBusy (e06): every CPU busy while Echo checks. The verdicts must be
// those of an idle machine: no false alarm on g01, the bug still found
// on c02.
func cpuBusy(ctx context.Context, p *ProbeEnv, broken bool) ([]Step, error) {
	return withSite(p, func(site *localSite) ([]Step, error) {
		if broken {
			stop := burnCPUs()
			defer stop()
		}
		if err := fixtureState(site, "c02", true); err != nil {
			return nil, err
		}
		return []Step{
			p.consent(ctx, "g01", site, nil, siteURL("g01"), "--fail-on", "none"),
			p.consent(ctx, "c02", site, nil, siteURL("c02"), "--fail-on", "none"),
		}, nil
	})
}

// burnCPUs keeps every core busy until stop is called.
func burnCPUs() (stop func()) {
	done := make(chan struct{})
	var wg sync.WaitGroup
	for range runtime.NumCPU() {
		wg.Add(1)
		go func() {
			defer wg.Done()
			x := 0
			for {
				select {
				case <-done:
					return
				default:
					for i := range 1 << 16 {
						x += i
					}
				}
			}
		}()
	}
	return func() { close(done); wg.Wait() }
}

// parallelProbe (e07): --parallel 1 and --parallel 3 on the same broken
// page must find the same issues.
func parallelProbe(ctx context.Context, p *ProbeEnv, broken bool) ([]Step, error) {
	return withSite(p, func(site *localSite) ([]Step, error) {
		if err := fixtureState(site, "c02", true); err != nil {
			return nil, err
		}
		three := p.consent(ctx, "parallel 3", site, nil, siteURL("c02"), "--parallel", "3", "--fail-on", "none")
		if !broken {
			return []Step{three}, nil
		}
		one := p.consent(ctx, "parallel 1", site, nil, siteURL("c02"), "--parallel", "1", "--fail-on", "none")
		a, b := issueKeys(one.env), issueKeys(three.env)
		if slices.Equal(a, b) {
			three.note("same issues: yes")
		} else {
			three.note("same issues: no (parallel 1: %s; parallel 3: %s)", strings.Join(a, ", "), strings.Join(b, ", "))
		}
		return []Step{one, three}, nil
	})
}

// issueKeys lists e's issue keys, sorted; nil for no report.
func issueKeys(e *judge.Envelope) []string {
	if e == nil {
		return nil
	}
	var keys []string
	for _, is := range e.Data.Issues {
		keys = append(keys, is.Key)
	}
	slices.Sort(keys)
	return keys
}

// heldScenarios are the broken states whose tags send before the
// choice or after Reject, the hits a broken hold would leak.
var heldScenarios = []string{"c01", "c02", "c09", "c13", "c14", "c22", "c23"}

// heldEverywhere (e08): in the default (held) mode no tracker hit may
// reach the sink, even on pages that fire before the choice.
func heldEverywhere(ctx context.Context, p *ProbeEnv, broken bool) ([]Step, error) {
	return withSite(p, func(site *localSite) ([]Step, error) {
		ids := []string{"g01"}
		if broken {
			ids = heldScenarios
		}
		var steps []Step
		for _, id := range ids {
			if err := fixtureState(site, id, broken); err != nil {
				return nil, err
			}
			site.srv.ResetScenario(id)
			s := p.consent(ctx, id, site, nil, siteURL(id), "--fail-on", "none")
			c := site.srv.Count(id)
			s.note("sink hits: %d", c.Hits)
			steps = append(steps, s)
		}
		return steps, nil
	})
}

// sendHitsProduction (e09): --send-hits on a host listed under
// production: is refused (exit 2) before any browser starts: -v would
// print the browser line if one did.
func sendHitsProduction(ctx context.Context, p *ProbeEnv, broken bool) ([]Step, error) {
	return withSite(p, func(site *localSite) ([]Step, error) {
		cfg := "# e09: the shop is live.\n"
		if broken {
			cfg += "production:\n  - c01.shop.test\n"
		}
		if err := os.MkdirAll(filepath.Join(p.Work, ".echo"), 0o750); err != nil {
			return nil, err
		}
		if err := os.WriteFile(filepath.Join(p.Work, ".echo", "config.yaml"), []byte(cfg), 0o600); err != nil {
			return nil, err
		}
		return []Step{p.consent(ctx, "check", site, nil, siteURL("c01"), "--send-hits", "-v", "--fail-on", "none")}, nil
	})
}

// sendHitsComplete (e10): with --send-hits every hit Echo reports must
// have reached the sink and every hit at the sink must be in Echo's
// list, one for one by host and path. It proves Echo's hit list is
// complete without trusting Echo's own capture.
func sendHitsComplete(ctx context.Context, p *ProbeEnv, broken bool) ([]Step, error) {
	return withSite(p, func(site *localSite) ([]Step, error) {
		id := "g01"
		if broken {
			id = "c01"
			if err := fixtureState(site, id, true); err != nil {
				return nil, err
			}
		}
		site.srv.ResetScenario(id)
		s := p.consent(ctx, "check", site, nil, siteURL(id), "--send-hits", "--fail-on", "none")
		// Hits sent at the very end of a run may still be in flight.
		time.Sleep(time.Second)
		sink := map[string]int{}
		for _, h := range site.srv.Hits(id) {
			sink[h.Host+h.Path]++
		}
		reported := map[string]int{}
		if s.env != nil {
			s.env.Runs(func(_ consent.Page, r consent.Run) {
				for _, h := range r.Hits {
					if h.Kind == consent.HitScript {
						continue
					}
					if u, err := url.Parse(h.URL); err == nil {
						reported[u.Hostname()+u.EscapedPath()]++
					}
				}
			})
		}
		s.note("sink hits: %d, reported hits: %d", total(sink), total(reported))
		s.note("unmatched: %d%s", unmatched(sink, reported), describeUnmatched(sink, reported))
		return []Step{s}, nil
	})
}

// total sums counts.
func total(m map[string]int) int {
	n := 0
	for _, v := range m {
		n += v
	}
	return n
}

// unmatched is how many hits one side has that the other doesn't.
func unmatched(a, b map[string]int) int {
	n := 0
	for k, v := range a {
		n += max(v-b[k], 0)
	}
	for k, v := range b {
		n += max(v-a[k], 0)
	}
	return n
}

// describeUnmatched lists the differing addresses for the note.
func describeUnmatched(sink, reported map[string]int) string {
	var out []string
	for k := range sink {
		if sink[k] != reported[k] {
			out = append(out, fmt.Sprintf("%s sink %d reported %d", k, sink[k], reported[k]))
		}
	}
	for k := range reported {
		if _, ok := sink[k]; !ok {
			out = append(out, fmt.Sprintf("%s sink 0 reported %d", k, reported[k]))
		}
	}
	if len(out) == 0 {
		return ""
	}
	slices.Sort(out)
	return " (" + strings.Join(out, "; ") + ")"
}

// oldBaseline (e11): a baseline written by Echo 1.2.0
// (testdata/runs/1.2.0) loads and compares; the good state compares
// with a baseline this Echo saved itself.
func oldBaseline(ctx context.Context, p *ProbeEnv, broken bool) ([]Step, error) {
	return withSite(p, func(site *localSite) ([]Step, error) {
		against := "base"
		var steps []Step
		if broken {
			against = filepath.Join(p.Root, "testdata", "runs", "1.2.0", "g01", "consent.json")
		} else {
			steps = append(steps, p.consent(ctx, "save", site, nil, siteURL("g01"), "--save", "base", "--fail-on", "none"))
		}
		return append(steps, p.consent(ctx, "compare", site, nil, siteURL("g01"), "--against", against, "--fail-on", "none")), nil
	})
}

// selfSigned (e12): an https site with a self-signed certificate. With
// -k Echo checks it; without, it exits 3 naming the certificate.
func selfSigned(ctx context.Context, p *ProbeEnv, broken bool) ([]Step, error) {
	cert, err := selfSignedCert("localhost")
	if err != nil {
		return nil, err
	}
	h := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		_, _ = w.Write([]byte(`<!doctype html><html><head><title>Local shop</title></head><body><h1>Local shop</h1><a href="/page-2/">Products</a></body></html>`))
	})
	stop, err := serve(p.addr(portTLS), h, &tls.Config{Certificates: []tls.Certificate{cert}, MinVersion: tls.VersionTLS12})
	if err != nil {
		return nil, err
	}
	defer stop()
	_, port, _ := net.SplitHostPort(p.addr(portTLS))
	args := []string{"https://localhost:" + port + "/", "--fail-on", "none"}
	if !broken {
		args = append(args, "-k")
	}
	return []Step{p.consent(ctx, "check", nil, nil, args...)}, nil
}

// selfSignedCert makes a throwaway self-signed certificate for host.
func selfSignedCert(host string) (tls.Certificate, error) {
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return tls.Certificate{}, err
	}
	tmpl := &x509.Certificate{SerialNumber: big.NewInt(time.Now().UnixNano()), Subject: pkix.Name{CommonName: host},
		NotBefore: time.Now().Add(-time.Hour), NotAfter: time.Now().Add(24 * time.Hour), DNSNames: []string{host},
		KeyUsage: x509.KeyUsageDigitalSignature, ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth}}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &key.PublicKey, key)
	if err != nil {
		return tls.Certificate{}, err
	}
	return tls.Certificate{Certificate: [][]byte{der}, PrivateKey: key}, nil
}

// fixtureProbe runs one fixture site in the probe's state.
func fixtureProbe(id string, extra ...string) ProbeFunc {
	return func(ctx context.Context, p *ProbeEnv, broken bool) ([]Step, error) {
		return withSite(p, func(site *localSite) ([]Step, error) {
			if err := fixtureState(site, id, broken); err != nil {
				return nil, err
			}
			args := append([]string{siteURL(id), "--fail-on", "none"}, extra...)
			return []Step{p.consent(ctx, "check", site, nil, args...)}, nil
		})
	}
}

// noBannerCMP (e13): the CMP's script is on the page but its banner
// doesn't show within --banner-wait (slow-banner broken: Cookiebot
// after 15 s) and no tag fires. Echo must say it couldn't check (exit 3, no_banner).
func noBannerCMP(ctx context.Context, p *ProbeEnv, broken bool) ([]Step, error) {
	return fixtureProbe("slow-banner")(ctx, p, broken)
}

// requestStorm (e14): more requests than the per-run limit; Echo stops
// recording and says the run is cut short (limit-reached, exit 3).
func requestStorm(ctx context.Context, p *ProbeEnv, broken bool) ([]Step, error) {
	return fixtureProbe("request-storm")(ctx, p, broken)
}

// hugeDatalayer (e15): a dataLayer flood. Echo's memory stays bounded
// and the run says it was truncated.
func hugeDatalayer(ctx context.Context, p *ProbeEnv, broken bool) ([]Step, error) {
	steps, err := fixtureProbe("huge-datalayer")(ctx, p, broken)
	if err != nil {
		return nil, err
	}
	return steps, nil
}

// ssrfProbe (e16): a page that asks for private addresses (the admin
// listener; broken adds the cloud metadata address and 10.0.0.5). Every
// such request must be refused: the testbed's internal counter stays 0.
// It runs only in the sealed Docker testbed, where those addresses lead
// nowhere.
func ssrfProbe(ctx context.Context, p *ProbeEnv, broken bool) ([]Step, error) {
	if p.Bed == nil {
		return nil, errors.New("needs the sealed Docker testbed")
	}
	id := "ssrf-probe"
	if err := p.Bed.SetBroken(id, broken); err != nil {
		return nil, err
	}
	defer func() { _ = p.Bed.SetBroken(id, false) }()
	if err := p.Bed.ResetSink(id); err != nil {
		return nil, err
	}
	rel, err := filepath.Rel(p.benchWork(), p.Work)
	if err != nil {
		return nil, err
	}
	ctx, cancel := context.WithTimeout(ctx, p.Timeout)
	defer cancel()
	ex := run(p.Bed.Command(ctx, filepath.ToSlash(rel), []string{"consent", siteURL(id), "--json", "-o", stepFile("check"), "--fail-on", "none"}))
	s := p.finish("check", ex)
	c, err := p.Bed.Count(id)
	if err != nil {
		return nil, err
	}
	s.note("internal: %d", c.Internal)
	return []Step{s}, nil
}

// benchWork is the benchmark's work folder, two levels above a probe
// state's (work/env/<id>/<state>), which Docker mode mounts as /work.
func (p *ProbeEnv) benchWork() string { return filepath.Dir(filepath.Dir(filepath.Dir(p.Work))) }

// hostileMarkers are what must never reach an output raw from the
// hostile fixtures: terminal escapes and bell, bidi overrides and live
// markup in the HTML, JUnit and Markdown reports.
var hostileMarkers = []struct{ name, s string }{
	{"escape code", "\x1b"},
	{"bell", "\x07"},
	{"bidi override", "\u202e"},
	{"img tag", "<img src=x"},
	{"script tag", "<script>alert"},
	{"iframe tag", "<iframe src=//evil.test"},
}

// hostileText (e17): markup, escape codes, bidi overrides, Markdown and
// CSV injection in a banner and a page. Every output (terminal, JSON,
// Markdown, JUnit, HTML) must carry them escaped.
func hostileText(ctx context.Context, p *ProbeEnv, broken bool) ([]Step, error) {
	return withSite(p, func(site *localSite) ([]Step, error) {
		var steps []Step
		for _, id := range []string{"hostile-banner", "hostile-text"} {
			if err := fixtureState(site, id, broken); err != nil {
				return nil, err
			}
			s := p.consent(ctx, id, site, nil, siteURL(id), "--fail-on", "none", "--plain",
				"-o", id+".md", "-o", id+".xml", "-o", id+".html")
			var found []string
			for _, f := range []string{id + ".md", id + ".xml", id + ".html"} {
				data, err := os.ReadFile(filepath.Join(p.Work, f)) //nolint:gosec // the probe's own reports
				if err != nil {
					found = append(found, f+" missing")
					continue
				}
				for _, m := range hostileMarkers {
					if strings.Contains(string(data), m.s) {
						found = append(found, f+" has a raw "+m.name)
					}
				}
			}
			for _, m := range hostileMarkers[:3] {
				if strings.Contains(s.Output, m.s) {
					found = append(found, "the terminal output has a raw "+m.name)
				}
			}
			if len(found) == 0 {
				s.note("raw hostile text: none")
			} else {
				s.note("raw hostile text: %s", strings.Join(found, ", "))
			}
			steps = append(steps, s)
		}
		return steps, nil
	})
}

// secretsProbe (e18): a token in the page URL that the tags copy into
// every hit, a header and a cookie passed through env:, personal data in
// hits. No raw value (nor its URL-encoded forms) may be in any output or
// file. The values are made at run time: a literal token in the tree
// would be a real-looking secret (and GitHub push protection blocks
// those).
func secretsProbe(ctx context.Context, p *ProbeEnv, broken bool) ([]Step, error) {
	return withSite(p, func(site *localSite) ([]Step, error) {
		token := fakeToken()
		auth, cookie := fakeToken(), fakeToken()
		env := p.env("RHYTHM_ECHO_TB_AUTH=Bearer "+auth, "RHYTHM_ECHO_TB_COOKIE=tb_session="+cookie)
		secrets := []string{token, auth, cookie, "buyer@shop.test", "buyer-4711", "+15550100"}
		var steps []Step
		for _, id := range []string{"secret-url", "pii-beacons"} {
			if err := fixtureState(site, id, broken); err != nil {
				return nil, err
			}
			u := siteURL(id) + "?token=" + url.QueryEscape(token) + "&email=buyer%40shop.test"
			s := p.consent(ctx, id, site, env, u, "--fail-on", "none", "--har", "-v",
				"-H", "Authorization: env:RHYTHM_ECHO_TB_AUTH", "--cookie", "env:RHYTHM_ECHO_TB_COOKIE",
				"-o", id+".md", "-o", id+".xml", "-o", id+".html")
			leaks := findSecrets(p.Work, s.Output, secrets)
			if len(leaks) == 0 {
				s.note("raw secrets found: none")
			} else {
				s.note("raw secrets found: %s", strings.Join(leaks, ", "))
			}
			// The leak list names files, never the values themselves.
			steps = append(steps, s)
		}
		return steps, nil
	})
}

// fakeToken is a random test token, assembled at run time.
func fakeToken() string {
	b := make([]byte, 16)
	_, _ = rand.Read(b)
	return "tb" + "tok" + "_" + hex.EncodeToString(b)
}

// encodings are a secret's raw, URL-encoded and double-encoded forms.
func encodings(s string) []string {
	once := url.QueryEscape(s)
	return slices.Compact([]string{s, once, url.QueryEscape(once), url.PathEscape(s)})
}

// findSecrets lists where any form of any secret appears: in printed
// (the step's output) or in a file under dir. It names the place and the
// secret's index, never the value.
func findSecrets(dir, printed string, secrets []string) []string {
	var found []string
	scan := func(where, text string) {
		for i, sec := range secrets {
			for _, form := range encodings(sec) {
				if strings.Contains(text, form) {
					found = append(found, fmt.Sprintf("secret %d in %s", i+1, where))
					break
				}
			}
		}
	}
	scan("the output", printed)
	_ = filepath.WalkDir(dir, func(path string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() || strings.HasPrefix(path, filepath.Join(dir, "testbed")) {
			return nil
		}
		data, err := os.ReadFile(path) //nolint:gosec // the probe's own folder
		if err == nil {
			rel, _ := filepath.Rel(dir, path)
			scan(filepath.ToSlash(rel), string(data))
		}
		return nil
	})
	return found
}
