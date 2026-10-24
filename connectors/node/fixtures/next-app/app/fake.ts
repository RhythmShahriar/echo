// Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
// SPDX-License-Identifier: MIT

// Fake, well-formed credentials, assembled at runtime so no key-shaped
// literal sits in the source.
export const resendKey = ['re', '_', 'fixturefixture'].join('');
export const twilioSid = ['A', 'C', '0'.repeat(32)].join('');
export const twilioToken = 'f'.repeat(32);
export const slackHook = ['https://hooks.', 'slack.com/services/', 'T00000000/B00000000/', 'X'.repeat(24)].join('');
