# Echo for AI coding agents (MCP)

`rhythm-echo mcp` is a [Model Context Protocol](https://modelcontextprotocol.io)
server on stdin and stdout. A coding agent can then ask Echo what the site
sent ("what did checkout send?"), get the code from a sign-up email and
check what changed since a snapshot, without reading terminal output.

Start Echo first (`rhythm echo up`), then add the server to the agent from
the project folder (the one with `.echo/config.yaml`).

Claude Code:

```sh
claude mcp add echo -- rhythm-echo mcp
```

Any client that takes a JSON config (Cursor, VS Code, Windsurf, Zed):

```json
{
  "mcpServers": {
    "echo": {
      "command": "rhythm-echo",
      "args": ["mcp"],
      "env": { "RHYTHM_ECHO_URL": "http://127.0.0.1:8025" }
    }
  }
}
```

Flags: `--url` (or `RHYTHM_ECHO_URL`) where Echo runs, `--token` (or
`RHYTHM_ECHO_TOKEN`) when Echo was started with one, `--config` for another
config file. The help is `rhythm-echo mcp --help`.

## Tools

| Tool | What it does | Changes anything |
|---|---|---|
| `list_messages` | What Echo captured, newest first, one line each. Filters: `channel`, `to`, `run`, `action`, `since` ("15m" or a time), `limit` | no |
| `get_message` | One message with every field (`id` from the list, or `latest`) | no |
| `latest_code` | The one-time code from the newest matching email or SMS (`to`, `channel`, `since`) and nothing else from it | no |
| `run_action` | Runs actions from `.echo/config.yaml` (`actions`, all when empty) and returns what each sent and the issues found | runs the actions |
| `snapshot` | Runs the actions and saves the result as a snapshot (`name`, `actions`) | runs the actions, writes `.echo/snapshots` |
| `compare` | Runs the actions again and compares with a snapshot (`snapshot`, the latest when empty): verdict, counts and every change | runs the actions, writes `.echo/runs` |
| `check_domain` | SPF, DKIM, DMARC, MX, MTA-STS, TLS-RPT and BIMI for a sending `domain` (`provider` when known) | no |
| `check_consent` | Cookie consent on a page: no choice, Accept and Reject in a real browser, tracking hits held, Consent Mode decoded (`url`, `runs`, `consent_mode`, `geo`). Needs no running Echo; only for sites you own or are authorised to test | loads the page, writes `.echo/consent` |

## What the agent sees

Every result is masked before it leaves Echo: credential headers by name
(anything with token, secret, key, auth, signature, session, password,
cookie or credential in it), the codes and keys in one-time links
(`?code=`, `?key=`, `?token=`, `?sig=`…), Telegram bot tokens, `/key/…`
paths and everything the Rhythm kit recognizes (API keys, passwords in
JSON and forms, URLs with passwords). A masked value keeps at most four
characters.

`latest_code` is the exception by design: it returns the code itself
(and the message ID), so a test can type it in. It never returns the
message around it.

The action tools run what `.echo/config.yaml` lists, including its
`command:` actions, so give the agent a project whose config you trust.
They run one at a time.

## Protocol

JSON-RPC 2.0, one message per line. `initialize` negotiates the protocol
version (2025-06-18, 2025-03-26 or 2024-11-05; a client asking for another
gets 2025-06-18), then `tools/list` and `tools/call`. `ping`,
`notifications/cancelled` and batches work. Results come as JSON text and
as `structuredContent`. A tool that fails returns `isError: true` with the
reason; an unknown tool or argument is a JSON-RPC error.
