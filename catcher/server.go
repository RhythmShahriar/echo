// Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
// SPDX-License-Identifier: MIT

package catcher

import (
	"context"
	"crypto/rand"
	"crypto/subtle"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"net/url"
	"path"
	"strconv"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"github.com/RhythmShahriar/echo/check"
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
	Mode  string   // seal (default) or observe
	Allow []string // hosts that may be reached even when sealed: "api.stripe.com", "*.sandbox.paypal.com"
	Token string   // required in X-Echo-Token when set (Echo listening beyond loopback)
	// WriteTokens and ReadToken split Token's access, so one Echo can
	// serve many sites (an agency hub) without each site's connector
	// reading every site's mail. A write token, mapped to the ID of the
	// site that holds it, may only send captures and begin and end
	// actions (and read health); what it captures is marked with that
	// site (Source.Site), so `site:client-a` finds it. Everything else
	// needs ReadToken (or Token), which reads every site's messages.
	WriteTokens map[string]string
	ReadToken   string
	Listen      string // the address Echo listens on; its host is accepted in Host besides loopback names
	MaxBody     int    // bytes of body kept per message; default 256 KiB
	// Site is the site being tested (a URL or host): links to it aren't
	// development links. AllowRecipients are the address and number globs
	// that may really be messaged. Both feed the checks run on every
	// message.
	Site            string
	AllowRecipients []string
	// SMTPAddr and ProxyAddr are where the SMTP sink and the proxy listen
	// ("off" when they don't run), so clients reach the real ones rather
	// than config defaults. Empty means not known: health leaves it out.
	SMTPAddr  string
	ProxyAddr string
	// BootID names this start of Echo; a client that sees it change knows
	// Echo restarted and lost what it held in memory. Default: random.
	BootID string
	// Store is where messages are kept: "memory", or "jsonl" for an
	// inbox from OpenInbox. Default: whichever the inbox is.
	Store   string
	Version string
	// Warn is told about things worth a line in the log, such as a
	// connector's site host that Echo ignores. Default: the standard log.
	Warn func(msg string)
}

// Server is the catcher's HTTP API.
type Server struct {
	Inbox   *Inbox
	cfg     Config
	mux     *http.ServeMux
	handler http.Handler // mux behind Protect and the token check
	warned  sync.Map     // site hosts already warned about
	pit     mailpitState // what the Mailpit routes keep beside the inbox
}

