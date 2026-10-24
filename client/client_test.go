// Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
// SPDX-License-Identifier: MIT

package client

import (
	"context"
	"errors"
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
