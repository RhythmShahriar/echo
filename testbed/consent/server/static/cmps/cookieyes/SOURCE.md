# CookieYes replica (Go mode)

captured: 2026-10-26

Docker mode runs the real CookieYes plugin (GPL) from wordpress.org; this
replica stands in for it where there is no WordPress. Written fresh from
the public markup (`.cky-consent-container`, `data-cky-tag` buttons,
`.cky-switch`, `.cky-btn-revisit-wrapper`), the `cookieyes-consent`
cookie, `getCkyConsent()` and the `cookieyes_consent_update` event:

- https://www.cookieyes.com/documentation/retrieving-consent-data-using-api-getckyconsent/
- https://www.cookieyes.com/documentation/change-cookie-consent-using-cookieyes/
- https://fr.matomo.org/faq/how-to/using-cookieyes-consent-manager-with-matomo
