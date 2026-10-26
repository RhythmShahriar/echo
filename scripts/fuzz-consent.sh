#!/bin/sh
# Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
# SPDX-License-Identifier: MIT

# Runs every fuzz target of the consent packages and of report, each for
# the given time (default 30s). go test fuzzes one target at a time, so
# each one is listed and run on its own. Stops at the first crasher; the
# failing input is in the package's testdata/fuzz folder.
#
#   scripts/fuzz-consent.sh [fuzztime]   (from the repository root)
set -eu

time=${1:-30s}

for pkg in $(go list ./consent/... ./report); do
	for name in $(go test -list '^Fuzz' "$pkg" | grep '^Fuzz' || true); do
		echo "fuzz $pkg $name ($time)"
		go test -run '^$' -fuzz "^${name}\$" -fuzztime "$time" "$pkg"
	done
done
