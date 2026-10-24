// Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
// SPDX-License-Identifier: MIT

package catcher

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/RhythmShahriar/echo/model"
)

// fakeClock stands in for time.After: Wait's quiet timers fire only when
// the test says so. Every timer Wait asks for is announced on asked.
type fakeClock struct {
	mu     sync.Mutex
	timers []chan time.Time
	asked  chan struct{}
}

func newFakeClock() *fakeClock { return &fakeClock{asked: make(chan struct{}, 100)} }

func (c *fakeClock) after(time.Duration) <-chan time.Time {
	ch := make(chan time.Time, 1)
	c.mu.Lock()
	c.timers = append(c.timers, ch)
	c.mu.Unlock()
	c.asked <- struct{}{}
	return ch
}

// fire fires the i-th timer Wait asked for (0 is the first).
func (c *fakeClock) fire(i int) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.timers[i] <- time.Time{}
}

func (c *fakeClock) waitAsked(t *testing.T) {
	t.Helper()
	select {
	case <-c.asked:
	case <-time.After(5 * time.Second):
		t.Fatal("Wait never started a quiet timer")
	}
}

func TestWaitCatchesLateMessages(t *testing.T) {
	in := NewInbox(10)
	clk := newFakeClock()
	in.after = clk.after
	lease := in.Begin("r1", "checkout")
	done := make(chan []model.Message, 1)
	go func() { done <- in.Wait(context.Background(), Filter{Run: "r1", Action: "checkout"}, 1, time.Second) }()

	clk.waitAsked(t)
	clk.fire(0) // quiet with nothing arrived yet: keep waiting
	clk.waitAsked(t)
	in.Add(model.Message{Channel: model.Email, Subject: "receipt"})
	clk.waitAsked(t)                              // the receipt starts a new quiet period
	in.Add(model.Message{Channel: model.Webhook}) // async, e.g. Action Scheduler
	clk.waitAsked(t)
	clk.fire(2) // the receipt's period is stale: the webhook restarted it
	clk.fire(3)
	got := <-done
	if len(got) != 2 {
		t.Errorf("want both messages, got %d", len(got))
	}
	in.End(lease)
	if m := in.Add(model.Message{}); m.Run != "" || m.Attribution != "" {
		t.Errorf("after End, messages aren't attributed: %+v", m)
	}
}

func TestLeasesKeepRunsApart(t *testing.T) {
	in := NewInbox(10)
	a := in.Begin("run-a", "signup")
	if m := in.Add(model.Message{Subject: "only one open"}); m.Run != "run-a" || m.Action != "signup" || m.Attribution != "" {
		t.Errorf("one action open: %+v", m)
	}
	b := in.Begin("run-b", "signup")
	if a == "" || b == "" || a == b {
		t.Fatalf("leases %q %q", a, b)
	}
	if m := in.Add(model.Message{Channel: model.Email, Subject: "via SMTP"}); m.Run != "" || m.Action != "" || m.Attribution != model.Ambiguous {
		t.Errorf("two actions open, untagged mail must be ambiguous: %+v", m)
	}
	if m := in.Add(model.Message{Run: "run-b", Action: "signup"}); m.Run != "run-b" || m.Attribution != "" {
		t.Errorf("tagged mail keeps its own run: %+v", m)
	}
	in.End(a)
	if m := in.Add(model.Message{}); m.Run != "run-b" {
		t.Errorf("after a ends, b is the only one: %+v", m)
	}
	in.End(a) // twice is harmless
	in.End(b)
	if m := in.Add(model.Message{}); m.Run != "" || m.Attribution != "" {
		t.Errorf("nothing open: %+v", m)
	}
	if l := in.Begin("", ""); l != "" {
		t.Errorf("an empty begin opens nothing: %q", l)
	}
}

func TestLeasesUnderLoad(t *testing.T) {
	in := NewInbox(10000)
	var wg sync.WaitGroup
	for i := range 50 {
		wg.Go(func() {
			run := fmt.Sprint("run-", i)
			for j := range 20 {
				lease := in.Begin(run, fmt.Sprint("a", j))
				if m := in.Add(model.Message{Run: run, Action: "tagged"}); m.Run != run {
					t.Errorf("tagged mail moved: %+v", m)
				}
				m := in.Add(model.Message{})
				if m.Run != "" && m.Attribution != "" {
					t.Errorf("attributed and ambiguous at once: %+v", m)
				}
				in.End(lease)
			}
		})
	}
	wg.Wait()
	if m := in.Add(model.Message{}); m.Run != "" || m.Attribution != "" {
		t.Errorf("every lease ended, yet: %+v", m)
	}
}

