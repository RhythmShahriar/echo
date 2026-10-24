// Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
// SPDX-License-Identifier: MIT

package recognize

import (
	"encoding/base64"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/RhythmShahriar/echo/model"
)

// Payment APIs.
//
// What a sealed site is answered is decided by the key's mode, because a
// fake success is only safe when the real call couldn't have moved money:
//
//   - Test mode (a Stripe sk_test_/rk_test_ key, PayPal's sandbox hosts):
//     Echo fakes a plausible success object, so checkout flows can be
//     exercised end to end on staging without reaching the provider.
//   - Live mode (sk_live_/rk_live_, PayPal's live hosts), or a key Echo
//     can't read at all: Echo refuses with the provider's own error shape,
//     so the SDK throws and the site records a failed payment. A staging
//     copy of production holding live keys must never see "paid" for an
//     order nobody paid for and must never charge a real card: these
//     are refused in observe mode and for allowlisted hosts too. Refuse
//     reports them so the catcher can mark the message blocked.
//
// Connectors mask keys before reporting them but keep the first eight
// characters ("sk_live_•••••"), which is all the mode check needs. The
// other gateways (Adyen, Braintree, Square, Authorize.net, Mollie,
// Razorpay) follow the same rules in gateways.go.
func init() {
	register(stripe)
	register(paypal)
}

// Assembled so the source holds no key-shaped literals.
var (
	stripeLive = []string{"sk" + "_live_", "rk" + "_live_"}
	stripeTest = []string{"sk" + "_test_", "rk" + "_test_"}
)

// stripeMode returns "live", "test" or "" (unknown) from the credentials:
// Bearer or Basic authorization (the key is the Basic user name).
func stripeMode(r Request) string {
	auth := r.Header("authorization")
	cands := []string{auth}
	if len(auth) > 6 && strings.EqualFold(auth[:6], "basic ") {
		if b, err := base64.StdEncoding.DecodeString(strings.TrimSpace(auth[6:])); err == nil {
			cands = append(cands, string(b))
		}
	}
	for _, c := range cands {
		for _, p := range stripeLive {
			if strings.Contains(c, p) {
				return "live"
			}
		}
		for _, p := range stripeTest {
			if strings.Contains(c, p) {
				return "test"
			}
		}
	}
	return ""
}

// stripeID matches object IDs in paths (pi_3Nx…, cus_P1a…), as opposed to
// resource names (payment_intents), which are lowercase words.
var stripeID = regexp.MustCompile(`^[a-z]+_[A-Za-z0-9_]*[A-Z0-9][A-Za-z0-9_]*$`)

// stripeCall splits /v1/payment_intents/pi_x/confirm into the resource
// ("payment_intents"), whether an ID was given and what's done ("confirm").
func stripeCall(r Request) (resource string, withID bool, action string) {
	s := segments(r)
	if len(s) > 0 && s[0] == "v1" {
		s = s[1:]
	}
	var res, rest []string
	for _, seg := range s {
		switch {
		case withID:
			rest = append(rest, seg)
		case stripeID.MatchString(seg):
			withID = true
		default:
			res = append(res, seg)
		}
	}
	resource = strings.Join(res, ".")
	switch {
	case len(rest) > 0 && r.Method == "POST" && len(rest) == 1:
		action = rest[0]
	case len(rest) > 0:
		action = strings.Join(rest, " ") + " " + verb(r.Method, false)
	default:
		action = verb(r.Method, withID)
	}
	return resource, withID, action
}

// stripePrefixes are the ID prefixes Stripe uses per resource.
var stripePrefixes = map[string]string{
	"payment_intents": "pi", "setup_intents": "seti", "customers": "cus", "charges": "ch", "refunds": "re",
	"payment_methods": "pm", "subscriptions": "sub", "invoices": "in", "prices": "price", "products": "prod",
	"payouts": "po", "transfers": "tr", "events": "evt", "tokens": "tok", "sources": "src", "accounts": "acct",
	"checkout.sessions": "cs_test", "billing_portal.sessions": "bps", "payment_links": "plink",
	"webhook_endpoints": "we", "invoiceitems": "ii", "subscription_items": "si", "coupons": "co",
}

