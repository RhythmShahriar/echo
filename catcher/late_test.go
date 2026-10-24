// Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
// SPDX-License-Identifier: MIT

package catcher

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/RhythmShahriar/echo/model"
	"github.com/RhythmShahriar/echo/wire"
)

// clock is a time the test sets by hand.
type clock struct{ t time.Time }

func (c *clock) now() time.Time              { return c.t }
func (c *clock) move(d time.Duration)        { c.t = c.t.Add(d) }
func (c *clock) date(d time.Duration) string { return c.t.Add(d).Format(time.RFC1123Z) }

// dated is an untagged email with a Date header.
func dated(date string) model.Message {
	m := model.Message{Channel: model.Email, Subject: "receipt"}
	if date != "" {
		m.Headers = map[string]string{"date": date}
	}
	return m
}

func TestLateMailGoesToItsAction(t *testing.T) {
	start := time.Date(2026, 10, 24, 12, 0, 0, 0, time.UTC)
	tests := []struct {
		name        string
		date        func(c *clock) string // relative to the moment the mail arrives, during action 2
		run, action string
		attribution string
		lateFor     string
	}{
		{"dated during action 1", func(c *clock) string { return c.date(-8 * time.Second) }, "r1", "", model.Late, "a1"},
		{"dated after action 1 ended, before action 2", func(c *clock) string { return c.date(-3 * time.Second) }, "r1", "", model.Late, "a1"},
		// The second action 2 began in still counts as action 2: a Date
		// header can't tell earlier in that second from later.
		{"dated the second action 2 began", func(c *clock) string { return c.date(-1500 * time.Millisecond) }, "r1", "a2", "", ""},
		{"dated during action 2", func(c *clock) string { return c.date(0) }, "r1", "a2", "", ""},
		{"no date", func(*clock) string { return "" }, "r1", "a2", "", ""},
		{"a date that doesn't parse", func(*clock) string { return "yesterday" }, "r1", "a2", "", ""},
		// A site clock far behind Echo's: the date fits no action.
		{"dated before action 1 began", func(c *clock) string { return c.date(-time.Hour) }, "r1", "a2", "", ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			c := &clock{t: start}
			in := NewInbox(10)
			in.now = c.now
			l1 := in.Begin("r1", "a1")
			c.move(5 * time.Second)
			in.End(l1)
			c.move(3500 * time.Millisecond) // action 2 begins at 8.5 s
			in.Begin("r1", "a2")
			c.move(1500 * time.Millisecond) // the mail arrives at 10 s
			m := in.Add(dated(tt.date(c)))
			if m.Run != tt.run || m.Action != tt.action || m.Attribution != tt.attribution || m.LateFor != tt.lateFor {
				t.Errorf("got run %q action %q attribution %q late_for %q, want %q %q %q %q",
					m.Run, m.Action, m.Attribution, m.LateFor, tt.run, tt.action, tt.attribution, tt.lateFor)
			}
		})
	}
}

func TestLateMailNeedsANewerActionOfTheSameRun(t *testing.T) {
	c := &clock{t: time.Date(2026, 10, 24, 12, 0, 0, 0, time.UTC)}
	in := NewInbox(10)
	in.now = c.now
	in.End(in.Begin("r1", "a1"))
	c.move(10 * time.Second)
	// Another run's action is open, and r1 has none: the old date doesn't
	// make r1's mail late, and the open action gets it as before.
	in.Begin("r2", "b1")
	c.move(5 * time.Second)
	if m := in.Add(dated(c.date(-12 * time.Second))); m.Run != "r2" || m.Attribution != "" {
		t.Errorf("only r2 open: %+v", m)
	}
	// With r1's next action open too, the old date picks r1's last.
	in.Begin("r1", "a2")
	c.move(5 * time.Second)
	if m := in.Add(dated(c.date(-17 * time.Second))); m.Run != "r1" || m.Attribution != model.Late || m.LateFor != "a1" {
		t.Errorf("r1 and r2 open: %+v", m)
	}
	// Undated while both are open: ambiguous, as without late mail.
	if m := in.Add(dated("")); m.Run != "" || m.Attribution != model.Ambiguous {
		t.Errorf("undated: %+v", m)
	}
	// Nothing open: nothing is attributed, late or not.
	in.End()
	in.End()
	if m := in.Add(dated(c.date(-17 * time.Second))); m.Run != "" || m.Attribution != "" {
		t.Errorf("nothing open: %+v", m)
	}
}

func TestEndWithoutLeaseRemembersTheAction(t *testing.T) {
	c := &clock{t: time.Date(2026, 10, 24, 12, 0, 0, 0, time.UTC)}
	in := NewInbox(10)
	in.now = c.now
	in.begin("r1", "a1", "10.0.0.1")
	c.move(5 * time.Second)
	in.endOldest("10.0.0.1")
	c.move(5 * time.Second)
	in.Begin("r1", "a2")
	c.move(time.Second)
	if m := in.Add(dated(c.date(-8 * time.Second))); m.LateFor != "a1" {
		t.Errorf("end without a lease forgot the action: %+v", m)
	}
}

func TestWaitFindsLateMail(t *testing.T) {
	s := NewServer(NewInbox(10), Config{})
	srv := httptest.NewServer(s)
	defer srv.Close()
	c := &clock{t: time.Now().Add(-time.Minute)}
	s.Inbox.now = c.now
	s.Inbox.End(s.Inbox.Begin("r1", "a1"))
	c.move(30 * time.Second)
	s.Inbox.Begin("r1", "a2")
	raw := "From: shop@example.com\r\nTo: buyer@example.com\r\nSubject: Your receipt\r\nDate: " + c.date(-20*time.Second) +
		"\r\n\r\nThanks for your order.\r\n"
	post(t, srv, wire.Capture{Version: 1, Kind: "email", Source: wire.Source{Via: "smtp"}, Email: &wire.Email{Raw: raw}})
	for action, want := range map[string]int{"a1": 1, "a2": 0} {
		resp, err := http.Get(srv.URL + "/v1/wait?run=r1&action=" + action + "&count=1&timeout=200ms&quiet=10ms")
		if err != nil {
			t.Fatal(err)
		}
		var got []model.Message
		_ = json.NewDecoder(resp.Body).Decode(&got)
		_ = resp.Body.Close()
		if len(got) != want {
			t.Fatalf("wait for %s: %d messages, want %d", action, len(got), want)
		}
		if want == 1 && (got[0].Attribution != model.Late || got[0].LateFor != "a1" || got[0].Action != "") {
			t.Errorf("wait for %s: %+v", action, got[0])
		}
	}
}
