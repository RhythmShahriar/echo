// @rhythmsh/echo for Node: Express, Fastify, Nest, scripts and workers.
// Next.js apps use @rhythmsh/echo/next from instrumentation.ts instead.

import { activate, announce } from './activate.js';
import type { Options } from './config.js';
import { installFetch } from './fetch.js';
import { installNode } from './node.js';

export type { Options } from './config.js';
export { flush } from './client.js';
export { deactivate } from './activate.js';
export { currentRun, echoMiddleware, runFromHeaders, withEchoRun, type HeadersLike, type RequestLike } from './run.js';

/**
 * register installs the hooks (fetch, http, https, Nodemailer) unless the
 * production guard says no. fetch and http are hooked before it returns;
 * the promise settles once Nodemailer's ESM build is patched as well.
 */
export async function register(o?: Options): Promise<boolean> {
  if (!activate(o)) return false;
  installFetch();
  const mail = await installNode();
  const hooks = ['fetch', 'http', 'https'];
  if (mail) hooks.push('nodemailer');
  announce('node', hooks);
  return true;
}
