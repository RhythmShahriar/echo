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

Notes users may notice:

- GA4 is also recognised on the site's own host (server-side tagging) when the request is `/g/collect` or `/g/s/collect` with `v=2` and a `tid=G-...`, or any `/<x>/collect` with `v=2`, `tid=G-...` and `cid` (renamed loaders).
- Google Tag Manager is script only: `www.googletagmanager.com/gtm.js`, `/gtag/js` and `/gtag/destination`, plus `/gtm.js?id=GTM-...` or `/gtag/js?id=G-...` on any host.
