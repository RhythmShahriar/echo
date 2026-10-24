// Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
// SPDX-License-Identifier: MIT

// @rhythmsh/echo/playwright: the OTP or magic link a test just triggered,
// in one call, from the Echo that caught the message.
//
//   import { echo } from '@rhythmsh/echo/playwright';
//   const { code } = await echo.latest({ to: 'ann@example.test', wait: '10s' });
//
// It works in any Node test runner; Playwright is not a dependency.

import { env } from './config.js';
import { TOKEN_HEADER } from './wire.js';
import { params, parse, resolve, waitMs, type Latest, type LatestOptions, type LatestQuery } from './latest.js';

export type { Latest, LatestOptions, LatestQuery, Link } from './latest.js';

/**
 * latest returns the newest message matching q, with its codes and links.
 * Echo waits up to q.wait (default 10s) for one to arrive; with none, it
 * throws, so a test fails where the message went missing.
 */
export async function latest(q: LatestQuery = {}, o: LatestOptions = {}): Promise<Latest> {
  const { url, token } = resolve(o, env());
  const headers: Record<string, string> = { accept: 'application/json' };
  if (token) headers[TOKEN_HEADER] = token;
  const res = await fetch(`${url}/v1/messages/latest?${new URLSearchParams(params(q))}`, {
    headers,
    // Echo holds the request open while it waits; allow that and a little more.
    signal: AbortSignal.timeout(waitMs(q.wait) + 5000),
  });
  return parse(res.status, await res.text(), q);
}

/** echo groups the helpers, for `echo.latest(...)`. */
export const echo = { latest };
