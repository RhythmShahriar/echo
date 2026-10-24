// Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
// SPDX-License-Identifier: MIT

package catcher

import (
	"context"
	"crypto/subtle"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"path"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/RhythmShahriar/echo/mask"
	"github.com/RhythmShahriar/echo/model"
	"github.com/RhythmShahriar/echo/recognize"
	"github.com/RhythmShahriar/echo/wire"
)

// MaxCapture is the largest capture the API accepts. Echo keeps MaxBody
// of each field, so a bigger capture only costs memory and time.
const MaxCapture = 8 << 20

// margin is how far past MaxBody a field is kept for masking. A secret
// that starts before the cut is still matched whole, so cutting first
// gives the same result as masking everything, without running every rule
// over megabytes that are thrown away.
const margin = 4 << 10

// Modes.
const (
	Seal    = "seal"    // nothing leaves except allowed hosts; Echo answers instead
	Observe = "observe" // everything is recorded and sent for real
)

// Config is how the catcher behaves.
type Config struct {
	Mode    string   // seal (default) or observe
	Allow   []string // hosts that may be reached even when sealed: "api.stripe.com", "*.sandbox.paypal.com"
	Token   string   // required in X-Echo-Token when set (Echo listening beyond loopback)
	Listen  string   // the address Echo listens on; its host is accepted in Host besides loopback names
	MaxBody int      // bytes of body kept per message; default 256 KiB
	Version string
}

// Server is the catcher's HTTP API.
type Server struct {
	Inbox   *Inbox
	cfg     Config
	mux     *http.ServeMux
	handler http.Handler // mux behind Protect and the token check
}

// NewServer returns the API over inbox.
func NewServer(inbox *Inbox, cfg Config) *Server {
	if cfg.Mode == "" {
		cfg.Mode = Seal
	}
	if cfg.MaxBody == 0 {
		cfg.MaxBody = 256 << 10
	}
	s := &Server{Inbox: inbox, cfg: cfg, mux: http.NewServeMux()}
	s.mux.HandleFunc("GET /v1/health", s.health)
	s.mux.HandleFunc("POST /v1/capture", s.capture)
	s.mux.HandleFunc("GET /v1/messages", s.list)
	s.mux.HandleFunc("GET /v1/messages/{id}", s.get)
	s.mux.HandleFunc("DELETE /v1/messages", s.clear)
	s.mux.HandleFunc("GET /v1/wait", s.wait)
	s.mux.HandleFunc("POST /v1/actions/begin", s.begin)
	s.mux.HandleFunc("POST /v1/actions/end", s.end)
	s.mailpitRoutes()
	s.handler = Protect(http.HandlerFunc(s.serve), ProtectOptions{Token: cfg.Token, Hosts: listenHosts(cfg.Listen)})
	return s
}

// listenHosts returns the host of listen as a name Protect accepts, unless
// it's a wildcard: a browser can send "Host: 0.0.0.0" to reach loopback,
// so a wildcard must not widen what's allowed.
func listenHosts(listen string) []string {
	host := hostOnly(listen)
	if ip := net.ParseIP(host); host == "" || ip != nil && ip.IsUnspecified() {
		return nil
	}
	return []string{host}
}

// Mode is seal or observe.
func (s *Server) Mode() string { return s.cfg.Mode }

// ServeHTTP serves the API. Requests pass Protect first, then the token
// check when a token is set.
func (s *Server) ServeHTTP(w http.ResponseWriter, r *http.Request) { s.handler.ServeHTTP(w, r) }

func (s *Server) serve(w http.ResponseWriter, r *http.Request) {
	if s.cfg.Token != "" && subtle.ConstantTimeCompare([]byte(r.Header.Get(wire.TokenHeader)), []byte(s.cfg.Token)) != 1 {
		http.Error(w, "missing or wrong "+wire.TokenHeader, http.StatusUnauthorized)
		return
	}
	s.mux.ServeHTTP(w, r)
}

