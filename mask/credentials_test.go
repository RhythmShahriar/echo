// Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
// SPDX-License-Identifier: MIT

package mask

import "testing"

// Each payload goes through Body, as Echo stores it, then Credentials
// counts what is left: credentials count, codes and link tokens don't.
func TestCredentialsCountsOnlyCredentials(t *testing.T) {
	for _, c := range []struct {
		name, in string
		want     int
	}{
		// Positive: a credential was masked.
		{"json api_key", `{"email":"a@b.test","api_key":"` + secret + `"}`, 1},
		{"json password", `{"user":"jane","password":"hunter22"}`, 1},
		{"json bare key, random", `{"key":"` + secret + `"}`, 1},
		{"form api_key", "name=jane&api_key=" + secret, 1},
		{"url api_key", `{"url":"https://crm.test/hook?api_key=` + secret + `"}`, 1},
		{"bearer", "Authorization: Bearer " + secret, 1},
		{"stripe key in text", "use sk_live_" + secret + "abcdef please", 1},
		{"env line", "DB_PASSWORD=" + secret, 1},
		{"telegram bot path", "https://api.telegram.org/bot123456:AAH" + secret + "abcdefghijk/sendMessage", 1},
		{"already masked", `{"api_key":"ab` + Hidden + `"}`, 1},
		// Negative: a one-time code, a link token or a signature.
		{"sms code", "Your code is 4411", 0},
		{"sms code first", Hidden + " is your verification code", 0},
		{"twilio form body", "To=%2B15551234567&From=%2B15557654321&Body=Your+code+is+" + Hidden, 0},
		{"json otp", `{"to":"+15551234567","otp":"482913"}`, 0},
		{"json login code", `{"login_code":"X7K9QZ"}`, 0},
		{"json login token", `{"login_token":"` + secret + `"}`, 0},
		{"magic link", "Sign in: https://app.test/login?token=" + secret, 0},
		{"reset path", "https://app.test/reset-password/" + Hidden + "?email=a%40b.test", 0},
		{"signed url", "https://bucket.test/f.pdf?X-Amz-Credential=abc&X-Amz-Signature=" + secret, 0},
		{"code hidden in a message field", `{"message":"` + Hidden + ` is your code"}`, 0},
		{"nothing masked", `{"order":1042}`, 0},
	} {
		if got := Credentials(Body(c.in)); got != c.want {
			t.Errorf("%s: Credentials(%q) = %d, want %d", c.name, Body(c.in), got, c.want)
		}
	}
}
