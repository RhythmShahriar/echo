// Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
// SPDX-License-Identifier: MIT

package recognize

import (
	"encoding/base64"
	"slices"
	"strings"
	"testing"

	"github.com/RhythmShahriar/echo/model"
)

var (
	mollieTest   = join("test", "_", "dHar4XY7LxsDOtmnkVtjNVWXLSlXsM")
	mollieLive   = join("live", "_", "dHar4XY7LxsDOtmnkVtjNVWXLSlXsM")
	razorpayTest = "Basic " + base64.StdEncoding.EncodeToString([]byte(join("rzp", "_test_", "1DP5mmOlF5G5ag:secret")))
	razorpayLive = "Basic " + base64.StdEncoding.EncodeToString([]byte(join("rzp", "_live_", "1DP5mmOlF5G5ag:secret")))
)

// gatewayCase is one gateway's live call (refused) and sandbox call
// (passed as normal).
type gatewayCase struct {
	provider      string
	live, sandbox Request
}

var gatewayPairs = []gatewayCase{
	{"adyen",
		jsonReq("POST", "https://1797a841fbb37ca7-AdyenDemo-checkout-live.adyenpayments.com/checkout/v71/payments", `{"amount":{"value":4000,"currency":"EUR"},"reference":"1042","shopperEmail":"ann@example.com"}`),
		jsonReq("POST", "https://checkout-test.adyen.com/v71/payments", `{"amount":{"value":4000,"currency":"EUR"},"reference":"1042","shopperEmail":"ann@example.com"}`)},
	{"braintree",
		req("POST", "https://api.braintreegateway.com/merchants/abc123/transactions", map[string]string{"content-type": "application/xml"},
			`<transaction><amount>40.00</amount><type>sale</type><customer><email>ann@example.com</email></customer></transaction>`),
		req("POST", "https://api.sandbox.braintreegateway.com/merchants/abc123/transactions", map[string]string{"content-type": "application/xml"},
			`<transaction><amount>40.00</amount><type>sale</type><customer><email>ann@example.com</email></customer></transaction>`)},
	{"braintree",
		jsonReq("POST", "https://payments.braintree-api.com/graphql", `{"query":"mutation { chargePaymentMethod }"}`),
		jsonReq("POST", "https://payments.sandbox.braintree-api.com/graphql", `{"query":"mutation { chargePaymentMethod }"}`)},
	{"square",
		jsonReq("POST", "https://connect.squareup.com/v2/payments", `{"source_id":"cnon:card","amount_money":{"amount":4000,"currency":"USD"},"buyer_email_address":"ann@example.com"}`),
		jsonReq("POST", "https://connect.squareupsandbox.com/v2/payments", `{"source_id":"cnon:card","amount_money":{"amount":4000,"currency":"USD"},"buyer_email_address":"ann@example.com"}`)},
	{"authorize-net",
		jsonReq("POST", "https://api.authorize.net/xml/v1/request.api", `{"createTransactionRequest":{"transactionRequest":{"transactionType":"authCaptureTransaction","amount":"40.00","customer":{"email":"ann@example.com"}}}}`),
		jsonReq("POST", "https://apitest.authorize.net/xml/v1/request.api", `{"createTransactionRequest":{"transactionRequest":{"transactionType":"authCaptureTransaction","amount":"40.00","customer":{"email":"ann@example.com"}}}}`)},
	{"authorize-net",
		req("POST", "https://api2.authorize.net/xml/v1/request.api", map[string]string{"content-type": "text/xml"}, `<createTransactionRequest/>`),
		req("POST", "https://apitest.authorize.net/xml/v1/request.api", map[string]string{"content-type": "text/xml"}, `<createTransactionRequest/>`)},
	{"mollie",
		jsonReq("POST", "https://api.mollie.com/v2/payments", `{"amount":{"currency":"EUR","value":"40.00"},"description":"Order 1042","billingEmail":"ann@example.com"}`, "authorization", "Bearer "+mollieLive),
		jsonReq("POST", "https://api.mollie.com/v2/payments", `{"amount":{"currency":"EUR","value":"40.00"},"description":"Order 1042","billingEmail":"ann@example.com"}`, "authorization", "Bearer "+mollieTest)},
	{"mollie",
		jsonReq("POST", "https://api.mollie.com/v2/payments", `{"amount":{"currency":"EUR","value":"1.00"}}`, "authorization", "Bearer "+join("access", "_", "abc")),
		jsonReq("POST", "https://api.mollie.com/v2/payments", `{"amount":{"currency":"EUR","value":"1.00"},"testmode":true}`, "authorization", "Bearer "+join("access", "_", "abc"))},
	{"razorpay",
		jsonReq("POST", "https://api.razorpay.com/v1/orders", `{"amount":400000,"currency":"INR","receipt":"1042"}`, "authorization", razorpayLive),
		jsonReq("POST", "https://api.razorpay.com/v1/orders", `{"amount":400000,"currency":"INR","receipt":"1042"}`, "authorization", razorpayTest)},
}

