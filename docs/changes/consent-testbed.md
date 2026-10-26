# Consent testbed: changes

Every user-visible change of the consent testbed (commands, flags, output
lines, messages), with the exact text.

## X1 Fixture server

- New command `go run ./testbed/consent/server/cmd/consent-testbed` with flags `-root` (default `testbed/consent`, or `.` inside it), `-listen` (default `127.0.0.1:23400`), `-admin` (default `127.0.0.1:23401`), `-tls`, `-ca` (default `<root>/ca`), `-make-ca <dir>`, `-print-map`, `-echo-proxy`, `-echo-ca`.
- `-tls` serves https beside plain http on the same `-listen` port: each connection is told apart by its first byte, because Chrome's host resolver rules and Echo's `--map-host` send every name and port to one address.
- Start lines: `consent-testbed: sites and tags on 127.0.0.1:23400 (http and https), admin and sink on 127.0.0.1:23401` (`(http)` without `-tls`) and `consent-testbed: map with <the -print-map line>`.
- `-print-map` prints one line: `--map-host '*.shop.test=127.0.0.1:23400' --map-host analytics.tiktok.com=127.0.0.1:23400 ... --map-only` (every stand-in host, sorted).
- `-make-ca <dir>` prints `consent-testbed: wrote the testbed CA to <dir>`; on an existing CA it fails with `consent-testbed: <dir>/ca.pem exists; delete the folder to make a new CA`. The CA is name-constrained to `.test`, `localhost` and the stand-in hosts.
- `-tls` without a CA: `consent-testbed: testbed CA: open <dir>/ca.pem: no such file or directory (make ca creates it)`.
- A root without `sites/`: `consent testbed: no sites folder in <root> (pass -root testbed/consent)`.
- `-echo-proxy` must be `http://` on a loopback address or a one-label Compose name: `-echo-proxy "<value>": want http://host:port of rhythm-echo up` and `-echo-proxy "<value>": want a loopback address or a Compose service name`.
- A host the server doesn't serve answers `421` with `not a consent testbed host: <host>` and is counted under `unmapped`.
- `/logout/` and `/cart/` on any scenario answer `This page must never be opened by a scan.` and are counted under `forbidden`.
- Admin endpoints (admin port only): `GET /_admin/health` (`ok`), `GET /_admin/state?id=c01[&state=good|broken]` (`{"id": "c01", "state": "broken"}`; unknown id 404 `no scenario "x"`; bad state 400 `state: want good or broken`), `GET /_admin/sites`, `GET /_sink/count[?scenario=c01]` (`{"hits", "by", "forbidden", "internal", "unmapped"}`), `GET /_sink/hits?scenario=c01`, `POST /_sink/reset` (204), `/_internal/*` (the ssrf-probe target, counted under `internal`).
- `testbed/consent/toggle.sh <id|all> good|broken` prints `c01 -> broken`; `toggle.sh status` prints `all scenarios good` or `broken: c01 c02`; an unknown id fails with `./toggle.sh: unknown scenario x (see sites/)`.
- `make -C testbed/consent ca`, `serve`, `map` and `toggle ID=c01 STATE=broken`.
