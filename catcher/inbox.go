// Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
// SPDX-License-Identifier: MIT

// Package catcher is Echo's running side: an inbox of captured messages,
// the HTTP API connectors and tools talk to, the seal policy that decides
// what may leave, and the SMTP sink.
package catcher

import (
	"context"
	"fmt"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/RhythmShahriar/echo/model"
)

// Inbox holds captured messages in memory, newest last.
type Inbox struct {
	mu      sync.Mutex
	msgs    []model.Message
	max     int
	changed chan struct{} // closed and replaced whenever a message is added or updated
	run     string        // the action in progress, for messages that don't say
	action  string
	seq     int
	onAdd   func(model.Message)
}

// NewInbox keeps at most max messages (oldest dropped first).
func NewInbox(max int) *Inbox {
	return &Inbox{max: max, changed: make(chan struct{})}
}

// OnAdd registers a callback for every new or updated message (a log, the
// live view). It's called with the inbox unlocked.
func (in *Inbox) OnAdd(fn func(model.Message)) { in.onAdd = fn }

// Begin marks run/action as in progress: messages captured until End that
// don't carry their own run are attributed to it (SMTP mail, connectors
// that couldn't see the X-Echo-Run header, async sends).
func (in *Inbox) Begin(run, action string) {
	in.mu.Lock()
	in.run, in.action = run, action
	in.mu.Unlock()
}

// End clears the action in progress.
func (in *Inbox) End() { in.Begin("", "") }

// Add stores m, filling its ID, time and run, and returns it.
func (in *Inbox) Add(m model.Message) model.Message {
	in.mu.Lock()
	in.seq++
	if m.At.IsZero() {
		m.At = time.Now().UTC()
	}
	if m.ID == "" {
		m.ID = model.NewID(fmt.Sprint(m.At.UnixNano(), in.seq, m.URL, m.Subject))
	}
	if m.Run == "" && m.Action == "" {
		m.Run, m.Action = in.run, in.action
	}
	in.msgs = append(in.msgs, m)
	if len(in.msgs) > in.max {
		in.msgs = slices.Delete(in.msgs, 0, len(in.msgs)-in.max)
	}
	in.notify()
	in.mu.Unlock()
	if in.onAdd != nil {
		in.onAdd(m)
	}
	return m
}

// Update changes the message with id (e.g. to attach the response once an
// observed request is sent). It reports whether the message was found.
func (in *Inbox) Update(id string, fn func(*model.Message)) bool {
	in.mu.Lock()
	var out model.Message
	found := false
	for i := range in.msgs {
		if in.msgs[i].ID == id {
			fn(&in.msgs[i])
			out, found = in.msgs[i], true
			break
		}
	}
	if found {
		in.notify()
	}
	in.mu.Unlock()
	if found && in.onAdd != nil {
		in.onAdd(out)
	}
	return found
}

func (in *Inbox) notify() {
	close(in.changed)
	in.changed = make(chan struct{})
}

// Get returns the message with id.
func (in *Inbox) Get(id string) (model.Message, bool) {
	in.mu.Lock()
	defer in.mu.Unlock()
	for _, m := range in.msgs {
		if m.ID == id {
			return m, true
		}
	}
	return model.Message{}, false
}

// Filter selects messages. Zero fields match anything.
type Filter struct {
	Run     string
	Action  string
	Channel model.Channel
	To      string // substring of any recipient, case-insensitive
	Since   time.Time
}

func (f Filter) match(m model.Message) bool {
	switch {
	case f.Run != "" && m.Run != f.Run,
		f.Action != "" && m.Action != f.Action,
		f.Channel != "" && m.Channel != f.Channel,
		!f.Since.IsZero() && m.At.Before(f.Since):
		return false
	}
	if f.To != "" {
		to := strings.ToLower(f.To)
		return slices.ContainsFunc(m.To, func(r string) bool { return strings.Contains(strings.ToLower(r), to) })
	}
	return true
}

// List returns the messages matching f, oldest first.
func (in *Inbox) List(f Filter) []model.Message {
	in.mu.Lock()
	defer in.mu.Unlock()
	var out []model.Message
	for _, m := range in.msgs {
		if f.match(m) {
			out = append(out, m)
		}
	}
	return out
}

// Wait returns the messages matching f once at least min of them have
// arrived and nothing new has matched for quiet, or when ctx ends. Async
// senders (cron, queues) keep adding for a while after an action, so
// waiting for quiet catches them.
func (in *Inbox) Wait(ctx context.Context, f Filter, min int, quiet time.Duration) []model.Message {
	last := -1
	timer := time.NewTimer(quiet)
	defer timer.Stop()
	for {
		in.mu.Lock()
		ch := in.changed
		in.mu.Unlock()
		got := in.List(f)
		if len(got) != last {
			last = len(got)
			if !timer.Stop() {
				select {
				case <-timer.C:
				default:
				}
			}
			timer.Reset(quiet)
		}
		select {
		case <-ctx.Done():
			return got
		case <-ch:
		case <-timer.C:
			if len(got) >= min {
				return got
			}
			timer.Reset(quiet)
		}
	}
}

// Clear removes the messages matching f and returns how many.
func (in *Inbox) Clear(f Filter) int {
	in.mu.Lock()
	defer in.mu.Unlock()
	n := len(in.msgs)
	in.msgs = slices.DeleteFunc(in.msgs, f.match)
	in.notify()
	return n - len(in.msgs)
}

// Delete removes the messages of channel with the given IDs and returns
// how many.
func (in *Inbox) Delete(channel model.Channel, ids []string) int {
	in.mu.Lock()
	defer in.mu.Unlock()
	n := len(in.msgs)
	in.msgs = slices.DeleteFunc(in.msgs, func(m model.Message) bool { return m.Channel == channel && slices.Contains(ids, m.ID) })
	in.notify()
	return n - len(in.msgs)
}
