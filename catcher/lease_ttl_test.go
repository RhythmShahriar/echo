// Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
// SPDX-License-Identifier: MIT

package catcher

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/RhythmShahriar/echo/model"
)

func TestLeakedLeaseExpires(t *testing.T) {
	c := &clock{t: time.Date(2026, 10, 24, 12, 0, 0, 0, time.UTC)}
	in := NewInbox(10)
	in.now = c.now
	var expired []string
	in.OnLeaseExpired(func(name string) { expired = append(expired, name) })
	in.Begin("crashed", "a1") // its runner dies before End
	c.move(time.Minute)
	live := in.Begin("r2", "a1")
	if m := in.Add(model.Message{}); m.Attribution != model.Ambiguous {
		t.Errorf("both open: %+v", m)
	}
	c.move(DefaultLeaseTTL - 2*time.Minute)
	if !in.Renew(live) {
		t.Fatal("renewing an open action failed")
	}
	c.move(2 * time.Minute) // past the leaked lease's TTL, not past r2's renewal
	if m := in.Add(model.Message{}); m.Run != "r2" || m.Action != "a1" || m.Attribution != "" {
		t.Errorf("the leaked action still counts: %+v", m)
	}
	if len(expired) != 1 || expired[0] != "crashed/a1" {
		t.Errorf("expired %q, want crashed/a1 once", expired)
	}
	in.End(live)
	if m := in.Add(model.Message{}); m.Run != "" || m.Attribution != "" {
		t.Errorf("nothing open, yet: %+v", m)
	}
}

func TestRenewAfterExpiry(t *testing.T) {
	c := &clock{t: time.Date(2026, 10, 24, 12, 0, 0, 0, time.UTC)}
	in := NewInbox(10)
	in.now = c.now
	in.SetLeaseTTL(time.Minute)
	l := in.Begin("r1", "a1")
	c.move(time.Minute)
	if in.Renew(l) {
		t.Error("renewed an action whose lease ran out")
	}
	if in.Renew("") || in.Renew("nope") {
		t.Error("renewed an unknown lease")
	}
	if m := in.Add(model.Message{}); m.Run != "" {
		t.Errorf("expired action still credited: %+v", m)
	}
}

func TestOldStyleEndSkipsALeakedAction(t *testing.T) {
	c := &clock{t: time.Date(2026, 10, 24, 12, 0, 0, 0, time.UTC)}
	in := NewInbox(10)
	in.now = c.now
	in.begin("crashed", "a1", "10.0.0.1")
	c.move(DefaultLeaseTTL)
	in.begin("live", "a1", "10.0.0.1")
	if !in.endOldest("10.0.0.1") {
		t.Fatal("no action closed")
	}
	if m := in.Add(model.Message{}); m.Run != "" {
		t.Errorf("the live action is still open: %+v", m)
	}
}

func TestExclusiveBeginOutlastsALeakedAction(t *testing.T) {
	in := NewInbox(10)
	if _, err := in.BeginScope(context.Background(), ScopeOptions{Run: "crashed", TTL: 100 * time.Millisecond}); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if _, err := in.BeginScope(ctx, ScopeOptions{Run: "x", Exclusive: true}); err != nil {
		t.Fatalf("an exclusive begin waited out its timeout behind a leaked action: %v", err)
	}
}

func TestActionsAPITTLAndRenew(t *testing.T) {
	s := NewServer(NewInbox(10), Config{Warn: func(string) {}})
	srv := httptest.NewServer(s)
	defer srv.Close()
	if code := postJSON(t, srv.URL+"/v1/actions/begin", `{"run":"r1","ttl":"soon"}`, nil); code != http.StatusBadRequest {
		t.Errorf("bad ttl: %d", code)
	}
	var got struct{ Lease string }
	if code := postJSON(t, srv.URL+"/v1/actions/begin", `{"run":"r1","ttl":"48h"}`, &got); code != 200 {
		t.Fatalf("begin: %d", code)
	}
	s.Inbox.mu.Lock()
	ttl := s.Inbox.scopes[0].ttl
	s.Inbox.mu.Unlock()
	if ttl != MaxLeaseTTL {
		t.Errorf("ttl %s, want it capped at %s", ttl, MaxLeaseTTL)
	}
	if code := postJSON(t, srv.URL+"/v1/actions/renew", `{"lease":"`+got.Lease+`"}`, nil); code != http.StatusNoContent {
		t.Errorf("renew: %d", code)
	}
	if code := postJSON(t, srv.URL+"/v1/actions/renew", `{"lease":"nope"}`, nil); code != http.StatusNotFound {
		t.Errorf("renew unknown: %d", code)
	}
	if code := postJSON(t, srv.URL+"/v1/actions/renew", `{"lease":`, nil); code != http.StatusBadRequest {
		t.Errorf("renew bad body: %d", code)
	}
}
