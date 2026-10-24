package catcher

import (
	"bytes"
	"context"
	"encoding/json"
	"net"
	"net/http"
	"net/http/httptest"
	gosmtp "net/smtp"
	"strings"
	"testing"
	"time"

	"github.com/RhythmShahriar/echo/model"
	"github.com/RhythmShahriar/echo/wire"
)

func post(t *testing.T, srv *httptest.Server, c wire.Capture) wire.Decision {
	t.Helper()
	b, _ := json.Marshal(c)
	resp, err := http.Post(srv.URL+"/v1/capture", "application/json", bytes.NewReader(b))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != 200 {
		t.Fatalf("capture: %s", resp.Status)
	}
	var d wire.Decision
	if err := json.NewDecoder(resp.Body).Decode(&d); err != nil {
		t.Fatal(err)
	}
	return d
}

func twilioCapture(phase string) wire.Capture {
	return wire.Capture{Version: 1, Kind: "http", Phase: phase, Run: "r1/order-form",
		Source: wire.Source{Via: "wordpress", Host: "shop.test"},
		HTTP: &wire.HTTP{Method: "POST", URL: "https://api.twilio.com/2010-04-01/Accounts/AC123/Messages.json",
			Headers: map[string]string{"Authorization": "Basic QUMxMjM6c2VjcmV0dG9rZW4=", "Content-Type": "application/x-www-form-urlencoded"},
			Body:    "To=%2B15551230000&From=%2B15550001111&Body=Your+order+%2342+has+shipped"}}
}

func TestSealFakesProviderResponses(t *testing.T) {
	s := NewServer(NewInbox(100), Config{})
	srv := httptest.NewServer(s)
	defer srv.Close()
	d := post(t, srv, twilioCapture(wire.Before))
	if d.Action != wire.Fake || d.Response == nil || d.Response.Status != 201 || !strings.Contains(d.Response.Body, `"status":"queued"`) {
		t.Fatalf("sealed Twilio call: %+v", d)
	}
	m, ok := s.Inbox.Get(d.ID)
	if !ok {
		t.Fatal("not stored")
	}
	if m.Channel != model.SMS || m.Provider != "twilio" || m.To[0] != "+15551230000" || m.Text != "Your order #42 has shipped" ||
		m.Status != model.Faked || m.Run != "r1" || m.Action != "order-form" {
		t.Errorf("message = %+v", m)
	}
	if strings.Contains(m.Headers["authorization"], "QUMx") {
		t.Errorf("authorization leaked: %q", m.Headers["authorization"])
	}
}

func TestObserveAndAllowLetRequestsThrough(t *testing.T) {
	s := NewServer(NewInbox(100), Config{Mode: Observe})
	srv := httptest.NewServer(s)
	defer srv.Close()
	d := post(t, srv, twilioCapture(wire.Before))
	if d.Action != wire.Allow {
		t.Fatalf("observe: %+v", d)
	}
	after := twilioCapture(wire.After)
	after.ID, after.HTTP.Status, after.HTTP.Response = d.ID, 400, `{"code":21211,"message":"Invalid 'To' Phone Number"}`
	post(t, srv, after)
	m, _ := s.Inbox.Get(d.ID)
	if m.Status != model.Failed || m.Response.Status != 400 {
		t.Errorf("observed failure not recorded: %+v", m)
	}

	// Allowlisted payment hosts are only reached with test credentials.
	testKey := strings.Join([]string{"Bearer sk", "_test_", "abcdefghijklmnop1234"}, "")
	sealed := NewServer(NewInbox(100), Config{Allow: []string{"*.stripe.com"}})
	srv2 := httptest.NewServer(sealed)
	defer srv2.Close()
	for _, u := range []string{"https://api.stripe.com/v1/payment_intents", "https://shop.test/wp-cron.php", "http://127.0.0.1:8080/x"} {
		c := wire.Capture{Version: 1, Kind: "http", Phase: wire.Before, Source: wire.Source{Host: "shop.test"},
			HTTP: &wire.HTTP{Method: "POST", URL: u, Headers: map[string]string{"Authorization": testKey}}}
		if d := post(t, srv2, c); d.Action != wire.Allow {
			t.Errorf("%s should be allowed when sealed: %+v", u, d)
		}
	}
}

