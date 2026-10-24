#!/usr/bin/env bash
# Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
# SPDX-License-Identifier: MIT

# Checks that the binary going into the image knows every flag of the
# Dockerfile's default CMD. An image whose defaults name a flag its Echo
# lacks (up --private-network before it shipped) fails at start for anyone
# running it without arguments, the DDEV add-on and the Lando recipe
# among them.
#
#   check-image-cmd.sh <rhythm-echo binary> [Dockerfile]
#
# It exits 1 and names the missing flags, or 0 when every one is there.
set -euo pipefail

bin=${1:?usage: check-image-cmd.sh <rhythm-echo binary> [Dockerfile]}
dockerfile=${2:-Dockerfile}

cmd=$(grep -E '^CMD \[' "$dockerfile" | tail -n 1) || {
	echo "::error::$dockerfile has no CMD [...] line"
	exit 1
}
help=$("$bin" up --help 2>&1) || true

missing=()
# Each "--name" or "--name=value" in the JSON array; the help lists flags
# as "--name" followed by a space or the end of the line.
while read -r flag; do
	[[ -z $flag ]] && continue
	if ! grep -qE -- "(^|[[:space:]])${flag}([[:space:]]|$)" <<<"$help"; then
		missing+=("$flag")
	fi
done < <(grep -oE '"--[a-z0-9-]+' <<<"$cmd" | tr -d '"')

if ((${#missing[@]})); then
	echo "::error::the image's default CMD uses ${missing[*]}, which this rhythm-echo up doesn't have"
	exit 1
fi
echo "Every flag in the image's default CMD is in this rhythm-echo up."
