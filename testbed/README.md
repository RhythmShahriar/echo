# Echo testbed

Twenty ways a site stops saying what it should, in one Docker Compose stack plus fifteen ways the environment around Echo goes wrong, for Echo's end-to-end tests and its public benchmark ([docs/benchmark.md](../docs/benchmark.md)).

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
| s14 | content | `order.created` no longer carries `billing.email` | BROKE message/fields-lost |
| s15 | integrations | the lead goes to a test Zap's catch hook | BROKE message/missing |
| s16 | integrations | the CRM answers 500 to the order webhook (observe mode); five failures disable it | BROKE message/failing or send/failed |
| s17 | deliverability | the autoresponder is sent from the newsletter domain, which SendGrid isn't set up for | BROKE dns/dmarc (fixture DNS) |
| s18 | deliverability | three more includes push SPF to 11 lookups | BROKE dns/spf (fixture DNS) |
| s19 | staging | a staging refresh brings a real customer address, with Echo in observe mode | NEW seal/real-recipient |
| s20 | inbound | a Stripe `payment_intent.succeeded` replay no longer triggers the payment email | BROKE message/missing |

Messages link to and come from `shop.example.com`: the testbed plays the live shop (`site:` in `.echo/config.yaml`), which is what makes a link to a staging host a problem worth reporting.

### Deliverability: the fixture DNS

s17 and s18 follow the advice compare gives for a new sender: they run `rhythm-echo check <From domain> --resolver 127.0.0.1:18870 --provider sendgrid --return-path em1234.shop.example.com` in both states and diff the issues. The benchmark serves [`dns/zone.yaml`](dns/zone.yaml) on 127.0.0.1:18870 (UDP and TCP). It answers only for the names in that file, never forwards and switches records with the scenario's state.

### Environment scenarios

The `environment:` list in `scenarios.yaml` (e01 to e15) is the other half: the site sends what it always did while the site is down, two runs share one Echo, Echo restarts, the resolver is gone, a connector can't reach Echo or the snapshot comes from an older Echo. Each has a probe in [`bench/probes.go`](bench/probes.go) (local) or [`bench/probes_testbed.go`](bench/probes_testbed.go) (testbed) that sets up the good and the broken state, runs Echo's commands as named steps and records what each printed, its exit code, its `--json` verdict and the comparison's changes. The manifest says what each step must show (`exit`, `verdict`, `says`, `never`, `change`, `no_class`): the good state must pass its checks and the scenario is caught when the broken state passes its own.

| ID | Needs | Broken state | Echo must |
|---|---|---|---|
| e01 | local | the site is down at snapshot, then at compare | exit 3 with a run error, never "all clear" |
| e02 | local | two compares at once against one Echo | no mail in the wrong run; the "could not be tied" note for the untagged SMTP mail only |
| e03 | local | Echo on 0.0.0.0 with a token, asked from another container | 401 without the token for the live view and the API alike; refuse 0.0.0.0 without a token |
| e04 | local | the queue sends the receipt after quiet, during the next action | credit it with a "came late" note, not BROKE |
| e05 | local | the PDF invoice is no longer attached | BROKE message/attachment-lost |
| e06 | testbed | a 2 MB newsletter with a 5 MB PDF through the Laravel connector | captured and faked within the connector's 2 s timeout |
| e07 | local | Echo restarts during a compare | say Echo restarted during the run |
| e08 | testbed | the sites can't reach Echo (it moved off the gate's ports) | BROKE for what's missing and say a connector can't reach Echo |
| e09 | testbed | s10 to s12 broken, with order numbers and dates that change | only the expected change, never CHANGED text |
| e10 | testbed | the Echo 1.0.0 WordPress connector against this Echo, then this connector against Echo 1.0.0 | mail captured both ways |
| e11 | local | the snapshot comes from Echo 1.0.0 (`bench/testdata/runs/1.0.0`) | load it and compare |
| e12 | local | two action names give the same ID | refuse the config, exit 2 |
| e13 | windows | snapshot and compare on Windows | work and catch the lost email |
| e14 | local | the resolver refuses, then never answers, for s17's and s18's domains | exit 3 naming the resolver, within 30 s |
| e15 | local | RTL, CJK and a right-to-left override in the order email | no mojibake or placeholder hit, no change, the override printed escaped |

The local probes need no Docker: they run their own Echo, fixture site and resolver on 127.0.0.1:23200-23239 (`go run -tags testbed ./testbed/bench -only env -tools none`, or `TestLocalProbes`). e10 builds Echo 1.0.0 from its git tag with the modules in the Go cache.

### Observe mode

s16 and s19 run Echo in observe mode, so the request or email really goes out. Their actions only reach the testbed's CRM (through the gate) and Mailpit and `TestManifest` checks that no other action is run in observe mode.

## Benchmark

