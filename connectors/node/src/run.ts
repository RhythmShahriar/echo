// Tying each capture to the action that caused it. Echo's browser and
// `echo run` send X-Echo-Run: <run>/<action> (header or cookie) with the
// request that triggers the work; the connector copies it into every capture
// made while that request is being handled.
//
// Lookup order: an explicit withEchoRun() scope, then a framework resolver
// (Next.js request headers), then RHYTHM_ECHO_RUN, which Echo's action
// runner sets on the scripts, cron jobs and workers it starts.

import { env } from './config.js';
import { als, state, type Context } from './state.js';
import { RUN_HEADER } from './wire.js';

/** Anything with request headers: a Fetch Headers, Node's IncomingHttpHeaders, a plain object. */
export type HeadersLike =
  | { get(name: string): string | null | undefined }
  | Record<string, string | string[] | undefined>;

/** Anything carrying headers: a Fetch Request, an Express or Node request. */
export interface RequestLike {
  headers: HeadersLike;
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

/** clean keeps a run tag printable and short; it ends up in Echo's UI and file names. */
function clean(v: string | undefined): string | undefined {
  if (!v) return undefined;
  let s = v.trim();
  try {
    if (s.includes('%')) s = decodeURIComponent(s);
  } catch {
    // keep it as sent
  }
  // eslint-disable-next-line no-control-regex
  s = s.replace(/[\u0000-\u001f\u007f]/g, '').slice(0, 200);
  return s || undefined;
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

/** runFromHeaders returns "<run>/<action>" from the X-Echo-Run header or cookie, if present. */
export function runFromHeaders(h: HeadersLike | null | undefined): string | undefined {
  if (!h) return undefined;
  return clean(header(h, RUN_HEADER)) ?? clean(cookie(h, RUN_HEADER));
}

/** contextFromHeaders adds the request's host, which counts as the site itself. */
export function contextFromHeaders(h: HeadersLike | null | undefined): Context {
  if (!h) return {};
  const host = (header(h, 'x-forwarded-host') ?? header(h, 'host'))?.split(',')[0]?.trim().toLowerCase();
  return { run: runFromHeaders(h), host: host || undefined };
}

/**
 * withEchoRun runs fn with the run taken from req (an Express, Node or
 * Fetch request) or given directly as "<run>/<action>". Every capture made
 * inside fn, including after awaits, carries that run.
 */
export function withEchoRun<T>(req: RequestLike | string | null | undefined, fn: () => T): T {
  const store = als();
  if (!store) return fn();
  const ctx: Context = typeof req === 'string' ? { run: clean(req) } : contextFromHeaders(req?.headers);
  return store.run(ctx, fn);
}

/** echoMiddleware is withEchoRun as Express/Connect middleware: app.use(echoMiddleware()). */
export function echoMiddleware(): (req: RequestLike, res: unknown, next: (err?: unknown) => void) => void {
  return (req, _res, next) => withEchoRun(req, () => next());
}

/** currentContext finds the run and site host for a capture being made now. */
export async function currentContext(): Promise<Context> {
  const ctx: Context = { ...als()?.getStore() };
  if (!ctx.run || !ctx.host) {
    for (const r of state().resolvers) {
      try {
        const found = await r();
        if (found) {
          ctx.run ??= found.run;
          ctx.host ??= found.host;
        }
      } catch {
        // a resolver outside its framework's request scope: nothing to add
      }
      if (ctx.run && ctx.host) break;
    }
  }
  ctx.run ??= clean(env().RHYTHM_ECHO_RUN);
  return ctx;
}

/** currentRun is the "<run>/<action>" a capture made now would carry. */
export async function currentRun(): Promise<string | undefined> {
  return (await currentContext()).run;
}
