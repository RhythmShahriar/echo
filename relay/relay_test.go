// Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
// SPDX-License-Identifier: MIT

package relay

import (
	"bytes"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/json"
	"errors"
	"io"
	"math/big"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/emersion/go-sasl"
	"github.com/emersion/go-smtp"

	"github.com/RhythmShahriar/echo/catcher"
	"github.com/RhythmShahriar/echo/model"
	"github.com/RhythmShahriar/echo/wire"
)

// Local SMTP servers: one with STARTTLS and a login, one without TLS.
// They share a port, since no test starts both and each closes its own:
// go test ./... runs packages in parallel and the fixture range is small.
const (
	tlsSMTP   = "127.0.0.1:20509"
	plainSMTP = tlsSMTP
	user      = "agency"
	password  = "s3cret-relay-pass"
	readToken = "read-token-0001"
)

type delivery struct {
	from string
	to   []string
	data string
}

// mta is a go-smtp server that keeps what it receives.
type mta struct {
	mu   sync.Mutex
	got  []delivery
	auth []string // logins tried
}

func (m *mta) NewSession(*smtp.Conn) (smtp.Session, error) { return &mtaSession{m: m}, nil }

func (m *mta) deliveries() []delivery {
	m.mu.Lock()
	defer m.mu.Unlock()
	return append([]delivery(nil), m.got...)
}

type mtaSession struct {
	m      *mta
	authed bool
	d      delivery
}

func (s *mtaSession) AuthMechanisms() []string { return []string{sasl.Plain} }
func (s *mtaSession) Auth(string) (sasl.Server, error) {
	return sasl.NewPlainServer(func(_, u, p string) error {
		s.m.mu.Lock()
		s.m.auth = append(s.m.auth, u)
		s.m.mu.Unlock()
		if u != user || p != password {
			return errors.New("535 5.7.8 bad login for " + u + " with " + p) // a server that echoes what it got
		}
		s.authed = true
		return nil
	}), nil
}
func (s *mtaSession) Mail(from string, _ *smtp.MailOptions) error {
	if !s.authed {
		return smtp.ErrAuthRequired
	}
	s.d.from = from
	return nil
}
func (s *mtaSession) Rcpt(to string, _ *smtp.RcptOptions) error {
	s.d.to = append(s.d.to, to)
	return nil
}
func (s *mtaSession) Data(r io.Reader) error {
	b, err := io.ReadAll(r)
	if err != nil {
		return err
	}
	s.d.data = string(b)
	s.m.mu.Lock()
	s.m.got = append(s.m.got, s.d)
	s.m.mu.Unlock()
	return nil
}
func (s *mtaSession) Reset()        { s.d = delivery{} }
func (s *mtaSession) Logout() error { return nil }

// certs returns a server certificate for 127.0.0.1 and a client config
// that trusts it.
func certs(t *testing.T) (*tls.Config, *tls.Config) {
	t.Helper()
	key, _ := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	tmpl := &x509.Certificate{SerialNumber: big.NewInt(1), Subject: pkix.Name{CommonName: "test mta"},
		NotBefore: time.Now().Add(-time.Hour), NotAfter: time.Now().Add(time.Hour), IPAddresses: []net.IP{net.ParseIP("127.0.0.1")},
		ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth}, KeyUsage: x509.KeyUsageDigitalSignature}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &key.PublicKey, key)
	if err != nil {
		t.Fatal(err)
	}
	leaf, _ := x509.ParseCertificate(der)
	pool := x509.NewCertPool()
	pool.AddCert(leaf)
	return &tls.Config{Certificates: []tls.Certificate{{Certificate: [][]byte{der}, PrivateKey: key}}, MinVersion: tls.VersionTLS12},
		&tls.Config{RootCAs: pool, MinVersion: tls.VersionTLS12}
}

func startMTA(t *testing.T, addr string, tlsCfg *tls.Config) *mta {
	t.Helper()
	m := &mta{}
	srv := smtp.NewServer(m)
	srv.Domain = "mta.test"
	srv.TLSConfig = tlsCfg
	ln, err := net.Listen("tcp", addr)
	if err != nil {
		t.Fatal(err)
	}
	go func() { _ = srv.Serve(ln) }()
	t.Cleanup(func() { _ = srv.Close() })
	return m
}

