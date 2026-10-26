# Consent verdict: user-visible changes

Every change the verdict lane makes to what people see: issue titles and
details, run details, inconclusive texts, notes and the fields of the
result. Exact texts are in backticks.

## V1 Page summaries

- Each page's `consent_mode` is filled from the none run (else the first run with a Consent Mode command or a Google hit with gcs or gcd): `present`, `v2` (the default that applies to `--geo` sets both `ad_user_data` and `ad_personalization`), `default_at_ms`, `first_google_hit_ms`, `default_before_first_hit` (true when there is a default and no Google hit, or the default came at or before the first hit), `default`, `wait_for_update_ms` and `region` of that default. A region-scoped default for the `--geo` region wins over a global one.
- Google hits (GA4, Universal Analytics, Google Ads, Floodlight, Google Ads remarketing, Google CCM) sent with a known gcs that has `ad_storage` and `analytics_storage` denied get `cookieless: true` when no Google cookie (`_ga*`, `_gid`, `_gcl_*`, `FPID`, `IDE`) was stored in the same phase before the hit or within 1 s after it. A cookie the browser refused does not count.
- `needs_no_consent` is true when no run found a banner or made a choice, no CMP was detected and the none run had no consent-required hit, cookie, storage key or tracker script (`--exempt` trackers never count; a script of a tracker the catalog doesn't know counts as needing consent). On such a page the Accept, Reject and Withdraw runs that ended `no-banner` lose their reason and get the detail `No banner and no trackers: nothing to consent to`.

## V2 Tracker checks

- What counts: request and frame hits, tracker cookies the browser stored and tracker storage keys whose tracker needs consent. `--exempt` trackers (and `consent.exempt`) count as exempt and their entries get `need: exempt` in the result. In the none run everything counts as before the choice; in the accept run only the before phase counts; reject adds after-reject, withdraw adds after-withdraw; the gpc run is read only for `consent/gpc-ignored`.
- One issue per check, page, run and tracker. `evidence` lists the event numbers of the hits, cookies, storage keys and scripts behind it, at most 50.
- Titles:
  - `consent/before-choice`: `<Name> fired before the visitor chose`
  - `consent/after-reject`: `<Name> fired after Reject` (a hit whose known gcs grants ad_storage or analytics_storage is left to `consent/reject-granted`)
  - `consent/after-withdraw`: `<Name> fired after consent was withdrawn`
  - `consent/embed`: `<Name> loaded before the visitor chose`, or `<Name> loaded after Reject` after Reject or the withdraw Reject
  - `consent/script-before` (strict only, Google Tag Manager included): `<Name> script loaded before the visitor chose (strict policy)`, or `<Name> script loaded after Reject (strict policy)`
  - `consent/cookieless-ping` (advanced only; basic and strict count the pings as hits): `<Name> sent <n> cookieless pings before the choice (allowed under --consent-mode advanced)`, with `after Reject` in place of `before the choice` after Reject; `1 cookieless ping` for one
  - `consent/gpc-ignored` (advertising hits and cookies in the gpc run): `<Name> fired with Global Privacy Control on`
  - `consent/meta-no-consent-api` (once per page, in the first run with a Meta Pixel error and no fbq consent call before its first hit): `Meta Pixel never received fbq('consent', ...)`
- Details:
  - one hit: `<host without www.><path>?<two key params> at <s.ss>s`, e.g. `facebook.com/tr?id=••••5678&ev=PageView at 0.84s`; the key parameters are the ID and the event (`id`, `tid`, `sdkid`, `pid`, `ev`, `en`, `event`, `e`, `ec`, `ea`), else the first parameters that aren't versions, client IDs, cache busters or nested URLs
  - several hits: `<n> hits (<events> on <path>, <path>)`, e.g. `3 hits (PageView on /, /shop/, /)`; at most three event names and five paths (then `...`); `<n> hits (on /, /shop/)` when the hits name no event
  - then `; cookie <name> written` per tracker cookie and `; <area> storage key <key> written` per storage key; when there is no hit the first of these ends with ` at <s.ss>s`
  - scripts: `<host><path>?<params> at <s.ss>s`, or `<n> loads (<url>, <url>)`
  - cookieless pings: `<host><path>?gcs=<gcs> at <t1> and <t2>; no Google cookie written`, e.g. `region1.google-analytics.com/g/collect?gcs=G100 at 0.62s and 9.90s; no Google cookie written`
  - meta-no-consent-api: `no fbq('consent', 'revoke') or fbq('consent', 'grant') before the first Meta Pixel hit at <s.ss>s`
- Fix lines: the tracker's catalog fix for before-choice, after-reject, after-withdraw and embed (else the check's); the check's fix for the others.

