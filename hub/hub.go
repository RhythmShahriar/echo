// Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
// SPDX-License-Identifier: MIT

// Package hub is Echo for agencies: one catcher that many staging sites
// send to, which keeps what each site sent apart and across restarts.
// Its scheduler runs every site's update window (an --after command such
// as "wp plugin update --all") and compares what each site sends before
// and after.
//
//	rhythm echo hub --store /srv/echo --sites sites.yaml --listen 0.0.0.0:8025 --tls-self-signed
//	rhythm echo schedule "Tue 02:00" --store /srv/echo --sites all --after "wp plugin update --all"
//
// Each site posts with its own write token, which can only add captures;
// reading needs the hub's read token, which sees every site and can filter
// by one (GET /v1/messages?site=client-a). Action markers are kept per
// site, so one site's run never claims another's mail. The inbox is an
// append-only JSON Lines file in the store. TLS keeps the tokens off the
// wire in clear.
package hub

import (
	"crypto/subtle"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/RhythmShahriar/echo/catcher"
	"github.com/RhythmShahriar/echo/model"
	"github.com/RhythmShahriar/echo/wire"
)

// Options configure a hub.
type Options struct {
	Store   string   // the folder for the inbox, TLS files and schedule results
	Sites   *Sites   // from LoadSites
	Mode    string   // seal (default) or observe
	Allow   []string // hosts reachable while sealed
	Keep    int      // messages kept, oldest dropped first; default 10000
	Version string
}

// Hub is the shared catcher: an http.Handler for the API and a source of
// SMTP sinks, both checking tokens and recording which site sent what.
type Hub struct {
	o       Options
	Inbox   *catcher.Inbox
	srv     *catcher.Server
	persist *persister

	mu     sync.Mutex
	siteOf map[string]string // message ID to site ID
	marks  map[string]mark   // the action in progress, by site ("" for every site)

	// Captures run one at a time with cur set to the sending site, so the
	// inbox callback (which has only the message) knows whose it is.
	capture sync.Mutex
	cur     string
}

// SiteHeader names the site an action marker from the read token is for.
// Without it the marker applies to every site, as on a plain catcher.
const SiteHeader = "X-Echo-Site"

// mark is an action in progress (POST /v1/actions/begin): untagged mail
// from its site is attributed to it.
type mark struct{ run, action string }

// New opens the hub's store, loads the inbox kept there and returns the
// hub. Close it to flush the inbox file.
func New(o Options) (*Hub, error) {
	if o.Sites == nil {
		return nil, errors.New("hub: no sites")
	}
	if o.Keep <= 0 {
		o.Keep = 10000
	}
	h := &Hub{o: o, Inbox: catcher.NewInbox(o.Keep), siteOf: map[string]string{}, marks: map[string]mark{}}
	p, recs, err := openPersister(o.Store, o.Keep)
	if err != nil {
		return nil, err
	}
	h.persist = p
	for _, r := range recs {
		h.Inbox.Add(r.Message) // keeps the stored ID, time and run
		h.siteOf[r.Message.ID] = r.Site
	}
	// The server checks no token itself: the hub does, by site.
	h.srv = catcher.NewServer(h.Inbox, catcher.Config{Mode: o.Mode, Allow: o.Allow, Version: o.Version})
	h.Inbox.OnAdd(h.added)
	return h, nil
}

// Close flushes and closes the inbox file.
func (h *Hub) Close() error { return h.persist.close() }

// Mode is seal or observe.
func (h *Hub) Mode() string { return h.srv.Mode() }

// SiteOf returns the site that sent the message with id.
func (h *Hub) SiteOf(id string) string {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.siteOf[id]
}

// added keeps every new or changed message in the store. It runs inside a
// capture, so cur is the site that sent it.
func (h *Hub) added(m model.Message) {
	h.mu.Lock()
	site, ok := h.siteOf[m.ID]
	if !ok {
		site = h.cur
		h.siteOf[m.ID] = site
	}
	h.mu.Unlock()
	if h.persist.append(record{Site: site, Message: m}) {
		h.compact()
	}
}

// compact rewrites the inbox file from what the inbox holds now (after a
// delete, or when the file has grown to twice what is kept).
func (h *Hub) compact() {
	ms := h.Inbox.List(catcher.Filter{})
	h.mu.Lock()
	recs := make([]record, 0, len(ms))
	keep := make(map[string]string, len(ms))
	for _, m := range ms {
		recs = append(recs, record{Site: h.siteOf[m.ID], Message: m})
		keep[m.ID] = h.siteOf[m.ID]
	}
	h.siteOf = keep
	h.mu.Unlock()
	h.persist.rewrite(recs)
}

// as runs fn as a capture from site. The inbox's action in progress is
// set to the site's own for the length of fn, so one site's marker never
// tags another site's mail.
func (h *Hub) as(site string, fn func() error) error {
	h.capture.Lock()
	defer h.capture.Unlock()
	h.cur = site
	h.mu.Lock()
	m, ok := h.marks[site]
	if !ok {
		m = h.marks[""]
	}
	h.mu.Unlock()
	h.Inbox.Begin(m.run, m.action)
	defer func() { h.cur = ""; h.Inbox.End() }()
	return fn()
}

// action serves POST /v1/actions/begin and /end for site ("" for every
// site). The hub keeps the markers itself instead of the catcher, which
// has one for everyone.
func (h *Hub) action(site string, w http.ResponseWriter, r *http.Request) {
	var m mark
	if r.URL.Path == "/v1/actions/begin" {
		var b struct {
			Run    string `json:"run"`
			Action string `json:"action"`
		}
		if err := json.NewDecoder(io.LimitReader(r.Body, 1<<16)).Decode(&b); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		m = mark{b.Run, b.Action}
	}
	h.mu.Lock()
	if m == (mark{}) {
		delete(h.marks, site)
	} else {
		h.marks[site] = m
	}
	h.mu.Unlock()
	w.WriteHeader(http.StatusNoContent)
}

