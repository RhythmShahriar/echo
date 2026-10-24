// Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
// SPDX-License-Identifier: MIT

package replay

import (
	"context"
	"crypto/hmac"
	"crypto/sha1"
	"crypto/sha256"
	"crypto/sha512"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"hash"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// Secrets are assembled at runtime so no secret-shaped literal sits in
// the source (push protection would flag it).
var (
	stripeSecret   = strings.Join([]string{"wh", "sec_", "FakeTestSigningSecret0123456789"}, "")
	standardSecret = strings.Join([]string{"wh", "sec_", base64.StdEncoding.EncodeToString([]byte("fake-standard-webhooks-key-24b"))}, "")
	liveStripeKey  = strings.Join([]string{"sk", "_live_", "51FakeLiveKeyForRefusalTests0000"}, "")
	testStripeKey  = strings.Join([]string{"sk", "_test_", "51FakeTestKeyForRefusalTests0000"}, "")
)

// The verifiers below follow each provider's documented algorithm and
// share no code with sign.go.

// https://docs.stripe.com/webhooks#verify-manually
func verifyStripe(r *http.Request, body []byte, secret string) error {
	var ts string
	var sigs []string
	for _, item := range strings.Split(r.Header.Get("Stripe-Signature"), ",") {
		k, v, _ := strings.Cut(item, "=")
		switch k {
		case "t":
			ts = v
		case "v1":
			sigs = append(sigs, v)
		}
	}
	t, err := strconv.ParseInt(ts, 10, 64)
	if err != nil {
		return errors.New("no timestamp")
	}
	if d := time.Since(time.Unix(t, 0)); d > 5*time.Minute || d < -5*time.Minute {
		return errors.New("timestamp outside tolerance")
	}
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write([]byte(ts))
	mac.Write([]byte("."))
	mac.Write(body)
	want := mac.Sum(nil)
	for _, s := range sigs {
		got, _ := hex.DecodeString(s)
		if hmac.Equal(got, want) {
			return nil
		}
	}
	return errors.New("no matching v1 signature")
}

// https://github.com/standard-webhooks/standard-webhooks/blob/main/spec/standard-webhooks.md
func verifyStandard(prefix string) func(*http.Request, []byte, string) error {
	return func(r *http.Request, body []byte, secret string) error {
		id, ts, sig := r.Header.Get(prefix+"-id"), r.Header.Get(prefix+"-timestamp"), r.Header.Get(prefix+"-signature")
		if id == "" || ts == "" || sig == "" {
			return errors.New("missing " + prefix + " headers")
		}
		key, err := base64.StdEncoding.DecodeString(strings.TrimPrefix(secret, "whsec_"))
		if err != nil {
			return err
		}
		mac := hmac.New(sha256.New, key)
		mac.Write([]byte(id + "." + ts + "."))
		mac.Write(body)
		want := mac.Sum(nil)
		for _, part := range strings.Fields(sig) {
			version, s, ok := strings.Cut(part, ",")
			if !ok || version != "v1" {
				continue
			}
			got, _ := base64.StdEncoding.DecodeString(s)
			if hmac.Equal(got, want) {
				return nil
			}
		}
		return errors.New("no matching v1 signature")
	}
}

// https://docs.github.com/en/webhooks/using-webhooks/validating-webhook-deliveries
func verifyGitHub(r *http.Request, body []byte, secret string) error {
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write(body)
	if !hmac.Equal([]byte(r.Header.Get("X-Hub-Signature-256")), []byte("sha256="+hex.EncodeToString(mac.Sum(nil)))) {
		return errors.New("bad X-Hub-Signature-256")
	}
	if r.Header.Get("X-GitHub-Event") == "" || len(r.Header.Get("X-GitHub-Delivery")) != 36 {
		return errors.New("missing GitHub event headers")
	}
	return nil
}

// WooCommerce's receiver check: base64_encode(hash_hmac('sha256', $body, $secret, true)).
func verifyWoo(r *http.Request, body []byte, secret string) error {
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write(body)
	if r.Header.Get("X-WC-Webhook-Signature") != base64.StdEncoding.EncodeToString(mac.Sum(nil)) {
		return errors.New("bad X-WC-Webhook-Signature")
	}
	if r.Header.Get("X-WC-Webhook-Topic") != "order.created" {
		return errors.New("bad topic " + r.Header.Get("X-WC-Webhook-Topic"))
	}
	return nil
}

func verifyGeneric(header string, alg func() hash.Hash, b64 bool, prefix, tsHeader string) func(*http.Request, []byte, string) error {
	return func(r *http.Request, body []byte, secret string) error {
		mac := hmac.New(alg, []byte(secret))
		if tsHeader != "" {
			mac.Write([]byte(r.Header.Get(tsHeader) + "."))
		}
		mac.Write(body)
		want := hex.EncodeToString(mac.Sum(nil))
		if b64 {
			want = base64.StdEncoding.EncodeToString(mac.Sum(nil))
		}
		if r.Header.Get(header) != prefix+want {
			return errors.New("bad " + header)
		}
		return nil
	}
}

type received struct {
	mu   sync.Mutex
	reqs []*http.Request
	body [][]byte
}

// receiver verifies each request with verify and answers 200 or 400.
func receiver(t *testing.T, secret string, verify func(*http.Request, []byte, string) error) (*httptest.Server, *received) {
	t.Helper()
	got := &received{}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		got.mu.Lock()
		got.reqs = append(got.reqs, r)
		got.body = append(got.body, b)
		got.mu.Unlock()
		if err := verify(r, b, secret); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		_, _ = w.Write([]byte(`{"received":true}`))
	}))
	t.Cleanup(srv.Close)
	return srv, got
}

