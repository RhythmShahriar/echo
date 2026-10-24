// Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
// SPDX-License-Identifier: MIT

// End-to-end: the packed package in a real Next.js app, against Echo's real
// Go catcher, sealed. Every provider request goes to its real hostname, so
// next start runs with all other egress routed to a dead proxy: a request
// that slipped past the connector fails instead of leaving the machine.
//
//   npm run test:e2e                      (needs Go: /usr/local/go/bin or on PATH)
//   E2E_BUNDLER=webpack npm run test:e2e  (the webpack build instead of Turbopack)
//
// Ports: catcher 18925, Next.js 18926.

import { execFileSync, spawn } from 'node:child_process';
import { existsSync, mkdirSync, readFileSync, rmSync } from 'node:fs';
import { dirname, join } from 'node:path';
import { fileURLToPath } from 'node:url';
import assert from 'node:assert/strict';

const here = dirname(fileURLToPath(import.meta.url));
const pkgDir = join(here, '..');
const app = join(here, 'next-app');
const repo = join(pkgDir, '..', '..');
const ECHO = 'http://127.0.0.1:18925';
const SITE = 'http://127.0.0.1:18926';
const npm = process.platform === 'win32' ? 'npm.cmd' : 'npm';
const children = [];

const sh = (cmd, args, opts = {}) => execFileSync(cmd, args, { stdio: 'inherit', ...opts });
const sleep = (ms) => new Promise((r) => setTimeout(r, ms));

function start(name, cmd, args, opts) {
  const child = spawn(cmd, args, { stdio: ['ignore', 'pipe', 'pipe'], ...opts });
  child.log = '';
  const keep = (d) => {
    child.log += d;
    if (process.env.E2E_VERBOSE) process.stdout.write(`[${name}] ${d}`);
  };
  child.stdout.on('data', keep);
  child.stderr.on('data', keep);
  children.push(child);
  return child;
}

async function until(what, fn, ms = 60000) {
  const end = Date.now() + ms;
  let last;
  while (Date.now() < end) {
    try {
      const v = await fn();
      if (v) return v;
    } catch (e) {
      last = e;
    }
    await sleep(200);
  }
  throw new Error(`timed out waiting for ${what}${last ? `: ${last.message}` : ''}`);
}

function cleanup() {
  for (const c of children) if (c.exitCode === null) c.kill('SIGTERM');
  rmSync(join(app, '.echo-pack'), { recursive: true, force: true });
}
process.on('exit', cleanup);
process.on('SIGINT', () => process.exit(130));

