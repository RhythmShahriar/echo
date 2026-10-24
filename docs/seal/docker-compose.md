# Hard seal with Docker Compose

The app and its database sit on a network with `internal: true`. Docker
gives that network no gateway, so nothing on it can reach the internet.

Echo is the only service on both networks:
- the sealed one, where the app reaches it as `echo`
- a normal one, which is its way out for allowed and observed traffic

Example: [examples/docker-compose.yml](examples/docker-compose.yml)
(WordPress and MariaDB), with [examples/nginx-front.conf](examples/nginx-front.conf).

```
            ┌──────────── sealed (internal: true) ─────────────┐
 browser ──►│ front ──► app ──► echo :8026 proxy                │──► internet
 127.0.0.1  │ (nginx)    │      echo :1025 SMTP                 │    (echo only:
 :8080      │            └──► db                                │     --allow, observe)
            └──────────────────────────────────────────────────┘
```

## Run it

```sh
export RHYTHM_ECHO_TOKEN=$(openssl rand -hex 16)
docker compose -f docs/seal/examples/docker-compose.yml up -d
```

Echo runs from its image, `ghcr.io/rhythmshahriar/echo:1`. Set
`RHYTHM_ECHO_IMAGE` to pin a release (`ghcr.io/rhythmshahriar/echo:1.2.3`)
or to use an image built from a checkout.

- The site is at http://127.0.0.1:8080.
- Echo's inbox and API are at http://127.0.0.1:8025. Send the token as
  `X-Echo-Token`.

## How it fits together

- **`echo`** runs `rhythm-echo up --seal --mitm` from Echo's image with:
  - the API on `:8025`
  - SMTP on `:1025`
  - the proxy on `:8026`

  It listens on `0.0.0.0` inside its container, so it requires the token,
  which it reads from `RHYTHM_ECHO_TOKEN`. The CA, including its private
  key, is kept in the `echo-state` volume (mounted at
  `/home/nonroot/.cache/rhythm/echo`), so trust survives restarts.
- **`echo-ca`** is a one-shot job. It copies the CA *certificate* from
  `http://echo:8026/ca.pem` into the `echo-ca` volume, which the app mounts
  read-only at `/echo-ca/ca.pem`. The key is never shared.
- **`app`** gets:
  - `HTTPS_PROXY`, `https_proxy` and `http_proxy`, pointing at Echo with the
    token as the proxy password
  - `RHYTHM_ECHO_URL`, `RHYTHM_ECHO_SMTP` and `RHYTHM_ECHO_TOKEN`, for the
    connectors and the sendmail shim
  - `NODE_EXTRA_CA_CERTS`

  Only lowercase `http_proxy` is set for plain HTTP (httpoxy).
- **`front`** is the way in. Services on an internal network can't publish
  ports, so nginx sits on both networks and forwards to `app:80`. It passes
  `X-Echo-Run` through, so Echo can link messages to the action that caused
  them. It is not a way out: it only proxies to the app.

For WordPress, two more steps:
- Add the [php-fpm.md](php-fpm.md) settings. The Apache image passes the
  container environment to PHP, so only CA trust and `sendmail_path` are left.
- Install the Echo mu-plugin (`rhythm echo install wordpress`).

## Check the seal

```sh
p="docker compose -f docs/seal/examples/docker-compose.yml"
# No route out: this fails at once.
$p exec app sh -c 'env -u https_proxy -u http_proxy curl -m 3 https://1.1.1.1/'
# Through Echo: faked while sealed, and recorded.
$p exec app curl -sS --cacert /echo-ca/ca.pem -d 'To=%2B15551230000&Body=hi' \
  https://api.twilio.com/2010-04-01/Accounts/AC123/Messages.json
# Mail goes to the sink.
$p exec app sh -c "printf 'To: a@example.test\nSubject: hi\n\nhi\n' | sendmail -S echo:1025 -t"
```

`go test -tags docker -run ComposeSeal ./proxy` runs these checks
automatically, with a small nginx app in place of WordPress (see
`proxy/testdata/compose.override.yml`). It checks that:
- the app has no route out
- the CA certificate is shared, and the key isn't
- HTTP and HTTPS requests to `api.twilio.com` are faked
- the proxy refuses clients without the token
- mail reaches the sink
- the inbox is readable from the host
- the site is reachable through the front

Ports are 18925 and 18980, and everything is removed afterwards.

## Notes

- **DNS:** Docker's embedded DNS resolves service names on the internal
  network. Proxied clients never resolve outside names themselves; the proxy
  does.
- **Letting a host through for real:** add `--allow api.stripe.com` to
  Echo's command (use test keys). Echo tunnels it with its real certificate
  and records the host.
- **Observe mode:** use `--observe` instead of `--seal`. Everything is
  recorded and forwarded. The network seal still guarantees that nothing
  bypasses Echo.
- **BusyBox `wget`** (Alpine) sends `GET` instead of `POST` through a proxy,
  and ignores `no_proxy`. Use curl for checks.
