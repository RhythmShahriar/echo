// Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
// SPDX-License-Identifier: MIT

// Package catcher is Echo's running side: an inbox of captured messages,
// the HTTP API connectors and tools talk to, the seal policy that decides
// what may leave, and the SMTP sink.
package catcher

import (
	"context"
	"crypto/rand"
	"fmt"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/RhythmShahriar/echo/model"
)

// DefaultMaxBytes is how much message data an inbox holds by default.
const DefaultMaxBytes = 256 << 20

// Inbox holds captured messages in memory, newest last.
type Inbox struct {
	mu       sync.Mutex
	msgs     []model.Message
	max      int
	maxBytes int64
	bytes    int64            // sum of size(m) over msgs
	changed  chan struct{}    // closed and replaced whenever a message is added or updated
	scopes   []scope          // the actions in progress, oldest first, for messages that don't say
	closed   map[string]scope // the last action each run closed, for mail that arrives late
	seq      int
	onAdd    func(model.Message)
	check    func(model.Message) []model.Issue // fills Issues; nil leaves them alone
	// after is time.After. Tests swap it for a clock they move by hand, so
	// Wait's quiet period doesn't depend on how fast the machine is.
	after func(time.Duration) <-chan time.Time
	now   func() time.Time // time.Now, swapped by tests that date mail around Begin and End
}

// scope is one action in progress, opened by Begin.
type scope struct {
	lease  string
	run    string
	action string
	owner  string // who opened it (the API caller's address), for an End without a lease
	begun  time.Time
	ended  time.Time // set once closed
}

// NewInbox keeps at most max messages and DefaultMaxBytes of them (oldest
// dropped first).
func NewInbox(max int) *Inbox {
	return &Inbox{max: max, maxBytes: DefaultMaxBytes, changed: make(chan struct{}), closed: map[string]scope{},
		after: time.After, now: time.Now}
}

// SetMaxBytes caps how much message data the inbox holds, dropping the
// oldest messages now if it holds more. The count cap alone let 10,000
// large messages fill gigabytes.
func (in *Inbox) SetMaxBytes(n int64) {
	in.mu.Lock()
	in.maxBytes = n
	in.evict()
	in.mu.Unlock()
}

// Bytes returns how much message data the inbox holds.
func (in *Inbox) Bytes() int64 {
	in.mu.Lock()
	defer in.mu.Unlock()
	return in.bytes
}

// evict drops the oldest messages until both caps hold, always keeping
// the newest one. Called with in.mu held.
func (in *Inbox) evict() {
	n := 0
	for len(in.msgs)-n > 1 && (len(in.msgs)-n > in.max || in.bytes > in.maxBytes) {
		in.bytes -= size(in.msgs[n])
		n++
	}
	if n > 0 {
		in.msgs = slices.Delete(in.msgs, 0, n)
	}
}

// size estimates the memory a message holds: its text fields, which are
// what grows. Fixed-size fields are left out.
func size(m model.Message) int64 {
	n := len(m.ID) + len(m.Run) + len(m.Action) + len(m.From) + len(m.ReplyTo) + len(m.Subject) + len(m.Text) + len(m.HTML) +
		len(m.Event) + len(m.Method) + len(m.URL) + len(m.Body) + len(m.Error) + len(m.LateFor)
	for _, list := range [][]string{m.To, m.CC, m.BCC} {
		for _, v := range list {
			n += len(v)
		}
	}
	for k, v := range m.Headers {
		n += len(k) + len(v)
	}
	if m.Response != nil {
		n += len(m.Response.Body)
	}
	for _, a := range m.Attachments {
		n += len(a.Name) + len(a.Type) + len(a.SHA256)
	}
	for _, i := range m.Issues {
		n += len(i.Check) + len(i.Title) + len(i.Detail) + len(i.Fix) + len(i.Key)
	}
	return int64(n)
}

