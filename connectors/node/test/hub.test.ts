// Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
// SPDX-License-Identifier: MIT

// One Echo for many sites (an agency hub): each site names itself in
// source.site and may hold only a write token, which can send captures
// but not read them.

import http from 'node:http';
import { afterAll, beforeAll, describe, expect, it } from 'vitest';

import { resolveConfig } from '../src/config.js';
import { flush, register, withEchoRun } from '../src/index.js';
import { fakeEcho, signRun, silent, target, type FakeEcho, type Target } from './helpers.js';

describe('configuration', () => {
  it('sends the write token and checks runs with the token', () => {
    const both = resolveConfig({}, { RHYTHM_ECHO_TOKEN: 'echo', RHYTHM_ECHO_WRITE_TOKEN: 'site', RHYTHM_ECHO_SITE_ID: ' client-a ' });
    expect(both).toMatchObject({ token: 'echo', sendToken: 'site', siteId: 'client-a' });
    // Only a write token: Echo signs this site's runs with it.
    expect(resolveConfig({}, { RHYTHM_ECHO_WRITE_TOKEN: 'site' })).toMatchObject({ token: 'site', sendToken: 'site', siteId: '' });
    expect(resolveConfig({}, { RHYTHM_ECHO_TOKEN: 'echo' })).toMatchObject({ token: 'echo', sendToken: 'echo' });
    expect(resolveConfig({ siteId: 'b', writeToken: 'w' }, {})).toMatchObject({ siteId: 'b', sendToken: 'w' });
  });
});

describe('a site on a hub', () => {
  let echo: FakeEcho;
  let api: Target;

  beforeAll(async () => {
    echo = await fakeEcho('seal');
    api = await target();
    process.env.RHYTHM_ECHO_SITE_ID = 'client-a';
    process.env.RHYTHM_ECHO_WRITE_TOKEN = 'client-a-write';
    try {
      await register({ url: echo.url, captureLoopback: true, logger: silent() });
    } finally {
      delete process.env.RHYTHM_ECHO_SITE_ID;
      delete process.env.RHYTHM_ECHO_WRITE_TOKEN;
    }
  });
  afterAll(async () => {
    await echo.close();
    await api.close();
  });

  it('names the site in every capture and sends only the write token', async () => {
    await withEchoRun({ headers: { 'x-echo-run': signRun('client-a-write', 'hub1/checkout') } }, async () => {
      await fetch(`${api.url}/fetch`, { method: 'POST', body: 'x' });
      await new Promise((ok, no) => http.get(`${api.url}/http`, (res) => res.resume().on('end', ok)).on('error', no));
    });
    await flush();
    expect(echo.captures.length).toBeGreaterThanOrEqual(2);
    for (const c of echo.captures) {
      expect(c.source.site).toBe('client-a');
      expect(c.run).toBe('hub1/checkout');
    }
    expect(new Set(echo.tokens)).toEqual(new Set(['client-a-write']));
  });
});
