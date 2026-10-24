// Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
// SPDX-License-Identifier: MIT

package client

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/RhythmShahriar/echo/catcher"
	"github.com/RhythmShahriar/echo/model"
	"github.com/RhythmShahriar/echo/wire"
)

func TestClientRoundTrip(t *testing.T) {
	srv := httptest.NewServer(catcher.NewServer(catcher.NewInbox(100), catcher.Config{Version: "test", Token: "t0k"}))
	defer srv.Close()
	ctx := context.Background()
	c := New(srv.URL, "t0k")
	h, err := c.Health(ctx)
	if err != nil || h.Mode != "seal" || len(h.Providers) == 0 {
		t.Fatalf("health: %+v %v", h, err)
	}
	if err := c.Begin(ctx, "r1", "signup"); err != nil {
		t.Fatal(err)
	}
	go func() {
		time.Sleep(100 * time.Millisecond)
		_, _ = c.Capture(ctx, wire.Capture{Kind: "email", Source: wire.Source{Via: "test"},
			Email: &wire.Email{To: []string{"new@example.com"}, Subject: "Welcome"}})
	}()
	got, err := c.Wait(ctx, Filter{Run: "r1", Action: "signup"}, 1, 300*time.Millisecond, 5*time.Second)
	if err != nil || len(got) != 1 || got[0].Subject != "Welcome" || got[0].Channel != model.Email {
		t.Fatalf("wait: %+v %v", got, err)
	}
	if err := c.End(ctx); err != nil {
		t.Fatal(err)
	}
	m, err := c.Message(ctx, got[0].ID)
	if err != nil || m.ID != got[0].ID {
		t.Fatalf("message: %v", err)
	}
	if n, err := c.Clear(ctx, Filter{Run: "r1"}); err != nil || n != 1 {
		t.Fatalf("clear: %d %v", n, err)
	}
	if list, _ := c.Messages(ctx, Filter{}); len(list) != 0 {
		t.Errorf("after clear: %d", len(list))
	}
}

func TestNotRunning(t *testing.T) {
	_, err := New("http://127.0.0.1:1", "").Health(context.Background())
	if !errors.Is(err, ErrNotRunning) {
		t.Errorf("err = %v", err)
	}
}

func TestAPIErrors(t *testing.T) {
	srv := httptest.NewServer(catcher.NewServer(catcher.NewInbox(10), catcher.Config{Version: "test", Token: "t0k"}))
	defer srv.Close()
	_, err := New(srv.URL, "t0k").Message(context.Background(), "nope")
	if !IsNotFound(err) || err.Error() != "Echo answered 404 Not Found to GET /v1/messages/nope" {
		t.Errorf("unknown message: %v", err)
	}
	_, err = New(srv.URL, "wrong").Health(context.Background())
	if IsNotFound(err) || err == nil || !strings.Contains(err.Error(), "401 Unauthorized to GET /v1/health: missing or wrong") {
		t.Errorf("wrong token: %v", err)
	}
}

func TestLeases(t *testing.T) {
	s := catcher.NewServer(catcher.NewInbox(10), catcher.Config{Version: "test"})
	srv := httptest.NewServer(s)
	defer srv.Close()
	ctx := context.Background()
	c := New(srv.URL, "")
	a, err := c.BeginLease(ctx, "run-a", "signup")
	if err != nil || a == "" {
		t.Fatalf("begin: %q %v", a, err)
	}
	b, _ := c.BeginLease(ctx, "run-b", "signup")
	if m := s.Inbox.Add(model.Message{}); m.Attribution != model.Ambiguous {
		t.Errorf("two open: %+v", m)
	}
	if err := c.EndLease(ctx, b); err != nil {
		t.Fatal(err)
	}
	if m := s.Inbox.Add(model.Message{}); m.Run != "run-a" {
		t.Errorf("EndLease closed the wrong action: %+v", m)
	}
	if err := c.EndLease(ctx, a); err != nil {
		t.Fatal(err)
	}
	if m := s.Inbox.Add(model.Message{}); m.Run != "" {
		t.Errorf("all ended: %+v", m)
	}
}

