# PHP, php-fpm and WordPress through Echo

These settings route a PHP site's mail and outbound HTTP through Echo,
where they are recorded and, while sealed, faked. They don't *enforce*
anything: pair them with a [network seal](README.md#recipes).

Examples:
- [examples/php-fpm-echo.conf](examples/php-fpm-echo.conf): pool `env[]`
  lines, `sendmail_path` and CA trust
- [examples/wp-config-echo.php](examples/wp-config-echo.php): `WP_PROXY_*`
  constants
- [examples/echo-ca.php](examples/echo-ca.php): a mu-plugin that makes
  `wp_remote_*` trust Echo's CA

## Mail: `sendmail_path`

```ini
sendmail_path = "/usr/local/bin/rhythm-echo sendmail -t -i"
```

`rhythm-echo sendmail` reads the message on stdin like sendmail and hands it
to Echo's SMTP sink (`RHYTHM_ECHO_SMTP`, default `127.0.0.1:1025`). Without
`RHYTHM_ECHO_SMTP` but with `RHYTHM_ECHO_URL`, it posts to the capture API
instead. It understands:
- `-t`: recipients from To, Cc and Bcc. Bcc is removed from the message and
  Echo records it as Bcc.
- `-i` and `-oi`: a line with a single dot doesn't end the message.
- `-f` and `-r`: the envelope sender.
- `-F`: the full name.

It ignores MTA-only options (`-o*`, `-B`, `-N`, `-v`…) and adds `Date` and
`Message-ID` when they're missing. It refuses to run and `mail()` returns
false, when `WP_ENVIRONMENT_TYPE`, `APP_ENV`, `VERCEL_ENV` or `NODE_ENV` says
`production`. Set `RHYTHM_ECHO_FORCE=1` to override.

Tested with PHP 8.3 `mail()` (`go test -tags docker -run PHPMail
./sendmail`). This covers To, Bcc, the `X-Echo-Run` header and lone-dot
lines.

## HTTP: proxy variables for php-fpm

php-fpm starts its workers with an empty environment (`clear_env = yes`), so
`HTTPS_PROXY` set on the container or the systemd unit never reaches PHP. Set
the variables per pool:

```ini
[www]
env[https_proxy] = http://echo:ECHO_TOKEN@127.0.0.1:8026
env[HTTPS_PROXY] = http://echo:ECHO_TOKEN@127.0.0.1:8026
env[http_proxy]  = http://echo:ECHO_TOKEN@127.0.0.1:8026
env[no_proxy]    = localhost,127.0.0.1,::1
```

libcurl reads these when each transfer starts, so they reach every SDK that
uses the cURL extension (Twilio, Stripe, AWS, Guzzle), unless the code sets
`CURLOPT_PROXY` itself. Notes:
- **Uppercase `HTTP_PROXY`:** libcurl never reads it, because under CGI a
  request header can set it (httpoxy).
- **Token:** drop `echo:ECHO_TOKEN@` when Echo listens on loopback without
  a token.
- **Apache with mod_php and the PHP CLI** inherit the process environment,
  so there `HTTPS_PROXY` on the container is enough.

**Setting it from PHP:** `putenv('https_proxy=http://127.0.0.1:8026')` at
runtime also works. libcurl picks it up on the next transfer: tested in
`php:8.3-cli` with both spellings (`go test -tags docker -run
SpikePHPPutenv ./proxy`). PHP undoes `putenv()` at the end of each request,
so a mu-plugin or `auto_prepend_file` would have to set it on every request.
Prefer `env[]`. The php-fpm behaviour is being checked separately.

## Trusting Echo's CA

```sh
cat /etc/ssl/certs/ca-certificates.crt "$(rhythm echo ca)" > /etc/rhythm-echo/bundle.pem
```

```ini
php_admin_value[curl.cainfo]    = /etc/rhythm-echo/bundle.pem
php_admin_value[openssl.cafile] = /etc/rhythm-echo/bundle.pem
```

Use a bundle, not Echo's certificate alone. Allowed hosts are tunnelled to
the real server, which presents its real certificate. Code that sets
`CURLOPT_CAINFO` itself ignores `curl.cainfo`:
- **WordPress:** see below.
- **Stripe's PHP SDK:** call `\Stripe\Stripe::setCABundlePath('/etc/rhythm-echo/bundle.pem')`,
  or allowlist `api.stripe.com` and use test keys.

## WordPress

With the Echo mu-plugin (`rhythm echo install wordpress`), `wp_mail()` and
every `wp_remote_*` call are captured in plain text by hooks, before any
network. The proxy is for the rest: SDKs and plugins that call cURL
directly.

If the mu-plugin isn't installed, WordPress can still send its HTTP API
through Echo with the `WP_PROXY_*` constants. Put these in `wp-config.php`;
the example wraps them in a production check:

```php
define( 'WP_PROXY_HOST', '127.0.0.1' );
define( 'WP_PROXY_PORT', '8026' );
define( 'WP_PROXY_USERNAME', 'echo' );                        // when Echo has a token
define( 'WP_PROXY_PASSWORD', getenv( 'RHYTHM_ECHO_TOKEN' ) );
define( 'WP_PROXY_BYPASS_HOSTS', 'localhost, 127.0.0.1, *.local' );
```

WordPress never proxies `localhost` or the site's own host, so cron loopbacks
keep working.

WordPress verifies TLS against its own bundle
(`wp-includes/certificates/ca-bundle.crt`), not the system's or
`curl.cainfo`. Add Echo's CA through the `http_request_args` filter with
[echo-ca.php](examples/echo-ca.php) as a mu-plugin:

```sh
cat wp-includes/certificates/ca-bundle.crt "$(rhythm echo ca)" > /etc/rhythm-echo/bundle.pem
```

Not yet tested here: the `WP_PROXY_*` constants and echo-ca.php against a
running WordPress (both pass `php -l`) and the php-fpm pool file in a real
php-fpm.
