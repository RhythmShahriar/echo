import { afterAll, beforeAll, beforeEach, describe, expect, it } from 'vitest';

import http from 'node:http';
import type { AddressInfo } from 'node:net';

import { echoMiddleware, flush, register, withEchoRun } from '../src/index.js';
import { state } from '../src/state.js';
import { afters, befores, fakeEcho, silent, target, type FakeEcho, type Target } from './helpers.js';

let echo: FakeEcho;
let api: Target;
const log = silent();

beforeAll(async () => {
  echo = await fakeEcho('seal');
  api = await target();
  // captureLoopback: the fake "real" API lives on 127.0.0.1
  await register({ url: echo.url, token: 'tok-123', captureLoopback: true, logger: log });
});
afterAll(async () => {
  await echo.close();
  await api.close();
});
beforeEach(async () => {
  await flush();
  echo.captures.length = 0;
  echo.tokens.length = 0;
  echo.mode = 'seal';
  echo.decide = undefined;
  api.requests.length = 0;
  state().mode = 'seal';
});

describe('fetch, sealed', () => {
  it('answers with the fake and never sends', async () => {
    const res = await fetch(`${api.url}/emails`, {
      method: 'POST',
      headers: { authorization: 'Bearer re_live_abcdefghijkl', 'content-type': 'application/json' },
      body: JSON.stringify({ to: 'ann@example.test', subject: 'Hi' }),
    });
    expect(res.status).toBe(200);
    expect(res.headers.get('x-echo')).toBe('fake');
    expect(await res.json()).toEqual({ id: 'faked-by-echo' });
    expect(api.requests).toHaveLength(0);
    const [c] = await echo.waitFor(befores);
    expect(c).toMatchObject({
      v: 1,
      kind: 'http',
      phase: 'before',
      source: { via: 'node', hook: 'fetch' },
      http: { method: 'POST', url: `${api.url}/emails`, body: '{"to":"ann@example.test","subject":"Hi"}' },
    });
    expect(c!.http!.headers!.authorization).toBe('Bearer •••••');
    expect(JSON.stringify(c)).not.toContain('re_live_abcdefghijkl');
    expect(echo.tokens[0]).toBe('tok-123');
  });

  it('logs one line when active', () => {
    expect(log.lines.filter((l) => l.includes('active')).length).toBe(1);
    expect(log.lines[0]).toMatch(/\[rhythm-echo\] active .*fetch, http, https/);
  });

  it('blocks with a TypeError like a network error', async () => {
    echo.decide = () => ({ action: 'block' });
    await expect(fetch(`${api.url}/x`)).rejects.toThrow(new TypeError('blocked by Echo'));
    expect(api.requests).toHaveLength(0);
  });

  it('turns a fake 204 into an empty response', async () => {
    echo.decide = () => ({ action: 'fake', response: { status: 204, body: 'ignored' } });
    const res = await fetch(`${api.url}/x`, { method: 'DELETE' });
    expect(res.status).toBe(204);
    expect(await res.text()).toBe('');
  });

  it('leaves Echo itself and data: URLs alone', async () => {
    const res = await fetch('data:text/plain,hello');
    expect(await res.text()).toBe('hello');
    expect(echo.captures).toHaveLength(0);
  });
});

describe('fetch, observe', () => {
  it('sends for real and reports the response', async () => {
    echo.mode = 'observe';
    const res = await fetch(`${api.url}/hook`, { method: 'POST', body: 'a=1' });
    expect(res.status).toBe(201);
    expect(await res.json()).toEqual({ real: true, got: 3 });
    expect(api.requests[0]).toMatchObject({ method: 'POST', url: '/hook', body: 'a=1' });
    await flush();
    const [b] = await echo.waitFor(befores);
    const [a] = await echo.waitFor(afters);
    expect(b!.http!.headers!['content-type']).toBe('text/plain;charset=UTF-8');
    expect(a!.id).toMatch(/^m\d+$/);
    expect(a).toMatchObject({ phase: 'after', http: { status: 201, response: '{"real":true,"got":3}' } });
  });
});

