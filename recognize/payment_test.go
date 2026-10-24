// Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
// SPDX-License-Identifier: MIT

package recognize

import (
	"encoding/base64"
	"encoding/json"
	"strings"
	"testing"

	"github.com/RhythmShahriar/echo/model"
	"github.com/RhythmShahriar/rhythm/kit/secrets"
)

var (
	stripeTestKey = join("sk", "_test_", "51AbCdEfGhIjKlMnOpQrStUvWx0123456789")
	stripeLiveKey = join("sk", "_live_", "51AbCdEfGhIjKlMnOpQrStUvWx0123456789")
)

func stripeReq(method, path, key, body string) Request {
	return req(method, "https://api.stripe.com"+path, map[string]string{
		"authorization": "Bearer " + key, "content-type": "application/x-www-form-urlencoded"}, body)
}

var paymentCases = []tcase{
	{name: "test payment intent", provider: "stripe", status: 200,
		req: stripeReq("POST", "/v1/payment_intents", stripeTestKey, "amount=4000&currency=usd&receipt_email=ann%40example.com&metadata%5Border%5D=1042"),
		check: func(t *testing.T, m model.Message) {
			eq(t, "channel", m.Channel, model.Payment)
			eq(t, "event", m.Event, "payment_intents create")
			eqList(t, "to", m.To, "ann@example.com")
			eq(t, "text", m.Text, "4000 usd")
			eq(t, "mode", m.Subject, "test mode")
		}},
	{name: "confirm", provider: "stripe", status: 200,
		req:   stripeReq("POST", "/v1/payment_intents/pi_3NxAbCdEf12345/confirm", stripeTestKey, ""),
		check: func(t *testing.T, m model.Message) { eq(t, "event", m.Event, "payment_intents confirm") }},
	{name: "customer update", provider: "stripe", status: 200,
		req:   stripeReq("POST", "/v1/customers/cus_P1aBcD2eF", stripeTestKey, "email=ann%40example.com"),
		check: func(t *testing.T, m model.Message) { eq(t, "event", m.Event, "customers update") }},
	{name: "list charges", provider: "stripe", status: 200,
		req:   stripeReq("GET", "/v1/charges?limit=3", stripeTestKey, ""),
		check: func(t *testing.T, m model.Message) { eq(t, "event", m.Event, "charges list") }},
	{name: "checkout session", provider: "stripe", status: 200,
		req:   stripeReq("POST", "/v1/checkout/sessions", stripeTestKey, "mode=payment"),
		check: func(t *testing.T, m model.Message) { eq(t, "event", m.Event, "checkout.sessions create") }},
	{name: "live key, masked by the connector", provider: "stripe", status: 403,
		req:   stripeReq("POST", "/v1/payment_intents", secrets.Mask(stripeLiveKey, 8), "amount=4000&currency=usd"),
		check: func(t *testing.T, m model.Message) { eq(t, "mode", m.Subject, "live mode") }},
	{name: "live key as Basic user", provider: "stripe", status: 403,
		req: req("POST", "https://api.stripe.com/v1/charges", map[string]string{
			"authorization": "Basic " + base64.StdEncoding.EncodeToString([]byte(stripeLiveKey+":"))}, "amount=100&currency=eur")},
	{name: "no readable key", provider: "stripe", status: 403,
		req: req("POST", "https://api.stripe.com/v1/refunds", map[string]string{"authorization": "•••••"}, "charge=ch_1")},
	{name: "sandbox order", provider: "paypal", status: 201,
		req: jsonReq("POST", "https://api-m.sandbox.paypal.com/v2/checkout/orders", `{"intent":"CAPTURE","purchase_units":[{"amount":{"currency_code":"USD","value":"40.00"}}]}`,
			"authorization", "Bearer A21AAexample"),
		check: func(t *testing.T, m model.Message) {
			eq(t, "event", m.Event, "checkout.orders create")
			eq(t, "text", m.Text, "40.00 USD")
			eq(t, "mode", m.Subject, "sandbox mode")
		}},
	{name: "sandbox capture", provider: "paypal", status: 201,
		req:   jsonReq("POST", "https://api-m.sandbox.paypal.com/v2/checkout/orders/5O190127TN364715T/capture", `{}`),
		check: func(t *testing.T, m model.Message) { eq(t, "event", m.Event, "checkout.orders capture") }},
	{name: "sandbox token", provider: "paypal", status: 200,
		req: formReq("https://api-m.sandbox.paypal.com/v1/oauth2/token", "grant_type=client_credentials"),
		check: func(t *testing.T, m model.Message) {
			eq(t, "event", m.Event, "oauth2.token create")
		}},
	{name: "live order", provider: "paypal", status: 403,
		req:   jsonReq("POST", "https://api-m.paypal.com/v2/checkout/orders", `{"intent":"CAPTURE"}`),
		check: func(t *testing.T, m model.Message) { eq(t, "mode", m.Subject, "live mode") }},
	{name: "live token", provider: "paypal", status: 401,
		req: formReq("https://api-m.paypal.com/v1/oauth2/token", "grant_type=client_credentials")},
}

var paymentNegatives = []negative{
	{"Stripe checkout page", "stripe", req("GET", "https://checkout.stripe.com/c/pay/cs_test_1", nil, "")},
	{"PayPal website", "paypal", req("GET", "https://www.paypal.com/checkoutnow", nil, "")},
}

// TestPaymentsRefuseLiveMode pins the payments decision: test mode gets a
// plausible success, live mode (or an unreadable key) an error the SDK
// throws on and Refuses says so for the catcher.
func TestPaymentsRefuseLiveMode(t *testing.T) {
	for _, c := range []struct {
		r      Request
		refuse bool
	}{
		{stripeReq("POST", "/v1/payment_intents", stripeTestKey, "amount=1&currency=usd"), false},
		{stripeReq("POST", "/v1/payment_intents", stripeLiveKey, "amount=1&currency=usd"), true},
		{stripeReq("POST", "/v1/payment_intents", join("rk", "_live_", "51AbCd"), ""), true},
		{jsonReq("POST", "https://api-m.sandbox.paypal.com/v2/checkout/orders", `{}`), false},
		{jsonReq("POST", "https://api.paypal.com/v2/checkout/orders", `{}`), true},
	} {
		var m model.Message
		rec := Recognize(c.r, &m)
		if got := Refuses(rec, c.r); got != c.refuse {
			t.Errorf("%s: Refuses = %v, want %v", c.r.URL, got, c.refuse)
		}
		f := FakeFor(rec, c.r, &m)
		if c.refuse != (f.Status >= 400) {
			t.Errorf("%s: fake status %d", c.r.URL, f.Status)
		}
		if c.refuse && !strings.Contains(f.Body, "Rhythm Echo refused") {
			t.Errorf("%s: refusal doesn't explain itself: %s", c.r.URL, f.Body)
		}
	}
	var m model.Message
	r := stripeReq("POST", "/v1/payment_intents", stripeTestKey, "amount=4000&currency=usd&confirm=true")
	f := FakeFor(Recognize(r, &m), r, &m)
	var pi map[string]any
	if err := json.Unmarshal([]byte(f.Body), &pi); err != nil {
		t.Fatal(err)
	}
	if pi["object"] != "payment_intent" || pi["status"] != "succeeded" || pi["amount"] != float64(4000) || pi["livemode"] != false ||
		!strings.HasPrefix(pi["id"].(string), "pi_") {
		t.Errorf("test-mode payment intent = %v", pi)
	}
	if Refuses(twilio, r) {
		t.Error("recognizers without Refuse must not refuse")
	}
}
