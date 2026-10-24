// Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
// SPDX-License-Identifier: MIT

package config

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
)

// tree makes each folder under a fresh root and returns the root, with
// $HOME moved outside it so the real home never takes part.
func tree(t *testing.T, dirs ...string) string {
	t.Helper()
	root := t.TempDir()
	t.Setenv("HOME", filepath.Join(t.TempDir(), "elsewhere"))
	t.Setenv("USERPROFILE", filepath.Join(t.TempDir(), "elsewhere"))
	for _, d := range dirs {
		if err := os.MkdirAll(filepath.Join(root, filepath.FromSlash(d)), 0o750); err != nil {
			t.Fatal(err)
		}
	}
	return root
}

func touch(t *testing.T, path string) {
	t.Helper()
	if err := os.WriteFile(path, []byte("actions: []\n"), 0o600); err != nil {
		t.Fatal(err)
	}
}

func TestFindFromANestedFolder(t *testing.T) {
	root := tree(t, "proj/.echo", "proj/wp-content/themes/shop")
	want := filepath.Join(root, "proj", ".echo", "config.yaml")
	touch(t, want)
	for _, start := range []string{"proj", "proj/wp-content", "proj/wp-content/themes/shop"} {
		got, err := Find(filepath.Join(root, filepath.FromSlash(start)))
		if err != nil || got != want {
			t.Errorf("Find(%s) = %q, %v; want %q", start, got, err, want)
		}
	}
}

func TestFindTakesTheNearest(t *testing.T) {
	root := tree(t, "outer/.echo", "outer/inner/.echo", "outer/inner/sub")
	touch(t, filepath.Join(root, "outer", ".echo", "config.yaml"))
	want := filepath.Join(root, "outer", "inner", ".echo", "config.yaml")
	touch(t, want)
	if got, err := Find(filepath.Join(root, "outer", "inner", "sub")); err != nil || got != want {
		t.Errorf("Find = %q, %v; want %q", got, err, want)
	}
}

func TestFindRelativeStart(t *testing.T) {
	root := tree(t, "proj/.echo", "proj/sub")
	want := filepath.Join(root, "proj", ".echo", "config.yaml")
	touch(t, want)
	t.Chdir(filepath.Join(root, "proj", "sub"))
	got, err := Find(".")
	if err != nil {
		t.Fatal(err)
	}
	// The temp dir can sit behind a symlink (/var on macOS), so compare
	// the files rather than the spellings.
	a, _ := os.Stat(got)
	b, _ := os.Stat(want)
	if !filepath.IsAbs(got) || !os.SameFile(a, b) {
		t.Errorf("Find(.) = %q; want %q", got, want)
	}
}

func TestFindNotFound(t *testing.T) {
	root := tree(t, "proj/sub", "proj/.echo") // a .echo folder without config.yaml
	_, err := Find(filepath.Join(root, "proj", "sub"))
	if !errors.Is(err, ErrNoConfig) {
		t.Fatalf("err = %v; want ErrNoConfig", err)
	}
	if err.Error() != "no .echo/config.yaml here or above; run rhythm echo init" {
		t.Errorf("message = %q", err)
	}
}

func TestFindStopsAtTheRepositoryRoot(t *testing.T) {
	for _, gitIsFile := range []bool{false, true} { // a clone, then a worktree
		root := tree(t, ".echo", "repo/sub")
		touch(t, filepath.Join(root, ".echo", "config.yaml"))
		git := filepath.Join(root, "repo", ".git")
		if gitIsFile {
			touch(t, git)
		} else if err := os.Mkdir(git, 0o750); err != nil {
			t.Fatal(err)
		}
		if _, err := Find(filepath.Join(root, "repo", "sub")); !errors.Is(err, ErrNoConfig) {
			t.Errorf(".git file %v: err = %v; the folder above the repository must not be searched", gitIsFile, err)
		}
		// The repository root itself is searched.
		_ = os.MkdirAll(filepath.Join(root, "repo", ".echo"), 0o750)
		want := filepath.Join(root, "repo", ".echo", "config.yaml")
		touch(t, want)
		if got, err := Find(filepath.Join(root, "repo", "sub")); err != nil || got != want {
			t.Errorf(".git file %v: Find = %q, %v; want %q", gitIsFile, got, err, want)
		}
	}
}

func TestFindStopsAtHome(t *testing.T) {
	root := tree(t, "home/.echo", "home/proj/sub")
	home := filepath.Join(root, "home")
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	touch(t, filepath.Join(home, ".echo", "config.yaml"))
	if got, err := Find(filepath.Join(home, "proj", "sub")); !errors.Is(err, ErrNoConfig) {
		t.Errorf("Find = %q, %v; $HOME must not be searched from below it", got, err)
	}
	// Run in $HOME itself, its own .echo is used.
	if got, err := Find(home); err != nil || got != filepath.Join(home, ".echo", "config.yaml") {
		t.Errorf("Find($HOME) = %q, %v", got, err)
	}
}
