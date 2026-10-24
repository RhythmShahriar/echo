#!/usr/bin/env bash
# Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
# SPDX-License-Identifier: MIT

# Lists the repository's shell scripts for shellcheck: every tracked .sh
# and .bats file and every file whose first line runs sh, bash or bats.
#
#   shell-files.sh   (from the repository root)
set -euo pipefail

git ls-files -z | while IFS= read -r -d '' f; do
	case $f in
	*/node_modules/* | */vendor/*) continue ;;
	*.sh | *.bats) printf '%s\n' "$f" ;;
	*)
		[ -f "$f" ] || continue
		IFS= read -r first <"$f" || true
		case $first in
		'#!'*/sh | '#!'*/bash | '#!'*'env sh' | '#!'*'env bash' | '#!'*'env bats') printf '%s\n' "$f" ;;
		esac
		;;
	esac
done
