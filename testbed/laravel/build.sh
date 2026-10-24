#!/bin/sh
# Builds the testbed's Laravel app into testbed/runtime/laravel, the way the
# connector's own end-to-end test does (connectors/laravel/fixtures/e2e.sh):
# a stock laravel/laravel, rhythmsh/echo-laravel from a path repository
# (copied, not symlinked, so the app runs without the checkout mounted),
# then the files in overlay/. Composer runs in Docker with network access;
# the app itself runs on the sealed network.
#
# The first build takes a minute or two; later ones only refresh the
# connector and the overlay.
set -eu

here=$(cd "$(dirname "$0")" && pwd)
testbed=$(cd "$here/.." && pwd)
pkg=$(cd "$testbed/../connectors/laravel" && pwd)
app="$testbed/runtime/laravel"
cache="$testbed/runtime/cache/composer"
mkdir -p "$app" "$cache"

run() {
	image=$1
	shift
	docker run --rm -u "$(id -u):$(id -g)" -e COMPOSER_HOME=/cache -e HOME=/tmp \
		-v "$cache":/cache -v "$pkg":/pkg:ro -v "$app":/app -w /app "$image" "$@"
}

if [ ! -f "$app/artisan" ]; then
	run composer:2 composer create-project --no-install --no-scripts --prefer-dist laravel/laravel . >/dev/null
	run composer:2 composer config platform.php 8.3.0
	run composer:2 composer config repositories.echo '{"type": "path", "url": "/pkg", "options": {"symlink": false}}'
	run composer:2 composer require --no-interaction --no-progress 'rhythmsh/echo-laravel:*@dev'
	cp "$app/.env.example" "$app/.env"
	run php:8.3-cli php artisan key:generate --force >/dev/null
	touch "$app/database/database.sqlite"
else
	# The connector may have changed since the last build.
	run composer:2 composer reinstall --no-interaction --no-progress rhythmsh/echo-laravel >/dev/null
fi
cp -R "$here/overlay/." "$app/"
run php:8.3-cli sh -c 'DB_CONNECTION=sqlite DB_DATABASE=/app/database/database.sqlite php artisan migrate --force >/dev/null && php artisan optimize:clear >/dev/null'
echo "laravel: built in runtime/laravel"
