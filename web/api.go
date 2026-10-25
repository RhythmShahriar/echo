// Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
// SPDX-License-Identifier: MIT

package web

import (
	"encoding/json"
	"fmt"
	"net/http"
	"slices"
	"sort"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/RhythmShahriar/echo/catcher"
	"github.com/RhythmShahriar/echo/check"
	"github.com/RhythmShahriar/echo/model"
)

// pageSize is how many summaries a page holds and maxPage the most a
// caller may ask for.
const (
	pageSize = 50
	maxPage  = 200
)

// Summary is one row of the list: enough to scan and search by, never a
// body. A page of 50 stays a few tens of KB whatever the messages hold.
type Summary struct {
	ID       string        `json:"id"`
	At       time.Time     `json:"at"`
	Channel  model.Channel `json:"channel"`
	Provider string        `json:"provider,omitempty"`
	Kind     string        `json:"kind"`  // "Email", "Twilio SMS", "Zapier webhook"
	Title    string        `json:"title"` // subject, event or first words
	From     string        `json:"from,omitempty"`
	To       []string      `json:"to,omitempty"`      // at most 3
	MoreTo   int           `json:"more_to,omitempty"` // recipients beyond the 3 in To
	Snippet  string        `json:"snippet,omitempty"` // the start of the plain text, on one line
	Status   model.Status  `json:"status"`
	Run      string        `json:"run,omitempty"`
	Action   string        `json:"action,omitempty"`
	Errors   int           `json:"errors,omitempty"` // content check issues by level
	Warnings int           `json:"warnings,omitempty"`
	Attach   int           `json:"attachments,omitempty"` // how many files it carries
}

// Page is what GET /ui/messages answers.
type Page struct {
	Messages []Summary `json:"messages"`
	// Next is the before= cursor for the following page, "" on the last.
	Next string `json:"next"`
	// Total counts the matching messages, Inbox all of them.
	Total  int    `json:"total"`
	Inbox  int    `json:"inbox"`
	Mode   string `json:"mode"` // seal or observe, for the header
	Facets Facets `json:"facets"`
	// ReadOnly is set for a viewer signed in with a Login: the page then
	// hides Delete and offers Sign out.
	ReadOnly bool `json:"read_only,omitempty"`
	// Relay lists the addresses the relay may release to, when up has
	// one (Options.Relay): the email detail then offers Release.
	Relay []string `json:"relay,omitempty"`
}

// Facets are the values the filters offer, from the whole inbox.
type Facets struct {
	Channels  []string `json:"channels"`
	Actions   []string `json:"actions"`
	Providers []string `json:"providers"`
	Checks    []string `json:"checks"` // check IDs with an issue somewhere
}

// Detail is what GET /ui/messages/{id} answers: the stored message, its
// content checks and the attachments found in its MIME body.
type Detail struct {
	Message     model.Message `json:"message"`
	Kind        string        `json:"kind"`   // as in Summary
	Title       string        `json:"title"`  // as in Summary
	Issues      []model.Issue `json:"issues"` // the content checks' findings, run now
	Attachments []Attachment  `json:"attachments"`
	Raw         string        `json:"raw"` // the MIME source, or the HTTP request as text
}

