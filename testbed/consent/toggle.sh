#!/bin/sh
# Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
# SPDX-License-Identifier: MIT

# Switches consent testbed scenarios between their good and broken state.
#
#   ./toggle.sh <id|all> good|broken    e.g. ./toggle.sh c01 broken
#   ./toggle.sh status                  lists the broken scenarios
#
# A scenario is broken while state/<id> exists. The testbed server reads
# it on every request, so a switch takes effect at once, with no restart.
# The server's admin port does the same over HTTP:
#   curl '127.0.0.1:23401/_admin/state?id=c01&state=broken'
set -eu

here=$(cd "$(dirname "$0")" && pwd)
state="$here/state"
mkdir -p "$state"

ids() { (cd "$here/sites" && for d in *; do [ -d "$d" ] && case $d in _*) ;; *) echo "$d" ;; esac; done); }
words() { printf '%s\n' "$1" | tr '\n' ' ' | sed 's/ $//'; }

usage() {
	echo "usage: $0 <id|all> good|broken" >&2
	echo "       $0 status" >&2
	exit 2
}

if [ "${1:-}" = "status" ]; then
	found=$(cd "$state" && for f in *; do [ -f "$f" ] && echo "$f"; done)
	if [ -z "$found" ]; then echo "all scenarios good"; else echo "broken: $(words "$found")"; fi
	exit 0
fi
[ $# -eq 2 ] || usage

case "$1" in
all) targets=$(ids) ;;
*) targets=$1 ;;
esac
for id in $targets; do
	ids | grep -qx "$id" || { echo "$0: unknown scenario $id (see sites/)" >&2; exit 2; }
done

for id in $targets; do
	case "$2" in
	good) rm -f "$state/$id" ;;
	broken) date -u +%Y-%m-%dT%H:%M:%SZ > "$state/$id" ;;
	*) usage ;;
	esac
done
echo "$(words "$targets") -> $2"