// stripe: api.stripe.com, form-encoded. The event is the resource and
// what was done to it: "payment_intents create", "payment_intents confirm".
var stripe = Recognizer{
	Provider: "stripe",
	Name:     "Stripe",
	Channel:  model.Payment,
	Hosts:    []string{"api.stripe.com"},
	Match:    func(r Request) bool { return host(r, "api.stripe.com") },
	Fill: func(r Request, m *model.Message) {
		if res, _, action := stripeCall(r); res != "" {
			m.Event = res + " " + action // a tunnel (CONNECT) has no path to name
		}
		f := values(r)
		if e := f.Get("receipt_email"); e != "" {
			m.To = []string{e}
		} else if e := f.Get("email"); e != "" {
			m.To = []string{e}
		}
		if a := f.Get("amount"); a != "" {
			m.Text = strings.TrimSpace(a + " " + f.Get("currency"))
		}
		if mode := stripeMode(r); mode != "" {
			m.Subject = mode + " mode"
		}
	},
	Refuse: func(r Request) bool { return stripeMode(r) != "test" },
	Fake: func(r Request, _ *model.Message) Fake {
		if stripeMode(r) != "test" {
			return jsonFake(403, js(map[string]any{"error": map[string]string{
				"type":    "invalid_request_error",
				"code":    "rhythm_echo_sealed",
				"message": "Rhythm Echo refused a live-mode call. Use a Stripe test key (sk_test_…) on staging.",
			}}))
		}
		res, withID, action := stripeCall(r)
		singular := strings.TrimSuffix(res, "s") // payment_intents → payment_intent, checkout.sessions → checkout.session
		if r.Method == "GET" && !withID {
			return jsonFake(200, js(map[string]any{"object": "list", "data": []any{}, "has_more": false, "url": r.URL.Path}))
		}
		prefix := stripePrefixes[res]
		if prefix == "" {
			prefix = "obj"
		}
		id := prefix + "_" + randHex(24)
		if r.Method == "DELETE" {
			return jsonFake(200, js(map[string]any{"id": id, "object": singular, "deleted": true}))
		}
		o := map[string]any{"id": id, "object": singular, "livemode": false, "created": time.Now().Unix(), "metadata": map[string]any{}}
		// Echo simple top-level fields (amount, currency, email…) so the
		// site reads back what it sent; nested fields (metadata[x]) and
		// credentials are left out.
		for k, v := range values(r) {
			if strings.ContainsAny(k, "[]") || k == "key" || strings.Contains(k, "secret") || len(v) == 0 {
				continue
			}
			if n, err := strconv.ParseInt(v[0], 10, 64); err == nil {
				o[k] = n
			} else {
				o[k] = v[0]
			}
		}
		switch res {
		case "payment_intents", "setup_intents":
			o["client_secret"] = id + "_secret_" + randHex(24)
			o["status"] = "requires_payment_method"
			if action == "confirm" || action == "capture" || o["confirm"] == "true" {
				o["status"] = "succeeded"
			}
			if action == "cancel" {
				o["status"] = "canceled"
			}
		case "charges":
			o["status"], o["paid"], o["captured"] = "succeeded", true, true
		case "refunds":
			o["status"] = "succeeded"
		case "checkout.sessions":
			o["status"], o["payment_status"] = "open", "unpaid"
			o["url"] = "https://checkout.stripe.com/c/pay/" + id
		case "subscriptions":
			o["status"] = "active"
		}
		return jsonFake(200, js(o))
	},
}