// OnAdd registers a callback for every new or updated message (a log, the
// live view). It's called with the inbox unlocked.
func (in *Inbox) OnAdd(fn func(model.Message)) { in.onAdd = fn }

// SetCheck makes the inbox find the problems in every message it stores
// (check.Message): Add and Update fill Message.Issues with fn's result, so
// whatever shows a message (the live view, watch, the API) shows its
// problems too, not only a run's report.
func (in *Inbox) SetCheck(fn func(model.Message) []model.Issue) {
	in.mu.Lock()
	in.check = fn
	in.mu.Unlock()
}

// Begin marks run/action as in progress and returns a lease that End
// takes to close it. Messages captured meanwhile that don't carry their
// own run (SMTP mail, connectors that couldn't see the X-Echo-Run header,
// async sends) are attributed to it, as long as it's the only action in
// progress. Several runs may be open at once (two compares, a runner and
// a manual send): untagged mail is then marked ambiguous instead of being
// credited to whichever began last. Mail dated before the open action
// began goes to the action its run closed last instead, marked late (see
// late). An empty run and action open nothing.
func (in *Inbox) Begin(run, action string) string { return in.begin(run, action, "") }

func (in *Inbox) begin(run, action, owner string) string {
	if run == "" && action == "" {
		return ""
	}
	in.mu.Lock()
	sc := scope{lease: rand.Text(), run: run, action: action, owner: owner, begun: in.now()}
	in.scopes = append(in.scopes, sc)
	in.mu.Unlock()
	return sc.lease
}

// End closes the actions with the given leases; an unknown or already
// closed lease is ignored. Called with no lease, it closes the oldest
// action in progress, as End did before leases.
func (in *Inbox) End(leases ...string) {
	in.mu.Lock()
	defer in.mu.Unlock()
	if len(leases) == 0 {
		if len(in.scopes) > 0 {
			in.close(0)
		}
		return
	}
	for i := len(in.scopes) - 1; i >= 0; i-- {
		if slices.Contains(leases, in.scopes[i].lease) {
			in.close(i)
		}
	}
}

// close ends the i-th open action and remembers it as its run's last, so
// mail it caused that arrives during the run's next action can still be
// credited to it. Called with in.mu held.
func (in *Inbox) close(i int) {
	sc := in.scopes[i]
	sc.ended = in.now()
	if sc.run != "" {
		if last, ok := in.closed[sc.run]; !ok || !last.ended.After(sc.ended) {
			in.closed[sc.run] = sc
		}
	}
	in.scopes = slices.Delete(in.scopes, i, i+1)
}

// endOldest closes the oldest action owner opened, for clients that don't
// send their lease. It reports whether there was one.
func (in *Inbox) endOldest(owner string) bool {
	in.mu.Lock()
	defer in.mu.Unlock()
	i := slices.IndexFunc(in.scopes, func(sc scope) bool { return sc.owner == owner })
	if i < 0 {
		return false
	}
	in.close(i)
	return true
}

// attribute fills the run of a message that doesn't name one. Called with
// in.mu held.
func (in *Inbox) attribute(m *model.Message) {
	if m.Run != "" || m.Action != "" {
		return
	}
	if last, ok := in.late(*m); ok {
		m.Run, m.Attribution, m.LateFor = last.run, model.Late, last.action
		return
	}
	switch len(in.scopes) {
	case 0:
	case 1:
		m.Run, m.Action = in.scopes[0].run, in.scopes[0].action
	default:
		m.Attribution = model.Ambiguous
	}
}

