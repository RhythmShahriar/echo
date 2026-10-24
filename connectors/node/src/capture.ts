// Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
// SPDX-License-Identifier: MIT

// Shared pieces of the fetch and http hooks: what to leave alone, masking,
// and a guess at which package made the call.

import type { Config } from './config.js';
import type { Context } from './state.js';

/** Hosts that are never messages: Echo itself, the site, loopback. */
export function skipHost(hostname: string, port: string, cfg: Config, ctx: Context): boolean {
  const h = hostname.toLowerCase().replace(/^\[|\]$/g, '');
  const hostPort = port ? `${h}:${port}` : h;
  if (cfg.echoHost && (hostPort === cfg.echoHost || h === cfg.echoHost)) return true;
  // the request's own host; a Host without a port matches that host on any port
  if (ctx.host && (ctx.host === hostPort || (!ctx.host.includes(':') && ctx.host === h))) return true;
  if (cfg.site.some((s) => s === h || s === hostPort)) return true;
  return !cfg.captureLoopback && isLoopback(h);
}

export function isLoopback(h: string): boolean {
  return h === 'localhost' || h.endsWith('.localhost') || h === '::1' || /^127\.\d+\.\d+\.\d+$/.test(h) || h === '0.0.0.0';
}

const secretHeader = /^(authorization|proxy-authorization|cookie|x-api-key|api-key|x-auth-token|x-postmark-server-token|x-postmark-account-token|x-echo-token)$/;
const secretish = /(secret|token|password|passwd|api[-_]?key)/i;

/** mask keeps the first few characters of a credential, like Echo's kit/secrets Mask. */
export function mask(v: string, keep = 0): string {
  const k = Math.min(keep, Math.floor(v.length / 3));
  return v.slice(0, k) + '•••••';
}

/** maskHeaders lowercases names and hides credentials before they leave the process. */
export function maskHeaders(h: Iterable<[string, string]>): Record<string, string> {
  const out: Record<string, string> = {};
  for (const [k0, v] of h) {
    const k = k0.toLowerCase();
    if (k === 'authorization' || k === 'proxy-authorization') {
      const sp = v.indexOf(' ');
      out[k] = sp > 0 ? `${v.slice(0, sp)} ${mask(v.slice(sp + 1))}` : mask(v);
    } else if (secretHeader.test(k) || secretish.test(k)) {
      out[k] = mask(v);
    } else {
      out[k] = v;
    }
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
