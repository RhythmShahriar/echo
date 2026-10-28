// Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
// SPDX-License-Identifier: MIT

package browse

import (
	"context"
	"encoding/json"
	"fmt"
	"sync"
	"time"

	"github.com/go-rod/rod/lib/proto"

	"github.com/RhythmShahriar/echo/consent"
	"github.com/RhythmShahriar/echo/consent/browse/egress"
	"github.com/RhythmShahriar/echo/internal/browser"
)

// Engine is what every run of one consent command shares: one Chrome,
// the browser-wide refuse-all listener, one egress guard per target and
// the hub that routes Chrome's events to the runs.
type Engine struct {
	plan     *consent.Plan
	b        *browser.Browser
	h        *hub
	internal *egress.Proxy
	ua       string

	mu     sync.Mutex
	guards map[string]*egress.Proxy // target URL -> its guard
	runs   []*Run
	// refused counts the requests closed runs refused in the browser
	// (--map-only): they never reach a guard, so its count misses them.
	refused int
	// frameless counts the frameless requests closed runs saw inside
	// their contexts: Chrome's own traffic the listeners may never see.
	frameless int
}

// Start launches Chrome for p with its egress listener and starts
// routing its events. Close it when the command ends.
func Start(ctx context.Context, p *consent.Plan) (*Engine, error) {
	internal, err := egress.StartRefuseAll(nil)
	if err != nil {
		return nil, fmt.Errorf("consent: starting the egress guard: %w", err)
	}
	b, err := Launch(ctx, p, internal.Addr())
	if err != nil {
		_ = internal.Close()
		return nil, err
	}
	h, err := newHub(ctx, b)
	if err != nil {
		b.Close()
		_ = internal.Close()
		return nil, err
	}
	e := &Engine{plan: p, b: b, h: h, internal: internal, guards: map[string]*egress.Proxy{}}
	if v, err := (proto.BrowserGetVersion{}).Call(b.Browser); err == nil {
		e.ua = v.UserAgent
	}
	return e, nil
}

// Browser names the running browser without its path, e.g. "Chrome
// 153.0.8010.52": results and reports are shared, the path can name the
// user. The -v Browser line has the path.
func (e *Engine) Browser() string { return e.b.Name() }

// Close ends every run still open, stops the listeners, closes Chrome
// and removes its profile.
func (e *Engine) Close() {
	e.mu.Lock()
	runs := e.runs
	e.runs = nil
	guards := e.guards
	e.guards = map[string]*egress.Proxy{}
	e.mu.Unlock()
	for _, r := range runs {
		r.Close()
	}
	e.h.close()
	e.b.Close()
	for _, g := range guards {
		_ = g.Close()
	}
	_ = e.internal.Close()
}

// Network sums what the listeners refused: the browser-wide one counts
// Chrome's own traffic, the guards the page traffic they refused and
// any browser service host a page context asked for. Requests a closed
// run refused in the browser (--map-only) are added to Refused and the
// frameless requests it saw in its context to BrowserInternal.
func (e *Engine) Network() consent.Network {
	st := e.internal.Stats()
	e.mu.Lock()
	defer e.mu.Unlock()
	for _, g := range e.guards {
		st = st.Add(g.Stats())
	}
	return consent.Network{Refused: st.Refused + e.refused, BrowserInternal: st.BrowserInternal + e.frameless}
}

// Mapped reports whether --map-host maps any host, so every writer can
// say the result came from mapped hosts rather than the real ones.
func (e *Engine) Mapped() bool { return newMapper(e.plan.MapHosts).any() }

// guard returns the target's guard, starting it on first use: every
// run of a target goes through the same one. A target --map-host
// answers gets a guard for public destinations only, since its own
// address is Echo's to answer and never looked up.
func (e *Engine) guard(t consent.Target) (*egress.Proxy, error) {
	key := t.URL.String()
	e.mu.Lock()
	defer e.mu.Unlock()
	if g, ok := e.guards[key]; ok {
		return g, nil
	}
	host := t.URL.Hostname()
	if ok, _ := newMapper(e.plan.MapHosts).lookup(host); ok {
		host = ""
	}
	// No Log: a refusal per request would flood the browser log; the
	// runs record each refused request instead.
	g, err := egress.Start(egress.Options{Target: host, MapHosts: mapKeys(e.plan.MapHosts), MapOnly: e.plan.MapOnly,
		Upstream: e.plan.Proxy})
	if err != nil {
		return nil, err
	}
	e.guards[key] = g
	return g, nil
}