// gatewayCases are the pairs as recognizer fixtures: a live call's fake
// is the gateway's refusal, a sandbox call's a success.
func gatewayCases() []tcase {
	var out []tcase
	for _, g := range gatewayPairs {
		var lm, sm model.Message
		out = append(out,
			tcase{name: "live " + g.live.URL.Host, provider: g.provider, req: g.live, status: FakeFor(Recognize(g.live, &lm), g.live, &lm).Status,
				check: func(t *testing.T, m model.Message) {
					eq(t, "channel", m.Channel, model.Payment)
					eq(t, "mode", m.Subject, "live mode")
				}},
			tcase{name: "sandbox " + g.sandbox.URL.Host, provider: g.provider, req: g.sandbox, status: FakeFor(Recognize(g.sandbox, &sm), g.sandbox, &sm).Status,
				check: func(t *testing.T, m model.Message) {
					if m.Subject != "sandbox mode" && m.Subject != "test mode" {
						t.Errorf("mode = %q", m.Subject)
					}
				}})
	}
	return out
}

var gatewayNegatives = []negative{
	{"Adyen website", "adyen", req("GET", "https://www.adyen.com/pricing", nil, "")},
	{"Adyen docs", "adyen", req("GET", "https://docs.adyen.com/online-payments", nil, "")},
	{"Braintree website", "braintree", req("GET", "https://www.braintreegateway.com/login", nil, "")},
	{"Square website", "square", req("GET", "https://squareup.com/us/en", nil, "")},
	{"Square developer site", "square", req("GET", "https://developer.squareup.com/docs", nil, "")},
	{"Authorize.net accept hosted page", "authorize-net", req("GET", "https://accept.authorize.net/payment/payment", nil, "")},
	{"Mollie website", "mollie", req("GET", "https://www.mollie.com/checkout/select-method/abc", nil, "")},
	{"Razorpay checkout script", "razorpay", req("GET", "https://checkout.razorpay.com/v1/checkout.js", nil, "")},
}

// TestGatewaysRefuseLiveCalls: per gateway, the live call is refused with
// an error the SDK throws on and the sandbox call passes as normal.
func TestGatewaysRefuseLiveCalls(t *testing.T) {
	for _, g := range gatewayPairs {
		var m model.Message
		rec := Recognize(g.live, &m)
		f := FakeFor(rec, g.live, &m)
		if rec.Provider != g.provider || !Refuses(rec, g.live) || f.Status < 400 || !strings.Contains(f.Body, "Rhythm Echo refused a live") {
			t.Errorf("%s live %s: provider %q, refused %v, fake %d %s", g.provider, g.live.URL, rec.Provider, Refuses(rec, g.live), f.Status, f.Body)
		}
		m = model.Message{}
		rec = Recognize(g.sandbox, &m)
		f = FakeFor(rec, g.sandbox, &m)
		if rec.Provider != g.provider || Refuses(rec, g.sandbox) || f.Status >= 300 {
			t.Errorf("%s sandbox %s: provider %q, refused %v, fake %d %s", g.provider, g.sandbox.URL, rec.Provider, Refuses(rec, g.sandbox), f.Status, f.Body)
		}
	}
}