func TestEmailCaptureParsesMIMEAndRedacts(t *testing.T) {
	s := NewServer(NewInbox(100), Config{})
	srv := httptest.NewServer(s)
	defer srv.Close()
	raw := "From: Shop <shop@shop.test>\r\nTo: buyer@example.com\r\nSubject: Order #42\r\nMIME-Version: 1.0\r\n" +
		"Content-Type: multipart/alternative; boundary=b1\r\n\r\n--b1\r\nContent-Type: text/plain\r\n\r\nThanks! Reset: https://shop.test/?token=abcdef123456\r\n" +
		"--b1\r\nContent-Type: text/html\r\n\r\n<p>Thanks!</p>\r\n--b1--\r\n"
	d := post(t, srv, wire.Capture{Version: 1, Kind: "email", Source: wire.Source{Via: "wordpress", Hook: "wp_mail"},
		Email: &wire.Email{Raw: raw}})
	if d.Action != wire.Fake {
		t.Errorf("sealed email must not be sent: %+v", d)
	}
	m, _ := s.Inbox.Get(d.ID)
	if m.Subject != "Order #42" || m.From != "shop@shop.test" || m.To[0] != "buyer@example.com" || m.HTML != "<p>Thanks!</p>" {
		t.Errorf("parsed = %+v", m)
	}
	if strings.Contains(m.Text, "abcdef123456") {
		t.Errorf("token in text leaked: %q", m.Text)
	}
}

func TestWaitCatchesLateMessages(t *testing.T) {
	in := NewInbox(10)
	in.Begin("r1", "checkout")
	go func() {
		time.Sleep(150 * time.Millisecond)
		in.Add(model.Message{Channel: model.Email, Subject: "receipt"})
		time.Sleep(150 * time.Millisecond)
		in.Add(model.Message{Channel: model.Webhook}) // async, e.g. Action Scheduler
	}()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	got := in.Wait(ctx, Filter{Run: "r1", Action: "checkout"}, 1, 400*time.Millisecond)
	if len(got) != 2 {
		t.Errorf("want both messages, got %d", len(got))
	}
	in.End()
	if m := in.Add(model.Message{}); m.Run != "" {
		t.Errorf("after End, messages aren't attributed: %+v", m)
	}
}

func TestTokenRequiredWhenSet(t *testing.T) {
	srv := httptest.NewServer(NewServer(NewInbox(1), Config{Token: "s3cret"}))
	defer srv.Close()
	resp, err := http.Get(srv.URL + "/v1/health")
	if err != nil {
		t.Fatal(err)
	}
	_ = resp.Body.Close()
	if resp.StatusCode != http.StatusUnauthorized {
		t.Errorf("want 401 without token, got %s", resp.Status)
	}
}

func TestSMTPSink(t *testing.T) {
	s := NewServer(NewInbox(10), Config{})
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	sink := NewSMTP(s, ln.Addr().String())
	go func() { _ = sink.Serve(ln) }()
	defer func() { _ = sink.Close() }()
	msg := "From: shop@shop.test\r\nTo: buyer@example.com\r\nSubject: Welcome\r\nX-Echo-Run: r9/signup\r\nX-Mailer: PHPMailer 6.9\r\n\r\nHello there\r\n"
	if err := gosmtp.SendMail(ln.Addr().String(), nil, "bounce@shop.test", []string{"buyer@example.com", "audit@shop.test"}, []byte(msg)); err != nil {
		t.Fatal(err)
	}
	got := s.Inbox.List(Filter{})
	if len(got) != 1 {
		t.Fatalf("captured %d", len(got))
	}
	m := got[0]
	if m.Subject != "Welcome" || m.Run != "r9" || m.Action != "signup" || m.Source.Via != "smtp" || m.Source.Plugin != "PHPMailer" ||
		len(m.BCC) != 1 || m.BCC[0] != "audit@shop.test" || strings.TrimSpace(m.Text) != "Hello there" {
		t.Errorf("smtp message = %+v", m)
	}
}

func TestEmailAfterPhaseUpdates(t *testing.T) {
	s := NewServer(NewInbox(10), Config{Mode: Observe})
	srv := httptest.NewServer(s)
	defer srv.Close()
	d := post(t, srv, wire.Capture{Version: 1, Kind: "email", Email: &wire.Email{To: []string{"a@example.com"}, Subject: "x"}})
	post(t, srv, wire.Capture{Version: 1, Kind: "email", Phase: wire.After, ID: d.ID, Error: "535 authentication failed"})
	m, _ := s.Inbox.Get(d.ID)
	if m.Status != model.Failed || m.Error == "" || len(s.Inbox.List(Filter{})) != 1 {
		t.Errorf("after phase: %+v", m)
	}
}

