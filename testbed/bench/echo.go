// Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
// SPDX-License-Identifier: MIT

//go:build testbed

package main

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"time"
)

// echoAPI is where the testbed's Echo answers on the host.
const echoAPI = "http://127.0.0.1:18860"

// minToken is the shortest token Echo accepts beyond loopback.
const minToken = 16

// TestbedToken reads the token of this testbed run: make up writes a fresh
// one to runtime/token and hands it to every container (compose's
// RHYTHM_ECHO_TOKEN, the WordPress connector's settings, the sendmail
// shim's SMTP login). Echo needs one because it listens beyond loopback
// so the containers can reach it. It is masked in everything the
// benchmark writes.
func TestbedToken(testbed string) (string, error) {
	f := filepath.Join(testbed, "runtime", "token")
	data, err := os.ReadFile(f) //nolint:gosec // the testbed's own file
	if err != nil {
		return "", fmt.Errorf("no token for this testbed run in %s; start the testbed with make -C testbed up: %w", f, err)
	}
	t := strings.TrimSpace(string(data))
	if len(t) < minToken {
		return "", fmt.Errorf("the token in %s is shorter than %d characters, which Echo refuses beyond loopback; run make -C testbed up again", f, minToken)
	}
	addSecret(t)
	return t, nil
}

// newToken returns a fresh random token, registered for masking.
func newToken() string {
	b := make([]byte, 24)
	_, _ = rand.Read(b)
	t := hex.EncodeToString(b)
	addSecret(t)
	return t
}

// secrets are the values masked in every output the benchmark keeps.
var secrets struct {
	sync.Mutex
	list []string
}

func addSecret(s string) {
	secrets.Lock()
	defer secrets.Unlock()
	secrets.list = append(secrets.list, s)
}

// mask replaces every token the benchmark uses in s with •••, so neither
// results.json nor docs/benchmark.md (nor an error) carries one.
func mask(s string) string {
	secrets.Lock()
	defer secrets.Unlock()
	for _, t := range secrets.list {
		if t != "" {
			s = strings.ReplaceAll(s, t, "•••")
		}
	}
	return s
}

// stripeSecret is the test endpoint's signing secret, assembled so no
// secret-shaped literal sits in the source. testbed-stripe.php builds the
// same value.
var stripeSecret = strings.Join([]string{"whsec", "_", "testbed", strings.Repeat("0", 25)}, "")

// Echo is a `rhythm-echo up` the benchmark runs on the host.
type Echo struct {
	Bin, Testbed, Work string
	Token              string // this testbed run's, from TestbedToken
	// Home, when set, replaces the testbed's runtime folder as the cache
	// and config folders: another Echo version keeps its own CA there.
	Home string
	Mode string
	// Ports, when set, moves the API, SMTP sink and proxy off the ports
	// the testbed's gate forwards (18860-18862), so the sites can't reach
	// Echo while the benchmark still can.
	Ports []string
	cmd   *exec.Cmd
	log   *os.File
	ports string // what the running Echo listens on
}

// api is the base URL the running Echo answers on.
func (e *Echo) api() string {
	if len(e.Ports) == 3 {
		return "http://127.0.0.1:" + e.Ports[0]
	}
	return echoAPI
}

// env is what every Echo command gets: the testbed's CA and config
// folders (so the proxy's CA is the one WordPress trusts and no Echo, an
// older one included, writes to the user's), no update check, the API
// and token (in the
// environment, never on a command line where ps shows it), the Stripe
// secret for replay actions and TESTBED for the config's commands.
func (e *Echo) env() []string {
	home := filepath.Join(e.Testbed, "runtime")
	if e.Home != "" {
		home = e.Home
	}
	return append(os.Environ(),
		"XDG_CACHE_HOME="+filepath.Join(home, "cache"), "XDG_CONFIG_HOME="+filepath.Join(home, "config"), "RHYTHM_NO_UPDATE_CHECK=1",
		"RHYTHM_ECHO_URL="+e.api(), "RHYTHM_ECHO_TOKEN="+e.Token,
		"TESTBED="+e.Testbed, "TESTBED_STRIPE_SECRET="+stripeSecret)
}

// Store is where a command run for the benchmark keeps its snapshots and
// runs: dir/.echo. Echo keeps them beside the config by default (since
// v1.1), which would put every scenario's "good" snapshot in one place.
func Store(dir string) []string { return []string{"--store", filepath.Join(dir, ".echo")} }

// Config is the testbed's Echo config file.
func (e *Echo) Config() string { return filepath.Join(e.Testbed, ".echo", "config.yaml") }

