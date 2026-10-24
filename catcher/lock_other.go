// Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
// SPDX-License-Identifier: MIT

//go:build !(darwin || linux || freebsd || openbsd || netbsd || dragonfly || windows)

package catcher

import "os"

// tryLock does nothing where Echo has no file lock to take: two Echos on
// one folder aren't caught there.
func tryLock(*os.File) error { return nil }
