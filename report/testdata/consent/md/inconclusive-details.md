## ♪ Echo consent

**Couldn't check.** 1 page, 3 runs, 0 trackers, policy advanced: Accept and Reject couldn't be tested on 1 page.

| Page | Banner | No choice | Accept | Reject |
|---|---|---|---|---|
| / | none |  | ? no banner | ? no banner |

### Runs

<details><summary>/ · No choice</summary>

- 0.00s load https://e13.consent.test/
- 0.20s script consent.cookiebot.com/uc.js
- 9.40s page /shop/ \(link 1 of 1\)
- 12.90s reload https://e13.consent.test/

</details>

<details><summary>/ · Accept</summary>

- 0.00s load https://e13.consent.test/

</details>

<details><summary>/ · Reject</summary>

- 0.00s load https://e13.consent.test/

</details>

### Couldn't check (2)

| Page | Run | Why |
|---|---|---|
| / | Accept | Accept: no banner showed within 10s \(Cookiebot's script loaded\) |
| / | Reject | Reject: no banner showed within 10s \(Cookiebot's script loaded\) |

Many banners show only to visitors in some countries: try --geo de, or an EU --proxy.

A banner Echo doesn't know: --accept-selector and --reject-selector (consent detect helps).

_Evidence, not legal advice. Echo 1.2.0, Chrome 156.0.8078.47, 2026-10-26 09:20 UTC. Hits held in the browser. Hosts mapped with --map-host. consent.json sha256 e3b0c44298fc..._
