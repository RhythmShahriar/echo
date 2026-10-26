// Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
// SPDX-License-Identifier: MIT

package server

import (
	"bytes"
	"crypto/tls"
	"embed"
	"encoding/json"
	"fmt"
	"io"
	"io/fs"
	"log"
	"mime"
	"net"
	"net/http"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"strings"
	"text/template"
	"time"
)

// static holds the stand-in tags, the CMP replicas and the vendored
// open-source CMPs.
//
//go:embed static
var static embed.FS

// Static is the embedded static/ folder: tags/, cmps/.
func Static() fs.FS {
	sub, err := fs.Sub(static, "static")
	if err != nil {
		panic(err) // the folder is embedded at build time
	}
	return sub
}

// Default addresses of Go mode (PRD 18.1: 127.0.0.1:23400-23439).
const (
	DefaultListen = "127.0.0.1:23400"
	DefaultAdmin  = "127.0.0.1:23401"
)

// Options configure a Server.
type Options struct {
	// Root is the testbed/consent folder: sites/ and state/ are read from it.
	Root string
	// CA, when set, serves https beside http on the site listener.
	CA *CA
	// Admin is the admin listener's address, given to the ssrf-probe
	// fixture as the private address it must not reach.
	Admin string
	// EchoProxy is Echo's HTTP proxy (rhythm-echo up) for c18's
	// server-side event. Without it the event is never sent: the
	// server makes no outbound request of its own.
	EchoProxy string
	// EchoCA is the PEM file of Echo's CA, which signs what its proxy
	// answers for graph.facebook.com.
	EchoCA string
	// Static replaces the embedded static/ folder, for tests.
	Static fs.FS
	// Log receives one line per notable event; nil discards them.
	Log func(string)
}

// Server serves every scenario site by its host name, the stand-in tags
// and CMP replicas on the tracker and CMP hosts. It counts every
// tracker hit that reaches it.
type Server struct {
	opts     Options
	sitesDir string
	stateDir string
	sink     *sink
	capi     *capiSender
}

// New returns a Server for the testbed folder in opts.Root.
func New(opts Options) (*Server, error) {
	sites := filepath.Join(opts.Root, "sites")
	if fi, err := os.Stat(sites); err != nil || !fi.IsDir() {
		return nil, fmt.Errorf("consent testbed: no sites folder in %s (pass -root testbed/consent)", opts.Root)
	}
	if opts.Static == nil {
		opts.Static = Static()
	}
	s := &Server{opts: opts, sitesDir: sites, stateDir: filepath.Join(opts.Root, "state"), sink: newSink()}
	if opts.EchoProxy != "" {
		c, err := newCAPISender(opts.EchoProxy, opts.EchoCA)
		if err != nil {
			return nil, err
		}
		s.capi = c
	}
	return s, nil
}

func (s *Server) logf(format string, args ...any) {
	if s.opts.Log != nil {
		s.opts.Log(fmt.Sprintf(format, args...))
	}
}

// Serve serves the sites and tags on l until it is closed: plain HTTP,
// and HTTPS too when the server has a CA.
func (s *Server) Serve(l net.Listener) error {
	var cfg *tls.Config
	if s.opts.CA != nil {
		cfg = s.opts.CA.TLSConfig()
	}
	// Handshakes Chrome abandons (its own background requests, a test's
	// self-signed check) are expected; they'd only be noise in the log.
	srv := &http.Server{Handler: s.Handler(), ReadHeaderTimeout: 10 * time.Second, ErrorLog: log.New(io.Discard, "", 0)}
	return srv.Serve(newSniffListener(l, cfg))
}

// ServeAdmin serves the admin and sink endpoints on l until it is closed.
func (s *Server) ServeAdmin(l net.Listener) error {
	srv := &http.Server{Handler: s.AdminHandler(), ReadHeaderTimeout: 10 * time.Second}
	return srv.Serve(l)
}

// Count is what reached the sink for scenario ("" for every scenario).
func (s *Server) Count(scenario string) Count { return s.sink.count(scenario) }

