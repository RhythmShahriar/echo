# Echo testbed

Twenty ways a site stops saying what it should, in one Docker Compose stack, for Echo's end-to-end tests and its public benchmark ([docs/benchmark.md](../docs/benchmark.md)).

Each scenario breaks one thing a site sends, the way a plugin update, a deploy, a migration or a DNS change would: an email that's no longer sent, a merge tag left in a receipt, an SMS that now costs three parts, a webhook that lost a field, a From domain that fails DMARC. Every scenario has a **good** and a **broken** state and `toggle.sh` switches between them right away, without a rebuild or restart.

## Start

```sh
make -C testbed up      # a few minutes the first time (Laravel and Next.js are built), then about 30 s
```

| Site | URL | What it is |
|---|---|---|
| WordPress | http://localhost:18850/ | WordPress 6.8.3 on PHP 8.3 with MariaDB 11.4: "Bean & Leaf", a coffee shop. Fixture plugins: a contact form (`testbed-forms`), a WooCommerce stand-in (`testbed-shop`: receipt through WP-Cron, Twilio SMS, Slack, GA4, Meta CAPI, webhooks through an Action Scheduler stand-in), an SMS add-on whose Twilio client is raw cURL (`testbed-shop-sms`) and a Stripe webhook endpoint (`testbed-stripe`). |
| Laravel | http://localhost:18851/ | A stock Laravel app with `rhythmsh/echo-laravel` from this checkout: `POST /orders` queues a Mailable and calls Twilio with the Http client. |
| Next.js | http://localhost:18852/ | The Node connector's fixture app with `@rhythmsh/echo` packed from this checkout: `POST /api/signup` (Resend-style fetch, Twilio fetch, Nodemailer) and an Edge route (`GET /api/edge`, Slack). |
| Plain PHP | http://localhost:18853/ | A legacy order page: `POST /order.php` sends with `mail()`, through `sendmail_path` = `rhythm-echo sendmail` (the static Linux binary built from this checkout). |
| Mailpit | http://localhost:18854/ | The shop's "real" mail server in observe mode and the benchmark's Mailpit column. |

The shop's CRM (`crm/index.php`) receives the order webhooks. It is only reachable from inside the stack.

Ports are bound to 127.0.0.1. `make up` builds Echo from this checkout into `runtime/` (Go is needed), installs Echo's WordPress connector into `wp/mu-plugins` with `rhythm-echo install wordpress`, builds the Laravel and Next.js apps in Docker (`laravel/build.sh`, `next/build.sh`, reusing the connectors' own end-to-end fixtures) and seeds a fresh WordPress. The database lives in tmpfs and `make down` deletes the WordPress volume, so every run starts from the same state. The built apps stay in `runtime/`; delete it to rebuild from scratch.

Accounts (local test accounts only): WordPress `admin` / `admin`.

### Nothing can leave

The sites run on a Docker network with `internal: true`: Docker gives it no gateway, so nothing on it can reach the internet, whatever a connector or Echo decides. One container, `gate` (nginx), is on both networks:

- the way in: it publishes the sites and Mailpit on 127.0.0.1:18850-18855, passing `X-Echo-Run` through
- the way to Echo: it forwards ports 18860 (API), 18861 (SMTP) and 18862 (proxy) to the Echo on the host, at `host.docker.internal`

