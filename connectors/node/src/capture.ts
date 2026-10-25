// Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
// SPDX-License-Identifier: MIT

// Shared pieces of the fetch and http hooks: what to leave alone, masking
// and a guess at which package made the call.

import type { Config } from './config.js';

/**
 * Hosts that are never messages: Echo itself and loopback. The site's own
 * hosts are not skipped: those calls are captured with source.host set.
 * Echo lets them through and records them as sent.
 */
export function skipHost(hostname: string, port: string, cfg: Config): boolean {
  const h = bare(hostname);
  const hostPort = port ? `${h}:${port}` : h;
  if (cfg.echoHost && (hostPort === cfg.echoHost || h === cfg.echoHost)) return true;
  return !cfg.captureLoopback && isLoopback(h);
}

/**
 * siteHost is the source.host to report for a call to hostname: the
 * configured site host it matches, or else the first configured one. A
 * site host without a port matches that host on any port.
 */
export function siteHost(hostname: string, port: string, cfg: Pick<Config, 'site'>): string | undefined {
  const h = bare(hostname);
  const hostPort = port ? `${h}:${port}` : h;
  return cfg.site.find((s) => s === hostPort || s === h) ?? cfg.site[0];
}

function bare(hostname: string): string {
  return hostname.toLowerCase().replace(/^\[|\]$/g, '');
}

/** isLoopback reports whether a host name stays on this machine ("0.0.0.0" reaches loopback too). */
export function isLoopback(h: string): boolean {
  return h === 'localhost' || h.endsWith('.localhost') || h === '::1' || /^127\.\d+\.\d+\.\d+$/.test(h) || h === '0.0.0.0';
}

/**
 * Header names whose whole value is a credential. The same rule as Echo's
 * catcher, so a header the connector lets through is not stored raw there
 * either.
 */
const secretHeader = /token|secret|key|auth|signature|session|password|cookie|credential/i;

/**
 * A Stripe key's mode prefix. It carries no secret and is what Echo reads
 * to decide whether a payment call may be faked (test) or must be refused
 * (live), so it stays.
 */
const keyMode = /^[spr]k_(?:live|test)_/;

/**
 * mask hides a credential whole. Only a Stripe key's mode prefix stays
 * ("sk_test_•••••"); an empty value stays empty.
 */
export function mask(v: string): string {
  if (v === '') return '';
  return (keyMode.exec(v)?.[0] ?? '') + '•••••';
}

/**
 * maskHeader returns a header's value as a capture may carry it. An
 * Authorization header keeps its scheme. A Basic one is decoded so
 * the user name (a Stripe key there) can keep its mode prefix.
 */
export function maskHeader(name: string, v: string): string {
  if (v === '' || !secretHeader.test(name)) return v;
  const m = /^([A-Za-z][A-Za-z0-9-]*)\s+(\S+)$/.exec(v.trim());
  if (!m || !/^(proxy-)?authorization$/i.test(name)) return mask(v);
  const scheme = m[1]!;
  const cred = m[2]!;
  if (scheme.toLowerCase() === 'basic') {
    const plain = base64Text(cred);
    const colon = plain?.indexOf(':') ?? -1;
    return `${scheme} ${mask(plain !== null && colon >= 0 ? plain.slice(0, colon) : cred)}`;
  }
  return `${scheme} ${mask(cred)}`;
}

/**
 * base64Text decodes a Basic credential, or gives null when it is not
 * base64. It uses atob and TextDecoder rather than Buffer, which the Edge
 * runtime does not have; this file runs there under the fetch hook.
 */
function base64Text(v: string): string | null {
  try {
    const bin = atob(v);
    const bytes = new Uint8Array(bin.length);
    for (let i = 0; i < bin.length; i++) bytes[i] = bin.charCodeAt(i);
    return new TextDecoder('utf-8', { fatal: false }).decode(bytes);
  } catch {
    return null;
  }
}

/** maskHeaders lowercases names and hides credentials before they leave the process. */
export function maskHeaders(h: Iterable<[string, string]>): Record<string, string> {
  const out: Record<string, string> = {};
  for (const [k0, v] of h) {
    const k = k0.toLowerCase();
    out[k] = maskHeader(k, v);
  }
  return out;
}

// Packages that only carry a request for another package; the sender is
// whatever called them.
const carriers = new Set([
  'next', 'undici', 'axios', 'follow-redirects', 'node-fetch', 'cross-fetch', 'got', 'ky',
  'superagent', 'needle', 'phin', 'gaxios', 'agent-base', 'https-proxy-agent', 'http-proxy-agent', 'react-dom',
  'react-server-dom-webpack', 'react-server-dom-turbopack', 'nodemailer',
]);

/**
 * guessPackage names the npm package that made the call, from the stack:
 * resend, @sendgrid/client, twilio. Best effort; minified bundles have no
 * package paths and give "".
 */
export function guessPackage(): string {
  const limit = Error.stackTraceLimit;
  Error.stackTraceLimit = 40;
  const stack = new Error().stack ?? '';
  Error.stackTraceLimit = limit;
  const lines = stack.split('\n').slice(1);
  // the first frame is this file: whatever package it sits in is ours
  const self = pkgIn(lines[0] ?? '');
  for (const line of lines.slice(1)) {
    const p = pkgIn(line);
    if (p && p !== self && !carriers.has(p)) return p;
  }
  return '';
}

/** pkgIn returns the innermost node_modules package named in a stack line. */
function pkgIn(line: string): string {
  const re = /node_modules[\\/]((?:@[^\\/]+[\\/])?[^\\/]+)/g;
  let m: RegExpExecArray | null;
  let last = '';
  while ((m = re.exec(line)) !== null) last = m[1]!.replace(/\\/g, '/');
  return last;
}

/** utf8 decodes bytes for a capture; invalid sequences become U+FFFD rather than failing. */
export function utf8(chunks: Uint8Array[], total: number): string {
  const all = new Uint8Array(total);
  let o = 0;
  for (const c of chunks) {
    all.set(c.subarray(0, total - o), o);
    o += Math.min(c.length, total - o);
    if (o >= total) break;
  }
  return new TextDecoder('utf-8', { fatal: false }).decode(all.subarray(0, o));
}
