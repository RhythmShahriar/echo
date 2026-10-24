// Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
// SPDX-License-Identifier: MIT

// Package catcher is Echo's running side: an inbox of captured messages,
// the HTTP API connectors and tools talk to, the seal policy that decides
// what may leave, and the SMTP sink.
package catcher

import (
	"cmp"
	"context"
	"crypto/rand"
	"errors"
	"fmt"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/RhythmShahriar/echo/model"
)

// DefaultMaxBytes is how much message data an inbox holds by default.
const DefaultMaxBytes = 256 << 20

// Inbox holds captured messages, newest last: in memory (NewInbox), or
// also in a folder that outlives a restart (OpenInbox).
type Inbox struct {
	mu       sync.Mutex
	msgs     []*entry          // oldest first
	index    map[string]*entry // by message ID, so Get and Update don't scan
	max      int
	maxBytes int64
	keepFor  time.Duration    // drop messages older than this; 0 keeps them
	j        *journal         // where changes are written; nil for memory only
	stop     chan struct{}    // closed by Close to end the retention ticker
	bytes    int64            // sum of size(m) over msgs
	changed  chan struct{}    // closed and replaced whenever the inbox changes
	events   []Event          // the latest changes, oldest first, for subscribers
	eventSeq uint64           // the Seq of the latest event
	scopes   []scope          // the actions in progress, oldest first, for messages that don't say
	ended    chan struct{}    // closed and replaced whenever an action ends or stops waiting, for BeginScope
	queued   int              // exclusive actions waiting to begin: new shared ones wait behind them
	closed   map[string]scope // the last action each run closed, for mail that arrives late
	// found holds the codes and links read from each message before it
	// was masked, by message ID. It lives in memory only, never with the
	// messages, so nothing that stores or reports them sees it.
	found   map[string]found
	seq     int
	deleted int // messages removed or dropped since the inbox was made, for Mailpit's info
	onAdd   func(model.Message)
	check   func(model.Message) []model.Issue // fills Issues; nil leaves them alone
	// after is time.After. Tests swap it for a clock they move by hand, so
	// Wait's quiet period doesn't depend on how fast the machine is.
	after func(time.Duration) <-chan time.Time
	now   func() time.Time // time.Now, swapped by tests that date mail around Begin and End
}

// entry is a stored message and its place in the inbox.
type entry struct {
	seq  int // rises with every message: a page cursor finds its place by it
	m    model.Message
	disk int64 // bytes of its latest record in the journal, for compaction
}

// scope is one action in progress, opened by Begin.
type scope struct {
	lease  string
	run    string
	action string
	owner  string // who opened it (the API caller's address), for an End without a lease
	begun  time.Time
	ended  time.Time // set once closed
	// exclusive is an action no other may overlap, so all mail that
	// arrives meanwhile is its own.
	exclusive bool
}

// name is how an error names the action: run/action.
func (sc scope) name() string {
	if sc.action == "" {
		return sc.run
	}
	return sc.run + "/" + sc.action
}

