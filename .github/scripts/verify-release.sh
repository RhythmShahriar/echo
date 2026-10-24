#!/usr/bin/env bash
# Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
# SPDX-License-Identifier: MIT

# Checks a downloaded Echo release before anything from it runs. First the
# cosign bundle must prove that checksums.txt was signed by Echo's release
# workflow (keyless, through GitHub's OIDC issuer), then the archive's hash
# must match its line in checksums.txt. Comparing hashes alone proves
# nothing: whoever can replace the archive can replace checksums.txt too.
# Anything missing or unexpected fails; there is no "skip" path.
#
#   verify-release.sh <dir> <archive>
#
# <dir> holds <archive>, checksums.txt and checksums.txt.sigstore.json.
# cosign must be on PATH (the Action installs it with sigstore/cosign-installer).
set -euo pipefail

# The signing identity, as .goreleaser.yaml documents it. The release runs
# in the shared reusable workflow, which a caller names by branch, tag or
# commit; the certificate records whichever was used. The repository check
# pins the release to RhythmShahriar/echo itself.
identity='^https://github\.com/RhythmShahriar/\.github/\.github/workflows/release-go\.yml@(refs/heads/master|refs/tags/v[0-9][0-9.]*|[0-9a-f]{40})$'
issuer=https://token.actions.githubusercontent.com
repository=RhythmShahriar/echo

fail() {
	echo "::error::Echo's release didn't verify: $1"
	exit 1
}

[ $# -eq 2 ] || {
	echo "usage: verify-release.sh <dir> <archive>" >&2
	exit 2
}
dir=$1 archive=$2

command -v cosign >/dev/null 2>&1 || fail "cosign isn't installed, so the signature can't be checked"
for f in "$archive" checksums.txt checksums.txt.sigstore.json; do
	[ -s "$dir/$f" ] || fail "$f is missing from the download"
done

cosign verify-blob \
	--bundle "$dir/checksums.txt.sigstore.json" \
	--certificate-identity-regexp "$identity" \
	--certificate-oidc-issuer "$issuer" \
	--certificate-github-workflow-repository "$repository" \
	"$dir/checksums.txt" ||
	fail "checksums.txt isn't signed by Echo's release workflow"

# Exactly one line must name the archive: two would let a forged line win.
lines=$(awk -v a="$archive" '$2 == a || $2 == "*" a' "$dir/checksums.txt")
[ -n "$lines" ] || fail "checksums.txt has no line for $archive"
[ "$(printf '%s\n' "$lines" | wc -l | tr -d ' ')" -eq 1 ] || fail "checksums.txt names $archive more than once"
want=$(printf '%s' "$lines" | awk '{print $1}')

if command -v sha256sum >/dev/null 2>&1; then
	got=$(sha256sum "$dir/$archive" | awk '{print $1}')
else
	got=$(shasum -a 256 "$dir/$archive" | awk '{print $1}') # macOS
fi
[ "$got" = "$want" ] || fail "$archive doesn't match its checksum (want $want, got $got)"
echo "Verified $archive: signed by $repository's release workflow, checksum matches."
