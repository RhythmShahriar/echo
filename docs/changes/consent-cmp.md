# Consent checks: CMP rules, detection and clicking

User-visible changes of the CMP driver (`consent/cmp`), with the exact
texts, for the release notes and the docs team.

## C1 Rule format, loader and data hash

- The rule pack is compiled in from `consent/cmp/rules/<id>.yaml`; the
  format is documented in `consent/cmp/rules/README.md`.
- `consent cmps` data (`Rules()`): DETECTED BY is the first detect
  selector and the first global joined by `, `, the global written as
  `window.<name>` and a shadow-DOM selector as `<host> (shadow DOM)`,
  e.g. `#CybotCookiebotDialog, window.Cookiebot`. ACCEPT is `button` or
  `button, api`. REJECT is `first layer` or `first layer or settings`,
  plus `, api` when the rule has an API reject. STATE FROM is
  `<cookie> cookie` (`<prefix>* cookies` for a prefix), `<key> storage`,
  `TCF string`, `CMP API` or `banner gone and stays gone`.
- The rules version is `1.2.0` (`consent.DataVersion`); the hash is the
  SHA-256 of the rule files concatenated in name order:
  `cat $(ls consent/cmp/rules/*.yaml | sort) | sha256sum`.
- `--cmp` naming no rule: `--cmp "<name>": want auto or one of <ids>`
  (ids comma-separated in rule order).
- Rule loading errors (build time only; a broken pack fails the tests
  and panics at start with `consent/cmp: <error>`) always name the file
  and a line: `<file>: line <n>: unknown key "<key>"`,
  `<file>: line <n>: api.reject: "eval(" is not allowed in a rule's JavaScript; rules make fixed calls only`,
  `<file>: line <n>: <key>: missing`.

## C2 Detection

- `Detect` looks every 250 ms until `--banner-wait` and stops at the
  first look that sees a banner. With `--cmp <id>` only that rule is
  tried (generic is not); with `--cmp auto` the named rules by priority,
  then generic.
- A banner counts as shown when its container is sized, not
  `display:none`, not `visibility:hidden`, not transparent (opacity
  above 0.01, ancestors included) and inside the page or scrollable into
  it. Markup that is there but hidden is `present`, not `found`.
- `present` comes from a detect selector, a window global (typeof only)
  or a loaded script URL (resource timing and `<script src>`). Page text
  is never read, so a page that only names a CMP is not present.
