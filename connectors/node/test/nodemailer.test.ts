// Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
// SPDX-License-Identifier: MIT

import { createHash } from 'node:crypto';
import { mkdtempSync, rmSync, writeFileSync } from 'node:fs';
import { createRequire } from 'node:module';
import { tmpdir } from 'node:os';
import { join } from 'node:path';
import nodemailer from 'nodemailer';
import { afterAll, beforeAll, beforeEach, describe, expect, it } from 'vitest';

import { register, withEchoRun } from '../src/index.js';
import { attachments, toEmail } from '../src/nodemailer.js';
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
  it('masks credential headers by name', () => {
    const e = toEmail({ headers: { 'X-Auth-Token': 'Zq9TOPSECRETvalue77', 'X-Request-Id': 'req-1' } });
    expect(e.headers).toEqual({ 'X-Auth-Token': '•••••', 'X-Request-Id': 'req-1' });
  });

  it('describes a PDF attachment, never sending its content', async () => {
    const pdf = Buffer.concat([Buffer.from('%PDF-1.7\n'), Buffer.alloc(4096, 7), Buffer.from('\n%%EOF\n')]);
    const t = nodemailer.createTransport(recorder().transport as never);
    await t.sendMail({ ...message, attachments: [{ filename: 'invoice-42.pdf', content: pdf }] });
    const [c] = await echo.waitFor((c) => c.kind === 'email');
    expect(c!.email!.attachments).toEqual([
      { name: 'invoice-42.pdf', type: 'application/pdf', size: pdf.length, sha256: createHash('sha256').update(pdf).digest('hex') },
    ]);
    expect(JSON.stringify(c)).not.toContain(pdf.toString('base64').slice(0, 40));
  });

  it('leaves the field out without attachments', async () => {
    const t = nodemailer.createTransport(recorder().transport as never);
    await t.sendMail(message);
    const [c] = await echo.waitFor((c) => c.kind === 'email');
    expect(c!.email).not.toHaveProperty('attachments');
  });

  it('reads files, data URIs and encoded strings; skips inline images', async () => {
    const dir = mkdtempSync(join(tmpdir(), 'echo-att-'));
    const file = join(dir, 'report.csv');
    writeFileSync(file, 'a,b\n1,2\n');
    const big = join(dir, 'big.zip');
    const bigBody = Buffer.alloc(300 << 10, 7);
    writeFileSync(big, bigBody);
    const sha = (b: string | Buffer) => createHash('sha256').update(b).digest('hex');
    const got = await attachments([
      { path: file },
      { filename: 'hi.txt', path: 'data:text/plain;base64,aGk=' },
      { filename: 'b.bin', content: 'aGk=', encoding: 'base64' },
      { filename: 'logo.png', content: Buffer.from('png'), cid: 'logo@shop' },
      { filename: 'remote.pdf', path: 'https://files.example.test/remote.pdf' },
      { path: big },
      { path: join(dir, 'missing.pdf') },
    ]);
    rmSync(dir, { recursive: true, force: true });
    expect(got).toEqual([
      { name: 'report.csv', type: 'text/csv', size: 8, sha256: sha('a,b\n1,2\n') },
      { name: 'hi.txt', type: 'text/plain', size: 2, sha256: sha('hi') },
      { name: 'b.bin', type: 'application/octet-stream', size: 2, sha256: sha('hi') },
      { name: 'remote.pdf', type: 'application/pdf' },
      // streamed in chunks: the hash still covers the whole file
      { name: 'big.zip', type: 'application/zip', size: bigBody.length, sha256: sha(bigBody) },
      // unreadable: name and type only, Nodemailer reports the error when it sends
      { name: 'missing.pdf', type: 'application/pdf' },
    ]);
  });

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