func (s *Server) health(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, map[string]any{"tool": "rhythm-echo", "version": s.cfg.Version, "mode": s.cfg.Mode,
		"wire": wire.Version, "providers": recognize.Providers()})
}

// capture records an email or an HTTP request and decides its fate.
func (s *Server) capture(w http.ResponseWriter, r *http.Request) {
	var c wire.Capture
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, MaxCapture)).Decode(&c); err != nil {
		if _, ok := errors.AsType[*http.MaxBytesError](err); ok {
			http.Error(w, fmt.Sprintf("capture too large: Echo accepts up to %d MiB", MaxCapture>>20), http.StatusRequestEntityTooLarge)
			return
		}
		http.Error(w, "bad capture: "+err.Error(), http.StatusBadRequest)
		return
	}
	d, err := s.Capture(c)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	writeJSON(w, http.StatusOK, d)
}

// Capture records c and returns the decision. Connectors reach it through
// the API; the SMTP sink and the proxy call it directly.
func (s *Server) Capture(c wire.Capture) (wire.Decision, error) {
	if c.Version > wire.Version {
		return wire.Decision{}, errors.New("this connector is newer than Echo; run: rhythm update")
	}
	run, action, _ := strings.Cut(c.Run, "/")
	src := model.Source{Via: c.Source.Via, Stack: c.Source.Stack, Plugin: c.Source.Plugin, Hook: c.Source.Hook, Host: c.Source.Host}
	switch c.Kind {
	case "email":
		// After a real send (observe mode), a connector may report how it
		// went; the message it captured before is updated, not duplicated.
		if c.Phase == wire.After && c.ID != "" {
			s.Inbox.Update(c.ID, func(m *model.Message) {
				if c.Error != "" {
					m.Status, m.Error = model.Failed, c.Error
				} else if m.Status != model.Failed {
					m.Status = model.Sent
				}
			})
			return wire.Decision{ID: c.ID, Mode: s.cfg.Mode, Action: wire.Allow}, nil
		}
		if c.Email == nil {
			return wire.Decision{}, errors.New("an email capture needs email")
		}
		m := s.email(c.Email)
		m.Run, m.Action, m.Source = run, action, src
		// Mail handed to Echo's own SMTP sink or sendmail stand-in ends
		// there in every mode: nothing delivers it, so it's never "sent".
		sent := s.cfg.Mode == Observe && !sink(c.Source.Via)
		m.Status, m.Error = model.Faked, c.Error
		if sent {
			m.Status = model.Sent
		}
		if c.Error != "" {
			m.Status = model.Failed
		}
		m = s.Inbox.Add(m)
		act := wire.Fake
		if sent {
			act = wire.Allow
		}
		return wire.Decision{ID: m.ID, Mode: s.cfg.Mode, Action: act}, nil
	case "http":
		if c.HTTP == nil {
			return wire.Decision{}, errors.New("an http capture needs http")
		}
		return s.http(c, run, action, src)
	}
	return wire.Decision{}, errors.New(`kind must be "email" or "http"`)
}

