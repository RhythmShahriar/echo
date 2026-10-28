# Consent checks: tracker catalog changes

What the tracker lane (`consent/tracker`) changes for users, with the
exact text they see. The docs team writes the wiki from this file.

## Catalog and loader (T1)

- The tracker catalog is compiled into `rhythm-echo` and registered as the consent classifier, so `consent` no longer reports `consent: tracker not linked into this build`.
- Catalog info, as `consent trackers` and every consent result show it (`catalog` in JSON): version `1.2.0`, the SHA-256 of the catalog files and the number of entries. The hash covers every file in `consent/tracker/catalog/` (path, NUL, bytes, NUL, in path order), so two builds with the same version but other data show other hashes.
- `consent trackers` lists the entries sorted by id. The Matches column is one line: the first three host and path patterns (hits first, then frames, then scripts), joined by `, ` and ended with ` and N more` when there are more; a rule that matches on any host reads `any host /path`; an entry with only cookies reads `cookie <name>`. Example for ga4: `www.google-analytics.com/g/collect, www.google-analytics.com/g/s/collect, region1.google-analytics.com/g/collect and 8 more`.
- Hold (answering a hit locally): only requests the catalog classifies as beacons (`kind: beacon`) of entries with consent `needed` or `exempt`. Scripts, frames, requests of type `script` or `document` and entries with consent `no` (necessary services) are never held.
- `first_party: true` on a hit means its host is the page's registrable domain or a subdomain of it (`metrics.shop.test` on `https://shop.test`).
- Hit `params` keeps only `gcs`, `gcd`, `npa`, `dma`, `en`, `ev`, `tid` (masked to its last 4), `id` (masked), `dl` and `dr` (masked URLs). Personal fields present in the request are listed by name only, as `personal`, e.g. `"personal": "ud[em],ph"`.
- Hit `consent` (Google) carries `gcs`, `gcd`, `npa` and `dma` raw; decoding them is the recorder's job.
- Hit `id` is the measurement or pixel ID masked to its last 4 (`••••D001`); for Google Ads, remarketing, Floodlight, Quora, Yandex and Adobe it comes from the path.
- Every URL Echo shows or stores (hits, scripts, events, HAR, reports, `--live`, `-v`) masks the IDs the catalog names the same way: `sdkid`, `pixel_code`, `idsite`, `marketerId`, `company_id` and `txn_id` on any host; each entry's other `params: id:` names (`a`, `pid`, `ti`, `tid`, `id`, `sid`) and its `id_path` ID (`/pagead/viewthroughconversion/••••0001/`, `/pagead/1p-user-list/••••0001/`) on that entry's hosts. A custom tracker's `params: id:` and `id_path` are masked on its hosts too. The masked bullets read as bullets in every output (issue details, `--live`, consent.json, reports), not as `%E2%80%A2`. A GA4 cookie's name carries the measurement ID (`_ga_<id>`): consent.json, the events, the HAR's Cookie and Set-Cookie headers and every report name it `_ga_••••D001` (classified by the real name first); `--show-ids` keeps it whole.
- Config `consent.trackers` (custom trackers): see T9 below.
- Catalog file errors (for catalog authors; custom trackers use the same messages without the file and line, see T9) always start with `<file>:<line>: `, for example `catalog/bad.yaml:4: unknown key "hostz"`, `catalog/other.yaml:1: id "shop-pixel" must match the file name (other.yaml)`, `catalog/x.yaml:8: a hit on any host needs params_all, params_match or body_json to prove it`, `catalog/x.fixtures.yaml:2: hits.match "https://px.shop.example/other" doesn't match shop-pixel`, `catalog/x.fixtures.yaml:3: hits.no_match "..." matches shop-pixel`, `<id> needs at least one hits.no_match fixture`, `<id> needs at least one cookies.match and one cookies.no_match fixture`, `an exempt entry sets no cookies`, `cookies: "_shpx" is already claimed by other-pixel`.

## Entries

All 47 PRD 12.1 entries.

Each entry with its consent need, what `consent trackers` shows under Matches and the fix line its issues carry.