// NewInbox keeps at most max messages and DefaultMaxBytes of them (oldest
// dropped first).
func NewInbox(max int) *Inbox {
	return &Inbox{max: max, maxBytes: DefaultMaxBytes, changed: make(chan struct{}), ended: make(chan struct{}), index: map[string]*entry{}, closed: map[string]scope{},
		found: map[string]found{},
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
// the newest one. It also drops every message older than keepFor. Called with in.mu
// held.
func (in *Inbox) evict() {
	n := 0
	var cutoff time.Time
	if in.keepFor > 0 {
		cutoff = in.now().Add(-in.keepFor)
	}
	for n < len(in.msgs) {
		left := len(in.msgs) - n
		over := left > 1 && (left > in.max || in.bytes > in.maxBytes)
		if !over && (cutoff.IsZero() || !in.msgs[n].m.At.Before(cutoff)) {
			break
		}
		in.forget(in.msgs[n])
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

// ScopeOptions describes an action BeginScope opens.
type ScopeOptions struct {
	Run, Action string
	// Exclusive waits until no other action is open, then keeps new ones
	// from opening until it ends: everything captured meanwhile is its
	// own, even untagged SMTP mail. Shared actions (the default) may
	// overlap each other; untagged mail is then ambiguous.
	Exclusive bool
}

// BusyError is why BeginScope gave up: ctx ended while other actions
// kept it from opening.
type BusyError struct {
	Exclusive bool     // the action asked to begin was exclusive
	Holder    string   // run/action of the exclusive action in the way, if one is open
	Open      []string // run/action of each open action an exclusive one waited for
}

func (e *BusyError) Error() string {
	switch {
	case e.Holder != "":
		return e.Holder + " holds Echo exclusively"
	case e.Exclusive:
		verb := " are"
		if len(e.Open) == 1 {
			verb = " is"
		}
		return "an exclusive action waits for every open one to end: " + strings.Join(e.Open, ", ") + verb + " still open"
	}
	return "an exclusive action is waiting to begin; new actions wait behind it"
}

// BeginScope opens an action as Begin does, waiting while other actions
// keep it from opening: an exclusive action waits until none is open, and
// any action waits while an exclusive one is open or waiting (so a stream
// of overlapping shared actions can't keep it out forever). When ctx ends
// first it returns a *BusyError. Begin itself never waits.
func (in *Inbox) BeginScope(ctx context.Context, o ScopeOptions) (string, error) {
	return in.beginScope(ctx, o, "")
}

func (in *Inbox) beginScope(ctx context.Context, o ScopeOptions, owner string) (string, error) {
	if o.Run == "" && o.Action == "" {
		return "", nil
	}
	in.mu.Lock()
	defer in.mu.Unlock()
	if o.Exclusive {
		in.queued++
		defer func() {
			in.queued--
			in.wake() // shared actions waiting behind this one may go now
		}()
	}
	for !in.free(o.Exclusive) {
		ch := in.ended
		in.mu.Unlock()
		select {
		case <-ctx.Done():
			in.mu.Lock()
			return "", in.busy(o.Exclusive)
		case <-ch:
		}
		in.mu.Lock()
	}
	sc := scope{lease: rand.Text(), run: o.Run, action: o.Action, owner: owner, begun: in.now(), exclusive: o.Exclusive}
	in.scopes = append(in.scopes, sc)
	return sc.lease, nil
}

// free reports whether an action may open now. Called with in.mu held.
func (in *Inbox) free(exclusive bool) bool {
	if exclusive {
		return len(in.scopes) == 0
	}
	// This action's own place in queued doesn't count: it isn't exclusive.
	return in.queued == 0 && !slices.ContainsFunc(in.scopes, func(sc scope) bool { return sc.exclusive })
}

// busy says what kept an action from opening. Called with in.mu held.
func (in *Inbox) busy(exclusive bool) *BusyError {
	e := &BusyError{Exclusive: exclusive}
	for _, sc := range in.scopes {
		if sc.exclusive {
			e.Holder = sc.name()
		}
		e.Open = append(e.Open, sc.name())
	}
	return e
}

// wake tells BeginScope calls waiting that the open actions changed.
// Called with in.mu held.
func (in *Inbox) wake() {
	close(in.ended)
	in.ended = make(chan struct{})
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
	in.wake()
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
// header). That time must fall before every open action began and
// after the closed action began; otherwise the message is attributed by
// arrival as usual. Only the last closed action of each open run is a
// candidate. Only one may fit. Called with in.mu held.
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
func (in *Inbox) Add(m model.Message) model.Message { return in.add(m, found{}) }

// add is Add, keeping f for Latest.
func (in *Inbox) add(m model.Message, f found) model.Message {
	in.mu.Lock()
	in.seq++
	seq, check := in.seq, in.check
	in.mu.Unlock()
	if m.At.IsZero() {
		m.At = in.now().UTC()
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
	e := &entry{seq: seq, m: m}
	in.msgs = append(in.msgs, e)
	in.index[m.ID] = e
	if !f.empty() {
		in.found[m.ID] = f
	}
	in.bytes += size(m)
	in.write(opAdd, e)
	in.record(EventAdd, m.ID)
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
	e, found := in.index[id]
	if found {
		before := size(e.m)
		fn(&e.m)
		if in.check != nil { // a failed send is a problem the capture didn't have
			e.m.Issues = in.check(e.m)
		}
		in.bytes += size(e.m) - before
		out = e.m
		in.write(opUpdate, e)
		in.record(EventUpdate, id)
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
	if e, ok := in.index[id]; ok {
		return e.m, true
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
	Q       Query // a search (see ParseQuery)
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
		if !slices.ContainsFunc(m.To, func(r string) bool { return strings.Contains(strings.ToLower(r), to) }) {
			return false
		}
	}
	return f.Q.Match(m)
}

// List returns the messages matching f, oldest first.
func (in *Inbox) List(f Filter) []model.Message {
	in.mu.Lock()
	defer in.mu.Unlock()
	var out []model.Message
	for _, e := range in.msgs {
		if f.match(e.m) {
			out = append(out, e.m)
		}
	}
	return out
}

// ErrUnknownCursor means a page cursor names a message the inbox no
// longer has: it was deleted or dropped to make room.
var ErrUnknownCursor = errors.New("no message with that ID; it was deleted or dropped, so start again without a cursor")

// Page returns up to limit messages matching f, newest first, starting
// after the message before names (from the newest when empty). next is
// the cursor for the following page, empty on the last. Messages added
// meanwhile land before the first page, so paging on never repeats or
// skips one.
func (in *Inbox) Page(f Filter, before string, limit int) (page []model.Message, next string, err error) {
	in.mu.Lock()
	defer in.mu.Unlock()
	end := len(in.msgs)
	if before != "" {
		e, ok := in.index[before]
		if !ok {
			return nil, "", ErrUnknownCursor
		}
		end, _ = slices.BinarySearchFunc(in.msgs, e.seq, func(e *entry, seq int) int { return cmp.Compare(e.seq, seq) })
	}
	page = []model.Message{}
	for i := end - 1; i >= 0; i-- {
		if !f.match(in.msgs[i].m) {
			continue
		}
		if len(page) == limit { // one more: there's a next page
			return page, page[len(page)-1].ID, nil
		}
		page = append(page, in.msgs[i].m)
	}
	return page, "", nil
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
	return in.remove(func(m model.Message) bool { return f.match(m) })
}

// Delete removes the messages of channel with the given IDs and returns
// how many.
func (in *Inbox) Delete(channel model.Channel, ids []string) int {
	in.mu.Lock()
	defer in.mu.Unlock()
	return in.remove(func(m model.Message) bool { return m.Channel == channel && slices.Contains(ids, m.ID) })
}

// Remove deletes the message with id and reports whether it was there.
func (in *Inbox) Remove(id string) bool {
	in.mu.Lock()
	defer in.mu.Unlock()
	if _, ok := in.index[id]; !ok {
		return false
	}
	return in.remove(func(m model.Message) bool { return m.ID == id }) > 0
}

// remove deletes the messages del picks and returns how many. Called with
// in.mu held.
func (in *Inbox) remove(del func(model.Message) bool) int {
	n := len(in.msgs)
	in.msgs = slices.DeleteFunc(in.msgs, func(e *entry) bool {
		if !del(e.m) {
			return false
		}
		in.forget(e)
		return true
	})
	in.notify()
	return n - len(in.msgs)
}

// forget takes e out of the index and the byte total and tells
// subscribers it's gone; the caller takes it out of msgs. Called with
// in.mu held.
func (in *Inbox) forget(e *entry) {
	in.bytes -= size(e.m)
	if in.index[e.m.ID] == e {
		delete(in.index, e.m.ID)
		delete(in.found, e.m.ID)
	}
	in.deleted++
	in.write(opDelete, e)
	in.record(EventDelete, e.m.ID)
}

// Event types.
const (
	EventAdd    = "add"
	EventUpdate = "update"
	EventDelete = "delete"
	// EventReset tells a subscriber that fell too far behind that it
	// missed changes and should read the inbox again.
	EventReset = "reset"
)

// maxEvents is how many recent changes the inbox keeps for subscribers.
// One that falls further behind gets a reset instead.
const maxEvents = 4096

// Event is one change to the inbox.
type Event struct {
	Seq  uint64 `json:"-"` // rises with every change; GET /v1/events sends it as the event ID
	Type string `json:"type"`
	ID   string `json:"id,omitempty"` // the message's
}

// record notes a change for subscribers. Called with in.mu held.
func (in *Inbox) record(typ, id string) {
	in.eventSeq++
	in.events = append(in.events, Event{Seq: in.eventSeq, Type: typ, ID: id})
	if len(in.events) >= 2*maxEvents { // trimmed in bulk, so adding stays cheap
		in.events = slices.Delete(in.events, 0, len(in.events)-maxEvents)
	}
}

// LastEvent returns the Seq of the latest change: a new subscriber asks
// Events for what comes after it.
func (in *Inbox) LastEvent() uint64 {
	in.mu.Lock()
	defer in.mu.Unlock()
	return in.eventSeq
}

// Events returns the changes after seq, the Seq to ask from next time and
// a channel closed at the next change. When the changes after seq are no
// longer kept, events is a single reset. Nothing a subscriber does holds
// the inbox up: Add only appends to a list.
func (in *Inbox) Events(seq uint64) (events []Event, next uint64, changed <-chan struct{}) {
	in.mu.Lock()
	defer in.mu.Unlock()
	switch {
	case seq == in.eventSeq:
		return nil, in.eventSeq, in.changed
	// Ahead of the inbox: a Last-Event-ID from before Echo restarted.
	case seq > in.eventSeq, len(in.events) == 0, seq+1 < in.events[0].Seq:
		return []Event{{Seq: in.eventSeq, Type: EventReset}}, in.eventSeq, in.changed
	}
	i, _ := slices.BinarySearchFunc(in.events, seq+1, func(e Event, seq uint64) int { return cmp.Compare(e.Seq, seq) })
	return slices.Clone(in.events[i:]), in.eventSeq, in.changed
}
