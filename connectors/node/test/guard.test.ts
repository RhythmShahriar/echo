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
  const on = (e: Record<string, string>) => guard(resolveConfig({}, e), e).active;

  it('stays off in production', () => {
    expect(on({ VERCEL_ENV: 'production' })).toBe(false);
    expect(on({ VERCEL_ENV: 'production', NODE_ENV: 'production' })).toBe(false);
    expect(on({ VERCEL_ENV: 'production', NODE_ENV: 'development' })).toBe(false);
    // a self-hosted production server, nothing pointing at Echo
    expect(on({ NODE_ENV: 'production' })).toBe(false);
    // next build: static generation must never be blocked
    expect(on({ NEXT_PHASE: 'phase-production-build', NODE_ENV: 'production', VERCEL_ENV: 'preview' })).toBe(false);
  });

  it('runs in preview and development', () => {
    expect(on({ VERCEL_ENV: 'preview', NODE_ENV: 'production' })).toBe(true);
    expect(on({ VERCEL_ENV: 'development' })).toBe(true);
    expect(on({ NODE_ENV: 'development' })).toBe(true);
    expect(on({})).toBe(true);
    expect(on({ NODE_ENV: 'production', RHYTHM_ECHO_URL: 'http://echo.internal:8025' })).toBe(true);
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
