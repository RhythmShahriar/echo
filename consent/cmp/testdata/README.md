# CMP replica pages

One folder per CMP rule. Each page is a replica written fresh for
Echo's tests from the vendor's public documentation and the markup its
banner carries on public pages; none is the vendor's code. Every page
names its source URL and capture date in a comment at the top. The
pages load nothing from the network: the vendor's script is a local
replica at a path that keeps the vendor's host in it (for example
`consent.cookiebot.com/uc.js`), so script detection works offline.

Every named rule has these pages, plus at most one special case:

| Page | What it shows |
|---|---|
| `first-layer.html` | banner shown, Reject on the first layer |
| `second-layer.html` | Reject only in the settings |
| `hidden.html` | banner markup there and hidden (returning visitor) |
| `late.html` | banner injected after 4 s |
| `two-banners.html` | the banner plus a newsletter offer with "Accept offer" |
| `text-only.html` | the CMP named in the text only, no CMP |
| `script-no-banner.html` | the CMP's script and global, no banner |

Special cases: `cookiebot/details-tab.html` (Reject through the Details
tab, styled switches pre-ticked), `onetrust/preference-centre.html`
(the preference center with styled switches, two pre-ticked) and
`cookieyes/cookie-law-info.html` (the older Cookie Law Info bar of a
returning visitor: present, not found; shown, the generic rule finds
it).
`usercentrics/closed-shadow.html` (the banner in a closed shadow root:
found, no button, the API under `--click button,api`) and
`borlabs/version-3.html` (the Borlabs Cookie 3 markup),
`didomi/tcf.html` (a TCF setup: `__tcfapi` and the choice kept in
`euconsent-v2` only) and `iubenda/floating.html` (the floating layout:
Accept and "Learn more and customize", with a close button never
clicked). Complianz has none.

## generic

`generic/` holds hand-made banners (no vendor) for the generic rule,
drawn by `generic/banner.js`: English, German with Reject only in the
settings, Arabic and Hebrew right to left, text split across elements
with `&nbsp;`, French "Continuer sans accepter", a policy link that says
Reject (never clicked), a late banner, a banner drawn in a same-origin
frame and one whose Reject only hides it (it comes back after the
reload: `state_unchanged`). The negative pages are never a banner and
nothing on them is clicked: a newsletter offer, an age gate, a login
dialog, a cart drawer, a returns form with Reject, a chat widget and a
shipping notice with OK. `lang-<code>.html` (one per language) and
their rows are generated from `generic_words.yaml` by
`TestGenericLanguagePages` (`-update` rewrites them). Generic rows add
`accept_text`, `reject_text`, `reject_reason` and `sees.generic`.

## expect.yaml

```yaml
cmp: cookiebot
pages:
  - page: late.html
    found: true            # a banner is shown within --banner-wait 10s
    present: true          # the CMP is on the page, shown or not
    banner_after_ms: 4000  # when the banner appears; not found within 2 s
    detected_by: "selector:#CybotCookiebotDialog and global:Cookiebot"
    accept_layer: 1        # the layer Find reports for Accept
    reject_layer: 1        # 1, or 2 when Reject is only in the settings
    fallback: false        # true: no button to click (closed shadow root);
                           # --click button,api makes the choice with the API
    tcf: false             # the page defines __tcfapi
    stores: tcf            # the choice is kept in the TC string only
    sees:                  # the page as the scripts see it (FakeTab tests)
      selectors: ["#CybotCookiebotDialog"]
      globals: [Cookiebot]
      scripts: [consent.cookiebot.com/uc.js]
      banner: true
      controls: [...]      # clickable on the first layer
      settings: [...]      # clickable once the settings are open
      toggles: [false, true, true]   # the switches when the settings open
      decoys: ["#newsletter-accept"] # never clicked
      closed: [...]        # controls behind a closed shadow root
```

The `consent/cmp` tests answer a FakeTab from `sees` and check the
rest; `TestReplicaMarkup` checks `sees` against the page markup. The
testbed serves these folders as they are and runs
`rhythm-echo consent detect --json` on every row in Chrome, checking
`found`, `present`, `detected_by`, the layers and the late timing.
