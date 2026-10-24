// Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
// SPDX-License-Identifier: MIT

// Package web is Echo Inbox, the live view `rhythm echo up` serves at /:
// one list of everything captured (email, SMS, chat, webhooks, payments,
// analytics), a detail pane with HTML, Text, Headers, Raw, JSON, Checks
// and Attachments tabs, search, filters, delete and live updates.
//
// The page is static and embedded (HTML, CSS and plain JavaScript, no
// CDN), so it costs the same whatever the inbox holds. It reads the inbox
// through a small JSON API under /ui/, one page of 50 summaries at a time,
// and listens for changes on a server-sent event stream. report.HTML
// stays the format for files. A Login (login.go) lets viewers without
// Echo's token sign in to read it.
package web

import (
	"embed"
	"io/fs"
	"net"
	"net/http"
	"strings"
	"sync"

	"github.com/RhythmShahriar/echo/catcher"
	"github.com/RhythmShahriar/echo/check"
	"github.com/RhythmShahriar/echo/model"
)

//go:embed static
var static embed.FS

// CSP is the page's Content-Security-Policy: nothing but Echo's own files.
// Message HTML never runs in the page; it gets its own frame and policy
// (see frameCSP).
const CSP = "default-src 'self'; img-src 'self' data:; style-src 'self'; frame-src 'self'; script-src 'self'"

// Paths the page fetches. The contract test drives exactly these.
const (
	PathMessages = "/ui/messages" // GET: a page of summaries; /{id}: one message; DELETE /{id}
	PathEvents   = "/ui/events"   // GET: server-sent events
)

// Options configures the UI.
type Options struct {
	Inbox  *catcher.Inbox
	Mode   string        // seal or observe, shown in the header
	Checks check.Options // what content checks expect
	// AnyHost serves requests whatever their Host header. Without it only
	// loopback names are served, so a DNS-rebinding page can't read the
	// inbox. up sets it when a token guards the listener.
	AnyHost bool
}

// UI serves Echo Inbox. Feed it every new or updated message with Notify
// (the inbox's OnAdd) and Close it before shutting the server down, so
// open event streams end.
type UI struct {
	o      Options
	mux    *http.ServeMux
	events *hub
	cache  factCache
}

// New returns the UI over o.Inbox.
func New(o Options) *UI {
	u := &UI{o: o, mux: http.NewServeMux(), events: newHub()}
	u.cache.m = map[string]cached{}
	assets, _ := fs.Sub(static, "static")
	files := http.FileServerFS(assets)
	index, _ := fs.ReadFile(assets, "index.html")
	u.mux.HandleFunc("GET /{$}", func(w http.ResponseWriter, r *http.Request) {
		// Written out, not through the file server, which redirects
		// /index.html to ./.
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		_, _ = w.Write(index)
	})
	u.mux.Handle("GET /assets/", http.StripPrefix("/assets", noIndex(files)))
	u.mux.HandleFunc("GET "+PathMessages, u.list)
	u.mux.HandleFunc("GET "+PathMessages+"/{id}", u.get)
	u.mux.HandleFunc("GET "+PathMessages+"/{id}/html", u.frame)
	u.mux.HandleFunc("DELETE "+PathMessages+"/{id}", u.remove)
	u.mux.HandleFunc("GET "+PathEvents, u.stream)
	return u
}

// Handler is the UI as an http.Handler.
func (u *UI) Handler() http.Handler { return u }

func (u *UI) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if !u.o.AnyHost && !loopbackHost(r.Host) {
		http.Error(w, "Echo Inbox answers only to localhost", http.StatusForbidden)
		return
	}
	// Browsers send Origin on a cross-site DELETE; only Echo's own page
	// may delete.
	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		if o := r.Header.Get("Origin"); o != "" && o != "http://"+r.Host && o != "https://"+r.Host {
			http.Error(w, "Echo Inbox refuses requests from other pages", http.StatusForbidden)
			return
		}
	}
	h := w.Header()
	h.Set("Content-Security-Policy", CSP)
	h.Set("X-Content-Type-Options", "nosniff")
	h.Set("Referrer-Policy", "no-referrer")
	h.Set("Cache-Control", "no-store")
	u.mux.ServeHTTP(w, r)
}

// Notify tells open pages that m was added or changed. It runs m's
// content checks once, here, so the list never has to run them for the
// whole inbox.
func (u *UI) Notify(m model.Message) {
	u.factsOf(m)
	u.events.publish("", m.ID)
}

// Issues returns m's content checks, run once per version of m.
func (u *UI) Issues(m model.Message) []model.Issue { return u.factsOf(m).issues }

// Close ends every open event stream. http.Server.Shutdown waits for
// active connections and a stream never ends on its own.
func (u *UI) Close() { u.events.close() }

// noIndex refuses directory listings of the assets.
func noIndex(h http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasSuffix(r.URL.Path, "/") {
			http.NotFound(w, r)
			return
		}
		h.ServeHTTP(w, r)
	})
}

// loopbackHost reports whether a Host header names this machine.
func loopbackHost(hostport string) bool {
	h := hostport
	if host, _, err := net.SplitHostPort(hostport); err == nil {
		h = host
	}
	h = strings.ToLower(strings.Trim(h, "[]"))
	if h == "localhost" || strings.HasSuffix(h, ".localhost") {
		return true
	}
	ip := net.ParseIP(h)
	return ip != nil && ip.IsLoopback()
}

// factCache keeps each message's content checks and attachments, so a
// page of the list doesn't redo them for the whole inbox. An entry is
// redone when the message changes (its status or content lengths).
type factCache struct {
	mu sync.Mutex
	m  map[string]cached
}

type cached struct {
	key   string
	facts facts
}
