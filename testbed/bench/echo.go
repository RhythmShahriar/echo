// Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
// SPDX-License-Identifier: MIT

//go:build testbed

package main

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"time"
)

// The testbed's Echo settings. The token is a fixed, local-only value: the
// sites send it (wp/mu-plugins/rhythm-echo/settings.php, compose's
// RHYTHM_ECHO_TOKEN) and Echo needs one because it listens beyond loopback
// so the containers can reach it.
const (
	echoAPI   = "http://127.0.0.1:18860"
	echoToken = "echo-testbed-token"
)

// stripeSecret is the test endpoint's signing secret, assembled so no
// secret-shaped literal sits in the source. testbed-stripe.php builds the
// same value.
var stripeSecret = strings.Join([]string{"whsec", "_", "testbed", strings.Repeat("0", 25)}, "")

// Echo is a `rhythm-echo up` the benchmark runs on the host.
type Echo struct {
	Bin, Testbed, Work string
	Mode               string
	cmd                *exec.Cmd
	log                *os.File
}

// env is what every Echo command gets: the testbed's CA folder (so the
// proxy's CA is the one WordPress trusts), the API and token, the Stripe
// secret for replay actions and TESTBED for the config's commands.
func (e *Echo) env() []string {
	return append(os.Environ(),
		"XDG_CACHE_HOME="+filepath.Join(e.Testbed, "runtime", "cache"),
		"RHYTHM_ECHO_URL="+echoAPI, "RHYTHM_ECHO_TOKEN="+echoToken,
		"TESTBED="+e.Testbed, "TESTBED_STRIPE_SECRET="+stripeSecret)
}

// Config is the testbed's Echo config file.
func (e *Echo) Config() string { return filepath.Join(e.Testbed, ".echo", "config.yaml") }

// Start runs Echo in mode (seal or observe), restarting it if it runs in
// the other mode and waits until it answers.
func (e *Echo) Start(ctx context.Context, mode string) error {
	if e.cmd != nil && e.Mode == mode {
		return nil
	}
	e.Stop()
	if healthy(ctx) {
		return errors.New("something already answers on " + echoAPI + " (a `make echo`?); stop it first")
	}
	if err := os.MkdirAll(e.Work, 0o750); err != nil {
		return err
	}
	log, err := os.OpenFile(filepath.Join(e.Work, "echo.log"), os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o600) //nolint:gosec // our own log
	if err != nil {
		return err
	}
	fmt.Fprintf(log, "\n--- %s: rhythm-echo up --%s\n", time.Now().Format(time.RFC3339), mode)
	cmd := exec.Command(e.Bin, "up", "--config", e.Config(), "--token", echoToken, "--"+mode, "--plain") //nolint:gosec,noctx // the binary under test; stopped with SIGINT
	cmd.Dir, cmd.Env, cmd.Stdout, cmd.Stderr = e.Work, e.env(), log, log
	if err := cmd.Start(); err != nil {
		_ = log.Close()
		return err
	}
	e.cmd, e.log, e.Mode = cmd, log, mode
	for i := 0; i < 100; i++ {
		if healthy(ctx) {
			return nil
		}
		time.Sleep(100 * time.Millisecond)
	}
	e.Stop()
	return fmt.Errorf("rhythm-echo up didn't answer on %s (see %s)", echoAPI, filepath.Join(e.Work, "echo.log"))
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
	e.cmd, e.Mode = nil, ""
}

func healthy(ctx context.Context) bool {
	ctx, cancel := context.WithTimeout(ctx, time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, echoAPI+"/v1/health", nil)
	if err != nil {
		return false
	}
	req.Header.Set("X-Echo-Token", echoToken)
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

// command runs name with args in dir and returns its combined output.
func command(ctx context.Context, timeout time.Duration, dir string, env []string, name string, args ...string) (string, error) {
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, name, args...) //nolint:gosec // the benchmark runs the tools it was asked to
	cmd.Dir, cmd.Env = dir, env
	var out bytes.Buffer
	cmd.Stdout, cmd.Stderr = &out, &out
	if err := cmd.Run(); err != nil {
		tail := strings.TrimSpace(out.String())
		if len(tail) > 400 {
			tail = "…" + tail[len(tail)-400:]
		}
		return out.String(), fmt.Errorf("%s %s: %w: %s", filepath.Base(name), strings.Join(args, " "), err, tail)
	}
	return out.String(), nil
}
