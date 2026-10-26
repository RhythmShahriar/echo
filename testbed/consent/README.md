# Echo consent testbed

Forty small shops, each a few pages with a cookie banner and the tags a real shop loads, for the end-to-end tests and the public benchmark of `rhythm-echo consent` ([docs/benchmark-consent.md](../../docs/benchmark-consent.md)).

Twenty-five scenarios are **bad**: each has a good state (a correct setup) and a broken state that breaks one thing the way a plugin update, a tag manager change or a CMP setting would. GA4 fires before the choice, the Meta Pixel ignores Consent Mode, Reject is reported as granted, a choice isn't kept on the next page, the server sends a Conversions API event after Reject. Fifteen scenarios are **good**: correct setups that must pass with no error and no warning, including the lookalikes that trip naive checkers. Eighteen environment probes break what is around Echo instead (no Chrome, a bot wall, Ctrl-C mid-run, a busy CPU) and thirteen security fixtures feed them hostile pages.

Every site is `<id>.shop.test`. `.test` is reserved, so a name that slips past the mapping can never reach a real site. The tracker and CMP hosts (`www.googletagmanager.com`, `connect.facebook.net`, `consent.cookiebot.com` and the rest) are served by the testbed itself: stand-in tags written to the vendors' documented behaviour and CMP replicas written fresh from public markup. Nothing a scenario does reaches the internet. A sink counts every tracking hit that arrives so the benchmark can prove Echo held them.

## Two modes

**Go mode** needs only Go and Chrome. One Go server (`server/`, command `server/cmd/consent-testbed`) serves every scenario and every stand-in host on one port, chosen by the Host header. `rhythm-echo consent` reaches it through `--map-host` and `--map-only`, so Chrome resolves nothing on its own. The browser integration tests (`consent_test.go`, `-tags browser`) and CI use this mode on Linux, Windows and macOS.

**Sealed Docker mode** is the benchmark of record. `docker-compose.yml` (project `echo-consent-testbed`) runs the same server behind an nginx `gate` that terminates TLS for every name, a resolver (`dns/`) that answers every name with the gate and never forwards, WordPress 6.8 with the real Complianz and CookieYes plugins (pinned by SHA-256 in `wp/plugins.lock`) and a `runner` with Chrome for Testing and the `rhythm-echo` built from this checkout. The runner is only on a network with no gateway: Chrome has no route out, whatever a page asks for. Its Chrome sandbox runs under `runner/seccomp-chrome.json`, Docker's default seccomp profile (moby/profiles, Apache-2.0) plus one rule that allows `clone`, `setns` and `unshare`. Every pulled image is pinned by digest and the resolver runs without root or capabilities. Echo's own `rhythm-echo up` (for c18) is the optional `echo` profile: `make up` starts it and the sites start without it. Ten scenarios also run there on the real plugins (the Docker variants below).

## Ports

All bound to 127.0.0.1.

| Mode | Port | What |
|---|---|---|
| Go | 23400 | every site and stand-in host, http and https on the same port |
| Go | 23401 | admin: state switches, sink counts, health |
| Go | 23410-23437 | the environment probes' own servers (`-probe-port` moves them) |
| Go | 23438-23439 | the Echo the benchmark starts for c18 (`--echo`) |
| Docker | 18950 | the gate: every site and tag host over https |
| Docker | 18951 | the tag hosts' sink and state endpoints |
| Docker | 18952 | the sites' sink (first-party hits) |
| Docker | 18953 | Echo's API (c18) |
| Docker | 18954-18969 | reserved for the testbed |

## Run one scenario by hand

From the repository root:

```sh
make build                              # bin/rhythm-echo
make -C testbed/consent serve           # makes the testbed CA on first use, then serves Go mode
make -C testbed/consent map             # prints the --map-host line
```

`make map` prints one line of flags, `--map-host '*.shop.test=127.0.0.1:23400' --map-host analytics.tiktok.com=127.0.0.1:23400 ... --map-only`, with every stand-in host. Paste it after the URL, or let the shell do it (`eval`, because the line quotes the star):

