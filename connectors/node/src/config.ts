// Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
// SPDX-License-Identifier: MIT

// Configuration comes from the environment only, so the same build works
// in development, on a staging server and on a Vercel preview.

export const DEFAULT_URL = 'http://127.0.0.1:8025';

export interface Options {
  /** Echo's capture API. Default: RHYTHM_ECHO_URL or http://127.0.0.1:8025. */
  url?: string;
  /** Sent as X-Echo-Token. Default: RHYTHM_ECHO_TOKEN. */
  token?: string;
  /** Activate even where the production guard says no. Default: RHYTHM_ECHO_FORCE. */
  force?: boolean;
  /**
   * The site's own hosts or URLs. Calls to them are captured and Echo lets
   * them through as the site talking to itself. Default: RHYTHM_ECHO_SITE
   * (comma separated), VERCEL_URL, VERCEL_BRANCH_URL, NEXT_PUBLIC_SITE_URL.
   * Never taken from a request's Host header, which a visitor controls.
   */
  site?: string[];
  /**
   * Capture requests to loopback hosts too. Off by default because local
   * services (a database proxy, the dev server calling itself) are not
   * messages; tests turn it on to point SDKs at a local fake API.
   */
  captureLoopback?: boolean;
  /** Bytes of each body kept in a capture. Default 256 KiB, Echo's own limit. */
  maxBody?: number;
  /** Milliseconds to wait for Echo's decision. Default 5000. */
  timeout?: number;
  /** Where the one-line status goes. Default console.info / console.warn. */
  logger?: Pick<Console, 'info' | 'warn'>;
}

export interface Config {
  url: string;
  echoHost: string;
  token: string;
  force: boolean;
  /** The site's own hosts, first the one reported as source.host. Empty when none is configured. */
  site: string[];
  captureLoopback: boolean;
  maxBody: number;
  timeout: number;
  logger: Pick<Console, 'info' | 'warn'>;
}

type Env = Record<string, string | undefined>;

export function env(): Env {
  // process is missing in browsers and some edge runtimes; Next.js's Edge
  // runtime has it.
  const p = (globalThis as { process?: { env?: Env } }).process;
  return p?.env ?? {};
}

const truthy = (v: string | undefined) => v !== undefined && /^(1|true|yes|on)$/i.test(v.trim());

export function resolveConfig(o: Options = {}, e: Env = env()): Config {
  const url = (o.url ?? e.RHYTHM_ECHO_URL ?? DEFAULT_URL).replace(/\/+$/, '');
  let echoHost = '';
  try {
    echoHost = new URL(url).host.toLowerCase();
  } catch {
    // an unparseable URL leaves every capture unanswered, which fails closed
  }
  const site = [
    ...new Set(
      [...(o.site ?? []), ...(e.RHYTHM_ECHO_SITE ?? '').split(','), e.VERCEL_URL, e.VERCEL_BRANCH_URL, e.NEXT_PUBLIC_SITE_URL]
        .map((s) => (s ?? '').trim().toLowerCase().replace(/^https?:\/\//, '').replace(/\/.*$/, ''))
        .filter(Boolean),
    ),
  ];
  return {
    url,
    echoHost,
    token: o.token ?? e.RHYTHM_ECHO_TOKEN ?? '',
    force: o.force ?? truthy(e.RHYTHM_ECHO_FORCE),
    site,
    captureLoopback: o.captureLoopback ?? false,
    maxBody: o.maxBody ?? 256 << 10,
    timeout: o.timeout ?? 5000,
    logger: o.logger ?? console,
  };
}

export interface Guard {
  active: boolean;
  reason: string;
}

/**
 * guard decides whether the connector may run here. A connector that is
 * active and cannot reach Echo blocks every outbound request, so a
 * production deploy that still has the package installed must stay inert.
 *
 * - VERCEL_ENV=production: off. VERCEL_ENV is the only reliable signal on
 *   Vercel, because `next start` sets NODE_ENV=production on previews too.
 * - NODE_ENV=production outside Vercel (a self-hosted server): off unless
 *   RHYTHM_ECHO_URL is set, which is a deliberate choice to use Echo there.
 * - `next build` (NEXT_PHASE=phase-production-build): off, so static
 *   generation is never blocked.
 * - RHYTHM_ECHO_FORCE overrides all of these.
 */
export function guard(cfg: Pick<Config, 'force'>, e: Env = env()): Guard {
  if (cfg.force) return { active: true, reason: 'forced by RHYTHM_ECHO_FORCE' };
  if (e.NEXT_PHASE === 'phase-production-build') return { active: false, reason: 'next build' };
  const vercel = (e.VERCEL_ENV ?? '').toLowerCase();
  if (vercel === 'production') return { active: false, reason: 'VERCEL_ENV=production' };
  if (vercel !== '') return { active: true, reason: `VERCEL_ENV=${vercel}` };
  if ((e.NODE_ENV ?? '').toLowerCase() === 'production' && !e.RHYTHM_ECHO_URL) {
    return { active: false, reason: 'NODE_ENV=production without RHYTHM_ECHO_URL' };
  }
  return { active: true, reason: `NODE_ENV=${e.NODE_ENV ?? 'unset'}` };
}
