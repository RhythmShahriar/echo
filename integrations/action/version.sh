#!/usr/bin/env bash
# Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
# SPDX-License-Identifier: MIT

# Prints the Echo version the Action installs. The binary follows the
# Action, so a new release can't change a pinned build:
#
#   VERSION set                      that version
#   used by a tag (@v1.2.3)          1.2.3's tag, v1.2.3
#   used by a commit SHA (@4f1c…)    the release tag on that commit, when
#                                    the GitHub API (gh, GH_TOKEN) names one
#   anything else (@main, @v1)       latest, with a warning
#
#   VERSION=… ACTION_REF=… version.sh
set -euo pipefail

if [ -n "${VERSION:-}" ]; then
	echo "$VERSION"
	exit 0
fi
ref=${ACTION_REF:-}
release='^v[0-9]+\.[0-9]+\.[0-9]+$'
if [[ $ref =~ $release ]]; then
	echo "$ref"
	exit 0
fi
if [[ $ref =~ ^[0-9a-f]{40}$ ]] && command -v gh >/dev/null && command -v jq >/dev/null; then
	tag=$(gh api --paginate "repos/${REPOSITORY:-RhythmShahriar/echo}/tags?per_page=100" 2>/dev/null |
		jq -r --arg sha "$ref" '.[] | select(.commit.sha == $sha) | .name' 2>/dev/null |
		grep -E "$release" | head -n 1) || tag=""
	if [ -n "$tag" ]; then
		echo "$tag"
		exit 0
	fi
	echo "::warning::Installing the latest Echo release because no release tag points at $ref. Set version so a new release can't change your build." >&2
	echo latest
	exit 0
fi
echo "::warning::Installing the latest Echo release because this Action isn't used by a release tag or its commit. Use RhythmShahriar/echo@vX.Y.Z (or its commit SHA) or set version so a new release can't change your build." >&2
echo latest
