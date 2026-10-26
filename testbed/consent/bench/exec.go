// Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
// SPDX-License-Identifier: MIT

package main

import (
	"bytes"
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/RhythmShahriar/echo/internal/browser/proc"
)

// Exec is how one command went.
type Exec struct {
	Exit   int           // the exit code; -1 when it couldn't start or was killed
	Stdout string        // what it printed
	Stderr string        // what it printed on stderr, plus why it couldn't start
	Wall   time.Duration // wall time
	MaxRSS int64         // the child's peak resident memory in bytes; 0 when unknown
}

// Output is stdout and stderr together, for says and never checks.
func (e Exec) Output() string { return e.Stdout + "\n" + e.Stderr }

// run starts cmd, waits for it and records how it went. A canceled
// context interrupts the child first (so rhythm-echo can close its
// browser and write what it has), then kills it after 15 seconds.
func run(cmd *exec.Cmd) Exec {
	var out, errOut bytes.Buffer
	if cmd.Stdout == nil {
		cmd.Stdout = &out
	}
	if cmd.Stderr == nil {
		cmd.Stderr = &errOut
	}
	proc.Interrupt(cmd)
	cmd.WaitDelay = 15 * time.Second
	_ = os.Remove(filepath.Join(cmd.Dir, rssFile))
	start := time.Now()
	err := cmd.Run()
	e := Exec{Wall: time.Since(start), Stdout: out.String(), Stderr: errOut.String()}
	var exitErr *exec.ExitError
	switch {
	case err == nil:
	case errors.As(err, &exitErr):
	default:
		e.Stderr += "\nbench: " + err.Error()
	}
	e.Exit = -1
	if cmd.ProcessState != nil {
		e.Exit = cmd.ProcessState.ExitCode()
		e.MaxRSS = maxRSS(cmd.ProcessState)
	}
	// In Docker mode GNU time in the runner wrote the peak of
	// rhythm-echo itself; the rusage above is docker's own client's.
	if data, err := os.ReadFile(filepath.Join(cmd.Dir, rssFile)); err == nil { //nolint:gosec // the run's own folder
		if kib, err := strconv.ParseInt(strings.TrimSpace(lastLine(string(data))), 10, 64); err == nil {
			e.MaxRSS = kib << 10
		}
	}
	return e
}

// lastLine is the last non-empty line of s.
func lastLine(s string) string {
	lines := strings.Split(strings.TrimSpace(s), "\n")
	return lines[len(lines)-1]
}

// execCommand is bin with args, for run.
func execCommand(ctx context.Context, bin string, args ...string) *exec.Cmd {
	return exec.CommandContext(ctx, bin, args...) //nolint:gosec // the binary under test
}
