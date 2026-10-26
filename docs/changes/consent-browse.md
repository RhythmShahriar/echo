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
  `browser traffic`, `not mapped`, `limit`.
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
  refused for every target and counted in `network.refused`.

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
- `--geo` knows the 27 EU states, GB, NO, IS, CH, US, CA, BR, AU, JP and
  IN (`UK` and `EL` read as GB and GR); `browse.GeoCountries()` lists
  them for the CLI's usage error. `--locale` and `--timezone` win.
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
