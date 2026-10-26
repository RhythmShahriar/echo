// Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
// SPDX-License-Identifier: MIT

package main

import (
	"os"
	"syscall"
)

// maxRSS is a finished child's peak resident memory in bytes. Linux
// counts ru_maxrss in KiB.
func maxRSS(ps *os.ProcessState) int64 {
	if ru, ok := ps.SysUsage().(*syscall.Rusage); ok {
		return ru.Maxrss << 10
	}
	return 0
}
