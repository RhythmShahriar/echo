// Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
// SPDX-License-Identifier: MIT

// Command conformance runs Echo's strict stand-in on its own, for the
// connectors' end-to-end tests until `rhythm echo conformance` exists:
//
//	GOWORK=off go run ./conformance/cmd/conformance --listen 127.0.0.1:18925 --expect email,http-before
package main

import (
	"os"

	"github.com/RhythmShahriar/echo/conformance"
)

func main() {
	os.Exit(conformance.Main(os.Args[1:], os.Stdin, os.Stdout, os.Stderr))
}
