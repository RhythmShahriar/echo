// Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
// SPDX-License-Identifier: MIT

package mask

import (
	"os"
	"strings"
	"testing"
)

// secret is a made-up credential no real scanner knows.
const secret = "Zq9TOPSECRETvalue77"

// Each rule has a positive case (the secret is gone) and a negative one
// (ordinary data is kept as it was).
func TestRules(t *testing.T) {
	for _, c := range []struct {
		name   string
		fn     func(string) string
		in     string
		gone   string // must not appear in the output ("" for negatives)
		want   string // must appear in the output
		intact bool   // the output equals the input
	}{
		// Header: masked whole by name.
		{"x-auth-token", hdr("X-Auth-Token"), secret, secret, Hidden, false},
		{"x-amz-security-token", hdr("x-amz-security-token"), secret, secret, Hidden, false},
		{"private-token", hdr("Private-Token"), secret, secret, Hidden, false},
		{"x-goog-api-key", hdr("X-Goog-Api-Key"), secret, secret, Hidden, false},
		{"x-shopify-access-token", hdr("X-Shopify-Access-Token"), secret, secret, Hidden, false},
		{"authorization", hdr("authorization"), "Bearer " + secret, secret, Hidden, false},
		{"cookie", hdr("Cookie"), "wordpress_logged_in=" + secret, secret, Hidden, false},
		{"cf-access-jwt-assertion", hdr("Cf-Access-Jwt-Assertion"), "eyJhbGciOiJIUzI1NiJ9.eyJzdWIiOiJqYW5lIiwiZXhwIjo5OTl9.c2lnbmF0dXJlX3ZhbHVl", "eyJzdWIi", Hidden, false},
		{"x-forwarded-for kept", hdr("X-Forwarded-For"), "203.0.113.7", "", "", true},
		{"x-request-id kept", hdr("X-Request-Id"), "req-7f3a9c-" + secret, "", "", true},
		{"content-type kept", hdr("Content-Type"), "application/json; charset=utf-8", "", "", true},
		{"empty stays empty", hdr("X-Wc-Webhook-Signature"), "", "", "", true},
		{"other header, url secret", hdr("List-Unsubscribe"), "<https://shop.test/unsub?token=" + secret + ">", secret, "token=" + Hidden, false},

		// URL: Telegram bot tokens.
		{"telegram", URL, "https://api.telegram.org/bot123456789:AAH" + secret + "abcdefghij/sendMessage", secret, "bot123456789:AAH", false},
		{"telegram without scheme", URL, "api.telegram.org/bot123456789:AAH" + secret + "abcdefghij/sendMessage", secret, "/sendMessage", false},
		{"bot path that is not a token", URL, "https://shop.test/bots/robot42/about", "", "", true},
		// URL: /key/<x> segments.
		{"ifttt", URL, "https://maker.ifttt.com/trigger/order/with/key/" + secret, secret, "/with/key/", false},
		{"keys path kept", URL, "https://shop.test/keyboards/42", "", "", true},
		// URL: query values.
		{"wp reset key", URL, "https://shop.test/wp-login.php?action=rp&key=" + secret + "&login=jane", secret, "login=jane", false},
		{"code", URL, "https://shop.test/verify?code=482913", "482913", "code=" + Hidden, false},
		{"k", URL, "https://shop.test/magic?k=" + secret, secret, "k=" + Hidden, false},
		{"otp", URL, "https://shop.test/login?otp=" + secret, secret, "otp=" + Hidden, false},
		{"reset", URL, "https://shop.test/a?reset=" + secret, secret, "reset=" + Hidden, false},
		{"sig in html", URL, "https://shop.test/dl?file=a.pdf&amp;sig=" + secret, secret, "file=a.pdf", false},
		{"hash", URL, "https://shop.test/confirm?hash=" + secret, secret, "hash=" + Hidden, false},
		{"auth", URL, "https://shop.test/hook?auth=" + secret, secret, "auth=" + Hidden, false},
		{"ticket", URL, "https://sso.test/validate?ticket=ST-" + secret, secret, "ticket=" + Hidden, false},
		{"author kept", URL, "https://shop.test/blog?author=jane&t=42", "", "", true},
		{"page kept", URL, "https://shop.test/orders?page=2&utm_source=email", "", "", true},
		{"keyword kept", URL, "https://shop.test/search?keyword=shoes&sort=price", "", "", true},
		{"gcs signed url", URL, "https://storage.googleapis.com/b/invoice.pdf?X-Goog-Algorithm=GOOG4-RSA-SHA256&X-Goog-Credential=svc%40p.iam.gserviceaccount.com&X-Goog-Signature=7f3a" + secret,
			"7f3a" + secret, "X-Goog-Signature=" + Hidden, false},
		{"s3 signed url", URL, "https://b.s3.amazonaws.com/a.pdf?X-Amz-Credential=AKIA%2F20261024&X-Amz-Security-Token=" + secret + "&X-Amz-Signature=9c1d" + secret,
			"9c1d" + secret, "X-Amz-Signature=" + Hidden, false},
		{"s3 security token", URL, "https://b.s3.amazonaws.com/a.pdf?X-Amz-Security-Token=" + secret, secret, "X-Amz-Security-Token=" + Hidden, false},
		{"magic", URL, "https://shop.test/login?magic=Zx81kQ0pL2mN7rT4", "Zx81kQ0pL2mN7rT4", "magic=" + Hidden, false},
		{"login_token", URL, "https://shop.test/login?login_token=" + secret, secret, "login_token=" + Hidden, false},
		{"api_key", URL, "https://api.shop.test/x?api_key=supersecretvalue123", "supersecretvalue123", "api_key=" + Hidden, false},
		{"signature in text", Text, "Download: https://b.s3.amazonaws.com/a.pdf?X-Amz-Signature=9c1d" + secret, "9c1d" + secret, "X-Amz-Signature=" + Hidden, false},
		{"monkey kept", URL, "https://shop.test/zoo?monkey=george&keys=3", "", "", true},

		// Text: the kit's rules, codes and URL on every URL.
		{"link in text", Text, "Reset: https://shop.test/wp-login.php?action=rp&key=" + secret + "&login=jane.", secret, "login=jane.", false},
		{"link in html", Text, `<a href="https://shop.test/magic?token=` + secret + `&amp;page=2">here</a>`, secret, "page=2\">here</a>", false},
		{"login code subject", Text, "Your login code is 482913", "482913", "Your login code is " + Hidden, false},
		{"otp text", Text, "OTP: 77120934", "77120934", Hidden, false},
		{"order number kept", Text, "Order #482913 has shipped", "", "", true},
		{"zip code kept", Text, "Zip code 90210", "", "", true},
		{"kit rule still applies", Text, "password=" + secret, secret, "password=", false},
		{"jwt in text", Text, "Bearer-less: eyJhbGciOiJIUzI1NiJ9.eyJzdWIiOiJqYW5lIiwiZXhwIjo5OTl9.c2lnbmF0dXJlX3ZhbHVl done", "eyJzdWIi", " done", false},
		{"jwt-like words kept", Text, "eyJ is how base64 JSON starts. See docs.eyJ.test", "", "", true},
		{"plain text kept", Text, "Thanks for your order, Jane!", "", "", true},

		// JSON: string and number values by key name.
		{"json pass", JSON, `{"auth":{"user":"u","pass":"` + secret + `"}}`, secret, `"user":"u"`, false},
		{"json key", JSON, `{"key":"` + secret + `"}`, secret, `"key":"`, false},
		{"json numeric apiKey", JSON, `{"apiKey": 123456789012, "n": 3}`, "123456789012", `"n": 3`, false},
		{"json pwd", JSON, `{"db_pwd":"` + secret + `"}`, secret, `"db_pwd"`, false},
		{"json escaped value", JSON, `{"token":"a\"` + secret + `"}`, secret, `"token"`, false},
		{"json bare key, random value", JSON, `{"key":"a8f3k29dk3m2x9"}`, "a8f3k29dk3m2x9", `"key":"`, false},
		{"json license_key", JSON, `{"license_key":"abc"}`, `"abc"`, `"license_key"`, false},
		{"json meta_data keys kept", JSON, `{"meta_data":[{"id":1,"key":"_billing_phone","value":"555"},{"key":"gift wrap","value":"yes"}]}`, "", "", true},
		{"json order_key kept", JSON, `{"order_key":"wc_order_Ab12Cd34","idempotency_key":"k-1"}`, "", "", true},
		{"json token counts kept", JSON, `{"usage":{"prompt_tokens":12,"total_tokens":40}}`, "", "", true},
		{"json author kept", JSON, `{"author": 12, "keywords": "shoes", "total": 42.5}`, "", "", true},
		{"json order kept", JSON, `{"id":4242,"status":"processing","line_items":[{"sku":"A1","qty":2}]}`, "", "", true},
		{"json otp number", JSON, `{"otp": 482913}`, "482913", `"otp": "` + Hidden + `"`, false},
		{"json code string", JSON, `{"code": "482913"}`, "48", `"code": "` + Hidden + `"`, false},
		{"json pin", JSON, `{"pin": 1234}`, "1234", `"pin": "` + Hidden + `"`, false},
		{"json login_code letters and digits", JSON, `{"login_code": "X7K9QZ"}`, "X7K9QZ", `"login_code": "` + Hidden + `"`, false},
		{"json bare numeric key", JSON, `{"key": 48291374}`, "48291374", `"key": "`, false},
		{"json error and postal codes kept", JSON, `{"code":"rest_forbidden","data":{"status":403},"postcode":"90210","country_code":"US","status_code":404}`, "", "", true},
		{"json short code and numeric key kept", JSON, `{"code":42,"key":2024,"shipping":"1234"}`, "", "", true},
		{"json cut short", JSON, `{"items":[1,2],"secret":"` + secret, "", secret, true}, // an unterminated value can't be read: nothing to mask, nothing broken
	} {
		t.Run(c.name, func(t *testing.T) {
			got := c.fn(c.in)
			if c.intact && got != c.in {
				t.Errorf("changed:\n  in  %q\n  out %q", c.in, got)
			}
			if c.gone != "" && strings.Contains(got, c.gone) {
				t.Errorf("%q still in %q", c.gone, got)
			}
			if c.want != "" && !strings.Contains(got, c.want) {
				t.Errorf("%q not in %q", c.want, got)
			}
		})
	}
}

