// Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
// SPDX-License-Identifier: MIT

package consenttestbed

import (
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

// toggleBed copies toggle.sh next to a sites folder with the given
// scenario ids, so the test never touches the real state/ folder.
func toggleBed(t *testing.T, ids ...string) string {
	t.Helper()
	if _, err := exec.LookPath("sh"); err != nil {
		t.Skip("no sh")
	}
	dir := t.TempDir()
	script, err := os.ReadFile("toggle.sh")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "toggle.sh"), script, 0o755); err != nil { //nolint:gosec // a script the test runs
		t.Fatal(err)
	}
	for _, id := range ids {
		if err := os.MkdirAll(filepath.Join(dir, "sites", id), 0o750); err != nil {
			t.Fatal(err)
		}
	}
	return dir
}

// runToggle runs the copied toggle.sh and returns its exit code.
func runToggle(t *testing.T, dir string, args ...string) int {
	t.Helper()
	cmd := exec.Command("sh", append([]string{filepath.Join(dir, "toggle.sh")}, args...)...) //nolint:gosec // the test's own copy
	out, err := cmd.CombinedOutput()
	var exit *exec.ExitError
	if errors.As(err, &exit) {
		return exit.ExitCode()
	}
	if err != nil {
		t.Fatalf("toggle.sh: %v\n%s", err, out)
	}
	return 0
}

// TestToggleKnownID: a real scenario id switches to broken and back.
func TestToggleKnownID(t *testing.T) {
	dir := toggleBed(t, "c01", "c02")
	if code := runToggle(t, dir, "c01", "broken"); code != 0 {
		t.Fatalf("c01 broken: exit %d", code)
	}
	if _, err := os.Stat(filepath.Join(dir, "state", "c01")); err != nil {
		t.Errorf("no state file: %v", err)
	}
	if code := runToggle(t, dir, "c01", "good"); code != 0 {
		t.Fatalf("c01 good: exit %d", code)
	}
	if _, err := os.Stat(filepath.Join(dir, "state", "c01")); !os.IsNotExist(err) {
		t.Errorf("state file left after good: %v", err)
	}
}

// TestToggleRejectsPatterns: the id is matched as a literal name, so a
// regex such as ".*" or "c0." is an unknown scenario and writes nothing.
func TestToggleRejectsPatterns(t *testing.T) {
	dir := toggleBed(t, "c01", "c02")
	for _, id := range []string{".*", "c0.", "c0[12]", "-v", "nope"} {
		if code := runToggle(t, dir, id, "broken"); code != 2 {
			t.Errorf("%q: exit %d, want 2", id, code)
		}
	}
	entries, err := os.ReadDir(filepath.Join(dir, "state"))
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range entries {
		t.Errorf("junk state file %q", e.Name())
	}
}
