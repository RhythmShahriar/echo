#!/usr/bin/env bash
# Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
# SPDX-License-Identifier: MIT

# Posts the Action's pull request comment, or updates the one an earlier
# run posted, so a pull request has one Echo comment however often it runs.
# A hidden marker with $KEY (the working-directory) finds it again, so two
# sites in one repository get one comment each. Only a comment the token's
# own account wrote counts. Anyone may post the marker; a comment the
# Action can't edit would otherwise stop it from posting at all.
#
#   comment.sh <body.md>
#
# Needs gh, jq and in the environment GH_TOKEN (a token that may write
# pull requests), GITHUB_REPOSITORY, PR (the pull request number) and KEY.
# AUTHOR, when set, is the token's login. Otherwise gh api user says; the
# workflow token can't ask, so it is github-actions[bot].
# Posting is best effort: when the token can't write (a fork's pull
# request, or no pull-requests: write), it warns and exits 0. It prints
# id=<comment id> to $GITHUB_OUTPUT when it posted.
set -euo pipefail

[ $# -eq 1 ] || {
	echo "usage: comment.sh <body.md>" >&2
	exit 2
}
marker="<!-- rhythm-echo comment: ${KEY:-.} -->"
body="$marker
$(cat "$1")"

warn() {
	echo "::warning::Couldn't $1 the pull request comment. The job needs \"permissions: pull-requests: write\"; a pull request from a fork gets a read-only token."
	exit 0
}

author=${AUTHOR:-}
if [ -z "$author" ]; then
	author=$(gh api user --jq .login 2>/dev/null) || author=""
	[ -n "$author" ] || author="github-actions[bot]"
fi

api="repos/$GITHUB_REPOSITORY/issues"
existing=$(gh api --paginate "$api/$PR/comments") || warn "read"
id=$(printf '%s' "$existing" | jq -r --arg m "$marker" --arg a "$author" \
	'.[] | select(.user.login == $a and (.body | startswith($m))) | .id' | head -n 1)
if [ -n "$id" ]; then
	gh api -X PATCH "$api/comments/$id" -f body="$body" >/dev/null || warn "update"
	echo "Updated the pull request comment $id."
else
	id=$(gh api -X POST "$api/$PR/comments" -f body="$body" --jq .id) || warn "post"
	echo "Posted the pull request comment $id."
fi
[ -z "${GITHUB_OUTPUT:-}" ] || echo "id=$id" >> "$GITHUB_OUTPUT"
