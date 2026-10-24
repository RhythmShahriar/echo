// Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
// SPDX-License-Identifier: MIT

package main

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/RhythmShahriar/echo/catcher"
	"github.com/RhythmShahriar/echo/client"
	"github.com/RhythmShahriar/echo/model"
	"github.com/RhythmShahriar/echo/wire"
)

func TestHeadersCommand(t *testing.T) {
	code, out, errOut := runCLI(t, "headers", "../../headers/testdata/phpmail.eml", "--plain")
	if code != 0 || !strings.Contains(out, "PHP") {
		t.Fatalf("headers: %d\n%s\n%s", code, out, errOut)
	}
	code, out, _ = runCLI(t, "headers", "../../headers/testdata/sendgrid.eml", "--json")
	if code != 0 || !strings.Contains(out, `"provider": "sendgrid"`) {
		t.Errorf("headers --json: %d\n%s", code, out)
	}
	if code, _, _ := runCLI(t, "headers"); code != exitUsage {
		t.Error("headers without a file is a usage error")
	}
}

func TestReplayCommand(t *testing.T) {
	secret := strings.Join([]string{"wh", "sec_", "CliTestSigningSecret0123456789"}, "")
	t.Setenv("ECHO_STRIPE_WEBHOOK_SECRET", secret)
	echoSrv := httptest.NewServer(catcher.NewServer(catcher.NewInbox(100), catcher.Config{Version: "test"}))
	defer echoSrv.Close()
	echo := client.New(echoSrv.URL, "")
	// A staging shop: verifies the Stripe signature and, like a payment
	// plugin, emails the customer.
	shop := stripeShop(t, secret, echo, nil)
	defer shop.Close()
	t.Chdir(t.TempDir())
	code, out, errOut := runCLI(t, "replay", "payment_intent.succeeded", "--to", shop.URL+"/webhook", "--url", echoSrv.URL, "--plain", "--wait", "2s")
	if code != 0 || !strings.Contains(out, "answered 200 and sent 1 email") {
		t.Fatalf("replay: %d\n%s\n%s", code, out, errOut)
	}
	// Live keys in reach stop it.
	t.Setenv("STRIPE_SECRET_KEY", strings.Join([]string{"sk", "_live_", "abcdefghijklmnop1234"}, ""))
	if code, _, errOut := runCLI(t, "replay", "payment_intent.succeeded", "--to", shop.URL, "--url", echoSrv.URL); code != exitUsage {
		t.Errorf("live key: exit %d %s", code, errOut)
	}
	if code, out, _ := runCLI(t, "replay", "--list"); code != 0 || !strings.Contains(out, "payment_intent.succeeded") {
		t.Errorf("--list:\n%s", out)
	}
}

// stripeShop is a staging shop's webhook endpoint: it checks the Stripe
// signature and, like a payment plugin, emails the customer through
// Echo, tagged with the run. seen, when set, gets each body it accepts.
func stripeShop(t *testing.T, secret string, echo *client.Client, seen chan<- string) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		sig := r.Header.Get("Stripe-Signature")
		var ts, v1 string
		for _, p := range strings.Split(sig, ",") {
			k, v, _ := strings.Cut(p, "=")
			switch k {
			case "t":
				ts = v
			case "v1":
				v1 = v
			}
		}
		mac := hmac.New(sha256.New, []byte(secret))
		mac.Write([]byte(ts + "." + string(body)))
		if !hmac.Equal([]byte(hex.EncodeToString(mac.Sum(nil))), []byte(v1)) {
			http.Error(w, "bad signature", 400)
			return
		}
		if seen != nil {
			seen <- string(body)
		}
		_, _ = echo.Capture(context.Background(), wire.Capture{Kind: "email", Run: r.Header.Get(wire.RunHeader), Source: wire.Source{Via: "test"},
			Email: &wire.Email{To: []string{"buyer@example.com"}, Subject: "Payment received", Text: "Thanks"}})
		w.WriteHeader(200)
	}))
}

