# CMP rules

One file per consent management platform, `<id>.yaml`, compiled into
Echo. `rhythm echo consent cmps` lists them with the version and the
SHA-256 of these files concatenated in name order:

```sh
cat $(ls consent/cmp/rules/*.yaml | sort) | sha256sum
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
    save: ["#CybotCookiebotDialogBodyLevelButtonLevelOptinAllowallSelection"]
reopen:
  selectors: ["#CybotCookiebotDialogBodyButtonDetails"]
  api: reopen              # the api entry to call when no selector shows
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
geo_override:              # only when the vendor documents a test parameter
  param: otgeo
  value_from: country
sources: [https://www.cookiebot.com/en/developer/]
fixtures: [testdata/cookiebot/first-layer.html]
```

Selectors use the `--accept-selector` syntax: `a >> b` is two steps
clicked in order, `host::shadow button` enters host's open shadow root.
Detect selectors, the banner and the toggles are one step each.

The loader refuses, with the file and line:

- an unknown key;
- a selector `--accept-selector` would refuse;
- an `api` or `state_js` call that is more than one constant expression
  (no `;`, template literals or string concatenation) or that contains
  `eval(`, `Function(`, `fetch(`, `XMLHttpRequest`, `document.cookie=`
  or `localStorage.setItem`;
- a missing `id`, `name`, `banner`, `verify.parser` or `sources`.

Each rule needs replica pages under `testdata/<id>/` with an
`expect.yaml` row each; `TestEveryRuleHasFixtures` lists the pages.
