# Iubenda replica

captured: 2026-10-26

Written fresh for the testbed; no iubenda code is copied. The embed
(`_iub.csConfiguration` with `siteId` and `cookiePolicyId`, then
`cdn.iubenda.com/cs/iubenda_cs.js`), `#iubenda-cs-banner` and its
`.iubenda-cs-accept-btn`, `.iubenda-cs-reject-btn` and
`.iubenda-cs-customize-btn`, the `_iub_cs-<id>` cookie, the
`callback` hooks and the `_iub_cs_activate` script marking follow:

- https://www.iubenda.com/en/help/680
- https://www.iubenda.com/it/?p=36984

`_iub.cs.api.acceptAll`, `rejectAll` and `openPreferences` are the names
in Echo's rule (consent/cmp/rules/iubenda.yaml); the public pages above
did not list them, so check them when this replica is refreshed.
