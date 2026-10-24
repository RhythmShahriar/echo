import { createRequire } from 'node:module';
import nodemailer from 'nodemailer';
import { afterAll, beforeAll, beforeEach, describe, expect, it } from 'vitest';

import { register, withEchoRun } from '../src/index.js';
import { fakeEcho, silent, type FakeEcho } from './helpers.js';

const require = createRequire(import.meta.url);
const nodemailerCJS = require('nodemailer') as typeof nodemailer;

let echo: FakeEcho;
const log = silent();

beforeAll(async () => {
  echo = await fakeEcho('seal');
  await register({ url: echo.url, logger: log });
});
afterAll(() => echo.close());
beforeEach(() => {
  echo.captures.length = 0;
  echo.mode = 'seal';
});

/** recorder is a custom transport: it shows whether Nodemailer really sent. */
function recorder() {
  const sent: unknown[] = [];
  return {
    sent,
    transport: {
      name: 'recorder',
      version: '1',
      send(mail: { data: unknown }, cb: (err: Error | null, info: unknown) => void) {
        sent.push(mail.data);
        cb(null, { messageId: '<real@test>', accepted: ['x'] });
      },
    },
  };
}

const message = {
  from: '"Shop" <shop@example.test>',
  to: 'Ann <ann@example.test>, bob@example.test',
  cc: [{ name: 'Doe, J', address: 'j@example.test' }],
  subject: 'Order #1001',
  text: 'Thanks',
  html: '<p>Thanks</p>',
  headers: { 'X-Order': '1001' },
};

describe('nodemailer', () => {
  it('announces the hook', () => {
    expect(log.lines.join('\n')).toContain('nodemailer');
  });

  it('sealed: captures and resolves a fake info without sending (ESM build)', async () => {
    const r = recorder();
    const t = nodemailer.createTransport(r.transport as never);
    const info = (await withEchoRun('r1/order', () => t.sendMail(message))) as unknown as Record<string, unknown>;
    expect(r.sent).toHaveLength(0);
    expect(info.messageId).toMatch(/^<m\d+@echo\.invalid>$/);
    expect(info.accepted).toEqual(['ann@example.test', 'bob@example.test', 'j@example.test']);
    const [c] = await echo.waitFor((c) => c.kind === 'email');
    expect(c).toMatchObject({
      kind: 'email',
      run: 'r1/order',
      source: { via: 'node', hook: 'nodemailer' },
      email: {
        from: '"Shop" <shop@example.test>',
        to: ['Ann <ann@example.test>', 'bob@example.test'],
        cc: ['Doe, J <j@example.test>'],
        subject: 'Order #1001',
        text: 'Thanks',
        html: '<p>Thanks</p>',
        headers: { 'X-Order': '1001' },
      },
    });
  });

  it('sealed: the callback form and the CJS build work too', async () => {
    const r = recorder();
    const t = nodemailerCJS.createTransport(r.transport as never, { from: 'defaults@example.test' });
    const info = await new Promise<Record<string, unknown>>((resolve, reject) =>
      t.sendMail({ to: 'c@example.test', subject: 'cb' }, (err, i) => (err ? reject(err) : resolve(i as never))),
    );
    expect(r.sent).toHaveLength(0);
    expect(info.accepted).toEqual(['c@example.test']);
    const [c] = await echo.waitFor((c) => c.kind === 'email');
    expect(c!.email!.from).toBe('defaults@example.test');
  });

  it('observe: captures and sends normally', async () => {
    echo.mode = 'observe';
    const r = recorder();
    const t = nodemailer.createTransport(r.transport as never);
    const info = (await t.sendMail(message)) as unknown as Record<string, unknown>;
    expect(r.sent).toHaveLength(1);
    expect(info.messageId).toBe('<real@test>');
    await echo.waitFor((c) => c.kind === 'email');
  });

  it('covers the built-in transports', async () => {
    // jsonTransport never touches the network
    const t = nodemailerCJS.createTransport({ jsonTransport: true });
    const info = (await t.sendMail({ to: 'd@example.test', subject: 'early' })) as unknown as Record<string, unknown>;
    expect(info.response).toContain('Captured by Echo');
  });
});
