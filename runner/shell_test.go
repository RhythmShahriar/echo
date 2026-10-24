// Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
// SPDX-License-Identifier: MIT

package runner

import (
	"context"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"testing"
	"time"
)

// longCommand writes the PID of a 25 second child to pidFile, waits for
// it, then prints done: the shape of a command action whose shell is not
// the process that keeps running.
func longCommand(pidFile string) string {
	if runtime.GOOS == "windows" {
		// powershell starts the child and records its PID; ping is the
		// portable 25 second sleep.
		return `powershell -NoProfile -Command "$p = Start-Process -PassThru -NoNewWindow ping -ArgumentList '-n','26','127.0.0.1'; Set-Content -Path '` +
			pidFile + `' -Value $p.Id; $p.WaitForExit(); 'done'"`
	}
	return `sleep 25 & echo $! > '` + pidFile + `'; wait; echo done`
}

// A canceled command action stops its whole process tree and returns at
// once, instead of when the child exits (23 s after Ctrl+C before).
func TestCommandCanceledStopsChildren(t *testing.T) {
	pidFile := filepath.Join(t.TempDir(), "pid")
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- command(ctx, "", longCommand(pidFile), "r/a", nil) }()

	pid := 0
	for deadline := time.Now().Add(10 * time.Second); pid == 0 && time.Now().Before(deadline); {
		if b, err := os.ReadFile(pidFile); err == nil {
			pid, _ = strconv.Atoi(strings.TrimSpace(string(b)))
		}
		time.Sleep(20 * time.Millisecond)
	}
	if pid == 0 {
		cancel()
		t.Fatal("the command never wrote its child's PID")
	}
	time.Sleep(200 * time.Millisecond)
	cancel()
	start := time.Now()
	select {
	case err := <-done:
		if err == nil {
			t.Error("a canceled command reported success")
		}
	case <-time.After(10 * time.Second):
		t.Fatal("command still running 10 s after cancel")
	}
	if d := time.Since(start); d > 3*time.Second {
		t.Errorf("command returned %v after cancel, want under 3s", d)
	}
	// The kill is asynchronous; give the system a moment to reap.
	for deadline := time.Now().Add(2 * time.Second); alive(pid); time.Sleep(50 * time.Millisecond) {
		if time.Now().After(deadline) {
			t.Errorf("child %d is still running after cancel", pid)
			break
		}
	}
}

func TestShellEnv(t *testing.T) {
	cmd := `echo "$ECHO_SHELL_TEST"`
	if runtime.GOOS == "windows" {
		cmd = `echo %ECHO_SHELL_TEST%`
	}
	out, err := Shell(context.Background(), cmd, []string{"ECHO_SHELL_TEST=hello there"}).Output()
	if err != nil || strings.TrimSpace(string(out)) != "hello there" {
		t.Errorf("Shell: %q %v", out, err)
	}
}
