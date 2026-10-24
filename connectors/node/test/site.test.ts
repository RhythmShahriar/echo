// Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
// SPDX-License-Identifier: MIT

// The site's own host comes from configuration, never from a request
// header a visitor controls (security #2). A forged X-Forwarded-Host must
// not unseal a provider. Calls to the configured site still go out and are
// recorded.

import http from 'node:http';

import { afterAll, beforeAll, beforeEach, describe, expect, it } from 'vitest';

import { siteHost } from '../src/capture.js';
import { flush, register, withEchoRun } from '../src/index.js';
import { afters, befores, fakeEcho, silent, target, type FakeEcho, type Target } from './helpers.js';

let echo: FakeEcho;
let shop: Target;

beforeAll(async () => {
  echo = await fakeEcho('seal');
  shop = await target();
  process.env.RHYTHM_ECHO_SITE = 'https://shop.test';
  try {
    await register({ url: echo.url, logger: silent() });
  } finally {
    delete process.env.RHYTHM_ECHO_SITE;
  }
});
afterAll(async () => {
  await echo.close();
  await shop.close();
});
beforeEach(async () => {
  await flush();
  echo.captures.length = 0;
  shop.requests.length = 0;
});

/**
 * getShop calls http://shop.test:<port><path>, resolving shop.test to the
 * local stand-in. Its own agent skips the dead test proxy that the global
 * agent uses for every non-loopback host.
 */
function getShop(path: string): Promise<{ status: number; body: string }> {
  const port = new URL(shop.url).port;
  return new Promise((resolve, reject) => {
    http
      .get(
        {
          host: 'shop.test',
          port,
          path,
          agent: new http.Agent(),
          lookup: ((_h: string, o: { all?: boolean }, cb: (...a: unknown[]) => void) =>
            o.all ? cb(null, [{ address: '127.0.0.1', family: 4 }]) : cb(null, '127.0.0.1', 4)) as unknown as http.RequestOptions['lookup'],
        },
        (res) => {
          let body = '';
          res.setEncoding('utf8');
          res.on('data', (d: string) => (body += d));
          res.on('end', () => resolve({ status: res.statusCode ?? 0, body }));
        },
      )
      .on('error', reject);
  });
}

describe('the site host', () => {
  it('ignores a forged X-Forwarded-Host: the provider call is captured and faked', async () => {
    const forged = { headers: { host: 'api.sendgrid.com', 'x-forwarded-host': 'api.sendgrid.com', 'x-echo-run': 'r1/contact' } };
    const res = await withEchoRun(forged, () =>
      fetch('https://api.sendgrid.com/v3/mail/send', { method: 'POST', body: '{"personalizations":[]}' }),
    );
    expect(res.headers.get('x-echo')).toBe('fake');
    const [c] = await echo.waitFor(befores);
    expect(c).toMatchObject({ run: 'r1/contact', source: { host: 'shop.test' }, http: { url: 'https://api.sendgrid.com/v3/mail/send' } });
  });

  it('lets calls to the configured site through and records them as sent', async () => {
    const res = await withEchoRun({ headers: { 'x-echo-run': 'r1/self' } }, () => getShop('/wp-cron'));
    expect(res.status).toBe(201);
    expect(shop.requests).toHaveLength(1);
    const [before] = await echo.waitFor(befores);
    expect(before).toMatchObject({ run: 'r1/self', source: { host: 'shop.test' } });
    const [after] = await echo.waitFor(afters);
    expect(after).toMatchObject({ id: expect.any(String), http: { status: 201 } });
  });
});

describe('siteHost', () => {
  const cfg = { site: ['shop.test', 'cdn.test:8443'] };
  it('reports the matching site host, else the first one', () => {
    expect(siteHost('SHOP.test', '8080', cfg)).toBe('shop.test');
    expect(siteHost('cdn.test', '8443', cfg)).toBe('cdn.test:8443');
    expect(siteHost('cdn.test', '443', cfg)).toBe('shop.test');
    expect(siteHost('api.twilio.com', '', cfg)).toBe('shop.test');
    expect(siteHost('api.twilio.com', '', { site: [] })).toBeUndefined();
  });
});
