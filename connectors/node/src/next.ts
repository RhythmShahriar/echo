// @rhythm/echo/next, for instrumentation.ts:
//
//   export async function register() {
//     const { register } = await import('@rhythm/echo/next');
//     await register();
//   }
//
// It runs in both the Node and the Edge runtime. Everything Node-only sits
// behind `process.env.NEXT_RUNTIME === 'nodejs'`, which Next.js replaces at
// build time, so the Edge bundle drops it.

import { activate, announce } from './activate.js';
import type { Options } from './config.js';
import { installFetch } from './fetch.js';
import { contextFromHeaders } from './run.js';
import { state, type Context } from './state.js';

export type { Options } from './config.js';
export { flush } from './client.js';
export { deactivate } from './activate.js';
export { currentRun, runFromHeaders, withEchoRun, type HeadersLike, type RequestLike } from './run.js';

let nodeContext: (() => Context | undefined | null) | undefined;

export async function register(o?: Options): Promise<boolean> {
  if (!activate(o)) return false;
  installFetch();
  const s = state();
  if (!s.resolvers.includes(nextContext)) s.resolvers.push(nextContext);
  const hooks = ['fetch'];
  if (process.env.NEXT_RUNTIME === 'nodejs') {
    const node = await import('./node.js');
    nodeContext = node.nextNodeContext;
    const mail = await node.installNode();
    hooks.push('http', 'https');
    if (mail) hooks.push('nodemailer');
  }
  announce(process.env.NEXT_RUNTIME === 'edge' ? 'edge' : 'nodejs', hooks);
  return true;
}

/**
 * nextContext finds X-Echo-Run on the request being handled: route
 * handlers, server actions, middleware-free pages. On Node it reads Next's
 * request store; elsewhere, or on a Next.js without that store, it calls
 * headers(), which throws outside a request (caught by the caller).
 */
async function nextContext(): Promise<Context | undefined> {
  if (nodeContext) {
    const c = nodeContext();
    if (c !== null) return c;
  }
  const { headers } = await import('next/headers');
  return contextFromHeaders(await headers());
}

type NextConfig = { serverExternalPackages?: string[] } & Record<string, unknown>;
type NextConfigInput = NextConfig | ((...args: unknown[]) => NextConfig | Promise<NextConfig>);

/**
 * withEcho wraps next.config so Nodemailer stays an external package.
 * Next.js bundles server dependencies by default, and a bundled copy of
 * Nodemailer is out of the connector's reach; an external one is the copy
 * in node_modules, which register() patches. It changes nothing else, and is
 * harmless in production.
 *
 *   // next.config.mjs
 *   import { withEcho } from '@rhythm/echo/next';
 *   export default withEcho({ ... });
 */
export function withEcho<T extends NextConfigInput>(config: T): T {
  const add = (c: NextConfig): NextConfig => {
    const ext = new Set(c.serverExternalPackages ?? []);
    ext.add('nodemailer');
    return { ...c, serverExternalPackages: [...ext] };
  };
  if (typeof config === 'function') {
    return (async (...args: unknown[]) => add(await config(...args))) as T;
  }
  return add(config as NextConfig) as T;
}
