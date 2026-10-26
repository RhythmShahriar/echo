// Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
// SPDX-License-Identifier: MIT

//go:build testbed

// Command bench runs rhythm-echo consent against every consent testbed
// scenario and writes docs/benchmark-consent.md. Until it is built it
// says so and exits 3, so make bench-consent fails loudly.
package main

import (
	"fmt"
	"os"
)

func main() {
	fmt.Fprintln(os.Stderr, "consent bench isn't built yet")
	os.Exit(3)
}
