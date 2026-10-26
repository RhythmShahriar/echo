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
  selector first, then global, then script:
  `selector:#CybotCookiebotDialog and global:Cookiebot`,
  `global:Cookiebot and script:consent.cookiebot.com/uc.js`, or
  `generic` for the generic rule. (`consent detect` prints it as
  `#CybotCookiebotDialog and window.Cookiebot`; that rendering is the
  cli lane's.)
- `tcf` is true when the page defines `__tcfapi`; `banner_at_ms` is the
  run-clock time of the look that saw the banner.
- When every look failed (the page kept navigating), Detect returns the
  last script error instead of "no banner".
