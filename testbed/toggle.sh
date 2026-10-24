#!/bin/sh
# Switches testbed scenarios between their good and broken state.
#
#   ./toggle.sh <id|all> good|broken    e.g. ./toggle.sh s05 broken, ./toggle.sh 5 good
#   ./toggle.sh status                  lists the broken scenarios
#
# A scenario is broken while state/<id> exists. Every container mounts
# state/ read-only and the fixtures check it on every request, job and
# command (bin/cron checks it too, and the benchmark's fixture DNS server on
# every query), so a switch takes effect immediately, with no rebuild or
# restart.
set -eu

here=$(cd "$(dirname "$0")" && pwd)
state="$here/state"
mkdir -p "$state"

ids() { sed -n 's/^  - id: *\(s[0-9][0-9]\).*/\1/p' "$here/scenarios.yaml"; }
words() { printf '%s\n' "$1" | tr '\n' ' ' | sed 's/ $//'; }

usage() {
	echo "usage: $0 <id|all> good|broken" >&2
	echo "       $0 status" >&2
	exit 2
}

if [ "${1:-}" = "status" ]; then
	found=$(cd "$state" && ls s[0-9][0-9] 2>/dev/null || true)
	if [ -z "$found" ]; then echo "all scenarios good"; else echo "broken: $(words "$found")"; fi
	exit 0
fi
[ $# -eq 2 ] || usage

case "$1" in
all) targets=$(ids) ;;
s[0-9][0-9]) targets=$1 ;;
[0-9]) targets=s0$1 ;;
[0-9][0-9] | 0[0-9]) targets=s$1 ;;
*) usage ;;
esac
for id in $targets; do
	ids | grep -qx "$id" || { echo "$0: unknown scenario $id (see scenarios.yaml)" >&2; exit 2; }
done

for id in $targets; do
	case "$2" in
	good) rm -f "$state/$id" ;;
	broken) date -u +%Y-%m-%dT%H:%M:%SZ > "$state/$id" ;;
	*) usage ;;
	esac
done
echo "$(words "$targets") → $2"
