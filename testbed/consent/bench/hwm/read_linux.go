// Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
// SPDX-License-Identifier: MIT

//go:build linux

package hwm

import (
	"os"
	"strconv"
)

// Read is pid's own peak resident memory so far, in bytes.
func Read(pid int) (int64, bool) {
	data, err := os.ReadFile("/proc/" + strconv.Itoa(pid) + "/status")
	if err != nil {
		return 0, false
	}
	return Parse(data)
}