func (s *Server) http(c wire.Capture, run, action string, src model.Source) (wire.Decision, error) {
	h := c.HTTP
	if c.Phase == wire.After && c.ID != "" {
		s.Inbox.Update(c.ID, func(m *model.Message) {
			m.Response = &model.Response{Status: h.Status, Body: s.fitAny(h.Response, mask.Body)}
			switch {
			case c.Error != "":
				m.Status, m.Error = model.Failed, c.Error
			case h.Status >= 400:
				m.Status = model.Failed
			default:
				m.Status = model.Sent
			}
		})
		return wire.Decision{ID: c.ID, Mode: s.cfg.Mode, Action: wire.Allow}, nil
	}
	u, err := url.Parse(h.URL)
	if err != nil || u.Host == "" {
		return wire.Decision{}, errors.New("http capture: bad url")
	}
	// The platform's own housekeeping (update checks) isn't something the
	// site says to anyone: let it through without recording it.
	if infrastructure(u.Hostname()) {
		return wire.Decision{Mode: s.cfg.Mode, Action: wire.Allow}, nil
	}
	req := recognize.Request{Method: strings.ToUpper(h.Method), URL: u, Headers: lower(h.Headers), Body: []byte(h.Body)}
	m := model.Message{Run: run, Action: action, Source: src, Method: req.Method,
		Headers: s.headers(req.Headers)}
	m.Body = s.fit(&m, h.Body, mask.Body)
	rec := recognize.Recognize(req, &m)
	m.URL = s.fitAny(recognize.RedactURL(rec, h.URL), mask.Text) // a catch hook's URL is its credential
	// Recognizers copy what they read from the raw request: the SMS text,
	// the chat message and for unknown APIs the host and path as To.
	s.maskFields(&m)
	allowed := s.allowed(u.Hostname(), src.Host)
	d := wire.Decision{Mode: s.cfg.Mode}
	switch {
	case c.Phase == wire.After: // observed with nothing before: record as sent
		m.Status = model.Sent
		m.Response = &model.Response{Status: h.Status, Body: s.fitAny(h.Response, mask.Body)}
		if h.Status >= 400 || c.Error != "" {
			m.Status, m.Error = model.Failed, c.Error
		}
		d.Action = wire.Allow
	case recognize.Refuses(rec, req):
		// A live payment (or one whose mode Echo can't read) is refused
		// in every mode, allowlisted or not: a test must never charge a
		// card. It's answered with the provider's own refusal.
		f := recognize.FakeFor(rec, req, &m)
		m.Status, m.Error = model.Blocked, "refused: a live-mode "+recognize.Name(m.Provider)+" call; use test credentials on staging"
		m.Response = &model.Response{Status: f.Status, Body: s.fitAny(f.Body, nil)}
		d.Action, d.Response = wire.Fake, &wire.Response{Status: f.Status, Headers: f.Headers, Body: f.Body}
	case s.cfg.Mode == Observe || allowed:
		m.Status = model.Sent // until the after phase says otherwise
		d.Action = wire.Allow
	default:
		f := recognize.FakeFor(rec, req, &m)
		m.Status = model.Faked
		m.Response = &model.Response{Status: f.Status, Body: s.fitAny(f.Body, nil)}
		d.Action, d.Response = wire.Fake, &wire.Response{Status: f.Status, Headers: f.Headers, Body: f.Body}
	}
	m = s.Inbox.Add(m)
	d.ID = m.ID
	return d, nil
}

// sink reports whether mail came in through one of Echo's own endpoints
// (the SMTP sink, the sendmail stand-in), which keep it and never deliver.
func sink(via string) bool { return via == "smtp" || via == "sendmail" }

// allowed reports whether host may be reached when sealed: the site
// itself (cron and loopback calls) and the configured allowlist.
func (s *Server) allowed(host, site string) bool {
	host = strings.ToLower(host)
	if site != "" && strings.EqualFold(host, hostOnly(site)) {
		return true
	}
	if ip := net.ParseIP(host); ip != nil && ip.IsLoopback() || host == "localhost" {
		return true
	}
	for _, a := range s.cfg.Allow {
		if ok, _ := path.Match(strings.ToLower(a), host); ok {
			return true
		}
	}
	return false
}

func (s *Server) email(e *wire.Email) model.Message {
	m := model.Message{Channel: model.Email, Provider: "", From: e.From, To: e.To, CC: e.CC, BCC: e.BCC,
		ReplyTo: e.ReplyTo, Subject: e.Subject, Text: e.Text, HTML: e.HTML, Headers: lower(e.Headers)}
	if e.Raw != "" {
		if parsed, err := ParseMIME([]byte(e.Raw)); err == nil {
			fillEmpty(&m, parsed)
		}
		// Decoded first: a reset link in a base64 part is invisible to
		// every rule.
		m.Body = s.fit(&m, e.Raw, mask.MIME)
	}
	m.Headers = s.headers(m.Headers)
	s.maskFields(&m)
	return m
}

