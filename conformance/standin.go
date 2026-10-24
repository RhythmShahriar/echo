// Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
// SPDX-License-Identifier: MIT

package conformance

import (
	"bytes"
	"crypto/subtle"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"sort"
	"strings"
	"sync"
	"text/tabwriter"

	"github.com/RhythmShahriar/echo/catcher"
	"github.com/RhythmShahriar/echo/wire"
)

// Options configure the stand-in.
type Options struct {
	Mode  string   // seal (default) or observe
	Token string   // required in X-Echo-Token when set
	Allow []string // hosts reachable when sealed
	// Wire is the wire version the stand-in speaks: 0 for this Echo's
	// (wire.Version), or an older one to check that a connector falls back.
	// Speaking 1 it says so in /v1/health and refuses newer captures the way
	// an Echo of that version does.
	Wire int
	// Expect lists the scenarios the run must include (see Scenarios); the
	// report fails each one no capture showed.
	Expect []string
}

// Scenarios are what the stand-in recognizes in captures, for Expect and
// the report, in report order.
var Scenarios = []Scenario{
	{"email", "an email reported before it is sent", func(c wire.Capture) bool { return c.Kind == "email" && c.Phase == "" }},
	{"email-after", "a real send's outcome reported after (observe)", func(c wire.Capture) bool { return c.Kind == "email" && c.Phase == wire.After }},
	{"http-before", "an HTTP request reported before it is sent", func(c wire.Capture) bool { return c.Kind == "http" && c.Phase == wire.Before }},
	{"http-after", "an allowed request's response reported after", func(c wire.Capture) bool { return c.Kind == "http" && c.Phase == wire.After }},
	{"run", "a capture tied to a run", func(c wire.Capture) bool { return c.Run != "" }},
	{"site", "a capture that names its site", func(c wire.Capture) bool { return c.Source.Site != "" }},
	{"attachments", "an email that lists its attachments", func(c wire.Capture) bool { return c.Email != nil && len(c.Email.Attachments) > 0 }},
	{"wire-1", "a capture in wire 1", func(c wire.Capture) bool { return c.Version == 1 }},
	{"wire-2", "a capture in wire 2", func(c wire.Capture) bool { return c.Version == 2 }},
}

// Scenario is one kind of capture the stand-in recognizes.
type Scenario struct {
	Name  string
	About string
	Match func(wire.Capture) bool
}

// newer is what an Echo answers a capture from a newer wire version:
// the catcher's own refusal.
const newer = catcher.Newer

// Server is the strict stand-in: Echo's real catcher behind a check of
// every capture against the wire schema.
type Server struct {
	opt    Options
	schema *Schema
	echo   *catcher.Server

	mu       sync.Mutex
	captures int
	seen     map[string]int
	refused  int // newer captures refused while speaking an older wire
	problems []Problem
}

// Problem is a capture the stand-in refused, with why.
type Problem struct {
	Problems []string        `json:"problems"`
	Capture  json.RawMessage `json:"capture"`
}

// New returns a stand-in.
func New(o Options) (*Server, error) {
	if o.Mode == "" {
		o.Mode = catcher.Seal
	}
	if o.Mode != catcher.Seal && o.Mode != catcher.Observe {
		return nil, fmt.Errorf("mode must be seal or observe, not %q", o.Mode)
	}
	if o.Wire == 0 {
		o.Wire = wire.Version
	}
	if o.Wire < wire.MinVersion || o.Wire > wire.Version {
		return nil, fmt.Errorf("wire must be %d to %d, not %d", wire.MinVersion, wire.Version, o.Wire)
	}
	for _, e := range o.Expect {
		if scenario(e) == nil {
			return nil, fmt.Errorf("unknown scenario %q: want %s", e, scenarioNames())
		}
	}
	s, err := WireSchema()
	if err != nil {
		return nil, err
	}
	echo := catcher.NewServer(catcher.NewInbox(10000), catcher.Config{Mode: o.Mode, Allow: o.Allow, Token: o.Token, Version: "conformance"})
	return &Server{opt: o, schema: s, echo: echo, seen: map[string]int{}}, nil
}

// Catcher is the Echo behind the stand-in, for its SMTP sink and inbox.
func (s *Server) Catcher() *catcher.Server { return s.echo }

// ServeHTTP answers like Echo, with captures checked first. It also serves
// the report at GET /v1/conformance.
func (s *Server) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if s.opt.Token != "" && subtle.ConstantTimeCompare([]byte(r.Header.Get(wire.TokenHeader)), []byte(s.opt.Token)) != 1 {
		http.Error(w, "missing or wrong "+wire.TokenHeader, http.StatusUnauthorized)
		return
	}
	switch {
	case r.Method == http.MethodGet && r.URL.Path == "/v1/conformance":
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(s.Report())
	case r.Method == http.MethodGet && r.URL.Path == "/v1/health" && s.opt.Wire != wire.Version:
		s.health(w, r)
	case r.Method == http.MethodPost && r.URL.Path == "/v1/capture":
		s.capture(w, r)
	default:
		s.echo.ServeHTTP(w, r)
	}
}

// health is Echo's, saying the older wire version the stand-in speaks.
func (s *Server) health(w http.ResponseWriter, r *http.Request) {
	rec := httptest.NewRecorder()
	s.echo.ServeHTTP(rec, r)
	var h map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &h); err != nil {
		http.Error(w, "health: "+err.Error(), http.StatusInternalServerError)
		return
	}
	h["wire"] = s.opt.Wire
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(h)
}

