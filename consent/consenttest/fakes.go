// Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
// SPDX-License-Identifier: MIT

package consenttest

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/RhythmShahriar/echo/consent"
)

// Answer is a scripted reply to an Eval call.
type Answer struct {
	Result json.RawMessage
	Err    error
}

// ScriptName is the name FakeTab answers a script by: the text of its
// first line when that is a comment ("// banner-visible" names the
// script banner-visible), else the whole script.
func ScriptName(js string) string {
	first, _, _ := strings.Cut(strings.TrimSpace(js), "\n")
	if name, ok := strings.CutPrefix(first, "//"); ok {
		return strings.TrimSpace(name)
	}
	return js
}

// FakeTab is a consent.Tab in memory: scripted Eval answers keyed by
// script (exact text first, then ScriptName), a cookie jar, local
// storage, a click log and a clock. Unscripted evals return null. It is
// safe for concurrent use.
type FakeTab struct {
	mu sync.Mutex

	Isolated  map[string]Answer // EvalIsolated answers
	Main      map[string]Answer // EvalMain answers
	Jar       []consent.Cookie  // what Cookies returns
	Values    map[string]string // cookie values, by name, for CookieValue
	Storage   map[string]string // localStorage, for LocalStorage
	Clicks    map[string]consent.ClickResult
	FrameList []consent.Frame
	// OnClick, when set, runs after each click under no lock, so a test
	// can change the tab (set the CMP cookie, hide the banner).
	OnClick func(t *FakeTab, l consent.Locator)

	Clock   int64             // run clock, in ms
	Clicked []consent.Locator // every Click, in order
	Evals   []string          // every script evaluated, by ScriptName
	Shots   []string          // every screenshot name
	Reloads int
}

var _ consent.Tab = (*FakeTab)(nil)

func (t *FakeTab) eval(answers map[string]Answer, js string) (json.RawMessage, error) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.Evals = append(t.Evals, ScriptName(js))
	if a, ok := answers[js]; ok {
		return a.Result, a.Err
	}
	if a, ok := answers[ScriptName(js)]; ok {
		return a.Result, a.Err
	}
	return json.RawMessage("null"), nil
}

// EvalIsolated answers from Isolated.
func (t *FakeTab) EvalIsolated(_ context.Context, js string, _ ...any) (json.RawMessage, error) {
	return t.eval(t.Isolated, js)
}

// EvalMain answers from Main.
func (t *FakeTab) EvalMain(_ context.Context, js string) (json.RawMessage, error) {
	return t.eval(t.Main, js)
}

// Click logs l and answers from Clicks by l.CSS; an unscripted click
// succeeds at the current clock time.
func (t *FakeTab) Click(_ context.Context, l consent.Locator) (consent.ClickResult, error) {
	t.mu.Lock()
	t.Clicked = append(t.Clicked, l)
	res, ok := t.Clicks[l.CSS]
	if !ok {
		res = consent.ClickResult{Clicked: true, Text: l.CSS, AtMS: t.Clock}
	}
	on := t.OnClick
	t.mu.Unlock()
	if on != nil {
		on(t, l)
	}
	return res, nil
}

// Cookies returns a copy of Jar.
func (t *FakeTab) Cookies(context.Context) ([]consent.Cookie, error) {
	t.mu.Lock()
	defer t.mu.Unlock()
	return slices.Clone(t.Jar), nil
}

// CookieValue reads Values.
func (t *FakeTab) CookieValue(_ context.Context, name string) (string, error) {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.Values[name], nil
}

// LocalStorage reads Storage.
func (t *FakeTab) LocalStorage(_ context.Context, key string) (string, error) {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.Storage[key], nil
}

// Screenshot logs name and returns evidence/<name>.png.
func (t *FakeTab) Screenshot(_ context.Context, name string) (string, error) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.Shots = append(t.Shots, name)
	return "evidence/" + name + ".png", nil
}

// Frames returns FrameList.
func (t *FakeTab) Frames(context.Context) ([]consent.Frame, error) {
	t.mu.Lock()
	defer t.mu.Unlock()
	return slices.Clone(t.FrameList), nil
}

// Now returns Clock.
func (t *FakeTab) Now() int64 {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.Clock
}

