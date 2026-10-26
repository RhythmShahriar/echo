// Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
// SPDX-License-Identifier: MIT

package main

import (
	"context"
	"io"
	"os"

	consentcli "github.com/RhythmShahriar/echo/consent/cli"
	"github.com/RhythmShahriar/rhythm/kit/update"
)

// cmdConsent runs rhythm echo consent, which parses its own flags. It
// hands the command what no consent package does itself: opening the
// report (--open) and the update check, through the same kit prompt as
// Echo's other commands.
func cmdConsent(args []string, stdin io.Reader, stdout, stderr io.Writer) int {
	return consentcli.Main(args, consentcli.Env{Version: version, Stdin: stdin, Stdout: stdout, Stderr: stderr,
		Open: openFile, Update: consentUpdate(stdout, stderr)})
}

// consentUpdate starts the check for a new release, as session.start
// does; what it returns offers it once the output is written.
func consentUpdate(stdout, stderr io.Writer) func(context.Context) func(context.Context) {
	return func(ctx context.Context) func(context.Context) {
		pending := update.StartCheck(ctx, &update.Updater{Repo: "RhythmShahriar/echo", Binary: "rhythm-echo", Current: version,
			UserAgent: "rhythm-echo/" + version + " (+https://rythm.sh/echo)", API: os.Getenv("RHYTHM_GITHUB_API")})
		return func(ctx context.Context) {
			if ctx.Err() == nil {
				update.PromptAtExit(ctx, update.PromptOptions{Check: pending, Stdout: stdout, Out: stderr})
			}
		}
	}
}
