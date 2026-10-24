// Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
// SPDX-License-Identifier: MIT

// The Nodemailer hook. Node only.
//
// It patches sendMail on the transporter class (Mailer.prototype) rather
// than the createTransport export: ESM exports can't be reassigned, and
// transporters created before register() are covered too. The class is
// found through a throwaway JSON transport, so no internal file paths are
// assumed. Nodemailer 7+ ships ESM and CJS builds with separate classes;
// both are patched.

import { createHash } from 'node:crypto';
import fs from 'node:fs';
import { createRequire } from 'node:module';
import path from 'node:path';
import { pathToFileURL } from 'node:url';

import { guessPackage, maskHeader } from './capture.js';
import { send, sealed } from './client.js';
import { currentContext } from './run.js';
import { state, warnOnce } from './state.js';
import type { Attachment, Email } from './wire.js';

type SendMail = (this: Mailer, data: MailData, cb?: (err: Error | null, info?: unknown) => void) => unknown;
interface Mailer {
  sendMail: SendMail;
  _defaults?: MailData;
}
interface Nodemailer {
  createTransport(t: unknown): Mailer;
}
type Address = string | { name?: string; address?: string };
type MailData = {
  from?: Address;
  to?: Address | Address[];
  cc?: Address | Address[];
  bcc?: Address | Address[];
  replyTo?: Address | Address[];
  subject?: string;
  text?: unknown;
  html?: unknown;
  headers?: Record<string, unknown>;
  attachments?: MailAttachment[];
  envelope?: unknown;
};
/** The parts of a Nodemailer attachment the connector reads. */
type MailAttachment = {
  filename?: string | false;
  content?: unknown;
  path?: unknown;
  href?: unknown;
  contentType?: string;
  encoding?: string;
  cid?: string;
  contentDisposition?: string;
};

const PATCHED = Symbol.for('rhythm.echo.nodemailer');

/** installNodemailer patches every copy of Nodemailer the app can load; it's a no-op without one. */
export async function installNodemailer(from = process.cwd()): Promise<number> {
  const s = state();
  if (s.patched.has('nodemailer')) return 0;
  s.patched.add('nodemailer');
  let req: NodeJS.Require;
  let main: string;
  try {
    req = createRequire(path.join(from, 'noop.js'));
    main = req.resolve('nodemailer');
  } catch {
    return 0; // not installed
  }
  let n = 0;
  try {
    // synchronously, so `node -r` / `--import` callers are covered before the app starts
    if (patchCopy(req('nodemailer') as Nodemailer)) n++;
  } catch {
    // an ESM-only release: the import below finds it
  }
  const esm = esmEntry(main);
  if (esm) {
    try {
      const url = pathToFileURL(esm).href;
      // bundlers must leave this import to Node: it's the app's own copy, found at runtime
      const mod = (await import(/* webpackIgnore: true */ /* turbopackIgnore: true */ /* @vite-ignore */ url)) as {
        default?: Nodemailer;
      } & Partial<Nodemailer>;
      if (patchCopy((mod.createTransport ? mod : mod.default) as Nodemailer)) n++;
    } catch {
      // the CJS copy is still patched
    }
  }
  return n;
}

/** esmEntry finds the ESM build Node would load for `import 'nodemailer'`. */
function esmEntry(main: string): string | undefined {
  let dir = path.dirname(main);
  for (let i = 0; i < 6; i++) {
    const pj = path.join(dir, 'package.json');
    try {
      const p = JSON.parse(fs.readFileSync(pj, 'utf8')) as { name?: string; exports?: unknown };
      if (p.name === 'nodemailer') {
        const dot = (p.exports as Record<string, unknown> | undefined)?.['.'];
        const imp = typeof dot === 'object' && dot ? (dot as Record<string, unknown>).import : undefined;
        const file = typeof imp === 'string' ? imp : typeof imp === 'object' && imp ? (imp as Record<string, unknown>).default : undefined;
        return typeof file === 'string' ? path.join(dir, file) : undefined;
      }
    } catch {
      // keep walking up
    }
    dir = path.dirname(dir);
  }
  return undefined;
}

