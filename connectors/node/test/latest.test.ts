// Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
// SPDX-License-Identifier: MIT

// @rhythmsh/echo/playwright and /cypress against a fake Echo that answers
// GET /v1/messages/latest the way Echo does: it holds the request for up
// to `wait` and answers 404 when nothing arrived.

import http from 'node:http';
import type { AddressInfo } from 'node:net';
import { afterAll, afterEach, beforeAll, describe, expect, it } from 'vitest';

import { registerEchoCommands } from '../src/cypress.js';
import { echo, latest } from '../src/playwright.js';
import { waitMs, waitParam } from '../src/latest.js';

interface Seen {
  query: URLSearchParams;
  token: string | undefined;
}

const otp = {
  codes: ['482913'],
  links: [{ kind: 'magic', url: 'https://shop.test/login?t=abc' }],
  message: { id: 'm1', channel: 'email', to: ['ann@example.test'], subject: 'Your code' },
};

let server: http.Server;
let url = '';
const seen: Seen[] = [];
// arrives: when the message "arrives", in ms after the request; null: never.
let arrives: number | null = 0;

beforeAll(async () => {
  server = http.createServer((req, res) => {
    const u = new URL(req.url ?? '/', 'http://x');
    if (u.pathname !== '/v1/messages/latest') {
      res.writeHead(404).end();
      return;
    }
    seen.push({ query: u.searchParams, token: req.headers['x-echo-token'] as string | undefined });
    if (process.env.FAKE_ECHO_TOKEN && req.headers['x-echo-token'] !== process.env.FAKE_ECHO_TOKEN) {
      res.writeHead(401).end('missing or wrong X-Echo-Token\n');
      return;
    }
    const wait = waitMs(u.searchParams.get('wait') ?? '0s');
    if (arrives === null || arrives > wait) {
      setTimeout(() => res.writeHead(404).end(`no message matched within ${u.searchParams.get('wait')}\n`), Math.min(wait, 200));
      return;
    }
    setTimeout(() => res.writeHead(200, { 'content-type': 'application/json' }).end(JSON.stringify(otp)), arrives);
  });
  await new Promise<void>((r) => server.listen(0, '127.0.0.1', r));
  url = `http://127.0.0.1:${(server.address() as AddressInfo).port}`;
});
afterAll(() => new Promise<void>((r) => server.close(() => r())));
afterEach(() => {
  seen.length = 0;
  arrives = 0;
  delete process.env.RHYTHM_ECHO_URL;
  delete process.env.RHYTHM_ECHO_TOKEN;
  delete process.env.FAKE_ECHO_TOKEN;
});

describe('playwright', () => {
  it('returns the codes and links, with the first of each picked out', async () => {
    const got = await latest({ to: 'ann@example.test', channel: ['email', 'sms'], action: 'login' }, { url });
    expect(got.code).toBe('482913');
    expect(got.link).toBe('https://shop.test/login?t=abc');
    expect(got.codes).toEqual(['482913']);
    expect(got.message.subject).toBe('Your code');
    const q = seen[0]!.query;
    expect(q.get('to')).toBe('ann@example.test');
    expect(q.get('channel')).toBe('email,sms');
    expect(q.get('action')).toBe('login');
    expect(q.get('wait')).toBe('10s');
  });

  it('waits for a message that arrives late', async () => {
    arrives = 300;
    const got = await echo.latest({ to: 'ann@example.test', wait: 2000 }, { url });
    expect(got.code).toBe('482913');
    expect(seen[0]!.query.get('wait')).toBe('2000ms');
  });

  it('throws when nothing arrives in time', async () => {
    arrives = null;
    await expect(latest({ to: 'bob@example.test', wait: '1s' }, { url })).rejects.toThrow(
      /no message to bob@example.test: no message matched within 1s/,
    );
  });

  it('takes Echo and the token from the environment', async () => {
    process.env.RHYTHM_ECHO_URL = url + '/';
    process.env.RHYTHM_ECHO_TOKEN = 'tok';
    process.env.FAKE_ECHO_TOKEN = 'tok';
    await latest({ to: 'ann@example.test' });
    expect(seen[0]!.token).toBe('tok');
  });

  it('reports a refused token with Echo\'s words', async () => {
    process.env.FAKE_ECHO_TOKEN = 'tok';
    await expect(latest({ to: 'ann@example.test' }, { url, token: 'nope' })).rejects.toThrow(/answered 401 .*wrong X-Echo-Token/);
  });

  it('turns waits into Go durations', () => {
    expect(waitParam(undefined)).toBe('10s');
    expect(waitParam(1500)).toBe('1500ms');
    expect(waitParam('1m')).toBe('1m');
    expect(waitMs('1.5s')).toBe(1500);
    expect(waitMs('1m')).toBe(60000);
    expect(waitMs(undefined)).toBe(10000);
    expect(waitMs(250)).toBe(250);
    expect(waitMs('0')).toBe(0);
    // compound Go durations: the client must wait as long as Echo does
    expect(waitMs('1m30s')).toBe(90000);
    expect(waitMs('1h2m3.5s')).toBe(3723500);
    expect(waitMs('1500us')).toBe(1.5);
  });

  it('refuses a wait Echo would not read instead of guessing', () => {
    for (const w of ['', '10', 'soon', '1m30', '-1s', '1d', '10 s']) {
      expect(() => waitMs(w)).toThrow(/is not a duration Echo reads/);
    }
  });
});

describe('cypress', () => {
  it('registers cy.echoLatest, which calls Echo through cy.request', async () => {
    const commands: Record<string, (...a: unknown[]) => unknown> = {};
    const env: Record<string, string> = { RHYTHM_ECHO_URL: url, RHYTHM_ECHO_TOKEN: 'cy-token' };
    const requests: { headers: Record<string, string>; failOnStatusCode: boolean; timeout: number }[] = [];
    const g = globalThis as Record<string, unknown>;
    g.Cypress = { env: (k: string) => env[k], Commands: { add: (name: string, fn: (...a: unknown[]) => unknown) => (commands[name] = fn) } };
    // cy.request, as a chainable that settles with the response.
    g.cy = {
      request(o: { url: string; headers: Record<string, string>; failOnStatusCode: boolean; timeout: number }) {
        requests.push(o);
        const res = fetch(o.url, { headers: o.headers }).then(async (r) => ({ status: r.status, body: await r.text() }));
        return { then: (fn: (r: { status: number; body: unknown }) => unknown) => res.then(fn) };
      },
    };
    try {
      expect(registerEchoCommands()).toBe(true);
      const got = (await commands.echoLatest!({ to: 'ann@example.test', wait: '2s' })) as { code?: string };
      expect(got.code).toBe('482913');
      expect(requests[0]!.headers['X-Echo-Token']).toBe('cy-token');
      expect(requests[0]!.failOnStatusCode).toBe(false);
      expect(requests[0]!.timeout).toBe(7000);
      expect(seen[0]!.query.get('wait')).toBe('2s');

      arrives = null;
      await expect(commands.echoLatest!({ to: 'bob@example.test', wait: '1s' })).rejects.toThrow(/no message to bob@example.test/);
    } finally {
      delete g.Cypress;
      delete g.cy;
    }
  });

  it('does nothing outside Cypress', () => {
    expect(registerEchoCommands()).toBe(false);
  });
});
