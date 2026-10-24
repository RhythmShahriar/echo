// Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
// SPDX-License-Identifier: MIT

import { readFileSync } from 'node:fs';

import { afterEach, describe, expect, it } from 'vitest';

import { resolveConfig } from '../src/config.js';
import { accept, contextFromHeaders, verifyRun } from '../src/run.js';
import { state } from '../src/state.js';
import { silent } from './helpers.js';

interface Vectors {
  verify: { name: string; token: string; value: string; run: string; action: string; ok: boolean }[];
  accept: { name: string; token: string; value: string; remote: string; forwarded: boolean; run: string }[];
}

const vectors = JSON.parse(
  readFileSync(new URL('../../../conformance/testdata/run-signature.json', import.meta.url), 'utf8'),
) as Vectors;

const withToken = (token: string, log: Pick<Console, 'info' | 'warn'> = silent()) => {
  state().cfg = resolveConfig({ token, logger: log }, {});
  state().warned.clear();
  return log;
};

describe('verifyRun (shared vectors)', () => {
  it.each(vectors.verify.map((v) => [v.name, v] as const))('%s', async (_, v) => {
    expect(await verifyRun(v.token, v.value)).toEqual({ run: v.run, action: v.action, ok: v.ok });
  });

  it('works with Web Crypto too, as on Edge', async () => {
    const hmac = state().hmac;
    state().hmac = undefined;
    try {
      const v = vectors.verify[0]!;
      expect(await verifyRun(v.token, v.value)).toEqual({ run: v.run, action: v.action, ok: true });
    } finally {
      state().hmac = hmac;
    }
  });
});

describe('accept (shared vectors)', () => {
  afterEach(() => {
    state().cfg = undefined;
  });

  it.each(vectors.accept.map((v) => [v.name, v] as const))('%s', async (_, v) => {
    withToken(v.token);
    expect(await accept({ value: v.value, remote: v.remote, forwarded: v.forwarded })).toBe(v.run || undefined);
  });

  it('logs an ignored value once', async () => {
    const lines: string[] = [];
    withToken('secret', { info: () => {}, warn: (m: string) => void lines.push(m) });
    await accept({ value: 'r1/a', remote: '127.0.0.1', forwarded: false });
    await accept({ value: 'r1/b', remote: '127.0.0.1', forwarded: false });
    expect(lines).toEqual(['[rhythm-echo] ignored an unsigned X-Echo-Run: with RHYTHM_ECHO_TOKEN set, only runs Echo signed with it count']);
  });
});

describe('contextFromHeaders', () => {
  it('notes the peer and any forwarding header', () => {
    expect(contextFromHeaders({ 'x-echo-run': 'r/a' }, '127.0.0.1')).toEqual({ claim: { value: 'r/a', remote: '127.0.0.1', forwarded: false } });
    expect(contextFromHeaders({ 'x-echo-run': 'r/a', 'X-Real-IP': '198.51.100.7' })).toEqual({
      claim: { value: 'r/a', remote: undefined, forwarded: true },
    });
    expect(contextFromHeaders({})).toEqual({});
  });
});
