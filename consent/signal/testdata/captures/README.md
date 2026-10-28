# Real-hit captures for the gcs and gcd decoders

Echo's testbed serves a stand-in for `gtag.js`, so the testbed checks Echo
against our model of Google. The gcd letter table is unofficial and
community-documented. This capture checks both against what the real
`gtag.js` sends. The maintainer runs it by hand. It is not run in CI or
in the testbed. Nothing in `go test` contacts Google.

## Rules

- Only on the owner's own staging property, with his own GA4 test
  measurement ID. Never on a client's site or anyone else's property.
- Copy only the `gcs` and `gcd` values into the fixtures. Never the
  measurement ID, client ID, cookies, page URL or any other parameter.
- A captured value that doesn't decode is a finding: fix the letter table
  in `../../gcd.go` (with its source) or open an issue. Don't keep it as an
  unknown row.

## Steps

1. Open the staging page in a fresh Chrome profile with DevTools open on
   the Network tab, filtered by `collect?`. Keep "Preserve log" on.
2. Set the Consent Mode default the page should test (all denied, with
   `wait_for_update` of 500 ms, then a second pass with
   `analytics_storage` granted by default).
3. For each consent state below, load the page and take the first
   `page_view` hit after the state is in place. From its query string
   copy `gcs` and `gcd`.
   - No choice (banner left open).
   - Accept all.
   - Reject all.
   - Analytics only (the CMP's settings layer).
   - Withdraw: accept, then reopen the banner and reject.
   - Advanced mode with no choice (cookieless ping).
4. Add one row per hit to `../gcd.yaml`:

   ```yaml
   - raw: 13r3r3p3p5
     known: true
     signals: [denied/granted, denied/granted, denied/unset, denied/unset]
     gcs: G111
     gcs_signals: [granted, granted]
     source: "capture: accept all, gtag.js on staging"
     date: 2026-10-26
   ```

   `source` starts with `capture` and names the state. `date` is the day
   of the capture (YYYY-MM-DD). `signals` is what the hit should mean
   for that state, so a wrong letter table fails the test.
5. Run `GOWORK=off go test ./consent/signal/` and fix any failure before
   committing. `TestFixtureRowsHaveSource` refuses a capture row without
   `gcs`, `gcs_signals` or a date, or one that doesn't decode.
6. Note the capture date in the benchmark notes, so readers can see when
   the decoders were last checked against real hits.

Repeat after a Consent Mode change from Google or when a community source
adds a letter.