// NewServer returns the API over inbox.
func NewServer(inbox *Inbox, cfg Config) *Server {
	if cfg.Mode == "" {
		cfg.Mode = Seal
	}
	if cfg.MaxBody == 0 {
		cfg.MaxBody = 256 << 10
	}
	if cfg.BootID == "" {
		cfg.BootID = rand.Text()
	}
	if cfg.Store == "" {
		cfg.Store = "memory"
		if inbox.Persistent() {
			cfg.Store = "jsonl"
		}
	}
	if cfg.Warn == nil {
		cfg.Warn = func(msg string) { log.Print("echo: " + msg) }
	}
	s := &Server{Inbox: inbox, cfg: cfg, mux: http.NewServeMux(), pit: mailpitState{started: time.Now(), read: map[string]bool{}}}
	opts := check.Options{SiteHost: hostOnly(cfg.Site), AllowRecipients: cfg.AllowRecipients}
	inbox.SetCheck(func(m model.Message) []model.Issue { return check.Message(m, opts) })
	s.mux.HandleFunc("GET /v1/health", s.health)
	s.mux.HandleFunc("POST /v1/capture", s.capture)
	s.mux.HandleFunc("GET /v1/messages", s.list)
	s.mux.HandleFunc("GET /v1/messages/latest", s.latest)
	s.mux.HandleFunc("GET /v1/messages/{id}", s.get)
	s.mux.HandleFunc("DELETE /v1/messages", s.clear)
	s.mux.HandleFunc("DELETE /v1/messages/{id}", s.deleteOne)
	s.mux.HandleFunc("GET /v1/events", s.events)
	s.mux.HandleFunc("GET /v1/wait", s.wait)
	s.mux.HandleFunc("POST /v1/actions/begin", s.begin)
	s.mux.HandleFunc("POST /v1/actions/end", s.end)
	s.mailpitRoutes()
	tokens := []string{cfg.ReadToken}
	for t := range cfg.WriteTokens {
		tokens = append(tokens, t)
	}
	s.handler = Protect(http.HandlerFunc(s.serve), ProtectOptions{Token: cfg.Token, Tokens: tokens, Hosts: listenHosts(cfg.Listen)})
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

// BootID names this start of Echo, as health reports it.
func (s *Server) BootID() string { return s.cfg.BootID }

// ServeHTTP serves the API. Requests pass Protect first, then the token
// check when a token is set.
func (s *Server) ServeHTTP(w http.ResponseWriter, r *http.Request) { s.handler.ServeHTTP(w, r) }

func (s *Server) serve(w http.ResponseWriter, r *http.Request) {
	if s.cfg.Token == "" && s.cfg.ReadToken == "" && len(s.cfg.WriteTokens) == 0 {
		s.mux.ServeHTTP(w, r)
		return
	}
	tok := r.Header.Get(wire.TokenHeader)
	if tok != "" && (same(tok, s.cfg.Token) || same(tok, s.cfg.ReadToken)) {
		s.mux.ServeHTTP(w, r)
		return
	}
	site, ok := s.writeSite(tok)
	if !ok {
		http.Error(w, "missing or wrong "+wire.TokenHeader, http.StatusUnauthorized)
		return
	}
	if !writeRoute(r) {
		http.Error(w, "this is a write token: it may only send captures and begin or end actions; reading messages needs the read token",
			http.StatusForbidden)
		return
	}
	s.mux.ServeHTTP(w, r.WithContext(context.WithValue(r.Context(), siteKey{}, site)))
}

// same compares a token in constant time; an empty want never matches.
func same(got, want string) bool {
	return want != "" && subtle.ConstantTimeCompare([]byte(got), []byte(want)) == 1
}

// writeSite returns the site of the write token tok. Every token is
// compared, so the time taken doesn't tell which one nearly matched.
func (s *Server) writeSite(tok string) (string, bool) {
	site, found := "", false
	for t, id := range s.cfg.WriteTokens {
		if tok != "" && same(tok, t) {
			site, found = id, true
		}
	}
	return site, found
}

// writeRoute reports whether a write token may make request r: what a
// connector or a runner on that site sends.
func writeRoute(r *http.Request) bool {
	switch r.Method + " " + r.URL.Path {
	case "POST /v1/capture", "POST /v1/actions/begin", "POST /v1/actions/end", "GET /v1/health":
		return true
	}
	return false
}

// siteKey holds the site of a request made with a write token.
type siteKey struct{}

// requestSite returns the site r's write token is for, or "".
func requestSite(r *http.Request) string {
	site, _ := r.Context().Value(siteKey{}).(string)
	return site
}

// capabilities are the API features this catcher has beyond wire version
// 1, so a client can tell a newer Echo from an older one.
var capabilities = []string{
	"leases",      // begin answers a lease that end takes
	"issues",      // every message carries the checks' findings
	"late",        // untagged mail dated before the open action is credited to the one before
	"search",      // /v1/messages takes q, limit and before; since takes a duration
	"events",      // GET /v1/events streams changes; DELETE /v1/messages/{id}
	"attachments", // emails list their files: name, type, size and sha256
	"codes",       // GET /v1/messages/latest answers one-time codes and links
	"exclusive",   // begin takes "exclusive": true and waits for other actions
	"site-tokens", // per-site write tokens and a read token can split the single token
}

// health is what GET /v1/health answers, in this order.
type health struct {
	Tool         string   `json:"tool"`
	Version      string   `json:"version"`
	Mode         string   `json:"mode"`
	Wire         int      `json:"wire"`
	SMTP         string   `json:"smtp,omitempty"`
	Proxy        string   `json:"proxy,omitempty"`
	BootID       string   `json:"boot_id"`
	Store        string   `json:"store"`
	Capabilities []string `json:"capabilities"`
	Providers    []string `json:"providers"`
}

func (s *Server) health(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, health{Tool: "rhythm-echo", Version: s.cfg.Version, Mode: s.cfg.Mode, Wire: wire.Version,
		SMTP: s.cfg.SMTPAddr, Proxy: s.cfg.ProxyAddr, BootID: s.cfg.BootID, Store: s.cfg.Store,
		Capabilities: capabilities, Providers: recognize.Providers()})
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
	d, err := s.captureFor(c, requestSite(r))
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	writeJSON(w, http.StatusOK, d)
}