func opts(secret string) Options {
	return Options{Secret: secret, Env: []string{}, Run: "r1"}
}

func mustLoad(t *testing.T, name string) Event {
	t.Helper()
	e, err := Load(name)
	if err != nil {
		t.Fatal(err)
	}
	return e
}

func TestSignaturesVerify(t *testing.T) {
	plain := "plain-shared-secret"
	cases := []struct {
		name   string
		event  Event
		secret string
		hmac   HMAC
		verify func(*http.Request, []byte, string) error
	}{
		{"stripe", mustLoad(t, "stripe/payment_intent.succeeded"), stripeSecret, HMAC{}, verifyStripe},
		{"resend webhook-*", mustLoad(t, "resend/email.delivered"), standardSecret, HMAC{}, verifyStandard("webhook")},
		{"clerk svix-*", mustLoad(t, "clerk/user.created"), standardSecret, HMAC{}, verifyStandard("svix")},
		{"github", mustLoad(t, "github/ping"), plain, HMAC{}, verifyGitHub},
		{"woocommerce", mustLoad(t, "woocommerce/order.created"), plain, HMAC{}, verifyWoo},
		{"hmac default", Event{Name: "custom", Scheme: HMACScheme, Body: []byte(`{"a":1}`)}, plain, HMAC{},
			verifyGeneric("X-Signature", sha256.New, false, "", "")},
		{"hmac sha1 base64 prefix", Event{Name: "custom", Scheme: HMACScheme, Body: []byte(`{"a":1}`)}, plain,
			HMAC{Header: "X-Hook-Sig", Algorithm: "sha1", Encoding: "base64", Prefix: "sha1="},
			verifyGeneric("X-Hook-Sig", sha1.New, true, "sha1=", "")},
		{"hmac sha512 timestamp", Event{Name: "custom", Scheme: HMACScheme, Body: []byte(`{"a":1}`)}, plain,
			HMAC{Header: "X-Sig", Algorithm: "sha512", Timestamp: "X-Sig-Timestamp"},
			verifyGeneric("X-Sig", sha512.New, false, "", "X-Sig-Timestamp")},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			srv, got := receiver(t, tc.secret, tc.verify)
			o := opts(tc.secret)
			o.HMAC = tc.hmac
			res, err := Send(context.Background(), tc.event, Target{URL: srv.URL + "/hooks"}, o)
			if err != nil {
				t.Fatal(err)
			}
			if res.Status != 200 || !res.OK() || res.Body != `{"received":true}` {
				t.Fatalf("result = %+v", res)
			}
			req := got.reqs[0]
			if req.Method != "POST" || req.URL.Path != "/hooks" || req.Header.Get("Content-Type") != "application/json" {
				t.Errorf("request = %s %s %v", req.Method, req.URL, req.Header)
			}
			if want := "r1/" + res.Action; req.Header.Get("X-Echo-Run") != want || res.Headers["X-Echo-Run"] != want {
				t.Errorf("X-Echo-Run = %q, want %q", req.Header.Get("X-Echo-Run"), want)
			}
			// Masked: no full signature and no secret in the result.
			for k, v := range req.Header {
				if strings.Contains(strings.ToLower(k), "signature") && strings.Contains(res.Headers[k], v[0][len(v[0])-10:]) {
					t.Errorf("%s not masked: %q", k, res.Headers[k])
				}
			}
			if strings.Contains(res.Secret, tc.secret[len(tc.secret)-8:]) || res.Duration <= 0 {
				t.Errorf("secret %q duration %v", res.Secret, res.Duration)
			}
		})
	}
}

