// Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
// SPDX-License-Identifier: MIT

//go:build race

package kitcheck

// raceOn is true when the tests run with -race, which makes timings
// meaningless.
const raceOn = true
