## ♪ Echo consent

**Findings.** 1 page, 3 runs, 3 trackers, policy advanced: Meta Pixel fired before the choice and after Reject.

| Page | Banner | No choice | Accept | Reject |
|---|---|---|---|---|
| / | Complianz | ✗ Meta Pixel | Google Analytics 4, Google Ads, Meta Pixel | ✗ Meta Pixel (3 pages) |

### Errors (2)

| Check | Page | Run | What | Fix |
|---|---|---|---|---|
| consent/before-choice | / | No choice | Meta Pixel fired before the visitor chose | Load the tracker only after the visitor accepts: let the consent banner block its script, or start it with consent denied and grant it on Accept. |
| consent/after-reject | / | Reject | Meta Pixel fired after Reject | Make Reject stop the tracker: block its script while consent is denied and keep it blocked on every page after the choice. |

### Notices (1)

| Check | Page | Run | What | Fix |
|---|---|---|---|---|
| consent/meta-no-consent-api | / | No choice | Meta Pixel never received fbq\('consent', ...\) | Meta Pixel doesn't read Google Consent Mode. Call fbq\('consent', 'revoke'\) before fbq\('init'\), then fbq\('consent', 'grant'\) on Accept. |

### Consent Mode

Default: v2, all four signals denied at 0.12s. Accept: update granted at 2.0s. Reject: update denied at 2.0s. gcd decoded with an unofficial mapping.

_Evidence, not legal advice. Echo 1.2.0, Chrome 156.0.8078.47, 2026-10-26 09:20 UTC. Hits held in the browser. Hosts mapped with --map-host. consent.json sha256 e3b0c44298fc..._
