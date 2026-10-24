// Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
// SPDX-License-Identifier: MIT

package catcher

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"slices"
	"strings"
	"testing"

	"github.com/RhythmShahriar/echo/model"
	"github.com/RhythmShahriar/echo/wire"
)

// wireFixture is a capture and decision pair from the connectors' shared
// conformance kit (conformance/testdata), copied into testdata/wire so
// the catcher is held to what connectors are tested against.
type wireFixture struct {
	Capture  json.RawMessage `json:"capture"`
	Decision wire.Decision   `json:"decision"`
}

func readFixture(t *testing.T, name string) wireFixture {
	t.Helper()
	b, err := os.ReadFile("testdata/wire/" + name)
	if err != nil {
		t.Fatal(err)
	}
	var f wireFixture
	if err := json.Unmarshal(b, &f); err != nil {
		t.Fatal(err)
	}
	return f
}

func TestWireVersionsAccepted(t *testing.T) {
	for _, name := range []string{"capture-v1.json", "capture-v2-site-attachments.json"} {
		t.Run(name, func(t *testing.T) {
			f := readFixture(t, name)
			s := NewServer(NewInbox(10), Config{Mode: f.Decision.Mode})
			srv := httptest.NewServer(s)
			defer srv.Close()
			code, body := call(t, http.MethodPost, srv.URL+"/v1/capture", "", string(f.Capture))
			var d wire.Decision
			if code != http.StatusOK || json.Unmarshal([]byte(body), &d) != nil {
				t.Fatalf("capture: %d %s", code, body)
			}
			if d.Mode != f.Decision.Mode || d.Action != f.Decision.Action || d.ID == "" {
				t.Errorf("decision = %+v, want %+v", d, f.Decision)
			}
			m, _ := s.Inbox.Get(d.ID)
			if m.Subject != "Your receipt" {
				t.Errorf("message: %+v", m)
			}
		})
	}
}

func TestWire2Fields(t *testing.T) {
	f := readFixture(t, "capture-v2-site-attachments.json")
	s := NewServer(NewInbox(10), Config{})
	srv := httptest.NewServer(s)
	defer srv.Close()
	_, body := call(t, http.MethodPost, srv.URL+"/v1/capture", "", string(f.Capture))
	var d wire.Decision
	_ = json.Unmarshal([]byte(body), &d)
	m, _ := s.Inbox.Get(d.ID)
	if m.Source.Site != "client-a" {
		t.Errorf("source.site is kept: %+v", m.Source)
	}
	want := []model.Attachment{{Name: "invoice.pdf", Type: "application/pdf", Size: 13,
		SHA256: "7f0b1c0a44b0a5d1b8d0b6c1c1e0b0f1e2d3c4b5a69788796a5b4c3d2e1f0a9b"}}
	if !slices.Equal(m.Attachments, want) {
		t.Errorf("attachments = %+v, want %+v", m.Attachments, want)
	}

	// A write token's site wins over the one a capture names, so one
	// site can't file mail under another.
	s = NewServer(NewInbox(10), Config{WriteTokens: map[string]string{"wb": "client-b"}})
	srv2 := httptest.NewServer(s)
	defer srv2.Close()
	_, body = call(t, http.MethodPost, srv2.URL+"/v1/capture", "wb", string(f.Capture))
	_ = json.Unmarshal([]byte(body), &d)
	if m, _ := s.Inbox.Get(d.ID); m.Source.Site != "client-b" {
		t.Errorf("the write token's site is kept: %+v", m.Source)
	}
}

func TestWire2AttachmentsCleaned(t *testing.T) {
	x := extras{}
	_ = json.Unmarshal([]byte(`{"email":{"attachments":[{"name":"a.pdf","size":-4,"sha256":"not hex"},{"name":"b","sha256":"ABCDEF"}]}}`), &x)
	got := x.attachments()
	if len(got) != 2 || got[0].Size != 0 || got[0].SHA256 != "" || got[1].SHA256 != "" {
		t.Errorf("attachments = %+v", got)
	}
	if (extras{}).attachments() != nil {
		t.Error("no attachments: nil")
	}
}

func TestWire2AttachmentsYieldToMIME(t *testing.T) {
	raw := "From: a@shop.test\r\nTo: b@example.test\r\nSubject: s\r\nContent-Type: multipart/mixed; boundary=b\r\n\r\n" +
		"--b\r\nContent-Type: text/plain\r\n\r\nhi\r\n" +
		"--b\r\nContent-Type: text/csv\r\nContent-Disposition: attachment; filename=real.csv\r\n\r\nx,y\r\n--b--\r\n"
	c, _ := json.Marshal(map[string]any{"v": 2, "kind": "email", "source": map[string]string{"via": "node"},
		"email": map[string]any{"raw": raw, "attachments": []map[string]any{{"name": "claimed.pdf"}}}})
	s := NewServer(NewInbox(10), Config{})
	srv := httptest.NewServer(s)
	defer srv.Close()
	_, body := call(t, http.MethodPost, srv.URL+"/v1/capture", "", string(c))
	var d wire.Decision
	_ = json.Unmarshal([]byte(body), &d)
	m, _ := s.Inbox.Get(d.ID)
	if len(m.Attachments) != 1 || m.Attachments[0].Name != "real.csv" {
		t.Errorf("the MIME's files win: %+v", m.Attachments)
	}
}

