// Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
// SPDX-License-Identifier: MIT

// Tying each capture to the action that caused it. Echo's browser and
// `echo run` send X-Echo-Run: <run>/<action> (header or cookie) with the
// request that triggers the work; the connector copies it into every capture
// made while that request is being handled.
//
// Lookup order: an explicit withEchoRun() scope, then a framework resolver
// (Next.js request headers), then RHYTHM_ECHO_RUN, which Echo's action
// runner sets on the scripts, cron jobs and workers it starts.
//
// A visitor can send X-Echo-Run too and add messages to a baseline or drop
// them from one. So a value from a request counts only when it is signed
// with the token (wire.SignRun in Echo) or, with no token configured, when
// it came straight from this machine: a loopback peer and no forwarding
// headers. Anything else is ignored, with one warning per reason.

import { env } from './config.js';
import { als, state, warnOnce, type Context, type RunClaim } from './state.js';
import { RUN_HEADER } from './wire.js';

/** Anything with request headers: a Fetch Headers, Node's IncomingHttpHeaders, a plain object. */
export type HeadersLike =
  | { get(name: string): string | null | undefined }
  | Record<string, string | string[] | undefined>;

/** Anything carrying headers: a Fetch Request, an Express or Node request. */
export interface RequestLike {
  headers: HeadersLike;
  /** Node's socket (Express, Fastify, Nest): its remoteAddress tells a call from this machine. */
  socket?: { remoteAddress?: string | undefined } | null;
}

function header(h: HeadersLike, name: string): string | undefined {
  if (typeof (h as { get?: unknown }).get === 'function') {
    return (h as { get(name: string): string | null | undefined }).get(name) ?? undefined;
  }
  const rec = h as Record<string, string | string[] | undefined>;
  const lower = name.toLowerCase();
  for (const k of Object.keys(rec)) {
    if (k.toLowerCase() !== lower) continue;
    const v = rec[k];
    return Array.isArray(v) ? v.join(name === 'cookie' ? '; ' : ', ') : v;
  }
  return undefined;
}

/**
 * MAX_RUN is the longest run value (X-Echo-Run, its cookie or
 * RHYTHM_ECHO_RUN) a connector takes, in bytes, signature included. Every
 * connector uses the same limit and refuses a longer value whole: cutting
 * it would cut the signature too.
 */
export const MAX_RUN = 512;

/** bytes is v's length in UTF-8, as Echo and the PHP connectors count it. */
function bytes(v: string): number {
  return new TextEncoder().encode(v).length;
}

/** clean keeps a run tag printable; it ends up in Echo's UI and file names. A value over MAX_RUN is refused. */
function clean(v: string | undefined): string | undefined {
  if (!v) return undefined;
  let s = v.trim();
  try {
    if (s.includes('%')) s = decodeURIComponent(s);
  } catch {
    // keep it as sent
  }
  // eslint-disable-next-line no-control-regex
  s = s.replace(/[\u0000-\u001f\u007f]/g, '');
  return s && bytes(s) <= MAX_RUN ? s : undefined;
}

function cookie(h: HeadersLike, name: string): string | undefined {
  const raw = header(h, 'cookie');
  if (!raw) return undefined;
  for (const part of raw.split(';')) {
    const i = part.indexOf('=');
    if (i > 0 && part.slice(0, i).trim() === name) return part.slice(i + 1);
  }
  return undefined;
}

/**
 * runFromHeaders returns the X-Echo-Run header or cookie as sent, if
 * present. It is not checked: pass the request to withEchoRun() instead of
 * this value, so a visitor's value is ignored.
 */
export function runFromHeaders(h: HeadersLike | null | undefined): string | undefined {
  if (!h) return undefined;
  return clean(header(h, RUN_HEADER)) ?? clean(cookie(h, RUN_HEADER));
}

const forwarding = ['x-forwarded-for', 'forwarded', 'x-real-ip'];

/**
 * contextFromHeaders is what a request tells the connector: only the run,
 * checked later by currentContext. The site's own host never comes from
 * Host or X-Forwarded-Host, because a visitor who set either to
 * api.sendgrid.com would unseal SendGrid.
 */
export function contextFromHeaders(h: HeadersLike | null | undefined, remote?: string): Context {
  const value = runFromHeaders(h);
  if (!h || value === undefined) return {};
  return { claim: { value, remote, forwarded: forwarding.some((n) => header(h, n) !== undefined) } };
}

/**
 * withEchoRun runs fn with the run taken from req (an Express, Node or
 * Fetch request) or given directly as "<run>/<action>". Every capture made
 * inside fn, including after awaits, carries that run. A string is the
 * app's own word and is taken as given; a request's value is checked.
 */
export function withEchoRun<T>(req: RequestLike | string | null | undefined, fn: () => T): T {
  const store = als();
  if (!store) return fn();
  const ctx: Context =
    typeof req === 'string' ? { run: trusted(req) } : contextFromHeaders(req?.headers, req?.socket?.remoteAddress ?? undefined);
  return store.run(ctx, fn);
}

/** echoMiddleware is withEchoRun as Express/Connect middleware: app.use(echoMiddleware()). */
export function echoMiddleware(): (req: RequestLike, res: unknown, next: (err?: unknown) => void) => void {
  return (req, _res, next) => withEchoRun(req, () => next());
}

/** currentContext finds the run for a capture being made now. */
export async function currentContext(): Promise<Context> {
  const scope = als()?.getStore();
  if (scope?.claim) {
    // checked once per request: the scope object is shared by its captures
    scope.run = await accept(scope.claim);
    delete scope.claim;
  }
  const ctx: Context = { run: scope?.run };
  if (!ctx.run) {
    for (const r of state().resolvers) {
      try {
        const c = await r();
        ctx.run ??= c?.claim ? await accept(c.claim) : c?.run;
      } catch {
        // a resolver outside its framework's request scope: nothing to add
      }
      if (ctx.run) break;
    }
  }
  ctx.run ??= trusted(env().RHYTHM_ECHO_RUN);
  return ctx;
}

