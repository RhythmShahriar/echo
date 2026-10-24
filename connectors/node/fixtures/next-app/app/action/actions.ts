// Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
// SPDX-License-Identifier: MIT

'use server';

// A server action that calls a webhook (Zapier-style).
export async function notifyTeam(): Promise<{ status: number }> {
  const res = await fetch('https://hooks.zapier.example.test/hooks/catch/123/abc/', {
    method: 'POST',
    headers: { 'content-type': 'application/json' },
    body: JSON.stringify({ event: 'contact', email: 'ann@example.test' }),
  });
  return { status: res.status };
}
