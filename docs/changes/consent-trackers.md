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
- Config `consent.trackers` (custom trackers) is refused for now with `consent: trackers: custom trackers from the config aren't supported in this build yet` (an empty list is accepted). Item T9 builds them.
- Catalog file errors (for catalog authors; custom trackers will use the same messages) always start with `<file>:<line>: `, for example `catalog/bad.yaml:4: unknown key "hostz"`, `catalog/other.yaml:1: id "shop-pixel" must match the file name (other.yaml)`, `catalog/x.yaml:8: a hit on any host needs params_all, params_match or body_json to prove it`, `catalog/x.fixtures.yaml:2: hits.match "https://px.shop.example/other" doesn't match shop-pixel`, `catalog/x.fixtures.yaml:3: hits.no_match "..." matches shop-pixel`, `<id> needs at least one hits.no_match fixture`, `<id> needs at least one cookies.match and one cookies.no_match fixture`, `an exempt entry sets no cookies`, `cookies: "_shpx" is already claimed by other-pixel`.

## Entries

38 of the 47 PRD 12.1 entries; the embeds and necessary services come with T5.

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

Notes users may notice:

- GA4 is also recognised on the site's own host (server-side tagging) when the request is `/g/collect` or `/g/s/collect` with `v=2` and a `tid=G-...`, or any `/<x>/collect` with `v=2`, `tid=G-...` and `cid` (renamed loaders).
- Google Tag Manager is script only: `www.googletagmanager.com/gtm.js`, `/gtag/js` and `/gtag/destination`, plus `/gtm.js?id=GTM-...` or `/gtag/js?id=G-...` on any host.
- Meta Pixel `/tr` on any host counts when it has both `id` and `ev`.
- Cookies with generic names only count on the vendor's domain: `IDE`, `test_cookie`, `DSID` (doubleclick.net), `MUID` (bing.com), `CLID` (clarity.ms), `fr` (facebook.com), LinkedIn's `bcookie`, `lidc`, `li_sugr`, `UserMatchHistory`, `AnalyticsSyncHistory` (linkedin.com), `personalization_id` (twitter.com, x.com), `t_gid`, `t_pt_gid` (taboola.com), `obuid` (outbrain.com), `ad-id`, `ad-privacy` (amazon-adsystem.com).
- `consent/tracker/NOTICE` credits the Open Cookie Database (Apache-2.0) for the cookie names of taboola, snap and outbrain.
- PostHog `/e/` and `/i/v0/e/` on any host when posthog-js's `ver` and `_` parameters are there; Adobe `/b/ss/` on any host with `AQB` and `AQE`; Matomo `/matomo.php` and `/piwik.php` with `idsite` and `rec`; Plausible `/api/event` when the body is JSON with `n` and `u`.
- Plausible and Fathom are `exempt` (cookieless) and list no cookies.
- Matomo and Piwik PRO share the `_pk_` cookies; Echo reports them as `matomo`. Piwik PRO's own cookies are the `stg_` Tag Manager cookies.