- `detected_by` in the JSON holds at most two pieces joined by ` and `,
  the first of each kind, selector first, then global, then script:
  `selector:#CybotCookiebotDialog and global:Cookiebot`,
  `global:Cookiebot and script:consent.cookiebot.com/uc.js`, or
  `generic` for the generic rule. (`consent detect` prints it as
  `#CybotCookiebotDialog and window.Cookiebot`; that rendering is the
  cli lane's.)
- `tcf` is true when the page defines `__tcfapi`; `banner_at_ms` is the
  run-clock time of the look that saw the banner.
- When every look failed (the page kept navigating), Detect returns the
  last script error instead of "no banner".

## C3 Clicking and verification

- Before a click the control must be visible, sized, enabled, the
  top-most element at its centre, inside the banner container (a step
  after the first, or a second-layer step, may be anywhere on the page)
  and a `button`, `[role=button]`, `input[type=button|submit]` or an `a`
  whose `href` stays on the site. Echo waits up to `--banner-wait` for
  that, then the run is `no_button`.
- A Reject first tries the first layer. When it has no Reject after 1 s
  (or at `--banner-wait`), Echo opens the settings, waits for Save,
  switches off every enabled switch that is on, saves and records
  `layer: 2`; `selector` is then `<opener> >> <save>`, e.g.
  `#onetrust-pc-btn-handler >> #onetrust-pc-sdk .save-preference-btn-handler`.
- After a click Echo waits up to `--settle` for the stored state to
  match the choice and the banner to be gone. A click with no effect is
  retried, at most 3 clicks in all (`clicks` in the JSON).
- Reasons: `no_button` (never clickable), `click_missed` (no change
  after 3 clicks, or the control was covered each time),
  `state_unchanged` (the banner went or the state moved while the stored
  choice isn't the one clicked: hiding is never a Reject),
  `navigation_failed` (the click left the site).
- `verified_by` is `cookie:<name>` (or the state reader's source),
  `api`, `tcf` or `banner`. A category the CMP doesn't list counts as
  not granted.
- `--click api` calls the rule's fixed function (`how: api`,
  `selector` is the call, e.g. `OneTrust.RejectAll()`, `clicks: 0`,
  `layer: 0`). A rule with no API gives `no_button`; a call that throws
  gives `click_missed`.
- `--click button,api`: no button gives the API choice with
  `fallback: true`. A Reject button that was `click_missed` or
  `state_unchanged` keeps its reason and sets `api_worked` to whether
  the API reject verified.
- `--accept-selector` and `--reject-selector` replace the rule's
  controls (no second layer). On a banner no rule knows, the choice is
  verified by the clicked control being gone (`verified_by: banner`).
- Without the state reader linked, Choose fails with
  `consent: state reader not linked into this build`.
- A run kind other than accept or reject: `consent: a <kind> run makes no choice on the banner; want accept or reject`.

## C4 Cookiebot and OneTrust rules

- `consent cmps` rows:
  - `cookiebot  #CybotCookiebotDialog, window.Cookiebot  button, api  first layer or settings, api  CookieConsent cookie`
  - `onetrust  #onetrust-banner-sdk, window.OneTrust  button, api  first layer or settings, api  OptanonConsent cookie`
  (the PRD 6.4 table shows Cookiebot's REJECT as `first layer, api`;
  the rule has a second layer for dialogs without Deny, so the column
  reads `first layer or settings, api`).
- Cookiebot: detected by `#CybotCookiebotDialog`, `window.Cookiebot` or
  a `consent.cookiebot.com/uc.js` or `consent.cookiebot.eu/uc.js`
  script. Accept `#CybotCookiebotDialogBodyLevelButtonLevelOptinAllowAll`
  then `#CybotCookiebotDialogBodyButtonAccept`; Reject
  `#CybotCookiebotDialogBodyButtonDecline`, else Customize
  (`#CybotCookiebotDialogBodyLevelButtonCustomize`) or the Details tab
  (`#CybotCookiebotDialogNavDetails`), the level switches off and
  `#CybotCookiebotDialogBodyLevelButtonLevelOptinAllowallSelection`.
  API: `Cookiebot.submitCustomConsent(true,true,true)`,
  `Cookiebot.submitCustomConsent(false,false,false)`, `Cookiebot.renew()`,
  state `JSON.stringify(Cookiebot.consent)`. Verified from the
  `CookieConsent` cookie: Accept needs statistics and marketing on,
  Reject both off.
- OneTrust: detected by `#onetrust-banner-sdk`, `#onetrust-consent-sdk`,
  `window.OneTrust`, `window.Optanon` or a `cdn.cookielaw.org` or
  `optanon.blob.core.windows.net` script. Accept
  `#onetrust-accept-btn-handler`; Reject `#onetrust-reject-all-handler`,
  else Cookie Settings (`#onetrust-pc-btn-handler`), every
  `input.category-switch-handler` off and Confirm My Choices
  (`.save-preference-btn-handler`). API: `OneTrust.AllowAll()`,
  `OneTrust.RejectAll()`, `OneTrust.ToggleInfoDisplay()`, state
  `JSON.stringify({groups: OnetrustActiveGroups})`. Verified from
  `OptanonConsent` and `OptanonAlertBoxClosed`: Reject needs C0002 and
  C0004 off (or absent); Accept needs a decided state only, because
  which groups exist is each site's setup.
- Checked against the vendors' documentation, with these differences
  (the vendor wins where it speaks):
  - Cookiebot's developer page documents the API and the cookie but no
    dialog element IDs; the rule's IDs are the ones the served dialog
    carries.
  - OneTrust's JavaScript API page documents `AllowAll`, `RejectAll`,
    `ToggleInfoDisplay`, `OnetrustActiveGroups` and the two cookies;
    it doesn't mention `Optanon` (an older global, kept as detection
    evidence only) or the element IDs.
  - OneTrust doesn't document `otgeo`; it documents
    `OneTrust.setGeoLocation(country, state)` instead. The rule keeps
    `geo_override: {param: otgeo, value_from: country}` marked
    unverified, used only with `--geo` (C9 adds the note).
- Replica pages for both under `consent/cmp/testdata/<cmp>/` with an
  `expect.yaml` each (format in `consent/cmp/testdata/README.md`); all
  pass Detect, Find and Choose (Accept and Reject, button and API) in
  headless Chrome 153 as well as in the FakeTab tests.

## C5 CookieYes and Complianz rules

- `consent cmps` rows:
  - `cookieyes  .cky-consent-container, window.getCkyConsent  button  first layer or settings  cookieyes-consent cookie`
  - `complianz  #cmplz-cookiebanner-container, window.complianz  button  first layer or settings  cmplz_* cookies`
  (PRD 6.4 shows CookieYes as `button, api` and `first layer, api`:
  CookieYes documents no accept or reject function, only
  `getCkyConsent()`, so the rule has a state call and no API choice.
  Both rules have a settings layer, so REJECT reads
  `first layer or settings`.)
- CookieYes: detected by `.cky-consent-container`, the older Cookie Law
  Info markup (`#cookie-law-info-bar`, `#wt-cli-accept-all-btn`,
  `#wt-cli-reject-btn`), `window.getCkyConsent` or a `cdn-cookieyes.com`
  script. Accept `[data-cky-tag=accept-button]`; Reject
  `[data-cky-tag=reject-button]`, else Customize
  (`[data-cky-tag=settings-button]`), every `.cky-switch` checkbox off
  and Save My Preferences (`[data-cky-tag=detail-save-button]`, in the
  preference center modal outside the banner). Verified from the
  `cookieyes-consent` cookie: Reject needs `analytics` and
  `advertisement` off; Accept needs a decided state (`action:yes`)
  only. A Cookie Law Info bar is reported present, not found: it keeps
  its choice in other cookies the rule can't verify.
- Complianz: detected by `#cmplz-cookiebanner-container`,
  `.cmplz-cookiebanner`, `window.complianz`, `window.cmplz_set_cookie`
  or a `complianz-gdpr/cookiebanner/js/complianz` script (the script is
  an addition to the lane text: the plugin loads it from that path).
  Accept `.cmplz-accept`; Reject `.cmplz-deny`, else View preferences
  (`.cmplz-view-preferences`, which opens the categories inside the
  banner), every `.cmplz-consent-checkbox` off and
  `.cmplz-save-preferences`. Verified from the `cmplz_<category>`
  cookies (`allow` or `deny`): Accept needs statistics and marketing
  allowed, Reject both denied.
- Checked against the plugins' current markup: the banner templates of
  cookie-law-info 3.5.7 and complianz-gdpr 7.5.6 from wordpress.org
  (GPL) carry every class and `data-cky-tag` the rules use; nothing
  differed from the lane text. Complianz also defines
  `window.cmplz_accept_all` and `window.cmplz_deny_all`; they are not
  in its documentation, so the rule doesn't call them.
- Replica pages for both under `consent/cmp/testdata/` (seven standard
  pages each, plus `cookieyes/cookie-law-info.html`), with
  `expect.yaml`. All rows pass Detect, Find and Choose (Accept and
  Reject) in headless Chrome as well as in the FakeTab tests.
