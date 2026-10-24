// Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
// SPDX-License-Identifier: MIT

// The http and https hook, for SDKs that skip fetch: axios (Twilio,
// SendGrid, Postmark), got, older clients. Node only.
//
// http.request is synchronous and the caller writes the body afterwards, so
// the decision can't be made up front. The hook lets Node build a real
// ClientRequest, but hands it an agent that holds the request back: the
// body is buffered as usual (Node keeps it until a socket arrives), and once
// the caller has ended the request the capture goes to Echo. Then:
//   allow  the real agent gets the request, exactly as if nothing happened
//   fake   the request gets an in-memory socket that answers with Echo's
//          response, parsed by Node's own HTTP parser
//   block  the request fails as if the connection had been refused
// Requests that never reach an agent (a custom createConnection, a Unix
// socket, CONNECT) pass through untouched.

import http from 'node:http';
import https from 'node:https';
import { syncBuiltinESMExports } from 'node:module';
import { Duplex } from 'node:stream';
import zlib from 'node:zlib';

import { guessPackage, maskHeaders, skipHost } from './capture.js';
import { errText, send, sealed, sendLater } from './client.js';
import { currentContext } from './run.js';
import { als, state } from './state.js';
import type { Capture, Decision } from './wire.js';

type Mod = typeof http | typeof https;
type Agent = http.Agent & { addRequest(req: http.ClientRequest, opts: unknown): void };
type Opts = http.RequestOptions & { _defaultAgent?: Agent };
type AnyFn = (...args: unknown[]) => unknown;

const REAL = Symbol('rhythm.echo.realAgent');

export function installHTTP(): void {
  const s = state();
  if (s.patched.has('http')) return;
  s.patched.add('http');
  patch(http, 'http:');
  patch(https, 'https:');
  try {
    syncBuiltinESMExports(); // `import { request } from 'node:http'` sees the hook too
  } catch {
    // older Node: the default export is patched, named imports are not
  }
}

function patch(mod: Mod, protocol: 'http:' | 'https:'): void {
  const orig = mod.request as unknown as AnyFn;
  const hook = protocol === 'https:' ? 'https.request' : 'http.request';
  const request = function request(...args: unknown[]): http.ClientRequest {
    const plan = state().active ? planFor(args, mod, protocol) : null;
    if (!plan) return orig.apply(mod, args) as http.ClientRequest;
    const plugin = guessPackage();
    const req = orig.apply(mod, plan.args) as http.ClientRequest;
    hold(req, plan, protocol, hook, plugin);
    return req;
  };
  const get = function get(...args: unknown[]): http.ClientRequest {
    const req = request(...args);
    req.end();
    return req;
  };
  (mod as { request: unknown }).request = request;
  (mod as { get: unknown }).get = get;
}

interface Plan {
  args: unknown[];
  real: Agent;
  wrapper: Agent;
  addRequest: Promise<unknown>; // resolves with the options Node passed to addRequest
}

/** planFor swaps in a holding agent, or returns null to leave the call alone. */
function planFor(args: unknown[], mod: Mod, protocol: string): Plan | null {
  const cfg = state().cfg;
  if (!cfg) return null;
  const [a0, a1] = args;
  let url: URL | undefined;
  let opts: Opts;
  let build: (o: Opts) => unknown[];
  if (typeof a0 === 'string' || a0 instanceof URL) {
    try {
      url = new URL(String(a0));
    } catch {
      return null;
    }
    if (a1 && typeof a1 === 'object') {
      opts = a1 as Opts;
      build = (o) => [a0, o, ...args.slice(2)];
    } else {
      opts = {};
      build = (o) => [a0, o, ...args.slice(1)];
    }
  } else if (a0 && typeof a0 === 'object') {
    opts = a0 as Opts;
    build = (o) => [o, ...args.slice(1)];
  } else {
    return null;
  }
  if (opts.socketPath || (opts.method ?? '').toUpperCase() === 'CONNECT') return null;
  if (opts.agent === undefined && typeof opts.createConnection === 'function') return null;
  const proto = opts.protocol ?? url?.protocol ?? protocol;
  if (proto !== protocol) return null; // Node will throw ERR_INVALID_PROTOCOL itself
  const hostname = opts.hostname ?? (opts.host ? opts.host.replace(/:\d+$/, '') : undefined) ?? url?.hostname ?? 'localhost';
  const port = String(opts.port ?? url?.port ?? '');
  if (skipHost(hostname, port, cfg, als()?.getStore() ?? {})) return null;

  let real = opts.agent as (Agent & { [REAL]?: Agent }) | false | undefined;
  if (real === false) {
    const def = (opts._defaultAgent ?? mod.globalAgent) as Agent;
    real = new (def.constructor as new () => Agent)();
  } else if (real === undefined) {
    real = (opts._defaultAgent ?? mod.globalAgent) as Agent;
  }
  if (real[REAL]) real = real[REAL]; // a redirect re-using options we built
  if (typeof real.addRequest !== 'function') return null;

  let gotAdd!: (o: unknown) => void;
  const addRequest = new Promise<unknown>((r) => (gotAdd = r));
  const wrapper = Object.create(real) as Agent & { [REAL]: Agent };
  wrapper[REAL] = real;
  wrapper.addRequest = (_req, o) => gotAdd(o);
  return { args: build({ ...opts, agent: wrapper }), real, wrapper, addRequest };
}

