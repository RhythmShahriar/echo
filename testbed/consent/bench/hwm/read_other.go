// Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
// SPDX-License-Identifier: MIT

//go:build !linux

package hwm

// Read is unknown outside Linux: no other system the benchmark runs on
// keeps a per-process mark another process can read without cgo.
func Read(int) (int64, bool) { return 0, false }