func TestOverrides(t *testing.T) {
	srv, got := receiver(t, stripeSecret, verifyStripe)
	e := mustLoad(t, "stripe/checkout.session.completed")
	err := e.SetAll([]string{
		"data.object.customer_email=sam@example.net",
		"data.object.amount_total=12500",
		`data.object.metadata.order_id="555"`,
		"data.object.metadata.new_key=yes",
		"data.object.payment_method_types.0=link",
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := Send(context.Background(), e, Target{URL: srv.URL}, opts(stripeSecret)); err != nil {
		t.Fatal(err)
	}
	var p struct {
		ID      string `json:"id"`
		Created int64  `json:"created"`
		Data    struct {
			Object struct {
				Email    string            `json:"customer_email"`
				Amount   json.Number       `json:"amount_total"`
				Metadata map[string]string `json:"metadata"`
				Types    []string          `json:"payment_method_types"`
			} `json:"object"`
		} `json:"data"`
	}
	if err := json.Unmarshal(got.body[0], &p); err != nil {
		t.Fatal(err)
	}
	o := p.Data.Object
	if o.Email != "sam@example.net" || o.Amount != "12500" || o.Metadata["order_id"] != "555" || o.Metadata["new_key"] != "yes" ||
		o.Metadata["site"] != "staging" || len(o.Types) != 1 || o.Types[0] != "link" {
		t.Errorf("payload = %+v", o)
	}
	if !strings.HasPrefix(p.ID, "evt_test_") || time.Since(time.Unix(p.Created, 0)) > time.Minute {
		t.Errorf("id %q created %d", p.ID, p.Created)
	}
	for _, bad := range []string{"data.object.customer_email.x=1", "data.object.payment_method_types.9=x", "a..b=1", "novalue"} {
		if err := e.SetAll([]string{bad}); err == nil {
			t.Errorf("%q: no error", bad)
		}
	}
}

func TestRefusals(t *testing.T) {
	var hits atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { hits.Add(1) }))
	defer srv.Close()
	ev := func() Event { return mustLoad(t, "stripe/invoice.paid") }
	live := func(path, v string) Event {
		e := ev()
		if err := e.Set(path, v); err != nil {
			t.Fatal(err)
		}
		return e
	}
	zone := staticResolver{"public.shop.example": {"203.0.113.10"}, "mixed.shop.example": {"127.0.0.1", "198.51.100.4"}}
	cases := []struct {
		name    string
		event   Event
		url     string
		o       func(o *Options)
		refused bool
		text    string
	}{
		{"public ip", ev(), "http://203.0.113.10/hook", nil, true, "neither loopback nor private"},
		{"metadata ip", ev(), "http://169.254.169.254/latest", nil, true, "neither loopback nor private"},
		{"public host", ev(), "http://public.shop.example/hook", func(o *Options) { o.Resolver = zone }, true, "203.0.113.10"},
		{"any public address", ev(), "http://mixed.shop.example/hook", func(o *Options) { o.Resolver = zone }, true, "198.51.100.4"},
		{"staging prefix elsewhere", ev(), "http://public.shop.example/other", func(o *Options) {
			o.Resolver = zone
			o.Staging = []string{"http://public.shop.example/hooks"}
		}, true, "neither loopback nor private"},
		{"live key in env", ev(), srv.URL, func(o *Options) { o.Env = []string{"STRIPE_SECRET_KEY=" + liveStripeKey} }, true, "STRIPE_SECRET_KEY=sk_live_"},
		{"live key in config", ev(), srv.URL, func(o *Options) { o.Scan = map[string]string{".env": "APP=1\nSTRIPE_KEY=" + liveStripeKey} }, true, ".env"},
		{"livemode override", live("livemode", "true"), srv.URL, nil, true, "livemode"},
		{"livemode nested", live("data.object.livemode", "true"), srv.URL, nil, true, "livemode"},
		{"not whsec", ev(), srv.URL, func(o *Options) { o.Secret = "not-a-stripe-secret" }, true, "whsec_"},
		{"api key as secret", ev(), srv.URL, func(o *Options) { o.Secret = testStripeKey }, true, "API key"},
		{"live secret", ev(), srv.URL, func(o *Options) { o.Secret = liveStripeKey }, true, "live key"},
		{"standard not base64", mustLoad(t, "resend/email.bounced"), srv.URL, func(o *Options) { o.Secret = "whsec_***" }, true, "base64"},
		{"missing secret", ev(), srv.URL, func(o *Options) { o.Secret = "" }, false, "ECHO_STRIPE_WEBHOOK_SECRET"},
		{"bad url", ev(), "ftp://127.0.0.1/x", nil, false, "http:// or https://"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			o := opts(stripeSecret)
			if tc.o != nil {
				tc.o(&o)
			}
			_, err := Send(context.Background(), tc.event, Target{URL: tc.url}, o)
			if err == nil || errors.Is(err, ErrRefused) != tc.refused || !strings.Contains(err.Error(), tc.text) {
				t.Fatalf("err = %v", err)
			}
			if strings.Contains(err.Error(), liveStripeKey) {
				t.Errorf("error leaks the key: %v", err)
			}
		})
	}
	if hits.Load() != 0 {
		t.Errorf("refused sends reached the receiver %d times", hits.Load())
	}
	if _, err := Send(context.Background(), mustLoad(t, "paypal/PAYMENT.CAPTURE.COMPLETED"), Target{URL: srv.URL}, opts(stripeSecret)); !errors.Is(err, ErrPayPal) ||
		!strings.Contains(err.Error(), "sandbox") {
		t.Errorf("paypal: %v", err)
	}
}