| id | name | category | consent | matches | fix |
|---|---|---|---|---|---|
| `floodlight` | Google Floodlight | advertising | needed | `fls.doubleclick.net, ad.doubleclick.net/activity*, ad.doubleclick.net/ddm/activity/*` | Set a Consent Mode default (all four signals denied) before the Floodlight tag, or let the banner block it until consent |
| `ga4` | Google Analytics 4 | analytics | needed | `www.google-analytics.com/g/collect, www.google-analytics.com/g/s/collect, region1.google-analytics.com/g/collect and 8 more` | Set a Consent Mode default (all four signals denied) before the Google tag, or let the banner block it until consent |
| `google-ads` | Google Ads | advertising | needed | `googleads.g.doubleclick.net/pagead/viewthroughconversion/*, googleads.g.doubleclick.net/pagead/conversion/*, www.googleadservices.com/pagead/conversion/* and 4 more` | Set a Consent Mode default (all four signals denied) before the Google tag, or let the banner block Google Ads until consent |
| `google-ccm` | Google tag measurement (ccm) | advertising | needed | `www.google.com/ccm/collect` | Set a Consent Mode default (all four signals denied) before the Google tag, or let the banner block it until consent |
| `google-remarketing` | Google Ads remarketing | advertising | needed | `www.google.com/pagead/1p-user-list/*` | Set a Consent Mode default (all four signals denied) before the Google tag, or let the banner block remarketing until consent |
| `google-tag-manager` | Google Tag Manager | analytics | needed | `www.googletagmanager.com/gtm.js, www.googletagmanager.com/gtag/js, www.googletagmanager.com/gtag/destination and 2 more` | Under strict policy load Google Tag Manager only after consent |
| `universal-analytics` | Universal Analytics | analytics | needed | `www.google-analytics.com/collect, www.google-analytics.com/r/collect, www.google-analytics.com/j/collect and 9 more` | Remove Universal Analytics (Google no longer processes its data) or let the banner block it until consent |
| `amazon-ads` | Amazon Ads | advertising | needed | `s.amazon-adsystem.com/iu3*, s.amazon-adsystem.com/ecm3*, c.amazon-adsystem.com` | Load Amazon Ads tags only after the banner reports marketing consent |
| `criteo` | Criteo | advertising | needed | `sslwidget.criteo.com, dis.criteo.com, gum.criteo.com and 2 more` | Load the Criteo OneTag only after the banner reports marketing consent |
| `hubspot` | HubSpot | analytics | needed | `track.hubspot.com/__ptq.gif, js.hs-scripts.com, js.hs-analytics.net and 2 more` | Turn on HubSpot's cookie consent banner or let your banner block the HubSpot tracking code until consent |
| `klaviyo` | Klaviyo | advertising | needed | `a.klaviyo.com/api/track*, a.klaviyo.com/client/events*, static.klaviyo.com/onsite/js/**` | Load Klaviyo's onsite script only after the banner reports marketing consent |
| `linkedin` | LinkedIn Insight Tag | advertising | needed | `px.ads.linkedin.com/collect*, px.ads.linkedin.com/wa/**, snap.licdn.com/li.lms-analytics/insight.min.js and 1 more` | Load the LinkedIn Insight Tag only after the banner reports marketing consent |
| `meta-pixel` | Meta Pixel | advertising | needed | `www.facebook.com/tr, facebook.com/tr, any host /tr and 3 more` | Meta Pixel doesn't read Google Consent Mode. Call fbq('consent', 'revoke') before fbq('init'), then fbq('consent', 'grant') when the banner reports marketing consent, or let the banner block the pixel script. |
| `microsoft-uet` | Microsoft UET (Bing Ads) | advertising | needed | `bat.bing.com/action/*, bat.bing.com/bat.js, bat.bing.com/p/action/*.js` | Set uetq.push('consent', 'default', {ad_storage: 'denied'}) before the UET tag, or let the banner block it until consent |
| `outbrain` | Outbrain Pixel | advertising | needed | `tr.outbrain.com/unifiedPixel*, traffic.outbrain.com/network/trackpxl, amplify.outbrain.com/cp/obtp.js` | Load the Outbrain Pixel only after the banner reports marketing consent |
| `pinterest` | Pinterest Tag | advertising | needed | `ct.pinterest.com/v3/*, ct.pinterest.com/user/*, s.pinimg.com/ct/core.js and 1 more` | Load the Pinterest Tag only after the banner reports marketing consent |
| `quora` | Quora Pixel | advertising | needed | `q.quora.com/_/ad/**, a.quora.com/qevents.js` | Load the Quora Pixel only after the banner reports marketing consent |
| `reddit` | Reddit Pixel | advertising | needed | `alb.reddit.com/rp.gif, www.redditstatic.com/ads/pixel.js` | Load the Reddit Pixel only after the banner reports marketing consent |
| `snap` | Snap Pixel | advertising | needed | `tr.snapchat.com/p, tr.snapchat.com/cm/*, sc-static.net/scevent.min.js and 1 more` | Load the Snap Pixel only after the banner reports marketing consent |
| `taboola` | Taboola Pixel | advertising | needed | `trc.taboola.com, cdn.taboola.com` | Load the Taboola Pixel only after the banner reports marketing consent |
| `tiktok` | TikTok Pixel | advertising | needed | `analytics.tiktok.com/api/v2/pixel, analytics.tiktok.com/api/v2/pixel/**, analytics.tiktok.com/i18n/pixel/events.js and 2 more` | Load the TikTok Pixel only after the banner reports marketing consent, or call ttq.holdConsent() first and ttq.grantConsent() on consent |
| `x-pixel` | X (Twitter) Pixel | advertising | needed | `analytics.twitter.com/i/adsct, t.co/i/adsct, static.ads-twitter.com/uwt.js and 1 more` | Load the X Pixel only after the banner reports marketing consent |
| `adobe-analytics` | Adobe Analytics | analytics | needed | `*.sc.omtrdc.net/b/ss/**, *.2o7.net/b/ss/**, any host /b/ss/** and 2 more` | Set Adobe's defaultConsent to pending (Web SDK) or turn on Opt-In in the Experience Cloud ID service until the visitor accepts |
| `amplitude` | Amplitude | analytics | needed | `api2.amplitude.com/2/httpapi, api2.amplitude.com/batch, api.eu.amplitude.com/2/httpapi and 3 more` | Initialize Amplitude with optOut true and turn it off once the visitor accepts |
| `fathom` | Fathom Analytics | analytics | exempt | `cdn.usefathom.com/, cdn.usefathom.com/script.js` | (none: exempt) |
| `fullstory` | FullStory | session-replay | needed | `rs.fullstory.com/rec/**, rs.eu1.fullstory.com/rec/**, edge.fullstory.com` | Start FullStory with _fs_capture_on_startup set to false and call FS('start') once the visitor accepts |
| `heap` | Heap | analytics | needed | `heapanalytics.com/h, heapanalytics.com/api/**, c.us.heap-api.com and 4 more` | Load Heap only after the visitor accepts analytics |
| `hotjar` | Hotjar | session-replay | needed | `*.hotjar.com/api/**, *.hotjar.io, static.hotjar.com/c/hotjar-*.js and 1 more` | Load Hotjar only after the visitor accepts analytics or session recording |
| `lucky-orange` | Lucky Orange | session-replay | needed | `api.luckyorange.com, *.luckyorange.net, *.visitors.live and 1 more` | Load Lucky Orange only after the visitor accepts analytics or session recording |
| `matomo` | Matomo | analytics | needed | `any host /matomo.php, any host /piwik.php, any host /js/tracker.php and 4 more` | Call _paq.push(['requireCookieConsent']) before tracking and rememberCookieConsentGiven once the visitor accepts, or run Matomo without cookies |
| `microsoft-clarity` | Microsoft Clarity | session-replay | needed | `*.clarity.ms/collect, www.clarity.ms/tag/*, www.clarity.ms/s/** and 1 more` | Load Clarity only after consent, or send it the visitor's choice with window.clarity('consentv2', ...) from the banner |
| `mixpanel` | Mixpanel | analytics | needed | `api-js.mixpanel.com/track*, api-js.mixpanel.com/engage*, api-js.mixpanel.com/groups* and 4 more` | Initialize Mixpanel with opt_out_tracking_by_default and call mixpanel.opt_in_tracking() once the visitor accepts |
| `mouseflow` | Mouseflow | session-replay | needed | `o2.mouseflow.com, cdn.mouseflow.com` | Load Mouseflow only after the visitor accepts analytics or session recording |
| `piwik-pro` | Piwik PRO | analytics | needed | `*.piwik.pro/ppms.php, *.containers.piwik.pro/*.js, *.piwik.pro/ppms.js` | Turn on Piwik PRO's consent manager or let your banner block the container until the visitor accepts |
| `plausible` | Plausible | analytics | exempt | `plausible.io/api/event, any host /api/event, plausible.io/js/*.js` | (none: exempt) |
| `posthog` | PostHog | analytics | needed | `us.i.posthog.com/e/*, us.i.posthog.com/i/v0/e/*, us.i.posthog.com/batch/* and 17 more` | Initialize PostHog with opt_out_capturing_by_default and call posthog.opt_in_capturing() once the visitor accepts |
| `segment` | Segment | analytics | needed | `api.segment.io/v1/*, events.eu1.segmentapis.com/v1/*, cdn.segment.com/analytics.js/** and 3 more` | Load analytics.js only after consent, or use Segment's Consent Management to hold events until the visitor accepts |
| `yandex-metrica` | Yandex Metrica | analytics | needed | `mc.yandex.ru/watch/**, mc.yandex.ru/webvisor/**, mc.yandex.ru/clmap/** and 7 more` | Load Yandex Metrica only after the visitor accepts analytics |
| `google-maps` | Google Maps | embed | needed | `www.google.com/maps/embed*, www.google.com/maps/embed/**, maps.google.com/maps/embed* and 2 more` | Show a static image with a click-to-load button and load the map only after consent |
| `vimeo` | Vimeo embed | embed | needed | `player.vimeo.com/video/*` | Add dnt=1 to the Vimeo player URL or load the player only after consent |
| `youtube` | YouTube embed | embed | needed | `www.youtube.com/api/stats/*, www.youtube.com/youtubei/v1/log_event, youtube.com/api/stats/* and 6 more` | Embed the video from www.youtube-nocookie.com or behind a click-to-load placeholder that loads the player only after consent |
| `google-fonts` | Google Fonts | functional | no | `fonts.googleapis.com/css, fonts.googleapis.com/css2, fonts.googleapis.com/icon and 1 more` | (none: consent no) |
| `hcaptcha` | hCaptcha | necessary | no | `hcaptcha.com/checksiteconfig, hcaptcha.com/getcaptcha/**, hcaptcha.com/checkcaptcha/** and 9 more` | (none: consent no) |
| `paypal` | PayPal checkout | necessary | no | `www.paypal.com/smart/buttons, www.paypal.com/smart/menu, www.paypal.com/smart/card-fields and 1 more` | (none: consent no) |
| `recaptcha` | Google reCAPTCHA | necessary | no | `www.google.com/recaptcha/api2/*, www.google.com/recaptcha/enterprise/*, www.recaptcha.net/recaptcha/api2/* and 14 more` | (none: consent no) |
| `stripe` | Stripe.js | necessary | no | `m.stripe.com, js.stripe.com/v3/*.html, m.stripe.network and 3 more` | (none: consent no) |
| `turnstile` | Cloudflare Turnstile | necessary | no | `challenges.cloudflare.com/cdn-cgi/challenge-platform/**, challenges.cloudflare.com/turnstile/v0/api.js` | (none: consent no) |

