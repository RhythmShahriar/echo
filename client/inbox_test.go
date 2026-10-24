// Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
// SPDX-License-Identifier: MIT

package client

import (
	"context"
	"fmt"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/RhythmShahriar/echo/catcher"
	"github.com/RhythmShahriar/echo/model"
)

func TestListPagesAndSearches(t *testing.T) {
	in := catcher.NewInbox(100)
	srv := httptest.NewServer(catcher.NewServer(in, catcher.Config{Token: "t0k"}))
	defer srv.Close()
	for i := range 5 {
		in.Add(model.Message{Channel: model.Email, Subject: fmt.Sprint("Order ", i)})
	}
	in.Add(model.Message{Channel: model.SMS, Text: "Order 9 shipped"})
	c := New(srv.URL, "t0k")
	ctx := context.Background()
	var got []string
	f := Filter{Q: "channel:email order", Limit: 2}
	for {
		p, err := c.List(ctx, f)
		if err != nil {
			t.Fatal(err)
		}
		for _, m := range p.Messages {
			got = append(got, m.Subject)
		}
		if p.Next == "" {
			break
		}
		f.Before = p.Next
	}
	if fmt.Sprint(got) != "[Order 4 Order 3 Order 2 Order 1 Order 0]" {
		t.Errorf("pages: %v", got)
	}
	// Messages keeps its array, oldest first, and takes a search too.
	if ms, err := c.Messages(ctx, Filter{Q: "shipped", Limit: 1}); err != nil || len(ms) != 1 || ms[0].Channel != model.SMS {
		t.Errorf("messages with q: %v %v", ms, err)
	}
	if _, err := c.List(ctx, Filter{Q: "has:pdf"}); err == nil {
		t.Error("a bad search should fail")
	}
}

func TestEventsAndDeleteOne(t *testing.T) {
	in := catcher.NewInbox(100)
	srv := httptest.NewServer(catcher.NewServer(in, catcher.Config{Token: "t0k"}))
	defer srv.Close()
	c := New(srv.URL, "t0k")
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	if _, err := New(srv.URL, "wrong").Events(ctx); err == nil {
		t.Error("events without the token should fail")
	}
	evs, err := c.Events(ctx)
	if err != nil {
		t.Fatal(err)
	}
	// Events starts from the moment the server takes the request; a
	// message added before that isn't sent. Add until the first arrives.
	var m model.Message
	deadline := time.After(10 * time.Second)
	var first Event
wait:
	for {
		m = in.Add(model.Message{Channel: model.Email})
		select {
		case first = <-evs:
			break wait
		case <-time.After(20 * time.Millisecond):
		case <-deadline:
			t.Fatal("no event")
		}
	}
	if first.Type != EventAdd {
		t.Fatalf("first event %+v", first)
	}
	for first.ID != m.ID { // events for the adds before
		first = <-evs
	}
	if err := c.DeleteOne(ctx, m.ID); err != nil {
		t.Fatal(err)
	}
	if e := <-evs; e.Type != EventDelete || e.ID != m.ID {
		t.Errorf("after delete: %+v", e)
	}
	if err := c.DeleteOne(ctx, m.ID); !IsNotFound(err) {
		t.Errorf("delete again: %v", err)
	}
	cancel()
	for range evs { // closed once ctx ends
	}
}