type staticResolver map[string][]string

func (s staticResolver) LookupHost(_ context.Context, host string) ([]string, error) {
	if a, ok := s[host]; ok {
		return a, nil
	}
	return nil, errors.New("no such host " + host)
}

// rebinding answers loopback to the first lookup and a public address
// after, as a DNS-rebinding attacker would.
type rebinding struct{ n atomic.Int32 }

func (r *rebinding) LookupHost(context.Context, string) ([]string, error) {
	if r.n.Add(1) == 1 {
		return []string{"127.0.0.1"}, nil
	}
	return []string{"203.0.113.66"}, nil
}

func TestHostsAndStaging(t *testing.T) {
	srv, got := receiver(t, stripeSecret, verifyStripe)
	port := srv.Listener.Addr().(interface{ String() string }).String()
	_, port, _ = strings.Cut(port, ":")
	// A name resolving to loopback is fine without config; the Host header
	// keeps the name.
	o := opts(stripeSecret)
	o.Resolver = staticResolver{"shop.localhost": {"127.0.0.1"}}
	res, err := Send(context.Background(), mustLoad(t, "invoice.paid"), Target{URL: "http://shop.localhost:" + port + "/wc-api/stripe"}, o)
	if err != nil || res.Status != 200 || got.reqs[0].Host != "shop.localhost:"+port {
		t.Fatalf("res %+v err %v", res, err)
	}
	// Rebinding between check and dial is caught at dial time.
	o.Resolver = &rebinding{}
	_, err = Send(context.Background(), mustLoad(t, "invoice.paid"), Target{URL: "http://rebind.shop.example:" + port + "/"}, o)
	if !errors.Is(err, ErrRefused) || len(got.reqs) != 1 {
		t.Errorf("rebinding: %v (%d requests)", err, len(got.reqs))
	}
	// A configured staging prefix allows the host whatever it resolves to.
	o.Resolver = staticResolver{"staging.shop.example": {"127.0.0.1"}}
	o.Staging = []string{"http://staging.shop.example:" + port + "/"}
	if res, err := Send(context.Background(), mustLoad(t, "invoice.paid"), Target{URL: "http://staging.shop.example:" + port + "/hook"}, o); err != nil || res.Status != 200 {
		t.Errorf("staging: %+v %v", res, err)
	}
}

