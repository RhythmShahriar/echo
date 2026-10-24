// Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
// SPDX-License-Identifier: MIT

//go:build !unix

package main

import "os/exec"

// killGroup leaves cmd as it is: Windows has no process groups to kill
// this way, so canceling kills only the process itself and WaitDelay
// stops Echo waiting on its children.
func killGroup(*exec.Cmd) {}
