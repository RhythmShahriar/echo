# Borlabs Cookie replica

captured: 2026-10-26

Written fresh for the testbed (Borlabs Cookie is a commercial plugin; no
code is copied). Borlabs 2 markup (`#BorlabsCookieBox`,
`._brlbs-box-wrap`, `._brlbs-btn-accept-all`, `[data-cookie-accept-all]`,
`._brlbs-refuse-btn`, `[data-cookie-refuse]`, `[data-cookie-individual]`)
and the Borlabs 3 markup (`data-borlabs-cookie-actions`), the
`borlabs-cookie` cookie and `window.BorlabsCookie.Consents` (`hasConsent`,
`hasConsentForServiceGroup`) with the `borlabs-cookie-consent-saved`
event follow:

- https://borlabs.io/kb/javascript-api/
- https://borlabs.io/kb/javascript-api-v3/

Blocked scripts use `type="text/plain" data-borlabs-cookie-group`, a
simplification of the plugin's content and script blockers.