// Hits is the tracker hits recorded for scenario, oldest first.
func (s *Server) Hits(scenario string) []Hit { return s.sink.recordedHits(scenario) }

// Reset clears the sink's counters.
func (s *Server) Reset() { s.sink.reset() }

// ResetScenario clears the sink's counters for one scenario.
func (s *Server) ResetScenario(id string) { s.sink.resetScenario(id) }

// hostOnly is the lower-case host name of a Host header, without port
// or trailing dot.
func hostOnly(h string) string {
	if host, _, err := net.SplitHostPort(h); err == nil {
		h = host
	}
	return strings.TrimSuffix(strings.ToLower(h), ".")
}

// siteOf splits a scenario host: metrics.c12.shop.test is site c12 with
// sub-host metrics.
func siteOf(host string) (id, sub string, ok bool) {
	rest, found := strings.CutSuffix(host, SiteSuffix)
	if !found || rest == "" {
		return "", "", false
	}
	if i := strings.LastIndexByte(rest, '.'); i >= 0 {
		sub, rest = rest[:i], rest[i+1:]
	}
	return rest, sub, validID(rest)
}

// Handler serves the site listener: scenario sites, stand-in tags and
// the sink.
func (s *Server) Handler() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		host := hostOnly(r.Host)
		w.Header().Set("Cache-Control", "no-store")
		id, sub, isSite := siteOf(host)
		_, isTag := tagHosts[host]
		switch {
		case (isSite || isTag) && isHit(host, r.URL.Path):
			s.sink.hit(r, host)
			answerHit(w, r)
		case isSite && s.HasSite(id):
			s.serveSite(w, r, id, sub)
		case isTag:
			s.serveTag(w, r, host)
		default:
			s.sink.countUnmapped(r, host)
			s.logf("unmapped host %s%s", host, r.URL.Path)
			http.Error(w, "not a consent testbed host: "+host, http.StatusMisdirectedRequest)
		}
	})
}

// gif is a 1x1 transparent GIF, the answer to an image hit.
var gif = []byte("GIF89a\x01\x00\x01\x00\x80\x00\x00\x00\x00\x00\xff\xff\xff!\xf9\x04\x01\x00\x00\x00\x00,\x00\x00\x00\x00\x01\x00\x01\x00\x00\x02\x02D\x01\x00;")