function patchCopy(nm: Nodemailer): boolean {
  let proto: (Mailer & { [PATCHED]?: true }) | undefined;
  try {
    proto = Object.getPrototypeOf(nm.createTransport({ jsonTransport: true })) as Mailer & { [PATCHED]?: true };
  } catch {
    return false;
  }
  if (!proto || typeof proto.sendMail !== 'function' || proto[PATCHED]) return false;
  const orig = proto.sendMail;
  proto[PATCHED] = true;
  proto.sendMail = function sendMail(this: Mailer, data: MailData, cb?: (err: Error | null, info?: unknown) => void) {
    if (!state().active) return orig.call(this, data, cb);
    const plugin = guessPackage();
    const p = deliver(this, orig, data ?? {}, plugin);
    if (typeof cb === 'function') {
      p.then((info) => cb(null, info), (err: Error) => cb(err));
      return undefined;
    }
    return p;
  };
  return true;
}

async function deliver(mailer: Mailer, orig: SendMail, data: MailData, plugin: string): Promise<unknown> {
  const mail: MailData = { ...(mailer._defaults ?? {}), ...data };
  const ctx = await currentContext();
  const email = toEmail(mail);
  // The connector sends no raw MIME, so Echo learns of files only from this.
  const files = await attachments(mail.attachments);
  if (files.length) email.attachments = files;
  const d = await send({
    kind: 'email',
    run: ctx.run,
    source: { via: 'node', stack: 'node', hook: 'nodemailer', plugin: plugin || undefined, host: state().cfg?.site[0], site: state().cfg?.siteId || undefined },
    email,
  });
  if (!d) {
    if (sealed()) throw Object.assign(new Error('blocked by Echo: Echo is unreachable and the site is sealed'), { code: 'EECHOBLOCKED' });
    return orig.call(mailer, data);
  }
  if (d.action === 'allow') return orig.call(mailer, data);
  warnOnceSealed();
  const rcpts = [...(email.to ?? []), ...(email.cc ?? []), ...(email.bcc ?? [])];
  return {
    messageId: `<${d.id || Date.now().toString(36)}@echo.invalid>`,
    accepted: rcpts.map(bare),
    rejected: [],
    pending: [],
    envelope: { from: email.from ? bare(email.from) : false, to: rcpts.map(bare) },
    response: '250 2.0.0 Captured by Echo, not sent',
  };
}

function warnOnceSealed(): void {
  // one reminder, so a developer wondering where the mail went knows
  warnOnce('mail-sealed', 'Nodemailer: messages are captured by Echo and not sent (sealed)');
}

export function toEmail(m: MailData): Email {
  const headers: Record<string, string> = {};
  for (const [k, v] of Object.entries(m.headers ?? {})) {
    if (typeof v === 'string') headers[k] = v;
    else if (Array.isArray(v)) headers[k] = v.map(String).join(', ');
    else if (v && typeof v === 'object' && 'value' in v) headers[k] = String((v as { value: unknown }).value);
    if (k in headers) headers[k] = maskHeader(k, headers[k]!);
  }
  const replyTo = list(m.replyTo);
  return {
    from: list(m.from)[0],
    to: list(m.to),
    cc: list(m.cc),
    bcc: list(m.bcc),
    reply_to: replyTo.length ? replyTo.join(', ') : undefined,
    subject: m.subject,
    text: content(m.text),
    html: content(m.html),
    headers: Object.keys(headers).length ? headers : undefined,
  };
}

// File types by extension, for an attachment without a contentType. The
// rest is application/octet-stream, as Nodemailer has it.
const types: Record<string, string> = {
  pdf: 'application/pdf', zip: 'application/zip', json: 'application/json', xml: 'application/xml',
  csv: 'text/csv', txt: 'text/plain', html: 'text/html', htm: 'text/html', ics: 'text/calendar',
  png: 'image/png', jpg: 'image/jpeg', jpeg: 'image/jpeg', gif: 'image/gif', webp: 'image/webp', svg: 'image/svg+xml',
  doc: 'application/msword', docx: 'application/vnd.openxmlformats-officedocument.wordprocessingml.document',
  xls: 'application/vnd.ms-excel', xlsx: 'application/vnd.openxmlformats-officedocument.spreadsheetml.sheet',
};

