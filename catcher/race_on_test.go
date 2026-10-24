// Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
// SPDX-License-Identifier: MIT

//go:build race

package catcher

// slowdown scales time limits in tests: the race detector makes regular
// expressions about fifteen times slower.
const slowdown = 20