func (s *Server) capture(w http.ResponseWriter, r *http.Request) {
	body, err := io.ReadAll(io.LimitReader(r.Body, 32<<20))
	if err != nil {
		http.Error(w, "bad capture: "+err.Error(), http.StatusBadRequest)
		return
	}
	var c wire.Capture
	_ = json.Unmarshal(body, &c)
	if c.Version > s.opt.Wire && c.Version <= wire.Version {
		// What an Echo of that version says. Not a problem: the connector
		// is expected to ask /v1/health and send the older version. A
		// version no Echo speaks yet is a problem, found below.
		s.mu.Lock()
		s.refused++
		s.mu.Unlock()
		http.Error(w, newer, http.StatusBadRequest)
		return
	}
	if problems := s.schema.Capture(body); len(problems) > 0 {
		s.mu.Lock()
		s.captures++
		s.problems = append(s.problems, Problem{Problems: problems, Capture: append(json.RawMessage(nil), body...)})
		s.mu.Unlock()
		http.Error(w, "conformance: "+strings.Join(problems, "; "), http.StatusBadRequest)
		return
	}
	s.mu.Lock()
	s.captures++
	for _, sc := range Scenarios {
		if sc.Match(c) {
			s.seen[sc.Name]++
		}
	}
	s.mu.Unlock()
	r.Body = io.NopCloser(bytes.NewReader(body))
	s.echo.ServeHTTP(w, r)
}

// Report is what the stand-in saw: one row per check, then the refused
// captures.
type Report struct {
	OK       bool      `json:"ok"`
	Captures int       `json:"captures"`
	Rows     []Row     `json:"rows"`
	Problems []Problem `json:"problems,omitempty"`
}

// Row is one line of the report. Result is pass, fail or info.
type Row struct {
	Check  string `json:"check"`
	Result string `json:"result"`
	Detail string `json:"detail"`
}

// Report sums up the captures so far.
func (s *Server) Report() Report {
	s.mu.Lock()
	defer s.mu.Unlock()
	r := Report{OK: true, Captures: s.captures, Problems: append([]Problem(nil), s.problems...)}
	add := func(check, result, detail string) {
		r.Rows = append(r.Rows, Row{check, result, detail})
		if result == "fail" {
			r.OK = false
		}
	}
	switch {
	case s.captures == 0:
		add("captures", "fail", "no captures arrived")
	case len(s.problems) == 0:
		add("schema", "pass", fmt.Sprintf("%s, all valid", count(s.captures, "capture")))
	default:
		add("schema", "fail", fmt.Sprintf("%d of %s broke the wire schema: %s", len(s.problems), count(s.captures, "capture"), s.problems[0].Problems[0]))
	}
	if s.opt.Wire != wire.Version {
		switch {
		case s.refused == 0:
			add("fallback", "pass", fmt.Sprintf("every capture was wire %d from the start", s.opt.Wire))
		case s.seen[fmt.Sprintf("wire-%d", s.opt.Wire)] > 0:
			add("fallback", "pass", fmt.Sprintf("refused %s, then got wire %d", count(s.refused, "newer capture"), s.opt.Wire))
		default:
			add("fallback", "fail", fmt.Sprintf("refused %s and never got wire %d: read /v1/health and send its version", count(s.refused, "newer capture"), s.opt.Wire))
		}
	}
	expect := map[string]bool{}
	for _, e := range s.opt.Expect {
		expect[e] = true
	}
	for _, sc := range Scenarios {
		n := s.seen[sc.Name]
		switch {
		case expect[sc.Name] && n == 0:
			add(sc.Name, "fail", "expected "+sc.About+", saw none")
		case expect[sc.Name]:
			add(sc.Name, "pass", fmt.Sprintf("%d: %s", n, sc.About))
		case n > 0:
			add(sc.Name, "info", fmt.Sprintf("%d: %s", n, sc.About))
		}
	}
	return r
}

// Table writes the report as a table, then each refused capture's
// problems.
func (r Report) Table(w io.Writer) {
	tw := tabwriter.NewWriter(w, 0, 0, 2, ' ', 0)
	fmt.Fprintln(tw, "CHECK\tRESULT\tDETAIL")
	for _, row := range r.Rows {
		fmt.Fprintf(tw, "%s\t%s\t%s\n", row.Check, strings.ToUpper(row.Result), row.Detail)
	}
	_ = tw.Flush()
	for i, p := range r.Problems {
		fmt.Fprintf(w, "\nrefused capture %d:\n", i+1)
		for _, line := range p.Problems {
			fmt.Fprintf(w, "  %s\n", line)
		}
	}
	if r.OK {
		fmt.Fprintln(w, "\nPASS")
	} else {
		fmt.Fprintln(w, "\nFAIL")
	}
}

// count is "1 capture" or "3 captures".
func count(n int, noun string) string {
	if n == 1 {
		return "1 " + noun
	}
	return fmt.Sprintf("%d %ss", n, noun)
}

func scenario(name string) *Scenario {
	for i := range Scenarios {
		if Scenarios[i].Name == name {
			return &Scenarios[i]
		}
	}
	return nil
}

func scenarioNames() string {
	names := make([]string, 0, len(Scenarios))
	for _, sc := range Scenarios {
		names = append(names, sc.Name)
	}
	sort.Strings(names)
	return strings.Join(names, ", ")
}
