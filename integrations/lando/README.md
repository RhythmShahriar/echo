# Echo for Lando

Add Echo as a service in `.lando.yml` and point the app's mailer at it.
Lando normally replaces a service's entrypoint with its own shell script;
Echo's image has no shell, so `entrypoint` names Echo directly and the
codebase isn't mounted.

```yaml
services:
  echo:
    api: 3
    type: lando
    app_mount: false
    entrypoint: /usr/local/bin/rhythm-echo
    services:
      image: ghcr.io/rhythmshahriar/echo:1
      command: up --listen=0.0.0.0:8025 --smtp=0.0.0.0:1025 --private-network --hostname=echo.myapp.lndo.site
      ports:
        - 8025
  appserver:
    overrides:
      environment:
        MAIL_MAILER: smtp
        MAIL_HOST: echo
        MAIL_PORT: 1025
        RHYTHM_ECHO_URL: http://echo:8025

proxy:
  echo:
    - echo.myapp.lndo.site:8025
```

Then `lando rebuild -y` and open https://echo.myapp.lndo.site (use your
app's name, in `--hostname` too). Without a token Echo answers only to
the names `--hostname` gives, its service name (`http://echo:8025`) and IP
addresses, so a web page can't reach it through a name of its own. Remove Lando's `mailpit` or `mailhog` service if the app has
one, or leave it and point only the mailer at Echo.

For PHP's `mail()`, copy a Linux `rhythm-echo` binary into the app and set
`sendmail_path = "/app/bin/rhythm-echo sendmail -t -i"` in the app's
php.ini, with `RHYTHM_ECHO_SMTP: echo:1025` in the environment above. Any
SMTP client that sends to `echo:1025` (such as msmtp) works too.

This recipe isn't run in CI; the DDEV add-on (`../ddev`) is.
