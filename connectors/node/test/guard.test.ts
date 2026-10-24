// Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
// SPDX-License-Identifier: MIT

import http from 'node:http';
import type { AddressInfo } from 'node:net';
import { afterAll, beforeAll, describe, expect, it } from 'vitest';

import { guard, resolveConfig } from '../src/config.js';
import { register } from '../src/index.js';
import { runFromHeaders } from '../src/run.js';
import { state } from '../src/state.js';
import { fakeEcho, silent, target, type Target } from './helpers.js';

describe('production guard', () => {
  const check = (e: Record<string, string>) => guard(resolveConfig({}, e), e);
  const on = (e: Record<string, string>) => check(e).active;
  const url = 'http://echo.internal:8025';

  it('stays off in production', () => {
    expect(on({ VERCEL_ENV: 'production' })).toBe(false);
    expect(on({ VERCEL_ENV: 'production', NODE_ENV: 'production' })).toBe(false);
    expect(on({ VERCEL_ENV: 'production', NODE_ENV: 'development' })).toBe(false);
    // a self-hosted production server, nothing pointing at Echo
    expect(on({ NODE_ENV: 'production' })).toBe(false);
    // next build: static generation must never be blocked
    expect(on({ NEXT_PHASE: 'phase-production-build', NODE_ENV: 'production', VERCEL_ENV: 'preview' })).toBe(false);
  });

  // security #7: each row was active before
  it('treats unset NODE_ENV outside Vercel as production', () => {
    expect(check({})).toMatchObject({ active: false, reason: expect.stringContaining('NODE_ENV unset') });
    expect(check({}).warning).toBeUndefined();
    expect(on({ NODE_ENV: ' ' })).toBe(false);
    // pointing it at Echo is a choice to use it there
    expect(check({ RHYTHM_ECHO_URL: url })).toEqual({ active: true, reason: 'NODE_ENV unset, RHYTHM_ECHO_URL set' });
  });

  it('treats a misspelled NODE_ENV as production, with a warning', () => {
    for (const v of ['prod', 'prodution', 'dev', 'staging2']) {
      const g = check({ NODE_ENV: v, RHYTHM_ECHO_URL: url });
      expect(g.active).toBe(false);
      expect(g.reason).toBe(`NODE_ENV=${v}, so production`);
      expect(g.warning).toContain(`NODE_ENV=${v} is not one of development, test, staging, preview, local or production`);
    }
  });

  it('needs RHYTHM_ECHO_FORCE, not just the URL, with NODE_ENV=production', () => {
    expect(check({ NODE_ENV: 'production', RHYTHM_ECHO_URL: url })).toMatchObject({ active: false, reason: expect.stringContaining('RHYTHM_ECHO_FORCE=1') });
    expect(check({ NODE_ENV: 'production', RHYTHM_ECHO_URL: url, RHYTHM_ECHO_FORCE: '1' })).toEqual({ active: true, reason: 'forced by RHYTHM_ECHO_FORCE' });
  });

  it('runs in preview and development', () => {
    expect(on({ VERCEL_ENV: 'preview', NODE_ENV: 'production' })).toBe(true);
    expect(on({ VERCEL_ENV: 'development' })).toBe(true);
    expect(check({ NODE_ENV: 'development' })).toEqual({ active: true, reason: 'NODE_ENV=development' });
    for (const v of ['test', 'staging', 'preview', 'local', 'Development']) expect(on({ NODE_ENV: v })).toBe(true);
  });

  it('warns once on register and stays off', async () => {
    const before = process.env.NODE_ENV;
    process.env.NODE_ENV = 'prod';
    try {
      const log = silent();
      expect(await register({ logger: log })).toBe(false);
      expect(await register({ logger: log })).toBe(false);
      expect(log.lines).toEqual([expect.stringMatching(/^\[rhythm-echo\] NODE_ENV=prod is not one of .* treating it as production and staying off$/)]);
    } finally {
      process.env.NODE_ENV = before;
    }
  });

  // A staging server running `next start` has NODE_ENV=production: with
  // Echo configured, staying off silently would send real mail unexplained.
  it('says why it is off when Echo is configured', async () => {
    const before = { ...process.env };
    try {
      for (const [k, v] of Object.entries({ NODE_ENV: 'production', RHYTHM_ECHO_URL: 'http://127.0.0.1:20499' })) process.env[k] = v;
      state().warned.clear();
      const log = silent();
      expect(await register({ logger: log })).toBe(false);
      expect(await register({ logger: log })).toBe(false);
      expect(log.lines).toEqual(['[rhythm-echo] off: NODE_ENV=production (set RHYTHM_ECHO_FORCE=1 only on a server that is not production)']);

      // an option counts as configuring it too
      delete process.env.RHYTHM_ECHO_URL;
      state().warned.clear();
      const log2 = silent();
      expect(await register({ url: 'http://127.0.0.1:20499', logger: log2 })).toBe(false);
      expect(log2.lines).toHaveLength(1);

      // nothing points at Echo: a production server that merely has the package stays quiet
      state().warned.clear();
      const log3 = silent();
      expect(await register({ logger: log3 })).toBe(false);
      expect(log3.lines).toHaveLength(0);

      // certain production stays quiet even with settings shared from preview
      process.env.VERCEL_ENV = 'production';
      process.env.RHYTHM_ECHO_URL = 'http://127.0.0.1:20499';
      state().warned.clear();
      const log4 = silent();
      expect(await register({ logger: log4 })).toBe(false);
      expect(log4.lines).toHaveLength(0);
    } finally {
      for (const k of Object.keys(process.env)) if (!(k in before)) delete process.env[k];
      Object.assign(process.env, before);
      state().warned.clear();
    }
  });

  it('names the reason in the active line', async () => {
    const echo = await fakeEcho('seal');
    try {
      const log = silent();
      expect(await register({ url: echo.url, logger: log })).toBe(true);
      expect(log.lines[0]).toMatch(/^\[rhythm-echo\] active \(NODE_ENV=test, node\): capturing fetch, http, https/);
    } finally {
      await echo.close();
    }
  });

  it('can be forced', () => {
    expect(on({ VERCEL_ENV: 'production', RHYTHM_ECHO_FORCE: '1' })).toBe(true);
    expect(on({ NODE_ENV: 'production', RHYTHM_ECHO_FORCE: 'true' })).toBe(true);
    expect(on({ VERCEL_ENV: 'production', RHYTHM_ECHO_FORCE: '0' })).toBe(false);
  });

  it('reads the URL, token and site from the environment', () => {
    const c = resolveConfig({}, { RHYTHM_ECHO_URL: 'http://echo.test:9000/', RHYTHM_ECHO_TOKEN: 't', VERCEL_URL: 'app-git-x.vercel.app', RHYTHM_ECHO_SITE: 'https://shop.test/,cdn.test' });
    expect(c).toMatchObject({ url: 'http://echo.test:9000', echoHost: 'echo.test:9000', token: 't' });
    expect(c.site).toEqual(['shop.test', 'cdn.test', 'app-git-x.vercel.app']);
    expect(resolveConfig({}, {}).url).toBe('http://127.0.0.1:8025');
  });

  it('does nothing when registered in production', async () => {
    const echo = await fakeEcho('seal');
    const api = await target();
    process.env.VERCEL_ENV = 'production';
    try {
      const log = silent();
      expect(await register({ url: echo.url, captureLoopback: true, logger: log })).toBe(false);
      const res = await fetch(`${api.url}/prod`);
      expect(res.status).toBe(201);
      expect(echo.captures).toHaveLength(0);
      expect(log.lines).toHaveLength(0);
    } finally {
      delete process.env.VERCEL_ENV;
      await echo.close();
      await api.close();
    }
  });
});