// answerHit answers a tracker hit the way the endpoints do: an image
// for GET, no content for a POST or beacon.
func answerHit(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Access-Control-Allow-Origin", "*")
	if r.Method == http.MethodGet {
		w.Header().Set("Content-Type", "image/gif")
		_, _ = w.Write(gif)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// serveTag serves a stand-in file on a tracker or CMP host.
func (s *Server) serveTag(w http.ResponseWriter, r *http.Request, host string) {
	for _, rt := range tagHosts[host] {
		if rt.file == "" || !strings.HasPrefix(r.URL.Path, rt.prefix) {
			continue
		}
		if host == "www.youtube.com" {
			// The embed page sets the cookies the real one does, in the
			// third-party frame (SameSite=None needs Secure).
			for _, c := range []string{"VISITOR_INFO1_LIVE=tbvisitor1", "YSC=tbysc1"} {
				w.Header().Add("Set-Cookie", c+"; Path=/; Max-Age=15552000; Secure; SameSite=None")
			}
		}
		s.serveStatic(w, rt.file)
		return
	}
	http.NotFound(w, r)
}

// serveStatic writes static/<name>. A CMP replica's script is served
// after cmps/common.js, the helpers every replica shares (cookies,
// script activation, Consent Mode updates), as one file.
func (s *Server) serveStatic(w http.ResponseWriter, name string) {
	data, err := fs.ReadFile(s.opts.Static, name)
	if err != nil {
		http.Error(w, "missing stand-in "+name, http.StatusInternalServerError)
		return
	}
	if strings.HasPrefix(name, "cmps/") && strings.HasSuffix(name, ".js") && !strings.HasPrefix(name, "cmps/vendor/") {
		common, err := fs.ReadFile(s.opts.Static, "cmps/common.js")
		if err != nil {
			http.Error(w, "missing cmps/common.js", http.StatusInternalServerError)
			return
		}
		data = append(append(common, '\n'), data...)
	}
	ct := mime.TypeByExtension(path.Ext(name))
	if ct == "" || strings.HasSuffix(name, ".js") {
		ct = "text/javascript; charset=utf-8"
	}
	if strings.HasSuffix(name, ".html") {
		ct = "text/html; charset=utf-8"
	}
	w.Header().Set("Content-Type", ct)
	w.Header().Set("Access-Control-Allow-Origin", "*")
	_, _ = w.Write(data)
}

// pagePath is the paths a site renders from its template: / and any
// folder-style path such as /page-2/ or /blog/.
var pagePath = regexp.MustCompile(`^/([a-z0-9-]+/)*$`)

// Page is what a site template sees.
type Page struct {
	ID       string // the scenario: c01
	Host     string // c01.shop.test
	Sub      string // the sub-host before the scenario, e.g. metrics
	Path     string // /, /page-2/
	Broken   bool
	Internal string // the private address the ssrf-probe page asks for
}

// serveSite serves one request to a scenario site.
func (s *Server) serveSite(w http.ResponseWriter, r *http.Request, id, sub string) {
	p := r.URL.Path
	for _, rt := range sitePaths {
		if strings.HasPrefix(p, rt.prefix) {
			s.serveStatic(w, rt.file)
			return
		}
	}
	broken := s.Broken(id)
	if s.special(w, r, id, broken) {
		return
	}
	switch {
	case p == "/logout" || p == "/logout/" || p == "/cart" || p == "/cart/":
		// A runner that follows these logs the visitor out or fills a
		// cart: the sink counts each visit and the harness fails on any.
		s.sink.countForbidden(id)
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		_, _ = io.WriteString(w, "<!doctype html><title>Not for scans</title><p>This page must never be opened by a scan.</p>\n")
	case p == "/api/collect":
		// g14's first-party lookalike: the site's own endpoint, not a tracker.
		w.WriteHeader(http.StatusNoContent)
	case p == "/favicon.ico":
		w.WriteHeader(http.StatusNoContent)
	case pagePath.MatchString(p):
		s.renderPage(w, r, Page{ID: id, Host: hostOnly(r.Host), Sub: sub, Path: p, Broken: broken, Internal: s.opts.Admin})
	default:
		s.serveSiteFile(w, r, id)
	}
}

// serveSiteFile serves a file from sites/<id>/ other than the template.
func (s *Server) serveSiteFile(w http.ResponseWriter, r *http.Request, id string) {
	rel := strings.TrimPrefix(path.Clean(r.URL.Path), "/")
	if path.Base(rel) == "site.html" || !fs.ValidPath(rel) {
		http.NotFound(w, r)
		return
	}
	// os.DirFS refuses names that climb out of the site folder.
	site := os.DirFS(filepath.Join(s.sitesDir, id))
	if fi, err := fs.Stat(site, rel); err != nil || fi.IsDir() {
		http.NotFound(w, r)
		return
	}
	http.ServeFileFS(w, r, site, rel) //nolint:gosec // fs.ValidPath and os.DirFS keep it in the site folder
}

// renderPage renders sites/<id>/site.html inside sites/_layout.html with
// the partials in sites/_partials/. The files are read on every request,
// so an edit shows on the next load.
func (s *Server) renderPage(w http.ResponseWriter, r *http.Request, p Page) {
	files := []string{filepath.Join(s.sitesDir, "_layout.html")}
	partials, _ := filepath.Glob(filepath.Join(s.sitesDir, "_partials", "*.html"))
	files = append(files, partials...)
	files = append(files, filepath.Join(s.sitesDir, p.ID, "site.html"))
	t, err := template.New("_layout.html").Funcs(funcs).ParseFiles(files...)
	if err != nil {
		http.Error(w, "template: "+err.Error(), http.StatusInternalServerError)
		return
	}
	var buf bytes.Buffer
	if err := t.ExecuteTemplate(&buf, "layout", p); err != nil {
		http.Error(w, "template: "+err.Error(), http.StatusInternalServerError)
		return
	}
	if s.capi != nil && p.ID == capiScenario {
		s.capi.maybeSend(r, p.Broken)
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	_, _ = w.Write(buf.Bytes())
}

// funcs are the helpers site templates may call: dict builds the
// arguments of a partial, {{template "gtag" (dict "mode" "advanced")}}.
var funcs = template.FuncMap{
	"dict": func(kv ...any) (map[string]any, error) {
		if len(kv)%2 != 0 {
			return nil, fmt.Errorf("dict: odd number of arguments")
		}
		m := make(map[string]any, len(kv)/2)
		for i := 0; i < len(kv); i += 2 {
			k, ok := kv[i].(string)
			if !ok {
				return nil, fmt.Errorf("dict: key %v isn't a string", kv[i])
			}
			m[k] = kv[i+1]
		}
		return m, nil
	},
}

// AdminHandler serves the admin listener: state, sink counters and the
// private target of the ssrf-probe fixture. It is never reachable on the
// site listener.
func (s *Server) AdminHandler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /_admin/health", func(w http.ResponseWriter, _ *http.Request) {
		_, _ = fmt.Fprintln(w, "ok")
	})
	mux.HandleFunc("GET /_admin/state", s.adminState)
	mux.HandleFunc("GET /_admin/sites", func(w http.ResponseWriter, _ *http.Request) {
		ids, err := s.Sites()
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		out := map[string]string{}
		for _, id := range ids {
			out[id] = stateName(s.Broken(id))
		}
		writeJSON(w, out)
	})
	mux.HandleFunc("GET /_sink/count", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, s.sink.count(r.URL.Query().Get("scenario")))
	})
	mux.HandleFunc("GET /_sink/hits", func(w http.ResponseWriter, r *http.Request) {
		hits := s.sink.recordedHits(r.URL.Query().Get("scenario"))
		if hits == nil {
			hits = []Hit{}
		}
		writeJSON(w, hits)
	})
	mux.HandleFunc("POST /_sink/reset", func(w http.ResponseWriter, r *http.Request) {
		// ?scenario=c01 clears one scenario, so parallel benchmark jobs
		// don't clear each other's counts; without it every count goes.
		if id := r.URL.Query().Get("scenario"); id != "" {
			s.sink.resetScenario(id)
		} else {
			s.sink.reset()
		}
		w.WriteHeader(http.StatusNoContent)
	})
	mux.HandleFunc("/_internal/", func(w http.ResponseWriter, r *http.Request) {
		// Reaching this from a page means an egress guard let a page
		// through to a private address.
		sc := r.URL.Query().Get("s")
		if !validID(sc) {
			sc = "unknown"
		}
		s.sink.countInternal(sc)
		s.logf("internal address reached by %s", sc)
		w.Header().Set("Access-Control-Allow-Origin", "*")
		_, _ = fmt.Fprintln(w, "internal")
	})
	return mux
}

func stateName(broken bool) string {
	if broken {
		return "broken"
	}
	return "good"
}

// adminState answers GET /_admin/state?id=c01[&state=good|broken].
func (s *Server) adminState(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	id := q.Get("id")
	if !s.HasSite(id) {
		http.Error(w, "no scenario "+fmt.Sprintf("%q", id), http.StatusNotFound)
		return
	}
	switch q.Get("state") {
	case "":
	case "good", "broken":
		if err := s.SetBroken(id, q.Get("state") == "broken"); err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
	default:
		http.Error(w, "state: want good or broken", http.StatusBadRequest)
		return
	}
	writeJSON(w, map[string]string{"id": id, "state": stateName(s.Broken(id))})
}

func writeJSON(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", "application/json")
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	_ = enc.Encode(v)
}
