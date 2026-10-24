// Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
// SPDX-License-Identifier: MIT

package sendmail

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"github.com/RhythmShahriar/echo/config"
)

// ErrNoConfig is FindConfig's error when no folder it may search has a
// .echo/config.yaml.
var ErrNoConfig = errors.New("no .echo/config.yaml here or above")

// UntrustedError is FindConfig's error when the nearest config sits in a
// folder above the start that someone else could have put there. A
// config runs commands and is told the token, so it isn't used.
type UntrustedError struct {
	Path   string // the config found
	Reason string // why it isn't used, e.g. "its folder is writable by anyone"
}

func (e *UntrustedError) Error() string {
	return fmt.Sprintf("not using %s: %s", e.Path, e.Reason)
}

// FindConfig returns the absolute path of the nearest .echo/config.yaml
// in start or a folder above it, as git finds .git, so Echo works from
// any subfolder of a project. The search ends:
//
//   - at the repository root (the folder holding .git, which is searched);
//   - below $HOME (which isn't, unless start is $HOME): a stray ~/.echo
//     would otherwise catch every project outside a repository;
//   - at the first folder above start that isn't yours (or root's), or
//     that anyone may write to, such as /tmp. Anyone who can write a
//     shared parent could otherwise plant a config there that runs their
//     commands and is sent your token. Git's safe.directory guards the
//     same way. A config found there is reported as an *UntrustedError.
//
// Start itself is always searched: running Echo there is the choice to
// trust it. Nothing is checked on Windows, which has no Unix owner or
// mode bits to read.
func FindConfig(start string) (string, error) {
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
	for first := true; ; first = false {
		p := filepath.Join(dir, config.Dir, "config.yaml")
		if !first {
			if why := untrusted(dir); why != "" {
				if fi, err := os.Stat(p); err == nil && fi.Mode().IsRegular() {
					return "", &UntrustedError{Path: p, Reason: "its folder " + why}
				}
				return "", ErrNoConfig
			}
		}
		if fi, err := os.Stat(p); err == nil && fi.Mode().IsRegular() {
			if !first {
				// The folder is safe; .echo and the file in it must be too.
				for _, q := range []string{filepath.Dir(p), p} {
					if why := untrusted(q); why != "" {
						return "", &UntrustedError{Path: p, Reason: rel(dir, q) + " " + why}
					}
				}
			}
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

// untrusted says why someone else could have written path ("is writable
// by anyone", "belongs to someone else"), or "" when only you or root
// could have.
func untrusted(path string) string {
	fi, err := os.Stat(path)
	if err != nil {
		return "can't be read"
	}
	return untrustedInfo(fi)
}


// rel names path for a message: relative to dir when it is inside it.
func rel(dir, path string) string {
	if r, err := filepath.Rel(dir, path); err == nil {
		return r
	}
	return path
}
