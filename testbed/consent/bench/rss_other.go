// Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
// SPDX-License-Identifier: MIT

//go:build !linux && !darwin

package main

import "os"

// maxRSS is unknown here: Windows reports no rusage for a child. The
// memory target is measured on Linux (Docker mode).
func maxRSS(*os.ProcessState) int64 { return 0 }
