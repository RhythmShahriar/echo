// Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
// SPDX-License-Identifier: MIT

package catcher

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/RhythmShahriar/echo/model"
)

func TestPagingIsStableUnderConcurrentAdds(t *testing.T) {
	const n = 10000
	in := NewInbox(2 * n)
	want := make([]string, n)
	for i := range n {
		want[n-1-i] = in.Add(model.Message{Channel: model.Email, Subject: fmt.Sprint("m", i)}).ID
	}
	ctx, cancel := context.WithCancel(context.Background())
	var wg sync.WaitGroup
	wg.Go(func() {
		for i := 0; ctx.Err() == nil; i++ {
			in.Add(model.Message{Channel: model.Email, Subject: fmt.Sprint("new", i)})
		}
	})
	// The first page starts at the newest message right now; after it,
	// pages only go back in time, so new mail never shifts them.
	first, next, err := in.Page(Filter{}, want[0], 50)
	if err != nil {
		t.Fatal(err)
	}
	got := append([]string{want[0]}, ids(first)...)
	for next != "" {
		var page []model.Message
		if page, next, err = in.Page(Filter{}, next, 50); err != nil {
			t.Fatal(err)
		}
		if len(page) == 0 || len(page) > 50 {
			t.Fatalf("page of %d", len(page))
		}
		got = append(got, ids(page)...)
	}
	cancel()
	wg.Wait()
	if strings.Join(got, ",") != strings.Join(want, ",") {
		t.Fatalf("paging gave %d messages, want the %d added before, newest first", len(got), len(want))
	}
}

func TestPageFiltersAndEnds(t *testing.T) {
	in := NewInbox(100)
	var sms []string
	for i := range 7 {
		ch := model.Email
		if i%2 == 0 {
			ch = model.SMS
		}
		m := in.Add(model.Message{Channel: ch})
		if ch == model.SMS {
			sms = append([]string{m.ID}, sms...)
		}
	}
	q, _ := ParseQuery("channel:sms")
	p1, next, _ := in.Page(Filter{Q: q}, "", 2)
	p2, next2, _ := in.Page(Filter{Q: q}, next, 2)
	if got := ids(append(p1, p2...)); strings.Join(got, ",") != strings.Join(sms, ",") || next2 != "" {
		t.Errorf("sms pages %v then next %q, want %v and no next", got, next2, sms)
	}
	// An exact fit: the last page has no next.
	if _, next, _ := in.Page(Filter{Q: q}, "", 4); next != "" {
		t.Errorf("4 of 4: next %q", next)
	}
	if _, _, err := in.Page(Filter{}, "gone", 2); !errors.Is(err, ErrUnknownCursor) {
		t.Errorf("unknown cursor: %v", err)
	}
}

// getJSON GETs path and decodes the answer, returning the status.
func getJSON(t *testing.T, srv *httptest.Server, path string, v any) (int, string) {
	t.Helper()
	resp, err := http.Get(srv.URL + path)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = resp.Body.Close() }()
	b, _ := io.ReadAll(resp.Body)
	if resp.StatusCode == http.StatusOK && v != nil {
		if err := json.Unmarshal(b, v); err != nil {
			t.Fatalf("%s: %v in %s", path, err, b)
		}
	}
	return resp.StatusCode, string(b)
}

func TestMessagesAPIPagesAndSearches(t *testing.T) {
	s := NewServer(NewInbox(100), Config{})
	srv := httptest.NewServer(s)
	defer srv.Close()
	old := s.Inbox.Add(model.Message{Channel: model.Email, Subject: "Welcome", To: []string{"a@example.com"},
		At: time.Now().Add(-time.Hour)})
	for i := range 3 {
		s.Inbox.Add(model.Message{Channel: model.Email, Subject: fmt.Sprint("Order ", i), To: []string{"b@example.com"}})
	}

	// Without limit or before: the array, oldest first, as before.
	var all []model.Message
	if getJSON(t, srv, "/v1/messages", &all); len(all) != 4 || all[0].ID != old.ID {
		t.Errorf("plain list: %d messages", len(all))
	}
	var p page
	getJSON(t, srv, "/v1/messages?limit=2&q="+url.QueryEscape(`subject:order to:b@`), &p)
	if len(p.Messages) != 2 || p.Messages[0].Subject != "Order 2" || p.Next != p.Messages[1].ID {
		t.Fatalf("first page: %+v", p)
	}
	next := p.Next
	p = page{}
	getJSON(t, srv, "/v1/messages?limit=2&q=subject:order&before="+next, &p)
	if len(p.Messages) != 1 || p.Messages[0].Subject != "Order 0" || p.Next != "" {
		t.Errorf("last page: %+v", p)
	}
	if getJSON(t, srv, "/v1/messages?since=10m", &all); len(all) != 3 {
		t.Errorf("since=10m: %d messages, want the 3 new ones", len(all))
	}
	p = page{}
	if getJSON(t, srv, "/v1/messages?before="+old.ID, &p); len(p.Messages) != 0 || p.Next != "" {
		t.Errorf("before the oldest: %+v", p)
	}
	for path, want := range map[string]string{
		"/v1/messages?q=has:pdf":                  "q: has:pdf: has takes attachment or issue",
		"/v1/messages?limit=x":                    "limit: want a number above 0",
		"/v1/messages?limit=0":                    "limit: want a number above 0",
		"/v1/messages?before=nope":                "before: no message with that ID",
		"/v1/wait?q=channel:fax":                  "q: channel:fax: not a channel",
		"/v1/messages?q=" + url.QueryEscape(`"x`): "q: a quote in the search isn't closed",
	} {
		if code, body := getJSON(t, srv, path, nil); code != http.StatusBadRequest || !strings.Contains(body, want) {
			t.Errorf("%s: %d %q, want 400 with %q", path, code, body, want)
		}
	}
}

