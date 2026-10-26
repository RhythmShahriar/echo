# Consent: browser, recorder, egress guard and run steps (lane browse)

What this lane changed that a user, a writer or another lane can see,
with the exact texts.

## B1 Browser launch for consent

- Chrome gets these switches, in this order, after Echo's own:
  `--proxy-server=http://127.0.0.1:<refuse-all port>`,
  `--proxy-bypass-list=<-loopback>`,
  `--force-webrtc-ip-handling-policy=disable_non_proxied_udp`,
  `--disable-quic`, `--no-first-run`, `--disable-background-networking`,
  `--disable-component-update`, `--disable-sync`,
  `--disable-features=OptimizationHints,MediaRouter,Translate`,
  `--lang=<locale>`. DevTools stays on 127.0.0.1 with
  `--remote-debugging-port=0`.
- rod's default `--disable-site-isolation-trials` is no longer passed
  (internal/browser, every launch): cross-site frames get their own
  process as in a visitor's Chrome.
- Fatal codes: no Chrome found with downloads off, a `--browser` path
  that doesn't exist or a failed Chrome for Testing download give
  `no-browser` (message 9) with the masked error as Detail; root without
  `--no-sandbox` gives `sandbox-root` (message 10); with
  `RHYTHM_ECHO_IMAGE=1` and no installed browser (no `--browser`) the
  check gives `no-browser-image` (message 16) and never downloads.
- Any other start failure is a plain error:
  `consent: starting Chrome: <masked error>`.
- The plan's log writer gets `Browser: Chrome <version> (<path>)` (or
  `Browser: Chrome for Testing <version> (downloaded)`) and, without the
  sandbox, `WARNING: Chrome is running without its sandbox. A page that
  exploits a browser bug could take over this machine; only check sites
  you trust.` Control characters in these lines are escaped.
- Locale: `--locale`, else the `--geo` country's, else the machine's
  (`LC_ALL`, `LC_MESSAGES`, `LANG`; `de_DE.UTF-8` reads as `de-DE`, `C`
  and `POSIX` as `en-US`), else `en-US`. A value not shaped like a
  language tag is `en-US`.

## B2 Egress proxy

- Refusal reasons as `request.refused` spells them: `private address`,
  `browser traffic`, `not mapped`, `mapped host outside the tab`,
  `limit`.
- A host `--map-host` covers is never dialed at its real address: a
  request for it that reaches a guard (one Chrome sends outside the
  tab's interception, such as a connection opened ahead of a service
  worker) is refused with `mapped host outside the tab` and counted in
  `network.refused`, with or without `--map-only`. A frameless request
  the tab does see for a mapped host is answered from the mapped
  address; with `--map-only` an unmapped one is failed in the browser.
  Both still count in `network.browser_internal`.
- A refused request gets `403` with the body
  `refused by rhythm-echo consent: <reason>` and the header
  `X-Rhythm-Echo-Refused: <reason>`. The 257th open tunnel on one
  listener gets `503` `too many open tunnels`. Idle tunnels close after
  30 s.
- A bad `--proxy`: `--proxy <masked url>: want http://host:port` (http
  proxies only; the login is always masked).
- Browser service hosts (`clients2.google.com`, `update.googleapis.com`,
  `redirector.gvt1.com`, `android.clients.google.com`,
  `safebrowsing.googleapis.com`, `optimizationguide-pa.googleapis.com`,
  `content-autofill.googleapis.com`) are refused on every listener and
  counted in `network.browser_internal`, not in `network.refused`
  (PRD 13.7; the lane spec listed them under Refused).
  `169.254.169.254`, `fd00:ec2::254` and `metadata.google.internal` are
  refused for every target and counted in `network.refused`, also when
  a name resolves to one of them (checked on every resolved address).

## B3 Contexts, emulation and click hardening

- User agent: the browser's own with `HeadlessChrome` made `Chrome` plus
  ` rhythm-echo-consent/<version>` (`dev` when the build has none);
  mobile replaces the platform with `(Linux; Android 10; K)` and
  `Safari/` with `Mobile Safari/`. `-A` is used as given, with no suffix.
  Client hints name `Chromium` and `Not_A Brand` only.
- `Accept-Language`: `<locale>,<language>;q=0.9` (`de-DE,de;q=0.9`).
- Viewports: `desktop` 1366x900 at 1x; `mobile` 390x844 at 3x, touch
  with 5 points, `navigator.platform` `Linux armv8l`.
