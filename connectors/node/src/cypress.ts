// Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
// SPDX-License-Identifier: MIT

// @rhythmsh/echo/cypress: cy.echoLatest(), the OTP or magic link a test
// just triggered. Import it once from the support file:
//
//   // cypress/support/e2e.ts
//   import '@rhythmsh/echo/cypress';
//
//   cy.echoLatest({ to: 'ann@example.test' }).its('code').then((code) => cy.get('#otp').type(code));
//
// It calls Echo with cy.request, which Cypress sends from Node, so Echo
// needs no CORS. Echo's URL and token come from Cypress.env:
// RHYTHM_ECHO_URL and RHYTHM_ECHO_TOKEN (CYPRESS_RHYTHM_ECHO_URL in the
// shell, or env in cypress.config).

import { params, parse, resolve, waitMs, type Latest, type LatestOptions, type LatestQuery } from './latest.js';
import { TOKEN_HEADER } from './wire.js';

export type { Latest, LatestOptions, LatestQuery, Link } from './latest.js';

// Merged into Cypress's own types when they are installed; the type
// parameter must match theirs.
declare global {
  namespace Cypress {
    interface Chainable<Subject = any> {
      /** The newest message Echo caught that matches q, with its codes and links. */
      echoLatest(q?: LatestQuery, o?: LatestOptions): Chainable<Latest>;
    }
  }
}

/** The part of Cypress this file uses, so it builds without Cypress installed. */
interface CypressLike {
  env(name: string): unknown;
  Commands: { add(name: string, fn: (...args: never[]) => unknown): void };
}
interface CyLike {
  request(o: {
    url: string;
    headers: Record<string, string>;
    failOnStatusCode: boolean;
    timeout: number;
    log?: boolean;
  }): { then(fn: (res: { status: number; body: unknown }) => unknown): unknown };
}

/**
 * registerEchoCommands adds cy.echoLatest. Importing this module calls it
 * when Cypress is loaded; call it yourself only if you import the module
 * before Cypress exists.
 */
export function registerEchoCommands(): boolean {
  const g = globalThis as { Cypress?: CypressLike; cy?: CyLike };
  const C = g.Cypress;
  if (!C) return false;
  C.Commands.add('echoLatest', (q: LatestQuery = {}, o: LatestOptions = {}) => {
    const e = { RHYTHM_ECHO_URL: str(C.env('RHYTHM_ECHO_URL')), RHYTHM_ECHO_TOKEN: str(C.env('RHYTHM_ECHO_TOKEN')) };
    const { url, token } = resolve(o, e);
    const headers: Record<string, string> = { accept: 'application/json' };
    if (token) headers[TOKEN_HEADER] = token;
    return g.cy!.request({
      url: `${url}/v1/messages/latest?${new URLSearchParams(params(q))}`,
      headers,
      failOnStatusCode: false,
      timeout: waitMs(q.wait) + 5000,
    }).then((res) => parse(res.status, res.body, q));
  });
  return true;
}

function str(v: unknown): string | undefined {
  return typeof v === 'string' && v !== '' ? v : undefined;
}

registerEchoCommands();
