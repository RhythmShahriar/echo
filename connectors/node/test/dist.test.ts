// The built package, loaded the ways an app loads it: `node --import`
// (ESM), `node -r` (CJS), and both at once (one shared set of hooks).
// Run `npm run build` first; `npm test` does.

import { execFile } from 'node:child_process';
import { existsSync } from 'node:fs';
import { join } from 'node:path';
import { promisify } from 'node:util';
import { afterAll, beforeAll, describe, expect, it } from 'vitest';

import { befores, fakeEcho, type FakeEcho } from './helpers.js';

const run = promisify(execFile);
const dist = join(import.meta.dirname, '..', 'dist');
let echo: FakeEcho;

beforeAll(async () => {
  echo = await fakeEcho('seal');
});
afterAll(() => echo.close());

const script = `
const r = await fetch('https://api.example.test/hook', { method: 'POST', body: 'x' });
const https = await import('node:https');
const s = await new Promise((ok, no) => https.get('https://api.example.test/get', (res) => ok(res.statusCode)).on('error', no));
console.log(JSON.stringify({ fetch: r.status, https: s, via: typeof require }));
`;

async function node(args: string[], extraEnv: Record<string, string> = {}) {
  const { stdout } = await run(process.execPath, [...args, '--input-type=module', '-e', script], {
    env: { ...process.env, RHYTHM_ECHO_URL: echo.url, RHYTHM_ECHO_RUN: 'dist/run', NODE_ENV: 'development', ...extraEnv },
  });
  return stdout;
}

describe.skipIf(!existsSync(join(dist, 'esm', 'register.js')))('built package', () => {
  it('node --import (ESM build)', async () => {
    echo.captures.length = 0;
    const out = await node(['--import', join(dist, 'esm', 'register.js')]);
    expect(out).toContain('[rhythm-echo] active');
    expect(out).toContain('{"fetch":200,"https":200');
    const got = await echo.waitFor(befores, 2);
    expect(got.map((c) => c.run)).toEqual(['dist/run', 'dist/run']);
  });

  it('node -r (CJS build)', async () => {
    echo.captures.length = 0;
    const out = await node(['-r', join(dist, 'cjs', 'register.js')]);
    expect(out).toContain('{"fetch":200,"https":200');
    await echo.waitFor(befores, 2);
  });

  it('both builds in one process patch once and share state', async () => {
    echo.captures.length = 0;
    const out = await node(['-r', join(dist, 'cjs', 'register.js'), '--import', join(dist, 'esm', 'register.js')]);
    expect(out.match(/\[rhythm-echo\] active/g)).toHaveLength(1);
    expect(out).toContain('{"fetch":200,"https":200');
    const got = await echo.waitFor(befores, 2);
    expect(got).toHaveLength(2); // not four: no double capture
  });

  it('stays inert in production', async () => {
    const { stdout } = await run(
      process.execPath,
      ['--import', join(dist, 'esm', 'register.js'), '-e', "console.log(Symbol.for('rhythm.echo.fetch') in fetch)"],
      { env: { ...process.env, RHYTHM_ECHO_URL: echo.url, VERCEL_ENV: 'production' } },
    );
    expect(stdout.trim()).toBe('false');
  });
});