// paypal: the REST API. Live and sandbox are separate hosts, so the mode
// comes from the host. The event is the path without IDs:
// "checkout.orders create", "checkout.orders capture", "oauth2.token".
var paypal = Recognizer{
	Provider: "paypal",
	Name:     "PayPal",
	Channel:  model.Payment,
	Hosts:    []string{"api-m.paypal.com", "api.paypal.com", "api-m.sandbox.paypal.com", "api.sandbox.paypal.com"},
	Match: func(r Request) bool {
		return host(r, "api-m.paypal.com", "api.paypal.com", "api-m.sandbox.paypal.com", "api.sandbox.paypal.com")
	},
	Fill: func(r Request, m *model.Message) {
		res, _, action := paypalCall(r)
		m.Event = res + " " + action
		m.Subject = "live mode"
		if paypalSandbox(r) {
			m.Subject = "sandbox mode"
		}
		var p map[string]any
		if r.JSON(&p) {
			if units, ok := p["purchase_units"].([]any); ok && len(units) > 0 {
				amt := obj(obj(units[0])["amount"])
				m.Text = strings.TrimSpace(str(amt["value"]) + " " + str(amt["currency_code"]))
			}
			if e := str(dig(p, "payer", "email_address")); e != "" {
				m.To = []string{e}
			}
		}
	},
	Refuse: func(r Request) bool { return !paypalSandbox(r) },
	Fake: func(r Request, _ *model.Message) Fake {
		res, withID, action := paypalCall(r)
		if !paypalSandbox(r) {
			if res == "oauth2.token" {
				return jsonFake(401, js(map[string]string{"error": "invalid_client",
					"error_description": "Rhythm Echo refused a live PayPal call. Use sandbox credentials on staging."}))
			}
			return jsonFake(403, js(map[string]any{"name": "NOT_AUTHORIZED", "message": "Authorization failed due to insufficient permissions.",
				"debug_id": randHex(13), "details": []map[string]string{{"issue": "PERMISSION_DENIED",
					"description": "Rhythm Echo refused a live PayPal call. Use the sandbox on staging."}},
				"links": []any{}}))
		}
		id := strings.ToUpper(randHex(17))
		switch {
		case res == "oauth2.token":
			return jsonFake(200, js(map[string]any{"scope": "https://uri.paypal.com/services/payments/payment",
				"access_token": "A21AA" + randHex(40), "token_type": "Bearer", "app_id": "APP-80W284485P519543T",
				"expires_in": 32400, "nonce": now() + randHex(20)}))
		case res == "checkout.orders" && action == "create":
			return jsonFake(201, js(map[string]any{"id": id, "status": "CREATED", "links": []map[string]string{
				{"href": "https://api-m.sandbox.paypal.com/v2/checkout/orders/" + id, "rel": "self", "method": "GET"},
				{"href": "https://www.sandbox.paypal.com/checkoutnow?token=" + id, "rel": "approve", "method": "GET"},
				{"href": "https://api-m.sandbox.paypal.com/v2/checkout/orders/" + id + "/capture", "rel": "capture", "method": "POST"},
			}}))
		case action == "capture" || action == "authorize":
			kind := "captures"
			if action == "authorize" {
				kind = "authorizations"
			}
			return jsonFake(201, js(map[string]any{"id": paypalID(r), "status": "COMPLETED", "purchase_units": []any{
				map[string]any{"payments": map[string]any{kind: []any{map[string]any{"id": id, "status": "COMPLETED"}}}}}}))
		case r.Method == "PATCH":
			return Fake{Status: 204}
		case r.Method == "GET" && withID:
			return jsonFake(200, js(map[string]any{"id": paypalID(r), "status": "APPROVED"}))
		}
		return jsonFake(200, js(map[string]any{"id": id, "status": "COMPLETED"}))
	},
}

func paypalSandbox(r Request) bool {
	return strings.Contains(strings.ToLower(r.URL.Hostname()), ".sandbox.")
}

// paypalIDRe matches PayPal object IDs: upper-case letters and digits.
var paypalIDRe = regexp.MustCompile(`^[A-Z0-9-]{10,}$`)

func paypalCall(r Request) (resource string, withID bool, action string) {
	s := segments(r)
	if len(s) > 0 && (s[0] == "v1" || s[0] == "v2") {
		s = s[1:]
	}
	var res, rest []string
	for _, seg := range s {
		switch {
		case withID:
			rest = append(rest, seg)
		case paypalIDRe.MatchString(seg):
			withID = true
		default:
			res = append(res, seg)
		}
	}
	resource = strings.Join(res, ".")
	if len(rest) > 0 {
		return resource, withID, strings.Join(rest, " ")
	}
	if resource == "oauth2.token" {
		return resource, withID, "create"
	}
	return resource, withID, verb(r.Method, withID)
}

func paypalID(r Request) string {
	for _, seg := range segments(r) {
		if paypalIDRe.MatchString(seg) {
			return seg
		}
	}
	return strings.ToUpper(randHex(17))
}