```sh
map=$(make -s -C testbed/consent map)
eval "bin/rhythm-echo consent https://c01.shop.test/ -k $map"
testbed/consent/toggle.sh c01 broken
eval "bin/rhythm-echo consent https://c01.shop.test/ -k $map"
```

The good state passes; the broken one reports GA4 before the choice and no Consent Mode default. `-k` is there because Chrome doesn't trust the testbed CA (`ca/ca.pem`, git-ignored, name-constrained to `.test`, `localhost` and the stand-in hosts); use `http://` instead to skip TLS. What the sink counted: `curl -s '127.0.0.1:23401/_sink/count?scenario=c01'`.

For the sealed mode: `make -C testbed/consent up` (a few minutes the first time), `make -C testbed/consent bench`, `make -C testbed/consent down` (deletes every volume).

## Switch a scenario

A scenario is broken while `state/<id>` exists; the server reads it on every request, so a switch takes effect at once.

```sh
testbed/consent/toggle.sh c01 broken
testbed/consent/toggle.sh c01 good
testbed/consent/toggle.sh all good
testbed/consent/toggle.sh status        # "all scenarios good" or "broken: c01 c02"
make -C testbed/consent toggle ID=c01 STATE=broken
curl '127.0.0.1:23401/_admin/state?id=c01&state=broken'   # the admin port only
```

## The benchmark

```sh
make bench-consent                      # Docker mode, after make -C testbed/consent up
go run -tags testbed ./testbed/consent/bench -bin bin/rhythm-echo -mode go -only c01,g01
make -C testbed/consent reproduce       # two full runs, then -diff: fails when any outcome differs
```

Each scenario runs good then broken. A scenario is caught when every `expect` matches, no `forbid` does, the sink counted no hit (held mode) and every stand-in it uses shows in the run's scripts; a stand-in that didn't load is a miss, never a catch. The control pass runs every good state ten times: any error or warning outside the scenario's allow list is a false alarm, any round that differs from the first is a flake. The benchmark also times each page and `consent detect`, runs a 50-site fleet, reads Echo's own peak memory (Chrome left out, Linux only) and its binary size against v1.1.2, then writes `docs/benchmark-consent.md` and `bench/work/results.json`. `WEC_BIN` and `AUTOCONSENT_DIR` add the EDPS Website Evidence Collector and DuckDuckGo autoconsent as reference columns; without them they read "not run".

## What the results mean

The scores say how Echo does on this testbed, nothing more. The commercial CMPs (Cookiebot, OneTrust, Usercentrics, Didomi, Iubenda and Borlabs, plus Complianz and CookieYes in Go mode) are replicas of public markup captured on the dates in their `SOURCE.md`, not the vendors' code: a vendor that changes its banner tomorrow can break Echo on real sites while this testbed still passes. Google's and Meta's tags are stand-ins written to community-documented behaviour, not the real scripts. Klaro, orestbida cookieconsent and the two WordPress plugins are the real thing at pinned versions. Nothing reached the internet: the sink count is in the report and must be 0. A clean result is evidence about what a browser did on these pages, not legal advice.

## Refresh a replica

