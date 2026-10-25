// Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
// SPDX-License-Identifier: MIT

// The newest message Echo caught for an address, with the one-time codes
// and links in it, from GET /v1/messages/latest. Shared by the Playwright
// and Cypress helpers; it has no Node imports so Cypress can bundle it.

/** What to look for. Every field is optional; Echo returns the newest match. */
export interface LatestQuery {
  /** An email address or phone number the message was sent to. */
  to?: string;
  /** One channel or several: email, sms, whatsapp… */
  channel?: string | string[];
  /** Only messages from this run and action (X-Echo-Run). */
  run?: string;
  /** See run. */
  action?: string;
  /** Only messages caught after this time. */
  since?: Date | string;
  /** Echo's search query, as the inbox takes it. */
  q?: string;
  /**
   * How long Echo waits for a match: a Go duration ('10s', '1500ms') or
   * milliseconds. Default 10s, at most 1m. 0 asks once without waiting.
   */
  wait?: string | number;
}

/** A link Echo found in a message, with what it looks like it is for. */
export interface Link {
  url: string;
  /** magic, verify, reset, login or unsubscribe. */
  kind: string;
}

/** Echo's answer, with the first code and link picked out. */
export interface Latest {
  /** The one-time codes found, unmasked, most likely first. */
  codes: string[];
  /** The links of a known kind, unmasked, in the order they appear. */
  links: Link[];
  /** The message as Echo stores it, credentials masked. */
  message: Record<string, unknown>;
  /** The first code, if any: what a login or checkout test types in. */
  code?: string;
  /** The first link's URL, if any. */
  link?: string;
}

/** Where Echo is and how to authenticate. Defaults come from the environment. */
export interface LatestOptions {
  /** Default: RHYTHM_ECHO_URL or http://127.0.0.1:8025. */
  url?: string;
  /** Sent as X-Echo-Token. Default: RHYTHM_ECHO_TOKEN. */
  token?: string;
}

/** DEFAULT_WAIT is how long Echo waits for a match when the query names no wait. */
export const DEFAULT_WAIT = '10s';

/** waitParam turns a wait into the Go duration Echo reads. */
export function waitParam(wait: string | number | undefined): string {
  if (wait === undefined) return DEFAULT_WAIT;
  if (typeof wait === 'number') return `${Math.max(0, Math.round(wait))}ms`;
  return wait;
}

/** Go duration units, in milliseconds. */
const units: Record<string, number> = { ns: 1e-6, us: 1e-3, 'µs': 1e-3, 'μs': 1e-3, ms: 1, s: 1000, m: 60000, h: 3600000 };

/**
 * waitMs is the same wait in milliseconds, for the client's own timeout.
 * It reads Go durations the way Echo does (compound ones such as "1m30s"
 * too) and throws on anything else: guessing a shorter wait would make
 * the client give up while Echo is still waiting.
 */
export function waitMs(wait: string | number | undefined): number {
  const w = waitParam(wait).trim();
  if (w === '0') return 0;
  const part = /(\d+(?:\.\d*)?|\.\d+)(ns|us|µs|μs|ms|s|m|h)/y;
  let total = 0;
  let at = 0;
  while (at < w.length) {
    part.lastIndex = at;
    const m = part.exec(w);
    if (!m) break;
    total += Number(m[1]) * (units[m[2] ?? ''] ?? 0);
    at = part.lastIndex;
  }
  if (w === '' || at < w.length) throw new TypeError(`wait "${w}" is not a duration Echo reads, such as 500ms, 10s or 1m30s`);
  return total;
}

/** params is the query string for a LatestQuery, as [name, value] pairs. */
export function params(q: LatestQuery): [string, string][] {
  const out: [string, string][] = [];
  if (q.to) out.push(['to', q.to]);
  if (q.channel) out.push(['channel', Array.isArray(q.channel) ? q.channel.join(',') : q.channel]);
  if (q.run) out.push(['run', q.run]);
  if (q.action) out.push(['action', q.action]);
  if (q.since) out.push(['since', q.since instanceof Date ? q.since.toISOString() : q.since]);
  if (q.q) out.push(['q', q.q]);
  out.push(['wait', waitParam(q.wait)]);
  return out;
}

/** describe names what was asked for, for error messages. */
export function describe(q: LatestQuery): string {
  const parts = [q.to ? `to ${q.to}` : '', q.channel ? `on ${[q.channel].flat().join(', ')}` : '', q.action ? `for ${q.action}` : ''];
  return parts.filter(Boolean).join(' ') || 'at all';
}

/**
 * parse checks Echo's answer: status 200 with {codes, links, message}.
 * Echo answers 404 when nothing matched in time; that and any other
 * status throw with Echo's own words.
 */
export function parse(status: number, body: unknown, q: LatestQuery): Latest {
  if (status !== 200) {
    const text = typeof body === 'string' ? body.trim() : JSON.stringify(body);
    if (status === 404) throw new Error(`[rhythm-echo] no message ${describe(q)}: ${text || 'none matched'}`);
    throw new Error(`[rhythm-echo] Echo answered ${status} for the latest message: ${text}`);
  }
  const b = (typeof body === 'string' ? JSON.parse(body) : body) as Partial<Latest> | null;
  if (!b || typeof b !== 'object' || !b.message) throw new Error('[rhythm-echo] Echo answered something that is not a latest message');
  const codes = Array.isArray(b.codes) ? b.codes : [];
  const links = Array.isArray(b.links) ? b.links : [];
  const out: Latest = { codes, links, message: b.message };
  if (codes[0] !== undefined) out.code = codes[0];
  if (links[0] !== undefined) out.link = links[0].url;
  return out;
}

type Env = Record<string, string | undefined>;

/** resolve fills Echo's URL and token from the options, else from env. */
export function resolve(o: LatestOptions, e: Env): { url: string; token: string } {
  return {
    url: (o.url ?? e.RHYTHM_ECHO_URL ?? 'http://127.0.0.1:8025').replace(/\/+$/, ''),
    token: o.token ?? e.RHYTHM_ECHO_TOKEN ?? '',
  };
}
