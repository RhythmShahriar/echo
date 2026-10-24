// Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
// SPDX-License-Identifier: MIT

// Turning the connector on: configuration, the production guard and the
// one status line. Shared by the Node and Next.js entry points; safe on Edge.

import { guard, resolveConfig, type Options } from './config.js';
import { state } from './state.js';

/** activate applies options and the production guard; false means every hook passes calls straight through. */
export function activate(o?: Options): boolean {
  const s = state();
  const cfg = resolveConfig(o);
  const g = guard(cfg);
  // Another Echo may speak another wire version: find out again.
  if (s.cfg?.url !== cfg.url) delete s.wire;
  s.cfg = cfg;
  s.active = g.active;
  if (g.active) reason = g.reason;
  if (g.warning && !s.warned.has('guard')) {
    s.warned.add('guard');
    cfg.logger.warn(`[rhythm-echo] ${g.warning}`);
  }
  return g.active;
}

let reason = '';

/** announce logs one line per runtime, so it's obvious in the server log that mail and requests are being captured. */
export function announce(runtime: string, hooks: string[]): void {
  const s = state();
  const key = `announce-${runtime}`;
  if (!s.cfg || !s.active || s.warned.has(key)) return;
  s.warned.add(key);
  s.cfg.logger.info(`[rhythm-echo] active (${reason}, ${runtime}): capturing ${hooks.join(', ')} via ${s.cfg.url}`);
}

/** deactivate leaves the hooks installed but makes them pass everything through. */
export function deactivate(): void {
  state().active = false;
}
