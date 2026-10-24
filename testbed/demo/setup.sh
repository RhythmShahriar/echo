# Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
# SPDX-License-Identifier: MIT

# shellcheck shell=bash
# Sourced by demo.tape, hidden: the demo's shell. Starts Echo the way
# make echo does, with every scenario good, then names the binary from
# this checkout `rhythm echo`, as the rhythm CLI runs it.
cd "$(dirname "${BASH_SOURCE[0]}")/.." || return 1
export TESTBED=$PWD
export XDG_CACHE_HOME=$PWD/runtime/cache RHYTHM_ECHO_TOKEN=echo-testbed-token RHYTHM_ECHO_URL=http://127.0.0.1:18860
export RHYTHM_NO_UPDATE_CHECK=1
rhythm() {
	[ "$1" = echo ] && shift
	"$TESTBED/runtime/rhythm-echo" "$@"
}
./toggle.sh all good >/dev/null
rm -rf .echo/snapshots/before
runtime/rhythm-echo up --config .echo/config.yaml --token "$RHYTHM_ECHO_TOKEN" --seal --plain >runtime/demo-echo.log 2>&1 &
export DEMO_ECHO=$! # teardown.sh stops it
for _ in $(seq 1 50); do curl -fsS -o /dev/null -H "X-Echo-Token: $RHYTHM_ECHO_TOKEN" "$RHYTHM_ECHO_URL/v1/health" && break; sleep 0.2; done
PS1='\[\e[35m\]staging\[\e[0m\] $ '
