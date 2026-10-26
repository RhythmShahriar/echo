<p align="center">
  <picture>
    <source media="(prefers-color-scheme: dark)" srcset="docs/assets/banner-dark.svg">
    <img alt="rhythm echo" src="docs/assets/banner-light.svg" width="100%">
  </picture>
</p>

<p align="center">
  <b>Is your site still saying what it should?</b><br>
  Echo captures every email, SMS, webhook and API call your site sends, seals staging so nothing reaches real people and shows what each form, order or sign-up sends before and after a change. Built for PHP &amp; WordPress, with Laravel and Next.js connectors.
</p>

<p align="center">
  <a href="https://github.com/RhythmShahriar/echo/releases/latest"><img alt="Release" src="https://img.shields.io/github/v/release/RhythmShahriar/echo?style=flat-square&color=ff7c52&label=release"></a>
  <a href="https://github.com/RhythmShahriar/echo/actions/workflows/ci.yml"><img alt="CI" src="https://img.shields.io/github/actions/workflow/status/RhythmShahriar/echo/ci.yml?branch=master&style=flat-square&label=ci"></a>
  <a href="LICENSE"><img alt="License" src="https://img.shields.io/badge/license-MIT-100f0d?style=flat-square"></a>
  <a href="https://www.npmjs.com/package/@rhythmsh/echo"><img alt="npm" src="https://img.shields.io/npm/v/@rhythmsh/echo?style=flat-square&color=100f0d&label=npm"></a>
  <a href="https://packagist.org/packages/rhythmsh/echo-laravel"><img alt="Packagist" src="https://img.shields.io/packagist/v/rhythmsh/echo-laravel?style=flat-square&color=100f0d&label=packagist"></a>
</p>

<p align="center">
  <a href="#install">Install</a> ·
  <a href="#seal-a-site">Seal a site</a> ·
  <a href="#check-a-change">Check a change</a> ·
  <a href="#what-it-catches">What it catches</a> ·
  <a href="#cookie-consent-checks">Cookie consent</a> ·
  <a href="https://github.com/RhythmShahriar/rhythm/wiki/Echo">Docs</a> ·
  <a href="https://github.com/RhythmShahriar/rhythm/wiki/Echo-Benchmark">Benchmark</a>
</p>

<br>

<p align="center">
  <img alt="An Echo comparison after a plugin update: the shop owner's notification email is no longer sent, an SMS now has an unfilled placeholder and a webhook lost a field" src="docs/assets/compare.svg" width="720">
</p>

<br>

## The Rhythm set