// TestRunReplayAction runs a replay action from .echo/config.yaml: the
// replayer signs the event with the configured secret, applies set: in
// a fixed order and fails the action when the site rejects it.
func TestRunReplayAction(t *testing.T) {
	secret := strings.Join([]string{"wh", "sec_", "CliRunReplaySecret0123456789"}, "")
	t.Setenv("SHOP_STRIPE_SECRET", secret)
	echoSrv := httptest.NewServer(catcher.NewServer(catcher.NewInbox(100), catcher.Config{Version: "test"}))
	defer echoSrv.Close()
	seen := make(chan string, 4)
	shop := stripeShop(t, secret, client.New(echoSrv.URL, ""), seen)
	defer shop.Close()
	t.Chdir(t.TempDir())
	cfg := func(secretEnv string) string {
		return "site: " + shop.URL + "\nquiet: 200ms\ntimeout: 3s\nactions:\n" +
			"  - name: Paid\n    replay:\n      event: payment_intent.succeeded\n      to: /webhook\n" +
			"      secret_env: " + secretEnv + "\n      set: {data.object.amount: \"1999\", data.object.currency: eur}\n"
	}
	writeConfig(t, cfg("SHOP_STRIPE_SECRET"))
	code, out, errOut := runCLI(t, "run", "--url", echoSrv.URL, "--plain")
	if code != exitOK || !strings.Contains(out, "1 email") {
		t.Fatalf("run with a replay action: %d\n%s\n%s", code, out, errOut)
	}
	var compact bytes.Buffer
	_ = json.Compact(&compact, []byte(<-seen))
	if body := compact.String(); !strings.Contains(body, `"amount":1999`) || !strings.Contains(body, `"currency":"eur"`) {
		t.Errorf("set: was not applied:\n%s", body)
	}

	// The site rejects a wrong signature, so the only action failed.
	t.Setenv("WRONG_SECRET", strings.Join([]string{"wh", "sec_", "NotTheShopsSecret0123456789"}, ""))
	writeConfig(t, cfg("WRONG_SECRET"))
	if code, out, errOut := runCLI(t, "run", "--url", echoSrv.URL, "--plain"); code != exitError || !strings.Contains(out+errOut, "the site answered 400") {
		t.Errorf("rejected replay: %d\n%s\n%s", code, out, errOut)
	}
	// An event that doesn't exist fails the action too.
	writeConfig(t, strings.Replace(cfg("SHOP_STRIPE_SECRET"), "payment_intent.succeeded", "no.such.event", 1))
	if code, out, errOut := runCLI(t, "run", "--url", echoSrv.URL, "--plain"); code != exitError {
		t.Errorf("unknown event: %d\n%s\n%s", code, out, errOut)
	}
}

