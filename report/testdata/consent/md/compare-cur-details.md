## ♪ Echo consent

**Broke.** 1 page, 3 runs, 4 trackers, policy advanced: 2 broke, 1 new, 0 changed, 0 fixed against baseline.

| Page | Banner | No choice | Accept | Reject |
|---|---|---|---|---|
| / | Complianz | ✗ TikTok Pixel | Google Analytics 4, Google Ads, Meta Pixel, TikTok Pixel | ✗ TikTok Pixel |

### Errors (2)

| Check | Page | Run | What | Fix |
|---|---|---|---|---|
| consent/before-choice | / | No choice | TikTok Pixel fired before the visitor chose \(analytics.tiktok.com/api/v2/pixel?sdkid=••••9KQ1&event=Pageview at 0.91s; cookie \_ttp written\) | Load the tracker only after the visitor accepts: let the consent banner block its script, or start it with consent denied and grant it on Accept. |
| consent/before-choice | / | Reject | TikTok Pixel fired before the visitor chose \(analytics.tiktok.com/api/v2/pixel?sdkid=••••9KQ1&event=Pageview at 0.91s\) | as above |

### Runs

<details><summary>/ · No choice</summary>

- 0.00s load https://c08.consent.test/
- 0.12s gtag consent default  all four denied
- 0.30s script googletagmanager.com/gtm.js?id=GTM-••••XK2
- 0.41s banner Complianz shown
- 0.61s script connect.facebook.net/en\_US/fbevents.js
- 0.70s script analytics.tiktok.com/i18n/pixel/events.js
- 0.91s hit TikTok Pixel Pageview \(held\)
- 0.93s cookie \_ttp \(TikTok Pixel\)
- 9.40s page /shop/ \(link 1 of 1\)
- 12.90s reload https://c08.consent.test/

</details>

<details><summary>/ · Accept</summary>

- 0.00s load https://c08.consent.test/
- 0.12s gtag consent default  all four denied
- 0.30s script googletagmanager.com/gtm.js?id=GTM-••••XK2
- 0.41s banner Complianz shown
- 2.01s click "Accept" \(.cmplz-accept\)
- 2.03s gtag consent update   all four granted
- 2.30s hit Google Analytics 4 page\_view gcs=G111 gcd=13r3r3r3r5 \(held\)
- 2.32s cookie \_ga \(Google Analytics 4\)
- 2.34s hit Google Ads gcs=G111 gcd=13r3r3r3r5 \(held\)
- 2.38s hit Meta Pixel PageView \(held\)
- 2.40s cookie \_fbp \(Meta Pixel\)
- 2.42s hit TikTok Pixel Pageview \(held\)
- 9.40s page /shop/ \(link 1 of 1\)
- 9.70s hit Google Analytics 4 page\_view gcs=G111 gcd=13r3r3r3r5 \(held\)
- 9.74s hit Google Ads gcs=G111 gcd=13r3r3r3r5 \(held\)
- 9.78s hit Meta Pixel PageView \(held\)
- 9.82s hit TikTok Pixel Pageview \(held\)
- 12.90s reload https://c08.consent.test/
- 13.20s hit Google Analytics 4 page\_view gcs=G111 gcd=13r3r3r3r5 \(held\)
- 13.24s hit Google Ads gcs=G111 gcd=13r3r3r3r5 \(held\)
- 13.28s hit Meta Pixel PageView \(held\)
- 13.32s hit TikTok Pixel Pageview \(held\)

</details>

<details><summary>/ · Reject</summary>

- 0.00s load https://c08.consent.test/
- 0.12s gtag consent default  all four denied
- 0.30s script googletagmanager.com/gtm.js?id=GTM-••••XK2
- 0.41s banner Complianz shown
- 0.91s hit TikTok Pixel Pageview \(held\)
- 1.98s click "Deny" \(.cmplz-deny\)
- 2.00s gtag consent update   all four denied
- 9.40s page /shop/ \(link 1 of 1\)
- 12.90s reload https://c08.consent.test/

</details>

### Consent Mode

Default: v2, all four signals denied at 0.12s. Accept: update granted at 2.0s. Reject: update denied at 2.0s. gcd decoded with an unofficial mapping.

### Changes against baseline (3)

| Change | What | Page | Run | Before and after |
|---|---|---|---|---|
| BROKE | TikTok Pixel now fires before the choice | / | No choice |  |
| BROKE | TikTok Pixel now fires before the choice | / | Reject |  |
| NEW | TikTok Pixel fires after Accept | / | Accept |  |

_Evidence, not legal advice. Echo 1.2.0, Chrome 156.0.8078.47, 2026-10-26 09:20 UTC. Hits held in the browser. Hosts mapped with --map-host. consent.json sha256 e3b0c44298fc..._
