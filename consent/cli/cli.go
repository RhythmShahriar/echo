// Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
// SPDX-License-Identifier: MIT

// Package cli is the rhythm echo consent command: its flags, its
// subcommands (detect, report, verify, cmps, trackers, prune), its
// messages and how it turns a consent result into output and an exit
// code. cmd/rhythm-echo hands it the command line.
package cli

import (
	"fmt"
	"io"
	"time"

	kitcli "github.com/RhythmShahriar/rhythm/kit/cli"
)

// Usage is the help for rhythm echo consent.
var Usage = `rhythm echo consent: check cookie consent: no choice, Accept and Reject in a real browser

Usage:
  rhythm echo consent [<url>...] [flags]

Not built yet in this version.
`

// Env is what Main runs with: Echo's version and the streams, plus a
// clock tests can set (nil means time.Now).
type Env struct {
	Version string
	Stdin   io.Reader
	Stdout  io.Writer
	Stderr  io.Writer
	Now     func() time.Time
}

// Main runs rhythm echo consent with args (the words after "consent")
// and returns the exit code.
func Main(args []string, env Env) int {
	_ = args
	fmt.Fprintln(env.Stderr, "rhythm echo: consent isn't built yet")
	return kitcli.ExitUnavailable
}