func (u *UI) list(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	limit := pageSize
	if n, err := strconv.Atoi(q.Get("limit")); err == nil && n > 0 {
		limit = min(n, maxPage)
	}
	query := Parse(q.Get("q"))
	all := u.o.Inbox.List(catcher.Filter{})
	// Newest first; before= starts after the message with that ID.
	start := len(all) - 1
	if b := q.Get("before"); b != "" {
		start = -1
		for i := len(all) - 1; i >= 0; i-- {
			if all[i].ID == b {
				start = i - 1
				break
			}
		}
	}
	p := Page{Messages: []Summary{}, Inbox: len(all), Mode: u.o.Mode, ReadOnly: ReadOnly(r.Context()), Relay: u.o.Relay}
	var facets facetSet
	// Content checks cost up to a few ms for a large email, so they're
	// only run here for the rows on the page and for a query that needs
	// them; the rest come from the cache Notify fills as messages arrive.
	needs := query.needsFacts()
	for i := len(all) - 1; i >= 0; i-- {
		m := all[i]
		f, ok := u.cachedFacts(m)
		if !ok && needs {
			f, ok = u.factsOf(m), true
		}
		facets.add(m, f.issues)
		if !query.Match(m, f) {
			continue
		}
		p.Total++
		if i > start {
			continue
		}
		if len(p.Messages) < limit {
			if !ok {
				f = u.factsOf(m)
			}
			p.Messages = append(p.Messages, summarize(m, f))
		} else if p.Next == "" {
			p.Next = p.Messages[len(p.Messages)-1].ID
		}
	}
	p.Facets = facets.sorted()
	u.prune(all)
	writeJSON(w, p)
}

func (u *UI) get(w http.ResponseWriter, r *http.Request) {
	m, ok := u.o.Inbox.Get(r.PathValue("id"))
	if !ok {
		http.NotFound(w, r)
		return
	}
	f := u.factsOf(m)
	issues, att := f.issues, f.attachments
	if issues == nil {
		issues = []model.Issue{}
	}
	if att == nil {
		att = []Attachment{}
	}
	writeJSON(w, Detail{Message: m, Kind: check.Kind(m), Title: title(m), Issues: issues, Attachments: att, Raw: raw(m)})
}

func (u *UI) remove(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	m, ok := u.o.Inbox.Get(id)
	if !ok {
		http.NotFound(w, r)
		return
	}
	u.o.Inbox.Delete(m.Channel, []string{id})
	u.events.publish("delete", id)
	w.WriteHeader(http.StatusNoContent)
}

// frameCSP is the policy for a message's HTML: no scripts, no requests
// (remote images only when asked) and a sandbox, so the email can't
// reach the page, the API or the token even when opened on its own. Only
// Echo Inbox itself may frame it.
// Email relies on inline styles, which is why it isn't a srcdoc frame:
// that would inherit the page's style-src 'self'.
func frameCSP(images bool) string {
	img := "data:"
	if images {
		img = "* data:"
	}
	return "default-src 'none'; style-src 'unsafe-inline'; font-src data:; img-src " + img +
		"; frame-ancestors 'self'; sandbox allow-popups allow-popups-to-escape-sandbox"
}

// frame serves a message's HTML for the HTML tab's iframe. Links open in
// a new tab; the frame itself never navigates.
func (u *UI) frame(w http.ResponseWriter, r *http.Request) {
	m, ok := u.o.Inbox.Get(r.PathValue("id"))
	if !ok || m.HTML == "" {
		http.NotFound(w, r)
		return
	}
	w.Header().Set("Content-Security-Policy", frameCSP(r.URL.Query().Get("images") == "1"))
	w.Header().Set("X-Frame-Options", "SAMEORIGIN")
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	_, _ = fmt.Fprint(w, `<!doctype html><meta charset="utf-8"><base target="_blank">`, m.HTML)
}

// facts are what the list derives from a message beyond its fields.
type facts struct {
	issues      []model.Issue
	attachments []Attachment
}

// factKey changes whenever a message's checks could: its status or the
// length of what's checked.
func factKey(m model.Message) string {
	return fmt.Sprint(m.Status, len(m.Subject), len(m.Text), len(m.HTML), len(m.Body), m.Response != nil)
}

// cachedFacts returns m's facts when they're cached and current.
func (u *UI) cachedFacts(m model.Message) (facts, bool) {
	c := &u.cache
	c.mu.Lock()
	defer c.mu.Unlock()
	e, ok := c.m[m.ID]
	return e.facts, ok && e.key == factKey(m)
}

