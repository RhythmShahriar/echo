// Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
// SPDX-License-Identifier: MIT

package catcher

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/RhythmShahriar/echo/model"
	"github.com/RhythmShahriar/echo/wire"
)

// call sends a request with token and returns the status and body.
func call(t *testing.T, method, u, token, body string) (int, string) {
	t.Helper()
	req, _ := http.NewRequest(method, u, strings.NewReader(body))
	if body != "" {
		req.Header.Set("Content-Type", "application/json")
	}
	if token != "" {
		req.Header.Set(wire.TokenHeader, token)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = resp.Body.Close() }()
	b, _ := io.ReadAll(resp.Body)
	return resp.StatusCode, string(b)
}

func emailCapture(subject string) string {
	b, _ := json.Marshal(wire.Capture{Version: 1, Kind: "email", Source: wire.Source{Via: "wordpress"},
		Email: &wire.Email{To: []string{"ann@example.com"}, Subject: subject, Text: "hello"}})
	return string(b)
}

func TestSplitTokens(t *testing.T) {
	s := NewServer(NewInbox(10), Config{ReadToken: "reader", WriteTokens: map[string]string{"wa": "client-a", "wb": "client-b"}})
	srv := httptest.NewServer(s)
	defer srv.Close()

	var da, db wire.Decision
	for tok, d := range map[string]*wire.Decision{"wa": &da, "wb": &db} {
		code, body := call(t, http.MethodPost, srv.URL+"/v1/capture", tok, emailCapture("from "+tok))
		if code != http.StatusOK || json.Unmarshal([]byte(body), d) != nil {
			t.Fatalf("capture with %s: %d %s", tok, code, body)
		}
	}
	if m, _ := s.Inbox.Get(da.ID); m.Source.Site != "client-a" {
		t.Errorf("a write token's capture is marked with its site: %+v", m.Source)
	}

	// A write token can't read, not even its own site's mail.
	const forbidden = "this is a write token: it may only send captures and begin, renew or end actions; reading messages needs the read token"
	for _, path := range []string{"/v1/messages", "/v1/messages/" + da.ID, "/v1/wait?timeout=1ms", "/v1/events", "/v1/messages/latest",
		"/api/v1/messages", "/api/v1/info"} {
		if code, body := call(t, http.MethodGet, srv.URL+path, "wa", ""); code != http.StatusForbidden || strings.TrimSpace(body) != forbidden {
			t.Errorf("GET %s with a write token: %d %s", path, code, body)
		}
	}
	if code, _ := call(t, http.MethodDelete, srv.URL+"/v1/messages", "wa", ""); code != http.StatusForbidden {
		t.Errorf("DELETE with a write token: %d", code)
	}
	// It may begin, renew and end actions and read health.
	code, body := call(t, http.MethodPost, srv.URL+"/v1/actions/begin", "wa", `{"run":"r1","action":"signup"}`)
	var lease struct{ Lease string }
	if code != http.StatusOK || json.Unmarshal([]byte(body), &lease) != nil || lease.Lease == "" {
		t.Fatalf("begin with a write token: %d %s", code, body)
	}
	if code, body := call(t, http.MethodPost, srv.URL+"/v1/actions/renew", "wa", `{"lease":"`+lease.Lease+`"}`); code != http.StatusNoContent {
		t.Errorf("renew with a write token: %d %s", code, body)
	}
	if code, body := call(t, http.MethodPost, srv.URL+"/v1/actions/end", "wa", `{"lease":"`+lease.Lease+`"}`); code != http.StatusNoContent {
		t.Errorf("end with a write token: %d %s", code, body)
	}
	if code, _ := call(t, http.MethodGet, srv.URL+"/v1/health", "wa", ""); code != http.StatusOK {
		t.Errorf("health with a write token: %d", code)
	}
	// A site can't report on another site's message.
	after, _ := json.Marshal(wire.Capture{Version: 1, Kind: "email", Phase: wire.After, ID: db.ID, Error: "bounced"})
	if code, body := call(t, http.MethodPost, srv.URL+"/v1/capture", "wa", string(after)); code != http.StatusBadRequest ||
		!strings.Contains(body, "no message with that ID was captured by this site") {
		t.Errorf("after phase on another site's message: %d %s", code, body)
	}
	if m, _ := s.Inbox.Get(db.ID); m.Status == model.Failed {
		t.Errorf("client-a changed client-b's message: %+v", m)
	}
	if code, _ := call(t, http.MethodPost, srv.URL+"/v1/capture", "wb", string(after)); code != http.StatusOK {
		t.Errorf("after phase on its own message: %d", code)
	}

	// The read token reads every site; site: picks one.
	list := func(q string) []model.Message {
		t.Helper()
		code, body := call(t, http.MethodGet, srv.URL+"/v1/messages?q="+url.QueryEscape(q), "reader", "")
		var ms []model.Message
		if code != http.StatusOK || json.Unmarshal([]byte(body), &ms) != nil {
			t.Fatalf("list %q: %d %s", q, code, body)
		}
		return ms
	}
	if ms := list(""); len(ms) != 2 {
		t.Errorf("read token lists %d", len(ms))
	}
	if ms := list("site:client-a"); len(ms) != 1 || ms[0].ID != da.ID {
		t.Errorf("site:client-a: %+v", ms)
	}

	// No token, or a wrong one, is turned away.
	for _, tok := range []string{"", "nope", "client-a"} {
		if code, _ := call(t, http.MethodPost, srv.URL+"/v1/capture", tok, emailCapture("x")); code != http.StatusUnauthorized {
			t.Errorf("capture with %q: %d", tok, code)
		}
	}
}

