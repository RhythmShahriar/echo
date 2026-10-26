# OneTrust replica

captured: 2026-10-26

Written fresh for the testbed; no OneTrust code is copied. The element IDs
and classes (`#onetrust-banner-sdk`, `#onetrust-accept-btn-handler`,
`#onetrust-reject-all-handler`, `#onetrust-pc-btn-handler`,
`#onetrust-pc-sdk`, `.category-switch-handler`,
`.save-preference-btn-handler`, `#ot-sdk-btn`), the OptanonConsent and
OptanonAlertBoxClosed cookies, `OnetrustActiveGroups`, `OptanonWrapper`,
the `OneTrustGroupsUpdated` event and the `optanon-category-C000n` script
marking follow these public pages:

- https://my.onetrust.com/articles/en_US/Knowledge/UUID-518074a1-a6da-81c3-be52-bae7685d9c94
- https://docs.lokker.com/docs/onetrust/onetrust-best-practices

`OneTrust.AllowAll`, `RejectAll` and `ToggleInfoDisplay` are the names in
Echo's rule (consent/cmp/rules/onetrust.yaml); the public pages above did
not show `RejectAll`, so check it when this replica is refreshed.
