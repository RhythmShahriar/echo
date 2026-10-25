// Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
// SPDX-License-Identifier: MIT

package recognize

import (
	"encoding/base64"
	"net/url"
	"regexp"
	"slices"
	"strings"

	"github.com/RhythmShahriar/echo/model"
)

// Payment gateways beyond Stripe and PayPal. Each one is told test from
// live the way the gateway itself does it:
//
//   - by host, where sandbox and live are separate hosts (Adyen,
//     Braintree, Square, Authorize.net): a call to a live host is refused;
//   - by key, where one host serves both (Mollie, Razorpay): a call is
//     refused unless its key is a test key. A key Echo can't read counts
//     as live, as does a tunnel, which carries no key at all.
//
// A refused call is answered with the gateway's own error shape, so the
// SDK throws and the site records a failed payment, in every mode and for
// allowlisted hosts too (see payment.go). Sandbox calls are recognized
// and, when sealed, faked with a plausible success.
func init() {
	for _, g := range gateways {
		register(g.recognizer())
	}
}

// Gateway describes one payment API whose live calls Echo always refuses.
type Gateway struct {
	Provider string // slug ("adyen")
	Name     string // display name ("Adyen")
	// By is how test and live calls are told apart: "host" (separate
	// sandbox hosts) or "key" (the API key's prefix, on one host).
	By string
	// Live and Sandbox are host patterns (as in Recognizer.Hosts). For a
	// gateway told by key both list the same hosts.
	Live, Sandbox []string
}

// IsLive reports whether a call to host is live whatever its key: a
// host-decided gateway's live host. A host-decided gateway's sandbox
// host and a key-decided gateway's shared host are not.
func (g Gateway) IsLive(host string) bool {
	u := &url.URL{Host: host}
	return g.By == "host" && slices.ContainsFunc(g.Live, func(p string) bool { return hostCovered(p, u) })
}

// PaymentGateways lists every payment API Echo recognizes, with how it
// tells test from live, for `rhythm echo doctor` and the docs.
func PaymentGateways() []Gateway {
	out := []Gateway{
		{Provider: "stripe", Name: "Stripe", By: "key", Live: stripe.Hosts, Sandbox: stripe.Hosts},
		{Provider: "paypal", Name: "PayPal", By: "host", Live: []string{"api-m.paypal.com", "api.paypal.com"},
			Sandbox: []string{"api-m.sandbox.paypal.com", "api.sandbox.paypal.com"}},
	}
	for _, g := range gateways {
		out = append(out, g.Gateway)
	}
	return out
}

// gateway is a Gateway with the code that reads its calls.
type gateway struct {
	Gateway
	test   func(r Request) bool              // a test-mode call (for By "key"; By "host" uses Sandbox)
	fill   func(r Request, m *model.Message) // recipients and amount
	refuse func(r Request) Fake              // the gateway's own refusal
	fake   func(r Request) Fake              // a sandbox success
}

func (g gateway) covered(r Request, hosts []string) bool {
	return slices.ContainsFunc(hosts, func(p string) bool { return hostCovered(p, r.URL) })
}

// isTest reports whether r is a test-mode call.
func (g gateway) isTest(r Request) bool {
	if g.By == "host" {
		return g.covered(r, g.Sandbox)
	}
	return g.test(r)
}

