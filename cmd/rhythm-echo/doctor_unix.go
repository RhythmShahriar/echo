// Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
// SPDX-License-Identifier: MIT

//go:build unix

package main

import (
	"os/exec"
	"syscall"
)

// killGroup starts cmd in a process group of its own and makes canceling
// it kill the whole group, so a child the shell started (sleep, wp cron)
// doesn't live on after Ctrl+C. The group ID is the child's PID, which
// Setpgid makes it.
func killGroup(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	cmd.Cancel = func() error {
		return syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
	}
}