func hdr(name string) func(string) string { return func(v string) string { return Header(name, v) } }

func TestKeepsAtMostFour(t *testing.T) {
	for _, in := range []string{
		"https://maker.ifttt.com/trigger/x/with/key/" + secret,
		"https://api.telegram.org/bot1:" + secret + secret + "/getMe",
		`{"token":"` + secret + `"}`,
	} {
		got := Body(in)
		for i := 0; i+5 <= len(secret); i++ {
			if strings.Contains(got, secret[i:i+5]) {
				t.Errorf("%q keeps %q of the secret", got, secret[i:i+5])
			}
		}
	}
}

func TestHeadersMasksEveryValue(t *testing.T) {
	got := Headers(map[string]string{"x-shopify-access-token": secret, "x-request-id": "r-1"})
	if got["x-shopify-access-token"] != Hidden || got["x-request-id"] != "r-1" {
		t.Errorf("headers = %v", got)
	}
	if Headers(nil) != nil {
		t.Error("nil headers should stay nil")
	}
}

func TestMIMEDecodesAndRedacts(t *testing.T) {
	raw, err := os.ReadFile("testdata/encoded.eml")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(raw), "Content-Transfer-Encoding: base64") || strings.Contains(string(raw), "key="+secret) {
		t.Fatal("the fixture should hide the reset link in base64")
	}
	sum, _ := os.ReadFile("testdata/encoded.pdf.sha256")
	size, hash, _ := strings.Cut(strings.TrimSpace(string(sum)), " ")
	got := MIME(string(raw))
	for _, gone := range []string{secret, "482913", "JVBERi0", "Content-Transfer-Encoding: base64", "quoted-printable"} {
		if strings.Contains(got, gone) {
			t.Errorf("%q in masked MIME:\n%s", gone, got)
		}
	}
	for _, want := range []string{
		"Subject: Your login code is " + Hidden,
		"X-Auth-Token: " + Hidden,
		"X-Request-Id: req-7f3a9c",
		"Reset your password: https://shop.test/wp-login.php?action=rp&key=" + Hidden + "&login=jane",
		"magic?token=" + Hidden + "&amp;page=2",
		"– café", // quoted-printable decoded to UTF-8
		"Content-Transfer-Encoding: 8bit",
		"[attachment invoice-42.pdf application/pdf " + size + " sha256:" + hash + "]",
		"--outer--",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("%q not in masked MIME:\n%s", want, got)
		}
	}
}