// recognizer turns g into a Recognizer that claims its live and sandbox
// hosts. Refuse and Fake both ask isTest, so a call is faked only when
// the real one could not have moved money; anything else gets the
// gateway's own refusal.
func (g gateway) recognizer() Recognizer {
	hosts := slices.Clone(g.Live)
	for _, h := range g.Sandbox {
		if !slices.Contains(hosts, h) {
			hosts = append(hosts, h)
		}
	}
	return Recognizer{
		Provider: g.Provider,
		Name:     g.Name,
		Channel:  model.Payment,
		Hosts:    hosts,
		Match:    func(r Request) bool { return g.covered(r, hosts) },
		Fill: func(r Request, m *model.Message) {
			if s := segments(r); len(s) > 0 {
				m.Event = gatewayEvent(r) // a tunnel (CONNECT) has no path to name
			}
			switch {
			case !g.isTest(r):
				m.Subject = "live mode"
			case g.By == "host":
				m.Subject = "sandbox mode"
			default:
				m.Subject = "test mode"
			}
			if g.fill != nil {
				g.fill(r, m)
			}
		},
		Refuse: func(r Request) bool { return !g.isTest(r) },
		Fake: func(r Request, _ *model.Message) Fake {
			if !g.isTest(r) {
				return g.refuse(r)
			}
			return g.fake(r)
		},
	}
}

// refusal is the sentence every refusal carries.
func refusal(name, fix string) string {
	return "Rhythm Echo refused a live " + name + " call. " + fix
}

var (
	versionSeg = regexp.MustCompile(`^v\d+(\.\d+)?$`)
	digit      = regexp.MustCompile(`\d`)
)

// gatewayEvent names a REST call by its path without the version and
// IDs: "payments create", "orders.capture create".
func gatewayEvent(r Request) string {
	s := segments(r)
	if len(s) > 0 && versionSeg.MatchString(s[0]) {
		s = s[1:]
	}
	if len(s) > 0 && s[0] == "api" {
		s = s[1:]
	}
	isID := func(seg string) bool { return len(seg) >= 6 && digit.MatchString(seg) || numeric.MatchString(seg) }
	return restEvent(s, isID, r.Method)
}

// lastSegment returns the last path segment of r, or "".
func lastSegment(r Request) string {
	s := segments(r)
	if len(s) == 0 {
		return ""
	}
	return s[len(s)-1]
}

// amountOf reads "4000 EUR" from a JSON body's amount object or fields.
func amountOf(p map[string]any, amount, value, currency string) string {
	a := p[amount]
	if o, ok := a.(map[string]any); ok {
		return strings.TrimSpace(str(o[value]) + " " + str(o[currency]))
	}
	if s := str(a); s != "" {
		return strings.TrimSpace(s + " " + str(p[currency]))
	}
	return ""
}

// basicUser returns the user name of a Basic authorization header.
func basicUser(r Request) string {
	auth := r.Header("authorization")
	if len(auth) <= 6 || !strings.EqualFold(auth[:6], "basic ") {
		return ""
	}
	b, err := base64.StdEncoding.DecodeString(strings.TrimSpace(auth[6:]))
	if err != nil {
		return ""
	}
	user, _, _ := strings.Cut(string(b), ":")
	return user
}

// bearer returns the token of a Bearer authorization header.
func bearer(r Request) string {
	auth := r.Header("authorization")
	if len(auth) <= 7 || !strings.EqualFold(auth[:7], "bearer ") {
		return ""
	}
	return strings.TrimSpace(auth[7:])
}

var gateways = []gateway{adyen, braintree, square, authorizeNet, mollie, razorpay}

// adyen: Checkout, Classic (pal) and the management APIs. Test endpoints
// are {service}-test.adyen.com; live ones carry the merchant's prefix,
// {prefix}-{service}-live.adyenpayments.com, or {service}-live.adyen.com.
var adyen = gateway{
	Gateway: Gateway{Provider: "adyen", Name: "Adyen", By: "host",
		Live:    []string{"*-live.adyenpayments.com", "*-live.adyen.com"},
		Sandbox: []string{"*-test.adyen.com"}},
	fill: func(r Request, m *model.Message) {
		var p map[string]any
		if r.JSON(&p) {
			m.Text = amountOf(p, "amount", "value", "currency")
			if e := str(p["shopperEmail"]); e != "" {
				m.To = []string{e}
			}
		}
	},
	refuse: func(Request) Fake {
		return jsonFake(403, js(map[string]any{"status": 403, "errorCode": "010", "errorType": "security",
			"message": refusal("Adyen", "Use the test endpoints (*-test.adyen.com) on staging."), "pspReference": strings.ToUpper(randHex(16))}))
	},
	fake: func(r Request) Fake {
		psp := strings.ToUpper(randHex(16))
		var p map[string]any
		r.JSON(&p)
		o := map[string]any{"pspReference": psp, "merchantReference": str(p["reference"])}
		switch lastSegment(r) {
		case "sessions":
			o = map[string]any{"id": "CS" + strings.ToUpper(randHex(14)), "sessionData": randHex(32), "reference": str(p["reference"]),
				"amount": p["amount"], "expiresAt": now()}
		case "payments", "authorise", "details": //nolint:misspell // Adyen Classic spells its endpoint so
			o["resultCode"] = "Authorised"
		default:
			o["status"] = "received"
		}
		return jsonFake(200, js(o))
	},
}

