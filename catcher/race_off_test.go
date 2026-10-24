// Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
// SPDX-License-Identifier: MIT

//go:build !race

package catcher

// slowdown scales time limits in tests (see race_on_test.go).
const slowdown = 1
