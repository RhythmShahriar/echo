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

## Follow-ups for other lanes

- cli: leave `Plan.Locale` and `Plan.Timezone` empty when the flags are
  not given; browse resolves `--geo`, then the machine. Validate `--geo`
  against `browse.GeoCountries()`.
- testbed: a service worker can't register from a `--map-host` https
  host: Chrome fetches its script outside any tab's interception, so the
  request reaches the guard, which can't answer a mapped host over TLS.
  Serve service-worker fixtures from a 127.0.0.1 or localhost target
  (the B4 test does).