// maskFields masks the fields a message's text and recipients are read
// into, each cut to size: a subject can hold a login code, an HTML link a
// reset key and a recipient copied from an API path a bot token.
func (s *Server) maskFields(m *model.Message) {
	m.Subject, m.Text, m.HTML = s.fit(m, m.Subject, mask.Text), s.fit(m, m.Text, mask.Text), s.fit(m, m.HTML, mask.Text)
	m.From, m.ReplyTo = s.fit(m, m.From, maskAddress), s.fit(m, m.ReplyTo, maskAddress)
	m.To, m.CC, m.BCC = s.addresses(m, m.To), s.addresses(m, m.CC), s.addresses(m, m.BCC)
}

func maskAddress(v string) string { return mask.URL(mask.Text(v)) }

// addresses returns a masked copy of list: it may be the caller's.
func (s *Server) addresses(m *model.Message, list []string) []string {
	if list == nil {
		return nil
	}
	out := make([]string, len(list))
	for i, v := range list {
		out[i] = s.fit(m, v, maskAddress)
	}
	return out
}

// headers masks every header value, each cut to size.
func (s *Server) headers(h map[string]string) map[string]string {
	out := make(map[string]string, len(h))
	for k, v := range h {
		out[k] = s.fitAny(v, func(v string) string { return mask.Header(k, v) })
	}
	return out
}

// fillEmpty copies fields the connector didn't send from a parsed MIME
// message.
func fillEmpty(m *model.Message, p model.Message) {
	if m.From == "" {
		m.From = p.From
	}
	if len(m.To) == 0 {
		m.To = p.To
	}
	if len(m.CC) == 0 {
		m.CC = p.CC
	}
	if m.Subject == "" {
		m.Subject = p.Subject
	}
	if m.Text == "" {
		m.Text = p.Text
	}
	if m.HTML == "" {
		m.HTML = p.HTML
	}
	if m.ReplyTo == "" {
		m.ReplyTo = p.ReplyTo
	}
	if len(m.Headers) == 0 {
		m.Headers = p.Headers
	}
}

// fit masks v with fn and keeps at most MaxBody bytes of the result,
// marking m as truncated when it cuts. v is cut to MaxBody plus a margin
// before masking: masking all of a 20 MiB body took 29 s and all but
// MaxBody of it was thrown away.
func (s *Server) fit(m *model.Message, v string, fn func(string) string) string {
	v, cut := s.cutMasked(v, fn)
	m.Truncated = m.Truncated || cut
	return v
}

// fitAny is fit for a field Truncated doesn't cover (a response, a
// header, the URL). fn may be nil for text Echo wrote itself.
func (s *Server) fitAny(v string, fn func(string) string) string {
	v, _ = s.cutMasked(v, fn)
	return v
}

func (s *Server) cutMasked(v string, fn func(string) string) (string, bool) {
	cut := false
	if limit := s.cfg.MaxBody + margin; len(v) > limit {
		v, cut = v[:runeStart(v, limit)], true
	}
	if fn != nil {
		v = fn(v)
	}
	if len(v) > s.cfg.MaxBody {
		v, cut = v[:runeStart(v, s.cfg.MaxBody)], true
	}
	if cut {
		v += "\n…(truncated)"
	}
	return v, cut
}

// runeStart moves i back to the start of the UTF-8 sequence it falls in,
// so a cut never splits a character.
func runeStart(v string, i int) int {
	for j := i; j > 0 && j > i-utf8.UTFMax; j-- {
		if utf8.RuneStart(v[j]) {
			return j
		}
	}
	return i
}

func filterFrom(q url.Values) Filter {
	f := Filter{Run: q.Get("run"), Action: q.Get("action"), Channel: model.Channel(q.Get("channel")), To: q.Get("to")}
	if s := q.Get("since"); s != "" {
		if t, err := time.Parse(time.RFC3339Nano, s); err == nil {
			f.Since = t
		}
	}
	return f
}

