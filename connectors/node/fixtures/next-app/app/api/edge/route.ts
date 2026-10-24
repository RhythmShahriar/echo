// Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
// SPDX-License-Identifier: MIT

// An Edge route: the fetch hook works in the Edge runtime too.
import { slackHook } from '../../fake';

export const runtime = 'edge';
export const dynamic = 'force-dynamic';

export async function GET() {
  const res = await fetch(slackHook, {
    method: 'POST',
    headers: { 'content-type': 'application/json' },
    body: JSON.stringify({ text: 'New order #1001' }),
  });
  return Response.json({ status: res.status, body: await res.text(), runtime: process.env.NEXT_RUNTIME });
}