// TestGatewayTunnelsAreRefused: a tunnel shows only the host. A live host
// is refused, so is a host shared by test and live (no key to read); a
// sandbox host is not.
func TestGatewayTunnelsAreRefused(t *testing.T) {
	for host, refused := range map[string]bool{
		"api.braintreegateway.com": true, "api.sandbox.braintreegateway.com": false, "connect.squareup.com": true,
		"connect.squareupsandbox.com": false, "api.mollie.com": true, "api.razorpay.com": true, "apitest.authorize.net": false,
		"abc-AdyenDemo-pal-live.adyenpayments.com": true, "pal-test.adyen.com": false,
	} {
		r := req("CONNECT", "https://"+host+"/", nil, "")
		var m model.Message
		rec := Recognize(r, &m)
		if rec.Channel != model.Payment || Refuses(rec, r) != refused || m.Event != "" {
			t.Errorf("%s: %s refused=%v event %q", host, rec.Provider, Refuses(rec, r), m.Event)
		}
	}
}

func TestGatewayFill(t *testing.T) {
	var m model.Message
	Recognize(gatewayPairs[0].sandbox, &m)
	eq(t, "event", m.Event, "payments create")
	eq(t, "text", m.Text, "4000 EUR")
	eqList(t, "to", m.To, "ann@example.com")
	m = model.Message{}
	Recognize(gatewayPairs[4].sandbox, &m)
	eq(t, "event", m.Event, "createTransaction")
	eq(t, "text", m.Text, "40.00")
	m = model.Message{}
	Recognize(gatewayPairs[1].live, &m)
	eq(t, "text", m.Text, "40.00")
	eqList(t, "to", m.To, "ann@example.com")
}

func TestPaymentGateways(t *testing.T) {
	var names []string
	for _, g := range PaymentGateways() {
		names = append(names, g.Provider)
		if g.By != "host" && g.By != "key" || len(g.Live) == 0 || len(g.Sandbox) == 0 || g.Name == "" {
			t.Errorf("gateway %+v", g)
		}
	}
	want := []string{"stripe", "paypal", "adyen", "braintree", "square", "authorize-net", "mollie", "razorpay"}
	if !slices.Equal(names, want) {
		t.Errorf("PaymentGateways = %q, want %q", names, want)
	}
	for _, c := range []struct {
		host string
		live bool
	}{
		{"api.braintreegateway.com", true}, {"api.sandbox.braintreegateway.com", false}, {"api.mollie.com", false},
		{"api-m.paypal.com", true}, {"x-Shop-checkout-live.adyenpayments.com", true},
	} {
		got := slices.ContainsFunc(PaymentGateways(), func(g Gateway) bool { return g.IsLive(c.host) })
		if got != c.live {
			t.Errorf("IsLive(%s) = %v", c.host, got)
		}
	}
}

// GatewayRequest is one gateway call for the catcher test in
// modes_test.go (package recognize_test, which can import catcher).
type GatewayRequest struct {
	Provider, Method, URL string
	Headers               map[string]string
	Body                  string
	Live                  bool
}

// GatewayRequests exports gatewayPairs.
func GatewayRequests() []GatewayRequest {
	var out []GatewayRequest
	for _, g := range gatewayPairs {
		for _, r := range []Request{g.live, g.sandbox} {
			out = append(out, GatewayRequest{Provider: g.provider, Method: r.Method, URL: r.URL.String(), Headers: r.Headers,
				Body: string(r.Body), Live: r.URL == g.live.URL})
		}
	}
	return out
}