func isAction(r *http.Request) bool {
	return r.Method == http.MethodPost && (r.URL.Path == "/v1/actions/begin" || r.URL.Path == "/v1/actions/end")
}

// tokenSite returns who token belongs to: "read" for the read token, a
// site ID for a write token, "" for anything else. Every token is compared
// in constant time, so timing says nothing about which one nearly matched.
func (h *Hub) tokenSite(token string) string {
	if token == "" {
		return ""
	}
	who := ""
	if subtle.ConstantTimeCompare([]byte(token), []byte(h.o.Sites.readToken)) == 1 {
		who = "read"
	}
	for _, s := range h.o.Sites.Sites {
		if subtle.ConstantTimeCompare([]byte(token), []byte(s.token)) == 1 {
			who = s.ID
		}
	}
	return who
}

func (h *Hub) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	switch who := h.tokenSite(r.Header.Get(wire.TokenHeader)); who {
	case "":
		http.Error(w, "missing or wrong "+wire.TokenHeader, http.StatusUnauthorized)
	case "read":
		h.read(w, r)
	default:
		h.write(who, w, r)
	}
}

// write serves a site's write token: captures, the site's own action
// markers and health, nothing that reads.
func (h *Hub) write(site string, w http.ResponseWriter, r *http.Request) {
	switch {
	case r.Method == http.MethodPost && r.URL.Path == "/v1/capture":
		h.captureHTTP(site, w, r)
	case isAction(r):
		h.action(site, w, r)
	case r.Method == http.MethodGet && r.URL.Path == "/v1/health":
		h.srv.ServeHTTP(w, r)
	default:
		http.Error(w, "a site's write token can only send captures; reading needs the hub's read token", http.StatusForbidden)
	}
}

func (h *Hub) captureHTTP(site string, w http.ResponseWriter, r *http.Request) {
	var c wire.Capture
	if err := json.NewDecoder(io.LimitReader(r.Body, 32<<20)).Decode(&c); err != nil {
		http.Error(w, "bad capture: "+err.Error(), http.StatusBadRequest)
		return
	}
	// The after phase updates a message by ID: only the site that sent it
	// may.
	if c.ID != "" {
		if owner := h.SiteOf(c.ID); owner != "" && owner != site {
			http.Error(w, "that message belongs to another site", http.StatusForbidden)
			return
		}
	}
	var d wire.Decision
	err := h.as(site, func() (err error) {
		d, err = h.srv.Capture(c)
		return err
	})
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	writeJSON(w, http.StatusOK, d)
}

// read serves the read token: the whole API, plus the site filter and the
// site list.
func (h *Hub) read(w http.ResponseWriter, r *http.Request) {
	switch {
	case r.Method == http.MethodGet && r.URL.Path == "/v1/messages":
		h.list(w, r)
	case r.Method == http.MethodGet && r.URL.Path == "/v1/sites":
		h.sites(w)
	case isAction(r):
		site := r.Header.Get(SiteHeader)
		if _, ok := h.o.Sites.Site(site); site != "" && !ok {
			http.Error(w, fmt.Sprintf("no site %q", site), http.StatusNotFound)
			return
		}
		h.action(site, w, r)
	case r.Method == http.MethodDelete:
		h.srv.ServeHTTP(w, r)
		h.compact() // deleted messages stay deleted after a restart
	case r.Method == http.MethodPost && r.URL.Path == "/v1/capture":
		http.Error(w, "the read token can't send captures; each site uses its own write token", http.StatusForbidden)
	default:
		h.srv.ServeHTTP(w, r)
	}
}

// list is GET /v1/messages with the catcher's filters plus site.
func (h *Hub) list(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	f := catcher.Filter{Run: q.Get("run"), Action: q.Get("action"), Channel: model.Channel(q.Get("channel")), To: q.Get("to")}
	if s := q.Get("since"); s != "" {
		if t, err := time.Parse(time.RFC3339Nano, s); err == nil {
			f.Since = t
		}
	}
	site := q.Get("site")
	if site != "" {
		if _, ok := h.o.Sites.Site(site); !ok {
			http.Error(w, fmt.Sprintf("no site %q", site), http.StatusNotFound)
			return
		}
	}
	out := []model.Message{}
	for _, m := range h.Inbox.List(f) {
		if site == "" || h.SiteOf(m.ID) == site {
			out = append(out, m)
		}
	}
	writeJSON(w, http.StatusOK, out)
}

// SiteInfo is one row of GET /v1/sites.
type SiteInfo struct {
	ID       string    `json:"id"`
	Messages int       `json:"messages"`
	Last     time.Time `json:"last,omitzero"`
	Schedule string    `json:"schedule,omitempty"`
}

func (h *Hub) sites(w http.ResponseWriter) {
	rows := map[string]*SiteInfo{}
	var out []*SiteInfo
	for _, s := range h.o.Sites.Sites {
		row := &SiteInfo{ID: s.ID, Schedule: s.Schedule}
		rows[s.ID] = row
		out = append(out, row)
	}
	for _, m := range h.Inbox.List(catcher.Filter{}) {
		if row := rows[h.SiteOf(m.ID)]; row != nil {
			row.Messages++
			if m.At.After(row.Last) {
				row.Last = m.At
			}
		}
	}
	slices.SortStableFunc(out, func(a, b *SiteInfo) int { return strings.Compare(a.ID, b.ID) })
	writeJSON(w, http.StatusOK, out)
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	enc := json.NewEncoder(w)
	enc.SetEscapeHTML(false)
	_ = enc.Encode(v)
}