Echo runs on the host, not in the stack: the benchmark starts its own (`rhythm-echo up` on 0.0.0.0:18860-18862 with `--mitm` and the testbed's token, `echo-testbed-token`), or start one with `make echo` (`make echo OBSERVE=1` for observe mode). It needs the token because it listens beyond loopback. Its proxy CA lives in `runtime/cache` (`XDG_CACHE_HOME`) and WordPress trusts the certificate (`runtime/ca.pem`, never the key).

So this is also the hard seal from [docs/seal/docker-compose.md](../docs/seal/docker-compose.md), with Echo outside the stack. In observe mode, what Echo lets through still can't leave: mail goes to Mailpit, the CRM is local and anything else fails.

## Make targets

| Target | Does |
|---|---|
| `make up` | build Echo and the apps, install the connector, start the stack, seed WordPress, wait until every site answers |
| `make down` | stop and delete everything (containers, database, WordPress volume) |
| `make reset` | `down` then `up` |
| `make good` | every scenario good (`make good ID=s05` for one) |
| `make broken ID=s05` | break one scenario (without `ID`, every scenario) |
| `make status` | list broken scenarios |
| `make echo` | run Echo in the foreground the way the benchmark does (`OBSERVE=1`: observe mode) |
| `make logs` | follow the sites' logs |
| `make wp ARGS="option get admin_email"` | run WP-CLI in the WordPress container |

`bin/wp` and `bin/artisan` run WP-CLI and `php artisan` in the containers (`docker exec`), passing `RHYTHM_ECHO_RUN` on, so the connectors tie what they send to Echo's action. `bin/cron` is the shop server's crontab line (`wp cron event run --due-now`).

## Switching a scenario

```sh
testbed/toggle.sh s05 broken   # or: 5 broken, 05 broken
testbed/toggle.sh s05 good
testbed/toggle.sh all good
testbed/toggle.sh status
```

A scenario is broken while `testbed/state/<id>` exists. Every container mounts `state/` read-only and the fixtures check it on every request, job and command: `testbed_broken('s05')` in WordPress, `file_exists('/state/s05')` in Laravel, plain PHP and the CRM, `existsSync('/state/s07')` in Next.js. `bin/cron` and the benchmark's fixture DNS server check it too.

## Scenarios

[`scenarios.yaml`](scenarios.yaml) is the manifest. For each scenario it gives the stack, the Echo action that shows it (from [`.echo/config.yaml`](.echo/config.yaml)), the expected change (class, check, title) and where the broken state is implemented.

| ID | Area | Broken state | Expected |
|---|---|---|---|
| s01 | lost | the contact form no longer emails the shop owner | BROKE message/missing |
| s02 | lost | the contact form's autoresponder is off | BROKE message/missing |
| s03 | lost | the SMS add-on (raw cURL Twilio client, caught by Echo's proxy) listens on a hook that's never fired | BROKE message/missing |
| s04 | lost | the new-order Slack alert is gone | BROKE message/missing |
| s05 | lost | the GA4 purchase event isn't sent | BROKE message/missing |
| s06 | lost | Laravel's queued Mailable names a missing view, so the job throws in the worker | BROKE message/missing |
| s07 | lost | the Next.js Resend call throws (renamed API key variable); the route still answers 200 | BROKE message/missing |
| s08 | lost | the crontab line fails, so WP-Cron never sends the deferred receipt | BROKE message/missing |
| s09 | content | the owner's notification goes to the old agency's address | NEW message/new or BROKE message/missing |
| s10 | content | `{customer_first_name}` is left in the receipt | BROKE content/placeholder |
| s11 | content | plain PHP prints `Warning: Undefined variable $total` into the order email | BROKE content/php-error |
| s12 | content | the receipt links to `staging.shop.test` | NEW content/dev-link |
| s13 | content | the order SMS gained emoji: UCS-2, 3 parts | NEW sms/parts |
| s14 | content | `order.created` no longer carries `billing.email` | CHANGED message/fields-lost |
| s15 | integrations | the lead goes to a test Zap's catch hook | BROKE message/missing |
| s16 | integrations | the CRM answers 500 to the order webhook (observe mode); five failures disable it | BROKE message/failing or send/failed |
| s17 | deliverability | the autoresponder is sent from the newsletter domain, which SendGrid isn't set up for | BROKE dns/dmarc (fixture DNS) |
| s18 | deliverability | three more includes push SPF to 11 lookups | BROKE dns/spf (fixture DNS) |
| s19 | staging | a staging refresh brings a real customer address, with Echo in observe mode | NEW seal/real-recipient |
| s20 | inbound | a Stripe `payment_intent.succeeded` replay no longer triggers the payment email | BROKE message/missing |

Messages link to and come from `shop.example.com`: the testbed plays the live shop (`site:` in `.echo/config.yaml`), which is what makes a link to a staging host a problem worth reporting.

### Deliverability: the fixture DNS

s17 and s18 follow the advice compare gives for a new sender: they run `rhythm-echo check <From domain> --resolver 127.0.0.1:18870 --provider sendgrid --return-path em1234.shop.example.com` in both states and diff the issues. The benchmark serves [`dns/zone.yaml`](dns/zone.yaml) on 127.0.0.1:18870 (UDP and TCP). It answers only for the names in that file, never forwards and switches records with the scenario's state.

### Observe mode

s16 and s19 run Echo in observe mode, so the request or email really goes out. Their actions only reach the testbed's CRM (through the gate) and Mailpit and `TestManifest` checks that no other action is run in observe mode.

## Benchmark

```sh
make build                                  # bin/rhythm-echo
make -C testbed up
go run -tags testbed ./testbed/bench        # writes docs/benchmark.md
make -C testbed down
```

For each scenario, the harness in [`bench/`](bench/) does the following, in `testbed/bench/work/<id>/`:

1. runs the manifest's `reset` commands (drain what an earlier broken flush left queued), sets the good state and starts Echo in the scenario's mode
2. runs `rhythm-echo snapshot --config testbed/.echo/config.yaml --action "<action>" --name good --fail-on none --json -o good.json`
3. breaks the scenario
4. runs `rhythm-echo compare --config … --action "<action>" --snapshot good --fail-on none --json -o compare.json` and reads the comparison from the envelope's `data`
5. for `dns:` scenarios, runs `rhythm-echo check --fail-on none` on the From domains in each state (see above) and adds the difference to the comparison
6. passes when the comparison has a change matching one of the scenario's `expect` entries

Echo's commands get `TESTBED` (for the config's commands), `RHYTHM_ECHO_URL`, `RHYTHM_ECHO_TOKEN`, `XDG_CACHE_HOME` (the testbed's proxy CA) and `TESTBED_STRIPE_SECRET` (the Stripe test endpoint's signing secret, which `testbed-stripe.php` also builds). To run the actions by hand, start `make echo` and, from `testbed/`, set those variables the same way (see `bench/echo.go`).

Mailpit is judged on the same states: the harness delivers the SMTP email each state sent into the testbed's Mailpit and reads it back through Mailpit's API. The rule is in [`bench/mailpit.go`](bench/mailpit.go) and on the scoreboard. MessagePit isn't run.

Flags:

| Flag | Default | |
|---|---|---|
| `-bin` | `bin/rhythm-echo` | the binary to benchmark |
| `-only` | all | `s01,s05`: some scenarios |
| `-tools` | `auto` | `auto` (Mailpit when it answers), `none` or `mailpit` |
| `-out` | `docs/benchmark.md` | the scoreboard |
| `-work` | `testbed/bench/work` | runs and `results.json` |
| `-timeout` | `3m` | per command |

The bench package has the `testbed` build tag, so a plain `go test ./...` never needs Docker:

```sh
go test -tags testbed -v ./testbed/bench/
```

- `TestManifest`: every scenario's action exists, observe mode is only used for actions that stay inside the testbed and `toggle.sh` knows every ID.
- `TestFixtureDNS`: the fixture DNS serves the zone, follows CNAMEs, splits long TXT records and switches s18's record.
- `TestGoodStateIsClean` (needs the testbed up): builds Echo, runs every action with every scenario good and checks that each sent something, sealed, with nothing above a notice. So a caught scenario is caught because of its broken state alone.

## Adding a scenario

1. Implement the broken state behind `testbed_broken('sNN')` (or `/state/sNN`), next to the code it breaks and say what it does in the file's header.
2. If no action shows it, add one to `.echo/config.yaml`.
3. Add it to `scenarios.yaml` with a precise `expect` (and `mailpit` when the problem is visible in an email).
4. Check both states by hand (`make echo`, then `rhythm-echo run --action …`) and that the good state stays clean (`TestGoodStateIsClean`).