| | Asks | Checks |
|---|---|---|
| [Tuner](https://github.com/RhythmShahriar/tuner) | Is my site safe? | what an attacker can find |
| [Soundcheck](https://github.com/RhythmShahriar/soundcheck) | Did my change break anything? | what visitors see and do |
| **Echo** | **Is my site still saying what it should?** | **everything the site sends** |

## Install

```sh
curl -fsSL https://rythm.sh/install | sh               # macOS · Linux
irm https://rythm.sh/install.ps1 | iex                 # Windows (PowerShell)
go install github.com/RhythmShahriar/echo/cmd/rhythm-echo@latest
```

Every download is checked against the release checksums, which are [signed with cosign](https://github.com/RhythmShahriar/rhythm/wiki/Verifying-Releases).

`rhythm echo` and `rhythm-echo` are the same command: the `rhythm` CLI runs the `rhythm-echo` binary for you. Use either; the examples here use `rhythm echo`.

## Seal a site

Start Echo, then connect the site. Nothing the site sends leaves the machine; Echo answers in each service's place, so the site carries on as if the email went out and the SMS was delivered.

```sh
rhythm echo up                                   # API :8025 (live view), SMTP :1025
rhythm echo install wordpress /var/www/staging   # or: laravel, next, node, generic
rhythm echo doctor                               # canaries through every layer plus Chrome
rhythm echo watch                                # see each message as it's sent
```

| Stack | Connector | Catches |
|---|---|---|
| **WordPress** | one mu-plugin, written by `install` | every `wp_mail()` (beats FluentSMTP, Post SMTP), every `wp_remote_*()` call (SMS plugins, Woo webhooks, Slack, GA4, Meta CAPI, API mailers) and raw cURL SDKs through Echo's proxy |
| **Laravel** | `composer require rhythmsh/echo-laravel` | every mailer and transport, the `Http` client, Vonage and Slack notifications, queued jobs (the action follows them) |
| **Next.js · Node** | `npm i -D @rhythmsh/echo` + `instrumentation.ts` | `fetch` (Node and Edge), `http`/`https` (axios, SDKs), Nodemailer; built for Vercel previews too |
| **Anything** | SMTP, `sendmail_path`, `HTTPS_PROXY` | mail and outbound HTTPS from any language, through Echo's SMTP sink and MITM proxy |

Connectors switch themselves off in production. For a seal that can't be bypassed, run the app on an internal Docker network or behind a firewall with Echo as its only way out: [recipes](docs/seal).

## Check a change

List the actions that make your site send things in `.echo/config.yaml` (`rhythm echo init` writes a first one for WordPress, Laravel, Next.js, Node or PHP): HTTP requests, WP-CLI or artisan commands, or signed webhook replays. Forms and multi-step journeys are driven by [Soundcheck](https://github.com/RhythmShahriar/soundcheck) with `--echo`.

```yaml
# .echo/config.yaml: ask WordPress for a password reset, which sends one email
site: https://staging.shop.test
actions:
  - name: Lost password
    http:
      method: POST
      url: /wp-login.php?action=lostpassword
      form: {user_login: admin}
    expect: {email: 1}
```

Each action counts as done when nothing new arrives for `quiet` (default 2s), or after `timeout` (default 30s). Echo caps `quiet` at 30s and `timeout` at 60s: a longer value acts as the cap. For one run, `--quiet 500ms` and `--timeout 10s` on snapshot, compare and run override them (within the same caps; a value outside them is refused).

Commands find `.echo/config.yaml` in the folder you're in or one above it, so they work from any subfolder: flush and command actions run in the project's folder. Snapshot, compare, run and doctor name the config they use. The search stops at the repository root (below your home folder outside a repository) and skips a folder above that isn't yours or that anyone can write to (such as `/tmp`), since a config there could run someone else's commands. Snapshots and runs are kept next to the config; `--config` points at another one (its snapshots come with it) and `--store <dir>` keeps them elsewhere.

> [!WARNING]
> **`.echo/config.yaml` is code.** A `command` action or a `flush` entry runs in your shell on every `snapshot`, `compare` and `run`. `${env:NAME}` puts an environment variable into an `http` action, which sends it to the URL the action names. Read a config from a repository you didn't write before you run Echo in it, as you would a Makefile.

```sh
rhythm echo snapshot --name before-update        # what each action sends now
# …update plugins, deploy…
rhythm echo compare --open                       # what changed, in a report
```

| Class | Means |
|---|---|
| **BROKE** | a message sent in the snapshot is missing now, fails, or has a new error (an unfilled `{first_name}`, a PHP warning in the email) |
| **NEW** | a message or a problem that wasn't there before |
| **CHANGED** | the same message but different: recipient, subject, text, sender, fields of a payload |
| **FIXED** | a problem from the snapshot is gone |

| You want to… | Run |
|---|---|
| Check the contact form still emails the office | `rhythm soundcheck compare staging.shop.test --echo` |
| Replay a Stripe payment and see the order email | `rhythm echo replay payment_intent.succeeded --to /?wc-api=wc_stripe` |
| Check that the cookie banner's Reject really rejects | `rhythm echo consent https://staging.shop.test` |
| Check SPF, DKIM and DMARC for the sender you use | `rhythm echo check shop.com --from-mail received.eml` |
| Fail CI when a message stops being sent | `rhythm echo compare` (exits 1 on BROKE by default; `--fail-on new` is stricter) |
| Compare in CI with a baseline from another job | `rhythm echo snapshot --export base.tgz` there, `rhythm echo compare --against base.tgz` here |
| Get the login code or magic link just sent, in a test | `code=$(rhythm echo latest --to ann@shop.test --code)` (`--link --kind magic` for the link; looks back 1m and waits up to `--wait`, default 10s, then exits 1). A test that signs the same user in again should pass `--run` or a short `--since 5s`, or run `rhythm echo clear --to ann@shop.test` first, or it may get the previous code |
| Stream what the site sends into a script | `rhythm echo watch --json` (one JSON object per line) |
| Read any result in a script | `--json` on any command: one shape, with `kind`, `summary`, `verdict` and `data` |
| Accept a change you meant to make | `rhythm echo compare --accept <key>` (`compare -v` shows each change's key) |
| Add a check of your own, such as an old phone number | `rules:` in `.echo/config.yaml`, then `rhythm echo checks --test` |
| Let a coding agent read what the site sent | `claude mcp add echo -- rhythm-echo mcp` |
| Let a client sign off on an email before launch | `relay: {allow: ["*@client.example"]}` in `.echo/config.yaml` and the SMTP account in `RHYTHM_ECHO_RELAY=smtp://user:pass@smtp.agency.example:587`, then `rhythm echo up --login client:<password>`. Release in Echo Inbox's email view, or `rhythm echo release <id> --to ann@client.example` |
| Watch many client sites from one Echo | `rhythm echo hub`, `rhythm echo schedule` for update windows and `rhythm echo report --site <id> --store <dir> --pdf` for the client |

## What it catches

<table>
<tr>
<td width="50%" valign="top">

### 📭 Lost messages
The admin notification that stopped, the autoresponder a plugin update removed, the SMS a gateway change broke, the Slack alert, the GA4 purchase event, the queued mail stuck in a job, the webhook Zapier no longer receives.

</td>
<td width="50%" valign="top">

### ✍️ Wrong content
Unfilled placeholders (`{first_name}`, `*|FNAME|*`, `[your-name]`), PHP warnings printed into emails, links to staging or `localhost`, garbled encoding, emails too big for Gmail, SMS that cost three parts, payloads that lost fields.

</td>
</tr>
<tr>
<td valign="top">

### 🔒 Staging that talks to real people
Sealed by default: email, SMS, WhatsApp, webhooks, analytics and push are answered by Echo. Live-mode calls to Stripe, PayPal, Adyen, Braintree, Square, Authorize.net, Mollie and Razorpay get the gateway's own refusal in every mode, so a test can never charge a card through them, even in observe mode or on an allowlisted host. In observe mode, any real recipient outside your allowlist is flagged.

</td>
<td valign="top">

### 📬 Deliverability
SPF (including the 10-lookup limit), DKIM keys, DMARC policy and alignment for the service you actually send through, MX, MTA-STS and TLS-RPT. `headers` reads a received email and tells you who sent it and why it passed or failed. Never sends anything.

</td>
</tr>
</table>

54 services are recognized out of the box, from SendGrid, Mailgun, Postmark, Resend, SES and the Gmail and Microsoft Graph APIs to Twilio, Vonage, WhatsApp Cloud, Slack, Teams, GA4, Segment, Meta, FCM, APNs, Stripe, HubSpot, Klaviyo and Zapier (`rhythm echo providers` lists them). Anything else is captured as a generic webhook.

## Cookie consent checks

**Does Reject really reject?** Echo clicks your cookie banner three ways and shows every tracker that fired before the choice or after Reject, with Consent Mode decoded from the wire.

`rhythm echo consent <url>` loads the page in a real browser (the Chrome you have, or Chrome for Testing downloaded once) as three new visitors, each in a fresh profile and each followed by a scroll, one more page and a reload:

- **No choice:** the visitor ignores the banner. Nothing that needs consent should fire.
- **Accept:** Echo clicks Accept and checks the banner's own stored state says so. Trackers may fire now.
- **Reject:** Echo clicks Reject (on the first layer, or in the settings) and checks the stored state. Nothing that needs consent should fire, on this page or the next. Hiding a banner is never a Reject.

Hits are held in the browser: checking production adds nothing to anyone's analytics.

<!-- consent first run: the PRD 9.1 text until the integration build prints the real output; replace it then. -->
```
$ rhythm echo consent https://staging.shop.test
Note: use Echo on sites you own or are authorised to test. Consent checks load the site like
one new visitor: they never log in or submit forms and every tracking hit is recorded and
stopped in the browser. Results stay on this machine with IDs masked. This notice is shown once.
Downloading Chrome for Testing 156.0.8078.47 (172 MB) once, into ~/.cache/rhythm/soundcheck/browser ...

rhythm echo 1.2.0 - consent check of https://staging.shop.test/

Browser: Chrome for Testing 156.0.8078.47 (downloaded)
Hits held: tracking hits are recorded and stopped in the browser. Nothing reached Google, Meta or any other tracker.

https://staging.shop.test/
  Banner: Cookiebot, found after 0.8s, Reject on the banner
  Consent Mode: v2 default, all four signals denied at 0.18s, before the first Google hit
  [1/3] No choice   GA4: 2 cookieless pings (gcs G100); no tracker cookies
  [2/3] Accept      GA4, Google Ads, Meta Pixel fired; update granted at 2.3s; kept on 1 more page
  [3/3] Reject      nothing fired; update denied at 2.1s; kept on 1 more page

All clear - 0 errors, 0 warnings, 1 notice - 3 runs, 3 trackers, policy advanced
  notice  consent/cookieless-ping  GA4 sent 2 cookieless pings before the choice (allowed under --consent-mode advanced)

Evidence, not legal advice.
full report: .echo/consent/2026-10-26T09-14-03-a1b2c3/report.html
Tip: --save baseline keeps this as the baseline for the next check.
```

Every result is an evidence folder in `.echo/consent/`: `consent.json`, screenshots, every event of every run, a manifest with the SHA-256 of each file and `report.html`. `rhythm echo consent verify <folder>` checks that nothing in it changed since.

| You want to… | Run |
|---|---|
| Add Global Privacy Control and a withdrawal | `rhythm echo consent --runs +gpc,+withdraw https://shop.example` |
| Look like a visitor from Germany | `rhythm echo consent --geo de https://shop.example` (language and time zone, never the IP address) |
| Judge Google's cookieless pings as hits | `--consent-mode basic` or `--consent-mode strict` |
| See which buttons Echo would click, without a verdict | `rhythm echo consent detect https://shop.example` |
| Click a banner Echo doesn't know | `--accept-selector '#yes' --reject-selector '#no'` |
| Fail CI only when something got worse | `--save baseline` once, then `--against baseline --fail-on broke` |
| Check many sites | `--sites sites.yaml`, the same file Soundcheck fleet reads |
| Sign the evidence | `--sign-key team.key`, then `rhythm echo consent verify <folder> --key team.pub` |
| Read the result in a script | `--json`: the `rhythm-echo/v1` envelope with `kind: consent` |

Echo knows Cookiebot, Complianz, CookieYes, OneTrust, Usercentrics, Didomi, iubenda and Borlabs Cookie. Other banners are found by their cookie words and buttons in 30 languages (`rhythm echo consent cmps` lists the rules). Trackers come from an MIT catalogue with fixtures: GA4, Google Ads, Meta Pixel, TikTok, Microsoft Clarity, LinkedIn, Hotjar and more (`rhythm echo consent trackers`).

In CI:

```yaml
- uses: RhythmShahriar/echo@v1.2.0
  with:
    mode: consent
    site: https://staging.shop.test/ https://staging.shop.test/shop/
    fail-on: error
    pr-comment: true
```

The job summary and the pull request comment show tracker names, counts, check titles and page paths, never query strings, cookie values or IDs. `upload: true` keeps the evidence folder as the `echo-consent` artifact; `sign: true` signs `consent.json` with keyless cosign.

### Honest limits


> **What a pass means.** In these runs, from this machine, with this browser and these settings, no tracker on Echo's list sent a request or set a cookie before the visitor chose or after Reject. It does not mean the site complies with the GDPR, ePrivacy, CCPA/CPRA or any other law. Evidence, not legal advice.
>
> **One visitor, one place.** Banners differ by country, language and device. Geo-targeted banners may not show to a CI runner in the US; Echo can set the browser's language and time zone (`--geo`) but not its IP address. Run it from the region that matters, or through a proxy there.
>
> **Blind spots.** Trackers that are not on the list; server-side tagging on your own domain when its requests don't look like GA4 or Meta; fingerprinting without network requests; tags that wait longer than the settle time or need more interaction than a scroll and one more page; sites that hide tags from automated browsers.
>
> **Consent Mode advanced.** Google says its tags in advanced mode "send measurements without cookies" while consent is denied. Whether that is acceptable before consent is disputed. Echo reports these pings as `consent/cookieless-ping` under the default policy `advanced`; `--consent-mode basic` or `strict` counts them as hits.
>
> **gcd is unofficial.** Google doesn't document the `gcd` parameter. Echo shows the raw value next to a reading based on community documentation and treats letters it doesn't know as unknown.
>
> **Pseudonymised, not anonymised.** Masked or hashed identifiers in reports can still be personal data. Treat reports like logs.
>
> **What leaves the machine.** Requests to the site and to the scripts it loads (which shows this machine's IP address to those servers). No tracking hits unless `--send-hits`. Nothing goes to Rhythm. The update check contacts GitHub (`RHYTHM_OFFLINE=1` turns it off).
>
> **Banners Echo can't handle** are reported as "couldn't check" (exit 3), never as a pass. Tested CMPs and the dates their fixtures were captured are in the benchmark.
>
> **A signature** proves who made the evidence file and that it hasn't changed. It doesn't prove the site behaves the same for every visitor, region or day. Keyless signing in the Action writes the repository and workflow name to the public Rekor log.

More in the wiki: [Echo-Consent](https://github.com/RhythmShahriar/rhythm/wiki/Echo-Consent), [checks](https://github.com/RhythmShahriar/rhythm/wiki/Echo-Consent-Checks), [Consent Mode](https://github.com/RhythmShahriar/rhythm/wiki/Echo-Consent-Mode), [banners (CMPs)](https://github.com/RhythmShahriar/rhythm/wiki/Echo-Consent-CMPs), [CI](https://github.com/RhythmShahriar/rhythm/wiki/Echo-Consent-CI) and [evidence](https://github.com/RhythmShahriar/rhythm/wiki/Echo-Consent-Evidence).

## Why Echo

- **Compares, not just catches.** Mail catchers show you an inbox. Echo tells you what changed in what your site sends, ranked, after every update.
- **Every channel.** Email, SMS, WhatsApp, chat, webhooks, analytics, push and payment calls, in one place.
- **No test code.** Actions are a few lines of YAML; Soundcheck drives real forms; replay sends signed webhooks.
- **Local and free.** One binary, your machine or your CI. Captured messages stay on your machine, with credentials masked before they're stored.

## CI

```yaml
- uses: RhythmShahriar/echo@v1
  with:
    mode: compare
    fail-on: broke
```

Or start Echo for Soundcheck: `mode: up`, then `rhythm soundcheck compare … --echo`. `mode: consent` checks a cookie banner ([above](#cookie-consent-checks)). More in the [CI guide](https://github.com/RhythmShahriar/rhythm/wiki/Echo-CI).

## Docs

| | |
|---|---|
| [Your first run](https://github.com/RhythmShahriar/rhythm/wiki/Echo-First-Run) | seal a site and check a change, in ten minutes |
| [Connectors](https://github.com/RhythmShahriar/rhythm/wiki/Echo-Install-Connectors) | WordPress, Laravel, Next.js, Node, anything |
| [Sealing staging](https://github.com/RhythmShahriar/rhythm/wiki/Echo-Sealing-Staging) | what's caught, what's faked, hard seals |
| [Actions and compare](https://github.com/RhythmShahriar/rhythm/wiki/Echo-Actions-And-Compare) | `.echo/config.yaml`, snapshots, classes |
| [With Soundcheck](https://github.com/RhythmShahriar/rhythm/wiki/Echo-With-Soundcheck) | forms and flows, with their messages |
| [CLI reference](https://github.com/RhythmShahriar/rhythm/wiki/Echo-CLI-Reference) | every command, flag and exit code |

All of it lives in the **[Rhythm documentation wiki](https://github.com/RhythmShahriar/rhythm/wiki)**.

## Contributing

```sh
git clone https://github.com/RhythmShahriar/echo && cd echo
make hooks   # Conventional Commits check
make check   # lint + tests
```

The connectors have their own tests: `make -C connectors/laravel check` (Docker), `cd connectors/node && npm test`. See [CONTRIBUTING](https://github.com/RhythmShahriar/.github/blob/master/CONTRIBUTING.md).

## Responsible use & disclaimer

Echo is built to help people **test their own sites safely**.

- **Use it only on sites you own or are authorized to test.** Replays and actions send real requests to the site you point them at.
- **Captured messages contain personal data.** They stay on your machine (or CI runner); credentials are masked, content isn't. Treat `.echo/` like a database dump and use test data on staging.
- **Observe mode really sends.** Seal is the default for a reason; use observe only where real delivery is safe.
- **No warranty.** Echo is provided "as is", as set out in the [license](LICENSE). A connector can miss a message sent in a way it doesn't hook; use a hard seal where a leak would matter.

Found a vulnerability in Echo itself? Please [report it privately](https://github.com/RhythmShahriar/echo/security/advisories/new).

## License

[MIT](LICENSE).

<p align="center"><sub>Part of <a href="https://github.com/RhythmShahriar/rhythm"><b>rhythm</b></a> · Useful ideas. Working systems.</sub></p>