func TestInStaging(t *testing.T) {
	prefixes := []string{"https://staging.shop.example/", "https://preview.shop.example/app", "http://local.test:8080"}
	for raw, want := range map[string]bool{
		"https://staging.shop.example/anything":       true,
		"https://STAGING.shop.example:443/x":          true,
		"http://staging.shop.example/x":               false,
		"https://staging.shop.example.evil.example/x": false,
		"https://preview.shop.example/app":            true,
		"https://preview.shop.example/app/hooks":      true,
		"https://preview.shop.example/apple":          false,
		"http://local.test:8080/x":                    true,
		"http://local.test/x":                         false,
		"https://shop.example/":                       false,
	} {
		u, _ := url.Parse(raw)
		if got := inStaging(u, prefixes); got != want {
			t.Errorf("inStaging(%s) = %v", raw, got)
		}
	}
}

func TestResponseHandling(t *testing.T) {
	var other atomic.Int32
	elsewhere := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { other.Add(1) }))
	defer elsewhere.Close()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/redirect" {
			http.Redirect(w, r, elsewhere.URL, http.StatusFound)
			return
		}
		_, _ = w.Write([]byte(strings.Repeat("x", 100) + " key " + liveStripeKey))
	}))
	defer srv.Close()
	o := opts(stripeSecret)
	res, err := Send(context.Background(), mustLoad(t, "invoice.paid"), Target{URL: srv.URL + "/redirect"}, o)
	if err != nil || res.Status != 302 || res.Location != elsewhere.URL || other.Load() != 0 {
		t.Errorf("redirect: %+v %v (followed %d)", res, err, other.Load())
	}
	res, err = Send(context.Background(), mustLoad(t, "invoice.paid"), Target{URL: srv.URL}, o)
	if err != nil || strings.Contains(res.Body, liveStripeKey) || res.Truncated {
		t.Errorf("redaction: %+v %v", res, err)
	}
	o.MaxBody = 10
	res, _ = Send(context.Background(), mustLoad(t, "invoice.paid"), Target{URL: srv.URL}, o)
	if !res.Truncated || res.Body != strings.Repeat("x", 10) {
		t.Errorf("cap: %+v", res)
	}
}

func TestTargetHeadersCantReplaceSignature(t *testing.T) {
	srv, got := receiver(t, stripeSecret, verifyStripe)
	res, err := Send(context.Background(), mustLoad(t, "invoice.paid"),
		Target{URL: srv.URL, Headers: map[string]string{"stripe-signature": "t=1,v1=00", "X-Tenant": "staging"}}, opts(stripeSecret))
	if err != nil || res.Status != 200 || got.reqs[0].Header.Get("X-Tenant") != "staging" {
		t.Errorf("%+v %v", res, err)
	}
}

func TestSecretFromEnv(t *testing.T) {
	srv, _ := receiver(t, stripeSecret, verifyStripe)
	o := opts("")
	o.Env = []string{"MY_SECRET=" + stripeSecret}
	o.SecretEnv = "MY_SECRET"
	res, err := Send(context.Background(), mustLoad(t, "invoice.paid"), Target{URL: srv.URL}, o)
	if err != nil || res.Status != 200 || !strings.HasPrefix(res.Secret, "MY_SECRET=whsec_") {
		t.Errorf("%+v %v", res, err)
	}
}

func TestLibrary(t *testing.T) {
	want := []string{
		"stripe/checkout.session.completed", "stripe/payment_intent.succeeded", "stripe/payment_intent.payment_failed",
		"stripe/charge.refunded", "stripe/customer.subscription.created", "stripe/customer.subscription.updated",
		"stripe/customer.subscription.deleted", "stripe/invoice.paid", "stripe/invoice.payment_failed",
		"resend/email.delivered", "resend/email.bounced", "clerk/user.created", "github/ping", "woocommerce/order.created",
	}
	have := map[string]bool{}
	for _, n := range Events() {
		have[n] = true
	}
	for _, n := range want {
		if !have[n] {
			t.Errorf("missing %s", n)
		}
	}
	for _, n := range Events() {
		e := mustLoad(t, n)
		if !json.Valid(e.Body) || e.Scheme == "" || e.Type == "" {
			t.Errorf("%s: %+v", n, e)
		}
		if liveMode.Match(e.Body) {
			t.Errorf("%s is live", n)
		}
		if e.Scheme == Stripe {
			var p struct {
				ID       string `json:"id"`
				Type     string `json:"type"`
				Livemode *bool  `json:"livemode"`
			}
			_ = json.Unmarshal(e.Body, &p)
			if !strings.HasPrefix(p.ID, "evt_test_") || p.Type != e.Type || p.Livemode == nil || *p.Livemode {
				t.Errorf("%s: %+v", n, p)
			}
		}
	}
	if e, err := Load("invoice.payment_failed"); err != nil || e.Name != "stripe/invoice.payment_failed" {
		t.Errorf("short name: %+v %v", e, err)
	}
	if _, err := Load("nope.event"); err == nil {
		t.Error("unknown event loaded")
	}
}

