## ♪ Echo consent

**Findings.** 1 page, 3 runs, 3 trackers, policy advanced: Meta Pixel fired before the choice and after Reject.

| Page | Banner | No choice | Accept | Reject |
|---|---|---|---|---|
| / | Complianz | ✗ Meta Pixel | Google Analytics 4, Google Ads, Meta Pixel | ✗ Meta Pixel (3 pages) |

### Errors (2)

| Check | Page | What | Fix |
|---|---|---|---|
| consent/before-choice | / | Meta Pixel fired before the visitor chose \(3 hits \(PageView on /, /shop/, /\); cookie \_fbp written\) | Load the tracker only after the visitor accepts: let the consent banner block its script, or start it with consent denied and grant it on Accept. |
| consent/after-reject | / | Meta Pixel fired after Reject \(3 hits \(PageView on /, /shop/, /\); cookie \_fbp written\) | Make Reject stop the tracker: block its script while consent is denied and keep it blocked on every page after the choice. |

### Notices (1)

| Check | Page | What | Fix |
|---|---|---|---|
| consent/meta-no-consent-api | / | Meta Pixel never received fbq\('consent', ...\) \(no fbq\('consent', 'revoke'\) or fbq\('consent', 'grant'\) before the first Meta Pixel hit at 0.84s\) | Meta Pixel doesn't read Google Consent Mode. Call fbq\('consent', 'revoke'\) before fbq\('init'\), then fbq\('consent', 'grant'\) on Accept. |

### Runs

<details><summary>/ · No choice</summary>

- 0.00s load https://meta-ignores.consent.test/
- 0.12s gtag consent default  all four denied
- 0.30s script googletagmanager.com/gtm.js?id=GTM-••••XK2
- 0.41s banner Complianz shown
- 0.61s script connect.facebook.net/en\_US/fbevents.js
- 0.84s hit Meta Pixel PageView \(held\)
- 0.86s cookie \_fbp \(Meta Pixel\)
- 9.40s page /shop/ \(link 1 of 1\)
- 9.80s hit Meta Pixel PageView \(held\)
- 12.90s reload https://meta-ignores.consent.test/
- 13.30s hit Meta Pixel PageView \(held\)

</details>

<details><summary>/ · Accept</summary>

- 0.00s load https://meta-ignores.consent.test/
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
- 9.40s page /shop/ \(link 1 of 1\)
- 9.70s hit Google Analytics 4 page\_view gcs=G111 gcd=13r3r3r3r5 \(held\)
- 9.74s hit Google Ads gcs=G111 gcd=13r3r3r3r5 \(held\)
- 9.78s hit Meta Pixel PageView \(held\)
- 12.90s reload https://meta-ignores.consent.test/
- 13.20s hit Google Analytics 4 page\_view gcs=G111 gcd=13r3r3r3r5 \(held\)
- 13.24s hit Google Ads gcs=G111 gcd=13r3r3r3r5 \(held\)
- 13.28s hit Meta Pixel PageView \(held\)

</details>

<details><summary>/ · Reject</summary>

- 0.00s load https://meta-ignores.consent.test/
- 0.12s gtag consent default  all four denied
- 0.30s script googletagmanager.com/gtm.js?id=GTM-••••XK2
- 0.41s banner Complianz shown
- 1.98s click "Deny" \(.cmplz-deny\)
- 2.00s gtag consent update   all four denied
- 2.20s hit Meta Pixel PageView \(held\)
- 2.21s cookie \_fbp \(Meta Pixel\)
- 9.40s page /shop/ \(link 1 of 1\)
- 9.80s hit Meta Pixel PageView \(held\)
- 12.90s reload https://meta-ignores.consent.test/
- 13.30s hit Meta Pixel PageView \(held\)

</details>

### Consent Mode

Default: v2, all four signals denied at 0.12s. Accept: update granted at 2.0s. Reject: update denied at 2.0s. gcd decoded with an unofficial mapping.

_Evidence, not legal advice. Echo 1.2.0, Chrome 156.0.8078.47, 2026-10-26 09:20 UTC. Hits held in the browser. Hosts mapped with --map-host. consent.json sha256 e3b0c44298fc..._
