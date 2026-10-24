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
// site, so one site's run never claims another's mail. Every message
// carries its site (Source.Site), so a search for site:client-a finds
// only that site's mail, in the API and in Echo Inbox, which the read
// token opens (/?token=<read token> once in a browser). The inbox is the
// catcher's persistent one, kept in the store's inbox folder. TLS keeps
// the tokens off the wire in clear.
package hub

import (
	"bytes"
	"crypto/rand"
	"crypto/subtle"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/RhythmShahriar/echo/catcher"
	"github.com/RhythmShahriar/echo/web"
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

// Hub is the shared catcher: an http.Handler for the API and Echo Inbox
// and a source of SMTP sinks, both checking tokens and recording which
// site sent what (each message's Source.Site).
type Hub struct {
	o     Options
	Inbox *catcher.Inbox
	srv   *catcher.Server
	ui    *web.UI
	inner string // the catcher's own token, sent on requests the hub let through

	mu    sync.Mutex
	marks map[string]mark // the action in progress, by site ("" for every site)

	// Captures run one at a time, each with its site's action in
	// progress as the inbox's only one, so one site's marker never tags
	// another site's mail.
	capture sync.Mutex
}

// SiteHeader names the site an action marker from the read token is for.
// Without it the marker applies to every site, as on a plain catcher.
const SiteHeader = "X-Echo-Site"

// mark is an action in progress (POST /v1/actions/begin): untagged mail
// from its site is attributed to it.
type mark struct{ run, action string }

// InboxDir is the folder in the store the hub keeps its inbox in: the
// catcher's persistent inbox, kept across restarts.
const InboxDir = "inbox"

// New opens the hub's store, loads the inbox kept there and returns the
// hub. Close it to close the inbox. An inbox.jsonl an earlier hub kept in
// the store is moved into the inbox once.
func New(o Options) (*Hub, error) {
	if o.Sites == nil {
		return nil, errors.New("hub: no sites")
	}
	if o.Keep <= 0 {
		o.Keep = catcher.DefaultKeep
	}
	if err := os.MkdirAll(o.Store, 0o700); err != nil {
		return nil, err
	}
	inbox, err := catcher.OpenInbox(catcher.InboxOptions{Dir: filepath.Join(o.Store, InboxDir), Keep: o.Keep})
	if err != nil {
		return nil, err
	}
	if err := migrate(o.Store, inbox); err != nil {
		_ = inbox.Close()
		return nil, err
	}
	h := &Hub{o: o, Inbox: inbox, marks: map[string]mark{}}
	// The catcher knows each site's write token, so it marks what a site
	// sends with the site (Source.Site, which site: searches) and lets it
	// update only its own messages. The hub checks every token first; the
	// catcher gets a token of its own, which only the hub sends on what it
	// let in, so the catcher's loopback guard (Host and Origin) doesn't
	// refuse the hub's own name.
	h.inner = rand.Text()
	writes := map[string]string{}
	for _, s := range o.Sites.Sites {
		writes[s.token] = s.ID
	}
	h.srv = catcher.NewServer(inbox, catcher.Config{Mode: o.Mode, Allow: o.Allow, Version: o.Version, Token: h.inner, WriteTokens: writes})
	h.ui = web.New(web.Options{Inbox: inbox, Mode: h.srv.Mode(), AnyHost: true})
	inbox.OnAdd(h.ui.Notify)
	return h, nil
}

// Close ends Echo Inbox's event streams and closes the inbox.
func (h *Hub) Close() error {
	h.ui.Close()
	return h.Inbox.Close()
}

// Mode is seal or observe.
func (h *Hub) Mode() string { return h.srv.Mode() }

// SiteOf returns the site that sent the message with id.
func (h *Hub) SiteOf(id string) string {
	m, _ := h.Inbox.Get(id)
	return m.Source.Site
}

// as runs fn as a capture from site. The inbox's action in progress is
// set to the site's own for the length of fn, so one site's marker never
// tags another site's mail.
func (h *Hub) as(site string, fn func() error) error {
	h.capture.Lock()
	defer h.capture.Unlock()
	h.mu.Lock()
	m, ok := h.marks[site]
	if !ok {
		m = h.marks[""]
	}
	h.mu.Unlock()
	if lease := h.Inbox.Begin(m.run, m.action); lease != "" {
		defer h.Inbox.End(lease)
	}
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

// ServeHTTP serves the API to the read and write tokens, and Echo Inbox
// to the read token: a browser opens /?token=<read token> once and keeps
// it in a cookie (see browser).
func (h *Hub) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	tok := r.Header.Get(wire.TokenHeader)
	if tok == "" && h.inboxUI(r) {
		if h.browser(w, r) {
			return
		}
		if c, err := r.Cookie(tokenCookie); err == nil {
			tok = c.Value
		}
	}
	switch who := h.tokenSite(tok); who {
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
		h.pass(w, r)
	default:
		http.Error(w, "a site's write token can only send captures; reading needs the hub's read token", http.StatusForbidden)
	}
}

// captureHTTP hands a site's capture to the catcher with the site's own
// write token, so the catcher reads the whole capture (wire 2 fields
// included) and marks it with the site.
func (h *Hub) captureHTTP(site string, w http.ResponseWriter, r *http.Request) {
	body, err := io.ReadAll(http.MaxBytesReader(w, r.Body, catcher.MaxCapture))
	if err != nil {
		http.Error(w, fmt.Sprintf("capture too large: Echo accepts up to %d MiB", catcher.MaxCapture>>20), http.StatusRequestEntityTooLarge)
		return
	}
	// The after phase updates a message by ID: only the site that sent it
	// may. The catcher refuses it too; this answers 403 rather than 400.
	var c struct {
		ID string `json:"id"`
	}
	if json.Unmarshal(body, &c) == nil && c.ID != "" {
		if owner := h.SiteOf(c.ID); owner != "" && owner != site {
			http.Error(w, "that message belongs to another site", http.StatusForbidden)
			return
		}
	}
	r = r.Clone(r.Context())
	r.Body, r.ContentLength = io.NopCloser(bytes.NewReader(body)), int64(len(body))
	r.Header.Set("Content-Type", "application/json")
	_ = h.as(site, func() error {
		h.srv.ServeHTTP(w, r)
		return nil
	})
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
	case h.inboxUI(r):
		h.ui.ServeHTTP(w, r)
	case r.Method == http.MethodPost && r.URL.Path == "/v1/capture":
		http.Error(w, "the read token can't send captures; each site uses its own write token", http.StatusForbidden)
	default:
		h.pass(w, r)
	}
}