// braintree: the XML gateway API ({host}/merchants/{id}/…) and the
// GraphQL API. Sandbox has its own hosts.
var braintree = gateway{
	Gateway: Gateway{Provider: "braintree", Name: "Braintree", By: "host",
		Live:    []string{"api.braintreegateway.com", "payments.braintree-api.com"},
		Sandbox: []string{"api.sandbox.braintreegateway.com", "payments.sandbox.braintree-api.com"}},
	fill: func(r Request, m *model.Message) {
		if a := xmlField(string(r.Body), "amount"); a != "" {
			m.Text = a
		}
		if e := xmlField(string(r.Body), "email"); e != "" {
			m.To = []string{e}
		}
	},
	refuse: func(r Request) Fake {
		msg := refusal("Braintree", "Use the sandbox (api.sandbox.braintreegateway.com) on staging.")
		if strings.Contains(r.URL.Hostname(), "braintree-api.com") {
			return jsonFake(403, js(map[string]any{"errors": []any{map[string]any{"message": msg,
				"extensions": map[string]string{"errorClass": "AUTHORIZATION"}}}}))
		}
		return Fake{Status: 403, Headers: map[string]string{"content-type": "application/xml"},
			Body: `<?xml version="1.0" encoding="UTF-8"?><api-error-response><errors><errors type="array"/></errors><message>` + msg + `</message></api-error-response>`}
	},
	fake: func(r Request) Fake {
		if strings.Contains(r.URL.Hostname(), "braintree-api.com") {
			return jsonFake(200, js(map[string]any{"data": map[string]any{}, "extensions": map[string]string{"requestId": uuid()}}))
		}
		body := `<success type="boolean">true</success>`
		switch lastSegment(r) {
		case "transactions":
			body = `<transaction><id>` + randHex(8) + `</id><status>authorized</status><type>sale</type><amount>` +
				xmlField(string(r.Body), "amount") + `</amount></transaction>`
		case "client_token":
			body = `<client-token><value>` + base64.StdEncoding.EncodeToString([]byte(`{"version":2,"environment":"sandbox"}`)) + `</value></client-token>`
		}
		return Fake{Status: 201, Headers: map[string]string{"content-type": "application/xml"}, Body: `<?xml version="1.0" encoding="UTF-8"?>` + body}
	},
}

// xmlField returns the text of the first <name>…</name> in an XML body.
func xmlField(body, name string) string {
	_, rest, ok := strings.Cut(body, "<"+name+">")
	if !ok {
		return ""
	}
	v, _, _ := strings.Cut(rest, "</"+name+">")
	return strings.TrimSpace(v)
}