// Start runs Echo in mode (seal or observe), restarting it if it runs in
// the other mode and waits until it answers.
func (e *Echo) Start(ctx context.Context, mode string) error {
	if e.cmd != nil && e.Mode == mode && e.ports == strings.Join(e.Ports, ",") {
		return nil
	}
	e.Stop()
	if healthy(ctx, e.api(), e.Token) {
		return errors.New("something already answers on " + e.api() + " (a `make echo`?); stop it first")
	}
	if err := os.MkdirAll(e.Work, 0o750); err != nil {
		return err
	}
	log, err := os.OpenFile(filepath.Join(e.Work, "echo.log"), os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o600) //nolint:gosec // our own log
	if err != nil {
		return err
	}
	fmt.Fprintf(log, "\n--- %s: rhythm-echo up --%s\n", time.Now().Format(time.RFC3339), mode)
	args := []string{"up", "--config", e.Config(), "--" + mode, "--plain"}
	if len(e.Ports) == 3 {
		args = append(args, "--listen", "0.0.0.0:"+e.Ports[0], "--smtp", "0.0.0.0:"+e.Ports[1], "--proxy", "0.0.0.0:"+e.Ports[2])
	}
	cmd := exec.Command(e.Bin, args...) //nolint:gosec,noctx // the binary under test; stopped with SIGINT
	cmd.Dir, cmd.Env, cmd.Stdout, cmd.Stderr = e.Work, e.env(), log, log
	if err := cmd.Start(); err != nil {
		_ = log.Close()
		return err
	}
	e.cmd, e.log, e.Mode, e.ports = cmd, log, mode, strings.Join(e.Ports, ",")
	for i := 0; i < 100; i++ {
		if healthy(ctx, e.api(), e.Token) {
			return nil
		}
		time.Sleep(100 * time.Millisecond)
	}
	e.Stop()
	return fmt.Errorf("rhythm-echo up didn't answer on %s (see %s)", e.api(), filepath.Join(e.Work, "echo.log"))
}

// Stop stops Echo.
func (e *Echo) Stop() {
	if e.cmd == nil {
		return
	}
	_ = e.cmd.Process.Signal(syscall.SIGINT)
	done := make(chan struct{})
	go func() { _ = e.cmd.Wait(); close(done) }()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		_ = e.cmd.Process.Kill()
		<-done
	}
	_ = e.log.Close()
	e.cmd, e.Mode, e.ports = nil, "", ""
}

func healthy(ctx context.Context, api, token string) bool {
	ctx, cancel := context.WithTimeout(ctx, time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, api+"/v1/health", nil)
	if err != nil {
		return false
	}
	req.Header.Set("X-Echo-Token", token)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return false
	}
	_ = resp.Body.Close()
	return resp.StatusCode == http.StatusOK
}

// Run runs an Echo command in dir.
func (e *Echo) Run(ctx context.Context, timeout time.Duration, dir string, args ...string) (string, error) {
	return command(ctx, timeout, dir, e.env(), e.Bin, args...)
}

// Shell runs one of the manifest's reset commands (sh -c, from the testbed
// folder), with no RHYTHM_ECHO_RUN: what it sends belongs to no action.
func (e *Echo) Shell(ctx context.Context, timeout time.Duration, line string) error {
	_, err := command(ctx, timeout, e.Testbed, e.env(), "sh", "-c", line)
	return err
}

// command runs name with args in dir and returns its standard output,
// which --json keeps apart from the notes on standard error. A failure's
// error ends with what both said, masked.
func command(ctx context.Context, timeout time.Duration, dir string, env []string, name string, args ...string) (string, error) {
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, name, args...) //nolint:gosec // the benchmark runs the tools it was asked to
	cmd.Dir, cmd.Env = dir, env
	both := &syncBuffer{}
	stdout := &teeWriter{own: &bytes.Buffer{}, both: both}
	cmd.Stdout, cmd.Stderr = stdout, both
	if err := cmd.Run(); err != nil {
		tail := strings.TrimSpace(both.String())
		if len(tail) > 400 {
			tail = "…" + tail[len(tail)-400:]
		}
		return stdout.String(), errors.New(mask(fmt.Sprintf("%s %s: %v: %s", filepath.Base(name), strings.Join(args, " "), err, tail)))
	}
	return stdout.String(), nil
}

// syncBuffer is a bytes.Buffer safe for the two goroutines os/exec copies
// a command's standard output and standard error with.
type syncBuffer struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (b *syncBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.Write(p)
}

func (b *syncBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.String()
}

// teeWriter keeps standard output on its own and adds it to both. Only
// the standard output goroutine writes own, so it needs no lock.
type teeWriter struct {
	own  *bytes.Buffer
	both *syncBuffer
}

func (t *teeWriter) Write(p []byte) (int, error) {
	t.own.Write(p)
	return t.both.Write(p)
}

func (t *teeWriter) String() string { return t.own.String() }
