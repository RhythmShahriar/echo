import http from 'node:http';
import https from 'node:https';
import { request as namedRequest } from 'node:http';
import { Readable } from 'node:stream';
import zlib from 'node:zlib';
import axios from 'axios';
import { afterAll, beforeAll, beforeEach, describe, expect, it } from 'vitest';

import { flush, register, withEchoRun } from '../src/index.js';
import { state } from '../src/state.js';
import { afters, befores, fakeEcho, silent, target, type FakeEcho, type Target } from './helpers.js';

let echo: FakeEcho;
let api: Target;

beforeAll(async () => {
  echo = await fakeEcho('seal');
  api = await target();
  await register({ url: echo.url, captureLoopback: true, logger: silent() });
});
afterAll(async () => {
  await echo.close();
  await api.close();
});
beforeEach(async () => {
  await flush();
  echo.captures.length = 0;
  echo.mode = 'seal';
  echo.decide = undefined;
  api.requests.length = 0;
  state().mode = 'seal';
});

interface Got {
  status: number;
  headers: http.IncomingHttpHeaders;
  body: string;
}

function call(mod: typeof http | typeof https, url: string, opts: http.RequestOptions = {}, body?: string | Buffer[]): Promise<Got> {
  return new Promise((resolve, reject) => {
    const req = mod.request(url, opts, (res) => {
      let b = '';
      res.setEncoding('utf8');
      res.on('data', (d: string) => (b += d));
      res.on('end', () => resolve({ status: res.statusCode ?? 0, headers: res.headers, body: b }));
    });
    req.on('error', reject);
    if (Array.isArray(body)) Readable.from(body).pipe(req);
    else req.end(body);
  });
}

describe('http.request, sealed', () => {
  it('fakes the response through Node’s own parser', async () => {
    const got = await call(http, `${api.url}/2010-04-01/Accounts/AC123/Messages.json`, {
      method: 'POST',
      headers: { 'content-type': 'application/x-www-form-urlencoded', authorization: 'Basic QUMxMjM6c2VjcmV0' },
    }, 'To=%2B15550001&Body=Hello');
    expect(got.status).toBe(200);
    expect(got.headers['x-echo']).toBe('fake');
    expect(JSON.parse(got.body)).toEqual({ id: 'faked-by-echo' });
    expect(api.requests).toHaveLength(0);
    const [c] = await echo.waitFor(befores);
    expect(c).toMatchObject({
      source: { via: 'node', hook: 'http.request' },
      http: { method: 'POST', url: `${api.url}/2010-04-01/Accounts/AC123/Messages.json`, body: 'To=%2B15550001&Body=Hello' },
    });
    expect(c!.http!.headers!.authorization).toBe('Basic •••••');
  });

  it('fakes https to a real provider host without touching the network', async () => {
    const got = await call(https, 'https://api.sendgrid.com/v3/mail/send', { method: 'POST', headers: { 'content-type': 'application/json' } }, '{"x":1}');
    expect(got.status).toBe(200);
    const [c] = await echo.waitFor(befores);
    expect(c!.http).toMatchObject({ url: 'https://api.sendgrid.com/v3/mail/send', body: '{"x":1}' });
    expect(c!.source.hook).toBe('https.request');
  });

  it('covers http.get and named imports', async () => {
    const got = await new Promise<number>((resolve, reject) =>
      http.get(`${api.url}/get`, (res) => (res.resume(), resolve(res.statusCode ?? 0))).on('error', reject),
    );
    expect(got).toBe(200);
    await new Promise<void>((resolve, reject) => {
      const r = namedRequest(`${api.url}/named`, (res) => (res.resume(), res.on('end', resolve)));
      r.on('error', reject);
      r.end();
    });
    const urls = (await echo.waitFor(befores, 2)).map((c) => c.http!.url);
    expect(urls).toEqual([`${api.url}/get`, `${api.url}/named`]);
  });

  it('blocks with an error event', async () => {
    echo.decide = () => ({ action: 'block' });
    await expect(call(http, `${api.url}/blocked`)).rejects.toMatchObject({ code: 'EECHOBLOCKED', message: 'blocked by Echo' });
    expect(api.requests).toHaveLength(0);
  });

  it('waits for a piped body larger than the socket buffer', async () => {
    const chunks = Array.from({ length: 50 }, (_, i) => Buffer.alloc(4096, 97 + (i % 26)));
    const got = await call(http, `${api.url}/piped`, { method: 'PUT' }, chunks);
    expect(got.status).toBe(200);
    const [c] = await echo.waitFor(befores);
    expect(c!.http!.body!.length).toBe(50 * 4096);
  });

  it('carries the run', async () => {
    await withEchoRun({ headers: { 'x-echo-run': 'r9/order' } }, () => call(http, `${api.url}/run`));
    const [c] = await echo.waitFor(befores);
    expect(c!.run).toBe('r9/order');
  });

  it('works under axios (follow-redirects), as Twilio, SendGrid and Postmark use it', async () => {
    const res = await axios.post('https://api.postmarkapp.com/email', { To: 'ann@example.test' }, { headers: { 'X-Postmark-Server-Token': 'pm-secret-token' } });
    expect(res.status).toBe(200);
    expect(res.data).toEqual({ id: 'faked-by-echo' });
    const [c] = await echo.waitFor(befores);
    expect(c!.http!.url).toBe('https://api.postmarkapp.com/email');
    expect(c!.http!.headers!['x-postmark-server-token']).toBe('•••••');
  });
});

