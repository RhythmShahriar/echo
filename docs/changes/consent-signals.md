# Consent signals: changes

User-visible changes from the consent signal decoders (`consent/signal`),
one bullet each with the exact text.

## Decoders

- gcs: `G1` plus `0` (denied), `1` (granted) or `-` (unset) for
  ad_storage then analytics_storage. Any other value decodes as
  `"known": false` with both values `unknown`.
- gcd: a decoded gcd always comes with the line
  `decoded with an unofficial, community-documented mapping`
  (`signal.GCDUnofficial`; consumers print it).
- gcd: a value using the letter `e` carries the note
  `letter e is documented by one source` (`GCD.Note`).
- gcd: the part after the four signals is kept in `GCD.Extra`, made safe to
  print and capped at 64 characters.
- Hook records: a record over 4 KiB fails with `hook record too large`; one
  nested deeper than 10 levels fails with `hook record too deep`; broken
  JSON fails with `hook record: <the JSON error>`.
- Hook records: `Signal.Raw` is the masked record, at most 4 KiB, ending
  ` (cut)` when it or the record was cut.
- Consent Mode `region` entries longer than 10 characters or with
  characters other than letters, digits and `-` are dropped; at most 300
  are kept. `wait_for_update` is clamped to 0 to 30000 ms.
- `gtag('set', {ads_data_redaction: true})` (object form) is read as a
  `set` signal like `gtag('set', 'ads_data_redaction', true)`.
- `fbq('init', id, ...)`: `Signal.Raw` is rebuilt as
  `{"src":"fbq","args":["init","••••<last 4>"]}`; the further arguments
  (advanced matching data) are never kept.
- TCF: the core segment of a TC string is decoded (version, CMP ID,
  purposes with consent and with legitimate interest, the count of
  vendors with consent; overlapping vendor ranges count once). Errors:
  `TC string too large` (over 64 KiB), `TC string is not base64url`,
  `TC string too short`, `TC string version <n> is not 2` and
  `TC string vendor section too large` (MaxVendorId or NumEntries over
  10,000). Trailing `=` padding is accepted.
- GPP: the header's section IDs are listed by name (`tcfeuv2`,
  `tcfcav1`, `uspv1`, `usnat`, `usca`, `usva`, `usco`, `usut`, `usct`,
  others as `id:<n>`). `sale_opt_out` and `sharing_opt_out` read
  `denied` (opted out), `granted` (did not), `unset` (not applicable) or
  `unknown` (unreadable section); usnat wins over uspv1; uspv1 has
  no sharing field (`unset`). `valid` is false when the header lists a
  different number of sections than the string carries. A bare US
  Privacy string (`1YNN`) is read on its own; the third character is the
  opt-out of sale (`1YNN` granted, `1YYN` denied, `1---` unset). Errors:
  `GPP string too large` (over 16 KiB), `GPP string is not base64url`,
  `GPP string too short`, `GPP string type <n> is not 3`,
  `GPP header lists too many sections` (over 64) and
  `US Privacy string unreadable`.

## CMP stored state

- Parsers (`verify.parser`): `cookiebot`, `onetrust`, `cookieyes`,
  `complianz`, `usercentrics`, `borlabs`, `didomi`, `iubenda`, `tcf`,
  `banner`. Category names are the CMP's own: Cookiebot `necessary`,
  `preferences`, `statistics`, `marketing`; OneTrust the group IDs
  (`C0001` to `C0005` and any the site adds); CookieYes `necessary`,
  `functional`, `analytics`, `performance`, `advertisement`, `other`;
  Complianz the part after `cmplz_` (`functional`, `preferences`,
  `statistics`, `marketing`); Usercentrics `services_granted`,
  `services_denied` and each service by name; Borlabs the group names
  (`essential`, `statistics`, `marketing`, `external-media`); Didomi the
  purpose IDs; iubenda the purpose numbers `1` to `5`; TCF none (the
  decoded string is in `tcf`).
- `source` values: `cookie:CookieConsent`, `cookie:OptanonConsent`,
  `cookie:cookieyes-consent`, `cookie:cmplz_*`, `storage:uc_settings`,
  `storage:ucData`, `storage:uc_user_interaction`,
  `cookie:borlabs-cookie`, `cookie:didomi_token`, `cookie:_iub_cs-<id>`,
  `tcf`, `api` and `banner`.
- A stored value a parser can't read gives an undecided state and the
  error `consent: <cmp> state in <cookie or key> unreadable` (the value
  is never in it), unless the CMP's API state says decided.
- Unknown parser: `consent: no state parser "<name>"`.
- A rule with no cookies or storage keys reads the parser's defaults
  (for example `euconsent-v2` for `tcf`). A prefix name (`_iub_cs-*`)
  reads at most 10 cookies, sorted by name.
- Category names are made safe to print, capped at 64 characters and
  100 per state.
- Cookiebot's `CookieConsent=-1` (no consent needed in the visitor's
  region) is decided with no categories. Spec deviation: the state type
  has no note field, so the "no consent needed" note isn't carried.
- Usercentrics: the spec asked for granted and denied counts;
  categories are booleans, so `services_granted` and `services_denied`
  say whether any service has or lacks consent.
- API state (`verify.state_js`, else `api.state`): at most 64 KiB; a
  failing script is ignored. Read shapes: the generic
  `{"decided": bool, "categories": {...}}` for any parser; Cookiebot's
  `Cookiebot.consent`; OneTrust's `{"groups": OnetrustActiveGroups}`
  (never decided); CookieYes's `getCkyConsent()`; Usercentrics's
  per-service status list (never decided); Borlabs's
  `BorlabsCookie.Consents.getAll()` (decided when not null).
