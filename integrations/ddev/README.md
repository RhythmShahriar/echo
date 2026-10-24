# Echo for DDEV

Swaps DDEV's built-in Mailpit for [Echo](https://rythm.sh/echo), with no
change to the app: mail the site sends still goes to DDEV's Mailpit,
which now passes every message on to Echo.

```sh
ddev add-on get https://github.com/RhythmShahriar/echo/releases/latest/download/ddev-echo.tar.gz
ddev restart
ddev echo        # opens the inbox: https://<project>.ddev.site:8026
```

Pin a release by naming it in the URL (`.../releases/download/v1.2.3/ddev-echo.tar.gz`).

## What it changes

| | Before | With Echo |
|---|---|---|
| Inbox on `https://<project>.ddev.site:8026` (and `http://…:8025`) | Mailpit | Echo |
| Mailpit's own inbox | 8026 | 8030 (`http://…:8029`) |
| `mail()`, SMTP to `localhost:1025` | Mailpit | Mailpit, relayed to Echo |
| Inside the project network | | `http://echo:8025` (API), `echo:1025` (SMTP) |

The add-on writes three files into `.ddev/`:

- `docker-compose.echo.yaml`: the `echo` service, from `ghcr.io/rhythmshahriar/echo:1`.
- `config.echo.yaml`: moves Mailpit to ports 8029 and 8030 and sets
  `MP_SMTP_RELAY_HOST=echo`, `MP_SMTP_RELAY_PORT=1025` and
  `MP_SMTP_RELAY_ALL=true` in the web container.
- `commands/host/echo`: `ddev echo`.

Mailers set to `echo:1025` (`MAIL_HOST=echo` in Laravel) skip the relay.
Echo's Mailpit-style API (`/api/v1/messages`, `/api/v1/message/{ID}`)
works for tools written against Mailpit.

## Connectors and the proxy

Point the WordPress, Laravel or Node connector at Echo with
`RHYTHM_ECHO_URL=http://echo:8025` in `web_environment`. Echo listens
without a token because the DDEV network is private; to require one, set
`RHYTHM_ECHO_TOKEN` for both services:

```sh
ddev config --web-environment-add=RHYTHM_ECHO_URL=http://echo:8025,RHYTHM_ECHO_TOKEN=change-me
ddev dotenv set .ddev/.env.echo --rhythm-echo-token=change-me
```

## Another image

```sh
ddev dotenv set .ddev/.env.echo --echo-docker-image=ghcr.io/rhythmshahriar/echo:1.2.3
ddev restart
```

## Remove

```sh
ddev add-on remove echo
ddev restart
```

## Tests

`tests/test.bats` follows DDEV's add-on template. CI runs it on every pull
request with an image built from the same commit.
