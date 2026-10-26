// Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
// SPDX-License-Identifier: MIT

// Package judge decides whether a run of rhythm-echo consent on a
// testbed scenario caught it: it reads the consent envelope, matches it
// against the scenario's expect and forbid lists (scenarios.yaml),
// checks the tracker sink stayed empty and that every stand-in tag the
// page names was loaded. The benchmark (testbed/consent/bench) and the
// browser integration tests (testbed/consent, -tags browser) both judge
// with it, so they can never disagree on what a catch is.
package judge