Notes users may notice:

- GA4 is also recognised on the site's own host (server-side tagging) when the request is `/g/collect` or `/g/s/collect` with `v=2` and a `tid=G-...`, or any `/<x>/collect` with `v=2`, `tid=G-...` and `cid` (renamed loaders).
- Google Tag Manager is script only: `www.googletagmanager.com/gtm.js`, `/gtag/js` and `/gtag/destination`, plus `/gtm.js?id=GTM-...` or `/gtag/js?id=G-...` on any host.
- Meta Pixel `/tr` on any host counts when it has both `id` and `ev`.
- Cookies with generic names only count on the vendor's domain: `IDE`, `test_cookie`, `DSID` (doubleclick.net), `MUID` (bing.com), `CLID` (clarity.ms), `fr` (facebook.com), LinkedIn's `bcookie`, `lidc`, `li_sugr`, `UserMatchHistory`, `AnalyticsSyncHistory` (linkedin.com), `personalization_id` (twitter.com, x.com), `t_gid`, `t_pt_gid` (taboola.com), `obuid` (outbrain.com), `ad-id`, `ad-privacy` (amazon-adsystem.com).
- `consent/tracker/NOTICE` credits the Open Cookie Database (Apache-2.0) for the cookie names of taboola, snap and outbrain.
- PostHog `/e/` and `/i/v0/e/` on any host when posthog-js's `ver` and `_` parameters are there; Adobe `/b/ss/` on any host with `AQB` and `AQE`; Matomo `/matomo.php` and `/piwik.php` with `idsite` and `rec`; Plausible `/api/event` when the body is JSON with `n` and `u`.
- Plausible and Fathom are `exempt` (cookieless) and list no cookies.
- Matomo and Piwik PRO share the `_pk_` cookies; Echo reports them as `matomo`. Piwik PRO's own cookies are the `stg_` Tag Manager cookies.

