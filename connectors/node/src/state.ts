// Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
// SPDX-License-Identifier: MIT

// Process-wide state. It lives on globalThis under a registered symbol, not
// in module scope, because one process can load this package twice: the ESM
// build through `node --import @rhythmsh/echo/register` and the CJS build
// through the app's `require('@rhythmsh/echo')`, or a bundled copy next to an
// external one. Both copies must patch once and share one run context.

import type { Config } from './config.js';

/** Context is what a capture made now should carry. */
export interface Context {
  /** "<run>/<action>" from X-Echo-Run, once accepted. */
  run?: string;
  /** The X-Echo-Run a request sent, not yet checked (see run.ts accept). */
  claim?: RunClaim;
}

/** An X-Echo-Run value as a request sent it, with what is known about where it came from. */
export interface RunClaim {
  value: string;
  /** The peer's address, when the request object has a socket. */
  remote?: string;
  /** Whether X-Forwarded-For, Forwarded or X-Real-IP was sent. */
  forwarded: boolean;
}

/** Resolver finds the Context of the request being handled, for frameworks whose request isn't in the AsyncLocalStorage. */
export type Resolver = () => Context | undefined | Promise<Context | undefined>;

/** ALS is the part of AsyncLocalStorage the connector uses. */
export interface ALS<T> {
  getStore(): T | undefined;
  run<R>(store: T, fn: () => R): R;
}

/** State is the process-wide state, shared by every copy of the package. */
export interface State {
  /** The resolved configuration, once activate() ran. */
  cfg?: Config;
  /** Whether the hooks capture; false passes every call straight through. */
  active: boolean;
  /** Echo's mode from its last answer; seal until Echo says otherwise, so an unreachable Echo fails closed. */
  mode: string;
  /** The wire version Echo speaks, once it refused a newer one; unset means the newest. */
  wire?: number;
  /** When wire was learned (Date.now()), so an upgraded Echo gets the newest version again. */
  wireAt?: number;
  /** fetch as it was before the hook, which Echo itself is reached with. */
  origFetch?: typeof fetch;
  /** Hooks already installed, so a second copy of the package doesn't patch again. */
  patched: Set<string>;
  als?: ALS<Context>;
  /** Framework hooks that find the current request (Next.js), tried after the AsyncLocalStorage. */
  resolvers: Resolver[];
  /** Problems already logged (see warnOnce). */
  warned: Set<string>;
  /** "after" captures still on their way, for flush(). */
  inflight: Set<Promise<unknown>>;
  /** HMAC-SHA256, set on Node from node:crypto; elsewhere Web Crypto is used. */
  hmac?: (key: string, msg: string) => Uint8Array;
}

const KEY = Symbol.for('rhythm.echo.state');

/** state returns the process-wide state, creating it sealed and inactive. */
export function state(): State {
  const g = globalThis as unknown as Record<symbol, State | undefined>;
  let s = g[KEY];
  if (!s) {
    s = { active: false, mode: 'seal', patched: new Set(), resolvers: [], warned: new Set(), inflight: new Set() };
    g[KEY] = s;
  }
  return s;
}

/** warnOnce logs a problem the first time it happens, so a broken Echo doesn't flood the app's logs. */
export function warnOnce(key: string, msg: string): void {
  const s = state();
  if (s.warned.has(key)) return;
  s.warned.add(key);
  (s.cfg?.logger ?? console).warn(`[rhythm-echo] ${msg}`);
}

type ALSCtor = new <T>() => ALS<T>;

/**
 * als returns the shared AsyncLocalStorage: Next.js's Edge runtime puts the
 * class on globalThis, Node has it in node:async_hooks. Reached through
 * process.getBuiltinModule so this file stays importable from Edge bundles.
 */
export function als(): ALS<Context> | undefined {
  const s = state();
  if (s.als) return s.als;
  let Ctor = (globalThis as { AsyncLocalStorage?: ALSCtor }).AsyncLocalStorage;
  if (!Ctor) {
    const p = (globalThis as { process?: { getBuiltinModule?: (id: string) => unknown } }).process;
    try {
      Ctor = (p?.getBuiltinModule?.('node:async_hooks') as { AsyncLocalStorage?: ALSCtor } | undefined)?.AsyncLocalStorage;
    } catch {
      Ctor = undefined;
    }
  }
  if (Ctor) s.als = new Ctor<Context>();
  return s.als;
}
