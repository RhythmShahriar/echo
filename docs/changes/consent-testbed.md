# Consent testbed: changes

Every user-visible change of the consent testbed (commands, flags, output
lines, messages), with the exact text.

## X1 Fixture server

- New command `go run ./testbed/consent/server/cmd/consent-testbed` with flags `-root` (default `testbed/consent`, or `.` inside it), `-listen` (default `127.0.0.1:23400`), `-admin` (default `127.0.0.1:23401`), `-tls`, `-ca` (default `<root>/ca`), `-make-ca <dir>`, `-print-map`, `-echo-proxy`, `-echo-ca`.
- `-tls` serves https beside plain http on the same `-listen` port: each connection is told apart by its first byte, because Chrome's host resolver rules and Echo's `--map-host` send every name and port to one address.
- Start lines: `consent-testbed: sites and tags on 127.0.0.1:23400 (http and https), admin and sink on 127.0.0.1:23401` (`(http)` without `-tls`) and `consent-testbed: map with <the -print-map line>`.
- `-print-map` prints one line: `--map-host '*.shop.test=127.0.0.1:23400' --map-host analytics.tiktok.com=127.0.0.1:23400 ... --map-only` (every stand-in host, sorted).
- `-make-ca <dir>` prints `consent-testbed: wrote the testbed CA to <dir>`; on an existing CA it fails with `consent-testbed: <dir>/ca.pem exists; delete the folder to make a new CA`. The CA is name-constrained to `.test`, `localhost` and the stand-in hosts.
- `-tls` without a CA: `consent-testbed: testbed CA: open <dir>/ca.pem: no such file or directory (make ca creates it)`.
- A root without `sites/`: `consent testbed: no sites folder in <root> (pass -root testbed/consent)`.
- `-echo-proxy` must be `http://` on a loopback address or a one-label Compose name: `-echo-proxy "<value>": want http://host:port of rhythm-echo up` and `-echo-proxy "<value>": want a loopback address or a Compose service name`.
- A host the server doesn't serve answers `421` with `not a consent testbed host: <host>` and is counted under `unmapped`.
- `/logout/` and `/cart/` on any scenario answer `This page must never be opened by a scan.` and are counted under `forbidden`.
- Admin endpoints (admin port only): `GET /_admin/health` (`ok`), `GET /_admin/state?id=c01[&state=good|broken]` (`{"id": "c01", "state": "broken"}`; unknown id 404 `no scenario "x"`; bad state 400 `state: want good or broken`), `GET /_admin/sites`, `GET /_sink/count[?scenario=c01]` (`{"hits", "by", "forbidden", "internal", "unmapped"}`), `GET /_sink/hits?scenario=c01`, `POST /_sink/reset` (204), `/_internal/*` (the ssrf-probe target, counted under `internal`).
- `testbed/consent/toggle.sh <id|all> good|broken` prints `c01 -> broken`; `toggle.sh status` prints `all scenarios good` or `broken: c01 c02`; an unknown id fails with `./toggle.sh: unknown scenario x (see sites/)`.
- `make -C testbed/consent ca`, `serve`, `map` and `toggle ID=c01 STATE=broken`.

## X2 Stand-in tags

- The server serves stand-ins (no vendor code) at the real URLs: `www.googletagmanager.com/gtag/js` and `/gtm.js` (one Google stand-in), `connect.facebook.net/*/fbevents.js`, `analytics.tiktok.com/i18n/pixel/events.js`, `snap.licdn.com/li.lms-analytics/insight.min.js`, `bat.bing.com/bat.js`, `www.clarity.ms/tag/*`, `static.hotjar.com/c/*`, `plausible.io/js/*`, `www.google.com/recaptcha/api.js`, `fonts.googleapis.com/css`, `www.youtube.com/embed/*` (sets `VISITOR_INFO1_LIVE` and `YSC`, reports `/api/stats/qoe`) and `www.youtube-nocookie.com/embed/*` (nothing). On every scenario host: `/~partytown/loader.js` and `/~partytown/gtm-worker.js` (the Google stand-in in a Web Worker, sending with fetch).
- Hits the stand-ins send: `www.google-analytics.com/g/collect` (or the `transport_url` / `server_container_url` of the config), `googleads.g.doubleclick.net/pagead/viewthroughconversion/<id>/`, `www.facebook.com/tr`, `analytics.tiktok.com/api/v2/pixel`, `px.ads.linkedin.com/collect/`, `bat.bing.com/action/0`, `www.clarity.ms/collect`, `in.hotjar.com/api/v2/client/sites/<id>/visit-data`, `plausible.io/api/event`, `www.youtube.com/api/stats/qoe`. Each carries `_tbrun=<scenario>`.
- Google stand-in options on its script tag: `data-mode="advanced"` (cookieless pings while denied; default basic), `data-region` (default `DE`). `window.__tb_gtm = {ga4, ads}` is the GTM container. Without any Consent Mode command it sends no `gcs` and `gcd=13l3l3l3l5`.
- Fake IDs: `G-TESTBED001`, `GTM-TESTBED1`, `AW-100000001`, pixel `100000000000001`, TikTok `TESTBEDTT0001`, LinkedIn `1000001`, UET `100000001`, Clarity `tbclarity1`, Hotjar `1000001`.
- Every stand-in sets `window.__tb_loaded[<name>] = true` (`gtag`, `gtm`, `fbevents`, `tiktok`, `linkedin`, `uet`, `clarity`, `hotjar`, `plausible`, `recaptcha`, `youtube`, `youtube-nocookie`, `partytown`).
- `/_sink/count` `unmapped` now counts per scenario (by Referer); Chrome's own requests fall under scenario `unknown`.

