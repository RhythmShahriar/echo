#!/bin/sh
# Builds the testbed's Next.js app into testbed/runtime/next, reusing the
# Node connector's own fixture app (connectors/node/fixtures/next-app: its
# package.json, lockfile, next.config.mjs with withEcho, instrumentation.ts)
# with this testbed's routes from next/app. The connector is packed from
# this checkout (npm pack), as it would be published. npm runs in Docker
# with network access; the app itself runs on the sealed network.
set -eu

here=$(cd "$(dirname "$0")" && pwd)
testbed=$(cd "$here/.." && pwd)
connector=$(cd "$testbed/../connectors/node" && pwd)
fixture="$connector/fixtures/next-app"
runtime="$testbed/runtime"
app="$runtime/next"
pkg="$runtime/node-pkg"
mkdir -p "$app" "$pkg" "$runtime/cache/npm"

# The connector, without its node_modules or build output.
(cd "$connector" && tar --exclude=./node_modules --exclude=./dist --exclude=./.build --exclude=./fixtures -cf - .) | (cd "$pkg" && tar -xf -)
# The fixture app's project files, then the testbed's routes.
rm -rf "$app/app"
for f in package.json package-lock.json next.config.mjs instrumentation.ts tsconfig.json; do
	cp "$fixture/$f" "$app/$f"
done
cp -R "$here/app" "$app/app"

docker run --rm -u "$(id -u):$(id -g)" -e HOME=/tmp -e npm_config_cache=/cache -e npm_config_update_notifier=false -e NEXT_TELEMETRY_DISABLED=1 \
	-v "$runtime/cache/npm":/cache -v "$pkg":/pkg -v "$app":/app node:24-bookworm-slim sh -euc '
		cd /pkg
		npm ci --no-audit --no-fund --loglevel=error
		tgz=$(npm pack --silent --pack-destination /tmp | tail -n 1)
		cd /app
		[ -d node_modules/next ] || npm ci --no-audit --no-fund --loglevel=error
		npm install --no-save --no-audit --no-fund --loglevel=error "/tmp/$tgz"
		node_modules/.bin/next build >/tmp/next-build.log 2>&1 || { cat /tmp/next-build.log; exit 1; }
	'
echo "next: built in runtime/next"
