// Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
// SPDX-License-Identifier: MIT

package main

import (
	"bytes"
	"strings"
	"testing"
)

func TestConformanceIsACommand(t *testing.T) {
	if code, out, _ := runCLI(t, "help", "conformance"); code != exitOK || !strings.Contains(out, "Usage: rhythm echo conformance") {
		t.Errorf("help conformance: %d\n%s", code, out)
	}
	if code, _, _ := runCLI(t, "conformance", "--wire", "x"); code != exitUsage {
		t.Errorf("conformance with a bad flag: %d", code)
	}
	var b bytes.Buffer
	usage(&b)
	if !strings.Contains(b.String(), "rhythm echo conformance ") {
		t.Error("the main help lacks conformance")
	}
}
