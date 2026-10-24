// Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
// SPDX-License-Identifier: MIT

//go:build testbed

package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/RhythmShahriar/echo/model"
	"github.com/RhythmShahriar/echo/store"
)

// Addresses of the local environment probes: an Echo, a fixture site and
// a fixture resolver, all on 127.0.0.1:23200-23239, apart from the
// testbed's ports.
const (
	localAPI  = "127.0.0.1:23200"
	localSMTP = "127.0.0.1:23201"
	localSite = "127.0.0.1:23210"
	localDNS  = "127.0.0.1:23220"
	silentDNS = "127.0.0.1:23221" // takes queries and never answers
)

// localToken is the local probes' own token, fresh for every run and
// masked like the testbed's.
var localToken = newToken()

// probeConfig is the config every probe folder starts with. Echo finds
// the nearest .echo/config.yaml here or above (since v1.1) and the work
// folder is inside testbed/, whose config listens on 0.0.0.0 for the
// containers: without this one a local probe would run with that.
const probeConfig = "# A probe's own settings, so Echo doesn't read testbed/.echo/config.yaml above.\nmode: seal\n"

// ProbeEnv is what a probe works with.
type ProbeEnv struct {
	Bin     string        // the rhythm-echo under test
	Root    string        // the module root (for building Echo 1.0.0 and reading fixtures)
	Testbed string        // the testbed folder
	Work    string        // a fresh folder for this scenario and state
	Timeout time.Duration // per command
	Windows bool          // running on Windows

	// Site is the benchmark's context for scenarios that need the Docker
	// testbed; nil without it.
	Site *SiteBench
}

// SiteBench is what the testbed probes share with the site scenarios.
type SiteBench struct {
	Echo     *Echo
	Manifest *Manifest
	Options  options
	old      oldEcho
	// oldCLI: the CLI is Echo 1.0.0's, which keeps its runs in the folder
	// it runs in and has no --store.
	oldCLI bool
}

// oldEcho builds Echo 1.0.0 once, for the version skew probe.
type oldEcho struct {
	once sync.Once
	bin  string
	err  error
}

// For returns p for one state of scenario id, in a new empty folder.
func (p *ProbeEnv) For(id, state string) *ProbeEnv {
	q := *p
	q.Work = filepath.Join(p.Work, id, state)
	_ = os.RemoveAll(q.Work)
	_ = os.MkdirAll(filepath.Join(q.Work, ".echo"), 0o750)
	_ = writeConfig(q.Work, probeConfig)
	return &q
}

// dir returns a folder under the probe's work folder, created.
func (p *ProbeEnv) dir(name string) string {
	d := filepath.Join(p.Work, name)
	_ = os.MkdirAll(filepath.Join(d, ".echo"), 0o750)
	return d
}

// env is what every local Echo command gets: the local Echo's addresses,
// no update check and config and cache folders of its own, so a probe
// never reads or writes the user's.
func (p *ProbeEnv) env(token string) []string {
	return append(os.Environ(),
		"RHYTHM_ECHO_URL=http://"+localAPI, "RHYTHM_ECHO_TOKEN="+token, "RHYTHM_ECHO_SMTP="+localSMTP,
		"RHYTHM_NO_UPDATE_CHECK=1", "XDG_CONFIG_HOME="+filepath.Join(p.Work, "config"), "XDG_CACHE_HOME="+filepath.Join(p.Work, "cache"))
}

// writeConfig writes a project's .echo/config.yaml.
func writeConfig(dir, yaml string) error {
	return os.WriteFile(filepath.Join(dir, ".echo", "config.yaml"), []byte(yaml), 0o600)
}

// mailCmd writes raw, a whole RFC 5322 message, to dir/name.eml and
// returns a command action line that sends it through the sendmail
// stand-in of the binary under test, which tags it with the run. Input
// redirection reads the same in sh and cmd.
func (p *ProbeEnv) mailCmd(dir, name, raw string) (string, error) {
	file := filepath.Join(dir, name+".eml")
	if err := os.WriteFile(file, []byte(strings.ReplaceAll(raw, "\n", "\r\n")), 0o600); err != nil {
		return "", err
	}
	return `"` + p.Bin + `" sendmail -t < "` + file + `"`, nil
}

