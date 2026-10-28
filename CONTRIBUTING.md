# Contributing to Echo

Setup, commit messages and the review process are shared by every Rhythm tool and live in the
[shared CONTRIBUTING](https://github.com/RhythmShahriar/.github/blob/master/CONTRIBUTING.md).
This page covers what is particular to Echo.

```sh
make hooks   # Conventional Commits check
make check   # lint + tests
```

## Where things are

| Folder | What it holds |
|---|---|
| `cmd/rhythm-echo` | the command line: every command, its flags and its messages |
| `catcher`, `proxy`, `relay`, `sendmail` | the SMTP sink, the MITM proxy and what answers in each service's place |
| `recognize` | the provider catalogue: one YAML file per service, each with fixtures |
| `compare`, `report` | comparisons and every output format, with their JSON schemas in `report/schema` |
| `consent/...` | the cookie consent checks: `cli`, `browse` (Chrome), `cmp` (banners), `tracker` (the catalogue), `signal` (Consent Mode), `verdict`, `evidence` |
| `connectors` | the WordPress, Laravel and Node connectors, each with its own tests |
| `integrations` | the GitHub Action (`action.yml` and `integrations/action`), MCP and other CI systems |
| `testbed`, `testbed/consent` | the sites the benchmarks run on |

## Tests

- Every check needs a positive and a negative fixture: one where it reports the problem and one
  where it doesn't, plus one for each near miss it must ignore.
- Fixtures only ever run on `127.0.0.1`. Tests never contact real sites or services.
- Fake secrets are put together at runtime (`"sk_" + "live_x"`), never written whole.
- Golden files are rewritten with `-update` (`go test ./report -update`); read the diff before
  you commit it.

The connectors: `make test-connectors` (Node and Docker). The consent tests that start Chrome:
`make test-browser`. Every consent fuzz target: `make fuzz-consent`.

## The consent testbed

`testbed/consent` has 40 small shops, 25 broken in one way each and 15 correct, on one local Go
server. Start it with `make -C testbed/consent serve` (`LISTEN=` and `ADMIN=` move its ports) and
run a check with the flags `make -s -C testbed/consent map` prints, through `eval`. Its
[README](testbed/consent/README.md) lists every scenario. A change to a consent check comes with
the scenario that shows it; `make bench-consent` must still catch 25 of 25 with no false
alarm.
