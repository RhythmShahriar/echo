# CMP rules

One file per consent management platform, `<id>.yaml`, compiled into
Echo. `rhythm echo consent cmps` lists them with the version and the
SHA-256 of these files concatenated in name order, followed by the
generic rule's words:

```sh
cat $(ls consent/cmp/rules/*.yaml | sort) consent/cmp/generic_words.yaml | sha256sum
```

Every rule is written from the vendor's public documentation and markup,
never copied from another tool's rule set. `sources` names what it was
written from.

## Format

```yaml
id: cookiebot              # the file name, lowercase
name: Cookiebot            # as reports show it
priority: 10               # lower is tried first; generic is always last
detect:                    # any of these means the CMP is on the page
  selectors: ["#CybotCookiebotDialog"]
  globals: [Cookiebot]     # window names, tested with typeof only
  scripts: [consent.cookiebot.com/uc.js]   # URL substrings of loaded scripts
banner:
  selector: "#CybotCookiebotDialog"        # must be visible for the banner to count as shown
accept:                    # chains tried in order
  - "#CybotCookiebotDialogBodyLevelButtonLevelOptinAllowAll"
reject:
  first_layer: ["#CybotCookiebotDialogBodyButtonDecline"]
  second_layer:            # open the settings, switch toggles off, save
    open: ["#CybotCookiebotDialogBodyLevelButtonCustomize"]
    toggles_off: "input.CybotCookiebotDialogBodyLevelButton:not([disabled])"
    press_off: []          # buttons that switch a purpose off once pressed (Didomi's Disagree)
    save: ["#CybotCookiebotDialogBodyLevelButtonLevelOptinAllowallSelection"]
reopen:
  selectors: ["#CybotCookiebotDialogBodyButtonDetails"]
  api: reopen              # called when no selector shows, under --click api or button,api only
api:                       # fixed calls in the page's main world
  accept: Cookiebot.submitCustomConsent(true,true,true)
  reject: Cookiebot.submitCustomConsent(false,false,false)
  reopen: Cookiebot.renew()
  state: JSON.stringify(Cookiebot.consent)
verify:                    # where the stored choice is read
  parser: cookiebot        # cookiebot onetrust cookieyes complianz usercentrics borlabs didomi iubenda tcf banner
  cookies: [CookieConsent] # a trailing * is a prefix
  storage: []
  state_js: ""             # defaults to api.state
  accepted: {statistics: true, marketing: true}
  rejected: {statistics: false, marketing: false}
  tcf: false               # true: read euconsent-v2 when the CMP's own state is undecided
geo_override:              # only when the vendor documents a test parameter
  param: otgeo
  value_from: country
sources: [https://www.cookiebot.com/en/developer/]
fixtures: [testdata/cookiebot/first-layer.html]
```

Selectors use the `--accept-selector` syntax: `a >> b` is two steps
clicked in order, `host ::shadow button` enters host's open shadow root.
Detect selectors, the banner and the toggles are one step each. The
banner and `toggles_off` take one selector or a list, for a CMP that
draws its banner in more than one way (Usercentrics v2 and v3): any
visible one is the banner. When several elements match a selector the
first visible one counts. A banner behind a closed shadow root counts
as shown when hit tests land on its host; its buttons can't be reached,
so only the rule's `api` can choose there (`--click button,api`).

`press_off` buttons are pressed when `aria-pressed` isn't `true`.
With `verify.tcf` a choice kept only in the TC string is verified from
it: Accept needs purpose 1 consented, Reject no purpose consented
(`verified_by: tcf`).

The loader refuses, with the file and line:

- an unknown key;
- a selector `--accept-selector` would refuse;
- an `api` or `state_js` call that is more than one constant expression
  (no `;`, template literals or string concatenation) or that contains
  `eval(`, `Function(`, `fetch(`, `XMLHttpRequest`, `document.cookie=`
  or `localStorage.setItem`;
- a missing `id`, `name`, `banner`, `verify.parser` or `sources`.

`generic.yaml` is the rule for a banner no other rule knows. It has no
selectors: `consent/cmp/generic.go` looks for a visible fixed, sticky,
high (z-index 1000 or more) or modal element with a cookie or consent
word and ranks its buttons by text with the phrases of
`consent/cmp/generic_words.yaml` (30 languages; the file explains the
matching). It is tried last and verified by the banner staying gone
after a reload.

Each rule needs replica pages under `testdata/<id>/` with an
`expect.yaml` row each; `TestEveryRuleHasFixtures` lists the pages.
