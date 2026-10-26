// Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
// SPDX-License-Identifier: MIT

package browser

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"
)

// TestPinsMatchSoundcheck keeps Echo's Chrome for Testing pins the same
// as Soundcheck's, so both tools share one download (DownloadDir). It
// reads Soundcheck's cft_pins.go from the workspace checkout next to this
// repository and skips where there is none, as in CI.
func TestPinsMatchSoundcheck(t *testing.T) {
	theirs, err := os.ReadFile(filepath.Join("..", "..", "..", "soundcheck", "browser", "cft_pins.go"))
	if os.IsNotExist(err) {
		t.Skip("no Soundcheck checkout next to this repository; the pins are compared in the workspace")
	}
	if err != nil {
		t.Fatal(err)
	}
	ours, err := os.ReadFile("cft_pins.go")
	if err != nil {
		t.Fatal(err)
	}
	norm := func(b []byte) []byte { return bytes.ReplaceAll(b, []byte("\r\n"), []byte("\n")) }
	if !bytes.Equal(norm(ours), norm(theirs)) {
		t.Fatal("cft_pins.go differs from Soundcheck's; copy soundcheck/browser/cft_pins.go here (both tools must pin the same Chrome for Testing)")
	}
}