/** hold buffers the body, asks Echo once the caller has ended the request, then lets it go, fakes or blocks it. */
function hold(req: http.ClientRequest, plan: Plan, protocol: string, hook: string, plugin: string): void {
  const cfg = state().cfg!;
  (req as unknown as { agent: unknown }).agent = plan.real;
  const chunks: Buffer[] = [];
  let size = 0;
  let open!: () => void;
  const gate = new Promise<void>((r) => (open = r));
  let gated = true;
  const record = (chunk: unknown, enc: unknown) => {
    if (chunk === undefined || chunk === null || typeof chunk === 'function') return;
    const b = Buffer.isBuffer(chunk)
      ? chunk
      : typeof chunk === 'string'
        ? Buffer.from(chunk, typeof enc === 'string' ? (enc as BufferEncoding) : 'utf8')
        : Buffer.from(chunk as Uint8Array);
    if (size < cfg.maxBody) chunks.push(b.subarray(0, cfg.maxBody - size));
    size += b.length;
    if (size >= cfg.maxBody) open();
  };
  const write = req.write as AnyFn;
  const end = req.end as AnyFn;
  const flushHeaders = req.flushHeaders as AnyFn;
  req.write = function (this: http.ClientRequest, chunk: unknown, enc?: unknown, cb?: unknown) {
    record(chunk, enc);
    const ok = write.call(this, chunk, enc, cb) as boolean;
    // Node won't drain a request that has no socket yet; a piped body
    // would wait forever for an end() that only comes after the drain.
    if (!ok && gated) process.nextTick(() => this.emit('drain'));
    return ok;
  } as typeof req.write;
  req.end = function (this: http.ClientRequest, chunk?: unknown, enc?: unknown, cb?: unknown) {
    record(chunk, enc);
    const r = end.call(this, chunk, enc, cb);
    open();
    return r;
  } as typeof req.end;
  req.flushHeaders = function (this: http.ClientRequest) {
    flushHeaders.call(this);
    open();
  };

  void (async () => {
    const opts = await plan.addRequest;
    await gate;
    gated = false;
    let d: Decision | null = null;
    let base: Omit<Capture, 'v' | 'phase'> | undefined;
    let summary: { method: string; url: string } | undefined;
    try {
      const ctx = await currentContext();
      const hostHeader = String(req.getHeader('host') ?? req.host);
      const target = new URL(`${protocol}//${hostHeader}`);
      if (skipHost(target.hostname, target.port, cfg, ctx)) return plan.real.addRequest(req, opts);
      summary = { method: req.method, url: `${protocol}//${hostHeader}${req.path}` };
      base = {
        kind: 'http',
        run: ctx.run,
        source: { via: 'node', stack: 'node', hook, plugin: plugin || undefined, host: ctx.host },
      };
      d = await send({
        ...base,
        phase: 'before',
        http: { ...summary, headers: maskHeaders(headerEntries(req)), body: Buffer.concat(chunks).toString('utf8') },
      });
    } catch {
      d = null;
    }
    if (req.destroyed) return req.onSocket(new FakeSocket(protocol) as never);
    const upgrade = req.getHeader('upgrade') !== undefined;
    if (!d) {
      if (sealed()) return fail(req, protocol);
      return plan.real.addRequest(req, opts);
    }
    if (d.action === 'block' || (d.action === 'fake' && upgrade)) return fail(req, protocol);
    if (d.action === 'fake') {
      const sock = new FakeSocket(protocol);
      req.onSocket(sock as never);
      setImmediate(() => sock.respond(rawResponse(d.response, req.method)));
      return;
    }
    if (base && summary) observe(req, base, summary, d.id);
    plan.real.addRequest(req, opts);
  })();
}

function headerEntries(req: http.ClientRequest): [string, string][] {
  const out: [string, string][] = [];
  for (const [k, v] of Object.entries(req.getHeaders())) {
    if (v === undefined) continue;
    out.push([k, Array.isArray(v) ? v.join(', ') : String(v)]);
  }
  return out;
}

/** fail ends the request the way a refused connection would, so the SDK takes its error path. */
function fail(req: http.ClientRequest, protocol: string): void {
  const err = Object.assign(new Error('blocked by Echo'), { code: 'EECHOBLOCKED' });
  (req.onSocket as AnyFn).call(req, new FakeSocket(protocol), err);
}

