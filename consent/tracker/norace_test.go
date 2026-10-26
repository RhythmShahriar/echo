// Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
// SPDX-License-Identifier: MIT

//go:build !race

package tracker

import "time"

// matchBudget is how long classifying one fuzzed request may take
// (qa.md 8.1 #8: under 5 ms).
const matchBudget = 5 * time.Millisecond