func TestDeleteOneMessage(t *testing.T) {
	s := NewServer(NewInbox(10), Config{})
	srv := httptest.NewServer(s)
	defer srv.Close()
	m := s.Inbox.Add(model.Message{Channel: model.Email})
	keep := s.Inbox.Add(model.Message{Channel: model.SMS})
	del := func() int {
		req, _ := http.NewRequest(http.MethodDelete, srv.URL+"/v1/messages/"+m.ID, nil)
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		_ = resp.Body.Close()
		return resp.StatusCode
	}
	if code := del(); code != http.StatusNoContent {
		t.Errorf("delete: %d", code)
	}
	if code := del(); code != http.StatusNotFound {
		t.Errorf("delete again: %d", code)
	}
	if _, ok := s.Inbox.Get(m.ID); ok {
		t.Error("still there")
	}
	if got := s.Inbox.List(Filter{}); len(got) != 1 || got[0].ID != keep.ID || s.Inbox.Bytes() != size(keep) {
		t.Errorf("left %v, %d bytes", ids(got), s.Inbox.Bytes())
	}
}

// sse reads server-sent events from body.
type sse struct{ sc *bufio.Scanner }

// next returns the next event's ID and data, skipping comments and retry.
func (s sse) next(t *testing.T) (string, Event) {
	t.Helper()
	var id string
	for s.sc.Scan() {
		line := s.sc.Text()
		switch {
		case strings.HasPrefix(line, "id: "):
			id = strings.TrimPrefix(line, "id: ")
		case strings.HasPrefix(line, "data: "):
			var e Event
			if err := json.Unmarshal([]byte(strings.TrimPrefix(line, "data: ")), &e); err != nil {
				t.Fatal(err)
			}
			return id, e
		}
	}
	t.Fatalf("stream ended: %v", s.sc.Err())
	return "", Event{}
}

func subscribe(t *testing.T, ctx context.Context, srv *httptest.Server, lastID string) sse {
	t.Helper()
	req, _ := http.NewRequestWithContext(ctx, http.MethodGet, srv.URL+"/v1/events", nil)
	if lastID != "" {
		req.Header.Set("Last-Event-ID", lastID)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = resp.Body.Close() })
	if ct := resp.Header.Get("Content-Type"); resp.StatusCode != http.StatusOK || ct != "text/event-stream" {
		t.Fatalf("events: %s %s", resp.Status, ct)
	}
	sc := bufio.NewScanner(resp.Body)
	// The stream opens with a retry line: once it's read, the server
	// has taken its starting point and nothing added after is missed.
	if !sc.Scan() || sc.Text() != "retry: 2000" {
		t.Fatalf("stream start: %q", sc.Text())
	}
	return sse{sc}
}

func TestEventsStreamChanges(t *testing.T) {
	s := NewServer(NewInbox(10), Config{})
	srv := httptest.NewServer(s)
	defer srv.Close()
	s.Inbox.Add(model.Message{Channel: model.Email}) // before subscribing: not sent
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	ev := subscribe(t, ctx, srv, "")
	m := s.Inbox.Add(model.Message{Channel: model.HTTP})
	s.Inbox.Update(m.ID, func(m *model.Message) { m.Status = model.Sent })
	s.Inbox.Remove(m.ID)
	var lastID string
	for _, want := range []string{EventAdd, EventUpdate, EventDelete} {
		id, e := ev.next(t)
		if e.Type != want || e.ID != m.ID {
			t.Fatalf("got %+v, want %s of %s", e, want, m.ID)
		}
		if want == EventUpdate {
			lastID = id
		}
	}
	// Reconnecting with the update's ID replays what came after it.
	again := subscribe(t, ctx, srv, lastID)
	if _, e := again.next(t); e.Type != EventDelete || e.ID != m.ID {
		t.Errorf("resumed: %+v", e)
	}
}

func TestEventsResetWhenTooFarBehind(t *testing.T) {
	in := NewInbox(10)
	seq := in.LastEvent()
	for range 2 * maxEvents {
		in.Add(model.Message{})
	}
	evs, next, _ := in.Events(seq)
	if len(evs) != 1 || evs[0].Type != EventReset || next != in.LastEvent() {
		t.Errorf("far behind: %+v", evs)
	}
	// Ahead of the inbox (a Last-Event-ID from before a restart).
	if evs, _, _ := NewInbox(10).Events(99); len(evs) != 1 || evs[0].Type != EventReset {
		t.Errorf("ahead: %+v", evs)
	}
	if evs, _, _ := in.Events(in.LastEvent()); len(evs) != 0 {
		t.Errorf("up to date: %+v", evs)
	}
}

func TestSlowSubscriberDoesNotBlockAdd(t *testing.T) {
	s := NewServer(NewInbox(100), Config{})
	s.Inbox.SetCheck(nil) // checks aren't what's measured
	srv := httptest.NewServer(s)
	defer srv.Close()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	subscribe(t, ctx, srv, "") // and never read again
	done := make(chan struct{})
	go func() {
		// Far more events than fit in the socket buffers.
		for range 50000 {
			s.Inbox.Add(model.Message{Channel: model.Email, Subject: strings.Repeat("x", 100)})
		}
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(30 * time.Second):
		t.Fatal("Add blocked behind a subscriber that doesn't read")
	}
}
