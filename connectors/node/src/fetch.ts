// Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
// SPDX-License-Identifier: MIT

// The fetch hook, for Node and the Edge runtime. It wraps globalThis.fetch
// once, at register(); Next.js patches fetch lazily on top of whatever
// globalThis.fetch is by then, so its cache sits above this wrapper and
// only real network requests reach it.

import { guessPackage, maskHeaders, siteHost, skipHost, utf8 } from './capture.js';
import { send, sendLater, sealed, errText } from './client.js';
import { currentContext } from './run.js';
import { state } from './state.js';
import type { Capture } from './wire.js';

const WRAPPED = Symbol.for('rhythm.echo.fetch');

/** blocked is the error a blocked request rejects with, the same type fetch uses for network errors. */
export function blocked(): TypeError {
  return new TypeError('blocked by Echo');
}

export function installFetch(): void {
  const s = state();
  const orig = globalThis.fetch as (typeof fetch & { [WRAPPED]?: true }) | undefined;
  if (!orig || orig[WRAPPED] || s.patched.has('fetch')) return;
  s.origFetch = orig;
  s.patched.add('fetch');
  const wrapped = Object.assign(
    function fetch(input: RequestInfo | URL, init?: RequestInit): Promise<Response> {
      // the stack is only complete before the first await
      const plugin = state().active ? guessPackage() : '';
      return echoFetch(orig, input, init, plugin);
    },
    { [WRAPPED]: true as const },
  );
  globalThis.fetch = wrapped as typeof fetch;
}

async function echoFetch(orig: typeof fetch, input: RequestInfo | URL, init: RequestInit | undefined, plugin: string): Promise<Response> {
  const s = state();
  const cfg = s.cfg;
  if (!s.active || !cfg) return orig(input, init);
  let url: URL;
  try {
    url = new URL(input instanceof Request ? input.url : String(input));
  } catch {
    return orig(input, init); // let fetch report the bad URL itself
  }
  if (url.protocol !== 'http:' && url.protocol !== 'https:') return orig(input, init);
  if (skipHost(url.hostname, url.port, cfg)) return orig(input, init);
  const ctx = await currentContext();

  const method = (init?.method ?? (input instanceof Request ? input.method : 'GET')).toUpperCase();
  const headers = new Headers(input instanceof Request ? input.headers : undefined);
  if (init?.headers) new Headers(init.headers).forEach((v, k) => headers.set(k, v));
  const body = await readRequestBody(input, init, cfg.maxBody, headers);
  init = body.init;

  const base: Omit<Capture, 'v' | 'phase'> = {
    kind: 'http',
    run: ctx.run,
    source: { via: 'node', stack: 'node', hook: 'fetch', plugin: plugin || undefined, host: siteHost(url.hostname, url.port, cfg) },
  };
  const d = await send({
    ...base,
    phase: 'before',
    http: { method, url: url.href, headers: maskHeaders(headers), body: body.text },
  });

  if (!d) {
    if (sealed()) throw blocked();
    return orig(input, init);
  }
  if (d.action === 'block') throw blocked();
  if (d.action === 'fake') return fakeResponse(d.response, url.href);

  let res: Response;
  try {
    res = await orig(input, init);
  } catch (err) {
    sendLater({ ...base, phase: 'after', id: d.id, http: { method, url: url.href, status: 0 }, error: errText(err) });
    throw err;
  }
  const copy = res.clone();
  sendLater(
    readCapped(copy.body, cfg.maxBody).then((text) => ({
      ...base,
      phase: 'after' as const,
      id: d.id,
      http: { method, url: url.href, status: res.status, response: text },
    })),
  );
  return res;
}

const nullBody = new Set([101, 103, 204, 205, 304]);

/** fakeResponse turns Echo's provider-shaped answer into what the site's code gets back. */
export function fakeResponse(r: { status: number; headers?: Record<string, string>; body?: string } | undefined, url: string): Response {
  let status = r?.status ?? 200;
  if (status < 200 || status > 599) status = 200;
  const headers = new Headers();
  for (const [k, v] of Object.entries(r?.headers ?? {})) {
    // the body is already decoded text; these would make clients mangle it
    if (!/^(content-length|content-encoding|transfer-encoding|connection)$/i.test(k)) headers.set(k, v);
  }
  const res = new Response(nullBody.has(status) ? null : (r?.body ?? ''), { status, headers });
  try {
    Object.defineProperty(res, 'url', { value: url });
  } catch {
    // some runtimes freeze Response; url stays ""
  }
  return res;
}

interface Body {
  text?: string;
  init?: RequestInit;
}

/**
 * readRequestBody reads up to max bytes of the body for the capture,
 * without spending the body the real request still needs. Strings, forms
 * and buffers can be read again; a stream is teed and the request gets the
 * other branch.
 */
async function readRequestBody(input: RequestInfo | URL, init: RequestInit | undefined, max: number, headers: Headers): Promise<Body> {
  let b = init?.body;
  if (b === undefined || b === null) {
    if (input instanceof Request && input.body) return { text: await readCapped(input.clone().body, max), init };
    return { init };
  }
  if (isAsyncIterable(b) && !(b instanceof ReadableStream)) b = fromIterable(b);
  if (b instanceof ReadableStream) {
    const [mine, theirs] = b.tee();
    const next = { ...init, body: theirs, duplex: 'half' } as RequestInit;
    return { text: await readCapped(mine, max), init: next };
  }
  // A Response serialises every other body type exactly as fetch would,
  // including the content type fetch would add (form boundaries).
  const r = new Response(b as BodyInit);
  const ct = r.headers.get('content-type');
  if (ct && !headers.has('content-type')) headers.set('content-type', ct);
  return { text: await readCapped(r.body, max), init };
}

export async function readCapped(stream: ReadableStream<Uint8Array> | null, max: number): Promise<string> {
  if (!stream) return '';
  const reader = stream.getReader();
  const chunks: Uint8Array[] = [];
  let total = 0;
  try {
    while (total < max) {
      const { done, value } = await reader.read();
      if (done) break;
      chunks.push(value);
      total += value.length;
    }
  } catch {
    // a body that errors is the request's problem, not the capture's
  } finally {
    // stop pulling: the other branch of a tee keeps flowing on its own
    reader.cancel().catch(() => undefined);
  }
  return utf8(chunks, Math.min(total, max));
}

function isAsyncIterable(v: unknown): v is AsyncIterable<Uint8Array | string> {
  return typeof v === 'object' && v !== null && Symbol.asyncIterator in v;
}

function fromIterable(it: AsyncIterable<Uint8Array | string>): ReadableStream<Uint8Array> {
  const iter = it[Symbol.asyncIterator]();
  const enc = new TextEncoder();
  return new ReadableStream<Uint8Array>({
    async pull(c) {
      const { done, value } = await iter.next();
      if (done) c.close();
      else c.enqueue(typeof value === 'string' ? enc.encode(value) : value);
    },
    async cancel() {
      await iter.return?.();
    },
  });
}
