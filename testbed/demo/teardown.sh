# Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
# SPDX-License-Identifier: MIT

# shellcheck shell=bash
# Sourced by demo.tape, hidden: stops the demo's Echo and makes every
# scenario good again.
kill "$DEMO_ECHO" 2>/dev/null
./toggle.sh all good >/dev/null