```sh
make -C testbed up TOOLS="echo mailpit buggregator staging-shield"
make -C testbed benchmark TOOLS="echo mailpit buggregator staging-shield"   # writes docs/benchmark.md
make -C testbed reproduce   # twice; fails if any outcome differs
make -C testbed demo        # the 60-second demo, docs/assets/demo.gif (needs VHS)
make -C testbed down
```

`TOOLS` names what is judged besides Echo. `buggregator` starts Buggregator (`ghcr.io/buggregator/server`, `BUGGREGATOR_VERSION` picks the tag, 2.4.1 by default) on 127.0.0.1:18857 (API) and 18858 (SMTP); `staging-shield` downloads the Staging Shield plugin from wordpress.org into the WordPress image when it is built (version 1.0.2 unless `STAGING_SHIELD` names another), inactive until the benchmark activates it. Without `TOOLS`, `make up` sets up Mailpit only, as before.

For each scenario, the harness in [`bench/`](bench/) does the following, in `testbed/bench/work/<id>/`:

1. runs the manifest's `reset` commands (drain what an earlier broken flush left queued), sets the good state and starts Echo in the scenario's mode
2. runs `rhythm-echo snapshot --config testbed/.echo/config.yaml --action "<action>" --name good --fail-on none --json -o good.json`
3. breaks the scenario
4. runs `rhythm-echo compare --config … --action "<action>" --snapshot good --fail-on none --json -o compare.json` and reads the comparison from the envelope's `data`
5. for `dns:` scenarios, runs `rhythm-echo check --fail-on none` on the From domains in each state (see above) and adds the difference to the comparison
6. passes when the comparison has a change matching one of the scenario's `expect` entries

Echo's commands get `TESTBED` (for the config's commands), `RHYTHM_ECHO_URL`, `RHYTHM_ECHO_TOKEN`, `XDG_CACHE_HOME` (the testbed's proxy CA) and `TESTBED_STRIPE_SECRET` (the Stripe test endpoint's signing secret, which `testbed-stripe.php` also builds). To run the actions by hand, start `make echo` and, from `testbed/`, set those variables the same way (see `bench/echo.go`).

The other tools are judged on the same states. Mailpit and Buggregator: the harness delivers the SMTP email each state sent into the tool and reads it back through its API (rule in [`bench/tools.go`](bench/tools.go), adapters in [`bench/mailpit.go`](bench/mailpit.go) and [`bench/buggregator.go`](bench/buggregator.go)). Staging Shield ([`bench/stagingshield.go`](bench/stagingshield.go)): for the staging scenarios it is activated and the action run again: it counts when the email to the real address doesn't leave. The scoreboard says how each is judged. MessagePit isn't run.

Flags:

| Flag | Default | |
|---|---|---|
| `-bin` | `bin/rhythm-echo` | the binary to benchmark |
| `-only` | all | `s01,e05`: some scenarios; `env`: every environment scenario |
| `-tools` | `auto` | `auto` (each tool that answers), `none`, or names: `"mailpit buggregator staging-shield"` (each must answer) |
| `-diff` | | `-diff a.json b.json`: compare the outcomes of two `results.json` and exit 1 if they differ |
| `-out` | `docs/benchmark.md` | the scoreboard |
| `-work` | `testbed/bench/work` | runs and `results.json` |
| `-timeout` | `3m` | per command |

The bench package has the `testbed` build tag, so a plain `go test ./...` never needs Docker:

```sh
go test -tags testbed -v ./testbed/bench/
```

- `TestManifest`: every scenario's action exists, observe mode is only used for actions that stay inside the testbed and `toggle.sh` knows every ID.
- `TestFixtureDNS`: the fixture DNS serves the zone, follows CNAMEs, splits long TXT records and switches s18's record.
- `TestEnvironmentManifest`, `TestVerify`, `TestEvaluateEnv`, `TestPick`, `TestParseTools`, `TestDiffResults`: the environment scenarios' manifest and checks, the tool list and the comparison of two runs.
- `TestMailpitAgainstAStub`, `TestBuggregatorAgainstAStub`, `TestStagingShieldAgainstAStub`: each tool's adapter against a stand-in with that tool's API.
- `TestOldSnapshotsLoad`: every snapshot in `bench/testdata/runs` loads.
- `TestLocalProbes`: builds Echo and runs every local environment scenario (and the Windows one, which needs nothing Windows-only), each must be caught.
- `TestGoodStateIsClean` (needs the testbed up): builds Echo, runs every action with every scenario good and checks that each sent something, sealed, with nothing above a notice. So a caught scenario is caught because of its broken state alone.

## Adding a scenario

1. Implement the broken state behind `testbed_broken('sNN')` (or `/state/sNN`), next to the code it breaks and say what it does in the file's header.
2. If no action shows it, add one to `.echo/config.yaml`.
3. Add it to `scenarios.yaml` with a precise `expect` (and `mailpit` when the problem is visible in an email).
4. Check both states by hand (`make echo`, then `rhythm-echo run --action …`) and that the good state stays clean (`TestGoodStateIsClean`).