// Capture records c and returns the decision. Connectors reach it through
// the API; the SMTP sink and the proxy call it directly.
func (s *Server) Capture(c wire.Capture) (wire.Decision, error) { return s.captureFor(c, "") }

// captureFor is Capture for a site's write token: site marks what's
// captured; an after phase only updates that site's own messages.
func (s *Server) captureFor(c wire.Capture, site string) (wire.Decision, error) {
	if c.Version > wire.Version {
		return wire.Decision{}, errors.New("this connector is newer than Echo; run: rhythm update")
	}
	run, action, _ := strings.Cut(c.Run, "/")
	src := model.Source{Via: c.Source.Via, Stack: c.Source.Stack, Plugin: c.Source.Plugin, Hook: c.Source.Hook, Host: c.Source.Host,
		Site: site}
	if c.Phase == wire.After && c.ID != "" && site != "" {
		if m, ok := s.Inbox.Get(c.ID); ok && m.Source.Site != site {
			return wire.Decision{}, errors.New("no message with that ID was captured by this site")
		}
	}
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
		if c.Source.Via == "smtp" {
			s.pit.smtpAccepted.Add(1)
			s.pit.smtpBytes.Add(int64(len(c.Email.Raw)))
		}
		m, f := s.email(c.Email)
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
		m = s.Inbox.add(m, f)
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
	f := extract(m)                                              // an SMS login code, read before masking hides it
	// Recognizers copy what they read from the raw request: the SMS text,
	// the chat message and for unknown APIs the host and path as To.
	s.maskFields(&m)
	// The site may call itself (cron, loopback) while sealed. Only a host
	// that can't be a provider's counts as the site.
	allowed := s.allowed(u.Hostname(), s.siteHost(src.Host, u.Hostname(), rec.Provider != ""))
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
	f.hide(&m)
	m = s.Inbox.add(m, f)
	d.ID = m.ID
	return d, nil
}

// sink reports whether mail came in through one of Echo's own endpoints
// (the SMTP sink, the sendmail stand-in), which keep it and never deliver.
func sink(via string) bool { return via == "smtp" || via == "sendmail" }

// allowed reports whether host may be reached when sealed: the site
// itself (cron and loopback calls) and the configured allowlist. site
// comes from the connector and must pass siteHost first.
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

