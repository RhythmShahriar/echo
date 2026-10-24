// Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
// SPDX-License-Identifier: MIT

package proxy

import (
	"context"
	"crypto/tls"
	"encoding/base64"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/RhythmShahriar/echo/catcher"
	"github.com/RhythmShahriar/echo/model"
)

// The fake "api.twilio.com": an httptest TLS server with a certificate
// for that name from a throwaway "public" CA. Nothing in these tests
// resolves or reaches the real host: the proxy's dialer maps the name to
// the local server and every client is guarded against direct lookups.
const (
	twilioHost = "api.twilio.com"
	twilioPath = "/2010-04-01/Accounts/AC123/Messages.json"
	twilioForm = "To=%2B15551230000&From=%2B15550001111&Body=Your+order+%2342+has+shipped"
	upstreamOK = `{"sid":"SMupstream","status":"accepted-by-upstream"}`
)

var twilioAuth = "Basic " + base64.StdEncoding.EncodeToString([]byte("AC123:not-a-real-token"))

type harness struct {
	proxyURL   string
	echoCA     string // Echo's CA certificate file
	bundle     string // Echo's CA plus the "public" CA, for clients that see the real upstream (tunnels)
	inbox      *catcher.Inbox
	upstream   *httptest.Server
	hits       atomic.Int32 // requests the upstream received
	upstreamCA *CA
	ca         *CA
}

type setup struct {
	mode  string
	mitm  bool
	allow []string // proxy Allow
	token string
}

func newHarness(t *testing.T, s setup) *harness {
	t.Helper()
	h := &harness{}
	dir := t.TempDir()
	var err error
	if h.upstreamCA, err = LoadCA(filepath.Join(dir, "public-ca")); err != nil {
		t.Fatal(err)
	}
	if h.ca, err = LoadCA(filepath.Join(dir, "echo-ca")); err != nil {
		t.Fatal(err)
	}
	leaf, err := h.upstreamCA.Leaf(twilioHost)
	if err != nil {
		t.Fatal(err)
	}
	h.upstream = httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		h.hits.Add(1)
		_, _ = io.Copy(io.Discard, r.Body)
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusCreated)
		_, _ = io.WriteString(w, upstreamOK)
	}))
	h.upstream.TLS = &tls.Config{Certificates: []tls.Certificate{*leaf}, MinVersion: tls.VersionTLS12}
	h.upstream.StartTLS()
	t.Cleanup(h.upstream.Close)
	upAddr := h.upstream.Listener.Addr().String()

	// Only the fake upstream and loopback can be dialed.
	dial := func(ctx context.Context, network, addr string) (net.Conn, error) {
		host, _, _ := net.SplitHostPort(addr)
		switch {
		case host == twilioHost:
			addr = upAddr
		case host != "127.0.0.1" && host != "localhost":
			return nil, &net.OpError{Op: "dial", Net: network, Err: errNoNet{addr}}
		}
		var d net.Dialer
		return d.DialContext(ctx, network, addr)
	}
	tr := &http.Transport{DialContext: dial, TLSClientConfig: &tls.Config{RootCAs: h.upstreamCA.Pool(), MinVersion: tls.VersionTLS12}}
	t.Cleanup(tr.CloseIdleConnections)

	h.inbox = catcher.NewInbox(100)
	srv := catcher.NewServer(h.inbox, catcher.Config{Mode: s.mode})
	p, err := New(Options{Server: srv, CA: h.ca, MITM: s.mitm, Allow: s.allow, Token: s.token, Transport: tr, Dial: dial})
	if err != nil {
		t.Fatal(err)
	}
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	go func() { _ = p.Serve(l) }()
	t.Cleanup(func() { _ = l.Close(); _ = p.Close() })
	h.proxyURL = "http://" + l.Addr().String()

	h.echoCA = h.ca.CertPath
	h.bundle = filepath.Join(dir, "bundle.pem")
	if err := os.WriteFile(h.bundle, append(append([]byte{}, h.ca.PEM...), h.upstreamCA.PEM...), 0o600); err != nil {
		t.Fatal(err)
	}
	return h
}