- Denied in every run's context: downloads (reported) and the
  permissions geolocation, notifications, clipboard-read,
  clipboard-write, midi, camera, microphone, idle-detection,
  storage-access, top-level-storage-access, window-management,
  persistent-storage, background-sync and payment-handler.
- Events: a dialog is dismissed and recorded as `dialog` with the
  message (safe text, 200 characters); a popup is recorded as `popup`
  with its masked URL and closed (every request it makes is refused);
  a download as `download` with its masked URL; a file chooser as the
  note `the page opened a file chooser; Echo canceled it`.
- `--cookie name=value` becomes a host-only cookie for the target
  (Secure when the target is https); anything after `;` is ignored.
- `--geo` knows the 27 EU states, GB, NO, IS, LI, CH, US, CA, BR, AU,
  JP and IN (`UK` and `EL` read as GB and GR). Any other country is a
  usage error before the check (exit 2): `rhythm echo: --geo "zz": Echo
  has no language and time zone for that country; use --locale and
  --timezone, or an EU or EEA country, GB, CH, US, CA, BR, AU, JP or IN`
  (`consent: geo "zz": ...` from the config). `--locale` and
  `--timezone` win.
- The gpc run sends `Sec-GPC: 1` with every request and defines
  `navigator.globalPrivacyControl` as `true` before page scripts; its
  events are all in phase `gpc`.

## B4 Recorder

- `request.type`: document, script, image, xhr, fetch, beacon
  (sendBeacon and `<a ping>`), stylesheet, font, media, other.
- A held hit is answered `204` (with CORS headers for the page's
  Origin) or, for an image, `200 image/gif` with a transparent 1x1 GIF
  of 43 bytes; `status` is what the browser got. A CORS preflight to a
  hit endpoint is answered and not classified.
- `request.frame` is the origin of the requesting frame, or of the
  worker for a worker's request (`worker: true`, dedicated workers
  included).
- Past `Limits.Requests` the run stops recording; later requests are
  refused in the browser and counted in `dropped` with `truncated: true`.
- `body_sha256` and `body_bytes` cover the whole body; the catalog
  parses the first 64 KiB and `body_partial` says when it got less.
- A request a guard refused carries the guard's reason in `refused`.
- Started early from B5 (needed by the B2 and B4 tests): `--map-host`
  answers matching requests from the mapped address (`mapped: true`,
  Set-Cookie passed on, 15 s, 32 MiB, no redirects followed) and
  `--map-only` refuses unmapped hosts in the browser (`refused: not
  mapped`). B5's own tests come with that item.

## B5 --map-host and --map-only

- `network.refused` now also counts the requests a run refused in the
  browser with `--map-only` (`refused: not mapped`); before, only the
  guards' refusals were counted.
- `Engine.Mapped()` (for `Result.Mapped`, which every writer prints as
  `hosts mapped`) is true when `--map-host` maps any host.
- A mapped response's `Set-Cookie` lands as a cookie of the original
  host and is attributed to it (`set_by: response:<host>`, B7).
- A mapped hit endpoint in held mode is held, never fetched: the mapped
  server sees nothing. An `https://` URL of a mapped host is answered by
  Echo from a plain-http address, so no TLS is involved.

## B6 hooks.js and the binding

- Every page and frame target gets the binding `__rhythmEchoConsent` and
  hooks.js in the main world, before page scripts, on every document it
  loads. Pages can see both (and `window.__rhythmEchoHooks`), as with any
  DevTools-driven browser; the Runtime domain is on for page and frame
  targets because Chrome only exposes a binding to later documents then.
- Signals recorded (`kind: signal`): Consent Mode commands from `gtag`
  and `dataLayer.push`, `fbq` and `uetq` calls (as the signal decoder
  reads them); TCF `tcloaded`, `cmpuishown` and `useractioncomplete`
  (`source: tcf`, the TC string decoded into `tcf` when it reads); GPP
  events (`source: gpp`, the event name as safe text of at most 64
  characters, the string decoded into `gpp` when it reads).
- A record Echo can't read (broken JSON, over 4 KiB, a decoder error) is
  the note `unreadable hook record`.
- At most `Limits.HookRecords` (2,000) records and `Limits.HookBytes`
  (2 MiB) per run; hooks.js stops sending at the same limits in each
  document. Records past them are counted in `dropped` and set
  `truncated: true`.