## X3 CMP replicas and real open-source CMPs

- Replicas served at the vendors' URLs: `consent.cookiebot.com/uc.js` (Cookiebot), `cdn.cookielaw.org/scripttemplates/otSDKStub.js` (OneTrust), `app.usercentrics.eu/browser-ui/latest/loader.js` (Usercentrics, `data-tb-shadow="closed"` for a closed root), `sdk.privacy-center.org/<key>/loader.js` (Didomi, TCF v2.2 CMP ID 7), `cdn.iubenda.com/cs/iubenda_cs.js` (Iubenda), `cdn-cookieyes.com/client_data/<id>/script.js` (CookieYes); on every scenario host `/wp-content/plugins/complianz-gdpr/...` (Complianz), `/wp-content/plugins/borlabs-cookie/...` (Borlabs 2, or 3 with `data-borlabs-version="3"`) and `/_tb/banner.js` (generic banner, `data-lang="en|de|ar"`, `data-newsletter`).
- Stored state each writes: `CookieConsent`, `OptanonConsent` plus `OptanonAlertBoxClosed`, localStorage `uc_settings` plus `uc_user_interaction`, `didomi_token` plus `euconsent-v2`, `_iub_cs-<cookiePolicyId>`, `cookieyes-consent`, `cmplz_*` plus `cmplz_banner-status`, `borlabs-cookie`, `site_consent` (generic).
- Testbed switches on a replica's script tag: `data-tb-delay`, `data-tb-hide-bots`, `data-tb-cookie-path`, `data-tb-no-gcm`, `data-tb-gcm-v1`, `data-tb-no-first-layer-reject` and `data-tb-reject-granted` (OneTrust), `data-tb-no-accept-update` and `data-tb-tcf-reject-grants` (Didomi), `data-tb-gpc-accepts` (CookieYes), `data-tb-late-reject` and `data-tb-hide-only` (generic).
- Vendored as released: Klaro 0.7.22 (BSD-3-Clause) at `/vendor/klaro/klaro.js` and orestbida cookieconsent 3.1.0 (MIT) at `/vendor/cookieconsent/`, with their LICENSE files and `cmps/vendor/VERSIONS` (name, version, file, SHA-256).
- Test failure texts: `the <name> replica was captured on <date>, <n> days ago: refresh it from the vendor's current markup and update SOURCE.md` and `<name> <version>: <file> has SHA-256 <got>, VERSIONS says <want>`.

## X4 Scenario sites and manifest

- `testbed/consent/scenarios.yaml`: 25 bad (`c01` to `c25`) and 15 good (`g01` to `g15`) scenarios as PRD 18.1 lists them, plus `fixtures:` for the 13 security fixture sites (`ssrf-probe`, `hostile-banner`, `hostile-text`, `patched-dom`, `sgtm-first-party`, `pii-beacons`, `secret-url`, `huge-datalayer`, `request-storm`, `redirect-chain`, `redirect-loop`, `self-signed`, `slow-banner`). Fields as the lane spec plus `any: true` (one expect entry is enough: c20, c21).
- Each site is `testbed/consent/sites/<id>/site.html`, a template rendered into `sites/_layout.html` with the partials in `sites/_partials/` (tags and CMP snippets; templates may call `dict`). Every page has `/`, `/page-2/` (and any folder-style path) plus links to `/cart/` and `/logout/`.
- The layout's own consent glue: `window.tbConsent` and `tbOnConsent(cb)`, fed by the `tb-consent` window event every replica fires when it reports a choice.
- Expectations that go beyond the PRD table: c11 runs with `--click button,api` on a closed Usercentrics shadow root and expects `consent/click-fallback` too; c16 also expects `consent/cookieless-ping` (its tag pings in advanced mode); c20 runs with `--click button,api` and accepts any of a `click-failed` Reject, `consent/reject-broken`, `consent/click-fallback` or a confirmed Reject; c17 runs with `-A "... HeadlessChrome/156.0.8078.47 ..."`; c18 with `--echo {echo}`.
- Go package `testbed/consent/scenarios` loads the manifest; unknown keys and bad values fail with the file named, e.g. `scenarios.yaml: c01: area "nope": want one of consent-mode, trackers, cmp, choice, server, environment` and `scenarios.yaml: g01: a good scenario lists what it allows (allow: [] for nothing)`.
