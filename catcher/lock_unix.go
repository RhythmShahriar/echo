// Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
// SPDX-License-Identifier: MIT

//go:build darwin || linux || freebsd || openbsd || netbsd || dragonfly

package catcher

import (
	"errors"
	"os"
	"syscall"
)

// tryLock takes an exclusive flock on f without waiting.
func tryLock(f *os.File) error {
	err := syscall.Flock(int(f.Fd()), syscall.LOCK_EX|syscall.LOCK_NB) //nolint:gosec // a file descriptor fits an int
	if errors.Is(err, syscall.EWOULDBLOCK) {
		return errLocked
	}
	return err
}