// late finds the closed action an untagged message belongs to although it
// arrived during a later one: a queued mail sent after the action's quiet
// period was over. It needs the time the message was written (its Date
// header), and that time must fall before every open action began and
// after the closed action began; otherwise the message is attributed by
// arrival as usual. Only the last closed action of each open run is a
// candidate, and only one may fit. Called with in.mu held.
func (in *Inbox) late(m model.Message) (scope, bool) {
	if len(in.scopes) == 0 {
		return scope{}, false
	}
	sent, ok := sentAt(m.Headers)
	if !ok {
		return scope{}, false
	}
	// A Date header counts whole seconds: the message was written at some
	// point in [sent, sent+1s). It's late only if all of that second is
	// before the open action began.
	end := sent.Add(time.Second)
	for _, sc := range in.scopes {
		if end.After(sc.begun) {
			return scope{}, false
		}
	}
	var found []scope
	for _, sc := range in.scopes {
		last, ok := in.closed[sc.run]
		if ok && end.After(last.begun) && !slices.ContainsFunc(found, func(f scope) bool { return f.lease == last.lease }) {
			found = append(found, last)
		}
	}
	if len(found) != 1 {
		return scope{}, false
	}
	return found[0], true
}

// Add stores m after filling its ID, time, run and issues. It returns
// the stored message.
func (in *Inbox) Add(m model.Message) model.Message {
	in.mu.Lock()
	in.seq++
	seq, check := in.seq, in.check
	in.mu.Unlock()
	if m.At.IsZero() {
		m.At = time.Now().UTC()
	}
	if m.ID == "" {
		m.ID = model.NewID(fmt.Sprint(m.At.UnixNano(), seq, m.URL, m.Subject))
	}
	// Checked unlocked: the rules read up to MaxBody of text and HTML,
	// and SMTP, the proxy and connectors add at the same time.
	if check != nil {
		m.Issues = check(m)
	}
	in.mu.Lock()
	in.attribute(&m)
	in.msgs = append(in.msgs, m)
	in.bytes += size(m)
	in.evict()
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
			before := size(in.msgs[i])
			fn(&in.msgs[i])
			if in.check != nil { // a failed send is a problem the capture didn't have
				in.msgs[i].Issues = in.check(in.msgs[i])
			}
			in.bytes += size(in.msgs[i]) - before
			out, found = in.msgs[i], true
			break
		}
	}
	if found {
		in.evict()
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
	Run string
	// Action matches the action that caused a message, including one
	// that arrived late (LateFor): waiting on an action that has ended
	// still finds the mail it caused.
	Action  string
	Channel model.Channel
	To      string // substring of any recipient, case-insensitive
	Since   time.Time
}

func (f Filter) match(m model.Message) bool {
	switch {
	case f.Run != "" && m.Run != f.Run,
		f.Action != "" && m.Action != f.Action && (m.Attribution != model.Late || m.LateFor != f.Action),
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
	var still <-chan time.Time // fires once nothing new has matched for quiet
	for {
		in.mu.Lock()
		ch := in.changed
		in.mu.Unlock()
		got := in.List(f)
		if len(got) != last {
			last = len(got)
			still = in.after(quiet)
		}
		select {
		case <-ctx.Done():
			return got
		case <-ch:
		case <-still:
			if len(got) >= min {
				return got
			}
			still = in.after(quiet)
		}
	}
}

// Clear removes the messages matching f and returns how many.
func (in *Inbox) Clear(f Filter) int {
	in.mu.Lock()
	defer in.mu.Unlock()
	n := len(in.msgs)
	in.msgs = slices.DeleteFunc(in.msgs, func(m model.Message) bool { return in.drop(f.match(m), m) })
	in.notify()
	return n - len(in.msgs)
}

// Delete removes the messages of channel with the given IDs and returns
// how many.
func (in *Inbox) Delete(channel model.Channel, ids []string) int {
	in.mu.Lock()
	defer in.mu.Unlock()
	n := len(in.msgs)
	in.msgs = slices.DeleteFunc(in.msgs, func(m model.Message) bool {
		return in.drop(m.Channel == channel && slices.Contains(ids, m.ID), m)
	})
	in.notify()
	return n - len(in.msgs)
}

// drop returns del, taking m's size off the total when it's deleted.
// Called with in.mu held.
func (in *Inbox) drop(del bool, m model.Message) bool {
	if del {
		in.bytes -= size(m)
	}
	return del
}
