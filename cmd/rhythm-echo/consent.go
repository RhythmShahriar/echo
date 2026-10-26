// Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
// SPDX-License-Identifier: MIT

package main

import (
	"io"

	consentcli "github.com/RhythmShahriar/echo/consent/cli"
)

// cmdConsent runs rhythm echo consent, which parses its own flags.
func cmdConsent(args []string, stdin io.Reader, stdout, stderr io.Writer) int {
	return consentcli.Main(args, consentcli.Env{Version: version, Stdin: stdin, Stdout: stdout, Stderr: stderr})
}
