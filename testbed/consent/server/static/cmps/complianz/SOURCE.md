# Complianz replica (Go mode)

captured: 2026-10-26

Docker mode runs the real Complianz plugin (GPL) from wordpress.org; this
replica stands in for it where there is no WordPress. Written fresh from
the plugin's public markup and docs: `#cmplz-cookiebanner-container`,
`.cmplz-cookiebanner`, `.cmplz-accept`, `.cmplz-deny`,
`.cmplz-view-preferences`, `.cmplz-save-preferences`,
`.cmplz-consent-checkbox`, the `cmplz-show-banner` reopen class and the
`cmplz_<category>` cookies.

- https://wordpress.org/plugins/complianz-gdpr/
- https://complianz.io/add-a-link-to-show-the-cookie-banner/
- https://complianz.io/consent-mode-for-7-0

The `cmplz_status_change` event name is taken from the plugin's script and
was not found in the public docs.
