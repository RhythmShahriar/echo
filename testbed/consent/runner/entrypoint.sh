#!/bin/sh
# Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
# SPDX-License-Identifier: MIT

# Trusts the testbed CA in the user's NSS store, where Chrome on Linux
# reads it, then runs the command (sleep, for docker compose exec). The
# runner runs as the host user's id, so HOME is made here.
set -eu
mkdir -p "$HOME/.pki/nssdb"
if [ ! -f "$HOME/.pki/nssdb/cert9.db" ]; then
	certutil -d "sql:$HOME/.pki/nssdb" -N --empty-password
fi
certutil -d "sql:$HOME/.pki/nssdb" -D -n "Rhythm Echo consent testbed CA" 2>/dev/null || true
certutil -d "sql:$HOME/.pki/nssdb" -A -t "C,," -n "Rhythm Echo consent testbed CA" -i /testbed-ca.pem
exec "$@"