describe('request bodies', () => {
  beforeEach(() => {
    echo.mode = 'observe';
  });

  it('tees a ReadableStream: Echo and the API both get it', async () => {
    const parts = ['chunk-1,', 'chunk-2,', 'chunk-3'];
    const stream = new ReadableStream<Uint8Array>({
      start(c) {
        for (const p of parts) c.enqueue(new TextEncoder().encode(p));
        c.close();
      },
    });
    const res = await fetch(`${api.url}/upload`, { method: 'POST', body: stream, duplex: 'half' } as RequestInit);
    expect(res.status).toBe(201);
    expect(api.requests[0]!.body).toBe(parts.join(''));
    const [b] = await echo.waitFor(befores);
    expect(b!.http!.body).toBe(parts.join(''));
  });

  it('caps the captured body but sends all of it', async () => {
    await register({ url: echo.url, captureLoopback: true, maxBody: 1024, logger: log });
    try {
      const big = 'x'.repeat(200_000);
      const stream = new ReadableStream<Uint8Array>({
        start(c) {
          for (let i = 0; i < big.length; i += 10_000) c.enqueue(new TextEncoder().encode(big.slice(i, i + 10_000)));
          c.close();
        },
      });
      await fetch(`${api.url}/big`, { method: 'POST', body: stream, duplex: 'half' } as RequestInit);
      expect(api.requests[0]!.body.length).toBe(200_000);
      const [b] = await echo.waitFor(befores);
      expect(b!.http!.body!.length).toBe(1024);
    } finally {
      await register({ url: echo.url, token: 'tok-123', captureLoopback: true, logger: log });
    }
  });

  it('reads URLSearchParams, FormData, ArrayBuffer and a Request', async () => {
    await fetch(`${api.url}/form`, { method: 'POST', body: new URLSearchParams({ To: '+15550001', Body: 'Hi' }) });
    const fd = new FormData();
    fd.set('to', 'ann@example.test');
    await fetch(`${api.url}/multipart`, { method: 'POST', body: fd });
    await fetch(`${api.url}/bin`, { method: 'POST', body: new TextEncoder().encode('bytes').buffer });
    await fetch(new Request(`${api.url}/req`, { method: 'PUT', body: 'from a Request', headers: { 'x-api-key': 'k-secret-value' } }));
    const got = await echo.waitFor(befores, 4);
    const by = (p: string) => got.find((c) => c.http!.url.endsWith(p))!.http!;
    expect(by('/form').body).toBe('To=%2B15550001&Body=Hi');
    expect(by('/form').headers!['content-type']).toContain('application/x-www-form-urlencoded');
    expect(by('/multipart').headers!['content-type']).toMatch(/^multipart\/form-data; boundary=/);
    expect(by('/multipart').body).toContain('ann@example.test');
    expect(by('/bin').body).toBe('bytes');
    expect(by('/req')).toMatchObject({ method: 'PUT', body: 'from a Request' });
    expect(by('/req').headers!['x-api-key']).toBe('•••••');
    // every body still reached the API intact
    expect(api.requests.map((r) => r.body)).toContain('from a Request');
    expect(api.requests.find((r) => r.url === '/form')!.body).toBe('To=%2B15550001&Body=Hi');
  });
});

describe('runs', () => {
  it('carries X-Echo-Run from the incoming request', async () => {
    await withEchoRun({ headers: { 'x-echo-run': 'run7/signup' } }, async () => {
      await new Promise((r) => setTimeout(r, 5)); // survives awaits
      await fetch(`${api.url}/a`);
    });
    await withEchoRun({ headers: { cookie: 'theme=dark; X-Echo-Run=run7%2Fcheckout' } }, () => fetch(`${api.url}/b`));
    await withEchoRun(new Request('http://site.test/', { headers: { 'X-Echo-Run': 'run7/fetch-req' } }), () => fetch(`${api.url}/c`));
    await withEchoRun('run7/explicit', () => fetch(`${api.url}/d`));
    const got = await echo.waitFor(befores, 4);
    expect(got.map((c) => c.run)).toEqual(['run7/signup', 'run7/checkout', 'run7/fetch-req', 'run7/explicit']);
  });

  it('falls back to RHYTHM_ECHO_RUN, after the request scope', async () => {
    process.env.RHYTHM_ECHO_RUN = 'envrun/cron';
    try {
      await fetch(`${api.url}/e`);
      await withEchoRun({ headers: { 'x-echo-run': 'req/wins' } }, () => fetch(`${api.url}/f`));
      await withEchoRun({ headers: {} }, () => fetch(`${api.url}/g`));
      const got = await echo.waitFor(befores, 3);
      expect(got.map((c) => c.run)).toEqual(['envrun/cron', 'req/wins', 'envrun/cron']);
    } finally {
      delete process.env.RHYTHM_ECHO_RUN;
    }
  });

  it('echoMiddleware scopes an Express-style handler', async () => {
    // the test's own call to the site is captured too (captureLoopback): let it through
    echo.decide = (c) => (c.http?.url.endsWith('/signup') ? { action: 'allow' } : {});
    const mw = echoMiddleware();
    const site = http.createServer((req, res) =>
      mw(req, res, () => {
        void fetch(`${api.url}/from-middleware`).then(() => res.end('ok'));
      }),
    );
    await new Promise<void>((r) => site.listen(0, '127.0.0.1', r));
    try {
      const port = (site.address() as AddressInfo).port;
      await (await fetch(`http://127.0.0.1:${port}/signup`, { headers: { 'x-echo-run': 'mw/signup' } })).text();
      const [c] = await echo.waitFor((c) => befores(c) && c.http!.url.endsWith('/from-middleware'));
      expect(c!.run).toBe('mw/signup');
      expect(c!.source.host).toBe(`127.0.0.1:${port}`);
    } finally {
      site.close();
    }
  });

  it('treats the incoming request host as the site', async () => {
    const host = new URL(api.url).host;
    await withEchoRun({ headers: { host } }, () => fetch(`${api.url}/self`));
    expect(api.requests).toHaveLength(1);
    expect(echo.captures).toHaveLength(0);
  });
});