// plainMail is a short text email from the shop.
func plainMail(to, subject, body string) string {
	return "From: Bean & Leaf <shop@shop.example.com>\nTo: " + to + "\nSubject: " + subject +
		"\nMIME-Version: 1.0\nContent-Type: text/plain; charset=utf-8\n\n" + body + "\n"
}

// yamlQuote quotes s for a YAML double-quoted scalar.
func yamlQuote(s string) string {
	b, _ := json.Marshal(s)
	return string(b)
}

// localEcho is a `rhythm-echo up` on the local probe ports.
type localEcho struct {
	cmd  *exec.Cmd
	done chan struct{}
	log  *os.File
}

// startEcho runs `rhythm-echo up` sealed on the local ports with args
// added and waits until it answers.
func (p *ProbeEnv) startEcho(ctx context.Context, token string, args ...string) (*localEcho, error) {
	if answers(localAPI) {
		return nil, errors.New("something already answers on " + localAPI + "; stop it first")
	}
	log, err := os.OpenFile(filepath.Join(p.Work, "echo.log"), os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o600) //nolint:gosec // our own log
	if err != nil {
		return nil, err
	}
	args = append([]string{"up", "--seal", "--listen", localAPI, "--smtp", localSMTP, "--plain"}, args...)
	cmd := exec.Command(p.Bin, args...) //nolint:gosec,noctx // the binary under test; stopped with SIGINT
	cmd.Dir, cmd.Env, cmd.Stdout, cmd.Stderr = p.Work, p.env(token), log, log
	if err := cmd.Start(); err != nil {
		_ = log.Close()
		return nil, err
	}
	e := &localEcho{cmd: cmd, done: make(chan struct{}), log: log}
	go func() { _ = cmd.Wait(); close(e.done) }()
	listen := localAPI
	for i := range args {
		if args[i] == "--listen" && i+1 < len(args) {
			listen = args[i+1]
		}
	}
	listen = strings.Replace(listen, "0.0.0.0", "127.0.0.1", 1)
	for range 100 {
		select {
		case <-e.done:
			_ = log.Close()
			return nil, fmt.Errorf("rhythm-echo up exited (see %s)", log.Name())
		case <-ctx.Done():
			e.Stop()
			return nil, ctx.Err()
		case <-time.After(100 * time.Millisecond):
		}
		if answers(listen) {
			return e, nil
		}
	}
	e.Stop()
	return nil, fmt.Errorf("rhythm-echo up didn't answer on %s (see %s)", listen, log.Name())
}

// Stop stops Echo and waits until its ports are free.
func (e *localEcho) Stop() {
	if e == nil {
		return
	}
	if runtime.GOOS == "windows" {
		_ = e.cmd.Process.Kill()
	} else {
		_ = e.cmd.Process.Signal(syscall.SIGINT)
	}
	select {
	case <-e.done:
	case <-time.After(5 * time.Second):
		_ = e.cmd.Process.Kill()
		<-e.done
	}
	_ = e.log.Close()
}

// answers reports whether something accepts connections on addr.
func answers(addr string) bool {
	c, err := net.DialTimeout("tcp", addr, 200*time.Millisecond)
	if err != nil {
		return false
	}
	_ = c.Close()
	return true
}

// step runs one Echo command in dir as the named step: with --json -o
// <step>.json, so the observation has the verdict and what was sent and,
// for a compare, the changes; the output is everything printed plus the
// JSON report.
func (p *ProbeEnv) step(ctx context.Context, name, dir, token string, args ...string) Observation {
	return p.stepEnv(ctx, name, dir, p.env(token), args...)
}