func TestWireVersionRejected(t *testing.T) {
	f := readFixture(t, "broken-v3.json")
	srv := httptest.NewServer(NewServer(NewInbox(10), Config{}))
	defer srv.Close()
	code, body := call(t, http.MethodPost, srv.URL+"/v1/capture", "", string(f.Capture))
	want := "this connector speaks wire 3 and this Echo speaks wire 2 at most; run: rhythm update"
	if code != http.StatusBadRequest || strings.TrimSpace(body) != want {
		t.Errorf("v3: %d %q, want 400 %q", code, body, want)
	}
	code, body = call(t, http.MethodPost, srv.URL+"/v1/capture", "", `{"v":-1,"kind":"email","source":{"via":"node"},"email":{}}`)
	if code != http.StatusBadRequest || !strings.Contains(body, "send 1 to 2") {
		t.Errorf("v -1: %d %q", code, body)
	}
	// A capture without v is read as wire 1: the first connectors left
	// it out.
	if code, body = call(t, http.MethodPost, srv.URL+"/v1/capture", "", `{"kind":"email","source":{"via":"node"},"email":{"subject":"x"}}`); code != http.StatusOK {
		t.Errorf("no v: %d %q", code, body)
	}
}

func TestBadCaptureStillAnswers400(t *testing.T) {
	srv := httptest.NewServer(NewServer(NewInbox(10), Config{}))
	defer srv.Close()
	for _, body := range []string{"", "{", `{"v":"two"}`} {
		if code, got := call(t, http.MethodPost, srv.URL+"/v1/capture", "", body); code != http.StatusBadRequest || !strings.HasPrefix(got, "bad capture: ") {
			t.Errorf("%q: %d %q", body, code, got)
		}
	}
}

// TestSplitRunVectors holds splitRun to the shared X-Echo-Run vectors
// the connectors' wire.VerifyRun is tested with. Unlike VerifyRun, an
// unsigned value is taken (the capture itself carried the token).
func TestSplitRunVectors(t *testing.T) {
	b, err := os.ReadFile("testdata/wire/run-signature.json")
	if err != nil {
		t.Fatal(err)
	}
	var v struct {
		Verify []struct {
			Name, Token, Value, Run, Action string
			OK                              bool
		}
	}
	if err := json.Unmarshal(b, &v); err != nil || len(v.Verify) == 0 {
		t.Fatalf("vectors: %v", err)
	}
	for _, c := range v.Verify {
		run, action := splitRun(c.Value, []string{"", c.Token})
		wantRun, wantAction := c.Run, c.Action
		if !c.OK && c.Run == "" && !strings.Contains(c.Value, ".") {
			// Unsigned without a run/action split: the catcher keeps
			// what it can, as it did before signing existed.
			wantRun, wantAction, _ = strings.Cut(c.Value, "/")
		}
		if run != wantRun || action != wantAction {
			t.Errorf("%s: splitRun(%q) = %q %q, want %q %q", c.Name, c.Value, run, action, wantRun, wantAction)
		}
	}
}

func TestSignedRunCredited(t *testing.T) {
	const sig = "NhOp3HY0JQ76wSoPnn3sZg" // r1/checkout with "secret", from the shared vectors
	s := NewServer(NewInbox(10), Config{Token: "secret"})
	srv := httptest.NewServer(s)
	defer srv.Close()
	for value, want := range map[string]string{
		"r1/checkout." + sig: "r1/checkout",
		"r1/refund." + sig:   "/", // tampered: not credited to anything
		"r1/order.paid":      "r1/order.paid",
	} {
		c, _ := json.Marshal(wire.Capture{Version: 2, Kind: "email", Run: value, Source: wire.Source{Via: "node"},
			Email: &wire.Email{Subject: "x"}})
		_, body := call(t, http.MethodPost, srv.URL+"/v1/capture", "secret", string(c))
		var d wire.Decision
		_ = json.Unmarshal([]byte(body), &d)
		if m, _ := s.Inbox.Get(d.ID); m.Run+"/"+m.Action != want {
			t.Errorf("%q: run %q action %q, want %s", value, m.Run, m.Action, want)
		}
	}

	// A write token's site signs with that token; another site's doesn't count.
	s = NewServer(NewInbox(10), Config{WriteTokens: map[string]string{"secret": "a", "other": "b"}})
	if got := s.runTokens("b"); slices.Contains(got, "secret") {
		t.Errorf("site b's run tokens: %q", got)
	}
	if got := s.runTokens(""); !slices.Contains(got, "secret") || !slices.Contains(got, "other") {
		t.Errorf("no site: every write token: %q", got)
	}
}
