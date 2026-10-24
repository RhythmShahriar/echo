// Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
// SPDX-License-Identifier: MIT

package catcher

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/RhythmShahriar/echo/model"
)

// beginAsync starts BeginScope and returns where its lease and error
// arrive.
func beginAsync(in *Inbox, ctx context.Context, o ScopeOptions) <-chan error {
	done := make(chan error, 1)
	go func() {
		_, err := in.BeginScope(ctx, o)
		done <- err
	}()
	return done
}

// stillWaiting fails the test if done has an answer within a short while.
func stillWaiting(t *testing.T, done <-chan error, what string) {
	t.Helper()
	select {
	case err := <-done:
		t.Fatalf("%s didn't wait: %v", what, err)
	case <-time.After(50 * time.Millisecond):
	}
}

func began(t *testing.T, done <-chan error, what string) {
	t.Helper()
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("%s: %v", what, err)
		}
	case <-time.After(5 * time.Second):
		t.Fatalf("%s is still waiting", what)
	}
}

func TestExclusiveWaitsForOpenActions(t *testing.T) {
	in := NewInbox(10)
	ctx := context.Background()
	a := in.Begin("run-a", "signup")
	b := in.Begin("run-b", "signup")
	done := beginAsync(in, ctx, ScopeOptions{Run: "run-x", Action: "checkout", Exclusive: true})
	stillWaiting(t, done, "an exclusive action with two open")
	in.End(a)
	stillWaiting(t, done, "an exclusive action with one open")
	in.End(b)
	began(t, done, "exclusive")
	// Untagged mail meanwhile is the exclusive action's, unambiguously.
	if m := in.Add(model.Message{Channel: model.Email}); m.Run != "run-x" || m.Action != "checkout" || m.Attribution != "" {
		t.Errorf("mail during the exclusive action: %+v", m)
	}
}

func TestSharedActionWaitsForExclusive(t *testing.T) {
	in := NewInbox(10)
	ctx := context.Background()
	x, err := in.BeginScope(ctx, ScopeOptions{Run: "run-x", Action: "checkout", Exclusive: true})
	if err != nil || x == "" {
		t.Fatalf("exclusive on an idle inbox: %q %v", x, err)
	}
	shared := beginAsync(in, ctx, ScopeOptions{Run: "run-a", Action: "signup"})
	other := beginAsync(in, ctx, ScopeOptions{Run: "run-b", Action: "login", Exclusive: true})
	stillWaiting(t, shared, "a shared action while one is exclusive")
	stillWaiting(t, other, "a second exclusive action")
	in.End(x)
	// One of them opens; the other waits for it.
	select {
	case err := <-shared:
		if err != nil {
			t.Fatal(err)
		}
		stillWaiting(t, other, "an exclusive action behind a shared one")
		in.End(in.scopes[0].lease)
		began(t, other, "exclusive after the shared one")
	case err := <-other:
		if err != nil {
			t.Fatal(err)
		}
		stillWaiting(t, shared, "a shared action behind an exclusive one")
		in.End(in.scopes[0].lease)
		began(t, shared, "shared after the exclusive one")
	case <-time.After(5 * time.Second):
		t.Fatal("neither opened")
	}
}

func TestSharedActionsOverlapAndQueueBehindExclusive(t *testing.T) {
	in := NewInbox(10)
	ctx := context.Background()
	a, _ := in.BeginScope(ctx, ScopeOptions{Run: "run-a", Action: "one"})
	if _, err := in.BeginScope(ctx, ScopeOptions{Run: "run-b", Action: "two"}); err != nil {
		t.Fatalf("shared actions overlap: %v", err)
	}
	if m := in.Add(model.Message{}); m.Attribution != model.Ambiguous {
		t.Errorf("untagged with two open: %+v", m)
	}
	x := beginAsync(in, ctx, ScopeOptions{Run: "run-x", Action: "pay", Exclusive: true})
	stillWaiting(t, x, "exclusive")
	// With an exclusive action waiting, a new shared one waits too, so a
	// stream of them can't keep it out.
	late := beginAsync(in, ctx, ScopeOptions{Run: "run-c", Action: "three"})
	stillWaiting(t, late, "a shared action behind a waiting exclusive one")
	in.End(a)
	in.End(in.scopes[0].lease)
	began(t, x, "exclusive")
	stillWaiting(t, late, "a shared action while the exclusive one is open")
	in.End(in.scopes[0].lease)
	began(t, late, "the shared action after the exclusive one")
}

func TestBeginScopeGivesUp(t *testing.T) {
	in := NewInbox(10)
	in.Begin("run-a", "signup")
	in.Begin("run-b", "")
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()
	_, err := in.BeginScope(ctx, ScopeOptions{Run: "run-x", Exclusive: true})
	if be, ok := errors.AsType[*BusyError](err); !ok || err.Error() !=
		"an exclusive action waits for every open one to end: run-a/signup, run-b are still open" || !be.Exclusive {
		t.Fatalf("err = %v", err)
	}
	if in.queued != 0 {
		t.Errorf("a waiter that gave up is still queued: %d", in.queued)
	}
	// It gave up, so shared actions open again at once.
	if _, err := in.BeginScope(context.Background(), ScopeOptions{Run: "run-c"}); err != nil {
		t.Fatalf("shared after a give-up: %v", err)
	}
	if l, err := in.BeginScope(context.Background(), ScopeOptions{}); l != "" || err != nil {
		t.Errorf("an empty scope opens nothing: %q %v", l, err)
	}
}

func TestBeginAPIExclusive(t *testing.T) {
	s := NewServer(NewInbox(10), Config{})
	srv := httptest.NewServer(s)
	defer srv.Close()
	var out struct{ Lease string }
	if code := postJSON(t, srv.URL+"/v1/actions/begin", `{"run":"r1","action":"pay","exclusive":true}`, &out); code != 200 || out.Lease == "" {
		t.Fatalf("exclusive begin: %d %+v", code, out)
	}
	held := out.Lease
	resp, err := http.Post(srv.URL+"/v1/actions/begin", "application/json", strings.NewReader(`{"run":"r2","action":"signup","timeout":"50ms"}`))
	if err != nil {
		t.Fatal(err)
	}
	b, _ := io.ReadAll(resp.Body)
	_ = resp.Body.Close()
	want := "begin: waited 50ms: r1/pay holds Echo exclusively; end the actions in the way (POST /v1/actions/end) or retry with a longer timeout"
	if resp.StatusCode != http.StatusConflict || strings.TrimSpace(string(b)) != want {
		t.Fatalf("shared begin while held: %s %q", resp.Status, b)
	}
	// Ending the exclusive action lets a waiting begin through.
	done := make(chan int, 1)
	go func() { // not postJSON: t.Fatal only works in the test's own goroutine
		resp, err := http.Post(srv.URL+"/v1/actions/begin", "application/json", strings.NewReader(`{"run":"r2","action":"signup","timeout":"5s"}`))
		if err != nil {
			done <- 0
			return
		}
		_ = resp.Body.Close()
		done <- resp.StatusCode
	}()
	time.Sleep(30 * time.Millisecond)
	postJSON(t, srv.URL+"/v1/actions/end", `{"lease":"`+held+`"}`, nil)
	if code := <-done; code != 200 {
		t.Fatalf("waiting begin: %d", code)
	}
	if code := postJSON(t, srv.URL+"/v1/actions/begin", `{"run":"r3","timeout":"soon"}`, nil); code != http.StatusBadRequest {
		t.Errorf("bad timeout: %d", code)
	}
}