// stepEnv is step with the environment env.
func (p *ProbeEnv) stepEnv(ctx context.Context, name, dir string, env []string, args ...string) Observation {
	file := filepath.Join(dir, strings.ReplaceAll(name, " ", "-")+".json")
	_ = os.Remove(file)
	args = append(args, "--json", "-o", file)
	exit, out := p.run(ctx, dir, env, p.Bin, args...)
	o := Observation{Step: name, Exit: exit, Output: out}
	data, err := os.ReadFile(file) //nolint:gosec // our own output
	if err != nil {
		return o
	}
	o.Output += "\n" + string(data)
	var envelope struct {
		Kind    string          `json:"kind"`
		Verdict string          `json:"verdict"`
		Data    json.RawMessage `json:"data"`
	}
	if json.Unmarshal(data, &envelope) != nil {
		return o
	}
	o.Verdict = envelope.Verdict
	switch envelope.Kind {
	case "":
		// A bare run, as Echo 1.0.0 writes it, with no envelope.
		var r model.Run
		if json.Unmarshal(data, &r) == nil && r.Schema != "" {
			o.Output += "\n" + sentLines(&r)
		}
	case "run":
		var r model.Run
		if json.Unmarshal(envelope.Data, &r) == nil {
			o.Output += "\n" + sentLines(&r)
		}
	case "comparison":
		if c, err := readComparison(file); err == nil {
			o.Changes = c.Changes
			if r, err := store.LoadRun(under(dir, c.After.Dir)); err == nil {
				o.Output += "\n" + sentLines(r)
			}
		}
	}
	return o
}

// sentLines lists what a run's actions sent and how they failed, one
// line each, for checks to read: "message: email to ann@shop.test [faked]".
func sentLines(r *model.Run) string {
	var b strings.Builder
	for _, a := range r.Actions {
		if a.Error != "" {
			fmt.Fprintf(&b, "action error: %s: %s\n", a.Label, a.Error)
		}
		for _, m := range a.Messages {
			fmt.Fprintf(&b, "message: %s to %s [%s]\n", m.Channel, strings.Join(m.To, ", "), m.Status)
		}
	}
	return b.String()
}

// run runs a command and returns its exit code (-1 when it couldn't
// start or timed out) and everything it printed, masked.
func (p *ProbeEnv) run(ctx context.Context, dir string, env []string, name string, args ...string) (int, string) {
	ctx, cancel := context.WithTimeout(ctx, cmpTimeout(p.Timeout))
	defer cancel()
	cmd := exec.CommandContext(ctx, name, args...) //nolint:gosec // the benchmark runs the binary under test
	cmd.Dir, cmd.Env = dir, env
	var out bytes.Buffer
	cmd.Stdout, cmd.Stderr = &out, &out // the same writer: os/exec writes it from one goroutine at a time
	err := cmd.Run()
	var ee *exec.ExitError
	switch {
	case err == nil:
		return 0, mask(out.String())
	case errors.As(err, &ee) && ctx.Err() == nil:
		return ee.ExitCode(), mask(out.String())
	}
	return -1, mask(out.String() + "\n" + err.Error())
}

func cmpTimeout(d time.Duration) time.Duration {
	if d <= 0 {
		return 2 * time.Minute
	}
	return d
}

// fixtureSite is a small site on localSite whose handler a probe sets.
type fixtureSite struct {
	srv *http.Server
	ln  net.Listener
}

// startSite serves h on localSite.
func startSite(h http.Handler) (*fixtureSite, error) {
	ln, err := net.Listen("tcp", localSite)
	if err != nil {
		return nil, err
	}
	s := &fixtureSite{srv: &http.Server{Handler: h, ReadHeaderTimeout: 5 * time.Second}, ln: ln}
	go func() { _ = s.srv.Serve(ln) }()
	return s, nil
}

// Stop stops the site; nothing answers on localSite after.
func (s *fixtureSite) Stop() {
	if s == nil {
		return
	}
	_ = s.srv.Close()
}

// status sends a GET to Echo on 127.0.0.1 with the Host header host and,
// when set, the token header, then returns the status code.
func status(ctx context.Context, path, host, token string) (int, error) {
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, "http://"+localAPI+path, nil)
	if err != nil {
		return 0, err
	}
	req.Host = host
	if token != "" {
		req.Header.Set("X-Echo-Token", token)
	}
	c := &http.Client{CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	resp, err := c.Do(req)
	if err != nil {
		return 0, err
	}
	_, _ = io.Copy(io.Discard, resp.Body)
	_ = resp.Body.Close()
	return resp.StatusCode, nil
}
