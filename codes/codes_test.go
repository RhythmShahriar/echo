// Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
// SPDX-License-Identifier: MIT

package codes

import (
	"fmt"
	"strings"
	"testing"

	"github.com/RhythmShahriar/echo/model"
)

func TestCodes(t *testing.T) {
	tests := []struct {
		name string
		m    model.Message
		want []string
	}{
		{"text", model.Message{Text: "Your login code is 482913. It expires in 10 minutes."}, []string{"482913"}},
		{"code after the number", model.Message{Text: "482913 is your verification code."}, []string{"482913"}},
		{"subject", model.Message{Subject: "Your one-time passcode: 7310"}, []string{"7310"}},
		{"html in a tag", model.Message{HTML: `<p>Enter this code to sign in:</p><p><strong>904418</strong></p>`}, []string{"904418"}},
		{"html entity spaces", model.Message{HTML: `Your&nbsp;OTP&nbsp;is&nbsp;<b>55120</b>`}, []string{"55120"}},
		{"alphanumeric", model.Message{Text: "Use verification code K7Q2XP to confirm your email."}, []string{"K7Q2XP"}},
		{"8 digits", model.Message{Text: "PIN: 12345678"}, []string{"12345678"}},
		{"twilio sms", model.Message{Channel: model.SMS, Text: "Acme: your security code is 615204. Don't share it."}, []string{"615204"}},
		{"code in text and html counted once", model.Message{Text: "Code: 482913", HTML: "<p>Code: <b>482913</b></p>"}, []string{"482913"}},
		{"2FA", model.Message{Text: "Your 2FA code: 33 is wrong, use 330719"}, []string{"330719"}},

		{"subject names it", model.Message{Subject: "Your sign-in code", Text: "Hi Ann,\n\nUse 482913 to sign in."}, []string{"482913"}},
		{"subject names it, html", model.Message{Subject: "Verify your email", HTML: "<h1>Welcome</h1><p><b>K7Q2XP</b></p>"}, []string{"K7Q2XP"}},

		// Negatives.
		{"subject names it, order number", model.Message{Subject: "Your verification code", Text: "Your order 482913 is here."}, nil},
		{"subject names it, far down", model.Message{Subject: "Your code", Text: strings.Repeat("Hello. ", 100) + "Meeting 482913."}, nil},
		{"subject without a keyword", model.Message{Subject: "Welcome", Text: "Use 482913 to sign in."}, nil},
		{"no keyword", model.Message{Text: "Thanks! We received 482913 votes."}, nil},
		{"order number", model.Message{Text: "Order #482913 is confirmed. Use the code below at checkout."}, nil},
		{"order number without #", model.Message{Text: "Your verification for order 482913 is done."}, nil},
		{"price", model.Message{Text: "Your code is valid on orders over $1500 or 2500.00 USD."}, nil},
		{"price after", model.Message{Text: "Promo code ok: pay 1500 EUR"}, nil},
		{"year", model.Message{Text: "Verification complete. © 2026 Acme"}, nil},
		{"phone number", model.Message{Text: "Didn't ask for a code? Call +1 555 123 4567 or 555-0199."}, nil},
		{"international phone", model.Message{Text: "Questions about your code? +4915112345"}, nil},
		{"date", model.Message{Text: "Your code expired on 24.10.2026 at 12:00."}, nil},
		{"zip code", model.Message{Text: "Please confirm your zip code 90210."}, nil},
		{"discount code", model.Message{Text: "Your discount code SAVE20 is ready."}, nil},
		{"lower case word", model.Message{Text: "Your code is ready in version html5abc."}, nil},
		{"capital word", model.Message{Text: "VERIFY YOUR ACCOUNT. Code below."}, nil},
		{"far from the keyword", model.Message{Text: "Code of conduct. " + fmt.Sprintf("%080d", 0) + " Meeting 7310 attendees today and more."}, nil},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, _ := Extract(tt.m)
			if fmt.Sprint(got) != fmt.Sprint(tt.want) {
				t.Errorf("codes %q, want %q", got, tt.want)
			}
		})
	}
}

func TestLinks(t *testing.T) {
	m := model.Message{
		HTML: `<a href="https://shop.test/wp-login.php?action=rp&amp;key=Xk29sLq&amp;login=ann">Reset</a>
			<a href='https://app.test/email/verify/7/9f8e?expires=1&signature=abc'>Verify</a>
			<a href="https://app.test/auth/magic?token=eyJabc">Sign in</a>
			<a href="https://app.test/login">Log in</a>
			<a href="https://app.test/author/ann">Ann</a>
			<a href="https://shop.test/product/42">Shoes</a>
			<a href="mailto:help@shop.test">Help</a>
			<a href="https://news.test/u/unsubscribe?id=1">Unsubscribe</a>`,
		Text:    "Reset: https://shop.test/wp-login.php?action=rp&key=Xk29sLq&login=ann. Or https://app.test/sign-in?t=abc123.",
		Headers: map[string]string{"list-unsubscribe": "<mailto:u@news.test>, <https://news.test/one-click/9>"},
	}
	_, got := Extract(m)
	want := []Link{
		{Reset, "https://shop.test/wp-login.php?action=rp&key=Xk29sLq&login=ann"},
		{Verify, "https://app.test/email/verify/7/9f8e?expires=1&signature=abc"},
		{Magic, "https://app.test/auth/magic?token=eyJabc"},
		{Login, "https://app.test/login"},
		{Unsubscribe, "https://news.test/u/unsubscribe?id=1"},
		{Magic, "https://app.test/sign-in?t=abc123"},
		{Unsubscribe, "https://news.test/one-click/9"},
	}
	if fmt.Sprint(got) != fmt.Sprint(want) {
		t.Errorf("links\n%v\nwant\n%v", got, want)
	}
	if _, links := Extract(model.Message{Text: "See https://shop.test/product/42 and https://shop.test/cart"}); links != nil {
		t.Errorf("no kind: %v", links)
	}
}

func TestExtractReadsLargeMessagesInPart(t *testing.T) {
	big := make([]byte, 2*maxScan)
	for i := range big {
		big[i] = 'x'
	}
	got, _ := Extract(model.Message{Text: "Your code is 482913 " + string(big) + " PIN 7310"})
	if fmt.Sprint(got) != "[482913]" {
		t.Errorf("codes %v", got)
	}
}
