#!/bin/sh
# Publishes connectors/laravel to its own repository, which Packagist reads
# as rhythmsh/echo-laravel: the subtree's history goes to master, and the
# given version tag (e.g. v1.0.0) is created there too.
# Usage: scripts/split-laravel.sh v1.0.0
set -eu
tag=${1:?usage: scripts/split-laravel.sh <tag>}
remote=${ECHO_LARAVEL_REMOTE:-git@github.com:RhythmShahriar/echo-laravel.git}
cd "$(git rev-parse --show-toplevel)"
branch=$(git subtree split --prefix connectors/laravel)
git push "$remote" "$branch:refs/heads/master"
git push "$remote" "$branch:refs/tags/$tag"
echo "Pushed connectors/laravel at $tag to $remote"
