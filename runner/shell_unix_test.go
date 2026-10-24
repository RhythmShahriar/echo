// Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
// SPDX-License-Identifier: MIT

//go:build !windows

package runner

import (
	"errors"
	"os"
	"strconv"
	"strings"
	"syscall"
)

// alive reports whether pid is still running (kill -0). A zombie that is
// not reaped yet counts as gone: it runs nothing.
func alive(pid int) bool {
	err := syscall.Kill(pid, 0)
	if err != nil {
		return errors.Is(err, syscall.EPERM)
	}
	return !zombie(pid)
}

// zombie reports whether pid has exited and waits for its parent to reap
// it. Only Linux says so, through /proc; elsewhere it reports false.
func zombie(pid int) bool {
	b, err := os.ReadFile("/proc/" + strconv.Itoa(pid) + "/stat")
	if err != nil {
		return false
	}
	// The state follows the command name, which is in parentheses.
	_, rest, ok := strings.Cut(string(b), ") ")
	return ok && strings.HasPrefix(rest, "Z")
}