// square: the Square API (/v2/payments, /v2/orders…). Sandbox is
// connect.squareupsandbox.com.
var square = gateway{
	Gateway: Gateway{Provider: "square", Name: "Square", By: "host",
		Live: []string{"connect.squareup.com"}, Sandbox: []string{"connect.squareupsandbox.com"}},
	fill: func(r Request, m *model.Message) {
		var p map[string]any
		if r.JSON(&p) {
			m.Text = amountOf(p, "amount_money", "amount", "currency")
			if e := str(p["buyer_email_address"]); e != "" {
				m.To = []string{e}
			}
		}
	},
	refuse: func(Request) Fake {
		return jsonFake(403, js(map[string]any{"errors": []any{map[string]string{"category": "AUTHENTICATION_ERROR", "code": "FORBIDDEN",
			"detail": refusal("Square", "Use the sandbox (connect.squareupsandbox.com) on staging.")}}}))
	},
	fake: func(r Request) Fake {
		var p map[string]any
		r.JSON(&p)
		s := segments(r)
		switch {
		case r.Method == "POST" && len(s) == 2 && s[1] == "payments":
			return jsonFake(200, js(map[string]any{"payment": map[string]any{"id": strings.ToUpper(randHex(24)), "status": "COMPLETED",
				"amount_money": p["amount_money"], "created_at": now(), "receipt_url": "https://squareupsandbox.com/receipt/preview/" + randHex(24)}}))
		case r.Method == "POST" && len(s) == 2 && s[1] == "orders":
			return jsonFake(200, js(map[string]any{"order": map[string]any{"id": strings.ToUpper(randHex(24)), "state": "OPEN", "created_at": now()}}))
		}
		return jsonFake(200, `{}`)
	},
}

// authorizeNet: the JSON and XML API, one endpoint for every request
// type (createTransactionRequest…). Sandbox is apitest.authorize.net.
var authorizeNet = gateway{
	Gateway: Gateway{Provider: "authorize-net", Name: "Authorize.net", By: "host",
		Live: []string{"api.authorize.net", "api2.authorize.net"}, Sandbox: []string{"apitest.authorize.net"}},
	fill: func(r Request, m *model.Message) {
		var p map[string]any
		if !r.JSON(&p) {
			return
		}
		for k, v := range p { // {"createTransactionRequest": {...}}
			m.Event = strings.TrimSuffix(k, "Request")
			t := obj(dig(v, "transactionRequest"))
			m.Text = str(t["amount"])
			if e := str(dig(t, "customer", "email")); e != "" {
				m.To = []string{e}
			}
		}
	},
	refuse: func(r Request) Fake {
		msg := refusal("Authorize.net", "Use the sandbox (apitest.authorize.net) on staging.")
		if !isJSON(r) {
			return Fake{Status: 403, Headers: map[string]string{"content-type": "text/xml"},
				Body: `<?xml version="1.0" encoding="utf-8"?><ErrorResponse xmlns="AnetApi/xml/v1/schema/AnetApiSchema.xsd"><messages><resultCode>Error</resultCode><message><code>E00007</code><text>` +
					msg + `</text></message></messages></ErrorResponse>`}
		}
		return jsonFake(403, js(map[string]any{"messages": map[string]any{"resultCode": "Error",
			"message": []any{map[string]string{"code": "E00007", "text": msg}}}}))
	},
	fake: func(r Request) Fake {
		o := map[string]any{"messages": map[string]any{"resultCode": "Ok", "message": []any{map[string]string{"code": "I00001", "text": "Successful."}}}}
		if strings.Contains(string(r.Body), "createTransactionRequest") {
			o["transactionResponse"] = map[string]any{"responseCode": "1", "authCode": "ECHO01", "transId": randDigits(11),
				"messages": []any{map[string]string{"code": "1", "description": "This transaction has been approved."}}}
		}
		return jsonFake(200, js(o))
	},
}