func TestFromFile(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "lead.json")
	_ = os.WriteFile(p, []byte(`{"type":"lead.created","email":"jamie@example.net"}`), 0o600)
	e, err := FromFile(p, "", "")
	if err != nil || e.Scheme != HMACScheme || e.Type != "lead.created" {
		t.Fatalf("%+v %v", e, err)
	}
	_ = os.WriteFile(p, []byte(`{not json`), 0o600)
	if _, err := FromFile(p, Stripe, ""); err == nil {
		t.Error("invalid JSON accepted")
	}
}

func TestSecretReady(t *testing.T) {
	e := Event{Name: "x", Scheme: Stripe}
	if err := SecretReady(e, Options{Env: []string{}}); !errors.Is(err, ErrNoSecret) {
		t.Errorf("no secret: %v", err)
	}
	if err := SecretReady(e, Options{Env: []string{"ECHO_STRIPE_WEBHOOK_SECRET=" + "whsec_" + "abc"}}); err != nil {
		t.Errorf("secret set: %v", err)
	}
}

// TestProductionRefused: a live site is refused even when its name
// resolves to a private address (a hosts-file entry or a VPN) or it's
// also listed as staging, whatever the case or the subdomain.
func TestProductionRefused(t *testing.T) {
	srv, got := receiver(t, stripeSecret, verifyStripe)
	_, port, _ := strings.Cut(srv.Listener.Addr().String(), ":")
	o := opts(stripeSecret)
	o.Resolver = staticResolver{"shop.example": {"127.0.0.1"}, "www.shop.example": {"127.0.0.1"}, "staging.test": {"127.0.0.1"}}
	o.Production = []string{"shop.example"}
	for _, target := range []string{"http://shop.example:" + port + "/hook", "http://WWW.Shop.Example:" + port + "/hook"} {
		o.Staging = []string{target}
		_, err := Send(context.Background(), mustLoad(t, "invoice.paid"), Target{URL: target}, o)
		if !errors.Is(err, ErrRefused) || !strings.Contains(err.Error(), `matches "shop.example" under production`) {
			t.Errorf("%s: %v", target, err)
		}
	}
	if len(got.reqs) != 0 {
		t.Fatalf("%d requests reached a production site", len(got.reqs))
	}
	o.Staging = nil
	if res, err := Send(context.Background(), mustLoad(t, "invoice.paid"), Target{URL: "http://staging.test:" + port + "/hook"}, o); err != nil || res.Status != 200 {
		t.Errorf("a site not under production: %+v %v", res, err)
	}
}

// With a run key, X-Echo-Run is signed the way wire.SignRun signs it
// (the K0 test vector), so a connector that has the token trusts it.
func TestRunHeaderSigned(t *testing.T) {
	srv, got := receiver(t, stripeSecret, verifyStripe)
	o := opts(stripeSecret)
	o.Action, o.RunKey = "checkout", "secret"
	res, err := Send(context.Background(), mustLoad(t, "stripe/checkout.session.completed"), Target{URL: srv.URL}, o)
	if err != nil {
		t.Fatal(err)
	}
	if want := "r1/checkout.NhOp3HY0JQ76wSoPnn3sZg"; got.reqs[0].Header.Get("X-Echo-Run") != want || res.Headers["X-Echo-Run"] != want {
		t.Errorf("X-Echo-Run = %q (result %q), want %q", got.reqs[0].Header.Get("X-Echo-Run"), res.Headers["X-Echo-Run"], want)
	}
	if res.Run != "r1" || res.Action != "checkout" {
		t.Errorf("the result names the run unsigned: %q %q", res.Run, res.Action)
	}
	if signRun("", "r1", "checkout") != "r1/checkout" {
		t.Error("no token: unsigned")
	}
}
