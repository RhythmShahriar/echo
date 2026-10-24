// Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
// SPDX-License-Identifier: MIT

//go:build windows

package runner

import (
	"context"
	"os/exec"
	"strconv"
	"syscall"
)

// shell runs cmd with cmd /S /C in a new process group. The command line
// is passed as written: Go's argument quoting is for programs that parse
// arguments the C runtime way, which cmd does not. On cancel taskkill /T
// ends the whole tree, since Windows has no signal for a process group.
func shell(ctx context.Context, cmd string) *exec.Cmd {
	c := exec.CommandContext(ctx, "cmd")
	c.SysProcAttr = &syscall.SysProcAttr{
		CmdLine:       `cmd /S /C "` + cmd + `"`,
		CreationFlags: syscall.CREATE_NEW_PROCESS_GROUP,
	}
	c.Cancel = func() error {
		kill := exec.Command("taskkill", "/T", "/F", "/PID", strconv.Itoa(c.Process.Pid)) //nolint:gosec // our own child's PID
		if err := kill.Run(); err != nil {
			return c.Process.Kill()
		}
		return nil
	}
	return c
}
