// Talking to Echo. Every capture goes through send(), which never throws:
// null means "no decision", and each hook turns that into fail-closed or
// fail-open depending on the last mode Echo reported.

import { state, warnOnce } from './state.js';
import { TOKEN_HEADER, WIRE_VERSION, type Capture, type Decision } from './wire.js';

export async function send(c: Omit<Capture, 'v'>): Promise<Decision | null> {
  const s = state();
  const cfg = s.cfg;
  const f = s.origFetch ?? globalThis.fetch;
  if (!cfg || !f) return null;
  const headers: Record<string, string> = { 'content-type': 'application/json' };
  if (cfg.token) headers[TOKEN_HEADER] = cfg.token;
  try {
    const res = await f(`${cfg.url}/v1/capture`, {
      method: 'POST',
      headers,
      body: JSON.stringify({ v: WIRE_VERSION, ...c }),
      signal: AbortSignal.timeout(cfg.timeout),
      // keep Next.js's fetch cache out of it if a patched fetch is underneath
      cache: 'no-store',
    });
    if (!res.ok) {
      const text = (await res.text().catch(() => '')).trim().slice(0, 200);
      warnOnce(`status-${res.status}`, `Echo at ${cfg.url} answered ${res.status}${text ? `: ${text}` : ''}`);
      return null;
    }
    const d = (await res.json()) as Decision;
    if (d.mode) s.mode = d.mode;
    return d;
  } catch (err) {
    warnOnce(
      'unreachable',
      `Echo is unreachable at ${cfg.url} (${errText(err)}); ` +
        (s.mode === 'observe' ? 'letting messages through (observe mode)' : 'blocking outbound requests and mail (sealed)'),
    );
    return null;
  }
}

/** sealed reports whether a missing decision must block. */
export function sealed(): boolean {
  return state().mode !== 'observe';
}

export function errText(err: unknown): string {
  if (err instanceof Error) {
    const cause = (err as { cause?: unknown }).cause;
    return cause instanceof Error ? `${err.message}: ${cause.message}` : err.message;
  }
  return String(err);
}

/** Fire-and-forget for "after" captures: the app's response must not wait for Echo. */
export function sendLater(c: Omit<Capture, 'v'> | Promise<Omit<Capture, 'v'>>): void {
  const inflight = state().inflight;
  const p = Promise.resolve(c)
    .then(send)
    .catch(() => null)
    .finally(() => inflight.delete(p));
  inflight.add(p);
}

/**
 * flush waits for "after" captures still being sent. Serverless handlers
 * and tests call it before they finish; a long-running server doesn't need to.
 */
export async function flush(): Promise<void> {
  const inflight = state().inflight;
  while (inflight.size > 0) await Promise.all([...inflight]);
}
