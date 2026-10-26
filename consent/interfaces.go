// Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
// SPDX-License-Identifier: MIT

package consent

import (
	"context"
	"encoding/json"
	"net/url"
	"time"
)

// TrackerInfo is one catalog entry as `consent trackers` lists it.
type TrackerInfo struct {
	ID       string      `json:"id"`
	Name     string      `json:"name"`
	Vendor   string      `json:"vendor"`
	Category Category    `json:"category"`
	Need     ConsentNeed `json:"need"`
	Matches  string      `json:"matches"` // one-line summary for `consent trackers`
	Fix      string      `json:"fix"`
	Docs     string      `json:"docs"`
	Sources  []string    `json:"sources,omitempty"`
}

// Classifier is the tracker catalog: it tells tracker requests,
// cookies and storage keys from the rest (consent/tracker).
type Classifier interface {
	// Request classifies a request; body is the parse window (may be nil).
	// nil means not a tracker. Never panics on any input.
	Request(r *Request, body []byte, site *url.URL) *Hit
	// Cookie classifies a cookie: Tracker, Category and Need filled; nil
	// when it isn't a tracker cookie.
	Cookie(c *Cookie) *Hit
	// Storage classifies a storage key as Cookie does a cookie.
	Storage(k *StorageKey) *Hit
	// Hold reports whether r is a hit endpoint that may be answered
	// locally. It is false for every script URL.
	Hold(r *Request) bool
	// Trackers lists the catalog, custom entries included.
	Trackers() []TrackerInfo
	// Info names the compiled-in catalog: version, hash and count.
	Info() DataInfo
	// AddCustom loads consent.trackers from the config (YAML), checking fixtures.
	AddCustom(yaml []byte) error
}

// SignalDecoder reads consent signals: Google's gcs and gcd parameters,
// hook records and TCF and GPP strings (consent/signal).
type SignalDecoder interface {
	// GCS decodes a gcs parameter.
	GCS(raw string) GCS
	// GCD decodes a gcd parameter.
	GCD(raw string) GCD
	// Hook parses one hooks.js record (JSON, capped) into a Signal; nil when not a consent signal.
	Hook(record []byte) (*Signal, error)
	// TCF decodes a TCF consent string.
	TCF(tc string) (*TCF, error)
	// GPP decodes a GPP string.
	GPP(s string) (*GPP, error)
}

// VerifySpec says where a CMP keeps its stored choice.
type VerifySpec struct {
	Parser  string   // cookiebot, onetrust, cookieyes, complianz, usercentrics, borlabs, didomi, iubenda, tcf, banner
	Cookies []string // cookie names or prefixes the parser reads
	Storage []string // localStorage keys the parser reads
	StateJS string   // optional constant expression evaluated in the main world, returning JSON
}

// StateReader reads a CMP's stored choice from a tab (consent/signal).
type StateReader interface {
	// Read reads the state v describes; nil when there is none yet.
	Read(ctx context.Context, t Tab, v VerifySpec) (*CMPState, error)
}

// Frame is one frame of a tab.
type Frame struct {
	ID, URL, Origin string
	CrossOrigin     bool
}

// ClickResult is what one click on a banner control did.
type ClickResult struct {
	Clicked       bool
	Text          string
	Box           Box
	Covered       bool
	NavigatedAway bool
	AtMS          int64  // run clock time of the click: the pointerdown hook record, else the dispatch time
	Detail        string // why it failed, e.g. "closed shadow root"
}