// nxdomain answers every DNS query on loopback with NXDOMAIN and counts
// them, so tests can see which resolver a command used without real DNS.
func nxdomain(t *testing.T) (string, *atomic.Int32) {
	t.Helper()
	pc, err := net.ListenPacket("udp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = pc.Close() })
	var n atomic.Int32
	go func() {
		buf := make([]byte, 1500)
		for {
			size, from, err := pc.ReadFrom(buf)
			if err != nil {
				return
			}
			if size < 12 {
				continue
			}
			n.Add(1)
			resp := append([]byte(nil), buf[:size]...)
			resp[2] |= 0x80                   // a response
			resp[3] = resp[3]&0xf0 | 0x80 | 3 // recursion available, NXDOMAIN
			_, _ = pc.WriteTo(resp, from)
		}
	}()
	return pc.LocalAddr().String(), &n
}

func TestCheckAndHeadersOptions(t *testing.T) {
	if code, _, errOut := runCLI(t, "check", "shop.test", "--provider", "sendgird"); code != exitUsage ||
		!strings.Contains(errOut, `--provider "sendgird": use one of sendgrid, mailgun`) {
		t.Errorf("unknown provider: %d %q", code, errOut)
	}
	addr, n := nxdomain(t)
	code, out, errOut := runCLI(t, "check", "shop.test", "--provider", "ses", "--resolver", addr, "--plain")
	if code != 0 || !strings.Contains(out, "Sender: Amazon SES") || !strings.Contains(out, "Records: no mail records found") || n.Load() == 0 {
		t.Errorf("check: %d, %d queries\n%s\n%s", code, n.Load(), out, errOut)
	}
	if strings.Contains(out, "found -") || strings.Contains(out, "shop.test: ") {
		t.Errorf("issue lines still carry the records summary:\n%s", out)
	}
	before := n.Load()
	code, out, errOut = runCLI(t, "headers", "../../headers/testdata/sendgrid.eml", "--check", "--resolver", addr, "--plain")
	if code != 0 || n.Load() == before {
		t.Errorf("headers --check ignored --resolver: %d, %d queries\n%s\n%s", code, n.Load()-before, out, errOut)
	}
	if !strings.Contains(out, "Sent by: SendGrid") {
		t.Errorf("headers:\n%s", out)
	}
}

// blackHole is a DNS server on loopback that reads every query and never
// answers, like a firewalled or crashed resolver.
func blackHole(t *testing.T) string {
	t.Helper()
	pc, err := net.ListenPacket("udp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = pc.Close() })
	go func() {
		buf := make([]byte, 1500)
		for {
			if _, _, err := pc.ReadFrom(buf); err != nil {
				return
			}
		}
	}()
	return pc.LocalAddr().String()
}

// closedPort is a loopback UDP port nothing listens on: queries to it
// are refused at once.
func closedPort(t *testing.T) string {
	t.Helper()
	pc, err := net.ListenPacket("udp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	addr := pc.LocalAddr().String()
	_ = pc.Close()
	return addr
}

func TestCheckUnreachableResolverExitsThree(t *testing.T) {
	addr := blackHole(t)
	start := time.Now()
	// --fail-on none must not turn "nothing was checked" into a pass.
	code, out, errOut := runCLI(t, "check", "shop.test", "--resolver", addr, "--fail-on", "none", "--plain")
	took := time.Since(start)
	if code != exitError {
		t.Errorf("black hole: exit %d, want %d\n%s\n%s", code, exitError, out, errOut)
	}
	if took >= 16*time.Second {
		t.Errorf("black hole took %s, want under 16s", took)
	}
	want := "rhythm echo: Could not reach the resolver " + addr
	if !strings.Contains(errOut, want) || strings.Count(errOut, "rhythm echo:") != 1 || strings.Contains(errOut, "127.0.0.53") {
		t.Errorf("want one error naming %s:\n%s", addr, errOut)
	}
	if strings.Contains(out, "lookup failed") || strings.Contains(out, "Records:") {
		t.Errorf("per-record warnings hide the cause:\n%s", out)
	}
}

func TestCheckRefusedResolverJSON(t *testing.T) {
	addr := closedPort(t)
	code, out, errOut := runCLI(t, "check", "shop.test", "--resolver", addr, "--json")
	if code != exitError {
		t.Fatalf("refused: exit %d\n%s\n%s", code, out, errOut)
	}
	var rep struct {
		Unreachable bool          `json:"unreachable"`
		Resolver    string        `json:"resolver"`
		Issues      []model.Issue `json:"issues"`
	}
	if err := json.Unmarshal([]byte(out), &rep); err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
	if !rep.Unreachable || rep.Resolver != addr || len(rep.Issues) != 1 || rep.Issues[0].Check != "dns/resolver" ||
		!strings.Contains(rep.Issues[0].Fix, "at "+addr) {
		t.Errorf("json: %+v", rep)
	}
	// A bad --fail-on is still the user's mistake, not the resolver's.
	if code, _, _ := runCLI(t, "check", "shop.test", "--resolver", addr, "--fail-on", "broke"); code != exitUsage {
		t.Errorf("bad --fail-on: exit %d", code)
	}
}

func TestAnsweredLookups(t *testing.T) {
	for _, tc := range []struct {
		err  error
		want bool
	}{
		{nil, true},
		{&net.DNSError{Err: "no such host", IsNotFound: true}, true},
		{&net.DNSError{Err: "server misbehaving", IsTemporary: true}, true},
		{&net.DNSError{Err: "i/o timeout", IsTimeout: true}, false},
		{&net.DNSError{Err: "read udp 127.0.0.1:1->127.0.0.1:2: read: connection refused", IsTemporary: true}, false},
		{context.DeadlineExceeded, false},
	} {
		if got := answered(tc.err); got != tc.want {
			t.Errorf("answered(%v) = %v", tc.err, got)
		}
	}
	if got := lookupCause(&net.DNSError{Err: "dial udp 127.0.0.1:2: i/o timeout", Server: "127.0.0.53:53"}); got != "i/o timeout" {
		t.Errorf("lookupCause: %q", got)
	}
}
