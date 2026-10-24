package catcher

import (
	"context"
	"crypto/subtle"
	"encoding/json"
	"errors"
	"io"
	"net"
	"net/http"
	"net/url"
	"path"
	"strconv"
	"strings"
	"time"

	"github.com/RhythmShahriar/echo/model"
	"github.com/RhythmShahriar/echo/recognize"
	"github.com/RhythmShahriar/echo/wire"
	"github.com/RhythmShahriar/rhythm/kit/secrets"
)

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
	MaxBody int      // bytes of body kept per message; default 256 KiB
	Version string
}

// Server is the catcher's HTTP API.
type Server struct {
	Inbox *Inbox
	cfg   Config
	mux   *http.ServeMux
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
	return s
}

// Mode is seal or observe.
func (s *Server) Mode() string { return s.cfg.Mode }

func (s *Server) ServeHTTP(w http.ResponseWriter, r *http.Request) {
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
	if err := json.NewDecoder(io.LimitReader(r.Body, 32<<20)).Decode(&c); err != nil {
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
		if c.Email == nil {
			return wire.Decision{}, errors.New("an email capture needs email")
		}
		m := s.email(c.Email)
		m.Run, m.Action, m.Source = run, action, src
		m.Status, m.Error = model.Faked, c.Error
		if s.cfg.Mode == Observe {
			m.Status = model.Sent
		}
		if c.Error != "" {
			m.Status = model.Failed
		}
		m = s.Inbox.Add(m)
		act := wire.Fake
		if s.cfg.Mode == Observe {
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
			m.Response = &model.Response{Status: h.Status, Body: s.cut(secrets.Redact(h.Response))}
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
	req := recognize.Request{Method: strings.ToUpper(h.Method), URL: u, Headers: lower(h.Headers), Body: []byte(h.Body)}
	m := model.Message{Run: run, Action: action, Source: src, Method: req.Method,
		URL: secrets.Redact(h.URL), Headers: redactHeaders(req.Headers), Body: s.cut(secrets.Redact(h.Body))}
	rec := recognize.Recognize(req, &m)
	allowed := s.allowed(u.Hostname(), src.Host)
	d := wire.Decision{Mode: s.cfg.Mode}
	switch {
	case c.Phase == wire.After: // observed with nothing before: record as sent
		m.Status = model.Sent
		m.Response = &model.Response{Status: h.Status, Body: s.cut(secrets.Redact(h.Response))}
		if h.Status >= 400 || c.Error != "" {
			m.Status, m.Error = model.Failed, c.Error
		}
		d.Action = wire.Allow
	case s.cfg.Mode == Observe || allowed:
		m.Status = model.Sent // until the after phase says otherwise
		d.Action = wire.Allow
	default:
		f := recognize.FakeFor(rec, req, &m)
		m.Status = model.Faked
		m.Response = &model.Response{Status: f.Status, Body: s.cut(f.Body)}
		d.Action, d.Response = wire.Fake, &wire.Response{Status: f.Status, Headers: f.Headers, Body: f.Body}
	}
	m = s.Inbox.Add(m)
	d.ID = m.ID
	return d, nil
}

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
		ReplyTo: e.ReplyTo, Subject: e.Subject, Text: e.Text, HTML: e.HTML, Headers: redactHeaders(lower(e.Headers))}
	if e.Raw != "" {
		if parsed, err := ParseMIME([]byte(e.Raw)); err == nil {
			fillEmpty(&m, parsed)
		}
		m.Body = s.cut(secrets.Redact(e.Raw))
	}
	m.Text = secrets.Redact(m.Text)
	m.HTML = s.cut(secrets.Redact(m.HTML))
	return m
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

func (s *Server) cut(v string) string {
	if len(v) > s.cfg.MaxBody {
		return v[:s.cfg.MaxBody] + "\n…(truncated)"
	}
	return v
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
}

func (s *Server) begin(w http.ResponseWriter, r *http.Request) {
	var b actionBody
	if err := json.NewDecoder(io.LimitReader(r.Body, 1<<16)).Decode(&b); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	s.Inbox.Begin(b.Run, b.Action)
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) end(w http.ResponseWriter, _ *http.Request) {
	s.Inbox.End()
	w.WriteHeader(http.StatusNoContent)
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

// redactHeaders masks credentials in header values. Authorization-like
// headers are masked whole, whatever their format.
func redactHeaders(h map[string]string) map[string]string {
	out := make(map[string]string, len(h))
	for k, v := range h {
		switch k {
		case "authorization", "proxy-authorization", "cookie", "x-api-key", "api-key", "x-postmark-server-token", "x-echo-token":
			out[k] = secrets.Mask(v, 0)
		default:
			out[k] = secrets.Redact(v)
		}
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