// Advance moves the clock on by d.
func (t *FakeTab) Advance(d time.Duration) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.Clock += d.Milliseconds()
}

// SetCookie adds or replaces a cookie in the jar with its value.
func (t *FakeTab) SetCookie(c consent.Cookie, value string) {
	t.mu.Lock()
	defer t.mu.Unlock()
	c.Length = len(value)
	t.Jar = slices.DeleteFunc(t.Jar, func(o consent.Cookie) bool { return o.Name == c.Name })
	t.Jar = append(t.Jar, c)
	if t.Values == nil {
		t.Values = map[string]string{}
	}
	t.Values[c.Name] = value
}

// Reload counts the reload.
func (t *FakeTab) Reload(context.Context) error {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.Reloads++
	return nil
}

// FakeClassifier is a two-tracker catalog: GA4 (/g/collect, cookie
// _ga) and Meta Pixel (/tr, cookie _fbp).
type FakeClassifier struct {
	mu     sync.Mutex
	Custom [][]byte // every AddCustom call
}

var _ consent.Classifier = (*FakeClassifier)(nil)

var fakeTrackers = []consent.TrackerInfo{
	{ID: "ga4", Name: "Google Analytics 4", Vendor: "Google", Category: consent.CatAnalytics, Need: consent.NeedNeeded, Matches: "/g/collect; cookie _ga"},
	{ID: "meta-pixel", Name: "Meta Pixel", Vendor: "Meta", Category: consent.CatAdvertising, Need: consent.NeedNeeded, Matches: "/tr; cookie _fbp"},
}

func fakeHit(id string, kind consent.HitKind) *consent.Hit {
	for _, t := range fakeTrackers {
		if t.ID == id {
			return &consent.Hit{Tracker: t.ID, Name: t.Name, Category: t.Category, Need: t.Need, Kind: kind}
		}
	}
	return nil
}

// Request matches a path of /g/collect (ga4) or /tr (meta-pixel).
func (c *FakeClassifier) Request(r *consent.Request, _ []byte, _ *url.URL) *consent.Hit {
	if r == nil {
		return nil
	}
	u, err := url.Parse(r.URL)
	if err != nil {
		return nil
	}
	switch u.Path {
	case "/g/collect":
		return fakeHit("ga4", consent.HitBeacon)
	case "/tr", "/tr/":
		return fakeHit("meta-pixel", consent.HitBeacon)
	}
	return nil
}

// Cookie matches _ga (and _ga_*) and _fbp.
func (c *FakeClassifier) Cookie(k *consent.Cookie) *consent.Hit {
	switch {
	case k == nil:
		return nil
	case k.Name == "_ga" || strings.HasPrefix(k.Name, "_ga_"):
		return fakeHit("ga4", consent.HitBeacon)
	case k.Name == "_fbp":
		return fakeHit("meta-pixel", consent.HitBeacon)
	}
	return nil
}

// Storage matches nothing.
func (c *FakeClassifier) Storage(*consent.StorageKey) *consent.Hit { return nil }

// Hold holds what Request matches.
func (c *FakeClassifier) Hold(r *consent.Request) bool { return c.Request(r, nil, nil) != nil }

// Trackers lists the two trackers.
func (c *FakeClassifier) Trackers() []consent.TrackerInfo { return slices.Clone(fakeTrackers) }

// Info names the fake catalog.
func (c *FakeClassifier) Info() consent.DataInfo {
	return consent.DataInfo{Version: consent.DataVersion, SHA256: "fake", Count: len(fakeTrackers)}
}

// AddCustom records the YAML and accepts it.
func (c *FakeClassifier) AddCustom(yaml []byte) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.Custom = append(c.Custom, slices.Clone(yaml))
	return nil
}

// FakeSignals decodes gcs fully and Consent Mode hook records simply
// (["consent", "default"|"update", {...}]); gcd, TCF and GPP are left
// unknown.
type FakeSignals struct{}

var _ consent.SignalDecoder = FakeSignals{}

