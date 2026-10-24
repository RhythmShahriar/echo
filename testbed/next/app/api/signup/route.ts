// A Node route handler that sends what a signup sends: a welcome email
// through Resend's API, a verification code by Twilio SMS, and an account
// summary through Nodemailer (SMTP).
//
// Broken state (testbed/scenarios.yaml):
// - s07: the deploy renamed RESEND_API_KEY to RESEND_KEY, so the Resend call
//   throws before it is made. The route logs it and still answers 200, as
//   signup code usually does, so nobody notices.
import { existsSync } from 'node:fs';
import nodemailer from 'nodemailer';

import { resendKey, twilioSid, twilioToken } from '../../fake';

export const dynamic = 'force-dynamic';

async function sendWelcome(name: string, email: string) {
  const key = existsSync('/state/s07') ? undefined : resendKey;
  if (!key) throw new Error('Missing API key. Pass it to the constructor `new Resend("re_123")`');
  const res = await fetch('https://api.resend.com/emails', {
    method: 'POST',
    headers: { authorization: `Bearer ${key}`, 'content-type': 'application/json' },
    body: JSON.stringify({
      from: 'Bean & Leaf <hello@shop.example.com>',
      to: [email],
      subject: `Welcome to Bean & Leaf, ${name}`,
      html: `<p>Hi ${name},</p><p>Your account is ready. Your first order ships free.</p><p><a href="https://shop.example.com/account">Your account</a></p>`,
      text: `Hi ${name},\n\nYour account is ready. Your first order ships free.\n\nhttps://shop.example.com/account\n`,
    }),
  });
  return res.status;
}

export async function POST(req: Request) {
  const form = await req.formData();
  const name = String(form.get('name') ?? '');
  const email = String(form.get('email') ?? '');
  const phone = String(form.get('phone') ?? '');

  let welcome: number | string;
  try {
    welcome = await sendWelcome(name, email);
  } catch (e) {
    console.error('welcome email failed:', e);
    welcome = 'failed';
  }

  const sms = await fetch(`https://api.twilio.com/2010-04-01/Accounts/${twilioSid}/Messages.json`, {
    method: 'POST',
    headers: { authorization: `Basic ${btoa(`${twilioSid}:${twilioToken}`)}` },
    body: new URLSearchParams({ To: phone, From: '+15555550003', Body: 'Your Bean & Leaf code is 482913' }),
  });

  const mailer = nodemailer.createTransport({ host: 'mailpit', port: 1025, secure: false, ignoreTLS: true });
  const info = await mailer.sendMail({
    from: 'Bean & Leaf <accounts@shop.example.com>',
    to: email,
    subject: 'Your Bean & Leaf account',
    text: `Hi ${name},\n\nYou signed up with ${email}. If this wasn't you, reply to this email.\n\nBean & Leaf\n`,
  });

  return Response.json({ ok: true, welcome, sms: sms.status, mail: info.response });
}