func TestLegacyTokenUnchanged(t *testing.T) {
	s := NewServer(NewInbox(10), Config{Token: "s3cret"})
	srv := httptest.NewServer(s)
	defer srv.Close()
	if code, _ := call(t, http.MethodPost, srv.URL+"/v1/capture", "s3cret", emailCapture("hi")); code != http.StatusOK {
		t.Fatalf("capture: %d", code)
	}
	if code, body := call(t, http.MethodGet, srv.URL+"/v1/messages", "s3cret", ""); code != http.StatusOK || !strings.Contains(body, `"subject":"hi"`) {
		t.Fatalf("list: %d %s", code, body)
	}
	if m := s.Inbox.List(Filter{})[0]; m.Source.Site != "" {
		t.Errorf("a single token marks no site: %+v", m.Source)
	}
	if code, _ := call(t, http.MethodGet, srv.URL+"/v1/messages", "", ""); code != http.StatusUnauthorized {
		t.Errorf("no token: %d", code)
	}
	// With split tokens beside it, Token still reads and writes.
	s = NewServer(NewInbox(10), Config{Token: "s3cret", WriteTokens: map[string]string{"w": "client-a"}})
	srv2 := httptest.NewServer(s)
	defer srv2.Close()
	if code, _ := call(t, http.MethodGet, srv2.URL+"/v1/messages", "s3cret", ""); code != http.StatusOK {
		t.Errorf("Token beside write tokens: %d", code)
	}
}

func TestWriteTokensPassProtect(t *testing.T) {
	srv := httptest.NewServer(NewServer(NewInbox(10), Config{ReadToken: "reader", WriteTokens: map[string]string{"wa": "client-a"}}))
	defer srv.Close()
	// A token proves the caller isn't a web page, so a request with one
	// from a browser-like client still passes Protect.
	for _, tok := range []string{"wa", "reader"} {
		req, _ := http.NewRequest(http.MethodPost, srv.URL+"/v1/capture", strings.NewReader(emailCapture("x")))
		req.Header.Set("Content-Type", "text/plain")
		req.Header.Set("Origin", "https://evil.test")
		req.Header.Set(wire.TokenHeader, tok)
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		_ = resp.Body.Close()
		if resp.StatusCode != http.StatusOK {
			t.Errorf("%s: %s", tok, resp.Status)
		}
	}
}
