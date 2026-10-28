// Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
// SPDX-License-Identifier: MIT

// Package cli is the rhythm echo consent command: its flags, its
// subcommands (detect, report, verify, cmps, trackers, prune), its
// messages and how it turns a consent result into output and an exit
// code. cmd/rhythm-echo hands it the command line.
package cli

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"strings"
	"time"

	"github.com/RhythmShahriar/echo/report"
	kitcli "github.com/RhythmShahriar/rhythm/kit/cli"
)

// Env is what Main runs with: Echo's version and the streams, plus a
// clock tests can set (nil means time.Now). Open and Update come from
// cmd/rhythm-echo, since no consent package starts programs or talks to
// GitHub itself; nil turns them off.
type Env struct {
	Version string
	Stdin   io.Reader
	Stdout  io.Writer
	Stderr  io.Writer
	Now     func() time.Time
	// Open opens a report in the desktop's viewer (--open).
	Open func(path string) error
	// Update starts the check for a new release and returns what asks
	// about it at the end. Main calls it only for a person at a
	// terminal: never under --json, -q, CI or a pipe.
	Update func(ctx context.Context) (prompt func(ctx context.Context))
}

// now is the time by e's clock.
func (e Env) now() time.Time {
	if e.Now != nil {
		return e.Now()
	}
	return time.Now()
}

// The exit codes, the kit's shared ones (PRD 10.1).
const (
	exitOK          = kitcli.ExitOK
	exitFindings    = kitcli.ExitFindings
	exitUsage       = kitcli.ExitUsage
	exitUnavailable = kitcli.ExitUnavailable
	exitInterrupted = kitcli.ExitInterrupted
)

// Main runs rhythm echo consent with args (the words after "consent")
// and returns the exit code.
func Main(args []string, env Env) int {
	if env.Stdout == nil {
		env.Stdout = io.Discard
	}
	if env.Stderr == nil {
		env.Stderr = io.Discard
	}
	if env.Stdin == nil {
		env.Stdin = os.Stdin
	}
	s := subOf(newFlagSet(&options{}), args)
	o, pos, err := parse(s, args)
	if errors.Is(err, flag.ErrHelp) {
		fmt.Fprint(env.Stdout, usage(s))
		return exitOK
	}
	if err != nil {
		code := fail(env, err)
		if s == subCheck && jsonArgs(args) {
			_ = report.WriteConsentError(env.Stdout, env.Version, code, err.Error())
		}
		return code
	}
	c := &cmd{env: env, o: o, sub: s}
	switch s {
	case subCheck:
		return c.check(pos)
	case subDetect:
		return c.detect(pos)
	case subReport:
		return c.report(pos)
	case subVerify:
		return c.verify(pos)
	case subCmps:
		return c.cmps(pos)
	case subTrackers:
		return c.trackers(pos)
	}
	return c.prune(pos)
}

// cmd is one consent command line being run.
type cmd struct {
	env Env
	o   *options
	sub sub
	// jsonOut says the check writes its result to stdout as JSON, so
	// a check that stops first writes an error envelope there.
	jsonOut bool
}

// fail is the package's fail for the check: under --json it also
// writes the error envelope to stdout.
func (c *cmd) fail(err error) int {
	return c.jsonError(fail(c.env, err), errText(err))
}

// jsonError returns code. When the check's result goes to stdout as
// JSON it first writes there the error envelope of a check that
// stopped with code and msg.
func (c *cmd) jsonError(code int, msg string) int {
	if c.jsonOut {
		_ = report.WriteConsentError(c.env.Stdout, c.env.Version, code, msg)
	}
	return code
}

// jsonArgs reports whether a command line that didn't parse asked for
// JSON on stdout: --json and no -o, which could have taken it.
func jsonArgs(args []string) bool {
	json := false
	for _, a := range args {
		if a == "--" {
			break
		}
		name, _, _ := strings.Cut(strings.TrimLeft(a, "-"), "=")
		if !strings.HasPrefix(a, "-") {
			continue
		}
		switch name {
		case "json":
			json = !strings.HasSuffix(a, "=false")
		case "o", "output":
			return false
		}
	}
	return json
}

// fail prints err as Echo does and returns its exit code: 2 for a
// usage error, 3 otherwise.
func fail(env Env, err error) int {
	fmt.Fprintln(env.Stderr, "rhythm echo:", err)
	var ue kitcli.UsageError
	if errors.As(err, &ue) {
		return exitUsage
	}
	return exitUnavailable
}