## Embeds and necessary services (T5)

- Embeds (category `embed`, consent `needed`): `youtube`, `vimeo` and `google-maps`. Their iframes are hits of kind `frame`; the verdict reports them as `consent/embed`, not as trackers before the choice.
- YouTube counts only on `www.youtube.com` and `youtube.com` (the `/embed/` frame, `/api/stats/` and `/youtubei/v1/log_event`, the IFrame API scripts and the cookies `VISITOR_INFO1_LIVE`, `VISITOR_PRIVACY_METADATA`, `YSC` and `__Secure-ROLLOUT_TOKEN`, the last two on youtube.com only). `www.youtube-nocookie.com` never counts.
- Vimeo counts only for a `player.vimeo.com/video/` frame without `dnt=1` and for the `vuid` cookie on vimeo.com.
- Google Maps counts for the `www.google.com/maps/embed` frame and the Maps JavaScript API (`maps.googleapis.com/maps/api/js`).
- Necessary services (category `necessary`, consent `no`): `stripe` (Stripe.js), `paypal` (PayPal checkout), `recaptcha` (Google reCAPTCHA, also on www.recaptcha.net), `hcaptcha` and `turnstile` (Cloudflare Turnstile). Google Fonts is category `functional` with consent `no`. These are listed under "Seen, no consent needed", never judged and never held.
- Google Fonts stylesheet and font loads are reported with kind `script` (a load, never held).
- For the docs: the Munich Regional Court (LG Muenchen I, 3 O 17493/20, 20 January 2022) held that loading Google Fonts from Google's servers without consent shares the visitor's IP address unlawfully. Echo does not judge it; the docs can suggest self-hosting the fonts.

