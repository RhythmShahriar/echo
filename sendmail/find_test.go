// Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
// SPDX-License-Identifier: MIT

//go:build unix

package sendmail

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// tree makes each folder under a new temporary root, with a config in
// each folder named in configs, and returns the root.
func tree(t *testing.T, dirs []string, configs ...string) string {
	t.Helper()
	root := t.TempDir()
	t.Setenv("HOME", filepath.Join(root, "elsewhere"))
	for _, d := range dirs {
		if err := os.MkdirAll(filepath.Join(root, d), 0o750); err != nil {
			t.Fatal(err)
		}
	}
	for _, d := range configs {
		if err := os.MkdirAll(filepath.Join(root, d, ".echo"), 0o750); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(root, d, ".echo", "config.yaml"), []byte("smtp: 127.0.0.1:20479\n"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	return root
}

func TestFindConfigFromASubfolder(t *testing.T) {
	root := tree(t, []string{"proj/a/b"}, "proj")
	got, err := FindConfig(filepath.Join(root, "proj", "a", "b"))
	if want := filepath.Join(root, "proj", ".echo", "config.yaml"); err != nil || got != want {
		t.Errorf("FindConfig = %q, %v; want %q", got, err, want)
	}
}

func TestFindConfigStopsAtTheRepoAndHome(t *testing.T) {
	root := tree(t, []string{"repo/.git", "repo/a", "home/loose"}, ".", "home")
	if _, err := FindConfig(filepath.Join(root, "repo", "a")); !errors.Is(err, ErrNoConfig) {
		t.Errorf("found a config above the repository: %v", err)
	}
	t.Setenv("HOME", filepath.Join(root, "home"))
	if _, err := FindConfig(filepath.Join(root, "home", "loose")); !errors.Is(err, ErrNoConfig) {
		t.Errorf("found ~/.echo from a folder below it: %v", err)
	}
	if _, err := FindConfig(filepath.Join(root, "home")); err != nil {
		t.Errorf("FindConfig($HOME): %v", err)
	}
}

// A config planted in a folder anyone may write to, such as /tmp, isn't
// used from below it; from that folder itself it is.
func TestFindConfigSkipsWorldWritableFolders(t *testing.T) {
	root := tree(t, []string{"shared/victim/proj"}, "shared")
	shared := filepath.Join(root, "shared")
	if err := os.Chmod(shared, 0o777); err != nil { //nolint:gosec // the point of the test
		t.Fatal(err)
	}
	_, err := FindConfig(filepath.Join(shared, "victim", "proj"))
	var ue *UntrustedError
	if !errors.As(err, &ue) || ue.Path != filepath.Join(shared, ".echo", "config.yaml") ||
		err.Error() != "not using "+ue.Path+": its folder is writable by anyone" {
		t.Errorf("FindConfig below a world-writable folder = %v", err)
	}
	if _, err := FindConfig(shared); err != nil {
		t.Errorf("FindConfig in the folder itself: %v", err)
	}

	// The folder is safe but .echo in it isn't.
	if err := os.Chmod(shared, 0o750); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(filepath.Join(shared, ".echo"), 0o777); err != nil { //nolint:gosec // the point of the test
		t.Fatal(err)
	}
	if _, err := FindConfig(filepath.Join(shared, "victim")); !errors.As(err, &ue) || !strings.Contains(err.Error(), ": .echo is writable by anyone") {
		t.Errorf("FindConfig with a world-writable .echo = %v", err)
	}
}

// Folders above that belong to another user end the search.
func TestFindConfigSkipsOtherUsersFolders(t *testing.T) {
	root := tree(t, []string{"theirs/mine"}, "theirs")
	start := filepath.Join(root, "theirs", "mine")
	defer func(f func() int) { currentUID = f }(currentUID)
	currentUID = func() int { return os.Getuid() + 1 } // every folder is now someone else's
	var ue *UntrustedError
	if _, err := FindConfig(start); !errors.As(err, &ue) || !strings.HasSuffix(err.Error(), "its folder belongs to someone else") {
		t.Errorf("FindConfig below another user's folder = %v", err)
	}
	// Without a config there the search simply ends.
	if err := os.RemoveAll(filepath.Join(root, "theirs", ".echo")); err != nil {
		t.Fatal(err)
	}
	if _, err := FindConfig(start); !errors.Is(err, ErrNoConfig) {
		t.Errorf("FindConfig = %v; want ErrNoConfig", err)
	}
}

// The shim doesn't send mail to a planted config's address: it says so
// and falls back to the default.
func TestShimIgnoresAPlantedConfig(t *testing.T) {
	root := tree(t, []string{"shared/proj"}, "shared")
	if err := os.Chmod(filepath.Join(root, "shared"), 0o777); err != nil { //nolint:gosec // the point of the test
		t.Fatal(err)
	}
	t.Chdir(filepath.Join(root, "shared", "proj"))
	var stderr bytes.Buffer
	tg := findTarget(func(string) string { return "" }, &stderr)
	if tg.smtp != DefaultSMTP || !strings.Contains(stderr.String(), "rhythm-echo sendmail: not using ") ||
		!strings.Contains(stderr.String(), "writable by anyone") {
		t.Errorf("target = %+v, stderr %q", tg, stderr.String())
	}
}
