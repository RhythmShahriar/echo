// Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
// SPDX-License-Identifier: MIT

package catcher

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

// lockFile is the file in an inbox folder that the Echo using it holds a
// lock on, with its process ID in it. The lock goes with the process, so
// a crash never leaves the folder locked.
const lockFile = ".echo-inbox.lock"

// ErrInUse matches (errors.Is) what OpenInbox returns when another Echo
// keeps its inbox in the same folder. Two would append to the same
// journal and miss each other's deletes.
var ErrInUse = errors.New("the inbox folder is in use")

// inUseError names the Echo that holds an inbox folder.
type inUseError struct {
	who, dir string
}

func (e *inUseError) Error() string {
	return "open inbox: " + e.who + " keeps its inbox in " + e.dir + "; stop it first, or give this one a folder of its own"
}

func (e *inUseError) Is(target error) bool { return target == ErrInUse }

// errLocked is what tryLock returns when another process holds the lock.
var errLocked = errors.New("locked")

// lockInbox takes the lock on dir's lock file for this process and
// writes its PID there. The returned file holds the lock until it's
// closed.
func lockInbox(dir string) (*os.File, error) {
	path := filepath.Join(dir, lockFile)
	f, err := os.OpenFile(path, os.O_RDWR|os.O_CREATE, 0o600) //nolint:gosec // the inbox's own folder
	if err != nil {
		return nil, fmt.Errorf("open inbox: %w", err)
	}
	if err := tryLock(f); err != nil {
		_ = f.Close()
		if !errors.Is(err, errLocked) {
			return nil, fmt.Errorf("open inbox: lock %s: %w", path, err)
		}
		who := "another Echo"
		if b, rerr := os.ReadFile(path); rerr == nil { //nolint:gosec // the inbox's own folder
			if pid, perr := strconv.Atoi(strings.TrimSpace(string(b))); perr == nil && pid > 0 {
				who = fmt.Sprintf("another Echo (PID %d)", pid)
			}
		}
		return nil, &inUseError{who: who, dir: dir}
	}
	if err := f.Truncate(0); err == nil {
		_, _ = f.WriteAt([]byte(strconv.Itoa(os.Getpid())+"\n"), 0)
	}
	return f, nil
}