Each replica folder under `server/static/cmps/` has a `SOURCE.md` with the public pages it follows and a `captured: YYYY-MM-DD` line. `TestReplicaAge` fails when a capture is more than 180 days old. To refresh one: open the vendor's current documentation and a live banner, update the replica's markup, cookie format and API to match, run `go test -tags browser ./testbed/consent/server/` (its Accept and Reject must still write the vendor's state) and set the new capture date. The vendored CMPs are pinned in `server/static/cmps/vendor/VERSIONS` by SHA-256 (`TestVendoredHashes`); a new release means new files, a new version line and a new hash.

## Scenarios

The tables come from `scenarios.yaml`, which has the full expectations. `TestReadmeTableMatchesManifest` fails when they differ; after a change to the manifest, regenerate them:

```sh
go run -tags testbed ./testbed/consent/bench -list
```

and paste the output over this section's tables.

<!-- Scenario tables: generated by go run -tags testbed ./testbed/consent/bench -list. Don't edit by hand. -->

### Bad scenarios

The good state is a correct site; `toggle.sh <id> broken` switches on what the last but one column says.

| ID | Scenario | Area | CMP | Extra flags | Broken state | Echo must find |
|---|---|---|---|---|---|---|
| c01 | GA4 before consent | consent-mode | complianz |  | no Consent Mode default, so gtag config sends at once | `check=consent/before-choice tracker=ga4 level=error`; `check=consent/no-default level=error` |
| c02 | Meta Pixel ignores Consent Mode | trackers | complianz |  | Google is set up right; fbq init and PageView run with no revoke | `check=consent/before-choice tracker=meta-pixel level=error`; `check=consent/after-reject tracker=meta-pixel level=error`; `check=consent/meta-no-consent-api tracker=meta-pixel level=notice`; not `check=consent/before-choice tracker=ga4` |
| c03 | CMP misconfigured | cmp | cookiebot |  | auto-blocking off, Consent Mode integration off, no default; GTM fires GA4 and Ads whatever the choice | `check=consent/after-reject tracker=ga4 level=error`; `check=consent/after-reject tracker=google-ads level=error` |
| c04 | Reject reported as granted | consent-mode | onetrust |  | the category map sends analytics_storage granted for necessary only | `check=consent/reject-granted level=error` |
| c05 | Consent Mode default late | consent-mode | usercentrics |  | GTM runs first and the default is pushed 1.5 s later | `check=consent/default-late level=warning`; not `check=consent/no-default` |
| c06 | Consent Mode v1 only | consent-mode | cookieyes |  | the default and the updates leave out ad_user_data and ad_personalization | `check=consent/v2-signals level=warning` |
| c07 | Choice not kept | choice | borlabs |  | the CMP cookie is written with Path=/blog, so the next page shows the banner again | `check=consent/reject-not-kept level=error` |
| c08 | A plugin update adds TikTok before consent | trackers | didomi | `--against good` | a TikTok pixel loads unblocked; compared with a baseline saved in the good state | `class=broke check=consent/before-choice tracker=tiktok title~"TikTok Pixel now fires before the choice"` |
| c09 | Session replay before consent | trackers | iubenda |  | Clarity and Hotjar load at once and write cookies and storage before the choice | `check=consent/before-choice tracker=microsoft-clarity level=error`; `check=consent/before-choice tracker=hotjar level=error` |
| c10 | Reject only in the preference centre | cmp | onetrust |  | no Reject All on the banner's first layer | `check=consent/no-first-layer-reject level=warning`; `run=reject status=ok`; not `check=consent/after-reject` |
| c11 | YouTube embed after Reject | trackers | usercentrics | `--click button,api` | the youtube.com frame loads whatever the choice; the CMP is in a closed shadow root, so only its API can choose | `check=consent/embed tracker=youtube level=warning`; `check=consent/click-fallback level=notice` |
| c12 | Server-side GTM before the choice | server | cookiebot |  | no Consent Mode default, so GA4 sends to metrics.c12.shop.test at once | `check=consent/before-choice tracker=ga4 level=error first_party=true` |
| c13 | sendBeacon on pagehide after Reject | trackers | generic |  | a Meta beacon is sent on every pagehide whatever the choice | `check=consent/after-reject tracker=meta-pixel level=error` |
| c14 | GTM in a web worker | trackers | complianz |  | no Consent Mode default reaches the worker, so it sends at once | `check=consent/before-choice tracker=ga4 level=error` |
| c15 | GPC ignored | choice | cookieyes | `--runs +gpc` | the CMP reads the GPC signal as consent to everything | `check=consent/gpc-ignored level=warning run=gpc` |
| c16 | Accept never grants | consent-mode | didomi |  | Agree is stored but no Consent Mode update is sent; the Google tag pings in advanced mode | `check=consent/accept-not-granted level=warning`; `check=consent/cookieless-ping tracker=ga4 level=notice` |
| c17 | Banner hidden from bots | cmp | cookiebot | `-A "Mozilla/5.0 (X11; Linux x86_64) AppleWebKit/537.36 (KHTML, like Gecko) HeadlessChrome/156.0.8078.47 Safari/537.36"` | no banner for HeadlessChrome or webdriver and no Consent Mode default | `check=consent/no-banner level=warning`; `check=consent/before-choice tracker=ga4 level=error` |
| c18 | Server side after Reject | server | complianz | `--echo "{echo}"` | the shop's server sends a Meta Conversions API event on every page, whatever the choice | `check=consent/server-side level=error` |
| c19 | TCF mismatch | cmp | didomi |  | after Disagree the TC string still grants purposes 1 and 3 | `check=consent/tcf-mismatch level=warning` |
| c20 | Click does nothing | choice | generic | `--click button,api` | a transparent overlay covers the page and the Reject handler binds 6 s late | `run=reject status=click-failed`; or `check=consent/reject-broken level=error`; or `check=consent/click-fallback level=notice`; or `run=reject status=ok` |
| c21 | Reject only hides the banner | choice | generic |  | Reject stores nothing and runs the tags anyway | `run=reject reason=state_unchanged`; or `check=consent/after-reject level=error` |
| c22 | Scroll-gated LinkedIn tag | trackers | cookieyes |  | the Insight Tag loads unblocked and fires on the first scroll | `check=consent/before-choice tracker=linkedin level=error` |
| c23 | A second plugin's GA4 snippet | trackers | complianz |  | a minimal analytics snippet sends to /g/collect directly on every page | `check=consent/before-choice tracker=ga4 level=error`; `check=consent/after-reject tracker=ga4 level=error` |
| c24 | Withdraw keeps Meta firing | choice | onetrust | `--runs +withdraw` | the site keeps its own marketing flag from the first Accept, so Meta loads again after the withdrawal | `check=consent/after-withdraw tracker=meta-pixel level=error run=withdraw` |
| c25 | Advanced Consent Mode under strict | consent-mode | complianz | `--consent-mode strict` | GTM loads at once in advanced mode and sends cookieless pings | `check=consent/before-choice tracker=ga4 level=error`; `check=consent/script-before tracker=google-tag-manager level=error` |

### Good scenarios

No error and no warning in any run; only the notices listed may appear.

| ID | Scenario | Area | CMP | Extra flags | Notices allowed |
|---|---|---|---|---|---|
| g01 | The correct setup | consent-mode | complianz |  | none |
| g02 | The correct setup in Consent Mode advanced | consent-mode | complianz |  | `consent/cookieless-ping` |
| g03 | Cookiebot with auto-blocking and Consent Mode | cmp | cookiebot |  | none |
| g04 | OneTrust with Reject on the first layer | cmp | onetrust |  | none |
| g05 | CookieYes per its docs | cmp | cookieyes | `--runs +gpc` | none |
| g06 | Usercentrics in shadow DOM | cmp | usercentrics |  | none |
| g07 | Didomi with TCF agreeing with Consent Mode | cmp | didomi |  | none |
| g08 | Iubenda | cmp | iubenda |  | none |
| g09 | Borlabs | cmp | borlabs |  | none |
| g10 | Custom banner in Arabic | cmp | generic |  | none |
| g11 | Plausible before consent, Klaro for the rest | trackers | klaro |  | none |
| g12 | Brochure site with no trackers and no banner | environment | none |  | none |
| g13 | Scripts load before consent, every hit waits | trackers | cookieconsent |  | none |
| g14 | Lookalikes and a late banner | trackers | generic |  | none |
| g15 | A tag that waits for its own response | trackers | cookiebot |  | none |

### Docker variants

The same scenario on the real WordPress plugin, run only with `-mode docker`.

| ID | Scenario | CMP | Site |
|---|---|---|---|
| c01-wp | c01 | complianz-wp | c01-wp.shop.test |
| c02-wp | c02 | complianz-wp | c02-wp.shop.test |
| c06-wp | c06 | cookieyes-wp | c06-wp.shop.test |
| c14-wp | c14 | complianz-wp | c14-wp.shop.test |
| c22-wp | c22 | cookieyes-wp | c22-wp.shop.test |
| c23-wp | c23 | complianz-wp | c23-wp.shop.test |
| c25-wp | c25 | complianz-wp | c25-wp.shop.test |
| g01-wp | g01 | complianz-wp | g01-wp.shop.test |
| g02-wp | g02 | complianz-wp | g02-wp.shop.test |
| g05-wp | g05 | cookieyes-wp | g05-wp.shop.test |

### Environment probes

| ID | Probe | Needs | Broken state |
|---|---|---|---|
| e01 | The site is down | local | the testbed server stops before the check |
| e02 | No Chrome and no download | local | an empty PATH and cache, then --browser naming nothing, both with --no-download |
| e03 | A bot wall instead of the shop | local | the site answers a 403 Cloudflare challenge page |
| e04 | The named CMP isn't there | local | --cmp onetrust on a Cookiebot page instead of a OneTrust one |
| e05 | Ctrl-C during the Reject run | local | SIGINT (a console Ctrl-Break on Windows) once the Reject run's events file appears |
| e06 | Every CPU busy | local | a busy loop on every core during the checks |
| e07 | --parallel 1 against --parallel 3 | local | the broken c02 checked with --parallel 1 and with --parallel 3 |
| e08 | Held mode on pages that fire early | local | c01, c02, c09, c13, c14, c22 and c23 broken, all checked in the default held mode |
| e09 | --send-hits on a production host | local | the config lists c01.shop.test under production |
| e10 | Every hit Echo reports reached the sink | local | c01 broken with --send-hits; the good state is g01 with --send-hits |
| e11 | A baseline written by Echo 1.2.0 | local | --against testdata/runs/1.2.0/g01/consent.json instead of a baseline this Echo saved |
| e12 | A self-signed certificate | local | no -k on a local https site with a self-signed certificate |
| e13 | CMP present, banner never shows | local | slow-banner shows its banner after 15 s, past --banner-wait 10s |
| e14 | A request storm | local | request-storm sends more requests than the per-run limit |
| e15 | A huge dataLayer | local | huge-datalayer floods the dataLayer |
| e16 | Private addresses on the page | docker | ssrf-probe also asks for 169.254.169.254 and 10.0.0.5 |
| e17 | Hostile text in the banner and the page | local | markup, escape codes, bidi overrides, Markdown and CSV injection in the title, buttons, text and dataLayer |
| e18 | Secrets and personal data in the URL and hits | local | a token in the page URL copied into every hit, a header and cookie through env:, an e-mail, phone and user ID in hits; broken sends them before the choice |

### Security fixtures

| Site | What for |
|---|---|
| ssrf-probe | e16: private addresses the egress guard must refuse (broken adds the metadata and 10.x addresses; Docker mode only) |
| hostile-banner | e17: markup, escape codes and bidi overrides in the banner |
| hostile-text | e17: markup, Markdown, CSV and escape codes in page text and the dataLayer |
| patched-dom | page scripts that override DOM and JS APIs (isolated world) |
| sgtm-first-party | GA4 through a first-party server-side container |
| pii-beacons | e18: personal data in hits |
| secret-url | e18: a token in the page URL copied into every hit |
| huge-datalayer | e15: bounded memory under a dataLayer flood |
| request-storm | e14: more requests than the per-run limit |
| redirect-chain | redirects: 3 hops good, 12 broken |
| redirect-loop | redirects: a loop when broken |
| self-signed | e12: a self-signed certificate |
| slow-banner | the banner after 4 s good, 15 s broken |

<!-- End of the generated scenario tables. -->
