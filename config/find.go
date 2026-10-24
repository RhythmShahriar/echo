// Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
// SPDX-License-Identifier: MIT

package config

import (
	"errors"
	"os"
	"path/filepath"
)

// ErrNoConfig is what Find returns when no folder it searched has a
// .echo/config.yaml.
var ErrNoConfig = errors.New("no .echo/config.yaml here or above; run rhythm echo init")

// Find returns the absolute path of the nearest .echo/config.yaml in
// start or a folder above it, as git finds .git, so Echo works from any
// subfolder of a project. The search ends at the repository root (the
// folder holding .git, which is searched) and below $HOME (which isn't,
// unless start is $HOME): a stray ~/.echo would otherwise catch every
// project outside a repository. With nothing found it returns
// ErrNoConfig.
func Find(start string) (string, error) {
	dir, err := filepath.Abs(start)
	if err != nil {
		return "", err
	}
	home := ""
	if h, err := os.UserHomeDir(); err == nil && h != "" {
		if h, err := filepath.Abs(h); err == nil {
			home = h
		}
	}
	for {
		p := filepath.Join(dir, Dir, "config.yaml")
		if fi, err := os.Stat(p); err == nil && fi.Mode().IsRegular() {
			return p, nil
		}
		// .git is a folder in a clone and a file in a worktree or submodule.
		if _, err := os.Lstat(filepath.Join(dir, ".git")); err == nil {
			return "", ErrNoConfig
		}
		parent := filepath.Dir(dir)
		if parent == dir || parent == home {
			return "", ErrNoConfig
		}
		dir = parent
	}
}
