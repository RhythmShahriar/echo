// Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
// SPDX-License-Identifier: MIT

//go:build windows

package runner

import (
	"os/exec"
	"strconv"
	"strings"
)

// alive reports whether a process with pid is listed by tasklist.
func alive(pid int) bool {
	out, err := exec.Command("tasklist", "/FI", "PID eq "+strconv.Itoa(pid), "/NH").Output() //nolint:gosec // a test's own child PID
	return err == nil && strings.Contains(string(out), " "+strconv.Itoa(pid)+" ")
}
