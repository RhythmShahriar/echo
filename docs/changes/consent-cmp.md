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
