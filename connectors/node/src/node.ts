// Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
// SPDX-License-Identifier: MIT

// Everything that needs Node built-ins: the http/https hook, Nodemailer and
// the Next.js request lookup on the Node runtime. Kept apart so the Edge
// bundle of `@rhythmsh/echo/next` never reaches it.

import { createRequire } from 'node:module';
import path from 'node:path';
import { AsyncLocalStorage } from 'node:async_hooks';

import { installHTTP } from './http.js';
import { installNodemailer } from './nodemailer.js';
import { contextFromHeaders, type HeadersLike } from './run.js';
import { state, type Context } from './state.js';

/** installNode adds the Node-only hooks; the returned promise settles once Nodemailer's ESM build is patched too. */
export function installNode(): Promise<number> {
  const s = state();
  s.als ??= new AsyncLocalStorage<Context>();
  installHTTP();
  return installNodemailer().catch(() => 0);
}

interface WorkUnitStore {
  type?: string;
  headers?: HeadersLike;
}

let workUnit: { getStore(): WorkUnitStore | undefined } | null | undefined;

/**
 * nextNodeContext reads the current request's headers from Next.js's own
 * request store instead of calling headers(): headers() during a static
 * prerender would turn the page dynamic, and reading the store has no such
 * side effect. Returns undefined outside a request, or null when this Next.js
 * version has no such store (the caller then falls back to headers()).
 */
export function nextNodeContext(): Context | undefined | null {
  if (workUnit === undefined) {
    try {
      const req = createRequire(path.join(process.cwd(), 'noop.js'));
      const mod = req('next/dist/server/app-render/work-unit-async-storage.external.js') as {
        workUnitAsyncStorage?: { getStore(): WorkUnitStore | undefined };
      };
      workUnit = mod.workUnitAsyncStorage ?? null;
    } catch {
      workUnit = null;
    }
  }
  if (!workUnit) return null;
  const store = workUnit.getStore();
  if (store?.type !== 'request' || !store.headers) return undefined;
  return contextFromHeaders(store.headers);
}
