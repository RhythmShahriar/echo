// Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
// SPDX-License-Identifier: MIT

//go:build unix

package sendmail

import (
	"io/fs"
	"os"
	"syscall"
)

// currentUID is the user whose folders are trusted; tests replace it.
var currentUID = os.Getuid

// untrustedInfo says why someone else could have written the file fi
// describes, or "" when only its owner could have and that is you or
// root. Group write is allowed: many systems give each user a group of
// their own and a umask of 002, so every project folder would fail it.
func untrustedInfo(fi fs.FileInfo) string {
	if m := fi.Mode(); m&fs.ModeSticky != 0 || m.Perm()&0o002 != 0 {
		return "is writable by anyone"
	}
	if st, ok := fi.Sys().(*syscall.Stat_t); ok && int(st.Uid) != currentUID() && st.Uid != 0 {
		return "belongs to someone else"
	}
	return ""
}
