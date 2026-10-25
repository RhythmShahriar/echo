// Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
// SPDX-License-Identifier: MIT

// Configuration comes from the environment only, so the same build works
// in development, on a staging server and on a Vercel preview.

/** DEFAULT_URL is where `rhythm echo up` listens unless told otherwise. */
export const DEFAULT_URL = 'http://127.0.0.1:8025';

/** Options for register(). Every one is optional and falls back to the environment. */
export interface Options {
  /** Echo's capture API. Default: RHYTHM_ECHO_URL or http://127.0.0.1:8025. */
  url?: string;
  /**
   * Echo's token: what X-Echo-Run signatures are checked with, also sent
   * as X-Echo-Token unless writeToken is set. Default: RHYTHM_ECHO_TOKEN.
   */
  token?: string;
  /**
   * A token that may only send captures (a hub's per-site token). Sent as
   * X-Echo-Token instead of token; with no token it also checks run
   * signatures. Default: RHYTHM_ECHO_WRITE_TOKEN.
   */
  writeToken?: string;
  /**
   * This site's ID when one Echo serves many sites, sent as source.site.
   * Default: RHYTHM_ECHO_SITE_ID.
   */
  siteId?: string;
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

/** Config is Options resolved against the environment: what the hooks read. */
export interface Config {
  /** Echo's capture API, without a trailing slash. */
  url: string;
  /** Echo's host and port, so calls to Echo itself are never captured. */
  echoHost: string;
  /** What X-Echo-Run signatures are checked with: the token, else the write token. */
  token: string;
  /** What is sent as X-Echo-Token: the write token, else the token. */
  sendToken: string;
  /** source.site on every capture; empty when not set. */
  siteId: string;
  force: boolean;
  /** The site's own hosts, first the one reported as source.host. Empty when none is configured. */
  site: string[];
  captureLoopback: boolean;
  maxBody: number;
  timeout: number;
  logger: Pick<Console, 'info' | 'warn'>;
}

type Env = Record<string, string | undefined>;

/** env returns process.env, or an empty object where there is no process. */
export function env(): Env {
  // process is missing in browsers and some edge runtimes; Next.js's Edge
  // runtime has it.
  const p = (globalThis as { process?: { env?: Env } }).process;
  return p?.env ?? {};
}

const truthy = (v: string | undefined) => v !== undefined && /^(1|true|yes|on)$/i.test(v.trim());

/** resolveConfig fills every option from o, then the environment, then the defaults. */
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
  const token = o.token ?? e.RHYTHM_ECHO_TOKEN ?? '';
  const writeToken = o.writeToken ?? e.RHYTHM_ECHO_WRITE_TOKEN ?? '';
  return {
    url,
    echoHost,
    token: token || writeToken,
    sendToken: writeToken || token,
    siteId: (o.siteId ?? e.RHYTHM_ECHO_SITE_ID ?? '').trim(),
    force: o.force ?? truthy(e.RHYTHM_ECHO_FORCE),
    site,
    captureLoopback: o.captureLoopback ?? false,
    maxBody: o.maxBody ?? 256 << 10,
    timeout: o.timeout ?? 5000,
    logger: o.logger ?? console,
  };
}

/** Guard is the production guard's verdict. */
export interface Guard {
  /** Whether the hooks may capture here. */
  active: boolean;
  /** Why, for the one-line status. */
  reason: string;
  /** A one-line problem with the environment worth logging even when inactive. */
  warning?: string;
  /**
   * Off for certain (a Vercel production deploy, `next build`), so nothing
   * is logged even when RHYTHM_ECHO_* settings are present: env vars shared
   * by every Vercel environment would otherwise log on every cold start.
   */
  quiet?: boolean;
}

/** NODE_ENV values that mean "not production". Anything else counts as production. */
const nonProduction = new Set(['development', 'test', 'staging', 'preview', 'local']);

/**
 * guard decides whether the connector may run here. A connector that is
 * active and cannot reach Echo blocks every outbound request, so a
 * production deploy that still has the package installed must stay inert
 * and when in doubt the guard says production.
 *
 * - VERCEL_ENV=production: off. VERCEL_ENV is the only reliable signal on
 *   Vercel, because `next start` sets NODE_ENV=production on previews too.
 * - NODE_ENV=production outside Vercel (a self-hosted server): off. Only
 *   RHYTHM_ECHO_FORCE turns it on: RHYTHM_ECHO_URL alone is too easy to
 *   leave set in a production environment copied from staging.
 * - NODE_ENV unset outside Vercel: off unless RHYTHM_ECHO_URL is set. Many
 *   self-hosted services never set NODE_ENV, so unset is not development.
 * - NODE_ENV set to anything else that isn't a known non-production value
 *   ("prod", "dev"): off, with a warning, because a typo must not
 *   seal a production service.
 * - `next build` (NEXT_PHASE=phase-production-build): off, so static
 *   generation is never blocked.
 * - RHYTHM_ECHO_FORCE overrides all of these.
 */
export function guard(cfg: Pick<Config, 'force'>, e: Env = env()): Guard {
  if (cfg.force) return { active: true, reason: 'forced by RHYTHM_ECHO_FORCE' };
  if (e.NEXT_PHASE === 'phase-production-build') return { active: false, reason: 'next build', quiet: true };
  const vercel = (e.VERCEL_ENV ?? '').toLowerCase();
  if (vercel === 'production') return { active: false, reason: 'VERCEL_ENV=production', quiet: true };
  if (vercel !== '') return { active: true, reason: `VERCEL_ENV=${vercel}` };
  const raw = e.NODE_ENV;
  if (raw === undefined || raw.trim() === '') {
    if (e.RHYTHM_ECHO_URL) return { active: true, reason: 'NODE_ENV unset, RHYTHM_ECHO_URL set' };
    return { active: false, reason: 'NODE_ENV unset outside Vercel, so production (set RHYTHM_ECHO_URL or NODE_ENV=development)' };
  }
  const nodeEnv = raw.trim().toLowerCase();
  if (nodeEnv === 'production') {
    return { active: false, reason: 'NODE_ENV=production (set RHYTHM_ECHO_FORCE=1 only on a server that is not production)' };
  }
  if (!nonProduction.has(nodeEnv)) {
    return {
      active: false,
      reason: `NODE_ENV=${raw}, so production`,
      warning: `NODE_ENV=${raw} is not one of ${[...nonProduction].join(', ')} or production: treating it as production and staying off`,
    };
  }
  return { active: true, reason: `NODE_ENV=${nodeEnv}` };
}
