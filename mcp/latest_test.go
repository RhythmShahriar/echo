// Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
// SPDX-License-Identifier: MIT

package mcp

import (
	"context"
	"encoding/json"
	"net/http"
	"testing"
	"time"

	"github.com/RhythmShahriar/echo/catcher"
	"github.com/RhythmShahriar/echo/client"
	"github.com/RhythmShahriar/echo/model"
	"github.com/RhythmShahriar/echo/wire"
)

// TestLatestCodeAsksEcho: Echo stores the code masked, so latest_code asks
// it for the code it read first, past a welcome email sent after it.
func TestLatestCodeAsksEcho(t *testing.T) {
	echo := catcher.NewServer(catcher.NewInbox(10), catcher.Config{Version: "test"})
	listen(t, "127.0.0.1:21301", echo)
	for _, e := range []*wire.Email{
		{To: []string{"ann@example.com"}, Subject: "Your code", Text: "Your verification code is 731904."},
		{To: []string{"ann@example.com"}, Subject: "Welcome", Text: "Glad to have you. Reply to 20501 for help."},
	} {
		if _, err := echo.Capture(wire.Capture{Version: wire.Version, Kind: "email", Source: wire.Source{Via: "laravel"}, Email: e}); err != nil {
			t.Fatal(err)
		}
	}
	s := &Server{Echo: client.New("http://127.0.0.1:21301", "")}
	code, id, err := s.latestCode(context.Background(), "ann@", "", "")
	if err != nil || code != "731904" || id == "" {
		t.Errorf("latestCode = %q %q %v", code, id, err)
	}
	if _, _, err := s.latestCode(context.Background(), "nobody@", "", ""); err == nil || err.Error() != "no one-time code in the 0 emails and texts that match" {
		t.Errorf("nobody: %v", err)
	}
	// The masked text never yields a code of its own.
	if _, _, err := s.latestCode(context.Background(), "ann@", "sms", ""); err == nil {
		t.Error("an SMS code where none was sent")
	}
}

// TestLatestCodeOnAnOlderEcho: Echo 1.0.0 has no codes capability and
// stores the text as sent, so latest_code reads the code from it.
func TestLatestCodeOnAnOlderEcho(t *testing.T) {
	msgs := []model.Message{
		{ID: "m1", Channel: model.Email, To: []string{"ann@example.com"}, Subject: "Your code", Text: "Your verification code is 731904.", At: time.Now()},
		{ID: "m2", Channel: model.Email, To: []string{"ann@example.com"}, Subject: "Welcome", Text: "Glad to have you.", At: time.Now()},
	}
	listen(t, "127.0.0.1:21302", http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/v1/health":
			_, _ = w.Write([]byte(`{"ok":true,"version":"1.0.0","mode":"seal"}`))
		case "/v1/messages":
			_ = json.NewEncoder(w).Encode(msgs)
		default:
			http.NotFound(w, r)
		}
	}))
	s := &Server{Echo: client.New("http://127.0.0.1:21302", "")}
	if code, id, err := s.latestCode(context.Background(), "ann@", "", ""); err != nil || code != "731904" || id != "m1" {
		t.Errorf("latestCode = %q %q %v", code, id, err)
	}
}
