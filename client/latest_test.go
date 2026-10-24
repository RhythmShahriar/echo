// Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
// SPDX-License-Identifier: MIT

package client

import (
	"context"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/RhythmShahriar/echo/catcher"
	"github.com/RhythmShahriar/echo/codes"
	"github.com/RhythmShahriar/echo/model"
	"github.com/RhythmShahriar/echo/wire"
)

func TestLatest(t *testing.T) {
	srv := httptest.NewServer(catcher.NewServer(catcher.NewInbox(100), catcher.Config{Token: "t0k"}))
	defer srv.Close()
	c := New(srv.URL, "t0k")
	ctx := context.Background()
	if _, err := c.Latest(ctx, LatestQuery{To: "ann@", Wait: 10 * time.Millisecond}); !IsNotFound(err) {
		t.Errorf("nothing yet: %v", err)
	}
	since := time.Now()
	go func() {
		time.Sleep(50 * time.Millisecond)
		_, _ = c.Capture(ctx, wire.Capture{Kind: "email", Source: wire.Source{Via: "node"}, Email: &wire.Email{
			To: []string{"ann@example.com"}, Subject: "Your sign-in code",
			Text: "Use 482913 to sign in, or open https://app.test/login?token=abc123 (one-time link)."}})
	}()
	l, err := c.Latest(ctx, LatestQuery{To: "ann@", Channels: []model.Channel{model.Email, model.SMS}, Since: since, Wait: 10 * time.Second})
	if err != nil {
		t.Fatal(err)
	}
	if l.Code() != "482913" || l.Link(codes.Magic) != "https://app.test/login?token=abc123" || l.Link(codes.Reset) != "" {
		t.Errorf("latest: %+v", l)
	}
	if l.Message.Subject != "Your sign-in code" {
		t.Errorf("message: %+v", l.Message)
	}
	if _, err := New(srv.URL, "").Latest(ctx, LatestQuery{}); err == nil {
		t.Error("without the token Latest should fail")
	}
}
