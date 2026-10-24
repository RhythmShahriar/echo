# rhythmsh/echo-laravel

The Laravel connector for [Rhythm Echo](https://github.com/RhythmShahriar/echo). Echo captures everything a staging site **sends**, such as email, SMS, webhooks, Slack alerts and analytics calls, and checks it.

- **Seal** (the default): nothing leaves the site. Echo answers in place of the real service, with realistic fake responses shaped like the provider's.
- **Observe**: everything is recorded, then sent for real.

Laravel 11, 12 and 13 on PHP 8.2+.

```sh
composer require rhythmsh/echo-laravel        # not --dev: staging deploys skip dev packages, and it is inert in production
rhythm-echo up                               # Echo on 127.0.0.1:8025
php artisan echo:status
```

The service provider is auto-discovered. With nothing configured, the connector runs in every environment except `production` and `testing`.

## Configuration

Everything can be set from the environment. If you want the file, run `php artisan vendor:publish --tag=rhythm-echo` to publish `config/rhythm-echo.php`.

| Key | Env | Default | |
|---|---|---|---|
| `enabled` | `RHYTHM_ECHO_ENABLED` | unset | Unset means on, except in `production` and `testing`. Set it to `true` to capture in your test suite. |
| `url` | `RHYTHM_ECHO_URL` | `http://127.0.0.1:8025` | Where Echo listens. |
| `token` | `RHYTHM_ECHO_TOKEN` | none | Sent as `X-Echo-Token` when Echo listens beyond loopback. |
| `force` | `RHYTHM_ECHO_FORCE` | `false` | Run even when `APP_ENV=production`. Only for a production-like box that isn't production. |
| `mode` | `RHYTHM_ECHO_MODE` | `seal` | What to assume when Echo can't be reached. `seal` fails closed and `observe` lets traffic through. When Echo answers, its own mode decides. |
| `timeout` | `RHYTHM_ECHO_TIMEOUT` | `2` | Seconds to wait for Echo. |
| `guzzle` | `RHYTHM_ECHO_GUZZLE` | `true` | Also covers Guzzle clients built by the container, and the Vonage client. See below. |

**Production guard.** When `app()->environment('production')`, the connector does nothing unless `force` is set. `php artisan echo:status` says why it's off.

**Testing.** The app's own test suite keeps its `Mail::fake()` and `Http::fake()` untouched, because `testing` is off by default.

## What it covers

| Path | How | Covered |
|---|---|---|
| Mail: Mailables, `Mail::raw`, mail notifications, queued mail, every mailer, `Mail::build`, failover and round-robin | Every mailer's transport is wrapped (`EchoTransport`), so the capture happens under the Mailer, on the finished message | yes |
| `Http::` facade, including `Http::pool`, and packages built on it (such as the Slack Web API notification channel) | `Http::globalMiddleware` | yes |
| Guzzle clients the container builds (type-hinted `GuzzleHttp\Client`, such as the Slack webhook notification channel) | the container's `GuzzleHttp\Client` binding gets the same middleware | yes, when `guzzle` is on |
| Vonage SMS notifications | `vonage.http_client` points at the container's Guzzle client, unless you set your own | yes, when `guzzle` is on |
| `new GuzzleHttp\Client()`, raw cURL, SDKs with their own HTTP stack (the Twilio and Stripe PHP SDKs, AWS SDK) | not hooked | **no**: use Echo's proxy, see below |

**Captures.** An email capture carries from, to, cc, bcc, reply-to, subject, text, HTML, headers and the full MIME, with the mailer name in `source.plugin`. An HTTP capture carries the method, URL, headers and the first 256 KiB of the body, with the calling package in `source.plugin` when one can be found. Credential headers are masked before anything leaves the app: any header whose name contains `token`, `secret`, `key`, `auth`, `signature`, `session`, `password`, `cookie` or `credential` (any case), so `X-Auth-Token` becomes `•••••` while `X-Request-Id` is kept. `Authorization` keeps only its scheme (`Bearer •••••`). A Stripe key keeps its mode prefix (`Bearer sk_test_•••••`), which holds no secret and tells Echo whether it may fake the call. Echo redacts secrets in bodies.

**HTTP decisions.** Echo answers each request before it's sent:
- `fake`: the app gets Echo's response, and nothing is sent.
- `block`: the request fails with Laravel's `ConnectionException`, as if the network were down.
- `allow`: the request is sent, and the response is reported afterwards. Echo always allows the site's own host (the host of `APP_URL`, never the request's `Host` header), loopback and the hosts in its allowlist.

**Mail decisions.** Sealed, Echo keeps the message and the app sees a normal send. Observed, the real transport sends it, and the connector then reports the outcome against the same message: sent, or failed with the transport's error (a dead SMTP server, a rejected API key). The exception still reaches the app as usual.