- A record's own time is kept between 10 s before it arrived (never
  before the first navigation) and its arrival, so a page can't move a
  consent update far in time.
- A `dataLayer` the page declares with `const`, or one whose `push`
  can't be watched, is found by a poll every 100 ms for the first 30 s of
  each document; what it held then carries the poll's time (up to 100 ms
  late).
- The page's own `push` replacements (GTM) run as built and each call is
  reported once; `gtag`'s own dataLayer push is reported once, as gtag.

## B7 Cookies, Set-Cookie and storage

- `Run.Snapshot(ctx)` (called by the run after every step, B9) records
  each cookie that is new or changed since the last snapshot (HttpOnly
  included) and each new storage key or one whose value length changed.
- Cookie events: `set_by` is `response:<host>` when a response set it
  since the last snapshot (then `at_ms` is the response's time), else
  `script` (`at_ms` the snapshot's time). `expires` is 0 for a session
  cookie. Names are safe text, at most 256 characters. Values are never
  kept, only `length`.
- A cookie Chrome refused to store is recorded at once with `blocked`
  set to Chrome's reasons, sorted and comma-joined (for example
  `SameSiteNoneInsecure,ThirdPartyPhaseout`), once per name, domain and
  reasons.
- Storage events: `area` `local`, `session` or `indexeddb` (database
  names, `length` 0); `origin` per frame origin; keys are safe text of at
  most 256 characters; at most 500 keys per origin per snapshot; frames
  with an opaque origin are skipped.

## B8 consent.Tab

- `Run.Tab(page)` is the run's `consent.Tab`; `Run.Settle(ctx)` waits
  until no request started or ended for `--settle`, at most
  max(10s, 5 x `--settle`).
- `EvalIsolated` runs a function expression with its arguments in Echo's
  isolated world `rhythm-echo` of the top frame, or of the frame given as
  the last argument (`consent.Frame` or `*consent.Frame`); `EvalMain`
  evaluates an expression in the top frame's main world. Errors:
  `script result larger than 256 KiB`, `script failed: <message>` (safe
  text, 200 characters), `the frame is gone`.
- `Click` details when nothing is clicked: `closed shadow root`,
  `no element matches the shadow host`, `no element matches`,
  `bad selector`, `no frame matches <frame>`, `not visible`,
  `has no size`, `outside the viewport`, `covered by another element`
  (with `Covered` set). `Text` is safe text of at most 120 characters;
  `Box` is in top-frame CSS pixels. A click waits 1 s after the press:
  `NavigatedAway` is set when the top frame went to, or started loading,
  another host.
- Screenshots: `evidence/page-<n>/<run>-<name>.png` (0600, folders
  0700); the name keeps lower-case letters, digits and dashes (others
  become `-`, at most 40). None with `--no-screenshots`, none past 4 per
  run.
- `Reload` waits for the top frame's load event, then settles.
- `Frames` lists the top frame first; `CrossOrigin` is set for a frame
  whose origin differs from the top frame's.

## B4 follow-up from the trackers lane

- The catalog now gets each request's raw URL (cut at 8 KiB); the record
  keeps the masked one. A hit the body proves (a beacon whose need isn't
  `no`) is held even when its URL alone isn't a hit endpoint.

## B9 Run sequence, pool, retries and detect

- `browse.Runner` registers itself as the consent Runner. One Chrome per
  command; `--parallel` workers (one with `--show-browser`) take every
  page and run kind, pages in target order, runs in `--runs` order.
- Steps of a run (PRD 13.6): load the start URL and settle; snapshot;
  wait for the banner up to `--banner-wait`; screenshot `banner`; the
  choice; screenshot `after`; scroll; the same-site pages; reload the
  start URL; for Accept, Reject and Withdraw the CMP's stored state
  (`final_state`). Cookies and storage are snapshot after every step.
- Visits: `load` (the URL the start page ended on, `banner` from the
  banner check), one `page` per link followed, `reload` (the start URL).
  When `--geo` is set and the CMP found offers a geolocation override,
  the start page is loaded again with it (a second `load` visit) and the
  result notes `<CMP name> geolocation override <param>=<value>
  (unverified)`. With `--cmp <rule>` the parameter goes on the first
  load. This needs the cmp lane's optional `GeoParam(cmpID, country)`;
  without it nothing changes.
- `pages[].redirects` lists the start URL's redirect hops after the
  start URL, masked; `final_url` is the last one.
- A start page that can't be checked is a fatal error of its runs:
  - more than 10 redirects, or one URL a third time (a URL seen twice,
    as a cookie check does, is fine): `redirect-loop`, detail
    `a redirect loop, last to <url>` or `more than 10 redirects, last to
    <url>`. The load is stopped as soon as the loop shows.
  - Chrome's `ERR_CERT_*`, `ERR_SSL_*` or `ERR_BAD_SSL*` error: `tls`,
    detail Chrome's error text, e.g. `net::ERR_CERT_AUTHORITY_INVALID`.
  - any other load error: `unreachable`, detail the egress guard's own
    dial error (`dial tcp 127.0.0.1:443: connect: connection refused`)
    when it has one, else Chrome's error text. A document the guard
    answered 502 because it couldn't connect is `unreachable` too.
  - an end on another host (kit `httpx.SameSite`: http to https on the
    same host is fine) without `--follow-host`: `redirect-host` with the
    masked end URL in `To`.
  - a bot check: `bot-wall` with detail `Cloudflare` (`cf-mitigated:
    challenge`, or the title `Just a moment...` or `Attention Required! |
    Cloudflare`), `DataDome` (an `x-datadome` header, or a
    `captcha-delivery.com` frame), `PerimeterX` (the title `Access to
    this page has been denied`, `captcha.px-cdn.net`, `*.perimeterx.net`,
    `*.px-cloud.net`) or `Akamai` (`Server: AkamaiGHost` with the title
    `Access Denied`, or `/_sec/cp_challenge/`). Headers and script hosts
    count only when the page answered 403, 429 or 503; a title counts
    always.
  - the document answered 401 or 403: `http-status` with the status.
- When every run of a page hit one, `Run` returns the first run's error
  with the result. Otherwise each such run is `load-failed`, reason
  `navigation_failed`, with the detail
  `<url> didn't answer: <detail>`, `<url> answered 401 Unauthorized`,
  `<url> showed a bot check (Cloudflare) instead of the page`,
  `<url> redirects to <url>`,
  `<url> uses a certificate this machine doesn't trust (<detail>)` or
  `<url>: a redirect loop, last to <url>`. These runs are not retried:
  they describe the site, not a flaky run.
- A run that fails to open its context or load for another reason is
  `load-failed`, `navigation_failed`, detail the masked error.
- Statuses from the CMP driver's reason: `no_button` gives `no-button`,
  `click_missed` and `state_unchanged` give `click-failed`,
  `navigation_failed` gives `navigated-away`, `limit` gives
  `limit-reached`, `canceled` gives `canceled`. A `Choose` error other
  than the context's reads as `click_missed` with the masked error as
  detail. A withdraw run whose `Reopen` fails is `no-button`,
  `no_button`, with the driver's message as detail. `navigated-away`
  and `limit-reached` end the run at once (no scroll, pages or reload).
- The choice's time is the phase boundary when the choice was made, or
  a click happened (`clicks` above 0), or the API was used.
- No banner: Accept, Reject and Withdraw runs are `no-banner` with
  `no_banner` (`cmp_not_found` when `--cmp` names a rule), make no
  choice and still scroll, visit pages and reload. No-choice and GPC
  runs stay `ok` with no reason. When the banner check itself fails the
  run goes on as without a banner, detail
  `finding the banner failed: <masked error>`.
- No-choice and GPC runs wait until the Consent Mode default's
  `wait_for_update` plus `--settle` has passed since that default (from
  the hook signals; the region from `--geo`).
- Scroll: three mouse-wheel steps of max(100, (page height - viewport
  height) / 3 + 1) CSS pixels at the viewport's center, the mouse moving
  12 px between them, 150 ms apart; then settle.
- Pages: up to `--pages` (at most `Limits.Pages`) links of the page as
  it is after the scroll, in DOM order: http or https, a page rather
  than a file (kit crawl, so pdf, zip, doc, docx, xls and xlsx too), the
  same host as the start page's final URL, not that page itself, no
  repeats (kit `crawl.Normalize`). Skipped: `mailto:`, `tel:` and
  `javascript:` links and any whose path, query or text has the word
  logout, signout, login, cart, basket, checkout or account (also as
  `my...`), or the words log out, sign out or wp admin (so `/log-out`,
  `/wp-login.php`, `/wp-admin/`, `/my-account/`, `?add-to-cart=1` and a
  link reading "Log out"). `/cartography` is not skipped.
- Timeout: each run is bounded by `--timeout` from opening its context;
  then `timeout`, reason `timeout`, detail
  `the last request still open was <masked url>` (the newest request
  without an answer; held and refused ones never count), none when no
  request was open.
- Request limit: `limit-reached`, reason `limit`.
- Retries: an inconclusive run (any reason but `canceled`) is run again
  in a fresh context up to `--retries` times; `attempts` counts. Never
  when it recorded a hit, cookie or storage key of a consent-needing
  tracker before the choice or after Reject; then the first attempt is
  kept. Otherwise the last attempt is kept.
- Ctrl-C: every worker stops, runs not finished are `canceled`,
  `canceled`; Chrome closes, its profile is removed and the partial
  result comes back with the context's error.
- Result fields filled by browse: `started`, `duration_ms`, `echo`
  (Plan.Version), `browser`, `user_agent` (desktop or mobile as
  `--viewport`, without a run's GPC), `os` (`linux/amd64`), `sandbox`,
  `tls_verified` (not `-k`), `held`, `mapped`, `geo`, `locale`,
  `timezone` (as resolved), `viewport` (`desktop` when empty),
  `catalog`, `cmp_rules`, `network` (`requests` and `held` summed over
  every attempt; `refused` and `browser_internal` from the guards) and
  the notes above. `issues`, `inconclusive` and `notes` are `[]` rather
  than null.
- `consent detect`: one no-choice context, the same start-page checks
  (a fatal error comes back as it is), the banner, `Find` for Accept and
  Reject without a click, then one viewport screenshot saved as
  `Plan.Dir/<Plan.ID>.png` (0600); `screenshot` is that file name
  relative to `Plan.Dir`. None with `--no-screenshots`. `url` is the
  masked target. Bounded by `--timeout` (90s when unset).
- B3 fix: `Accept-Language` went out as `de-DE,de;q=0.9;q=0.9` (Chrome
  adds the weight itself); it is now `de-DE,de;q=0.9`.
- internal/browser: `Browser.NoSandbox` says whether Chrome runs
  without its sandbox. `Close` asks Chrome to close on a context of its
  own (2 s), so a canceled command still closes it before the process
  group is killed and the profile removed.
- Egress guard: `Proxy.DialError(host, port)` keeps why the last
  connection to a host failed (masked, at most 4,096 hosts).
- Recorder: a request still in flight when a run closes (a mapped
  request waiting on its server) is recorded as it stands; a mapped
  fetch ends when the run closes instead of after 15 s.

## B10 Event files and HAR

- Each run streams its events to `events/page-<n>-<run>.ndjson` in the
  evidence folder (folders 0700, the file 0600), one JSON object per
  line, as they happen, from the recorder's writer goroutine. When the
  end of the run changed an event's phase or visit (a click whose time
  came late), the file is written again from the final events, so it
  always matches the run. A retry starts the file again. `events_file`
  is the relative path.
- Every event in that file names its run (`"run": "reject"`) beside its
  phase.
- With `--har`, `har/page-<n>-<run>.har` (0600): HAR 1.2,
  `creator` `{"name": "rhythm-echo", "version": "<Echo's version or
  dev>"}`, one entry per recorded request in event order:
  `startedDateTime` (the run's first navigation plus `at_ms`), method,
  masked URL, `queryString` from the masked URL, request headers (Cookie,
  Authorization and Proxy-Authorization dropped; Referer, Location,
  Origin and Content-Location masked as URLs; the rest through kit
  `secrets.RedactHeader`; values masked whole, then cut at 8 KiB), status, status text,
  `httpVersion` (Chrome's protocol upper-cased, e.g. `H2`,
  `HTTP/1.1`), response headers with each Set-Cookie as its cookie name
  only (also for responses a `--map-host` address answered), content
  size and MIME type, `redirectURL` (masked), timings from Chrome
  (blocked, dns, connect, ssl, send, wait, receive in ms, -1 when a
  phase didn't happen, to the microsecond), `_type`, `_held`, `_mapped`
  and `_refused`. No request or response body, ever; `bodySize` is the
  request body's length. `har_file` is the relative path.
- An events or HAR file that can't be written doesn't stop the runs;
  at the end `Run` returns `*consent.FatalError{Code: write, URL: <the
  file's relative path>, Detail: <masked error>}` with the result (after
  a page's fatal error or the context's).

## B11 --echo

- With `--echo`, each run (each attempt) opens an action in Echo:
  `BeginLease(<Plan.ID>, page-<n>-<run>)`, e.g. `page-1-reject`. Every
  same-site request of the run (kit `httpx.SameSite` with the target)
  carries `X-Echo-Run: <Plan.ID>/page-<n>-<run>`, replacing one the page
  set; it goes to `--map-host` addresses too and never to a third party.
- After the run's reload settles the action ends (`EndLease`) and the
  run lists Echo's analytics messages for that run and action caught
  since the attempt began into `server`: `id`, `provider` (`ga4-mp` for
  Echo's `ga4`, `meta-capi` as it is), `event`, `at_ms` (the message's
  time minus the run's first navigation) and `phase` (from the run's
  choices).
- Each call to Echo waits at most 5 s. When Echo doesn't answer (or
  answers with an error) the result gets the note
  `Echo at <masked url> didn't answer; server-side tracking wasn't
  checked` once, Echo isn't asked again in that command, no run sends
  `X-Echo-Run` after that and the runs go on.
- A canceled run ends its action and asks for no messages.

## Integration fixes

- A CMP state read from a cookie (`source: cookie:<name>` in `final_state`
  and a choice's `state`) is now the cookie the top frame's page is
  sent: its domain, path and secure flag must match that page; of several
  the longest path wins. A choice stored under another path
  (Borlabs writing `Path=/blog`, read on `/`) or on another host is no
  choice, so `consent/reject-not-kept` reports it (`after the reload the
  CMP's state has no choice`) where the run used to read it as kept.

## Follow-ups for other lanes

- cli: leave `Plan.Locale` and `Plan.Timezone` empty when the flags are
  not given; browse resolves `--geo`, then the machine. Validate `--geo`
  against `browse.GeoCountries()`.
- testbed: a service worker can't register from a `--map-host` https
  host: Chrome fetches its script outside any tab's interception, so the
  request reaches the guard, which can't answer a mapped host over TLS.
  Serve service-worker fixtures from a 127.0.0.1 or localhost target
  (the B4 test does).
- cmp: `Tab.EvalIsolated` takes the function expression and its
  arguments; a `consent.Frame` (or `*consent.Frame`) given last picks the
  frame. `Tab.Click` returns a nil error with `Clicked` false and one of
  the details above when the element can't be clicked; an error means
  the browser failed. A closed shadow root reads as `closed shadow root`
  (c11 keeps working).
- signals: browse handles the tcf and gpp hook records itself
  (`Decoder.TCF`, `Decoder.GPP`); `Decoder.Hook` only gets the other
  sources.
- browse (B9, next batch): call `Run.Snapshot` after each step, use
  `Run.Tab(page)` for the CMP driver and `Run.Settle`, fill
  `Result.Mapped` from `Engine.Mapped()` and `Result.Network` from
  `Engine.Network()` after the runs close.

## Follow-ups for other lanes (batch 3, B9 to B11)

- cli: `Runner.Run` can return `*consent.FatalError` with code `write`
  (an events or HAR file couldn't be written; `URL` is the file's path
  relative to the evidence folder) together with a result. PRD 10.1
  makes that exit 3 ("a report couldn't be written"); PRD 10.2 has no
  message for it yet.
- cli: `Plan.Echo` must be set only with `--echo`; browse uses
  `Plan.ID` as Echo's run and `page-<n>-<run>` as the action.
- cmp: browse type-asserts `interface{ GeoParam(cmpID, country string)
  (string, string) }` on the CMP driver (country lower case, e.g. `de`);
  the note's name is the rule's `Name`. `State` is called only when
  `CMPInfo.Found` or `Present`. `Choice.AtMS` becomes the phase boundary
  when the reason is none, `Clicks` is above 0 or `How` is `api`; set it
  for every click.
- verdict: a timeout run's `detail` is already
  `the last request still open was <url>`; the verdict builds message 21
  from the events, which agrees. `load-failed` runs of a page that
  couldn't be checked carry the detail texts above. `server` providers
  are `ga4-mp` and `meta-capi`.
- verdict, report: `Result.Notes` may hold browse's notes (the geo
  override, Echo not answering) before the verdict writes its own.
- testbed: c18's `X-Echo-Run` value is `<Plan.ID>/page-1-reject` (the
  page's place in the plan). e05: the temporary profile is still rod's
  default under the child's TMPDIR.