/** currentRun is the "<run>/<action>" a capture made now would carry. */
export async function currentRun(): Promise<string | undefined> {
  return (await currentContext()).run;
}

/** A run signature: 16 bytes in unpadded base64url, 22 characters. */
const sigShape = /^[A-Za-z0-9_-]{22}$/;

/**
 * verifyRun ports Echo's wire.VerifyRun: the run and action of a value and
 * whether it carries a valid signature for token. An unsigned value keeps
 * its run and action with ok false; a signature that does not verify
 * gives nothing.
 */
export async function verifyRun(token: string, value: string): Promise<{ run: string; action: string; ok: boolean }> {
  const none = { run: '', action: '', ok: false };
  let v = value;
  let sig = '';
  const dot = value.lastIndexOf('.');
  if (dot >= 0 && sigShape.test(value.slice(dot + 1))) {
    v = value.slice(0, dot);
    sig = value.slice(dot + 1);
  }
  const slash = v.indexOf('/');
  if (slash <= 0) return none;
  const run = v.slice(0, slash);
  const action = v.slice(slash + 1);
  if (sig === '') return { run, action, ok: false };
  if (token === '') return none;
  const want = await runSig(token, v);
  return want !== undefined && equal(sig, want) ? { run, action, ok: true } : none;
}

/** webCrypto is the runtime's crypto.subtle, if it has one. */
function webCrypto(): SubtleCrypto | undefined {
  return (globalThis as { crypto?: Crypto }).crypto?.subtle;
}

/** canVerify reports whether this runtime can check a signature: node:crypto or Web Crypto. */
function canVerify(): boolean {
  return state().hmac !== undefined || webCrypto() !== undefined;
}

async function runSig(token: string, v: string): Promise<string | undefined> {
  const s = state();
  let mac: Uint8Array;
  if (s.hmac) {
    mac = s.hmac(token, v);
  } else {
    const subtle = webCrypto();
    if (!subtle) return undefined;
    const enc = new TextEncoder();
    const key = await subtle.importKey('raw', enc.encode(token), { name: 'HMAC', hash: 'SHA-256' }, false, ['sign']);
    mac = new Uint8Array(await subtle.sign('HMAC', key, enc.encode(v)));
  }
  let bin = '';
  for (const b of mac.subarray(0, 16)) bin += String.fromCharCode(b);
  return btoa(bin).replace(/\+/g, '-').replace(/\//g, '_').replace(/=+$/, '');
}

/** equal compares in constant time for strings of the same length. */
function equal(a: string, b: string): boolean {
  if (a.length !== b.length) return false;
  let d = 0;
  for (let i = 0; i < a.length; i++) d |= a.charCodeAt(i) ^ b.charCodeAt(i);
  return d === 0;
}

/**
 * trusted takes a run the app or Echo's runner gave (withEchoRun's string,
 * RHYTHM_ECHO_RUN), which a visitor cannot set. A signature, if Echo added
 * one, is dropped from the run captures carry.
 */
function trusted(v: string | undefined): string | undefined {
  const c = clean(v);
  if (c === undefined) return undefined;
  const dot = c.lastIndexOf('.');
  return dot >= 0 && sigShape.test(c.slice(dot + 1)) ? c.slice(0, dot) : c;
}

function isLoopbackAddr(a: string | undefined): boolean {
  if (!a) return false;
  const h = a.toLowerCase().replace(/^::ffff:/, '');
  return h === '::1' || /^127\.\d+\.\d+\.\d+$/.test(h);
}

/**
 * accept decides whether a request's X-Echo-Run counts and returns the
 * "<run>/<action>" captures carry (the signature dropped), or undefined.
 */
export async function accept(c: RunClaim): Promise<string | undefined> {
  const token = state().cfg?.token ?? '';
  if (bytes(c.value) > MAX_RUN) {
    warnOnce('run-long', `ignored an X-Echo-Run longer than ${MAX_RUN} bytes`);
    return undefined;
  }
  const dot = c.value.lastIndexOf('.');
  if (token && dot >= 0 && sigShape.test(c.value.slice(dot + 1)) && !canVerify()) {
    warnOnce('run-crypto', 'ignored a signed X-Echo-Run: this runtime has neither node:crypto nor Web Crypto (crypto.subtle) to check its signature');
    return undefined;
  }
  const { run, action, ok } = await verifyRun(token, c.value);
  if (run === '') {
    warnOnce('run-bad', `ignored an X-Echo-Run whose signature does not verify (${token ? 'not signed with this token' : 'RHYTHM_ECHO_TOKEN is not set here'})`);
    return undefined;
  }
  if (ok) return `${run}/${action}`;
  if (token) {
    warnOnce('run-unsigned', 'ignored an unsigned X-Echo-Run: with RHYTHM_ECHO_TOKEN set, only runs Echo signed with it count');
    return undefined;
  }
  if (!c.forwarded && isLoopbackAddr(c.remote)) return `${run}/${action}`;
  warnOnce(
    'run-remote',
    `ignored an X-Echo-Run from ${c.remote ? c.remote : 'a request with no peer address (as in Next.js and other Fetch-request apps)'}${c.forwarded ? ' behind a proxy' : ''}: without RHYTHM_ECHO_TOKEN only a direct call from this machine may set the run (set RHYTHM_ECHO_TOKEN here and the same token in Echo)`,
  );
  return undefined;
}
