## ♪ Echo consent

**Couldn't check.** 1 page, 3 runs, 0 trackers, policy advanced: No choice, Accept and Reject couldn't be tested on 1 page.

| Page | Banner | No choice | Accept | Reject |
|---|---|---|---|---|
| /b.html | Generic banner | ? tracker scripts were refused | ? tracker scripts were refused | ? tracker scripts were refused |

### Runs

<details><summary>/b.html · No choice</summary>

- 0.00s load http://gtm.test/b.html
- 0.04s script googletagmanager.com/gtag/js?id=G-••••2345
- 0.04s script connect.facebook.net/en\_US/fbevents.js
- 0.30s banner Generic banner shown
- 2.60s reload http://gtm.test/b.html

</details>

<details><summary>/b.html · Accept</summary>

- 0.00s load http://gtm.test/b.html
- 0.04s script googletagmanager.com/gtag/js?id=G-••••2345
- 0.04s script connect.facebook.net/en\_US/fbevents.js
- 0.30s banner Generic banner shown
- 1.20s click "Accept all" \(\#cookie-banner button:nth-of-type\(1\)\)
- 2.60s reload http://gtm.test/b.html

</details>

<details><summary>/b.html · Reject</summary>

- 0.00s load http://gtm.test/b.html
- 0.04s script googletagmanager.com/gtag/js?id=G-••••2345
- 0.04s script connect.facebook.net/en\_US/fbevents.js
- 0.30s banner Generic banner shown
- 1.20s click "Reject all" \(\#cookie-banner button:nth-of-type\(2\)\)
- 2.60s reload http://gtm.test/b.html

</details>

### Couldn't check (3)

| Page | Run | Why |
|---|---|---|
| /b.html | No choice | No choice: the tracker scripts of google-tag-manager and meta-pixel were refused by --map-only, so they never ran and weren't checked; drop --map-only to let them load |
| /b.html | Accept | Accept: the tracker scripts of google-tag-manager and meta-pixel were refused by --map-only, so they never ran and weren't checked; drop --map-only to let them load |
| /b.html | Reject | Reject: the tracker scripts of google-tag-manager and meta-pixel were refused by --map-only, so they never ran and weren't checked; drop --map-only to let them load |

_Evidence, not legal advice. Echo 1.2.0, Chrome 156.0.8078.47, 2026-10-26 09:20 UTC. Hits held in the browser. Hosts mapped with --map-host. consent.json sha256 e3b0c44298fc..._