// tokenCookie keeps the read token for a browser that opened Echo Inbox
// with ?token= once.
const tokenCookie = "echo_hub_token"

// inboxUI reports whether r is for Echo Inbox: its page, its files and
// the routes the page reads.
func (h *Hub) inboxUI(r *http.Request) bool {
	p := r.URL.Path
	return p == "/" || strings.HasPrefix(p, "/assets/") || strings.HasPrefix(p, "/ui/")
}

// browser answers a GET for Echo Inbox that carries ?token=: the read
// token is kept in an HttpOnly, same-site cookie and the browser is sent
// to the address without it, so the token leaves the address bar and
// the history. Any other token is refused. It reports whether it
// answered r.
func (h *Hub) browser(w http.ResponseWriter, r *http.Request) bool {
	q := r.URL.Query()
	if !q.Has("token") || (r.Method != http.MethodGet && r.Method != http.MethodHead) {
		return false
	}
	w.Header().Set("Cache-Control", "no-store")
	if h.tokenSite(q.Get("token")) != "read" {
		http.Error(w, "Echo Inbox needs the hub's read token: open /?token=<read token>", http.StatusUnauthorized)
		return true
	}
	http.SetCookie(w, &http.Cookie{Name: tokenCookie, Value: q.Get("token"), Path: "/", HttpOnly: true,
		SameSite: http.SameSiteStrictMode, Secure: r.TLS != nil})
	q.Del("token")
	back := url.URL{Path: r.URL.Path, RawQuery: q.Encode()}
	if !strings.HasPrefix(back.Path, "/") || strings.HasPrefix(back.Path, "//") || strings.HasPrefix(back.Path, "/\\") {
		back.Path = "/" // "//host" would send the browser to another site
	}
	http.Redirect(w, r, back.String(), http.StatusSeeOther)
	return true
}

// pass hands a request the hub has let in to the catcher, with the
// catcher's own token in place of the site's or the read token.
func (h *Hub) pass(w http.ResponseWriter, r *http.Request) {
	r = r.Clone(r.Context())
	r.Header.Set(wire.TokenHeader, h.inner)
	h.srv.ServeHTTP(w, r)
}

// list is GET /v1/messages: the catcher's, which takes site= (an exact
// site ID) besides its own filters, paging and search (q=site:client-a). An unknown site is 404,
// not an empty list, so a typo doesn't read as a quiet site.
func (h *Hub) list(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	if s := q.Get("since"); s != "" {
		_, terr := time.Parse(time.RFC3339Nano, s)
		d, derr := time.ParseDuration(s)
		if terr != nil && (derr != nil || d <= 0) {
			// The catcher ignores it, which would answer with every
			// message; a caller asking for recent mail would read them
			// as recent.
			http.Error(w, fmt.Sprintf("since %q isn't an RFC 3339 time, like 2026-10-24T12:00:00Z, or a duration, like 2h", s), http.StatusBadRequest)
			return
		}
	}
	if site := q.Get("site"); site != "" {
		if _, ok := h.o.Sites.Site(site); !ok {
			http.Error(w, fmt.Sprintf("no site %q", site), http.StatusNotFound)
			return
		}
	}
	h.pass(w, r)
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
		if row := rows[m.Source.Site]; row != nil {
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
