# Cookiebot replica

captured: 2026-10-26

Written fresh for the testbed; no Cookiebot code is copied. The element
IDs, the CookieConsent cookie format and the JavaScript API
(`Cookiebot.consent`, `submitCustomConsent(preferences, statistics,
marketing)`, `renew`, `withdraw`, the `CookiebotOnAccept`,
`CookiebotOnDecline` and `CookiebotOnConsentReady` events, the
`data-cookieconsent` script marking, `data-blockingmode` and
`data-consentmode`) follow these public pages:

- https://www.cookiebot.com/en/developer/
- https://usercentrics.com/docs/web/cookiebot/cookiebot-sdk

The markup around the IDs is simplified: one dialog, the buttons and the
checkboxes Echo's rule (consent/cmp/rules/cookiebot.yaml) names.
