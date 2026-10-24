# Echo connector conformance

A connector lives inside a site (WordPress, Laravel, Node, or one you write for Django or Rails), reports every email and outbound HTTP request to Echo and obeys Echo's answer. This folder is what a connector author needs to get that right without reading Echo's Go code.

| Piece | What it is |
|---|---|
| [`../wire/schema.json`](../wire/schema.json) | The wire protocol as a JSON Schema (draft 2020-12), generated from [`../wire/wire.go`](../wire/wire.go). `Capture` is what a connector posts, `Decision` is what Echo answers. |
| [`testdata/*.json`](testdata) | Shared vectors: captures with the decision Echo gives them, broken captures with what is wrong, plus what a connector must do when Echo gives no decision. |
| `rhythm echo conformance` | A strict stand-in Echo for a connector's end-to-end test. Until the command ships, run it with `GOWORK=off go run ./conformance/cmd/conformance`. |

## The protocol in short

- `POST /v1/capture` with a `Capture`, `X-Echo-Token` when Echo has a token. Echo answers `200` with a `Decision`.
- HTTP requests are reported `phase: before`. The decision's `action` is `allow` (send it, then report `phase: after` with the `id` and the response), `fake` (don't send: hand `response` to the app as the provider's answer) or `block` (don't send: fail the request).
- Emails are reported before sending. `fake` means Echo keeps it: report success to the app and send nothing. `allow` (observe mode) means send it, then report `phase: after` with the `id` and any `error`.
- No decision (Echo unreachable, any status but 200, no answer within the connector's own short timeout): sealed, nothing is sent; observing, it is sent. Which applies is the mode Echo last reported (Node) or the connector's configured fallback mode (Laravel, WordPress), seal by default.
- `v` is the wire version, now 2. Send 2. When Echo answers `400` with "newer than Echo", read `GET /v1/health`: an Echo that says `"wire": 1` gets captures with `v: 1` and without the fields marked `x-since: 2` in the schema (`source.site`, `email.attachments`). Remember that for the process.
- Mask credentials before they leave the site: see the connectors' READMEs for the header rule.

## Vectors

Each `testdata/*.json` file other than `run-signature.json` is one vector:

```json
{
  "name": "a sealed Twilio call is faked",
  "about": "why the rule exists",
  "valid": true,
  "problems": ["for broken captures: text the validator's problems must contain"],
  "mode": "seal",
  "echo": "up | unreachable | 5xx | timeout",
  "capture": { "v": 2, "kind": "http", "...": "..." },
  "decision": { "mode": "seal", "action": "fake", "response": { "status": 201 } },
  "connector": "fake | send | block"
}
```

- `capture-*.json`: valid captures and the decision Echo's real catcher gives them (`decision` lists the fields that must match).
- `broken-*.json`: captures the schema refuses, with the problems a validator must find.
- `fail-*.json`: Echo gives no decision; `connector` says whether the connector must block or send.
- `run-signature.json`: the `X-Echo-Run` signing vectors (`wire.SignRun`, `wire.VerifyRun`) and which run a connector takes from a request. A run value longer than `max_run` (512 bytes, signature included) is ignored whole, never cut.

The Go tests (`go test ./conformance/`) post every valid capture to Echo's real catcher and check the decision. The Node, Laravel and WordPress suites check their own captures against `schema.json` and run the `fail-*` vectors through their hooks.

## The stand-in

```sh
GOWORK=off go run ./conformance/cmd/conformance --listen 127.0.0.1:8025 --expect email,http-before
# point the connector at it (RHYTHM_ECHO_URL=http://127.0.0.1:8025), run the site's test, then Ctrl-C
```

It is Echo's real catcher (same decisions, same `/v1/messages` to read back) with every capture checked against the schema first. A capture that breaks it is answered `400 conformance: <path>: <problem>` and counted. When it stops (SIGINT or SIGTERM) it prints a table and exits 1 on any fail:

```
CHECK        RESULT  DETAIL
schema       PASS    5 captures, all valid
email        PASS    1: an email reported before it is sent
http-before  PASS    4: an HTTP request reported before it is sent
run          INFO    5: a capture tied to a run

PASS
```

`GET /v1/conformance` returns the same report as JSON, for scripts. Flags:

| Flag | Meaning |
|---|---|
| `--listen <addr>` | API address (default `127.0.0.1:8025`) |
| `--smtp <addr>` | also run Echo's SMTP sink (default off) |
| `--mode seal\|observe` | Echo's mode (default seal) |
| `--token <t>` | require this `X-Echo-Token` |
| `--allow <hosts>` | hosts reachable when sealed |
| `--wire 1` | act like an Echo that speaks wire 1: health says so, v2 captures get its refusal and the report checks the connector fell back |
| `--expect <list>` | scenarios the run must include: `email`, `email-after`, `http-before`, `http-after`, `run`, `site`, `attachments`, `wire-1`, `wire-2` |
| `--json` | print the report as JSON |

The Node (`connectors/node/fixtures/e2e.mjs`) and Laravel (`connectors/laravel/fixtures/e2e.sh`) end-to-end tests run against it.

## Changing the protocol

Edit the types in `wire/wire.go` (tags: `enum:"a,b"` lists a field's values, `since:"2"` marks the version that added it), then:

```sh
GOWORK=off go generate ./wire/
```

`conformance/gen` fails its test when `schema.json` is out of date.
