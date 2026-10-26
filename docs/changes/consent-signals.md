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
- Until the TCF and GPP decoders land, `TCF` and `GPP` return
  `consent: decoder not built yet`.
