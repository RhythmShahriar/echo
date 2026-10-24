// Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
// SPDX-License-Identifier: MIT

// A tiny Echo for tests: POST /v1/capture records the capture and answers
// like the real catcher (seal: fake HTTP and email; observe: allow; a call
// to the reported source.host: allow), unless a test sets its own decide().

import { createHmac } from 'node:crypto';
import http from 'node:http';
import type { AddressInfo } from 'node:net';

import type { Capture, Decision } from '../src/wire.js';

export interface FakeEcho {
  url: string;
  mode: 'seal' | 'observe';
  captures: Capture[];
  tokens: (string | undefined)[];
  decide?: (c: Capture) => Partial<Decision>;
  close(): Promise<void>;
  /** waitFor resolves once n captures match. */
  waitFor(pred: (c: Capture) => boolean, n?: number, ms?: number): Promise<Capture[]>;
}

export async function fakeEcho(mode: 'seal' | 'observe' = 'seal'): Promise<FakeEcho> {
  let seq = 0;
  const echo: FakeEcho = {
    url: '',
    mode,
    captures: [],
    tokens: [],
    close: () => new Promise((r) => server.close(() => r())),
    async waitFor(pred, n = 1, ms = 3000) {
      const end = Date.now() + ms;
      for (;;) {
        const got = echo.captures.filter(pred);
        if (got.length >= n) return got;
        if (Date.now() > end) throw new Error(`timed out waiting for ${n} captures; have ${JSON.stringify(echo.captures, null, 1)}`);
        await new Promise((r) => setTimeout(r, 10));
      }
    },
  };
  const server = http.createServer((req, res) => {
    if (req.method !== 'POST' || req.url !== '/v1/capture') {
      res.writeHead(404).end();
      return;
    }
    let body = '';
    req.setEncoding('utf8');
    req.on('data', (d: string) => (body += d));
    req.on('end', () => {
      const c = JSON.parse(body) as Capture;
      echo.captures.push(c);
      echo.tokens.push(req.headers['x-echo-token'] as string | undefined);
      const id = c.phase === 'after' && c.id ? c.id : `m${++seq}`;
      let d: Decision = { id, mode: echo.mode };
      if (c.kind === 'email') d.action = echo.mode === 'observe' ? 'allow' : 'fake';
      else if (c.phase === 'after') d.action = 'allow';
      else if (echo.mode === 'observe') d.action = 'allow';
      else if (c.source.host && new URL(c.http!.url).hostname === c.source.host.replace(/:\d+$/, '')) d.action = 'allow';
      else
        d = {
          ...d,
          action: 'fake',
          response: { status: 200, headers: { 'content-type': 'application/json', 'x-echo': 'fake' }, body: JSON.stringify({ id: 'faked-by-echo' }) },
        };
      if (echo.decide) d = { ...d, ...echo.decide(c) };
      res.writeHead(200, { 'content-type': 'application/json' }).end(JSON.stringify(d));
    });
  });
  await new Promise<void>((r) => server.listen(0, '127.0.0.1', r));
  echo.url = `http://127.0.0.1:${(server.address() as AddressInfo).port}`;
  return echo;
}

export interface Target {
  url: string;
  requests: { method: string; url: string; headers: http.IncomingHttpHeaders; body: string }[];
  close(): Promise<void>;
}

/** target is the "real" API an allowed request reaches. */
export async function target(): Promise<Target> {
  const t: Target = { url: '', requests: [], close: () => new Promise((r) => server.close(() => r())) };
  const server = http.createServer((req, res) => {
    let body = '';
    req.setEncoding('utf8');
    req.on('data', (d: string) => (body += d));
    req.on('end', () => {
      t.requests.push({ method: req.method ?? '', url: req.url ?? '', headers: req.headers, body });
      res.writeHead(201, { 'content-type': 'application/json' }).end(JSON.stringify({ real: true, got: body.length }));
    });
  });
  await new Promise<void>((r) => server.listen(0, '127.0.0.1', r));
  t.url = `http://127.0.0.1:${(server.address() as AddressInfo).port}`;
  return t;
}

/** silent collects log lines instead of printing them. */
export function silent(): { info: (m: string) => void; warn: (m: string) => void; lines: string[] } {
  const lines: string[] = [];
  return { lines, info: (m) => lines.push(m), warn: (m) => lines.push(m) };
}

export const befores = (c: Capture) => c.kind === 'http' && c.phase === 'before';
export const afters = (c: Capture) => c.kind === 'http' && c.phase === 'after';

/** signRun signs "<run>/<action>" the way Echo's wire.SignRun does. */
export function signRun(token: string, value: string): string {
  return `${value}.${createHmac('sha256', token).update(value).digest().subarray(0, 16).toString('base64url')}`;
}

/** loopback is a request from this machine, as withEchoRun sees an Express or Node request. */
export const loopback = { remoteAddress: '127.0.0.1' };