// factsOf returns m's content checks and attachments, from the cache when
// m hasn't changed since.
func (u *UI) factsOf(m model.Message) facts {
	if f, ok := u.cachedFacts(m); ok {
		return f
	}
	f := facts{issues: check.Message(m, u.o.Checks), attachments: attachments(m)}
	c := &u.cache
	c.mu.Lock()
	c.m[m.ID] = cached{factKey(m), f}
	c.mu.Unlock()
	return f
}

// prune drops cached facts of messages the inbox no longer holds, once
// they're as many as the live ones.
func (u *UI) prune(all []model.Message) {
	c := &u.cache
	c.mu.Lock()
	defer c.mu.Unlock()
	if len(c.m) <= 2*len(all)+100 {
		return
	}
	keep := make(map[string]cached, len(all))
	for _, m := range all {
		if e, ok := c.m[m.ID]; ok {
			keep[m.ID] = e
		}
	}
	c.m = keep
}

func summarize(m model.Message, f facts) Summary {
	s := Summary{ID: m.ID, At: m.At, Channel: m.Channel, Provider: m.Provider, Kind: check.Kind(m), Title: title(m),
		From: m.From, Status: m.Status, Run: m.Run, Action: m.Action, Snippet: clip(oneLine(m.Text), 140),
		Attach: len(f.attachments)}
	s.To = m.To
	if len(m.To) > 3 {
		s.To, s.MoreTo = m.To[:3], len(m.To)-3
	}
	for _, i := range f.issues {
		switch i.Level {
		case model.Error:
			s.Errors++
		case model.Warning:
			s.Warnings++
		}
	}
	return s
}

// title is what the list shows in bold: the subject, the event, or the
// first words of the text, or else the destination.
func title(m model.Message) string {
	for _, t := range []string{m.Subject, m.Event, oneLine(m.Text)} {
		if t = strings.TrimSpace(t); t != "" {
			return clip(t, 120)
		}
	}
	if m.URL != "" {
		return clip(m.Method+" "+m.URL, 120)
	}
	return "(no subject)"
}

// raw is the message as it went out: the MIME source of an email, or an
// HTTP request written out.
func raw(m model.Message) string {
	if m.Channel == model.Email || m.URL == "" {
		return m.Body
	}
	var b strings.Builder
	fmt.Fprintf(&b, "%s %s\n", m.Method, m.URL)
	keys := make([]string, 0, len(m.Headers))
	for k := range m.Headers {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		fmt.Fprintf(&b, "%s: %s\n", k, m.Headers[k])
	}
	b.WriteString("\n" + m.Body)
	if m.Response != nil {
		fmt.Fprintf(&b, "\n\n--- answered %d ---\n%s", m.Response.Status, m.Response.Body)
	}
	return b.String()
}

func oneLine(s string) string { return strings.Join(strings.Fields(s), " ") }

// clip cuts s to at most n characters, on a character boundary.
func clip(s string, n int) string {
	if utf8.RuneCountInString(s) <= n {
		return s
	}
	return string([]rune(s)[:n-1]) + "…"
}

type facetSet struct{ channels, actions, providers, checks []string }

func (f *facetSet) add(m model.Message, issues []model.Issue) {
	put := func(l *[]string, v string) {
		if v != "" && !slices.Contains(*l, v) {
			*l = append(*l, v)
		}
	}
	put(&f.channels, string(m.Channel))
	put(&f.actions, m.Action)
	put(&f.providers, m.Provider)
	for _, i := range issues {
		put(&f.checks, i.Check)
	}
}

func (f facetSet) sorted() Facets {
	out := Facets{Channels: f.channels, Actions: f.actions, Providers: f.providers, Checks: f.checks}
	for _, l := range []*[]string{&out.Channels, &out.Actions, &out.Providers, &out.Checks} {
		if *l == nil {
			*l = []string{}
		}
		slices.Sort(*l)
	}
	return out
}

func writeJSON(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", "application/json")
	enc := json.NewEncoder(w)
	enc.SetEscapeHTML(false)
	_ = enc.Encode(v)
}
