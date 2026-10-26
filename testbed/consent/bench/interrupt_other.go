// Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
// SPDX-License-Identifier: MIT

//go:build !windows

package main

import (
	"os"
	"os/exec"
)

// newGroup prepares cmd so interrupt can reach it; Unix needs nothing.
func newGroup(*exec.Cmd) {}

// interrupt sends cmd's process what Ctrl-C in its terminal would: SIGINT.
func interrupt(cmd *exec.Cmd) error { return cmd.Process.Signal(os.Interrupt) }
