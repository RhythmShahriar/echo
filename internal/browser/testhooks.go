// Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
// SPDX-License-Identifier: MIT

package browser

// SwapGeteuid replaces the user id check Launch makes, so tests of the
// packages that launch through this one can act as root. It returns a
// func that restores the real one. Tests calling it must not run in
// parallel.
func SwapGeteuid(f func() int) (restore func()) {
	old := geteuid
	geteuid = f
	return func() { geteuid = old }
}
