// Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
// SPDX-License-Identifier: MIT

//go:build !windows

package runner

import (
	"context"
	"os/exec"
	"syscall"
)

// shell starts sh in a process group of its own, so cancel can kill the
// shell and everything it started with one signal.
func shell(ctx context.Context, cmd string) *exec.Cmd {
	c := exec.CommandContext(ctx, "sh", "-c", cmd) //nolint:gosec // the user's own configured command
	c.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	c.Cancel = func() error {
		// The group's ID is the shell's PID; a negative PID signals the
		// whole group.
		if err := syscall.Kill(-c.Process.Pid, syscall.SIGKILL); err != nil {
			return c.Process.Kill()
		}
		return nil
	}
	return c
}
