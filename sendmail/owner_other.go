// Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
// SPDX-License-Identifier: MIT

//go:build !unix

package sendmail

import "io/fs"

// untrustedInfo trusts every file: Windows has no Unix owner in fi, and
// the mode bits Go reports there say nothing about who may write.
func untrustedInfo(fs.FileInfo) string { return "" }
