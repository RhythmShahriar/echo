#!/usr/bin/env bash
# Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
# SPDX-License-Identifier: MIT

# End-to-end test of the Laravel connector against Echo's conformance
# stand-in: Echo's real catcher behind a check of every capture against the
# wire schema.
#
# It runs the stand-in on the host (go build ./conformance/cmd/conformance),
# creates a stock Laravel app in fixtures/app the first time (composer
# create-project in Docker) with this package from a path repository,
# serves it in a php:8.3-cli container on the host network, hits one route
# with an X-Echo-Run header, flushes the queue the way Echo does, and checks
# what arrived.
#
#   connectors/laravel/fixtures/e2e.sh          # or: make -C connectors/laravel e2e
#   E2E_FRESH=1 connectors/laravel/fixtures/e2e.sh   # recreate the app first
#
# Needs docker, go, curl and jq. Uses ports 18960-18965 on 127.0.0.1.
set -euo pipefail

here=$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)
pkg=$(cd "$here/.." && pwd)
root=$(cd "$pkg/../.." && pwd) # the Echo Go module
app="$here/app"
api_port=${E2E_API_PORT:-18960}
smtp_port=${E2E_SMTP_PORT:-18961}
app_port=${E2E_APP_PORT:-18962}
obs_api_port=${E2E_OBSERVE_API_PORT:-18963}
obs_smtp_port=${E2E_OBSERVE_SMTP_PORT:-18964}
closed_port=${E2E_CLOSED_PORT:-18965} # nothing listens here: a dead SMTP server
api="http://127.0.0.1:$api_port"
obs_api="http://127.0.0.1:$obs_api_port"
site="http://127.0.0.1:$app_port"
name="rhythm-echo-laravel-e2e-$$"
cache=${E2E_CACHE:-$here/.cache}
php_image=php:8.3-cli
export PATH="/usr/local/go/bin:$PATH"

pids=()
cleanup() {
	docker rm -f "$name" >/dev/null 2>&1 || true
	for pid in "${pids[@]}"; do
		kill "$pid" 2>/dev/null || true
		wait "$pid" 2>/dev/null || true
	done
}
trap cleanup EXIT

fail() {
	echo "FAIL: $*" >&2
	exit 1
}

# in_app runs a command in Docker inside fixtures/app, as the current user,
# on the host network so the app reaches the catcher on 127.0.0.1.
in_app() {
	local image=$1
	shift
	docker run --rm -u "$(id -u):$(id -g)" --network host \
		-e COMPOSER_HOME=/cache -e HOME=/tmp \
		-e APP_ENV=staging -e APP_URL="$site" \
		-e MAIL_MAILER=smtp -e MAIL_HOST=127.0.0.1 -e MAIL_PORT="${E2E_MAIL_PORT:-$smtp_port}" -e MAIL_SCHEME= \
		-e QUEUE_CONNECTION=database \
		-e RHYTHM_ECHO_URL="${E2E_ECHO_URL:-$api}" \
		-e RHYTHM_ECHO_RUN="${RHYTHM_ECHO_RUN:-}" -e RHYTHM_ECHO_ENABLED="${RHYTHM_ECHO_ENABLED:-}" \
		-v "$cache":/cache -v "$pkg":/pkg -w /pkg/fixtures/app "$image" "$@"
}

composer() { in_app composer:2 composer "$@"; }
artisan() { in_app "$php_image" php artisan "$@"; }

# messages prints the catcher's messages for a run as JSON.
messages() { curl -fsS "$api/v1/messages?run=$1"; }

# start_catcher <api port> <smtp port> <mode> <scenarios> starts a stand-in
# Echo that expects those scenarios, then waits.
start_catcher() {
	local url="http://127.0.0.1:$1"
	if curl -fsS "$url/v1/health" >/dev/null 2>&1; then
		fail "something already listens on $url; stop it or pick other E2E_*_PORT values"
	fi
	"$cache/conformance" --listen "127.0.0.1:$1" --smtp "127.0.0.1:$2" --mode "$3" --expect "$4" &
	pids+=("$!")
	for _ in $(seq 50); do
		curl -fsS "$url/v1/health" >/dev/null 2>&1 && return
		sleep 0.1
	done
	fail "the catcher on $url did not start"
}

