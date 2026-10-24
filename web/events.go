// Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
// SPDX-License-Identifier: MIT

package web

import (
	"encoding/json"
	"fmt"
	"net/http"
	"sync"
	"time"
)

// Event is one line of the event stream: a message was added, updated
// or deleted. The page refetches what it shows; the event carries no
// content, so a stream reveals nothing a page couldn't fetch anyway.
type Event struct {
	Type string `json:"type"` // add, update or delete
	ID   string `json:"id"`
}

// keepAlive is how often an idle stream sends a comment, so proxies and
// browsers don't drop it.
const keepAlive = 25 * time.Second

// hub fans events out to the open streams. A slow stream loses events
// rather than holding up the inbox: publish never blocks and the page
// refetches on the next event it does get.
type hub struct {
	mu     sync.Mutex
	subs   map[chan Event]struct{}
	seen   map[string]bool // IDs announced as added, to tell add from update
	closed bool
	done   chan struct{}
}

func newHub() *hub {
	return &hub{subs: map[chan Event]struct{}{}, seen: map[string]bool{}, done: make(chan struct{})}
}

// publish sends an event; typ "" means add or update, whichever it is.
func (h *hub) publish(typ, id string) {
	h.mu.Lock()
	defer h.mu.Unlock()
	switch {
	case typ == "delete":
		delete(h.seen, id)
	case h.seen[id]:
		typ = "update"
	default:
		typ = "add"
		if len(h.seen) > 20000 { // the inbox keeps 10000; forget evicted IDs at once
			clear(h.seen)
		}
		h.seen[id] = true
	}
	for ch := range h.subs {
		select {
		case ch <- Event{typ, id}:
		default:
		}
	}
}

func (h *hub) subscribe() (chan Event, bool) {
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.closed {
		return nil, false
	}
	ch := make(chan Event, 64)
	h.subs[ch] = struct{}{}
	return ch, true
}

func (h *hub) unsubscribe(ch chan Event) {
	h.mu.Lock()
	delete(h.subs, ch)
	h.mu.Unlock()
}

func (h *hub) close() {
	h.mu.Lock()
	defer h.mu.Unlock()
	if !h.closed {
		h.closed = true
		close(h.done)
	}
}

// stream serves server-sent events until the client goes or the UI is
// closed.
func (u *UI) stream(w http.ResponseWriter, r *http.Request) {
	fl, ok := w.(http.Flusher)
	if !ok {
		http.Error(w, "streaming unsupported", http.StatusInternalServerError)
		return
	}
	ch, ok := u.events.subscribe()
	if !ok {
		http.Error(w, "Echo is stopping", http.StatusServiceUnavailable)
		return
	}
	defer u.events.unsubscribe(ch)
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("X-Accel-Buffering", "no")
	fmt.Fprint(w, "retry: 2000\n: echo inbox\n\n")
	fl.Flush()
	tick := time.NewTicker(keepAlive)
	defer tick.Stop()
	for {
		select {
		case <-r.Context().Done():
			return
		case <-u.events.done:
			return
		case <-tick.C:
			fmt.Fprint(w, ": ping\n\n")
		case e := <-ch:
			b, _ := json.Marshal(e)
			fmt.Fprintf(w, "data: %s\n\n", b)
		}
		fl.Flush()
	}
}
