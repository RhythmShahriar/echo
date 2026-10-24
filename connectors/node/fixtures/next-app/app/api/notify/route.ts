// A Node route handler that sends what a signup typically sends: a Resend
// email, a Twilio SMS and a Nodemailer email.
import nodemailer from 'nodemailer';

import { resendKey, twilioSid, twilioToken } from '../../fake';

export const dynamic = 'force-dynamic';

export async function POST() {
  const resend = await fetch('https://api.resend.com/emails', {
    method: 'POST',
    headers: { authorization: `Bearer ${resendKey}`, 'content-type': 'application/json' },
    body: JSON.stringify({ from: 'shop@example.test', to: ['ann@example.test'], subject: 'Welcome, Ann', html: '<p>Welcome</p>' }),
  });
  const twilio = await fetch(`https://api.twilio.com/2010-04-01/Accounts/${twilioSid}/Messages.json`, {
    method: 'POST',
    headers: { authorization: `Basic ${btoa(`${twilioSid}:${twilioToken}`)}` },
    body: new URLSearchParams({ To: '+15550001', From: '+15550002', Body: 'Your code is 123456' }),
  });
  const mailer = nodemailer.createTransport({ host: 'smtp.example.test', port: 587 });
  const info = await mailer.sendMail({
    from: 'shop@example.test',
    to: 'ann@example.test',
    subject: 'Your receipt',
    text: 'Thanks for your order',
  });
  return Response.json({
    resend: { status: resend.status, body: await resend.text() },
    twilio: { status: twilio.status, body: await twilio.text() },
    mail: { messageId: info.messageId, response: info.response },
  });
}
