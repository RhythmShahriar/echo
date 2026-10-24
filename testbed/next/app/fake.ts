// Fake, well-formed credentials, assembled at runtime so no key-shaped
// literal sits in the source.
export const resendKey = ['re', '_', 'testbedtestbed'].join('');
export const twilioSid = ['A', 'C', '0'.repeat(32)].join('');
export const twilioToken = 'c'.repeat(32);
export const slackHook = ['https://hooks.', 'slack.com/services/', 'T00000000/B00000001/', 'Y'.repeat(24)].join('');
