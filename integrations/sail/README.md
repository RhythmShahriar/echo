# Echo for Laravel Sail

Sail starts Mailpit as a service named `mailpit`. Swap its image for Echo
and keep the name, so nothing in `.env` changes:

```yaml
    mailpit:
        image: 'ghcr.io/rhythmshahriar/echo:1'   # was axllent/mailpit:latest
        ports:
            - '127.0.0.1:${FORWARD_MAILPIT_PORT:-1025}:1025'
            - '127.0.0.1:${FORWARD_MAILPIT_DASHBOARD_PORT:-8025}:8025'
        networks:
            - sail
```

[docker-compose.echo.yml](docker-compose.echo.yml) is the whole service.
Then run `sail up -d`: the inbox is at http://localhost:8025, with
Mailpit's API (`/api/v1/messages`) for tests that read it.

The ports are bound to loopback: Echo listens without a token on Sail's
private network and anyone who reaches the API can read what the app sent.
To reach it from elsewhere, set a token instead:

```yaml
        environment:
            RHYTHM_ECHO_TOKEN: '${RHYTHM_ECHO_TOKEN}'
```

The SMTP sink then asks for it: `MAIL_USERNAME=echo` and
`MAIL_PASSWORD=${RHYTHM_ECHO_TOKEN}` in `.env`.

To capture more than mail, add the Laravel connector
(`rhythm-echo install laravel`) with `RHYTHM_ECHO_URL=http://mailpit:8025`.
