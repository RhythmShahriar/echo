// An Edge route: tells the team about a new signup in Slack. Echo's fetch
// hook works in the Edge runtime too.
import { slackHook } from '../../fake';

export const runtime = 'edge';
export const dynamic = 'force-dynamic';

export async function GET() {
  const res = await fetch(slackHook, {
    method: 'POST',
    headers: { 'content-type': 'application/json' },
    body: JSON.stringify({ text: ':wave: New Bean & Leaf account signup' }),
  });
  return Response.json({ ok: res.ok, runtime: process.env.NEXT_RUNTIME });
}