// Tab is the slice of a browser tab the CMP driver and state reader use.
// consent/browse implements it with rod; consenttest.FakeTab in memory.
type Tab interface {
	// EvalIsolated runs js in an isolated world; the result is capped at 256 KiB.
	EvalIsolated(ctx context.Context, js string, args ...any) (json.RawMessage, error)
	// EvalMain runs js in the page's main world; constant scripts only.
	EvalMain(ctx context.Context, js string) (json.RawMessage, error)
	// Click checks the element is visible, sized and top-most and clicks
	// it with real mouse events.
	Click(ctx context.Context, l Locator) (ClickResult, error)
	// Cookies lists the tab's cookies; values are never returned.
	Cookies(ctx context.Context) ([]Cookie, error)
	// CookieValue reads one cookie's value, for state parsers only; it is never stored.
	CookieValue(ctx context.Context, name string) (string, error)
	// LocalStorage reads one localStorage value, for state parsers only; it is never stored.
	LocalStorage(ctx context.Context, key string) (string, error)
	// Screenshot saves the viewport as name and returns its path relative to Plan.Dir.
	Screenshot(ctx context.Context, name string) (string, error)
	// Frames lists the tab's frames.
	Frames(ctx context.Context) ([]Frame, error)
	// Now is AtMS on the run clock.
	Now() int64
	// Reload loads the page again.
	Reload(ctx context.Context) error
}

// CMPRuleInfo is one CMP rule as `consent cmps` lists it.
type CMPRuleInfo struct {
	ID         string   `json:"id"`
	Name       string   `json:"name"`
	DetectedBy string   `json:"detected_by"`
	Accept     string   `json:"accept"`
	Reject     string   `json:"reject"`
	StateFrom  string   `json:"state_from"`
	Sources    []string `json:"sources,omitempty"`
}

// CMPDriver finds a page's CMP and makes the choice on it (consent/cmp).
type CMPDriver interface {
	// Detect waits up to wait for a banner. CMPInfo.Found false with
	// Present true means the CMP is on the page but showed no banner.
	Detect(ctx context.Context, t Tab, p *Plan, wait time.Duration) (CMPInfo, error)
	// Find locates the Accept or Reject control without clicking it
	// (consent detect): Choice has How, Selector, Text, Layer and Box set.
	Find(ctx context.Context, t Tab, p *Plan, cmp CMPInfo, k RunKind) (*Choice, Reason, error)
	// Choose makes the Accept or Reject choice and verifies it.
	Choose(ctx context.Context, t Tab, p *Plan, cmp CMPInfo, k RunKind) (*Choice, Reason, error)
	// Reopen opens the banner or preference center again (withdraw run).
	Reopen(ctx context.Context, t Tab, p *Plan, cmp CMPInfo) error
	// BannerShowing reports whether the banner is visible now.
	BannerShowing(ctx context.Context, t Tab, cmp CMPInfo) (bool, error)
	// State reads the CMP's stored choice with the rule's verify spec (for
	// Run.FinalState after the reload); nil when the rule has none.
	State(ctx context.Context, t Tab, cmp CMPInfo) (*CMPState, error)
	// Rules lists the CMP rules.
	Rules() []CMPRuleInfo
	// Info names the compiled-in rule pack: version, hash and count.
	Info() DataInfo
}

// Runner loads the pages in a browser and records the runs
// (consent/browse).
type Runner interface {
	// Run loads every target with every run kind and returns the pages
	// with their runs filled; it does not judge. A failure that stops the
	// whole check is a *FatalError; a failed run is a Run with a Reason.
	Run(ctx context.Context, p *Plan) (*Result, error)
	// Detect loads p.Targets[0] once (hits held), waits for the banner and
	// finds both controls without clicking.
	Detect(ctx context.Context, p *Plan) (*DetectResult, error)
}

// DetectResult is what `consent detect` found on a page.
type DetectResult struct {
	URL        string  `json:"url"`
	CMP        CMPInfo `json:"cmp"`
	Accept     *Choice `json:"accept"`
	Reject     *Choice `json:"reject"`
	Screenshot string  `json:"screenshot"`
	Browser    string  `json:"browser"`
}

// Evaluator judges a recorded result (consent/verdict).
type Evaluator interface {
	// Evaluate fills Page.ConsentMode, Page.NeedsNone, r.Issues and r.Inconclusive.
	Evaluate(r *Result, p *Plan)
}

// Comparer compares a result with a baseline (consent/verdict).
type Comparer interface {
	// Compare lists what changed from base to cur; accept holds the keys
	// taken as intended.
	Compare(base, cur *Result, accept []string) *Comparison
}
