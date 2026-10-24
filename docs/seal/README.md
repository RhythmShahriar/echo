# Hard seal

Echo's connectors see what a site sends from the inside. They are how Echo
knows that a request is an order email or a Twilio SMS. But a connector only
sees what goes through the hooks it patches. An SDK that opens its own
socket, a plugin that calls `fsockopen`, or a cron job in another language
goes around it.

A **hard seal** closes that gap at the network level. The site has no route
to the internet at all. Its only way out is Echo:
- the proxy on `:8026`
- the SMTP sink on `:1025`
- the capture API on `:8025`

Anything that doesn't go through Echo fails instead of leaking. The seal
fails closed.

```
 site ──► Echo proxy :8026 ──► recorded; faked (seal) or forwarded (observe, --allow)
      ──► Echo SMTP  :1025 ──► recorded, never delivered
      ──► anything else    ──► refused by the network
```

## Recipes

| Where the site runs | Recipe | Tested |
|---|---|---|
| Docker Compose | [docker-compose.md](docker-compose.md): an `internal: true` network, with Echo as the only service on both networks | yes: `go test -tags docker -run ComposeSeal ./proxy` |
| A Linux VM or server | [linux-firewall.md](linux-firewall.md): nftables or iptables egress deny, where only Echo's user may connect out | yes, in a network namespace: `go test -run Firewall ./proxy` |
| php-fpm, WordPress | [php-fpm.md](php-fpm.md): `env[]` lines, `sendmail_path`, CA trust and `WP_PROXY_*` | partly (see the page) |

The network recipes enforce the seal. The php-fpm settings route traffic
through Echo, so it gets recorded and faked instead of simply refused. Use
both.

## Trusting Echo's CA

To fake or record HTTPS, the proxy terminates TLS with a certificate from
Echo's local CA (`rhythm echo up --proxy :8026 --mitm`). Clients must trust
that CA:
- `rhythm echo ca` prints the path to the certificate.
- Containers can fetch it from the proxy at `http://echo:8026/ca.pem`. It's
  a certificate, not a secret; the private key never leaves Echo.

Hosts on Echo's allowlist, and loopback and private addresses, are tunnelled
untouched, so they present their real certificates. Clients that replace
their trust store (rather than adding to it) need a **bundle**: the system
CAs plus Echo's.

```sh
cat /etc/ssl/certs/ca-certificates.crt "$(rhythm echo ca)" > /etc/rhythm-echo/bundle.pem
```

| Client | Proxy | Trust Echo's CA | Verified |
|---|---|---|---|
| curl | `HTTPS_PROXY`, `http_proxy` (lowercase) | `--cacert bundle.pem`, or `CURL_CA_BUNDLE` | yes |
| PHP cURL (Twilio SDK, Guzzle) | `https_proxy` or `HTTPS_PROXY` in the environment; for php-fpm, `env[]` | `curl.cainfo` and `openssl.cafile` in php.ini, or `CURLOPT_CAINFO` | yes (CLI) |
| WordPress `wp_remote_*` | `WP_PROXY_*` constants | `http_request_args` → `sslcertificates` ([echo-ca.php](examples/echo-ca.php)) | no |
| Node 24+ `fetch`, `https` | `NODE_USE_ENV_PROXY=1` with `HTTPS_PROXY` | `NODE_EXTRA_CA_CERTS=ca.pem` (adds to the defaults) | yes |
| Python `requests` | `HTTPS_PROXY` | `REQUESTS_CA_BUNDLE=bundle.pem` (replaces the defaults) | yes |
| Go `net/http` | `HTTPS_PROXY` | `SSL_CERT_FILE=bundle.pem`, or the system store | yes (tested with `RootCAs`) |
| Debian or Alpine system store | n/a | copy to `/usr/local/share/ca-certificates/rhythm-echo.crt`, then `update-ca-certificates` | no |

Some SDKs ship their own CA bundle and ignore all of these. For example,
Stripe's PHP SDK uses its own `data/ca-certificates.crt`. For those, either:
- put the API on Echo's allowlist (`--allow api.stripe.com`, test keys only),
  so it is tunnelled to the real server with its real certificate; or
- point the SDK at the bundle, e.g. `\Stripe\Stripe::setCABundlePath()`.

## What the proxy does with each request

| | Sealed | Observed |
|---|---|---|
| Plain HTTP | Recorded, then faked | Recorded and forwarded; the response is recorded |
| HTTPS, `--mitm` | Decrypted, recorded, faked | Decrypted, recorded, forwarded |
| HTTPS, no `--mitm` | Refused (403), recorded as blocked | Tunnelled; the host is recorded |
| Loopback, private ranges, single-label names, `--allow` | Tunnelled or forwarded, with the host recorded | same |

Interception speaks HTTP/1.1 only. The proxy offers just `http/1.1` in ALPN,
and clients fall back to it.

The proxy is never an open relay:
- It listens on loopback by default.
- Beyond loopback, it requires the Echo token as the proxy password:
  `http://echo:TOKEN@host:8026`, or `Proxy-Authorization: Bearer TOKEN`.
