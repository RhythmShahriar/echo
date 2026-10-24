# @rhythmsh/echo

The Next.js and Node connector for [Echo](https://github.com/RhythmShahriar/echo), Rhythm's tool that captures and checks everything a site sends: email, SMS, webhooks, chat alerts, analytics and payment calls.

The connector reports every outbound request and email to a running Echo (`rhythm echo up`). Echo then decides what happens to it:

- **Sealed** (Echo's default): nothing leaves. Echo answers each request with a realistic, provider-shaped response, and emails are recorded instead of sent.
- **Observe**: everything is recorded and sent for real.

It has no runtime dependencies, does nothing in production, and works on Vercel preview deployments.

```sh
npm install --save-dev @rhythmsh/echo   # or a regular dependency if previews build without dev deps
```

## Next.js (App Router)

```ts
// instrumentation.ts
export async function register() {
  const { register } = await import('@rhythmsh/echo/next');
  await register();
}
```

```js
// next.config.mjs: only needed if the app uses Nodemailer
import { withEcho } from '@rhythmsh/echo/next';

export default withEcho({
  /* your config */
});
```

`register()` runs in both runtimes:

- **Node.js:** hooks `fetch`, `http`/`https` and Nodemailer.
- **Edge:** hooks `fetch`.

Next.js bundles server dependencies, and the connector can't reach a bundled copy of Nodemailer. `withEcho` adds `nodemailer` to `serverExternalPackages` so the app uses the copy in `node_modules`, which is the one that gets patched. It changes nothing else. On Next.js 14, add `nodemailer` to `experimental.serverComponentsExternalPackages` yourself.

## Express, Fastify and other Node servers

Load the connector before the app. Use either the command line:

```sh
node --import @rhythmsh/echo/register server.js   # ESM or CJS app, Node >= 18.19
node -r @rhythmsh/echo/register server.js         # CJS app
```

or put `import '@rhythmsh/echo/register'` as the app's first import.

To tie captures to the request that caused them, add the middleware:

```js
import express from 'express';
import { echoMiddleware } from '@rhythmsh/echo';

const app = express();
app.use(echoMiddleware());
```

For other frameworks, wrap the handler with `withEchoRun(req, () => handle(req))`. `req` can be a Node, Express or Fetch `Request`.

## Scripts, cron jobs and workers

```sh
node --import @rhythmsh/echo/register scripts/send-digest.js
```

When Echo's action runner starts a command, it sets `RHYTHM_ECHO_RUN`, and captures carry that run. A worker that handles queued jobs can pass the run along itself, for example `withEchoRun(job.data.echoRun, () => process(job))`.

In serverless functions and short scripts, `await flush()` (exported by both entry points) before exiting. It waits for the response reports that are still being sent to Echo.

## Configuration

| Variable | Default | Meaning |
|---|---|---|
| `RHYTHM_ECHO_URL` | `http://127.0.0.1:8025` | Echo's capture API. On a preview deployment, this is a URL that reaches your Echo (a tunnel). |
| `RHYTHM_ECHO_TOKEN` | none | Sent as `X-Echo-Token`. Needed when Echo listens beyond loopback. |
| `RHYTHM_ECHO_FORCE` | off | `1` turns the connector on even where the production guard says no. Needed for `NODE_ENV=production` outside Vercel, such as `next start` on a staging server. |
| `RHYTHM_ECHO_SITE` | none | The site's own hosts or URLs, comma separated. `VERCEL_URL`, `VERCEL_BRANCH_URL` and `NEXT_PUBLIC_SITE_URL` count too. Calls to them are captured and Echo lets them through as sent. |
| `RHYTHM_ECHO_RUN` | none | `<run>/<action>` for work done outside a request. Echo's action runner sets it. |

`register(options)` accepts the same settings in code (`url`, `token`, `force`, `site`) as well as:

- `maxBody`: bytes of each body kept, default 256 KiB
- `timeout`: milliseconds to wait for Echo, default 5000
- `logger`
- `captureLoopback`: also capture requests to 127.0.0.1. It's off by default and meant for tests.

### Production guard

The connector stays completely inert, with no hooks installed, when any of these applies:

- `VERCEL_ENV=production`
- `NODE_ENV=production` outside Vercel. `RHYTHM_ECHO_URL` alone does not turn it on there. Only `RHYTHM_ECHO_FORCE=1` does.
- `NODE_ENV` unset outside Vercel, unless `RHYTHM_ECHO_URL` is set. Many self-hosted services never set `NODE_ENV`, so unset counts as production.
- `NODE_ENV` set to anything other than `development`, `test`, `staging`, `preview`, `local` or `production`. A typo such as `prod` counts as production. The connector logs one warning:

  ```
  [rhythm-echo] NODE_ENV=prod is not one of development, test, staging, preview, local or production: treating it as production and staying off
  ```

- during `next build` (`NEXT_PHASE=phase-production-build`), so static generation is never blocked

`RHYTHM_ECHO_FORCE=1` overrides all of these. Preview and development deployments on Vercel activate it, including `next start` on a preview, which runs with `NODE_ENV=production`. `next start` on your own staging server also runs with `NODE_ENV=production`, so set `RHYTHM_ECHO_FORCE=1` there.

When active, it logs one line per runtime:

```
[rhythm-echo] active (VERCEL_ENV=preview, nodejs): capturing fetch, http, https, nodemailer via https://echo.example.test
```

### Fail closed

If Echo can't be reached while the site is sealed, outbound requests fail as network errors and Nodemailer rejects. The connector logs one warning. It assumes the site is sealed until Echo has said otherwise. If Echo's last answer said observe, requests go through instead.

## What is captured

| Hook | Runtimes | Covers |
|---|---|---|
| `fetch` | Node, Edge | Resend, Postmark 5, Slack and Discord webhooks, GA4 and Meta CAPI over fetch, and any `fetch` call. Bodies can be strings, `URLSearchParams`, `FormData`, `Blob`, `ArrayBuffer`, streams or a `Request`. Streams are teed, so the request still gets the whole body. |
| `http` / `https` | Node | axios (Twilio, SendGrid), got, needle, `http.get`, and older SDKs. Requests are held until the body is written. An allowed request then goes out through its own agent unchanged. A faked one is answered through Node's own HTTP parser. |
| Nodemailer | Node | `sendMail` on every transport, in Nodemailer's ESM and CJS builds. When sealed, it resolves with a fake `info` (`messageId`, `accepted`) without connecting. |

The connector never captures requests to Echo itself or to loopback. Calls to the site's own host are captured and reported with `source.host`. Echo lets them through and records them as sent. The site's host comes only from configuration (`RHYTHM_ECHO_SITE`, `VERCEL_URL`, `VERCEL_BRANCH_URL`, `NEXT_PUBLIC_SITE_URL` or the `site` option), never from the incoming request's `Host` or `X-Forwarded-Host`, which a visitor can set to a provider's host. With none configured, every outbound call is treated as a call to someone else.

It masks credentials before they leave the process. Any header whose name contains `token`, `secret`, `key`, `auth`, `signature`, `session`, `password`, `cookie` or `credential` (any case) is masked whole: `x-auth-token` becomes `•••••` while `x-request-id` is kept. `authorization` keeps only its scheme (`Bearer •••••`). A Stripe key keeps its mode prefix (`Bearer sk_test_•••••`), which holds no secret and tells Echo whether it may fake the call. Echo then redacts secrets in bodies.

### Run IDs (`X-Echo-Run`)

Echo's browser and `rhythm echo run` send `X-Echo-Run: <run>/<action>` as a header or cookie. The connector copies it into each capture made while that request is handled. It finds the run in this order:

1. a `withEchoRun()` / `echoMiddleware()` scope
2. in Next.js, the current request: route handlers, server actions and dynamic pages, on both the Node and Edge runtimes
3. `RHYTHM_ECHO_RUN`

A visitor can send `X-Echo-Run` too and add messages to a baseline or drop them from one, so a value from a request counts only when Echo vouches for it:

- with `RHYTHM_ECHO_TOKEN` set, the value must carry Echo's signature (`<run>/<action>.<signature>`, which Echo adds when it has the same token). Unsigned or tampered values are ignored.
- without a token, the value counts only on a request straight from this machine: a loopback peer (`req.socket.remoteAddress` is 127.0.0.0/8 or ::1) and no `X-Forwarded-For`, `Forwarded` or `X-Real-IP` header. A Fetch `Request` and Next.js give no peer address and `next start` adds `X-Forwarded-For`, so Next.js apps need the token for runs.

An ignored value is logged once through the logger, for example `[rhythm-echo] ignored an unsigned X-Echo-Run: with RHYTHM_ECHO_TOKEN set, only runs Echo signed with it count`. A string passed to `withEchoRun()` and `RHYTHM_ECHO_RUN` are taken as given (a signature, if any, is dropped): pass the request itself, not `runFromHeaders(req.headers)`, so the value is checked.

On Node, the connector reads Next.js's request store directly rather than calling `headers()`, so a page that is being prerendered never turns dynamic because of it.

| Where the code runs | Run found via |
|---|---|
| Next.js route handler, server action, dynamic page | automatic |
| Next.js middleware, `after()` callbacks | `withEchoRun(request, fn)` |
| Express / Connect | `app.use(echoMiddleware())` |
| Fastify, Koa, Hono, plain `http` | `withEchoRun(req, fn)` |
| queue worker | `withEchoRun(job.data.echoRun, fn)` |
| script, cron job | `RHYTHM_ECHO_RUN` |

## Not covered

These paths bypass the connector:

- a separately installed `undici` package's `request()` or `fetch()`
- `http2`
- raw `net` / `tls` sockets
- a `ClientRequest` built by hand with `createConnection`
- a bundled Nodemailer without `withEcho`

To cover them, use Echo's proxy:

```sh
rhythm echo up --proxy 127.0.0.1:8026 --mitm
NODE_USE_ENV_PROXY=1 HTTPS_PROXY=http://127.0.0.1:8026 NODE_EXTRA_CA_CERTS="$(rhythm echo ca)" next start
```

`rhythm echo ca` prints the path of Echo's CA certificate, creating it on first use (`rhythm echo ca --pem` prints the certificate itself, to copy into a container). It lives in the user's cache folder:

| OS | Path |
|---|---|
| Linux | `~/.cache/rhythm/echo/ca/ca.pem` (or `$XDG_CACHE_HOME/rhythm/echo/ca/ca.pem`) |
| macOS | `~/Library/Caches/rhythm/echo/ca/ca.pem` |
| Windows | `%LocalAppData%\rhythm\echo\ca\ca.pem` |

`NODE_USE_ENV_PROXY` covers `fetch` from Node 22.21 and 24.0, and `http`/`https` from Node 22.21 and 24.5.

## Development

```sh
npm run test:ci    # npm ci --ignore-scripts, tsc, then the tests: what CI runs
npm test           # builds dist/, then the Vitest suite
npm run test:e2e   # the packed package in a real Next.js app against Echo's catcher (needs Go)
```

The suite is hermetic. No install script is needed (`npm ci --ignore-scripts` is enough). The tests start their own fakes on 127.0.0.1 (a fake Echo and fake provider APIs) and need no network. It runs from the repository root too, with `npm --prefix connectors/node test`. Run-signature tests read Echo's shared vectors in `conformance/testdata/run-signature.json`.

## License

MIT