describe('runFromHeaders', () => {
  it('reads the header, then the cookie, from any headers shape', () => {
    expect(runFromHeaders(new Headers({ 'x-echo-run': 'r/a' }))).toBe('r/a');
    expect(runFromHeaders({ 'X-Echo-Run': ['r/b'] })).toBe('r/b');
    expect(runFromHeaders({ cookie: 'a=1; X-Echo-Run=r%2Fc' })).toBe('r/c');
    expect(runFromHeaders({ 'x-echo-run': 'r/\u0000d\n' })).toBe('r/d');
    expect(runFromHeaders({})).toBeUndefined();
    expect(runFromHeaders(null)).toBeUndefined();
  });
});

describe('Echo unreachable', () => {
  let api: Target;
  let deadURL: string;
  const log = silent();

  beforeAll(async () => {
    api = await target();
    // a port that was just free: nothing listens there
    const s = http.createServer();
    await new Promise<void>((r) => s.listen(0, '127.0.0.1', r));
    deadURL = `http://127.0.0.1:${(s.address() as AddressInfo).port}`;
    await new Promise((r) => s.close(r));
    await register({ url: deadURL, captureLoopback: true, timeout: 1000, logger: log });
  });
  afterAll(() => api.close());

  it('fails closed when sealed, and says so once', async () => {
    await expect(fetch(`${api.url}/1`)).rejects.toThrow('blocked by Echo');
    await expect(fetch(`${api.url}/2`)).rejects.toThrow('blocked by Echo');
    const err = await new Promise<NodeJS.ErrnoException>((resolve) => {
      http.get(`${api.url}/3`, () => resolve(new Error('got a response'))).on('error', resolve);
    });
    expect(err.code).toBe('EECHOBLOCKED');
    expect(api.requests).toHaveLength(0);
    const warns = log.lines.filter((l) => l.includes('unreachable'));
    expect(warns).toHaveLength(1);
    expect(warns[0]).toContain('blocking outbound requests');
  });

  it('lets requests through when Echo last said observe', async () => {
    state().mode = 'observe';
    try {
      const res = await fetch(`${api.url}/4`);
      expect(res.status).toBe(201);
      expect(api.requests).toHaveLength(1);
    } finally {
      state().mode = 'seal';
    }
  });
});
