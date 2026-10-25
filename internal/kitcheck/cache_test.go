// Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
// SPDX-License-Identifier: MIT

package kitcheck

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/RhythmShahriar/rhythm/kit/cache"
)

// TestWriteFileLeavesNoTemp: run.json and comparison.json are written
// with cache.WriteJSON. When the rename fails (here the target is a
// folder that isn't empty), nothing may be left beside it.
func TestWriteFileLeavesNoTemp(t *testing.T) {
	dir := t.TempDir()
	target := filepath.Join(dir, "run.json")
	if err := os.MkdirAll(filepath.Join(target, "busy"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := cache.WriteFile(target, []byte("{}")); err == nil {
		t.Fatal("WriteFile over a folder succeeded")
	}
	if err := cache.WriteJSON(target, map[string]int{"a": 1}); err == nil {
		t.Fatal("WriteJSON over a folder succeeded")
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range entries {
		if e.Name() != "run.json" {
			t.Errorf("left behind: %s", e.Name())
		}
	}
}

// TestWriteFileReplaces is the ordinary case: the file holds the new data
// and its folder is private.
func TestWriteFileReplaces(t *testing.T) {
	path := filepath.Join(t.TempDir(), "runs", "r1", "run.json")
	for _, data := range []string{"old", "new"} {
		if err := cache.WriteFile(path, []byte(data)); err != nil {
			t.Fatal(err)
		}
	}
	if b, err := os.ReadFile(path); err != nil || string(b) != "new" { //nolint:gosec // the test's own file
		t.Errorf("read %q, %v", b, err)
	}
	if fi, err := os.Stat(filepath.Dir(path)); err != nil || runtime.GOOS != "windows" && fi.Mode().Perm()&0o077 != 0 {
		t.Errorf("folder mode %v, %v; want no group or other access", fi.Mode(), err)
	}
}
