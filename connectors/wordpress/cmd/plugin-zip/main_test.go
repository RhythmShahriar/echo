// Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
// SPDX-License-Identifier: MIT

package main

import (
	"archive/zip"
	"io"
	"path/filepath"
	"testing"
)

func TestWritesTheZip(t *testing.T) {
	out := filepath.Join(t.TempDir(), "seal.zip")
	if code := run([]string{"-o", out}, io.Discard); code != 0 {
		t.Fatalf("exit %d", code)
	}
	z, err := zip.OpenReader(out)
	if err != nil {
		t.Fatal(err)
	}
	defer z.Close()
	if len(z.File) == 0 || z.File[0].Name[:len("echo-staging-seal/")] != "echo-staging-seal/" {
		t.Errorf("zip = %v", z.File)
	}
	if code := run([]string{"-o", filepath.Join(t.TempDir(), "missing", "x.zip")}, io.Discard); code != 1 {
		t.Errorf("unwritable path: exit %d, want 1", code)
	}
}
