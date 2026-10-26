// Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
// SPDX-License-Identifier: MIT

//go:build windows

package main

import (
	"os/exec"
	"syscall"

	"golang.org/x/sys/windows"
)

// newGroup starts cmd in a process group of its own, so a console
// control event can be sent to it alone and not to the benchmark.
func newGroup(cmd *exec.Cmd) {
	if cmd.SysProcAttr == nil {
		cmd.SysProcAttr = &syscall.SysProcAttr{}
	}
	cmd.SysProcAttr.CreationFlags |= windows.CREATE_NEW_PROCESS_GROUP
}

// interrupt sends cmd's process group a Ctrl-Break console event. Go
// programs (rhythm-echo among them) get it as os.Interrupt, as they get
// Ctrl-C; Ctrl-C itself can't be sent to one group, Windows delivers it
// to every process on the console.
func interrupt(cmd *exec.Cmd) error {
	return windows.GenerateConsoleCtrlEvent(windows.CTRL_BREAK_EVENT, uint32(cmd.Process.Pid)) //nolint:gosec // a PID fits
}