## Matcher and Hold (T6)

- The catalog refuses to load when a script rule and a hit rule can fit one URL (same host or overlapping host patterns, a GET-capable hit and path patterns that meet), in one entry or across two. The message names both rules: `catalog/shop-tag.yaml:7: a script of shop-tag and a hit of shop-pixel (catalog/shop-pixel.yaml:7) both fit px.shop.example (script path (any), hit path /collect); a script must never look like a hit`. Custom trackers (T9) get the same check.
- hotjar scripts on `script.hotjar.com` now need a `/*.js` path and Microsoft Clarity scripts on `scripts.clarity.ms` a `/*/*.js` path, so a hit on those hosts is never taken for a script load.
- A hit rule on any host never matches a script or page load: a request of type `script` or `document`, or one whose path ends in `.js`, is never a beacon there, whatever parameters it carries (`/gtag/js?id=G-...&v=2&tid=G-...` on a first-party loader stays a script).
- A host longer than 253 bytes (the longest DNS name) is never a tracker.
- Query parameter order never changes a hit.

## Hit parsing (T7)

- A GA4 POST batch gives one event per body line (`\n` or `\r\n`, empty lines skipped), after an event in the query if there is one. At most 256 events (`consent.Limits.BatchEvents`) are kept; a longer batch adds `"events_truncated": "true"` to the hit's `params`. A body line over 64 KiB is ignored.
- Meta Pixel custom data parameters are listed by name only, sorted, as `"cd": "cd[content_ids],cd[currency],cd[value]"` (at most 32 names); their values are never kept.
- Meta Pixel `personal` now also names `ud[ge]`, `ud[db]`, `ud[ct]`, `ud[st]`, `ud[zp]` and `ud[country]` when present (Meta's advanced matching fields), never their values.
- A `dl` or `dr` page URL over 2048 bytes is left out of `params` rather than cut and masked.
- Every value a hit keeps is at most 1024 bytes (event names 100 characters, other parameters 256, `dl` and `dr` 1024, Consent Mode values 64).
- Plausible on any host needs a JSON object body whose `n` and `u` are JSON strings; any other body (a number or null for `n` or `u`, an array, a cut or malformed object, a form body) is not Plausible.

## Cookies that are never trackers (T8)

- A deny list, `consent/tracker/catalog/_not_trackers.yaml`, loads before the entries. A cookie or storage name on it is never reported as a tracker, even when an entry's pattern would fit it. It lists consent manager cookies (`CookieConsent`, `OptanonConsent`, `OptanonAlertBoxClosed`, `cookieyes-consent`, `cmplz_*`, `borlabs-cookie`, `euconsent-v2`, `didomi_token`, `_iub_cs-*`, `uc_*`, `CookieScriptConsent`, `cookielawinfo-*`), session and platform cookies (`PHPSESSID`, `JSESSIONID`, `laravel_session`, `XSRF-TOKEN`, `wordpress_*`, `wp-settings-*`, `wordpress_test_cookie`, `woocommerce_*`, `wp_woocommerce_session_*`) and Cloudflare's `__cf_bm`, `cf_clearance` and `__cfruid`; for storage `uc_*`, `didomi_token`, `euconsent-v2`, `_iub_cs-*` and `CookieScriptConsent`.
- A cookie no entry claims is not a tracker (`nil`); the report lists it under other cookies by name and length only. A tracker cookie's need (`needed`, `exempt`, `no`) comes from its entry.
- The catalog hash now covers `_not_trackers.yaml` and its fixtures too; the entry count stays 47.
- New catalog errors: `catalog/x.yaml:9: cookies: "PHPSESSID" is listed in _not_trackers.yaml as never a tracker`, `catalog/_not_trackers.fixtures.yaml:3: cookies.no_match "_shpx" is classified as shop-pixel`, `catalog/_not_trackers.fixtures.yaml:2: cookies.match "_shop" is no tracker: the deny list reaches too far`, `catalog/_not_trackers.yaml:1: cookies: "PHPSESSID": the deny list holds names only, never a domain`, `catalog/_other.yaml: only _not_trackers.yaml may start with _`.

## Your own trackers (T9)

- `consent: trackers:` in `.echo/config.yaml` lists a site's own trackers in the catalog's shape (`id`, `name`, `category`, `hits`, `scripts`, `frames`, `cookies`, `storage`, `params`, `any_host`, `consent`, `vendor`, `fix`, `docs`) plus `fixtures`. Defaults: `vendor` is the name, `consent` is `needed` (`no` for `category: necessary`), `fix` is `Load it only after the visitor consents` when consent is needed, `docs` may be left out. `sources` is always `config`; writing it is an error.
- Fixtures: `fixtures: {match: [...], no_match: [...]}` hold URLs (`"POST https://..."` or `{method, url, body, type, site}` as in catalog fixtures). An entry with `hits`, `scripts` or `frames` needs at least one of each. `fixtures: {cookies: {match, no_match}, storage: {match, no_match}}` hold names; they are optional beside URL fixtures and required (both sides) for an entry with only cookies or only storage. A match URL must be classified as the entry (beacon, script or frame), a no_match URL not at all.
- Your own trackers are tried before the built-in ones, for requests, cookies and storage keys: an entry for `www.google-analytics.com/g/collect` is reported under its own id instead of `ga4`; an entry may also claim a built-in cookie name such as `_ga`.
- `consent trackers` lists them with the built-in entries, sorted by id, with `sources: ["config"]`. The catalog info (version, hash, count) stays the built-in catalog's.
- They go through the built-in checks too: no name from `_not_trackers.yaml` and no script that looks like a hit (theirs or a built-in one).
- Limits: 200 trackers, 100 fixtures each, fixture URLs up to 2048 bytes, the list up to 1024 KiB.
- Errors (the config is refused, exit 2 through the CLI); every one starts with `consent: trackers: `:
  - `consent: trackers: "ga4" is a built-in tracker; pick another id`
  - `consent: trackers: "shop-pixel" is listed twice; give each tracker its own id`
  - `consent: trackers: shop-pixel needs fixtures: match and no_match, each with at least one URL`
  - `consent: trackers: shop-cookie needs fixtures: cookies: match and no_match, each with at least one name` (and `storage:` for a storage-only entry)
  - `consent: trackers: shop-pixel: fixture "https://px.shop.example/pixel.js" should not match but does`
  - `consent: trackers: shop-pixel: fixture "https://px.shop.example/collect?e=view" should match but doesn't`
  - `consent: trackers: shop-pixel: cookie fixture "_shpx_other" should match but doesn't` (and `storage fixture`)
  - `consent: trackers: shop-pixel: unknown key "hitz"`; `consent: trackers: entry 1: id "Shop Pixel" must be lowercase letters, digits and dashes` (an entry without a usable id is named by its position); `consent: trackers: entry 1: want a mapping of keys`
  - `consent: trackers: want a list of trackers, each with id, name, category, what it matches and fixtures`
  - `consent: trackers: shop-pixel: leave sources out: your own trackers are listed with the source config`
  - `consent: trackers: shop-pixel: cookies: "PHPSESSID" is listed in _not_trackers.yaml as never a tracker`
  - `consent: trackers: shop-gtm: a hit of shop-gtm and a script of google-tag-manager (catalog/google-tag-manager.yaml:<line>) both fit www.googletagmanager.com (...); a script must never look like a hit`
  - `consent: trackers: shop-pixel-2: cookies: "_shpx" is already claimed by shop-pixel`
  - `consent: trackers: 201 trackers is more than 200`, `consent: trackers: shop-pixel: 101 fixtures is more than 100`, `consent: trackers: shop-pixel: a fixture URL is longer than 2048 bytes`, `consent: trackers: the list is more than 1024 KiB`
  - The catalog's own messages otherwise, without file and line: `consent: trackers: shop-pixel: category "ads": want analytics, advertising, session-replay, social, embed, functional or necessary`, `consent: trackers: shop-pixel: docs must be an https:// URL`.
- `rhythm echo checks --test` prints one line per fixture of your own trackers (through the catalog's `FixtureReport`), for example:

  ```
  ok   shop-pixel  match    https://px.shop.example/collect?e=view
  ok   shop-pixel  no_match https://px.shop.example/pixel.js
  FAIL shop-wide   no_match https://stats.shop.example/sw.js  should not match but does
  ok   shop-wide   match    storage sw_id
  FAIL ga4         "ga4" is a built-in tracker; pick another id
  ```

  The id column is as wide as the longest id. URLs are shown with credentials and personal values masked (IDs kept). An entry that doesn't load gets one `FAIL <id>  <message>` line; a problem with the list as a whole, or one found only once the entries join the built-in catalog, a `FAIL consent: trackers: ...` line. Any `FAIL` line makes it fail.
