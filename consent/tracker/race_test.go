// Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
// SPDX-License-Identifier: MIT

//go:build race

package tracker

import "time"

// matchBudget is how long classifying one fuzzed request may take. The
// race detector slows everything down about tenfold.
const matchBudget = 50 * time.Millisecond
