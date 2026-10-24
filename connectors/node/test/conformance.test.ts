// Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
// SPDX-License-Identifier: MIT

// Echo's shared conformance vectors (conformance/testdata) and wire
// version fallback. Skipped when the package is used outside Echo's
// repository, where the vectors are not.

import { existsSync, readdirSync, readFileSync } from 'node:fs';
import http from 'node:http';
import type { AddressInfo } from 'node:net';
import { join } from 'node:path';
import { afterAll, beforeAll, describe, expect, it } from 'vitest';

import { register } from '../src/index.js';
import { state } from '../src/state.js';
import { downgrade, type Capture } from '../src/wire.js';
import { fakeEcho, silent, target, type FakeEcho, type Target } from './helpers.js';
import { captureProblems, decisionProblems, hasSchema, repo } from './schema.js';

interface Vector {
  name: string;
  valid: boolean;
  problems?: string[];
  mode?: string;
  echo?: string;
  capture: Record<string, unknown>;
  decision?: Record<string, unknown>;
  connector?: string;
}

const dir = join(repo, 'conformance', 'testdata');
const vectors: [string, Vector][] = existsSync(dir)
  ? readdirSync(dir)
      .filter((f) => f.endsWith('.json') && f !== 'run-signature.json')
      .map((f) => [f, JSON.parse(readFileSync(join(dir, f), 'utf8')) as Vector])
  : [];

describe.skipIf(!hasSchema() || vectors.length === 0)('conformance vectors', () => {
  it.each(vectors)('%s: valid as it says, with the same problems as Echo finds', (_, v) => {
    const problems = captureProblems(v.capture);
    expect(problems.length === 0, problems.join('\n')).toBe(v.valid);
    for (const want of v.problems ?? []) expect(problems.join('\n')).toContain(want);
    if (v.decision) expect(decisionProblems({ id: 'm1', ...v.decision })).toEqual([]);
  });

  it('downgrade leaves out what wire 1 lacks', () => {
    const v2 = vectors.find(([f]) => f === 'capture-v2-site-attachments.json')![1].capture as unknown as Capture;
    expect(captureProblems({ ...downgrade(v2), v: 1 })).toEqual([]);
    expect(captureProblems({ ...v2, v: 1 })).not.toEqual([]);
  });

  // Every fail-closed vector, through the fetch hook: no decision from Echo
  // (nothing listening, a 5xx, no answer in time) blocks while sealed and
  // sends while observing.
  describe('when Echo gives no decision', () => {
    let api: Target;
    let broken: http.Server;
    let brokenURL = '';
    let deadURL = '';
    let status = 503;

    beforeAll(async () => {
      api = await target();
      broken = http.createServer((req, res) => {
        if (status === 0) return; // never answers
        res.writeHead(status).end('echo is broken\n');
      });
      await new Promise<void>((r) => broken.listen(0, '127.0.0.1', r));
      brokenURL = `http://127.0.0.1:${(broken.address() as AddressInfo).port}`;
      const s = http.createServer();
      await new Promise<void>((r) => s.listen(0, '127.0.0.1', r));
      deadURL = `http://127.0.0.1:${(s.address() as AddressInfo).port}`;
      await new Promise((r) => s.close(r));
    });
    afterAll(async () => {
      broken.closeAllConnections();
      await new Promise((r) => broken.close(r));
      await api.close();
    });

    const failing = vectors.filter(([, v]) => v.echo && v.echo !== 'up');
    it.each(failing)('%s', async (_, v) => {
      status = v.echo === 'timeout' ? 0 : 503;
      await register({ url: v.echo === 'unreachable' ? deadURL : brokenURL, captureLoopback: true, timeout: 300, logger: silent() });
      state().mode = v.mode ?? 'seal';
      const before = api.requests.length;
      try {
        if (v.connector === 'block') {
          await expect(fetch(`${api.url}/vector`)).rejects.toThrow('blocked by Echo');
          expect(api.requests.length).toBe(before);
        } else {
          expect((await fetch(`${api.url}/vector`)).status).toBe(201);
          expect(api.requests.length).toBe(before + 1);
        }
      } finally {
        state().mode = 'seal';
      }
    });
  });
});

describe('an Echo that speaks wire 1', () => {
  let echo: FakeEcho;
  let api: Target;
  const log = silent();

  beforeAll(async () => {
    echo = await fakeEcho('seal');
    echo.wire = 1;
    api = await target();
    await register({ url: echo.url, captureLoopback: true, site: ['shop.test'], logger: log });
  });
  afterAll(async () => {
    await echo.close();
    await api.close();
  });

  it('gets wire 1 captures after one refusal, with one warning', async () => {
    expect((await fetch(`${api.url}/a`)).status).toBe(200);
    expect((await fetch(`${api.url}/b`)).status).toBe(200);
    expect(echo.captures.map((c) => c.v)).toEqual([1, 1]);
    expect(echo.health).toBe(1);
    expect(echo.invalid).toEqual([]);
    const warns = log.lines.filter((l) => l.includes('speaks wire 1'));
    expect(warns).toEqual([`[rhythm-echo] Echo at ${echo.url} speaks wire 1: sending that (rhythm update gets a newer Echo)`]);
  });

  it('asks again when pointed at another Echo', async () => {
    const newer = await fakeEcho('seal');
    try {
      await register({ url: newer.url, captureLoopback: true, logger: log });
      await fetch(`${api.url}/c`);
      expect(newer.captures.map((c) => c.v)).toEqual([2]);
    } finally {
      await newer.close();
    }
  });
});