# send_mail <subject> sends one mail through the app's default mailer.
send_mail() {
	artisan tinker --execute \
		"Illuminate\\Support\\Facades\\Mail::raw('hello', fn (\$m) => \$m->to('ada@example.test')->subject('$1'));"
}

check() { # check <description> <jq filter that must be true> <json>
	if [ "$(jq -r "$2" <<<"$3" 2>/dev/null)" != "true" ]; then
		jq . <<<"$3" >&2 2>/dev/null || echo "$3" >&2
		fail "$1"
	fi
	echo "ok: $1"
}

mkdir -p "$cache"

# 1. The stand-in Echo, sealed and observing.
(cd "$root" && GOWORK=off go build -o "$cache/conformance" ./conformance/cmd/conformance)
start_catcher "$api_port" "$smtp_port" seal email,http-before,run,wire-2
start_catcher "$obs_api_port" "$obs_smtp_port" observe email,email-after

# 2. The app, created once.
if [ -n "${E2E_FRESH:-}" ]; then
	rm -rf "$app"
fi
if [ ! -f "$app/artisan" ]; then
	rm -rf "$app"
	mkdir -p "$app"
	composer create-project --no-install --no-scripts --prefer-dist laravel/laravel . >/dev/null
	composer config platform.php 8.3.0
	composer config repositories.echo '{"type": "path", "url": "../..", "options": {"symlink": true}}'
	composer require --no-interaction --no-progress 'rhythmsh/echo-laravel:*@dev'
	cp "$app/.env.example" "$app/.env"
	artisan key:generate --force
	touch "$app/database/database.sqlite"
fi
cp -R "$here/overlay/." "$app/"
artisan migrate --force >/dev/null
artisan optimize:clear >/dev/null
artisan package:discover >/dev/null
grep -q 'Rhythm\\\\Echo\\\\EchoServiceProvider' "$app/bootstrap/cache/packages.php" ||
	fail "the service provider was not auto-discovered"
echo "ok: the service provider is auto-discovered"

# 3. Serve it.
docker run -d --name "$name" -u "$(id -u):$(id -g)" --network host \
	-e APP_ENV=staging -e APP_URL="$site" \
	-e MAIL_MAILER=smtp -e MAIL_HOST=127.0.0.1 -e MAIL_PORT="$smtp_port" -e MAIL_SCHEME= \
	-e QUEUE_CONNECTION=database -e RHYTHM_ECHO_URL="$api" \
	-v "$pkg":/pkg -w /pkg/fixtures/app/public "$php_image" \
	php -S "127.0.0.1:$app_port" ../vendor/laravel/framework/src/Illuminate/Foundation/resources/server.php >/dev/null
for _ in $(seq 50); do
	curl -fsS "$site/" >/dev/null 2>&1 && break
	sleep 0.1
done

artisan echo:status
echo "ok: echo:status"

# 4. One action, tagged the way Soundcheck and `echo run` tag it.
out=$(curl -fsS -H "X-Echo-Run: e2e1/checkout" "$site/echo-e2e") || {
	docker logs "$name" >&2
	fail "the route failed"
}
check "the Twilio call got Echo's fake 201" '.twilio == 201 and (.twilio_body.sid | startswith("SM"))' "$out"
before=$(messages e2e1)
check "the queued notification waits for the worker" '[.[] | select(.channel == "email")] | length == 1' "$before"

# 5. Flush the queue as Echo does; the worker has no request, only the
#    Context Laravel stored in the job.
artisan queue:work --stop-when-empty >/dev/null