// mapKeys lists the --map-host patterns.
func mapKeys(m map[string]string) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	return out
}

// Run is one run in its own fresh context: a tab with the run's
// emulation, interception and recorder in place before the first page.
type Run struct {
	e    *Engine
	s    *session
	once sync.Once
	out  *Recording
}

// Recording is what one run recorded.
type Recording struct {
	Events    []consent.Event
	Dropped   int
	Truncated bool
	Limit     bool // Limits.Requests was reached
	Requests  int  // requests recorded
	Held      int  // hits answered in the browser
	Refused   int  // requests refused before they left (--map-only)
	Internal  int  // requests without a page frame: browser traffic
	// Start is the wall time of the run's first navigation, where the
	// run clock (AtMS) starts.
	Start time.Time

	har map[*consent.Request]*harExtra // with --har
}

// Open opens a run of kind for target. wait bounds how long Chrome may
// take to open and set up the tab.
func (e *Engine) Open(ctx context.Context, t consent.Target, kind consent.RunKind, wait time.Duration) (*Run, error) {
	return e.open(ctx, t, kind, wait, runExtras{})
}

// runExtras are what a check adds to a run beyond the plan: where its
// events stream and the X-Echo-Run value its same-site requests carry.
type runExtras struct {
	sinks   []func(consent.Event)
	echoRun string
}

// open is Open with the check's extras.
func (e *Engine) open(ctx context.Context, t consent.Target, kind consent.RunKind, wait time.Duration, x runExtras) (*Run, error) {
	g, err := e.guard(t)
	if err != nil {
		return nil, err
	}
	env := newEmulation(e.plan, kind, e.ua, e.b.Version)
	s, err := e.h.openSession(ctx, sessionOptions{Plan: e.plan, Target: t, Kind: kind, Guard: g, Env: env, Timeout: wait,
		Sinks: x.sinks, EchoRun: x.echoRun})
	if err != nil {
		return nil, err
	}
	r := &Run{e: e, s: s}
	e.mu.Lock()
	e.runs = append(e.runs, r)
	e.mu.Unlock()
	return r, nil
}

// Navigate loads raw in the run's tab.
func (r *Run) Navigate(raw string) error { return r.s.navigate(raw) }

// EvalMain runs a constant script in the tab's main world.
func (r *Run) EvalMain(ctx context.Context, js string) (json.RawMessage, error) {
	return r.s.evalMain(ctx, js)
}

// BeginChoice is called just before the run clicks its choice: events
// wait until SetChoice gives the click's time.
func (r *Run) BeginChoice() { r.s.rec.beginChoice() }

// SetChoice says the run is in phase ph from run time at on (at < 0:
// no choice was made).
func (r *Run) SetChoice(at int64, ph consent.Phase) { r.s.rec.setChoice(at, ph) }

// AddVisit starts a visit (load, page, reload) at run time at and
// returns its index.
func (r *Run) AddVisit(at int64) int { return r.s.rec.addVisit(at) }

// Now is the current time on the run clock.
func (r *Run) Now() int64 { return r.s.rec.now() }

// PageTime puts a page time (performance.timeOrigin +
// performance.now(), ms) on the run clock.
func (r *Run) PageTime(ms float64) int64 { return r.s.rec.atPage(ms) }

// Snapshot records the cookies and storage keys that are new or changed
// since the last snapshot. The run calls it after every step.
func (r *Run) Snapshot(ctx context.Context) error { return r.s.snapshot(ctx) }

// LimitReached is closed when the run reaches Limits.Requests.
func (r *Run) LimitReached() <-chan struct{} { return r.s.rec.limit }

// Close ends the run, disposes of its context and returns what it
// recorded. It may be called again; the later calls return the same.
func (r *Run) Close() *Recording {
	r.once.Do(func() {
		r.out = r.s.close()
		r.e.mu.Lock()
		r.e.refused += r.out.Refused
		r.e.frameless += r.out.Internal
		for i, x := range r.e.runs {
			if x == r {
				r.e.runs = append(r.e.runs[:i], r.e.runs[i+1:]...)
				break
			}
		}
		r.e.mu.Unlock()
	})
	return r.out
}
