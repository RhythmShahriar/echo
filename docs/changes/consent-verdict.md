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
