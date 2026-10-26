// Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
// SPDX-License-Identifier: MIT

package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"time"
)

// Go mode's Echo for c18 (--echo): rhythm-echo up, sealed, on two of the
// probe range's ports, so it never meets a developer's own Echo on 8025.
const (
	echoAPI   = "127.0.0.1:23438"
	echoProxy = "127.0.0.1:23439"
)

// echoUp is a running rhythm-echo up.
type echoUp struct {
	cmd  *exec.Cmd
	done chan struct{}
	ca   string // its CA certificate, fetched from the proxy
}

// startEchoUp runs rhythm-echo up in dir with its own store and config
// folders and waits until it answers, then saves its CA for the testbed
// server's Conversions API sender.
func startEchoUp(ctx context.Context, bin, dir string) (*echoUp, error) {
	if answers(echoAPI) || answers(echoProxy) {
		return nil, errors.New("something already answers on " + echoAPI + " or " + echoProxy)
	}
	if err := os.MkdirAll(dir, 0o750); err != nil {
		return nil, err
	}
	log, err := os.Create(filepath.Join(dir, "echo.log")) //nolint:gosec // our own log
	if err != nil {
		return nil, err
	}
	cmd := exec.Command(bin, "up", "--seal", "--listen", echoAPI, "--smtp", "off", "--proxy", echoProxy, "--mitm", //nolint:gosec,noctx // the binary under test; stopped by stop
		"--store", filepath.Join(dir, "store"), "--plain")
	cmd.Dir = dir
	cmd.Env = append(os.Environ(), "RHYTHM_OFFLINE=1", "XDG_CONFIG_HOME="+filepath.Join(dir, "config"))
	cmd.Stdout, cmd.Stderr = log, log
	newGroup(cmd)
	if err := cmd.Start(); err != nil {
		_ = log.Close()
		return nil, err
	}
	e := &echoUp{cmd: cmd, done: make(chan struct{})}
	go func() { _ = cmd.Wait(); _ = log.Close(); close(e.done) }()
	for range 150 {
		select {
		case <-e.done:
			return nil, fmt.Errorf("rhythm-echo up exited (see %s)", log.Name())
		case <-ctx.Done():
			e.stop()
			return nil, ctx.Err()
		case <-time.After(100 * time.Millisecond):
		}
		if answers(echoAPI) && answers(echoProxy) {
			ca := filepath.Join(dir, "echo-ca.pem")
			if err := fetchCA(ctx, "http://"+echoProxy+"/ca.pem", ca); err != nil {
				e.stop()
				return nil, err
			}
			e.ca = ca
			return e, nil
		}
	}
	e.stop()
	return nil, fmt.Errorf("rhythm-echo up didn't answer on %s (see %s)", echoAPI, log.Name())
}

// fetchCA saves the PEM the proxy serves at u.
func fetchCA(ctx context.Context, u, path string) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		return err
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return err
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("the CA of rhythm-echo up: %s answered %s", u, resp.Status)
	}
	data, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return err
	}
	return os.WriteFile(path, data, 0o600)
}

// stop interrupts Echo and waits for it.
func (e *echoUp) stop() {
	if e == nil {
		return
	}
	_ = interrupt(e.cmd)
	select {
	case <-e.done:
	case <-time.After(10 * time.Second):
		_ = e.cmd.Process.Kill()
		<-e.done
	}
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