**Echo unreachable.** With `mode` set to `seal`, mail fails with a `TransportException` and HTTP calls fail with a `ConnectionException`. Nothing leaves. The `array` and `log` mailers keep working, because they never leave the machine. With `mode` set to `observe`, traffic goes out normally.

**No `Http::preventStrayRequests()`.** The seal doesn't use it, and you don't need to call it on staging. It throws on every request that isn't faked, including the site's own host and the hosts you allow. Echo already decides per host and answers sealed requests with provider-shaped fakes, which is what keeps code paths like webhook retry counters working.

**Why the transport and not only `MessageSending`?** A `MessageSending` listener that returns `false` does cancel the send. But Laravel dispatches that event with `until()`, which stops at the first listener that returns anything other than `null`. One other listener returning `true` would let the mail out. The transport wrapper can't be skipped like that. The `MessageSending` listener is only a fallback for apps that bind their own mail manager.

### Clients the connector can't see

For SDKs and raw clients, route them through Echo's HTTP proxy:

```sh
HTTPS_PROXY=http://127.0.0.1:8026 HTTP_PROXY=http://127.0.0.1:8026 php artisan queue:work
```

Then trust Echo's CA certificate in PHP's `openssl.cafile` or `curl.cainfo`. The connector's own calls to Echo always bypass the proxy.

As a fallback that needs no package at all, point the mailer at Echo's SMTP sink: `MAIL_MAILER=smtp MAIL_HOST=127.0.0.1 MAIL_PORT=1025`. Run `php artisan config:clear` after changing it.

## Runs and actions

Echo ties every message to the action that caused it, such as "Contact form on /contact/". Soundcheck and `rhythm echo run` tag each request with `X-Echo-Run: <run>/<action>`, as a header or a cookie. The connector finds the run in this order:

1. the `X-Echo-Run` header on the incoming request
2. the `X-Echo-Run` cookie
3. the [Context](https://laravel.com/docs/context) a queued job carried over from that request
4. the `RHYTHM_ECHO_RUN` environment variable, which Echo sets on the artisan commands it runs

A visitor can send `X-Echo-Run` too and add messages to a baseline or drop them from one, so the header and cookie count only when Echo vouches for them:

- with `RHYTHM_ECHO_TOKEN` set, the value must carry Echo's signature (`<run>/<action>.<signature>`, which Echo adds when it has the same token). Unsigned or tampered values are ignored.
- without a token, the value counts only on a request straight from this machine: `REMOTE_ADDR` is loopback (127.0.0.0/8 or ::1) and there is no `X-Forwarded-For`, `Forwarded` or `X-Real-IP` header. Behind a reverse proxy or Docker's port mapping, set a token.

An ignored value is logged once as a warning, for example `Rhythm Echo: ignored an unsigned X-Echo-Run: with RHYTHM_ECHO_TOKEN set, only runs Echo signed with it count`. `RHYTHM_ECHO_RUN` is always taken as given: a visitor cannot set it.

A global middleware stores the run as a *hidden* Context value, so it never appears in log lines. Laravel serialises Context into every job queued during the request and restores it in the worker. Queued mail, queued notifications and jobs therefore keep the run without any change to your code.

## Queues

Queued mail and jobs are sent when the worker runs them. For staging checks, either:

- set `QUEUE_CONNECTION=sync`, so everything is sent during the request, or
- leave the queue on and let Echo flush it after each action with `php artisan queue:work --stop-when-empty`. This is the Laravel flush command Echo runs.

## Commands

- `php artisan echo:status [--json]`: whether the connector is active (and why not), Echo's URL and mode, and a ping of `/v1/health`. It exits non-zero when the connector is off or Echo can't be reached.
- `php artisan echo:canary [--json]`: sends one mail to `canary@echo.invalid` and one `POST https://canary.echo.invalid/` through the normal paths (the default mailer and the `Http` facade). `rhythm echo doctor` checks that both arrive. The `.invalid` addresses can't be delivered even when the site is unsealed.

## Development

```sh
make test      # composer install, PHPUnit + Testbench (fake Echo on PHP's built-in server), Larastan level 6: what CI runs
make check     # php -l on every file, then make test
make phpunit   # only PHPUnit, after make install
make e2e       # a fresh Laravel app against Echo's real catcher (needs Go, Docker, jq)
```

Everything runs in Docker (`composer:2` and `php:8.3-cli`); `PHP_VERSION=8.2 make test` picks another PHP image. `make test COMPOSER=composer PHP=php` runs on the host's PHP, as CI does. Composer's download cache is `COMPOSER_CACHE_DIR` from the environment (default `~/.cache/composer`), mounted into Docker, so CI can keep it between runs; with `COMPOSER_DISABLE_NETWORK=1` the install uses only that cache. The tests talk only to a fake Echo they start on 127.0.0.1. Run-signature tests read Echo's shared vectors from `conformance/` in the Echo repository (or the folder `ECHO_CONFORMANCE` names) and skip without them.

## License

MIT