// GCS decodes G1xy: x is ad_storage, y analytics_storage.
func (FakeSignals) GCS(raw string) consent.GCS {
	if len(raw) != 4 || !strings.HasPrefix(raw, "G1") {
		return consent.GCS{AdStorage: consent.Unknown, AnalyticsStorage: consent.Unknown}
	}
	v := func(b byte) consent.Value {
		switch b {
		case '1':
			return consent.Granted
		case '0':
			return consent.Denied
		}
		return consent.Unknown
	}
	g := consent.GCS{AdStorage: v(raw[2]), AnalyticsStorage: v(raw[3])}
	g.Known = g.AdStorage != consent.Unknown && g.AnalyticsStorage != consent.Unknown
	return g
}

// GCD leaves every signal unknown.
func (FakeSignals) GCD(string) consent.GCD {
	u := consent.GCDSignal{Default: consent.Unknown, Update: consent.Unknown}
	return consent.GCD{AdStorage: u, AnalyticsStorage: u, AdUserData: u, AdPersonalization: u}
}

// Hook reads gtag and datalayer consent default and update records.
func (FakeSignals) Hook(record []byte) (*consent.Signal, error) {
	if len(record) > consent.MaxHookRecord {
		return nil, errors.New("hook record too large")
	}
	var h consent.HookRecord
	if err := json.Unmarshal(record, &h); err != nil {
		return nil, err
	}
	if (h.Src != "gtag" && h.Src != "datalayer") || len(h.Args) < 3 {
		return nil, nil
	}
	var verb, cmd string
	if json.Unmarshal(h.Args[0], &verb) != nil || verb != "consent" || json.Unmarshal(h.Args[1], &cmd) != nil {
		return nil, nil
	}
	if cmd != "default" && cmd != "update" {
		return nil, nil
	}
	var state map[string]string
	if err := json.Unmarshal(h.Args[2], &state); err != nil {
		return nil, nil
	}
	s := &consent.Signal{Source: h.Src, Command: cmd, State: map[string]consent.Value{}}
	for k, v := range state {
		switch consent.Value(v) {
		case consent.Granted, consent.Denied:
			s.State[k] = consent.Value(v)
		default:
			s.State[k] = consent.Unknown
		}
	}
	return s, nil
}

// TCF reports it doesn't decode.
func (FakeSignals) TCF(string) (*consent.TCF, error) {
	return nil, errors.New("consenttest: TCF not decoded")
}

// GPP reports it doesn't decode.
func (FakeSignals) GPP(string) (*consent.GPP, error) {
	return nil, errors.New("consenttest: GPP not decoded")
}

// FakeState returns State (nil: no stored choice) and logs the specs it
// was asked to read.
type FakeState struct {
	mu    sync.Mutex
	State *consent.CMPState
	Err   error
	Specs []consent.VerifySpec
}

var _ consent.StateReader = (*FakeState)(nil)

// Read returns State and Err.
func (s *FakeState) Read(_ context.Context, _ consent.Tab, v consent.VerifySpec) (*consent.CMPState, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.Specs = append(s.Specs, v)
	return s.State, s.Err
}

// FakeCMP is a CMP driver with scripted answers: Info for Detect, a
// Choice and Reason per run kind for Find and Choose, Showing for
// BannerShowing. Calls logs every method called, by name.
type FakeCMP struct {
	mu      sync.Mutex
	Found   consent.CMPInfo
	Choices map[consent.RunKind]*consent.Choice
	Reasons map[consent.RunKind]consent.Reason
	Showing bool
	Stored  *consent.CMPState
	Calls   []string
}

var _ consent.CMPDriver = (*FakeCMP)(nil)

func (c *FakeCMP) log(name string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.Calls = append(c.Calls, name)
}

// Detect returns Found.
func (c *FakeCMP) Detect(context.Context, consent.Tab, *consent.Plan, time.Duration) (consent.CMPInfo, error) {
	c.log("Detect")
	return c.Found, nil
}

// Find returns the scripted choice for k without its verification.
func (c *FakeCMP) Find(_ context.Context, _ consent.Tab, _ *consent.Plan, _ consent.CMPInfo, k consent.RunKind) (*consent.Choice, consent.Reason, error) {
	c.log("Find " + string(k))
	ch, r := c.choice(k)
	if ch != nil {
		ch.Verified, ch.VerifiedBy, ch.State = false, "", nil
	}
	return ch, r, nil
}

