# Consent verdict: user-visible changes

Every change the verdict lane makes to what people see: issue titles and
details, run details, inconclusive texts, notes and the fields of the
result. Exact texts are in backticks.

## V1 Page summaries

- Each page's `consent_mode` is filled from the none run (else the first run with a Consent Mode command or a Google hit with gcs or gcd): `present`, `v2` (the default that applies to `--geo` sets both `ad_user_data` and `ad_personalization`), `default_at_ms`, `first_google_hit_ms`, `default_before_first_hit` (true when there is a default and no Google hit, or the default came at or before the first hit), `default`, `wait_for_update_ms` and `region` of that default. A region-scoped default for the `--geo` region wins over a global one.
- Google hits (GA4, Universal Analytics, Google Ads, Floodlight, Google Ads remarketing, Google CCM) sent with a known gcs that has `ad_storage` and `analytics_storage` denied get `cookieless: true` when no Google cookie (`_ga*`, `_gid`, `_gcl_*`, `FPID`, `IDE`) was stored in the same phase before the hit or within 1 s after it. A cookie the browser refused does not count.
- `needs_no_consent` is true when no run found a banner or made a choice, no CMP was detected and the none run had no consent-required hit, cookie, storage key or tracker script (`--exempt` trackers never count; a script of a tracker the catalog doesn't know counts as needing consent). On such a page the Accept, Reject and Withdraw runs that ended `no-banner` lose their reason and get the detail `No banner and no trackers: nothing to consent to`.