/** observe sends the "after" capture when an allowed request's response has been read. */
function observe(req: http.ClientRequest, base: Omit<Capture, 'v' | 'phase'>, summary: { method: string; url: string }, id: string): void {
  const max = state().cfg!.maxBody;
  let done = false;
  const after = (status: number, response: string, error?: string) => {
    if (done) return;
    done = true;
    sendLater({ ...base, phase: 'after', id, http: { ...summary, status, response }, error });
  };
  // Watch 'error' without listening for it: an added listener would
  // swallow errors the app never handled.
  const emit = req.emit as AnyFn;
  req.emit = function (this: http.ClientRequest, ev: string, ...a: unknown[]) {
    if (ev === 'error') after(0, '', errText(a[0]));
    return emit.call(this, ev, ...a) as boolean;
  } as typeof req.emit;
  req.prependOnceListener('response', (res: http.IncomingMessage) => {
    // Node dumps a response nobody listens for; this listener would stop that.
    if (req.listenerCount('response') === 0) res.resume();
    const chunks: Buffer[] = [];
    let size = 0;
    const push = res.push.bind(res) as AnyFn;
    res.push = (chunk: unknown, enc?: BufferEncoding) => {
      if (chunk !== null && chunk !== undefined && size < max) {
        const b = Buffer.isBuffer(chunk) ? chunk : Buffer.from(chunk as string, enc);
        chunks.push(b.subarray(0, max - size));
        size += b.length;
      }
      return push(chunk, enc) as boolean;
    };
    const finish = () => after(res.statusCode ?? 0, decode(Buffer.concat(chunks), res.headers['content-encoding'], size > max));
    res.once('end', finish);
    res.once('close', finish);
  });
}

function decode(b: Buffer, encoding: string | undefined, truncated: boolean): string {
  try {
    if (!truncated) {
      if (encoding === 'gzip' || encoding === 'x-gzip') b = zlib.gunzipSync(b);
      else if (encoding === 'deflate') b = zlib.inflateSync(b);
      else if (encoding === 'br') b = zlib.brotliDecompressSync(b);
    } else if (encoding && encoding !== 'identity') {
      return `[${encoding} response over the capture limit]`;
    }
  } catch {
    return `[${encoding} response]`;
  }
  return b.toString('utf8');
}

const STATUS = http.STATUS_CODES;

/** rawResponse writes Echo's answer as HTTP/1.1 bytes for Node's parser. */
function rawResponse(r: Decision['response'], method: string): Buffer {
  let status = r?.status ?? 200;
  if (status < 200 || status > 599) status = 200;
  const noBody = method === 'HEAD' || status === 204 || status === 304;
  const body = noBody ? Buffer.alloc(0) : Buffer.from(r?.body ?? '', 'utf8');
  const line = (s: string) => s.replace(/[\r\n]+/g, ' ');
  let head = `HTTP/1.1 ${status} ${line(STATUS[status] ?? 'OK')}\r\n`;
  for (const [k, v] of Object.entries(r?.headers ?? {})) {
    if (/^(content-length|content-encoding|transfer-encoding|connection|keep-alive)$/i.test(k)) continue;
    head += `${line(k)}: ${line(v)}\r\n`;
  }
  if (!noBody) head += `content-length: ${body.length}\r\n`;
  head += 'connection: close\r\n\r\n';
  return Buffer.concat([Buffer.from(head, 'latin1'), body]);
}

/**
 * FakeSocket stands in for the connection of a faked or blocked request:
 * it swallows what the request writes and plays back Echo's response.
 */
class FakeSocket extends Duplex {
  connecting = false;
  remoteAddress = '127.0.0.1';
  remotePort = 0;
  remoteFamily = 'IPv4';
  bytesWritten = 0;
  encrypted: boolean;
  authorized = true;

  constructor(protocol: string) {
    super();
    this.encrypted = protocol === 'https:';
  }
  override _read(): void {}
  override _write(chunk: Buffer, _enc: BufferEncoding, cb: (err?: Error | null) => void): void {
    this.bytesWritten += chunk.length;
    cb();
  }
  respond(raw: Buffer): void {
    this.push(raw);
    this.push(null);
  }
  setTimeout(_ms: number, cb?: () => void): this {
    if (cb) this.once('timeout', cb);
    return this;
  }
  setNoDelay(): this {
    return this;
  }
  setKeepAlive(): this {
    return this;
  }
  ref(): this {
    return this;
  }
  unref(): this {
    return this;
  }
  destroySoon(): void {
    this.destroy();
  }
  address(): { address: string; family: string; port: number } {
    return { address: '127.0.0.1', family: 'IPv4', port: 0 };
  }
  getPeerCertificate(): Record<string, never> {
    return {};
  }
}