got=$(messages e2e1)
check "two emails from the connector" '[.[] | select(.channel == "email" and .source.via == "laravel")] | length == 2' "$got"
check "the Mailable's subject" 'any(.[]; .subject == "Your order #1001 has shipped")' "$got"
check "the queued notification's subject" 'any(.[]; .subject == "We received order #1001")' "$got"
check "the mailer name is the plugin" 'all(.[] | select(.channel == "email"); .source.plugin == "smtp")' "$got"
check "the Twilio SMS was recognised and faked" \
	'any(.[]; .channel == "sms" and .provider == "twilio" and .status == "faked" and .source.via == "laravel")' "$got"
check "the SMS recipient" 'any(.[] | select(.channel == "sms"); .to == ["+15555550100"])' "$got"
check "every capture carries the action" 'length == 3 and all(.[]; .run == "e2e1" and .action == "checkout")' "$got"
check "the Twilio auth header was masked" \
	'all(.[] | select(.channel == "sms"); (.headers.authorization // "") | contains("fake-auth-token") | not)' "$got"
all=$(curl -fsS "$api/v1/messages")
check "nothing reached the SMTP sink (the connector sealed it)" 'all(.[]; .source.via != "smtp")' "$all"

# 6. The doctor canary, run like Echo's action runner runs commands.
RHYTHM_ECHO_RUN=e2e2/doctor artisan echo:canary
canary=$(messages e2e2)
check "echo:canary sent one mail and one HTTP call" \
	'length == 2 and any(.[]; .to == ["canary@echo.invalid"]) and any(.[]; .url == "https://canary.echo.invalid/")' "$canary"
check "the canary carries RHYTHM_ECHO_RUN" 'all(.[]; .action == "doctor")' "$canary"

# 7. Control: with the connector off, the same mailer reaches the SMTP sink,
#    so the "nothing reached the SMTP sink" check above means something.
RHYTHM_ECHO_ENABLED=false send_mail "Echo off" >/dev/null
all=$(curl -fsS "$api/v1/messages")
check "control: with the connector off, mail goes out over SMTP" \
	'any(.[]; .source.via == "smtp" and .subject == "Echo off")' "$all"

# 8. Observe mode, against a second catcher: a real send is reported after.
#    Its SMTP sink stands in for the real mail server.
E2E_ECHO_URL=$obs_api E2E_MAIL_PORT=$obs_smtp_port send_mail "Observed ok" >/dev/null
obs=$(curl -fsS "$obs_api/v1/messages")
check "observe: the delivered mail is marked sent" \
	'any(.[]; .source.via == "laravel" and .subject == "Observed ok" and .status == "sent" and (.error // "") == "")' "$obs"
check "observe: and it really went out" 'any(.[]; .source.via == "smtp" and .subject == "Observed ok")' "$obs"

# A dead SMTP server: the send fails and Echo records why.
if E2E_ECHO_URL=$obs_api E2E_MAIL_PORT=$closed_port send_mail "Observed fail" >/dev/null 2>&1; then
	echo "note: tinker swallowed the transport error" >&2
fi
obs=$(curl -fsS "$obs_api/v1/messages")
check "observe: the failed mail is marked failed, with the transport's error" \
	'any(.[]; .subject == "Observed fail" and .status == "failed" and (.error | contains("'"$closed_port"'")))' "$obs"
check "observe: one message per mail, not a duplicate" '[.[] | select(.subject == "Observed fail")] | length == 1' "$obs"

# 9. Every capture followed the wire protocol.
for u in "$api" "$obs_api"; do
	report=$(curl -fsS "$u/v1/conformance")
	check "conformance at $u" '.ok' "$report"
	jq -r '.rows[] | "  \(.check)  \(.result | ascii_upcase)  \(.detail)"' <<<"$report"
done

echo "PASS: Laravel connector end to end ($(jq length <<<"$all") sealed, $(jq length <<<"$obs") observed messages)"
