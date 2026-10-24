// Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
// SPDX-License-Identifier: MIT

//go:build race

package compare

// raceEnabled: the race detector slows code several times over, so timing
// bounds aren't checked under it.
const raceEnabled = true