// inbox holds two captured emails: one with its raw MIME (and a Bcc line
// the release must drop), one with fields only.
func inbox(t *testing.T) (*catcher.Inbox, string, string) {
	t.Helper()
	in := catcher.NewInbox(100)
	s := catcher.NewServer(in, catcher.Config{})
	raw := "From: Shop <shop@shop.test>\nTo: Ann <ann@example.com>\nBcc: audit@shop.test\nSubject: Your order\n" +
		"Message-ID: <abc@shop.test>\nContent-Type: text/plain; charset=utf-8\n\nThanks for your order.\n.\nThat dot is fine.\n"
	d1, err := s.Capture(wire.Capture{Version: wire.Version, Kind: "email", Email: &wire.Email{Raw: raw}})
	if err != nil {
		t.Fatal(err)
	}
	d2, err := s.Capture(wire.Capture{Version: wire.Version, Kind: "email", Email: &wire.Email{From: "shop@shop.test",
		To: []string{"bob@example.com"}, Subject: "Welcome, Bob: ünïcode", Text: "Hello Bob", HTML: "<p>Hello <b>Bob</b></p>"}})
	if err != nil {
		t.Fatal(err)
	}
	return in, d1.ID, d2.ID
}

func release(t *testing.T, h http.Handler, id, to string, auth func(*http.Request)) (int, answer) {
	t.Helper()
	body, _ := json.Marshal(map[string]string{"to": to})
	req := httptest.NewRequest(http.MethodPost, "/v1/messages/"+id+"/release", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	if auth != nil {
		auth(req)
	}
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	var a answer
	_ = json.Unmarshal(rec.Body.Bytes(), &a)
	if strings.Contains(rec.Body.String(), password) {
		t.Errorf("the answer shows the relay password: %s", rec.Body)
	}
	return rec.Code, a
}

func withToken(r *http.Request) { r.Header.Set(wire.TokenHeader, readToken) }

// logs returns the release log entries in the inbox.
func logs(in *catcher.Inbox) []model.Message {
	return in.List(catcher.Filter{Run: "release"})
}

func TestRelease(t *testing.T) {
	serverTLS, clientTLS := certs(t)
	m := startMTA(t, tlsSMTP, serverTLS)
	in, rawID, fieldsID := inbox(t)
	h, err := Handler(in, Config{URL: "smtp://" + user + ":" + password + "@" + tlsSMTP, Allow: []string{"*@client.example", "Pat@Agency.test"},
		ReadToken: readToken, TLS: clientTLS,
		Session: func(r *http.Request) (string, bool) { return "client-pat", r.Header.Get("Cookie") == "session=ok" }})
	if err != nil {
		t.Fatal(err)
	}

	t.Run("allowed, raw MIME", func(t *testing.T) {
		code, a := release(t, h, rawID, "Ann Client <ann@client.example>", withToken)
		if code != http.StatusOK || a.Released != rawID || a.To != "ann@client.example" || a.Log == "" {
			t.Fatalf("%d %+v", code, a)
		}
		got := m.deliveries()
		if len(got) != 1 {
			t.Fatalf("delivered %d", len(got))
		}
		d := got[0]
		if d.from != "shop@shop.test" || strings.Join(d.to, ",") != "ann@client.example" {
			t.Errorf("envelope %s -> %v", d.from, d.to)
		}
		for _, want := range []string{"X-Echo-Release: " + rawID + "\r\n", "Subject: Your order\r\n", "\r\n.\r\nThat dot is fine."} {
			if !strings.Contains(d.data, want) {
				t.Errorf("message has no %q:\n%s", want, d.data)
			}
		}
		if strings.Contains(d.data, "audit@shop.test") {
			t.Errorf("the Bcc line went out:\n%s", d.data)
		}
		l, ok := in.Get(a.Log)
		if !ok || l.Status != model.Sent || l.Headers["x-echo-released-by"] != "read token" || l.Headers["x-echo-release-of"] != rawID ||
			l.To[0] != "ann@client.example" || l.At.IsZero() || l.Channel == model.Email {
			t.Errorf("release log %+v", l)
		}
	})

	t.Run("not allowlisted", func(t *testing.T) {
		before := len(m.deliveries())
		code, a := release(t, h, rawID, "ann@real-customer.example", withToken)
		if code != http.StatusForbidden || !strings.Contains(a.Error, "isn't on the relay's allowlist") {
			t.Errorf("%d %+v", code, a)
		}
		if len(m.deliveries()) != before {
			t.Error("a refused release was sent")
		}
		if l, ok := in.Get(a.Log); !ok || l.Status != model.Blocked || l.URL != "" {
			t.Errorf("refusal log %+v", l)
		}
	})

	t.Run("signed-in reader, rebuilt message", func(t *testing.T) {
		code, a := release(t, h, fieldsID, "pat@agency.test", func(r *http.Request) { r.Header.Set("Cookie", "session=ok") })
		if code != http.StatusOK {
			t.Fatalf("%d %+v", code, a)
		}
		got := m.deliveries()
		d := got[len(got)-1]
		for _, want := range []string{"Content-Type: multipart/alternative", "Content-Type: text/html; charset=utf-8", "Hello <b>Bob</b>",
			"Subject: =?utf-8?q?", "To: bob@example.com", "X-Echo-Release: " + fieldsID} {
			if !strings.Contains(d.data, want) {
				t.Errorf("rebuilt message has no %q:\n%s", want, d.data)
			}
		}
		if l, _ := in.Get(a.Log); l.Headers["x-echo-released-by"] != "client-pat" {
			t.Errorf("who: %+v", l.Headers)
		}
	})

	t.Run("refused requests", func(t *testing.T) {
		for _, tc := range []struct {
			name, id, to string
			auth         func(*http.Request)
			want         int
		}{
			{"no token", rawID, "ann@client.example", nil, http.StatusUnauthorized},
			{"wrong token", rawID, "ann@client.example", func(r *http.Request) { r.Header.Set(wire.TokenHeader, "nope") }, http.StatusUnauthorized},
			{"no session", rawID, "ann@client.example", func(r *http.Request) { r.Header.Set("Cookie", "session=bad") }, http.StatusUnauthorized},
			{"unknown message", "nope", "ann@client.example", withToken, http.StatusNotFound},
			{"two addresses", rawID, "ann@client.example, bob@client.example", withToken, http.StatusBadRequest},
			{"not an address", rawID, "ann", withToken, http.StatusBadRequest},
			{"quoted local part", rawID, `"x@evil.example"@client.example`, withToken, http.StatusForbidden},
			{"percent hack", rawID, "x%evil.example@client.example", withToken, http.StatusForbidden},
		} {
			before := len(m.deliveries())
			if code, a := release(t, h, tc.id, tc.to, tc.auth); code != tc.want {
				t.Errorf("%s: %d %+v, want %d", tc.name, code, a, tc.want)
			}
			if len(m.deliveries()) != before {
				t.Errorf("%s: sent", tc.name)
			}
		}
		// A release log entry can't be released itself.
		if code, _ := release(t, h, logs(in)[0].ID, "ann@client.example", withToken); code != http.StatusNotFound {
			t.Errorf("released a log entry: %d", code)
		}
	})

	t.Run("email from a provider's HTTP API", func(t *testing.T) {
		api := in.Add(model.Message{Run: "r1", Channel: model.Email, Provider: "sendgrid", Method: http.MethodPost,
			URL: "https://api.sendgrid.com/v3/mail/send", Status: model.Faked, Source: model.Source{Via: "proxy"},
			From: "shop@shop.test", To: []string{"ann@client.example", "other@victim.example"}, BCC: []string{"audit@shop.test"},
			Subject: "Your receipt", Text: "Thanks, Ann",
			Body: `{"personalizations":[{"to":[{"email":"ann@client.example"},{"email":"other@victim.example"}]}],"subject":"Your receipt"}`})
		code, a := release(t, h, api.ID, "ann@client.example", withToken)
		if code != http.StatusOK {
			t.Fatalf("%d %+v", code, a)
		}
		got := m.deliveries()
		d := got[len(got)-1]
		if strings.Contains(d.data, "personalizations") || strings.Contains(d.data, "audit@shop.test") {
			t.Errorf("sent the API request, not an email:\n%s", d.data)
		}
		for _, want := range []string{"From: shop@shop.test\r\n", "Subject: Your receipt\r\n", "Thanks, Ann", "X-Echo-Release: " + api.ID} {
			if !strings.Contains(d.data, want) {
				t.Errorf("message has no %q:\n%s", want, d.data)
			}
		}
	})
}

func TestReleaseNeverLeaksTheLogin(t *testing.T) {
	serverTLS, clientTLS := certs(t)
	m := startMTA(t, tlsSMTP, serverTLS)
	in, rawID, _ := inbox(t)
	h, err := Handler(in, Config{URL: "smtp://" + user + ":wrong-" + password + "@" + tlsSMTP, Allow: []string{"*@client.example"},
		ReadToken: readToken, TLS: clientTLS})
	if err != nil {
		t.Fatal(err)
	}
	code, a := release(t, h, rawID, "ann@client.example", withToken)
	if code != http.StatusBadGateway || !strings.Contains(a.Error, "the relay at "+tlsSMTP+" didn't take it") {
		t.Errorf("%d %+v", code, a)
	}
	if len(m.auth) != 1 || len(m.deliveries()) != 0 {
		t.Errorf("logins %v, deliveries %d", m.auth, len(m.deliveries()))
	}
	b, _ := json.Marshal(in.List(catcher.Filter{}))
	for _, secret := range []string{password, user + ":"} {
		if strings.Contains(string(b), secret) || strings.Contains(a.Error, secret) {
			t.Errorf("%q is in the inbox or the answer:\n%s\n%s", secret, b, a.Error)
		}
	}
	if l, _ := in.Get(a.Log); l.Status != model.Failed || l.URL != "smtp://"+tlsSMTP {
		t.Errorf("failure log %+v", l)
	}
}

func TestReleaseWantsTLS(t *testing.T) {
	m := startMTA(t, plainSMTP, nil) // offers no STARTTLS
	in, rawID, _ := inbox(t)
	h, err := Handler(in, Config{URL: "smtp://" + user + ":" + password + "@" + plainSMTP, Allow: []string{"*@client.example"}, ReadToken: readToken})
	if err != nil {
		t.Fatal(err)
	}
	code, a := release(t, h, rawID, "ann@client.example", withToken)
	if code != http.StatusBadGateway || !strings.Contains(a.Error, "doesn't offer STARTTLS") {
		t.Errorf("%d %+v", code, a)
	}
	if len(m.auth) != 0 || len(m.deliveries()) != 0 {
		t.Errorf("sent a login or a message in clear: %v %d", m.auth, len(m.deliveries()))
	}
}

func TestHandlerConfig(t *testing.T) {
	for _, tc := range []struct{ url, want string }{
		{"http://u:" + password + "@mail.test", `scheme "http"`},
		{"smtp://u:" + password + "@:587", "no host"},
		{"smtp://u:" + password + "@mail.test:bad port", "not a URL"},
		{"smtp://" + password + "@mail.test:%zz", "not a URL"},
	} {
		_, err := Handler(catcher.NewInbox(1), Config{URL: tc.url})
		if err == nil || !strings.Contains(err.Error(), tc.want) {
			t.Errorf("%s: %v, want %q", tc.url, err, tc.want)
		}
		if err != nil && strings.Contains(err.Error(), password) {
			t.Errorf("the error shows the password: %v", err)
		}
	}
	for url, port := range map[string]string{"smtp://mail.test": "587", "smtps://mail.test": "465", "smtp://mail.test:2525": "2525"} {
		a, err := parseURL(url)
		if err != nil || a.port != port {
			t.Errorf("%s: %+v %v", url, a, err)
		}
	}
}

func TestAllowed(t *testing.T) {
	allow := []string{"*@client.example", "pat@agency.test"}
	for addr, want := range map[string]bool{
		"ann@client.example":              true,
		"ANN@Client.Example":              true,
		"pat@agency.test":                 true,
		"ann@real.example":                false,
		"ann@client.example.evil":         false,
		"sam@agency.test":                 false,
		"ann@sub.client.example":          false,
		"client.example@other.com":        false,
		`"x@evil.example"@client.example`: false,
		"x@evil.example@client.example":   false,
		"x%evil.example@client.example":   false,
		"evil.example!x@client.example":   false,
		"@client.example":                 false,
	} {
		if got := Allowed(allow, addr); got != want {
			t.Errorf("Allowed(%q) = %v, want %v", addr, got, want)
		}
	}
	if Allowed(nil, "ann@client.example") {
		t.Error("an empty allowlist allowed an address")
	}
}

func TestMessageDropsBcc(t *testing.T) {
	raw := "From: a@shop.test\r\nBcc: x@shop.test,\r\n y@shop.test\r\nTo: b@example.com\r\n\r\nBcc: stays in the body\r\n"
	got := string(Message(model.Message{ID: "m1", Body: raw}))
	if strings.Contains(got, "x@shop.test") || strings.Contains(got, "y@shop.test") || !strings.Contains(got, "To: b@example.com") ||
		!strings.Contains(got, "\r\n\r\nBcc: stays in the body") {
		t.Errorf("got:\n%s", got)
	}
	// A cut raw message is rebuilt from the fields instead.
	got = string(Message(model.Message{ID: "m2", Body: "From: a\n\nlong…(truncated)", From: "a@shop.test", Text: "short"}))
	if strings.Contains(got, "truncated") || !strings.Contains(got, "short") {
		t.Errorf("cut message:\n%s", got)
	}
}
