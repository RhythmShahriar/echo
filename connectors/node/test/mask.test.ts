// Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
// SPDX-License-Identifier: MIT

import { describe, expect, it } from 'vitest';

import { mask, maskHeader, maskHeaders } from '../src/capture.js';

const b64 = (s: string) => Buffer.from(s).toString('base64');

describe('maskHeader', () => {
  it.each([
    ['x-auth-token', 'Zq9TOPSECRETvalue77'],
    ['X-Amz-Security-Token', 'Zq9TOPSECRETvalue77'],
    ['private-token', 'Zq9TOPSECRETvalue77'],
    ['x-goog-api-key', 'Zq9TOPSECRETvalue77'],
    ['x-shopify-access-token', 'Zq9TOPSECRETvalue77'],
    ['x-wc-webhook-signature', 'Zq9TOPSECRETvalue77'],
    ['x-session-id', 'Zq9TOPSECRETvalue77'],
    ['x-db-password', 'Zq9TOPSECRETvalue77'],
    ['set-cookie', 'sid=Zq9TOPSECRETvalue77'],
    ['x-client-credential', 'Zq9TOPSECRETvalue77'],
  ])('hides %s whole', (name, value) => {
    expect(maskHeader(name, value)).toBe('•••••');
  });

  it.each([
    ['x-request-id', 'req-1234567890abcdef'],
    ['content-type', 'application/json'],
    ['x-echo-run', 'r1/checkout.NhOp3HY0JQ76wSoPnn3sZg'],
    ['user-agent', 'stripe-node/14'],
  ])('keeps %s', (name, value) => {
    expect(maskHeader(name, value)).toBe(value);
  });

  it('shows at most four characters of a long credential', () => {
    const got = maskHeader('x-api-key', 'abcdefghijklmnopqrstuvwxyz0123456789');
    expect(got.replace(/•/g, '').length).toBeLessThanOrEqual(4);
  });

  it('keeps the scheme and a Stripe mode prefix', () => {
    expect(maskHeader('authorization', 'Bearer re_live_abcdefghijkl')).toBe('Bearer •••••');
    expect(maskHeader('Authorization', 'Bearer sk_test_abcdefghijkl')).toBe('Bearer sk_test_•••••');
    expect(maskHeader('authorization', `Basic ${b64('sk_live_abcdefghijkl:')}`)).toBe('Basic sk_live_•••••');
    expect(maskHeader('authorization', `Basic ${b64('user:hunter2')}`)).toBe('Basic •••••');
    expect(maskHeader('proxy-authorization', 'opaque')).toBe('•••••');
  });

  it('leaves an empty value empty', () => {
    expect(maskHeader('x-auth-token', '')).toBe('');
    expect(mask('')).toBe('');
  });
});

describe('maskHeaders', () => {
  it('lowercases names and masks by name', () => {
    expect(maskHeaders([['X-Auth-Token', 'Zq9TOPSECRETvalue77'], ['X-Request-Id', 'abc']])).toEqual({
      'x-auth-token': '•••••',
      'x-request-id': 'abc',
    });
  });
});
