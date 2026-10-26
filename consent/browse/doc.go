// Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
// SPDX-License-Identifier: MIT

// Package browse runs consent checks in Chrome: one fresh context per
// run, the egress guard, the recorder that turns the browser's events
// into consent events and the steps of a run (PRD 13.6). It is the only
// consent package that imports the browser; it registers itself as the
// consent Runner.
package browse
