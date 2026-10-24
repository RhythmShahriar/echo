// Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
// SPDX-License-Identifier: MIT

package mcp

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/RhythmShahriar/echo/model"
)

// Each rule with a value that must be masked and one that must stay.
func TestMaskText(t *testing.T) {
	for _, tc := range []struct {
		name, in, gone, kept string
	}{
		{"one-time link code", "Reset: https://shop.test/reset?code=482913&page=2", "482913", "page=2"},
		{"reset key", "https://shop.test/wp-login.php?action=rp&key=Zk3pQ9vX2mL7tR4w&login=ann", "Zk3pQ9vX2mL7tR4w", "login=ann"},
		{"signature", "https://cdn.test/file?sig=4f9a8b7c6d5e&w=200", "4f9a8b7c6d5e", "w=200"},
		{"telegram bot token", "https://api.telegram.org/bot123456:AAHdqTcvCH1vGWJxfSeofSAs0K5PALDsaw/sendMessage", "AAHdqTcvCH1vGWJxfSeofSAs0K5PALDsaw", "/sendMessage"},
		{"ifttt key path", "https://maker.ifttt.com/trigger/order/with/key/dE3fG4hI5jK6lM7n", "dE3fG4hI5jK6lM7n", "/trigger/order"},
		{"kit rule: stripe key", "key sk_live_4eC39HqLyjWDarjtT1zdp7dc here", "4eC39HqLyjWDarjtT1zdp7dc", "here"},
		{"plain text", "Your order W-1001 ships on 2026-10-24", "", "Your order W-1001 ships on 2026-10-24"},
		{"plain url", "https://shop.test/orders?page=2&sort=new", "", "https://shop.test/orders?page=2&sort=new"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got := maskText(tc.in)
			if tc.gone != "" && strings.Contains(got, tc.gone) {
				t.Errorf("%q kept %q", got, tc.gone)
			}
			if !strings.Contains(got, tc.kept) {
				t.Errorf("%q lost %q", got, tc.kept)
			}
		})
	}
}

func TestMaskHeaders(t *testing.T) {
	m := model.Message{ID: "abc", Channel: model.Webhook, Headers: map[string]string{
		"x-shopify-access-token": "shpat_0123456789abcdef", "x-amz-security-token": "FwoGZXIvYXdzEJr", "private-token": "glpat-123456789",
		"x-goog-api-key": "AIzaSyD-abcdefghijklmnop", "x-request-id": "req-42", "content-type": "application/json"}}
	b, _ := json.Marshal(Mask(m))
	for _, secret := range []string{"shpat_0123456789abcdef", "FwoGZXIvYXdzEJr", "glpat-123456789", "AIzaSyD-abcdefghijklmnop"} {
		if strings.Contains(string(b), secret) {
			t.Errorf("%s is in %s", secret, b)
		}
	}
	for _, kept := range []string{`"x-request-id":"req-42"`, `"content-type":"application/json"`, `"id":"abc"`} {
		if !strings.Contains(string(b), kept) {
			t.Errorf("%s is gone from %s", kept, b)
		}
	}
	// Outside headers a key named like a credential keeps its value: an
	// issue's comparison key is not a secret.
	b, _ = json.Marshal(Mask(model.Issue{Check: "content/merge-tag", Title: "x", Key: "content/merge-tag|x"}))
	if !strings.Contains(string(b), `"key":"content/merge-tag|x"`) {
		t.Errorf("issue key masked: %s", b)
	}
}

func TestCode(t *testing.T) {
	for _, tc := range []struct {
		name string
		m    model.Message
		want string
	}{
		{"after the word", model.Message{Subject: "Your verification code", Text: "Use 482913 to sign in."}, "482913"},
		{"code is", model.Message{Text: "Hi Ann, your login code is 7731. It expires in 10 minutes."}, "7731"},
		{"before the word", model.Message{Text: "582014 is your Shop verification code"}, "582014"},
		{"in the subject", model.Message{Subject: "293811 is your one-time passcode", Text: "Thanks"}, "293811"},
		{"letters and digits", model.Message{Text: "Your security code: K7Q2ZP"}, "K7Q2ZP"},
		{"in a link", model.Message{HTML: `<a href="https://shop.test/verify?code=661204">Verify</a>`}, "661204"},
		{"lone six digits in an SMS", model.Message{Channel: model.SMS, Text: "Shop: 902114"}, "902114"},
		{"html only", model.Message{HTML: "<p>Your code&nbsp;is <b>440912</b></p>"}, "440912"},
		{"year is no code", model.Message{Text: "Your 2026 sign-in summary"}, ""},
		{"order number is no code", model.Message{Subject: "Order W-1001 received", Text: "Thanks for your order of 3 items."}, ""},
		{"two numbers, no word", model.Message{Text: "Ticket 123456 and 654321"}, ""},
		{"word without a code", model.Message{Text: "Use the code from the app to sign in."}, ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := Code(tc.m); got != tc.want {
				t.Errorf("Code = %q, want %q", got, tc.want)
			}
		})
	}
}
