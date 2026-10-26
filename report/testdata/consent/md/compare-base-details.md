## ♪ Echo consent

**All clear.** 1 page, 3 runs, 3 trackers, policy advanced.

| Page | Banner | No choice | Accept | Reject |
|---|---|---|---|---|
| / | Complianz |  | Google Analytics 4, Google Ads, Meta Pixel |  |

### Runs

<details><summary>/ · No choice</summary>

- 0.00s load https://c08.consent.test/
- 0.12s gtag consent default  all four denied
- 0.30s script googletagmanager.com/gtm.js?id=GTM-••••XK2
- 0.41s banner Complianz shown
- 0.61s script connect.facebook.net/en\_US/fbevents.js
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
- 9.40s page /shop/ \(link 1 of 1\)
- 9.70s hit Google Analytics 4 page\_view gcs=G111 gcd=13r3r3r3r5 \(held\)
- 9.74s hit Google Ads gcs=G111 gcd=13r3r3r3r5 \(held\)
- 9.78s hit Meta Pixel PageView \(held\)
- 12.90s reload https://c08.consent.test/
- 13.20s hit Google Analytics 4 page\_view gcs=G111 gcd=13r3r3r3r5 \(held\)
- 13.24s hit Google Ads gcs=G111 gcd=13r3r3r3r5 \(held\)
- 13.28s hit Meta Pixel PageView \(held\)

</details>

<details><summary>/ · Reject</summary>

- 0.00s load https://c08.consent.test/
- 0.12s gtag consent default  all four denied
- 0.30s script googletagmanager.com/gtm.js?id=GTM-••••XK2
- 0.41s banner Complianz shown
- 1.98s click "Deny" \(.cmplz-deny\)
- 2.00s gtag consent update   all four denied
- 9.40s page /shop/ \(link 1 of 1\)
- 12.90s reload https://c08.consent.test/

</details>

### Consent Mode

Default: v2, all four signals denied at 0.12s. Accept: update granted at 2.0s. Reject: update denied at 2.0s. gcd decoded with an unofficial mapping.

_Evidence, not legal advice. Echo 1.2.0, Chrome 156.0.8078.47, 2026-10-19 08:02 UTC. Hits held in the browser. Hosts mapped with --map-host. consent.json sha256 e3b0c44298fc..._