func TestRenewLease(t *testing.T) {
	srv := httptest.NewServer(catcher.NewServer(catcher.NewInbox(10), catcher.Config{}))
	defer srv.Close()
	ctx := context.Background()
	c := New(srv.URL, "")
	lease, err := c.BeginLease(ctx, "r1", "a")
	if err != nil {
		t.Fatal(err)
	}
	if err := c.RenewLease(ctx, lease); err != nil {
		t.Errorf("renew an open action: %v", err)
	}
	_ = c.EndLease(ctx, lease)
	if err := c.RenewLease(ctx, lease); !IsNotFound(err) {
		t.Errorf("renew an ended action: %v, want a 404", err)
	}
}

// An Echo from before leases answers begin with 204 and no body.
func TestBeginLeaseOnOldEcho(t *testing.T) {
	var ends []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		if r.URL.Path == "/v1/actions/end" {
			ends = append(ends, string(body))
		}
		w.WriteHeader(http.StatusNoContent)
	}))
	defer srv.Close()
	c := New(srv.URL, "")
	lease, err := c.BeginLease(context.Background(), "r1", "a")
	if err != nil || lease != "" {
		t.Fatalf("old Echo: %q %v", lease, err)
	}
	if err := c.EndLease(context.Background(), lease); err != nil || len(ends) != 1 || ends[0] != "" {
		t.Errorf("EndLease without a lease ends the old way: %v %q", err, ends)
	}
}

func TestHealthFields(t *testing.T) {
	srv := httptest.NewServer(catcher.NewServer(catcher.NewInbox(1), catcher.Config{Version: "test", SMTPAddr: "0.0.0.0:1025", BootID: "b1"}))
	defer srv.Close()
	h, err := New(srv.URL, "").Health(context.Background())
	if err != nil || h.SMTP != "0.0.0.0:1025" || h.Proxy != "" || h.BootID != "b1" || h.Store != "memory" || !h.Supports("leases") || h.Supports("nope") {
		t.Errorf("health: %+v %v", h, err)
	}
}

// The health answer of Echo 1.0, before boot IDs and addresses.
func TestHealthFromOldEcho(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = io.WriteString(w, `{"tool":"rhythm-echo","version":"1.0.0","mode":"seal","wire":1,"providers":["twilio"]}`)
	}))
	defer srv.Close()
	h, err := New(srv.URL, "").Health(context.Background())
	if err != nil || h.Version != "1.0.0" || h.SMTP != "" || h.BootID != "" || h.Capabilities != nil || h.Supports("leases") {
		t.Errorf("old health: %+v %v", h, err)
	}
}

func TestBeginExclusive(t *testing.T) {
	srv := httptest.NewServer(catcher.NewServer(catcher.NewInbox(10), catcher.Config{Version: "test"}))
	defer srv.Close()
	ctx := context.Background()
	c := New(srv.URL, "")
	if h, _ := c.Health(ctx); !h.Supports("exclusive") {
		t.Fatalf("capabilities: %v", h.Capabilities)
	}
	lease, err := c.BeginExclusive(ctx, "r1", "pay", 0)
	if err != nil || lease == "" {
		t.Fatalf("begin: %q %v", lease, err)
	}
	_, err = c.BeginExclusive(ctx, "r2", "pay", 20*time.Millisecond)
	if !IsBusy(err) || !strings.Contains(err.Error(), "r1/pay holds Echo exclusively") {
		t.Fatalf("second exclusive: %v", err)
	}
	if err := c.EndLease(ctx, lease); err != nil {
		t.Fatal(err)
	}
	if l, err := c.BeginLease(ctx, "r3", "signup"); err != nil || l == "" || IsBusy(err) {
		t.Fatalf("shared after the exclusive one ended: %q %v", l, err)
	}
}
