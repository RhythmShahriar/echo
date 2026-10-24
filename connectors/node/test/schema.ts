// Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
// SPDX-License-Identifier: MIT

// Echo's wire schema (wire/schema.json) and a validator for the subset of
// JSON Schema it uses, a port of the Go one in conformance/schema.go. The
// fake Echo checks every capture the connector sends with it, so the whole
// suite fails on a capture Echo's own schema would refuse.

import { existsSync, readFileSync } from 'node:fs';
import { join } from 'node:path';

type Node = Record<string, unknown>;

/** The repository's conformance folder and schema, when the package sits inside it. */
export const repo = join(import.meta.dirname, '..', '..', '..');
const schemaFile = join(repo, 'wire', 'schema.json');

let defs: Record<string, Node> | null | undefined;

function load(): Record<string, Node> | null {
  if (defs === undefined) defs = existsSync(schemaFile) ? (JSON.parse(readFileSync(schemaFile, 'utf8')) as { $defs: Record<string, Node> }).$defs : null;
  return defs;
}

/** hasSchema reports whether wire/schema.json is there to check against. */
export const hasSchema = () => load() !== null;

/** captureProblems returns what is wrong with a capture, as "path: problem" lines. */
export function captureProblems(doc: unknown): string[] {
  return problems('Capture', doc);
}

/** decisionProblems returns what is wrong with Echo's answer. */
export function decisionProblems(doc: unknown): string[] {
  return problems('Decision', doc);
}

function problems(def: string, doc: unknown): string[] {
  const d = load();
  if (!d) return [];
  const v = (doc as { v?: unknown } | null)?.v;
  const version = typeof v === 'number' ? v : 2;
  const out: string[] = [];
  const fail = (path: string, msg: string): void => {
    out.push(`${path}: ${msg}`);
  };

  const kind = (x: unknown) =>
    x === null ? 'null' : Array.isArray(x) ? 'an array' : typeof x === 'object' ? 'an object' : typeof x === 'number' ? 'a number' : `a ${typeof x}`;

  const value = (path: string, x: unknown, n: Node): void => {
    if (typeof n.$ref === 'string') n = d[n.$ref.replace('#/$defs/', '')] ?? {};
    switch (n.type) {
      case 'object': {
        if (x === null || typeof x !== 'object' || Array.isArray(x)) return fail(path, `want an object, got ${kind(x)}`);
        const m = x as Record<string, unknown>;
        const props = (n.properties ?? {}) as Record<string, Node>;
        for (const r of (n.required ?? []) as string[]) if (!(r in m)) fail(`${path}.${r}`, 'missing (required)');
        for (const k of Object.keys(m).sort()) {
          const p = props[k];
          if (!p) {
            const extra = n.additionalProperties;
            if (extra && typeof extra === 'object') value(`${path}.${k}`, m[k], extra as Node);
            else if (extra === false) fail(`${path}.${k}`, 'not part of the wire protocol');
            continue;
          }
          if (typeof p['x-since'] === 'number' && p['x-since'] > version) {
            fail(`${path}.${k}`, `new in wire ${p['x-since']} while this capture says v ${version}: leave it out for an older Echo`);
            continue;
          }
          value(`${path}.${k}`, m[k], p);
        }
        return;
      }
      case 'array':
        if (!Array.isArray(x)) return fail(path, `want an array, got ${kind(x)}`);
        x.forEach((e, i) => value(`${path}[${i}]`, e, (n.items ?? {}) as Node));
        return;
      case 'string':
        if (typeof x !== 'string') return fail(path, `want a string, got ${kind(x)}`);
        if (Array.isArray(n.enum) && !n.enum.includes(x)) fail(path, `"${x}" is not one of ${n.enum.join(', ')}`);
        return;
      case 'integer':
        if (typeof x !== 'number' || !Number.isInteger(x)) return fail(path, `want an integer, got ${kind(x)}`);
        if (typeof n.minimum === 'number' && x < n.minimum) fail(path, `${x} is below ${n.minimum}`);
        if (typeof n.maximum === 'number' && x > n.maximum) fail(path, `${x} is above ${n.maximum}, the newest this Echo speaks`);
        return;
      case 'boolean':
        if (typeof x !== 'boolean') fail(path, `want true or false, got ${kind(x)}`);
    }
  };
  value(def.toLowerCase(), doc, { $ref: `#/$defs/${def}` });
  return out;
}