func TestMIMEKeepsAPlainMessage(t *testing.T) {
	raw, err := os.ReadFile("testdata/plain.eml")
	if err != nil {
		t.Fatal(err)
	}
	got := MIME(string(raw))
	for _, want := range []string{"Subject: Order #42 shipped", "?page=2&utm_source=email", "Zip code 90210", "From: shop@shop.test"} {
		if !strings.Contains(got, want) {
			t.Errorf("%q not in %s", want, got)
		}
	}
}

func TestMIMECutShort(t *testing.T) {
	raw, _ := os.ReadFile("testdata/encoded.eml")
	// Cut inside the PDF: the text parts are masked, the attachment is
	// named and marked as cut.
	i := strings.Index(string(raw), "JVBERi0")
	got := MIME(string(raw[:i+200]))
	if strings.Contains(got, secret) || !strings.Contains(got, "[attachment invoice-42.pdf application/pdf cut]") {
		t.Errorf("cut MIME:\n%s", got)
	}
	// Cut inside the base64 text part: what decodes is masked.
	j := strings.Index(string(raw), "base64\r\n\r\n") + 10
	if got := MIME(string(raw[:j+60])); strings.Contains(got, "SGkgSmFuZ") {
		t.Errorf("base64 left encoded: %s", got)
	}
}

func TestMIMEFallsBackToText(t *testing.T) {
	in := "not a header line\nReset: https://shop.test/?key=" + secret
	if got := MIME(in); strings.Contains(got, secret) {
		t.Errorf("unparsed MIME leaked: %q", got)
	}
}