func (s *Server) list(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, nonNil(s.Inbox.List(filterFrom(r.URL.Query()))))
}

func (s *Server) get(w http.ResponseWriter, r *http.Request) {
	m, ok := s.Inbox.Get(r.PathValue("id"))
	if !ok {
		http.NotFound(w, r)
		return
	}
	writeJSON(w, http.StatusOK, m)
}

func (s *Server) clear(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, map[string]int{"deleted": s.Inbox.Clear(filterFrom(r.URL.Query()))})
}

// wait blocks until count messages match and none arrive for quiet (both
// optional), up to timeout.
func (s *Server) wait(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	count, _ := strconv.Atoi(q.Get("count"))
	timeout := duration(q.Get("timeout"), 10*time.Second, time.Minute)
	quiet := duration(q.Get("quiet"), 1500*time.Millisecond, 30*time.Second)
	ctx, cancel := context.WithTimeout(r.Context(), timeout)
	defer cancel()
	writeJSON(w, http.StatusOK, nonNil(s.Inbox.Wait(ctx, filterFrom(q), count, quiet)))
}

type actionBody struct {
	Run    string `json:"run"`
	Action string `json:"action"`
	Lease  string `json:"lease,omitempty"`
}

// begin opens an action and answers {"lease": "..."}, which end takes to
// close exactly that action.
func (s *Server) begin(w http.ResponseWriter, r *http.Request) {
	var b actionBody
	if err := json.NewDecoder(io.LimitReader(r.Body, 1<<16)).Decode(&b); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"lease": s.Inbox.begin(b.Run, b.Action, caller(r))})
}

// end closes the action named by {"lease": "..."}. Clients from before
// leases send no body: the oldest action the same address opened is
// closed, so an old runner keeps working while another run is open.
func (s *Server) end(w http.ResponseWriter, r *http.Request) {
	var b actionBody
	if err := json.NewDecoder(io.LimitReader(r.Body, 1<<16)).Decode(&b); err != nil && !errors.Is(err, io.EOF) {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	if b.Lease != "" {
		s.Inbox.End(b.Lease)
	} else {
		s.Inbox.endOldest(caller(r))
	}
	w.WriteHeader(http.StatusNoContent)
}

// caller is the address a request came from, without the port: each
// request may come over a new connection.
func caller(r *http.Request) string {
	if host, _, err := net.SplitHostPort(r.RemoteAddr); err == nil {
		return host
	}
	return r.RemoteAddr
}

func duration(v string, def, limit time.Duration) time.Duration {
	d, err := time.ParseDuration(v)
	if err != nil || d <= 0 {
		return def
	}
	return min(d, limit)
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	enc := json.NewEncoder(w)
	enc.SetEscapeHTML(false)
	_ = enc.Encode(v)
}

func nonNil(ms []model.Message) []model.Message {
	if ms == nil {
		return []model.Message{}
	}
	return ms
}

func lower(h map[string]string) map[string]string {
	out := make(map[string]string, len(h))
	for k, v := range h {
		out[strings.ToLower(k)] = v
	}
	return out
}

func hostOnly(h string) string {
	if strings.Contains(h, "://") {
		if u, err := url.Parse(h); err == nil {
			return u.Hostname()
		}
	}
	if host, _, err := net.SplitHostPort(h); err == nil {
		return host
	}
	return h
}

// infrastructure hosts are platform update and API checks that sites make
// on their own; capturing them would only add noise to every action.
var infrastructureHosts = []string{"api.wordpress.org", "downloads.wordpress.org", "wordpress.org", "s.w.org", "planet.wordpress.org", "repo.packagist.org"}

func infrastructure(host string) bool {
	host = strings.ToLower(host)
	for _, h := range infrastructureHosts {
		if host == h {
			return true
		}
	}
	return false
}