describe('http.request, observe', () => {
  beforeEach(() => {
    echo.mode = 'observe';
  });

  it('sends for real and reports the response', async () => {
    const got = await call(http, `${api.url}/real`, { method: 'POST' }, 'hello');
    expect(got.status).toBe(201);
    expect(api.requests[0]).toMatchObject({ method: 'POST', url: '/real', body: 'hello' });
    await flush();
    const [a] = await echo.waitFor(afters);
    expect(a!.http).toMatchObject({ status: 201, response: '{"real":true,"got":5}' });
  });

  it('sends a piped body intact', async () => {
    const chunks = Array.from({ length: 40 }, () => Buffer.alloc(4096, 120));
    const got = await call(http, `${api.url}/piped-real`, { method: 'POST' }, chunks);
    expect(got.status).toBe(201);
    expect(api.requests[0]!.body.length).toBe(40 * 4096);
  });

  it('works with keep-alive agents and agent: false', async () => {
    const agent = new http.Agent({ keepAlive: true });
    try {
      for (let i = 0; i < 3; i++) expect((await call(http, `${api.url}/ka${i}`, { agent })).status).toBe(201);
      expect((await call(http, `${api.url}/noagent`, { agent: false })).status).toBe(201);
    } finally {
      agent.destroy();
    }
    expect(api.requests.map((r) => r.url)).toEqual(['/ka0', '/ka1', '/ka2', '/noagent']);
  });

  it('decodes a gzip response for the capture', async () => {
    const gz = http.createServer((_req, res) => {
      res.writeHead(200, { 'content-encoding': 'gzip', 'content-type': 'text/plain' }).end(zlib.gzipSync('zipped hello'));
    });
    await new Promise<void>((r) => gz.listen(0, '127.0.0.1', r));
    try {
      const port = (gz.address() as { port: number }).port;
      await call(http, `http://127.0.0.1:${port}/gz`);
      await flush();
      const [a] = await echo.waitFor((c) => afters(c) && c.http!.url.endsWith('/gz'));
      expect(a!.http!.response).toBe('zipped hello');
    } finally {
      gz.close();
    }
  });

  it('does not keep a response nobody reads', async () => {
    await new Promise<void>((resolve, reject) => {
      const r = http.request(`${api.url}/fire-and-forget`, { method: 'POST' });
      r.on('error', reject);
      r.on('close', () => resolve());
      r.end('x');
    });
    await flush();
    expect(api.requests).toHaveLength(1);
  });
});
