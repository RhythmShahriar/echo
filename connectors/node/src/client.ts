// Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
// SPDX-License-Identifier: MIT

// Talking to Echo. Every capture goes through send(), which never throws:
// null means "no decision" and each hook turns that into fail-closed or
// fail-open depending on the last mode Echo reported.

import { state, warnOnce } from './state.js';
import { downgrade, TOKEN_HEADER, WIRE_MIN_VERSION, WIRE_VERSION, type Capture, type Decision } from './wire.js';

/**
 * WIRE_RECHECK_MS is how long an older wire version is kept before the
 * newest is tried again. A long-running server outlives an Echo upgrade
 * and would otherwise keep dropping what the older wire lacks. WordPress
 * re-checks hourly too.
 */
export const WIRE_RECHECK_MS = 60 * 60 * 1000;

export async function send(c: Omit<Capture, 'v'>): Promise<Decision | null> {
  const s = state();
  const cfg = s.cfg;
  const f = s.origFetch ?? globalThis.fetch;
  if (!cfg || !f) return null;
  const headers: Record<string, string> = { 'content-type': 'application/json' };
  if (cfg.sendToken) headers[TOKEN_HEADER] = cfg.sendToken;
  const post = (v: number) =>
    f(`${cfg.url}/v1/capture`, {
      method: 'POST',
      headers,
      body: JSON.stringify({ v, ...(v < WIRE_VERSION ? downgrade(c) : c) }),
      signal: AbortSignal.timeout(cfg.timeout),
      // keep Next.js's fetch cache out of it if a patched fetch is underneath
      cache: 'no-store',
    });
  if (s.wire !== undefined && Date.now() - (s.wireAt ?? 0) >= WIRE_RECHECK_MS) {
    delete s.wire;
    delete s.wireAt;
  }
  try {
    let res = await post(s.wire ?? WIRE_VERSION);
    if (!res.ok) {
      let text = (await res.text().catch(() => '')).trim().slice(0, 200);
      // An older Echo refuses a newer wire version; ask which it speaks and
      // send that from now on.
      if (res.status === 400 && /newer than Echo/.test(text) && s.wire === undefined) {
        const older = await echoWire(f, headers, cfg.url, cfg.timeout);
        if (older !== null && older >= WIRE_MIN_VERSION && older < WIRE_VERSION) {
          s.wire = older;
          s.wireAt = Date.now();
          warnOnce('wire', `Echo at ${cfg.url} speaks wire ${older}: sending that (rhythm update gets a newer Echo)`);
          res = await post(older);
          text = res.ok ? '' : (await res.text().catch(() => '')).trim().slice(0, 200);
        }
      }
      if (!res.ok) {
        warnOnce(`status-${res.status}`, `Echo at ${cfg.url} answered ${res.status}${text ? `: ${text}` : ''}`);
        return null;
      }
    }
    const d = (await res.json()) as Decision;
    if (d.mode) s.mode = d.mode;
    return d;
  } catch (err) {
    warnOnce(
      'unreachable',
      `Echo is unreachable at ${cfg.url} (${errText(err)}); ` +
        (s.mode === 'observe' ? 'letting messages through (observe mode)' : 'blocking outbound requests and mail (sealed)'),
    );
    return null;
  }
}

/** echoWire is the wire version Echo's /v1/health names, or null. */
async function echoWire(f: typeof fetch, headers: Record<string, string>, url: string, timeout: number): Promise<number | null> {
  try {
    const res = await f(`${url}/v1/health`, { headers, signal: AbortSignal.timeout(timeout), cache: 'no-store' });
    if (!res.ok) return null;
    const h = (await res.json()) as { wire?: unknown };
    return typeof h.wire === 'number' ? h.wire : null;
  } catch {
    return null;
  }
}

/** sealed reports whether a missing decision must block. */
export function sealed(): boolean {
  return state().mode !== 'observe';
}

export function errText(err: unknown): string {
  if (err instanceof Error) {
    const cause = (err as { cause?: unknown }).cause;
    return cause instanceof Error ? `${err.message}: ${cause.message}` : err.message;
  }
  return String(err);
}

/** Fire-and-forget for "after" captures: the app's response must not wait for Echo. */
export function sendLater(c: Omit<Capture, 'v'> | Promise<Omit<Capture, 'v'>>): void {
  const inflight = state().inflight;
  const p = Promise.resolve(c)
    .then(send)
    .catch(() => null)
    .finally(() => inflight.delete(p));
  inflight.add(p);
}

/**
 * flush waits for "after" captures still being sent. Serverless handlers
 * and tests call it before they finish; a long-running server doesn't need to.
 */
export async function flush(): Promise<void> {
  const inflight = state().inflight;
  while (inflight.size > 0) await Promise.all([...inflight]);
}
