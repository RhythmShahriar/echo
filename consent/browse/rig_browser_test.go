// Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
// SPDX-License-Identifier: MIT

//go:build browser

package browse

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/RhythmShahriar/echo/consent"
	"github.com/RhythmShahriar/echo/consent/browse/egress"
	"github.com/RhythmShahriar/echo/consent/consenttest"
	"github.com/RhythmShahriar/echo/internal/browser"
	"github.com/RhythmShahriar/echo/internal/testenv"
)

// rig is one Engine for the browser tests, with the fake catalog and
// signal decoders linked.
type rig struct {
	t        *testing.T
	e        *Engine
	b        *browser.Browser
	h        *hub
	internal *egress.Proxy
	plan     *consent.Plan
	guards   []*egress.Proxy
}

// newRig starts an Engine for p.
func newRig(t *testing.T, p *consent.Plan) *rig {
	t.Helper()
	needChrome(t)
	consenttest.Use(t, consent.Stages{Classifier: &consenttest.FakeClassifier{}, Signals: consenttest.FakeSignals{}})
	if p == nil {
		p = consenttest.NewPlan(t)
	}
	p.Browser.NoDownload = true
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	e, err := Start(ctx, p)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(e.Close)
	return &rig{t: t, e: e, b: e.b, h: e.h, internal: e.internal, plan: p}
}

// open opens a run of kind for target and returns its session.
func (g *rig) open(target string, kind consent.RunKind) *session {
	g.t.Helper()
	return g.openRun(target, kind).s
}

// openRun opens a run of kind for target.
func (g *rig) openRun(target string, kind consent.RunKind) *Run {
	g.t.Helper()
	u, err := url.Parse(target)
	if err != nil {
		g.t.Fatal(err)
	}
	tg := consent.Target{URL: u}
	run, err := g.e.Open(context.Background(), tg, kind, testenv.Timeout(30*time.Second))
	if err != nil {
		g.t.Fatal(err)
	}
	gd, _ := g.e.guard(tg)
	g.guards = append(g.guards, gd)
	return run
}

// load navigates and waits for the load event plus a quiet moment.
func (g *rig) load(s *session, raw string) {
	g.t.Helper()
	if err := s.navigate(raw); err != nil {
		g.t.Fatalf("navigate %s: %v", raw, err)
	}
	g.waitFor(s, `document.readyState === "complete"`)
	time.Sleep(testenv.Timeout(300 * time.Millisecond))
}

// waitFor polls a main-world expression until it is true.
func (g *rig) waitFor(s *session, expr string) {
	g.t.Helper()
	deadline := time.Now().Add(testenv.Timeout(20 * time.Second))
	for {
		v, err := s.evalMain(context.Background(), expr)
		if err == nil && string(v) == "true" {
			return
		}
		if time.Now().After(deadline) {
			g.t.Fatalf("waiting for %s: %s %v", expr, v, err)
		}
		time.Sleep(50 * time.Millisecond)
	}
}

// eval runs a main-world expression and decodes its value into v.
func (g *rig) eval(s *session, expr string, v any) {
	g.t.Helper()
	raw, err := s.evalMain(context.Background(), expr)
	if err != nil {
		g.t.Fatalf("eval %s: %v", expr, err)
	}
	if v != nil {
		if err := json.Unmarshal(raw, v); err != nil {
			g.t.Fatalf("eval %s: %s: %v", expr, raw, err)
		}
	}
}

// site is a 127.0.0.1 fixture server answering by Host header, so one
// server stands in for several mapped hosts. Every request is counted
// by host and path.
type site struct {
	srv   *httptest.Server
	mu    sync.Mutex
	pages map[string]string // "host/path" -> body (Content-Type from the extension)
	hdrs  map[string]http.Header
	seen  []*http.Request
}

func newSite(t *testing.T) *site {
	t.Helper()
	s := &site{pages: map[string]string{}, hdrs: map[string]http.Header{}}
	s.srv = httptest.NewServer(http.HandlerFunc(s.serve))
	t.Cleanup(s.srv.Close)
	return s
}

// addr is the server's host:port for --map-host.
func (s *site) addr() string { return strings.TrimPrefix(s.srv.URL, "http://") }

// page serves body at host+path; headers are added to the response.
func (s *site) page(hostPath, body string, headers ...string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.pages[hostPath] = body
	h := http.Header{}
	for i := 0; i+1 < len(headers); i += 2 {
		h.Add(headers[i], headers[i+1])
	}
	s.hdrs[hostPath] = h
}

func (s *site) serve(w http.ResponseWriter, r *http.Request) {
	host := r.Host
	if h, _, ok := strings.Cut(host, ":"); ok && !strings.HasPrefix(host, "[") {
		host = h
	}
	key := host + r.URL.Path
	_, _ = io.Copy(io.Discard, r.Body)
	s.mu.Lock()
	s.seen = append(s.seen, r.Clone(context.Background()))
	body, ok := s.pages[key]
	hdr := s.hdrs[key]
	s.mu.Unlock()
	if !ok {
		http.NotFound(w, r)
		return
	}
	for k, vs := range hdr {
		for _, v := range vs {
			w.Header().Add(k, v)
		}
	}
	if w.Header().Get("Content-Type") == "" {
		switch {
		case strings.HasSuffix(r.URL.Path, ".js"):
			w.Header().Set("Content-Type", "text/javascript")
		case strings.HasSuffix(r.URL.Path, ".gif"):
			w.Header().Set("Content-Type", "image/gif")
		default:
			w.Header().Set("Content-Type", "text/html; charset=utf-8")
		}
	}
	_, _ = io.WriteString(w, body)
}

// hits counts the requests that reached host (and path, when given).
func (s *site) hits(host, path string) int {
	s.mu.Lock()
	defer s.mu.Unlock()
	n := 0
	for _, r := range s.seen {
		h := r.Host
		if i := strings.LastIndex(h, ":"); i > 0 && !strings.HasSuffix(h, "]") {
			h = h[:i]
		}
		if h == host && (path == "" || r.URL.Path == path) {
			n++
		}
	}
	return n
}

// requests returns the requests that reached host.
func (s *site) requests(host string) []*http.Request {
	s.mu.Lock()
	defer s.mu.Unlock()
	var out []*http.Request
	for _, r := range s.seen {
		if strings.HasPrefix(r.Host, host) {
			out = append(out, r)
		}
	}
	return out
}

// requestEvents are a recording's request events.
func requestEvents(rec *Recording) []consent.Event {
	var out []consent.Event
	for _, e := range rec.Events {
		if e.Kind == consent.EvRequest {
			out = append(out, e)
		}
	}
	return out
}

// kinds are the events of kind k.
func kinds(rec *Recording, k consent.EventKind) []consent.Event {
	var out []consent.Event
	for _, e := range rec.Events {
		if e.Kind == k {
			out = append(out, e)
		}
	}
	return out
}
