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
- Before a look returns the generic rule, the named-rule scan runs once
  more; the generic scan also reports which named rule's selector or
  banner is visible inside its container (or around it). A named banner
  shown by then, or a container holding a named rule's markup, is that
  rule (`detected_by` its own selector and global), never `generic`. So
  a CMP banner drawn between the two scans of one look is judged by the
  CMP's stored state, not by the banner staying gone.

## C3 Clicking and verification

- Before a click the control must be visible, sized, enabled, the
  top-most element at its centre, inside the banner container (a
  second-layer Save inside the rule's settings dialog,
  `reject.second_layer.dialog`, when the rule names one; a step after the
  first of a chain or the Save of a rule without a dialog may be
  anywhere on the page)
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
  not granted. A state the reader marks decided with no categories
  at all never verifies a choice that names categories: such a Reject
  gives `state_unchanged`.
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
  only. A Cookie Law Info bar alone makes CookieYes present, never
  found: it keeps its choice in other cookies the rule can't verify.
  Shown, that bar is found by the generic rule (C8) instead.
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

## C6 Usercentrics and Borlabs rules

- `consent cmps` rows:
  - `usercentrics  #usercentrics-root (shadow DOM), window.UC_UI  button, api  first layer or settings, api  uc_settings storage`
  - `borlabs  #BorlabsCookieBox, window.BorlabsCookie  button  first layer or settings  borlabs-cookie cookie`
  (PRD 6.4 shows Borlabs as `button, api` and `first layer or
  individual`: Borlabs documents an API to read consents, not to make a
  choice, so the rule has no API choice; the column words are the
  listing's own, `first layer or settings`.)
- DETECTED BY marks a detect selector that is the shadow host of the
  banner with ` (shadow DOM)`, as for a shadow selector.
- Rule format: `banner.selector` and `reject.second_layer.toggles_off`
  take one selector or a list. Any visible banner of the list is the
  banner shown; Accept and Reject controls must sit inside one of them.
- When a selector matches several elements, the first visible one is
  used (a CMP that prints one hidden banner per consent type).
- Closed shadow roots: a banner behind one is found when hit tests land
  on its host (`found: true`). Its buttons can't be reached, so Find and
  Choose return `no_button` at once instead of waiting `--banner-wait`;
  under `--click button,api` the choice is the API's with
  `fallback: true` (the run's `consent/click-fallback`).
- Usercentrics: detected by `#usercentrics-root`, `#usercentrics-cmp-ui`,
  `window.UC_UI`, `window.__ucCmp` or an `app.usercentrics.eu` or
  `web.cmp.usercentrics.eu` script. Banner, Accept, Deny, More and Save
  in the v2 root (`[data-testid=uc-default-banner]`,
  `uc-accept-all-button`, `uc-deny-all-button`, `uc-more-button`,
  `uc-save-button`) and the v3 root (`#uc-main-dialog`, `#accept`,
  `#deny`, `#more`, `#save`, the `#...` names of Usercentrics's custom
  CSS documentation). API calls try v2 first:
  `(window.UC_UI ? UC_UI.acceptAllConsents() : __ucCmp.acceptAllConsents())`,
  the same with `denyAllConsents` and `showSecondLayer` (the lane text
  had the UC_UI calls only; v3 pages have only `__ucCmp`). Verified
  from localStorage `uc_settings` (or `ucData`) and
  `uc_user_interaction`: Reject needs some service denied, Accept none
  (essential services stay granted after Deny). `verified_by` is the
  state reader's source, `storage:uc_settings` or `storage:ucData`.
  The v2 toggles have no documented selector: every enabled checkbox in
  the root is switched off; v3 uses `.uc-toggle`.
- Borlabs Cookie: detected by `#BorlabsCookieBox`, `._brlbs-box-wrap`,
  `.brlbs-cmpnt-container` (Borlabs 3, an addition to the lane text),
  `window.BorlabsCookie` or a `/borlabs-cookie/` script. Accept
  `._brlbs-btn-accept-all`, `[data-cookie-accept-all]` or
  `[data-borlabs-cookie-actions=accept-all]` (the Borlabs 3 button, as
  the testbed lane noted); Reject `[data-cookie-refuse]` or
  `[data-borlabs-cookie-actions=reject]` (`._brlbs-refuse-btn` stays
  first as the lane text has it; in Borlabs 2 it is the paragraph around
  the link, so the link's attribute is what gets clicked); settings
  `[data-cookie-individual]` or `[data-borlabs-cookie-actions=preferences]`,
  the group checkboxes off, Save `[data-cookie-accept]` or
  `[data-borlabs-cookie-actions=save-consents]`. Verified from the
  `borlabs-cookie` cookie: Reject needs the statistics and marketing
  groups empty or absent; Accept a decided state.
- Reopen data (used by the withdraw run): Usercentrics `#uc-privacy-button`
  in either root or the API; Borlabs `.borlabs-cookie-open-dialog-preferences`
  (Borlabs 3 docs) and `.borlabs-cookie-preference`.
- Replica pages for both (seven standard pages, plus
  `usercentrics/closed-shadow.html` and `borlabs/version-3.html`; the
  Usercentrics second-layer and two-banners pages use the v3 markup).
  All rows pass Detect, Find and Choose in headless Chrome and in the
  FakeTab tests; on the closed root `--click button` gives `no_button`
  in a few ms and `--click button,api` a verified API choice.

## C7 Didomi and iubenda rules

- `consent cmps` rows:
  - `didomi  #didomi-host, window.Didomi  button, api  first layer or settings, api  didomi_token cookie`
  - `iubenda  #iubenda-cs-banner, window._iub  button, api  first layer or settings, api  _iub_cs-* cookies`
  (PRD 6.4 shows Didomi's REJECT as `first layer or learn more, api`,
  iubenda's as `first layer, api` and its STATE FROM as
  `_iub_cs-* cookie`; the listing's own words are used.)
- Rule format: `reject.second_layer.press_off` (one selector or a
  list): buttons that switch a purpose off once pressed; each one whose
  `aria-pressed` isn't `true` is clicked once. `verify.tcf: true`: when
  the CMP's own state is undecided, the TC string (`euconsent-v2`) is
  read; Accept needs purpose 1 consented, Reject no purpose consented;
  `verified_by` is then `tcf`.
- A stored value the state reader can't read (it returns an undecided
  state with an error) now counts as a choice not verified
  (`state_unchanged` or `click_missed`), not a failed run.
- Didomi: detected by `#didomi-host`, `#didomi-notice`, `window.Didomi`,
  `window.didomiOnReady` or an `sdk.privacy-center.org` script. Accept
  `#didomi-notice-agree-button`; Reject `#didomi-notice-disagree-button`,
  else Learn More (`#didomi-notice-learn-more-button`), every purpose's
  Disagree pressed (`.didomi-consent-popup-categories button[data-choice=disagree]`
  or `.didomi-components-radio__option--disagree`) and Save
  (`.didomi-consent-popup-actions button[aria-label*=Save]`). API:
  `Didomi.setUserAgreeToAll()`, `Didomi.setUserDisagreeToAll()`,
  `Didomi.preferences.show()`, state
  `JSON.stringify(Didomi.getCurrentUserStatus())`. Verified from
  `didomi_token` (purpose IDs: Accept needs `cookies`, Reject `cookies`
  and `create_ads_profile` off), else from `euconsent-v2`. `tcf` is
  true when the page defines `__tcfapi`.
- iubenda: detected by `#iubenda-cs-banner`, `window._iub` or a
  `cdn.iubenda.com` or `cs.iubenda.com` script. Accept
  `.iubenda-cs-accept-btn`; Reject `.iubenda-cs-reject-btn`, else
  Customize (`.iubenda-cs-customize-btn`), every
  `#iubenda-iframe .purposes-item-input` off and
  `#iubenda-iframe .iubenda-cs-save-btn`. API:
  `_iub.cs.api.acceptAll()`, `_iub.cs.api.rejectAll()`,
  `_iub.cs.api.openPreferences()`, state
  `JSON.stringify(_iub.cs.api.getPreferences ? _iub.cs.api.getPreferences() : null)`.
  Verified from the `_iub_cs-<id>` cookie: Reject needs purposes 4
  (measurement) and 5 (marketing) off; Accept a decided state.
- Checked against the vendors' documentation: Didomi's Web SDK
  reference has the four calls (it names `shouldUserStatusBeCollected`,
  not the `shouldConsentBeCollected` some guides use); iubenda's
  advanced guide lists `acceptAll`, `rejectAll`, `openPreferences` and
  `getPreferences` (the testbed lane hadn't found them; they are
  there) and the `iubenda-cs-preferences-link` reopen class. Neither
  documents its element IDs and classes; they are the ones the served
  banners carry. iubenda's preferences panel selectors follow the
  testbed lane's replica and are unverified on a live site.
- Reopen data: Didomi and iubenda through the API, iubenda also
  `.iubenda-cs-preferences-link`.
- Replica pages for both (seven standard pages, plus `didomi/tcf.html`
  and `iubenda/floating.html`). All rows pass Detect, Find and Choose
  (button and `--click api`) in headless Chrome (the TCF page verified
  by its TC string) as well as in the FakeTab tests.

## C8 Generic rule

- `consent cmps` row:
  `generic  buttons whose text means accept or reject (30 languages)  button  first layer or settings  banner gone and stays gone`.
- The rules hash now also covers the generic rule's words:
  `cat $(ls consent/cmp/rules/*.yaml | sort) consent/cmp/generic_words.yaml | sha256sum`
  (it replaces the C1 command).
- Detection (`--cmp auto` after every named rule, or `--cmp generic`):
  a visible element that is fixed, sticky, has z-index 1000 or more, or
  is `role=dialog`, `role=alertdialog` or `aria-modal=true` (the
  outermost of nested ones), in the page or a same-origin frame, whose
  text has a cookie noun (cookie, piškotk, sīkdat, slapuk, küpsis, fianá,
  бисквитк, κουκίδ, çerez, ملفات تعريف الارتباط, עוגיות, vafrakök,
  informasjonskaps, eväste, süti, kolačić, sušen) or a consent word
  (consent, Einwilligung, Zustimmung, consentement, consenso,
  toestemming, zgoda, consentimento, samtycke, samtykke, suostumus,
  souhlas, súhlas, hozzájárulás, privatnost and the Bulgarian, Ukrainian,
  Greek, Arabic and Hebrew words), with at least one clickable control
  whose text means Accept or Reject. `detected_by: generic`, `present`
  equals `found`.
- Ignored: an element with a newsletter, subscribe, login, password,
  cart, checkout, shipping, delivery, age (over 18, älter als 18...),
  chat, returns or coupon word. When it also has a cookie noun it is a
  banner only when one of its clickable controls opens or saves
  settings, or accepts or rejects with a cookie or consent word in its
  own text ("Accept cookies"). So a newsletter or an age gate that
  mentions cookies ("We use cookies to remember this offer", buttons
  "Accept offer" and "No thanks") is never a banner, while a real banner
  that mentions a basket or a return visit and has a "Cookie settings"
  button stays one.
- Button text: NFKC, lower case, combining marks dropped (so accents and
  Greek tonos don't matter; an addition to the lane's NFKC), zero-width
  and other format characters removed, other non-letters read as
  spaces. Phrases in `consent/cmp/generic_words.yaml`: accept, reject,
  settings and save for bg cs da de el en es et fi fr ga hr hu it lt lv
  mt nl pl pt ro sk sl sv no is tr uk ar he (at least three accept and
  three reject each; the loader refuses a phrase with two meanings). The
  longest phrase found as whole words decides: "Continuer sans accepter"
  and "Accept only necessary" are Reject, "Accept selection" is Save. A
  text that is exactly a never_choice phrase (continue, close, ok, got
  it, x, ×, more, settings, learn more, privacy policy and their
  translations) is never Accept or Reject; a tie between meanings or a
  text over 80 characters is nothing.
- Never clicked: a link that loads another page or whose address holds a
  policy word (privacy, cookie, policy, datenschutz, politique,
  privacidad, informativa, polityka...).
- Reject: a Reject button; else, after 1 s, the settings button
  (`layer: 2`), then a Save (or Reject) button in a cookie dialog, with
  every enabled checkbox or `role=switch` of that dialog switched off;
  `selector` is `<opener> >> <save>`. `selector` of a generic choice is
  the control's CSS path, e.g.
  `html:nth-of-type(1) > body:nth-of-type(1) > div:nth-of-type(2) > button:nth-of-type(1)`,
  prefixed `frame(<url>) ` in a frame.
- Verified (`verified_by: banner`) when the banner is gone after the
  click and still gone for `--settle` after Echo reloads the page; a
  banner that comes back gives `state_unchanged`. The generic rule
  reloads the page once inside Choose.
- Fixtures: `consent/cmp/testdata/generic/` (12 positive pages, 9
  negative pages, 30 generated language pages) with `expect.yaml`. All
  pass Detect, Find and Choose in headless Chrome and in the FakeTab
  tests; on the negative pages nothing is found and `--cmp generic`
  clicks nothing (`no_button`).

## C9 Reopen, BannerShowing, Find and geo override

- Withdraw run, reopen step: Echo clicks the first of the rule's reopen
  controls that shows within `--banner-wait` (a footer or floating
  "cookie settings" button). When none shows, it calls the CMP's own
  reopen function (`how` stays `button` for the Reject that follows)
  only under `--click api` or `--click button,api`; `--click api` calls
  the function first.
- Reopen data per rule: Cookiebot `Cookiebot.renew()` (API only);
  OneTrust `#ot-sdk-btn`, `.ot-sdk-show-settings`,
  `#ot-sdk-btn-floating .ot-floating-button__open` (new in C9), else
  `OneTrust.ToggleInfoDisplay()`; CookieYes `[data-cky-tag=revisit-consent]`;
  Complianz `.cmplz-manage-consent`, `.cmplz-show-banner`; Usercentrics
  the privacy button in either root, else the API; Borlabs
  `.borlabs-cookie-open-dialog-preferences`, `.borlabs-cookie-preference`;
  Didomi `Didomi.preferences.show()` (API only); iubenda
  `.iubenda-cs-preferences-link`, else `_iub.cs.api.openPreferences()`.
  The generic rule has no reopen.
- Reopen errors (the withdraw run's reason text):
  - `consent: Cookiebot has no way to reopen the banner without its API; --click button,api allows it`
    (`<name> has no way ...` for any rule with only an API reopen)
  - `consent: <name> showed no button to reopen the banner (<selectors>); --click button,api allows its API`
  - `consent: <name> showed no button to reopen the banner (<selectors>)`
  - `consent: <name> has no known way to reopen the banner`
    (the generic rule, a custom-selector banner; `this banner` when
    nothing names it)
  - `consent: <name>'s reopen button <selector> left the page`
  - `consent: <name>'s reopen button <selector> couldn't be clicked`
  - `consent: <name>'s reopen call <call> failed: <error>`
- Reject after a reopen that opened the preference center itself
  (OneTrust, Didomi, Usercentrics and iubenda APIs): when the first
  layer and the settings opener don't show after 1 s and the settings'
  Save does, Echo switches the toggles off and clicks Save directly;
  `layer: 2` and `selector` is the Save alone, e.g.
  `#onetrust-pc-sdk .save-preference-btn-handler`. `consent detect`
  reports the same Save when the settings are open on load.
- `State` (the final state after the reload) reads the rule's verify
  spec; the generic rule gives `source: banner` with `decided` true when
  no banner shows.
- `--geo <cc>` with OneTrust: the driver gives the query parameter
  `otgeo=<cc>` (lower case) for the start URL; any other CMP, or a
  country that isn't two letters, gives none. The note the browse lane
  adds reads `OneTrust geolocation override otgeo=de (unverified)`.

## C10 Hardening and the cmps listing

- Every isolated-world script is a file in `consent/cmp/js/` compiled
  in and read as data by `TestJSIsReadOnly`: a small tokenizer drops
  comments, strings and regular expressions and fails on a network call
  (`fetch`, `XMLHttpRequest`, `sendBeacon`, `WebSocket`...), cookie or
  storage access (`.cookie`, `localStorage`, `sessionStorage`,
  `indexedDB`), DOM changes (`.remove(`, `innerHTML`, `style`,
  `classList`, `setAttribute`, `appendChild`...), synthetic events
  (`click`, `dispatchEvent`), code from strings (`eval`, `Function`,
  timers), any write to a property and any template literal. The
  main-world globals check passes the same test.
- Caps: a script's answer over 256 KiB is refused by the driver as well
  as the Tab, with
  `consent: a CMP script answered <n> bytes; the cap is 262144`. The
  scripts stop lists at 200 entries and texts at 200 characters; the
  generic scan at 20 containers of 50 controls, a frame address at 200
  characters. An element path (the generic rule's `selector`) over
  1024 characters is dropped instead of cut; an id over 64 characters
  is never used in one. Such a control reads as not there.
- `consent cmps` data: `Info()` gives the rules version (`1.2.0`), the
  rule count (9) and the hash; `Result.CMPRules` (`cmp_rules` in the
  JSON) carries the same. `consent/cmp/testdata/cmps.golden.json` holds
  the current rows and hash.

## Review fixes (round 3)

- Rule format: `reject.second_layer.dialog` (one selector or a list,
  optional) names the settings dialog the second-layer Save must be
  inside. Set for OneTrust (`#onetrust-pc-sdk`), Cookiebot
  (`#CybotCookiebotDialog`), CookieYes (`.cky-preference-center`), Didomi
  (`#didomi-popup`) and iubenda (`#iubenda-iframe`). Complianz, Borlabs
  and Usercentrics name none yet, so their Save is still probed anywhere
  on the page. A Save outside the named dialog is never clicked; when it
  is the only one, the Reject gives `no_button`.
- The rules hash changed again (`cmps.golden.json` sha256
  `92bb49a871e89194617e353f9c75856e59124f1b91d9576275a343cf3a9b407a`);
  `rhythm-echo consent cmps` prints it.
- Fixture text: the CookieYes and Didomi replica pages no longer carry
  a comma before "and" in their banner text (the selectors are
  unchanged).

## Integration fixes

- `BannerShowing` (and every banner test of a rule's `banner.selector`)
  counts a container with no size of its own as shown when one of its
  first 64 descendants is visible: Borlabs Cookie's `#BorlabsCookieBox`
  only wraps a `position: fixed` box. A banner that came back on the
  next page or the reload was missed before, so
  `After Reject, the banner came back on /page-2/` (with `the banner
  showed again on the next page (/page-2/) at 4.70s; the banner showed
  again on the reload (/) at 7.73s`) now shows for such a CMP.