async function main() {
  // 1. the package as it would be published
  const packDir = join(app, '.echo-pack');
  rmSync(packDir, { recursive: true, force: true });
  mkdirSync(packDir);
  const tgz = execFileSync(npm, ['pack', '--silent', '--pack-destination', packDir], { cwd: pkgDir, encoding: 'utf8' }).trim().split('\n').pop();
  if (!existsSync(join(app, 'node_modules', 'next'))) sh(npm, ['ci', '--no-audit', '--no-fund'], { cwd: app });
  sh(npm, ['install', '--no-save', '--no-audit', '--no-fund', join(packDir, tgz)], { cwd: app });

  // 2. Echo's real catcher, sealed
  // built first, then run directly: `go run` would leave the server behind when killed
  const goBin = existsSync('/usr/local/go/bin/go') ? '/usr/local/go/bin/go' : 'go';
  const catcherBin = join(packDir, process.platform === 'win32' ? 'catcher.exe' : 'catcher');
  sh(goBin, ['build', '-o', catcherBin, './connectors/node/fixtures/catcher'], { cwd: repo, env: { ...process.env, GOWORK: 'off' } });
  const catcher = start('catcher', catcherBin, ['-addr', '127.0.0.1:18925', '-mode', 'seal'], { cwd: repo });
  await until('the catcher', async () => (await fetch(`${ECHO}/v1/health`)).ok, 180000).catch((e) => {
    throw new Error(`${e.message}\n${catcher.log}`);
  });

  // 3. next build with RHYTHM_ECHO_URL set: the guard keeps the build untouched
  const env = { ...process.env, RHYTHM_ECHO_URL: ECHO, NEXT_TELEMETRY_DISABLED: '1' };
  delete env.NODE_ENV;
  delete env.VERCEL_ENV;
  // E2E_BUNDLER=webpack checks the webpack build; Turbopack is the default
  sh(join(app, 'node_modules', '.bin', 'next'), ['build', ...(process.env.E2E_BUNDLER === 'webpack' ? ['--webpack'] : [])], { cwd: app, env });

  // 4. next start, with every escaped request sent to a dead proxy. It runs
  // with NODE_ENV=production, which the guard only lets through when forced.
  const next = start('next', join(app, 'node_modules', '.bin', 'next'), ['start', '-p', '18926', '-H', '127.0.0.1'], {
    cwd: app,
    env: { ...env, RHYTHM_ECHO_FORCE: '1', NODE_USE_ENV_PROXY: '1', HTTP_PROXY: 'http://127.0.0.1:9', HTTPS_PROXY: 'http://127.0.0.1:9', NO_PROXY: '127.0.0.1,localhost' },
  });
  await until('next start', async () => (await fetch(`${SITE}/`)).ok).catch((e) => {
    throw new Error(`${e.message}\n${next.log}`);
  });
  await fetch(`${ECHO}/v1/messages`, { method: 'DELETE' });

  const results = {};
  // a Node route handler: Resend + Twilio fetches and a Nodemailer email
  const notify = await fetch(`${SITE}/api/notify`, { method: 'POST', headers: { 'X-Echo-Run': 'e2e/notify' } });
  results.notify = { status: notify.status, body: await notify.json().catch(() => null) };
  assert.equal(notify.status, 200, `notify: ${JSON.stringify(results.notify)}\n${next.log}`);
  assert.equal(results.notify.body.resend.status, 200);
  assert.equal(results.notify.body.twilio.status, 201);
  assert.match(results.notify.body.mail.response, /Captured by Echo/);

  // an Edge route
  const edge = await fetch(`${SITE}/api/edge`, { headers: { 'X-Echo-Run': 'e2e/edge' } });
  results.edge = { status: edge.status, body: await edge.json().catch(() => null) };
  assert.equal(edge.status, 200, `edge: ${JSON.stringify(results.edge)}\n${next.log}`);
  assert.equal(results.edge.body.runtime, 'edge');

  // a server action, run tag as a cookie this time
  const manifest = JSON.parse(readFileSync(join(app, '.next', 'server', 'server-reference-manifest.json'), 'utf8'));
  const actionId = Object.entries(manifest.node).find(([, v]) => v.exportedName === 'notifyTeam')?.[0] ?? Object.keys(manifest.node)[0];
  const action = await fetch(`${SITE}/action`, {
    method: 'POST',
    headers: { 'Next-Action': actionId, 'Content-Type': 'text/plain;charset=UTF-8', Origin: SITE, Cookie: 'X-Echo-Run=e2e%2Faction', Accept: 'text/x-component' },
    body: '[]',
  });
  results.action = { status: action.status };
  assert.equal(action.status, 200, `action: ${await action.text()}\n${next.log}`);

  // 5. what Echo caught
  const msgs = await until('captures', async () => {
    const all = await (await fetch(`${ECHO}/v1/messages`)).json();
    return all.length >= 5 ? all : null;
  }, 15000);
  const by = (action, pred = () => true) => msgs.filter((m) => m.run === 'e2e' && m.action === action && pred(m));
  const one = (label, list) => {
    assert.equal(list.length, 1, `${label}: ${JSON.stringify(msgs, null, 1)}`);
    return list[0];
  };
  const resend = one('resend', by('notify', (m) => m.url?.startsWith('https://api.resend.com/')));
  const twilio = one('twilio', by('notify', (m) => m.url?.includes('api.twilio.com')));
  const mail = one('nodemailer', by('notify', (m) => m.channel === 'email' && m.source?.hook === 'nodemailer'));
  const slack = one('edge slack', by('edge', (m) => m.url?.includes('hooks.slack.com')));
  const hook = one('server action', by('action', (m) => m.url?.includes('zapier')));
  for (const m of [resend, twilio, mail, slack, hook]) {
    assert.equal(m.status, 'faked', `${m.url ?? m.subject}: ${m.status}`);
    assert.equal(m.source.via, 'node');
  }
  assert.equal(twilio.provider, 'twilio');
  assert.equal(twilio.channel, 'sms');
  assert.equal(slack.provider, 'slack');
  assert.equal(mail.subject, 'Your receipt');
  assert.deepEqual(mail.to, ['ann@example.test']);
  assert.ok(!JSON.stringify(msgs).includes('fixturefixture'), 'a credential reached Echo unmasked');
  assert.match(next.log, /\[rhythm-echo\] active .*nodejs\): capturing fetch, http, https, nodemailer/);
  assert.match(next.log, /\[rhythm-echo\] active .*edge\): capturing fetch/);
  assert.doesNotMatch(next.log, /ECONNREFUSED 127\.0\.0\.1:9/, 'a request escaped the connector');

  console.log('\ne2e ok:');
  for (const m of msgs) console.log(`  ${m.run}/${m.action}  ${m.status.padEnd(6)} ${m.channel.padEnd(8)} ${m.provider ?? ''} ${m.url ?? m.subject}`);
}

main().then(
  () => {
    cleanup();
    process.exit(0);
  },
  (err) => {
    console.error(err);
    cleanup();
    process.exit(1);
  },
);
