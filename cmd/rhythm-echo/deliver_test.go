package main

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
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