// Choose returns the scripted choice and reason for k.
func (c *FakeCMP) Choose(_ context.Context, _ consent.Tab, _ *consent.Plan, _ consent.CMPInfo, k consent.RunKind) (*consent.Choice, consent.Reason, error) {
	c.log("Choose " + string(k))
	ch, r := c.choice(k)
	return ch, r, nil
}

func (c *FakeCMP) choice(k consent.RunKind) (*consent.Choice, consent.Reason) {
	c.mu.Lock()
	defer c.mu.Unlock()
	r := c.Reasons[k]
	ch := c.Choices[k]
	if ch == nil {
		if r != consent.ReasonNone {
			return nil, r
		}
		ch = &consent.Choice{How: "button", Selector: "#" + string(k), Text: k.Label(), Layer: 1, Clicks: 1, Verified: true, VerifiedBy: "banner"}
	}
	cp := *ch
	return &cp, r
}

// Reopen logs the call.
func (c *FakeCMP) Reopen(context.Context, consent.Tab, *consent.Plan, consent.CMPInfo) error {
	c.log("Reopen")
	return nil
}

// BannerShowing returns Showing.
func (c *FakeCMP) BannerShowing(context.Context, consent.Tab, consent.CMPInfo) (bool, error) {
	c.log("BannerShowing")
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.Showing, nil
}

// State returns Stored.
func (c *FakeCMP) State(context.Context, consent.Tab, consent.CMPInfo) (*consent.CMPState, error) {
	c.log("State")
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.Stored, nil
}

// Rules lists one rule, "fake".
func (c *FakeCMP) Rules() []consent.CMPRuleInfo {
	return []consent.CMPRuleInfo{{ID: "fake", Name: "Fake CMP", DetectedBy: "selector:#fake-banner", Accept: "#accept", Reject: "#reject", StateFrom: "banner"}}
}

// Info names the fake rule pack.
func (c *FakeCMP) Info() consent.DataInfo {
	return consent.DataInfo{Version: consent.DataVersion, SHA256: "fake", Count: 1}
}

// FakeRunner returns a copy of Result (and Err) from Run and Detected
// from Detect. Plans logs every plan it was given.
type FakeRunner struct {
	mu       sync.Mutex
	Result   *consent.Result
	Err      error
	Detected *consent.DetectResult
	Plans    []*consent.Plan
}

var _ consent.Runner = (*FakeRunner)(nil)

// Run returns a copy of Result with Err.
func (r *FakeRunner) Run(_ context.Context, p *consent.Plan) (*consent.Result, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.Plans = append(r.Plans, p)
	return CloneResult(r.Result), r.Err
}

// Detect returns Detected, or an error when it is nil.
func (r *FakeRunner) Detect(_ context.Context, p *consent.Plan) (*consent.DetectResult, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.Plans = append(r.Plans, p)
	if r.Detected == nil {
		return nil, fmt.Errorf("consenttest: no DetectResult scripted")
	}
	d := *r.Detected
	return &d, nil
}

// FakeEvaluator calls Func when set and counts the calls. Without Func
// it leaves the result as it is, so a golden result keeps its issues.
type FakeEvaluator struct {
	mu    sync.Mutex
	Func  func(r *consent.Result, p *consent.Plan)
	Calls int
}

var _ consent.Evaluator = (*FakeEvaluator)(nil)

// Evaluate runs Func.
func (e *FakeEvaluator) Evaluate(r *consent.Result, p *consent.Plan) {
	e.mu.Lock()
	e.Calls++
	f := e.Func
	e.mu.Unlock()
	if f != nil {
		f(r, p)
	}
}

// FakeComparer returns a copy of Comparison, or of the current result's
// own Comparison when that is nil (compare-cur holds the expected one).
type FakeComparer struct {
	Comparison *consent.Comparison
}

var _ consent.Comparer = (*FakeComparer)(nil)

// Compare returns Comparison, else cur.Comparison, else an empty one.
func (c *FakeComparer) Compare(base, cur *consent.Result, _ []string) *consent.Comparison {
	src := c.Comparison
	if src == nil && cur != nil {
		src = cur.Comparison
	}
	if src == nil {
		out := &consent.Comparison{Changes: []consent.Change{}}
		if base != nil {
			out.BaseID, out.BaseAt = base.ID, base.Started
		}
		return out
	}
	cp := *src
	cp.Changes = slices.Clone(src.Changes)
	return &cp
}
