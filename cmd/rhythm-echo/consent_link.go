// Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
// SPDX-License-Identifier: MIT

package main

// The consent stages register themselves when linked in; this file is
// what links them, so a build of rhythm-echo has every stage. norodflag
// comes first: rod reads its own flag from the command line when it
// starts, which a user's argument must never reach.
import (
	_ "github.com/RhythmShahriar/echo/internal/browser/norodflag"

	_ "github.com/RhythmShahriar/echo/consent/browse"
	_ "github.com/RhythmShahriar/echo/consent/cmp"
	_ "github.com/RhythmShahriar/echo/consent/signal"
	_ "github.com/RhythmShahriar/echo/consent/tracker"
	_ "github.com/RhythmShahriar/echo/consent/verdict"
)