/**
 * attachments describes the files a mail carries: name, type, size and
 * SHA-256 of the decoded content. Inline images (with a cid) are part of
 * the HTML, not attachments. A stream or a remote URL can't be read
 * without taking it from Nodemailer, so those go with a name and type only.
 */
export async function attachments(list: MailAttachment[] | undefined): Promise<Attachment[]> {
  const out: Attachment[] = [];
  for (const a of Array.isArray(list) ? list : []) {
    if (!a || typeof a !== 'object' || a.cid || a.contentDisposition === 'inline') continue;
    const where = typeof a.path === 'string' ? a.path : typeof a.href === 'string' ? a.href : '';
    const local = where !== '' && !/^(https?|data):/i.test(where);
    const name = typeof a.filename === 'string' ? a.filename : a.filename === false || !local ? '' : path.basename(where);
    const ext = /\.([a-z0-9]+)$/i.exec(name || where)?.[1]?.toLowerCase() ?? '';
    const att: Attachment = { name: name || undefined, type: a.contentType ?? types[ext] ?? 'application/octet-stream' };
    let body: Buffer | undefined;
    try {
      if (typeof a.content === 'string') body = Buffer.from(a.content, (a.encoding ?? 'utf8') as BufferEncoding);
      else if (a.content instanceof Uint8Array) body = Buffer.from(a.content);
      else if (a.content === undefined && /^data:/i.test(where)) body = dataURI(where);
      else if (a.content === undefined && local) Object.assign(att, await hashFile(where));
    } catch {
      // unreadable here: Nodemailer reports it when it sends
    }
    if (body) {
      att.size = body.length;
      att.sha256 = createHash('sha256').update(body).digest('hex');
    }
    out.push(att);
  }
  return out;
}

/**
 * hashFile gives a local file's size and SHA-256 by streaming it, so a large
 * attachment is not held in memory twice (here and in Nodemailer).
 */
async function hashFile(file: string): Promise<{ size: number; sha256: string }> {
  const h = createHash('sha256');
  let size = 0;
  for await (const chunk of fs.createReadStream(file)) {
    size += (chunk as Buffer).length;
    h.update(chunk as Buffer);
  }
  return { size, sha256: h.digest('hex') };
}

/** dataURI decodes "data:<type>;base64,<data>" (or percent-encoded data). */
function dataURI(uri: string): Buffer {
  const comma = uri.indexOf(',');
  const meta = uri.slice(5, comma);
  const data = uri.slice(comma + 1);
  return /;base64$/i.test(meta) ? Buffer.from(data, 'base64') : Buffer.from(decodeURIComponent(data), 'utf8');
}

function content(v: unknown): string | undefined {
  if (typeof v === 'string') return v;
  if (v instanceof Uint8Array) return Buffer.from(v).toString('utf8');
  if (v && typeof v === 'object' && 'content' in v) return content((v as { content: unknown }).content);
  return undefined; // a stream or a file path: Nodemailer reads it, the capture goes without
}

function list(v: Address | Address[] | undefined): string[] {
  if (!v) return [];
  const items = Array.isArray(v) ? v : [v];
  const out: string[] = [];
  for (const a of items) {
    if (typeof a === 'string') out.push(...splitAddresses(a));
    else if (a?.address) out.push(a.name ? `${a.name} <${a.address}>` : a.address);
  }
  return out;
}

/** splitAddresses splits "a@x, \"Doe, J\" <j@y>" on the commas between addresses. */
function splitAddresses(s: string): string[] {
  const out: string[] = [];
  let cur = '';
  let quoted = false;
  let angle = 0;
  for (const ch of s) {
    if (ch === '"') quoted = !quoted;
    else if (!quoted && ch === '<') angle++;
    else if (!quoted && ch === '>') angle = Math.max(0, angle - 1);
    if (ch === ',' && !quoted && angle === 0) {
      if (cur.trim()) out.push(cur.trim());
      cur = '';
    } else cur += ch;
  }
  if (cur.trim()) out.push(cur.trim());
  return out;
}

function bare(a: string): string {
  const m = /<([^>]+)>/.exec(a);
  return (m ? m[1]! : a).trim();
}
