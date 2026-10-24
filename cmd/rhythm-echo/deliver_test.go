// Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
// SPDX-License-Identifier: MIT

package main

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/RhythmShahriar/echo/catcher"
	"github.com/RhythmShahriar/echo/client"
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
	shop := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
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
		_, _ = echo.Capture(context.Background(), wire.Capture{Kind: "email", Run: r.Header.Get(wire.RunHeader), Source: wire.Source{Via: "test"},
			Email: &wire.Email{To: []string{"buyer@example.com"}, Subject: "Payment received", Text: "Thanks"}})
		w.WriteHeader(200)
	}))
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
