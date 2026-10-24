// Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
// SPDX-License-Identifier: MIT

// The real provider SDKs, sealed. Requests go to the real provider hosts,
// so each one must be answered by Echo without a connection: vitest.config.ts
// routes anything that escapes to a dead proxy, which would fail the test.

import { afterAll, beforeAll, beforeEach, describe, expect, it } from 'vitest';

import { flush, register } from '../src/index.js';
import type { Capture, Response as WireResponse } from '../src/wire.js';
import { afters, befores, fakeEcho, silent, target, type FakeEcho, type Target } from './helpers.js';

// Fake credentials, assembled so no key-shaped literal sits in the source.
const sgKey = ['S', 'G.', 'fake'.repeat(5), '.', 'fake'.repeat(10)].join('');
const reKey = ['re', '_', 'fakefakefake'].join('');
const twSid = ['A', 'C', '0'.repeat(32)].join('');
const twToken = 'f'.repeat(32);

let echo: FakeEcho;
let api: Target;

/** provider-shaped answers, like Echo's own recognizers give */
function shaped(c: Capture): WireResponse {
  const u = new URL(c.http!.url);
  switch (u.hostname) {
    case 'api.resend.com':
      return { status: 200, headers: { 'content-type': 'application/json' }, body: '{"id":"re-fake-1"}' };
    case 'api.sendgrid.com':
      return { status: 202, headers: { 'x-message-id': 'sg-fake-1' }, body: '' };
    case 'api.postmarkapp.com':
      return {
        status: 200,
        headers: { 'content-type': 'application/json' },
        body: JSON.stringify({ To: 'ann@example.test', SubmittedAt: new Date().toISOString(), MessageID: 'pm-fake-1', ErrorCode: 0, Message: 'OK' }),
      };
    case 'api.twilio.com':
      return {
        status: 201,
        headers: { 'content-type': 'application/json' },
        body: JSON.stringify({ sid: 'SM' + '0'.repeat(32), status: 'queued', to: '+15550001', body: 'Your code is 123456' }),
      };
  }
  return { status: 200, body: '{}' };
}

beforeAll(async () => {
  echo = await fakeEcho('seal');
  echo.decide = (c) => (echo.mode === 'seal' && c.kind === 'http' && c.phase === 'before' ? { action: 'fake', response: shaped(c) } : {});
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
});

const lastBefore = async () => (await echo.waitFor(befores)).at(-1)!;

describe('provider SDKs, sealed', () => {
  it('Resend (fetch)', async () => {
    const { Resend } = await import('resend');
    const resend = new Resend(reKey);
    const { data, error } = await resend.emails.send({ from: 'shop@example.test', to: 'ann@example.test', subject: 'Hi', html: '<p>Hi</p>' });
    expect(error).toBeNull();
    expect(data).toEqual({ id: 're-fake-1' });
    const c = await lastBefore();
    expect(c.http).toMatchObject({ method: 'POST', url: 'https://api.resend.com/emails' });
    expect(JSON.parse(c.http!.body!)).toMatchObject({ to: 'ann@example.test', subject: 'Hi' });
    expect(c.http!.headers!.authorization).toBe('Bearer •••••');
    expect(c.source.plugin).toBe('resend');
  });

  it('SendGrid (axios over https)', async () => {
    const sg = (await import('@sendgrid/mail')).default;
    sg.setApiKey(sgKey);
    const [res] = await sg.send({ from: 'shop@example.test', to: 'ann@example.test', subject: 'Hi', text: 'Hello' });
    expect(res.statusCode).toBe(202);
    const c = await lastBefore();
    expect(c.http).toMatchObject({ method: 'POST', url: 'https://api.sendgrid.com/v3/mail/send' });
    expect(c.source.hook).toBe('https.request');
    expect(c.source.plugin).toBe('@sendgrid/client');
    expect(JSON.stringify(c)).not.toContain(sgKey);
  });

  it('Postmark (fetch)', async () => {
    const postmark = await import('postmark');
    const client = new postmark.ServerClient('00000000-0000-0000-0000-000000000000');
    const res = await client.sendEmail({ From: 'shop@example.test', To: 'ann@example.test', Subject: 'Hi', TextBody: 'Hello' });
    expect(res.MessageID).toBe('pm-fake-1');
    const c = await lastBefore();
    expect(c.http).toMatchObject({ method: 'POST', url: 'https://api.postmarkapp.com/email' });
    expect(c.http!.headers!['x-postmark-server-token']).toBe('•••••');
  });

  it('Twilio (axios over https)', async () => {
    const twilio = (await import('twilio')).default;
    const client = twilio(twSid, twToken);
    const msg = await client.messages.create({ to: '+15550001', from: '+15550002', body: 'Your code is 123456' });
    expect(msg.status).toBe('queued');
    const c = await lastBefore();
    expect(c.http!.url).toBe(`https://api.twilio.com/2010-04-01/Accounts/${twSid}/Messages.json`);
    expect(c.http!.body).toContain('Body=Your%20code%20is%20123456');
    expect(c.http!.headers!.authorization).toBe('Basic •••••');
    expect(c.source.plugin).toBe('twilio');
  });
});

describe('provider SDKs, observe, against a local fake API', () => {
  it('Resend via RESEND_BASE_URL', async () => {
    echo.mode = 'observe';
    process.env.RESEND_BASE_URL = api.url;
    try {
      const { Resend } = await import('resend');
      await new Resend(reKey).emails.send({ from: 'shop@example.test', to: 'ann@example.test', subject: 'Hi', text: 'Hi' });
    } finally {
      delete process.env.RESEND_BASE_URL;
    }
    expect(api.requests.at(-1)!.url).toBe('/emails');
    await flush();
    const [a] = await echo.waitFor(afters);
    expect(a!.http!.status).toBe(201);
  });

  it('SendGrid via its baseUrl', async () => {
    echo.mode = 'observe';
    const sgClient = (await import('@sendgrid/client')).default;
    // the module exports a ready instance; its class gives a fresh one
    const client = new (sgClient.constructor as new () => typeof sgClient)();
    client.setApiKey(sgKey);
    client.setDefaultRequest('baseUrl', api.url);
    const [res] = await client.request({ method: 'POST', url: '/v3/mail/send', body: { personalizations: [] } });
    expect(res.statusCode).toBe(201);
    expect(api.requests.at(-1)!.url).toBe('/v3/mail/send');
    await flush();
    const [a] = await echo.waitFor(afters);
    expect(a!.source.hook).toBe('http.request');
  });
});
