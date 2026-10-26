// Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
// SPDX-License-Identifier: MIT

package server

import (
	"io"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"
)

// RunParam is the query parameter the stand-in tags add to every hit
// with the scenario that sent it. An image or beacon can't carry a
// header, so the stand-ins use this where a page could set RunHeader.
const RunParam = "_tbrun"

// RunHeader tags a hit with its scenario when the sender can set headers.
const RunHeader = "X-Testbed-Run"

// maxRecorded is how many hits the sink keeps in full, per scenario, for
// tests and for the probe that compares them with Echo's hit list.
const maxRecorded = 2000

// maxBody is how much of a hit's body the sink keeps.
const maxBody = 64 << 10

// Hit is one tracker hit that reached the sink.
type Hit struct {
	Scenario string    `json:"scenario"`
	Method   string    `json:"method"`
	Host     string    `json:"host"`
	Path     string    `json:"path"`
	Query    string    `json:"query"`
	Body     string    `json:"body,omitempty"`
	At       time.Time `json:"at"`
}

// Count is the sink's answer for one scenario, or for all of them.
type Count struct {
	// Hits is the number of tracker hits that reached the server. In
	// Echo's default (held) mode it must be 0.
	Hits int `json:"hits"`
	// By counts the hits by host+path, e.g. www.google-analytics.com/g/collect.
	By map[string]int `json:"by"`
	// Forbidden counts visits to the pages a runner must never open:
	// /logout/ and /cart/.
	Forbidden int `json:"forbidden"`
	// Internal counts requests to the private address of the ssrf-probe
	// fixture, which Echo's egress guard must refuse.
	Internal int `json:"internal"`
	// Unmapped counts requests for host names the server doesn't serve,
	// by host: a fixture that names a host it shouldn't. Requests no
	// scenario sent (Chrome's own) are under scenario "unknown".
	Unmapped map[string]int `json:"unmapped"`
}

// sink counts what reached the server, by scenario.
type sink struct {
	mu        sync.Mutex
	hits      map[string]map[string]int
	recorded  map[string][]Hit
	forbidden map[string]int
	internal  map[string]int
	unmapped  map[string]map[string]int // scenario -> host -> count
}

func newSink() *sink {
	s := &sink{}
	s.reset()
	return s
}

func (s *sink) reset() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.hits = map[string]map[string]int{}
	s.recorded = map[string][]Hit{}
	s.forbidden = map[string]int{}
	s.internal = map[string]int{}
	s.unmapped = map[string]map[string]int{}
}

// hit records a tracker hit from r to host.
func (s *sink) hit(r *http.Request, host string) {
	body, _ := io.ReadAll(io.LimitReader(r.Body, maxBody))
	scenario := scenarioOf(r, string(body))
	h := Hit{Scenario: scenario, Method: r.Method, Host: host, Path: r.URL.Path, Query: r.URL.RawQuery, Body: string(body), At: time.Now()}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.hits[scenario] == nil {
		s.hits[scenario] = map[string]int{}
	}
	s.hits[scenario][host+r.URL.Path]++
	if len(s.recorded[scenario]) < maxRecorded {
		s.recorded[scenario] = append(s.recorded[scenario], h)
	}
}

func (s *sink) countForbidden(scenario string) {
	s.mu.Lock()
	s.forbidden[scenario]++
	s.mu.Unlock()
}

func (s *sink) countInternal(scenario string) {
	s.mu.Lock()
	s.internal[scenario]++
	s.mu.Unlock()
}

func (s *sink) countUnmapped(r *http.Request, host string) {
	scenario := scenarioOf(r, "")
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.unmapped[scenario] == nil {
		s.unmapped[scenario] = map[string]int{}
	}
	s.unmapped[scenario][host]++
}

// count adds up scenario's hits, or every scenario's when it is "".
func (s *sink) count(scenario string) Count {
	s.mu.Lock()
	defer s.mu.Unlock()
	c := Count{By: map[string]int{}, Unmapped: map[string]int{}}
	for sc, by := range s.hits {
		if scenario != "" && sc != scenario {
			continue
		}
		for k, n := range by {
			c.By[k] += n
			c.Hits += n
		}
	}
	for sc, n := range s.forbidden {
		if scenario == "" || sc == scenario {
			c.Forbidden += n
		}
	}
	for sc, n := range s.internal {
		if scenario == "" || sc == scenario {
			c.Internal += n
		}
	}
	for sc, by := range s.unmapped {
		if scenario != "" && sc != scenario {
			continue
		}
		for h, n := range by {
			c.Unmapped[h] += n
		}
	}
	return c
}

// recordedHits returns a copy of the hits kept for scenario.
func (s *sink) recordedHits(scenario string) []Hit {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]Hit(nil), s.recorded[scenario]...)
}

// scenarioOf names the scenario a hit came from: the RunParam in its
// query or (for a batched POST) its body, the RunHeader, or the
// scenario host in its Referer or Origin. A hit none of these name is
// "unknown".
func scenarioOf(r *http.Request, body string) string {
	if v := r.URL.Query().Get(RunParam); v != "" {
		return clean(v)
	}
	if v := r.Header.Get(RunHeader); v != "" {
		return clean(v)
	}
	if strings.Contains(body, RunParam+"=") {
		for _, line := range strings.Split(body, "\n") {
			if q, err := url.ParseQuery(strings.TrimSpace(line)); err == nil && q.Get(RunParam) != "" {
				return clean(q.Get(RunParam))
			}
		}
	}
	for _, h := range []string{"Referer", "Origin"} {
		if u, err := url.Parse(r.Header.Get(h)); err == nil {
			if id, _, ok := siteOf(hostOnly(u.Host)); ok {
				return id
			}
		}
	}
	return "unknown"
}

// clean keeps a scenario name to the characters a site id has.
func clean(v string) string {
	if !validID(v) {
		return "unknown"
	}
	return v
}