// postJSON posts body to url and returns the status code, decoding the
// answer into out when it's not nil.
func postJSON(t *testing.T, url, body string, out any) int {
	t.Helper()
	req, _ := http.NewRequest(http.MethodPost, url, nil)
	if body != "" {
		req, _ = http.NewRequest(http.MethodPost, url, strings.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = resp.Body.Close() }()
	if out != nil {
		if err := json.NewDecoder(resp.Body).Decode(out); err != nil {
			t.Fatal(err)
		}
	}
	return resp.StatusCode
}

func TestActionsAPILeases(t *testing.T) {
	s := NewServer(NewInbox(10), Config{})
	srv := httptest.NewServer(s)
	defer srv.Close()
	var got struct{ Lease string }
	if code := postJSON(t, srv.URL+"/v1/actions/begin", `{"run":"r1","action":"signup"}`, &got); code != 200 || got.Lease == "" {
		t.Fatalf("begin: %d %+v", code, got)
	}
	other := s.Inbox.begin("r2", "checkout", "192.0.2.7") // another machine's run
	if m := s.Inbox.Add(model.Message{}); m.Attribution != model.Ambiguous {
		t.Errorf("two open: %+v", m)
	}
	if code := postJSON(t, srv.URL+"/v1/actions/end", `{"lease":"`+got.Lease+`"}`, nil); code != http.StatusNoContent {
		t.Fatalf("end: %d", code)
	}
	if m := s.Inbox.Add(model.Message{}); m.Run != "r2" {
		t.Errorf("the lease closed r1 only: %+v", m)
	}
	s.Inbox.End(other)

	// An old client: begin ignores the answer, end sends no body. Only
	// its own action closes, not the other machine's.
	postJSON(t, srv.URL+"/v1/actions/begin", `{"run":"old","action":"a1"}`, nil)
	s.Inbox.begin("r2", "checkout", "192.0.2.7")
	if code := postJSON(t, srv.URL+"/v1/actions/end", "", nil); code != http.StatusNoContent {
		t.Fatalf("old-style end: %d", code)
	}
	if m := s.Inbox.Add(model.Message{}); m.Run != "r2" {
		t.Errorf("an old-style end closes the caller's oldest action: %+v", m)
	}
	if code := postJSON(t, srv.URL+"/v1/actions/end", `{"lease":`, nil); code != http.StatusBadRequest {
		t.Errorf("bad body: %d", code)
	}
}

func TestEndWithoutLeaseClosesOldest(t *testing.T) {
	in := NewInbox(10)
	in.Begin("first", "a")
	in.Begin("second", "a")
	in.End()
	if m := in.Add(model.Message{}); m.Run != "second" {
		t.Errorf("End() closes the oldest: %+v", m)
	}
}

func TestOldStyleEndTreatsLoopbackAsOneCaller(t *testing.T) {
	s := NewServer(NewInbox(10), Config{})
	send := func(path, from, body string) int {
		r := httptest.NewRequest(http.MethodPost, path, strings.NewReader(body))
		r.RemoteAddr, r.Host = from, "127.0.0.1:20380"
		w := httptest.NewRecorder()
		s.ServeHTTP(w, r)
		return w.Code
	}
	if code := send("/v1/actions/begin", "127.0.0.1:50100", `{"run":"old","action":"a1"}`); code != http.StatusOK {
		t.Fatalf("begin: %d", code)
	}
	other := s.Inbox.begin("r2", "checkout", "192.0.2.7")
	defer s.Inbox.End(other)
	if code := send("/v1/actions/end", "[::1]:50101", ""); code != http.StatusNoContent {
		t.Fatalf("end: %d", code)
	}
	if m := s.Inbox.Add(model.Message{}); m.Run != "r2" {
		t.Errorf("an end over ::1 closes the action begun over 127.0.0.1: %+v", m)
	}
}
