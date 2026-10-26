## ♪ Echo consent

**Broke.** 1 page, 3 runs, 4 trackers, policy advanced: 2 broke, 1 new, 0 changed, 0 fixed against baseline.

| Page | Banner | No choice | Accept | Reject |
|---|---|---|---|---|
| / | Complianz | ✗ TikTok Pixel | Google Analytics 4, Google Ads, Meta Pixel, TikTok Pixel | ✗ TikTok Pixel |

### Errors (2)

| Check | Page | What | Fix |
|---|---|---|---|
| consent/before-choice | / | TikTok Pixel fired before the visitor chose | Load the tracker only after the visitor accepts: let the consent banner block its script, or start it with consent denied and grant it on Accept. |
| consent/before-choice | / | TikTok Pixel fired before the visitor chose | as above |

### Consent Mode

Default: v2, all four signals denied at 0.12s. Accept: update granted at 2.0s. Reject: update denied at 2.0s. gcd decoded with an unofficial mapping.

### Changes against baseline (3)

| Change | What | Page | Run | Before and after |
|---|---|---|---|---|
| BROKE | TikTok Pixel now fires before the choice | / | No choice |  |
| BROKE | TikTok Pixel now fires before the choice | / | Reject |  |
| NEW | TikTok Pixel fires after Accept | / | Accept |  |

_Evidence, not legal advice. Echo 1.2.0, Chrome 156.0.8078.47, 2026-10-26 09:20 UTC. Hits held in the browser. Hosts mapped with --map-host. consent.json sha256 e3b0c44298fc..._