type errNoNet struct{ addr string }

func (e errNoNet) Error() string { return "test network: refusing to dial " + e.addr }

// messages waits briefly for n messages (after-phase captures land just
// after the client has its answer).
func (h *harness) messages(t *testing.T, n int) []model.Message {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	return h.inbox.Wait(ctx, catcher.Filter{}, n, 200*time.Millisecond)
}

// scenario is one of the three outcomes every client is checked against.
type scenario struct {
	name   string
	setup  setup
	trust  func(h *harness) string
	status int
	body   string // substring of the answer
	hits   int32  // requests the upstream must have seen
	check  func(t *testing.T, ms []model.Message)
}

var scenarios = []scenario{
	{
		name: "fake", setup: setup{mode: catcher.Seal, mitm: true}, trust: func(h *harness) string { return h.echoCA },
		status: 201, body: `"status":"queued"`, hits: 0,
		check: func(t *testing.T, ms []model.Message) {
			if len(ms) != 1 || ms[0].Provider != "twilio" || ms[0].Status != model.Faked || ms[0].To[0] != "+15551230000" ||
				ms[0].Text != "Your order #42 has shipped" || ms[0].Source.Via != "proxy" {
				t.Errorf("messages = %+v", ms)
			}
			if a := ms[0].Headers["authorization"]; strings.Contains(a, "not-a-real-token") || strings.Contains(a, base64.StdEncoding.EncodeToString([]byte("AC123:not-a-real-token"))) {
				t.Errorf("credentials kept: %q", a)
			}
		},
	},
	{
		name: "allow", setup: setup{mode: catcher.Seal, mitm: true, allow: []string{"*.twilio.com"}}, trust: func(h *harness) string { return h.bundle },
		status: 201, body: "accepted-by-upstream", hits: 1,
		check: func(t *testing.T, ms []model.Message) {
			if len(ms) != 1 || ms[0].Method != http.MethodConnect || ms[0].URL != "https://api.twilio.com/" || ms[0].Status != model.Sent || ms[0].Body != "" {
				t.Errorf("an allowed host is recorded by name only: %+v", ms)
			}
		},
	},
	{
		name: "observe", setup: setup{mode: catcher.Observe, mitm: true}, trust: func(h *harness) string { return h.echoCA },
		status: 201, body: "accepted-by-upstream", hits: 1,
		check: func(t *testing.T, ms []model.Message) {
			if len(ms) != 1 || ms[0].Provider != "twilio" || ms[0].Status != model.Sent || ms[0].Response == nil ||
				ms[0].Response.Status != 201 || !strings.Contains(ms[0].Response.Body, "accepted-by-upstream") {
				t.Errorf("messages = %+v", ms)
			}
		},
	},
}

// runScenarios runs send (one client) through every scenario.
func runScenarios(t *testing.T, send func(t *testing.T, h *harness, ca string) (int, string)) {
	for _, sc := range scenarios {
		t.Run(sc.name, func(t *testing.T) {
			h := newHarness(t, sc.setup)
			status, body := send(t, h, sc.trust(h))
			if status != sc.status || !strings.Contains(body, sc.body) {
				t.Fatalf("got %d %q, want %d containing %q", status, body, sc.status, sc.body)
			}
			if got := h.hits.Load(); got != sc.hits {
				t.Errorf("upstream saw %d requests, want %d", got, sc.hits)
			}
			sc.check(t, h.messages(t, 1))
		})
	}
}

// parseOut reads "status\nbody" printed by an external client.
func parseOut(t *testing.T, out []byte) (int, string) {
	t.Helper()
	code, body, _ := strings.Cut(strings.TrimLeft(string(out), "\n"), "\n")
	var n int
	for _, c := range strings.TrimSpace(code) {
		if c < '0' || c > '9' {
			t.Fatalf("client output: %q", out)
		}
		n = n*10 + int(c-'0')
	}
	return n, body
}