// email turns a captured email into a masked message, with the codes and
// links read from it before masking.
func (s *Server) email(e *wire.Email) (model.Message, found) {
	m := model.Message{Channel: model.Email, Provider: "", From: e.From, To: e.To, CC: e.CC, BCC: e.BCC,
		ReplyTo: e.ReplyTo, Subject: e.Subject, Text: e.Text, HTML: e.HTML, Headers: lower(e.Headers)}
	if e.Raw != "" {
		// A message cut short or otherwise broken still gives what was
		// read before the error.
		if parsed, err := ParseMIME([]byte(e.Raw)); err == nil || parsed.Headers != nil {
			fillEmpty(&m, parsed)
		}
		// Decoded first: a reset link in a base64 part is invisible to
		// every rule.
		m.Body = s.fit(&m, e.Raw, mask.MIME)
	}
	if m.Attachments == nil {
		m.Attachments = headerAttachments(m.Headers["x-echo-attachments"])
	}
	for i := range m.Attachments { // a file name is the sender's text too
		m.Attachments[i].Name = s.fitAny(m.Attachments[i].Name, mask.Text)
	}
	f := extract(m)
	m.Headers = s.headers(m.Headers)
	s.maskFields(&m)
	f.hide(&m)
	return m, f
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
	if len(m.Attachments) == 0 {
		m.Attachments = p.Attachments
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

// filterFrom reads the filter of a request: run, action, channel, to,
// since (a time, or a duration back from now such as 10m) and q (a
// search, see ParseQuery).
func filterFrom(q url.Values) (Filter, error) {
	f := Filter{Run: q.Get("run"), Action: q.Get("action"), Channel: model.Channel(q.Get("channel")), To: q.Get("to")}
	if v := q.Get("since"); v != "" {
		if t, err := time.Parse(time.RFC3339Nano, v); err == nil {
			f.Since = t
		} else if d, err := time.ParseDuration(v); err == nil && d > 0 {
			f.Since = time.Now().Add(-d)
		}
		// Anything else is ignored, as it always was.
	}
	if v := q.Get("q"); v != "" {
		var err error
		if f.Q, err = ParseQuery(v); err != nil {
			return f, fmt.Errorf("q: %w", err)
		}
	}
	return f, nil
}

// filter is filterFrom for a handler: on a bad search it answers 400 and
// returns false.
func filter(w http.ResponseWriter, r *http.Request) (Filter, bool) {
	f, err := filterFrom(r.URL.Query())
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return f, false
	}
	return f, true
}

// Page sizes for GET /v1/messages.
const (
	defaultPage = 50
	maxPage     = 1000
)

// page is what GET /v1/messages answers when asked for a page.
type page struct {
	Messages []model.Message `json:"messages"`
	Next     string          `json:"next,omitempty"` // pass as before for the next page
}

// list answers every matching message oldest first, as a plain array.
// With limit or before it answers a page instead, newest first:
// {"messages": [...], "next": "<id>"}.
func (s *Server) list(w http.ResponseWriter, r *http.Request) {
	f, ok := filter(w, r)
	if !ok {
		return
	}
	q := r.URL.Query()
	if !q.Has("limit") && !q.Has("before") {
		writeJSON(w, http.StatusOK, nonNil(s.Inbox.List(f)))
		return
	}
	limit := defaultPage
	if v := q.Get("limit"); v != "" {
		n, err := strconv.Atoi(v)
		if err != nil || n <= 0 {
			http.Error(w, "limit: want a number above 0", http.StatusBadRequest)
			return
		}
		limit = min(n, maxPage)
	}
	ms, next, err := s.Inbox.Page(f, q.Get("before"), limit)
	if err != nil {
		http.Error(w, "before: "+err.Error(), http.StatusBadRequest)
		return
	}
	writeJSON(w, http.StatusOK, page{Messages: ms, Next: next})
}

// deleteOne deletes one message: 204, or 404 when there's no such
// message.
func (s *Server) deleteOne(w http.ResponseWriter, r *http.Request) {
	if !s.Inbox.Remove(r.PathValue("id")) {
		http.NotFound(w, r)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// heartbeat is how often an idle event stream sends a comment, so proxies
// and browsers don't take it for dead.
var heartbeat = 20 * time.Second

// events streams changes to the inbox as server-sent events, one per
// change: `data: {"type":"add","id":"..."}` with type add, update, delete
// or reset (the stream fell behind: read the messages again). Each event
// has an ID; a client that reconnects with Last-Event-ID gets what it
// missed.
func (s *Server) events(w http.ResponseWriter, r *http.Request) {
	rc := http.NewResponseController(w)
	seq := s.Inbox.LastEvent()
	if v := r.Header.Get("Last-Event-ID"); v != "" {
		if n, err := strconv.ParseUint(v, 10, 64); err == nil {
			seq = n
		}
	}
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("X-Accel-Buffering", "no") // nginx in front: don't hold events back
	w.WriteHeader(http.StatusOK)
	if _, err := io.WriteString(w, "retry: 2000\n\n"); err != nil || rc.Flush() != nil {
		return
	}
	tick := time.NewTicker(heartbeat)
	defer tick.Stop()
	for {
		evs, next, changed := s.Inbox.Events(seq)
		seq = next
		for _, e := range evs {
			b, _ := json.Marshal(e)
			if _, err := fmt.Fprintf(w, "id: %d\ndata: %s\n\n", e.Seq, b); err != nil {
				return
			}
		}
		if len(evs) > 0 && rc.Flush() != nil {
			return
		}
		select {
		case <-r.Context().Done():
			return
		case <-changed:
		case <-tick.C:
			if _, err := io.WriteString(w, ": ping\n\n"); err != nil || rc.Flush() != nil {
				return
			}
		}
	}
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
	f, ok := filter(w, r)
	if !ok {
		return
	}
	writeJSON(w, http.StatusOK, map[string]int{"deleted": s.Inbox.Clear(f)})
}

// wait blocks until count messages match and none arrive for quiet (both
// optional), up to timeout.
func (s *Server) wait(w http.ResponseWriter, r *http.Request) {
	f, ok := filter(w, r)
	if !ok {
		return
	}
	q := r.URL.Query()
	count, _ := strconv.Atoi(q.Get("count"))
	timeout := duration(q.Get("timeout"), 10*time.Second, time.Minute)
	quiet := duration(q.Get("quiet"), 1500*time.Millisecond, 30*time.Second)
	ctx, cancel := context.WithTimeout(r.Context(), timeout)
	defer cancel()
	writeJSON(w, http.StatusOK, nonNil(s.Inbox.Wait(ctx, f, count, quiet)))
}

type actionBody struct {
	Run    string `json:"run"`
	Action string `json:"action"`
	Lease  string `json:"lease,omitempty"`
	// Exclusive and Timeout are begin's: an exclusive action plus how
	// long to wait for other actions to let it open (a Go duration).
	Exclusive bool   `json:"exclusive,omitempty"`
	Timeout   string `json:"timeout,omitempty"`
}

// How long begin waits for other actions by default and at most.
const (
	beginWait    = 30 * time.Second
	maxBeginWait = time.Minute
)

// begin opens an action and answers {"lease": "..."}, which end takes to
// close exactly that action. With "exclusive": true it first waits for
// every open action to end; any begin waits while an exclusive action is
// open or waiting. Waiting longer than timeout answers 409.
func (s *Server) begin(w http.ResponseWriter, r *http.Request) {
	var b actionBody
	if err := json.NewDecoder(io.LimitReader(r.Body, 1<<16)).Decode(&b); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	timeout := beginWait
	if b.Timeout != "" {
		d, err := time.ParseDuration(b.Timeout)
		if err != nil || d <= 0 {
			http.Error(w, "timeout: want a duration such as 30s", http.StatusBadRequest)
			return
		}
		timeout = min(d, maxBeginWait)
	}
	ctx, cancel := context.WithTimeout(r.Context(), timeout)
	defer cancel()
	lease, err := s.Inbox.beginScope(ctx, ScopeOptions{Run: b.Run, Action: b.Action, Exclusive: b.Exclusive}, caller(r))
	if err != nil {
		http.Error(w, fmt.Sprintf("begin: waited %s: %v; end the actions in the way (POST /v1/actions/end) or retry with a longer timeout", timeout, err),
			http.StatusConflict)
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"lease": lease})
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
