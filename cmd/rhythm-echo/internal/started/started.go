// Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
// SPDX-License-Identifier: MIT

// Package started records when the process started, before the packages
// that can hold it up do.
//
// Bubble Tea, which the command's pickers use, asks the terminal for its
// background color when it initializes and waits up to five seconds for
// an answer that a terminal recorder or some multiplexers never send. Go
// initializes packages in the order of their import paths when nothing
// else decides it, and this one depends on nothing that does, so it
// initializes first: At is the time the user ran the command, not the
// time that wait ended.
package started

import "time"

// At is when the process started.
var At = time.Now()