func TestLivePaymentsBlockedAndInfrastructureSkipped(t *testing.T) {
	s := NewServer(NewInbox(10), Config{})
	srv := httptest.NewServer(s)
	defer srv.Close()
	live := strings.Join([]string{"Bearer sk", "_live_", "abcdefghijklmnop1234"}, "")
	d := post(t, srv, wire.Capture{Version: 1, Kind: "http", Phase: wire.Before, HTTP: &wire.HTTP{Method: "POST",
		URL: "https://api.stripe.com/v1/payment_intents", Headers: map[string]string{"Authorization": live}, Body: "amount=1000"}})
	if m, _ := s.Inbox.Get(d.ID); m.Status != model.Blocked || d.Response == nil || d.Response.Status < 400 {
		t.Errorf("live payment: %+v %+v", m, d.Response)
	}
	d = post(t, srv, wire.Capture{Version: 1, Kind: "http", Phase: wire.Before, HTTP: &wire.HTTP{Method: "GET", URL: "https://api.wordpress.org/core/version-check/1.7/"}})
	if d.Action != wire.Allow || len(s.Inbox.List(Filter{})) != 1 {
		t.Errorf("update checks pass through unrecorded: %+v, %d messages", d, len(s.Inbox.List(Filter{})))
	}
}

// TestLivePaymentsRefusedWhenAllowedOrObserved: an allowlist entry or
// observe mode lifts the seal, but never for a live-mode payment call.
func TestLivePaymentsRefusedWhenAllowedOrObserved(t *testing.T) {
	stripeKey := func(mode string) string {
		return strings.Join([]string{"Bearer sk", "_" + mode + "_", "abcdefghijklmnop1234"}, "")
	}
	stripeCall := func(auth string) wire.Capture {
		return wire.Capture{Version: 1, Kind: "http", Phase: wire.Before, HTTP: &wire.HTTP{Method: "POST",
			URL: "https://api.stripe.com/v1/payment_intents", Headers: map[string]string{"Authorization": auth}, Body: "amount=1000&confirm=true"}}
	}
	paypalCall := func(host string) wire.Capture {
		return wire.Capture{Version: 1, Kind: "http", Phase: wire.Before, HTTP: &wire.HTTP{Method: "POST",
			URL: "https://" + host + "/v2/checkout/orders", Headers: map[string]string{"Content-Type": "application/json"}, Body: `{}`}}
	}
	for _, c := range []struct {
		name    string
		cfg     Config
		capture wire.Capture
		refused bool
	}{
		{"allowlisted stripe, live key", Config{Allow: []string{"api.stripe.com"}}, stripeCall(stripeKey("live")), true},
		{"allowlisted stripe glob, live key", Config{Allow: []string{"*.stripe.com"}}, stripeCall(stripeKey("live")), true},
		{"observe, stripe live key", Config{Mode: Observe}, stripeCall(stripeKey("live")), true},
		{"observe, stripe key Echo can't read", Config{Mode: Observe}, stripeCall("Bearer •••••"), true},
		{"observe, paypal live", Config{Mode: Observe}, paypalCall("api-m.paypal.com"), true},
		{"allowlisted paypal live", Config{Allow: []string{"api-m.paypal.com"}}, paypalCall("api-m.paypal.com"), true},
		{"observe, stripe test key", Config{Mode: Observe}, stripeCall(stripeKey("test")), false},
		{"allowlisted stripe, test key", Config{Allow: []string{"api.stripe.com"}}, stripeCall(stripeKey("test")), false},
		{"observe, paypal sandbox", Config{Mode: Observe}, paypalCall("api-m.sandbox.paypal.com"), false},
	} {
		t.Run(c.name, func(t *testing.T) {
			s := NewServer(NewInbox(10), c.cfg)
			srv := httptest.NewServer(s)
			defer srv.Close()
			d := post(t, srv, c.capture)
			m, _ := s.Inbox.Get(d.ID)
			if !c.refused {
				if d.Action != wire.Allow || m.Status != model.Sent {
					t.Errorf("test-mode call should go through: %+v, status %s", d, m.Status)
				}
				return
			}
			if d.Action != wire.Fake || d.Response == nil || d.Response.Status < 400 {
				t.Errorf("live call not refused: %+v", d)
			}
			if m.Status != model.Blocked || m.Response == nil || m.Response.Status < 400 {
				t.Errorf("live call not recorded as blocked: %+v", m)
			}
		})
	}
}