// mollie: api.mollie.com serves test and live; the key says which
// (test_… or live_…). An OAuth access token (access_…) is in test mode
// only when the call says "testmode": true.
var mollie = gateway{
	Gateway: Gateway{Provider: "mollie", Name: "Mollie", By: "key", Live: []string{"api.mollie.com"}, Sandbox: []string{"api.mollie.com"}},
	test: func(r Request) bool {
		key := bearer(r)
		switch {
		case strings.HasPrefix(key, "test_"):
			return true
		case strings.HasPrefix(key, "access_"):
			var p map[string]any
			return r.JSON(&p) && p["testmode"] == true || r.URL.Query().Get("testmode") == "true"
		}
		return false
	},
	fill: func(r Request, m *model.Message) {
		var p map[string]any
		if r.JSON(&p) {
			m.Text = amountOf(p, "amount", "value", "currency")
			if e := str(p["billingEmail"]); e != "" {
				m.To = []string{e}
			}
		}
	},
	refuse: func(Request) Fake {
		f := jsonFake(403, js(map[string]any{"status": 403, "title": "Forbidden",
			"detail": refusal("Mollie", "Use a test API key (test_…) on staging."),
			"_links": map[string]any{"documentation": map[string]string{"href": "https://docs.mollie.com/overview/authentication", "type": "text/html"}}}))
		f.Headers["content-type"] = "application/hal+json"
		return f
	},
	fake: func(r Request) Fake {
		var p map[string]any
		r.JSON(&p)
		s := segments(r)
		res := "payment"
		if len(s) > 1 {
			res = strings.TrimSuffix(s[1], "s")
		}
		prefix := map[string]string{"payment": "tr", "order": "ord", "customer": "cst", "refund": "re", "subscription": "sub"}[res]
		if prefix == "" {
			prefix = res[:min(3, len(res))]
		}
		id := prefix + "_" + randHex(10)
		o := map[string]any{"resource": res, "id": id, "mode": "test", "createdAt": now(), "amount": p["amount"],
			"description": p["description"], "metadata": p["metadata"], "_links": map[string]any{}}
		status := 200
		if r.Method == "POST" {
			status = 201
		}
		if res == "payment" || res == "order" {
			o["status"] = "open"
			o["_links"] = map[string]any{"checkout": map[string]string{"href": "https://www.mollie.com/checkout/select-method/" + randHex(10), "type": "text/html"}}
		}
		f := jsonFake(status, js(o))
		f.Headers["content-type"] = "application/hal+json"
		return f
	},
}

// razorpay: api.razorpay.com serves test and live; the key ID (the Basic
// user name) says which: rzp_test_… or rzp_live_….
var razorpay = gateway{
	Gateway: Gateway{Provider: "razorpay", Name: "Razorpay", By: "key", Live: []string{"api.razorpay.com"}, Sandbox: []string{"api.razorpay.com"}},
	test:    func(r Request) bool { return strings.HasPrefix(basicUser(r), "rzp"+"_test_") },
	fill: func(r Request, m *model.Message) {
		var p map[string]any
		if r.JSON(&p) {
			m.Text = strings.TrimSpace(str(p["amount"]) + " " + str(p["currency"]))
			if e := str(dig(p, "customer", "email")); e != "" {
				m.To = []string{e}
			} else if e := str(p["email"]); e != "" {
				m.To = []string{e}
			}
		}
	},
	refuse: func(Request) Fake {
		return jsonFake(401, js(map[string]any{"error": map[string]any{"code": "BAD_REQUEST_ERROR",
			"description": refusal("Razorpay", "Use a test key (rzp_test_…) on staging."), "source": "NA", "step": "NA", "reason": "NA", "metadata": map[string]any{}}}))
	},
	fake: func(r Request) Fake {
		var p map[string]any
		r.JSON(&p)
		s := segments(r)
		entity := "order"
		if len(s) > 1 {
			entity = strings.TrimSuffix(s[1], "s")
		}
		o := map[string]any{"id": entity + "_" + randHex(14), "entity": entity, "amount": p["amount"], "currency": p["currency"],
			"receipt": p["receipt"], "status": "created", "created_at": 0, "notes": map[string]any{}}
		if len(s) > 1 && s[len(s)-1] == "capture" {
			o["entity"], o["id"], o["status"] = "payment", s[len(s)-2], "captured"
		}
		return jsonFake(200, js(o))
	},
}