## V3 Consent Mode checks

Only on pages with Google hits. These issues name no tracker: their key ends in `|`.

- `consent/no-default` (no Consent Mode default in any run and the first Google hit has no gcs): title `Google tags ran with no Consent Mode default`, detail `<Name> sent <host><path>?<params> at <s.ss>s with no gcs; no gtag('consent', 'default', ...) was seen in any run`.
- `consent/default-late` (the none run's default came after its first Google hit; never with no-default): title `The Consent Mode default came <n> ms after the first Google hit`, detail `first Google hit <Name> (<host><path>?<params>) at <s.ss>s; default at <s.ss>s`.
- `consent/v2-signals`: title `Consent Mode v2 signals ad_user_data and ad_personalization were never set`, detail `the default at <s.ss>s doesn't set ad_user_data and ad_personalization` (or one of them) and/or `gcd <raw> on <Name> at <s.ss>s shows <signals> unset (l)`, joined with `; `.
- `consent/accept-not-granted` (Accept confirmed, no update granted ad_storage or analytics_storage and every Google hit after the click has a known gcs with both denied; at least one such hit): title `After Accept, Google was never told storage is granted`, detail `<n> Google hits after Accept, the first at <s.ss>s, carried gcs <gcs>; no Consent Mode update granted ad_storage or analytics_storage`.
- `consent/reject-granted` (after the Reject click an update granted ad_storage or analytics_storage, or a Google hit's known gcs grants one): title `After Reject, Google was told <signal> is granted`, or `After Reject, Google was told ad_storage and analytics_storage are granted`; detail names each update (`gtag('consent', 'update') at 2.01s granted analytics_storage`, `dataLayer.push(['consent', 'update']) at ...`) and the hits (`<Name> hit with gcs G111 at 2.30s`, or `<n> Google hits with a granted gcs, the first <Name> with gcs <gcs> at <s.ss>s`).
- A gcs or gcd Echo can't read never raises reject-granted or accept-not-granted.
- The result note `gcd decoded with an unofficial, community-documented mapping.` is added once whenever a page with Google hits has a hit with a decoded gcd.

## V4 Choice checks and inconclusive runs

- `consent/reject-not-kept` (Reject confirmed, then a later page or the reload showed the banner, or the CMP state read after the reload has no choice or grants a category Reject turned off): title `After Reject, the banner came back on <path>`, or `After Reject, the CMP forgot the choice on <path>` when only the stored state is wrong; detail `the banner showed again on the next page (/blog/) at 9.00s` (`the reload` for the reload) and `after the reload <state source> has no choice` or `... grants marketing`, joined with `; `.
- `consent/reject-broken` (the button failed and the CMP's API reject worked): title `The Reject button doesn't work; only the CMP's API could reject`, detail `clicked "<text>" (<selector>) <n> times and the choice wasn't confirmed; <CMP>'s API reject worked`.
- `consent/no-first-layer-reject` (Reject found on layer 2 or deeper; not raised for a one-step `--reject-selector`): title `Reject isn't on the banner: Echo had to open settings`, detail `Reject was on layer <n>: Echo clicked "<text>" (<selector>)`.
- `consent/no-banner` (no run saw a banner and the page has before-choice issues; run none): title `<names> are on the page but no consent banner showed` (`is` for one tracker), detail PRD message 17 with the names and `--banner-wait`: `<names> are on the page but no consent banner showed within 10s. Many banners show only to visitors in some countries: try --geo de, or an EU --proxy. A banner Echo doesn't know: --accept-selector and --reject-selector`.
- `consent/click-fallback` (the choice or the withdraw Reject fell back to the API): title `Echo couldn't click the button and used <CMP>'s API, so the button itself wasn't tested` (`the CMP's API` when the CMP has no name), detail `<Run>: no <Accept|Reject> button was found with --click button,api`.
- `consent/tcf-mismatch` (a TCF CMP; the last TC string read after the choice): `The TCF string grants purposes 1 and 3 after Reject` or `The TCF string grants no purpose after Accept`; detail `TC string read at <s.ss>s (CMP <id>, <n> vendors with consent)`. An unreadable string adds the note `<Run> on <path>: the TCF string after the choice couldn't be read, so consent/tcf-mismatch wasn't checked`.
- `consent/server-side` (with `--echo`; one issue per provider): title `<Provider> received <events> from the server after Reject` (`after consent was withdrawn` in the withdraw run; `Meta Conversions API`, `GA4 Measurement Protocol`; `an event` when none is named), detail `<n> messages caught by Echo, the first at <s.ss>s (<ids>)`. The key's tracker part is the provider id (`meta-capi`, `ga4-mp`).
- `inconclusive` lists every run that still has a reason after the page summary, with these details:
  - `no_button`: `<Run>: <CMP>'s banner showed but Echo found no <Accept|Reject> on it or in its settings. Set --<accept|reject>-selector (rhythm echo consent detect shows what Echo sees), or --click button,api to use <CMP>'s API`
  - `click_missed`: `<Run>: clicked "<text>" (<selector>) <n> times and the banner stayed. Something may cover it (a chat bubble, a newsletter popup); --show-browser shows the page` (`once` for one click)
  - `state_unchanged`: `<Run>: clicked "<text>" and the banner closed but <CMP>'s stored state didn't change (<cookie> still has no choice). Hiding a banner is not rejecting` (`accepting` for Accept)
  - `timeout`: `<Run>: the run took longer than --timeout 90s; the last request still open was <url>` (the last request with no status that wasn't held or refused; the second half is left out when there is none)
  - `no_banner`: `<Run>: no banner showed within <banner-wait>` plus ` (<CMP>'s script loaded)` when the CMP is on the page
  - `cmp_not_found`: `<Run>: --cmp <name> was named but its banner wasn't found`
  - `navigation_failed`: the load error as browse recorded it, else `<Run>: the page didn't load`
  - `limit`: `stopped after 20000 requests` (the plan's request limit)
  - `canceled`: `interrupted`
  - Durations read as the flags take them: `10s`, `90s`, `2m`.

## V5 Levels, ignores and notes

- `checks:` levels apply to consent checks: `off` removes the check's issues, `error`, `warning` or `notice` replaces their level (and so what `--fail-on` and the exit code see).
- An `ignore:` entry hides an issue when its `check` matches and its `tracker`, `page` (a path, or a URL read as its path) and `run` match where set. The issue stays in `issues` in its place with `ignored` set to the entry and doesn't count in the summary or the exit code.
- An entry whose `until` date (UTC) has passed, or whose `until` isn't a `YYYY-MM-DD` date, hides nothing and adds one `consent/ignore-expired` notice per entry: title `The ignore for <check> on <tracker, or page, or every page> expired on <until>; the issue counts again`, detail `ignore: <check> until <until>, reason: <reason>`. The until day itself still applies.
- Issues are ordered by level (error, warning, notice), then page order, then run (none, accept, reject, gpc, withdraw), then tracker id.
- `notes` are, in this order: `Evidence, not legal advice.`; `Hits held in the browser.` or `Tracker hits were sent for real (--send-hits).`; `TLS not verified.` (with `-k`); `Chrome ran without its sandbox.` (with `--no-sandbox`); `Hosts mapped with --map-host.`; `--geo <cc> set the language and time zone, not the IP address.`; `gcd decoded with an unofficial, community-documented mapping.`; then any other note (the TCF note of V4, notes browse adds). Judging a result again doesn't repeat them.
