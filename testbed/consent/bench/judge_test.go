// Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
// SPDX-License-Identifier: MIT

package main

import (
	"strings"
	"testing"
	"time"

	"github.com/RhythmShahriar/echo/consent"
	"github.com/RhythmShahriar/echo/model"
	"github.com/RhythmShahriar/echo/testbed/consent/scenarios"
	"github.com/RhythmShahriar/echo/testbed/consent/server"
)

// c02 is the manifest's c02 as the judge sees it: three expects and a
// forbid.
func c02() scenarios.Scenario {
	return scenarios.Scenario{ID: "c02", CMP: "complianz",
		Expect: []scenarios.Match{
			{Check: "consent/before-choice", Tracker: "meta-pixel", Level: "error"},
			{Check: "consent/after-reject", Tracker: "meta-pixel", Level: "error"},
		},
		Forbid: []scenarios.Match{{Check: "consent/before-choice", Tracker: "ga4"}}}
}

func TestJudgeBadExpectAndForbid(t *testing.T) {
	before := issue("consent/before-choice", "meta-pixel", model.Error, "Meta Pixel fired before the visitor chose")
	after := issue("consent/after-reject", "meta-pixel", model.Error, "Meta Pixel fired after Reject")
	ga4 := issue("consent/before-choice", "ga4", model.Error, "GA4 fired before the visitor chose")
	for _, c := range []struct {
		name   string
		issues []consent.Issue
		want   string
		detail string
	}{
		{"every expect", []consent.Issue{before, after}, Caught, ""},
		{"one expect missing", []consent.Issue{before}, Missed, "no check=consent/after-reject tracker=meta-pixel level=error; found: consent/before-choice: Meta Pixel fired before"},
		{"forbid matched", []consent.Issue{before, after, ga4}, Missed, "caught for the wrong reason: check=consent/before-choice tracker=ga4"},
		{"nothing", nil, Missed, "found: none"},
		{"wrong level", []consent.Issue{before, issue("consent/after-reject", "meta-pixel", model.Warning, "x")}, Missed, "after-reject"},
	} {
		t.Run(c.name, func(t *testing.T) {
			v := judgeBad(c02(), Observed{Env: fakeEnvelope(true, c.issues)})
			if v.Outcome != c.want || !strings.Contains(v.Detail, c.detail) {
				t.Errorf("verdict = %+v, want %s with %q", v, c.want, c.detail)
			}
		})
	}
}

func TestJudgeTitleIsACaseInsensitiveSubstring(t *testing.T) {
	s := scenarios.Scenario{ID: "c08", Expect: []scenarios.Match{
		{Class: "broke", Check: "consent/before-choice", Tracker: "tiktok", Title: "TikTok Pixel now fires before the choice"}}}
	change := func(class, key, title string) func(*consent.Result) {
		return func(r *consent.Result) {
			r.Comparison = &consent.Comparison{Against: "good", Changes: []consent.Change{{Class: class, Key: key, Title: title}}}
		}
	}
	key := consent.IssueKey("consent/before-choice", "/", consent.RunNone, "tiktok")
	if v := judgeBad(s, Observed{Env: fakeEnvelope(true, nil, change("broke", key, "BROKE: tiktok pixel NOW fires before the choice on /"))}); v.Outcome != Caught {
		t.Errorf("a matching change: %+v", v)
	}
	// A reworded title is a miss; the miss shows the actual title
	// next to the expected one.
	v := judgeBad(s, Observed{Env: fakeEnvelope(true, nil, change("broke", key, "TikTok sends before consent"))})
	if v.Outcome != Missed || !strings.Contains(v.Detail, `title~"TikTok Pixel now fires before the choice"`) || !strings.Contains(v.Detail, "broke: TikTok sends before consent") {
		t.Errorf("a reworded title: %+v", v)
	}
	// The class and the key's tracker must match too; an accepted change
	// doesn't count.
	for _, mod := range []func(*consent.Result){
		change("new", key, "TikTok Pixel now fires before the choice"),
		change("broke", consent.IssueKey("consent/before-choice", "/", consent.RunNone, "meta-pixel"), "TikTok Pixel now fires before the choice"),
		func(r *consent.Result) {
			change("broke", key, "TikTok Pixel now fires before the choice")(r)
			r.Comparison.Changes[0].Accepted = true
		},
	} {
		if v := judgeBad(s, Observed{Env: fakeEnvelope(true, nil, mod)}); v.Outcome != Missed {
			t.Errorf("a change that doesn't match: %+v", v)
		}
	}
}

func TestJudgeRunOutcomesAndAny(t *testing.T) {
	s := scenarios.Scenario{ID: "c21", Any: true, Expect: []scenarios.Match{
		{Run: "reject", Reason: "state_unchanged"},
		{Check: "consent/after-reject", Level: "error"}}}
	inconclusive := func(r *consent.Result) {
		r.Inconclusive = []consent.Inconclusive{{Page: "/", Run: consent.RunReject, Reason: consent.ReasonStateUnchanged}}
	}
	if v := judgeBad(s, Observed{Env: fakeEnvelope(false, nil, inconclusive)}); v.Outcome != Caught || !strings.Contains(v.Found, "reason=state_unchanged") {
		t.Errorf("any with the inconclusive reason: %+v", v)
	}
	runReason := func(r *consent.Result) { r.Pages[0].Runs[2].Reason = consent.ReasonStateUnchanged }
	if v := judgeBad(s, Observed{Env: fakeEnvelope(false, nil, runReason)}); v.Outcome != Caught {
		t.Errorf("any with the run's reason: %+v", v)
	}
	if v := judgeBad(s, Observed{Env: fakeEnvelope(true, nil)}); v.Outcome != Missed {
		t.Errorf("any with nothing: %+v", v)
	}
	status := scenarios.Scenario{ID: "c10", Expect: []scenarios.Match{{Run: "reject", Status: "ok"}}}
	if v := judgeBad(status, Observed{Env: fakeEnvelope(true, nil)}); v.Outcome != Caught {
		t.Errorf("run status ok: %+v", v)
	}
	failed := func(r *consent.Result) { r.Pages[0].Runs[2].Status = consent.StatusClickFailed }
	if v := judgeBad(status, Observed{Env: fakeEnvelope(true, nil, failed)}); v.Outcome != Missed {
		t.Errorf("run status click-failed for ok: %+v", v)
	}
}

func TestJudgeNeedsAReportAnEmptySinkAndLoadedStandIns(t *testing.T) {
	good := fakeEnvelope(true, []consent.Issue{
		issue("consent/before-choice", "meta-pixel", model.Error, "x"), issue("consent/after-reject", "meta-pixel", model.Error, "y")})
	for _, c := range []struct {
		name string
		o    Observed
		want string
		says string
	}{
		{"no report", Observed{Exit: 3, Output: "rhythm echo: no Chrome"}, Failed, "no JSON report (exit 3): rhythm echo: no Chrome"},
		{"a leak", Observed{Env: good, Sink: server.Count{Hits: 2, By: map[string]int{"www.facebook.com/tr": 2}}}, Failed, "2 tracker hits reached the sink in held mode: www.facebook.com/tr 2"},
		{"a forbidden page", Observed{Env: good, Sink: server.Count{Forbidden: 1}}, Failed, "/logout/ or /cart/"},
		{"a stand-in that didn't load", Observed{Env: good, Missing: []string{"fbevents"}}, NotLoaded, "not in the run's scripts: fbevents"},
		{"all well", Observed{Env: good}, Caught, ""},
	} {
		t.Run(c.name, func(t *testing.T) {
			v := judgeBad(c02(), c.o)
			if v.Outcome != c.want || !strings.Contains(v.Detail, c.says) {
				t.Errorf("verdict = %+v, want %s with %q", v, c.want, c.says)
			}
		})
	}
	// A scenario that sends hits on purpose may leave them at the sink.
	off := false
	s := c02()
	s.SinkZero = &off
	if v := judgeBad(s, Observed{Env: good, Sink: server.Count{Hits: 5}}); v.Outcome != Caught {
		t.Errorf("sink_zero false: %+v", v)
	}
}

func TestJudgeGood(t *testing.T) {
	g02 := scenarios.Scenario{ID: "g02", Allow: []string{"consent/cookieless-ping"}}
	ping := issue("consent/cookieless-ping", "ga4", model.Notice, "GA4 sent cookieless pings")
	if v := judgeGood(g02, Observed{Env: fakeEnvelope(true, []consent.Issue{ping})}); v.Outcome != Clean {
		t.Errorf("an allowed notice: %+v", v)
	}
	g01 := scenarios.Scenario{ID: "g01", Allow: []string{}}
	// A notice outside allow isn't an error or warning: not a false alarm.
	if v := judgeGood(g01, Observed{Env: fakeEnvelope(true, []consent.Issue{ping})}); v.Outcome != Clean {
		t.Errorf("a notice: %+v", v)
	}
	warn := issue("consent/default-late", "", model.Warning, "The Consent Mode default came late")
	if v := judgeGood(g01, Observed{Env: fakeEnvelope(true, []consent.Issue{warn})}); v.Outcome != FalseAlarm || !strings.Contains(v.Detail, "warning consent/default-late") {
		t.Errorf("a warning: %+v", v)
	}
	ignored := warn
	ignored.Ignored = &consent.Ignore{Check: warn.Check, Reason: "known", Until: "2099-01-01"}
	if v := judgeGood(g01, Observed{Env: fakeEnvelope(true, []consent.Issue{ignored})}); v.Outcome != Clean {
		t.Errorf("an ignored warning: %+v", v)
	}
	noBanner := func(r *consent.Result) {
		r.Inconclusive = []consent.Inconclusive{{Run: consent.RunAccept, Reason: consent.ReasonNoBanner}}
	}
	if v := judgeGood(g01, Observed{Env: fakeEnvelope(false, nil, noBanner)}); v.Outcome != Failed || !strings.Contains(v.Detail, "accept no_banner") {
		t.Errorf("an inconclusive good state: %+v", v)
	}
	if v := judgeGood(g01, Observed{Env: fakeEnvelope(true, nil), Sink: server.Count{Hits: 1}}); v.Outcome != Failed {
		t.Errorf("a leak in a good state: %+v", v)
	}
}

func TestEnvelopeKind(t *testing.T) {
	if _, err := parseEnvelope([]byte(`{"kind":"run","data":{}}`)); err == nil || !strings.Contains(err.Error(), `kind "run"`) {
		t.Errorf("a run envelope: %v", err)
	}
	if _, err := parseEnvelope([]byte(`{}`)); err == nil {
		t.Error("no kind: no error")
	}
	if _, err := parseEnvelope([]byte(`{"kind":`)); err == nil {
		t.Error("broken JSON: no error")
	}
	if e, err := parseEnvelope([]byte(`{"kind":"consent","complete":true,"verdict":"ok","data":{"pages":[]}}`)); err != nil || !e.Complete {
		t.Errorf("a consent envelope: %v %+v", err, e)
	}
}

func TestPercentile(t *testing.T) {
	s := func(n ...int) []time.Duration {
		var out []time.Duration
		for _, x := range n {
			out = append(out, time.Duration(x)*time.Second)
		}
		return out
	}
	for _, c := range []struct {
		xs   []time.Duration
		p    float64
		want time.Duration
	}{
		{nil, 50, 0},
		{s(7), 95, 7 * time.Second},
		{s(5, 1, 3, 2, 4), 50, 3 * time.Second},
		{s(1, 2, 3, 4), 50, 2 * time.Second},
		{s(1, 2, 3, 4, 5, 6, 7, 8, 9, 10, 11, 12, 13, 14, 15, 16, 17, 18, 19, 20), 95, 19 * time.Second},
		{s(1, 2, 3, 4, 5, 6, 7, 8, 9, 10, 11, 12, 13, 14, 15, 16, 17, 18, 19, 20), 100, 20 * time.Second},
	} {
		if got := percentile(c.xs, c.p); got != c.want {
			t.Errorf("percentile(%v, %v) = %v, want %v", c.xs, c.p, got, c.want)
		}
	}
	if got := maxDuration(s(3, 9, 2)); got != 9*time.Second {
		t.Errorf("max = %v", got)
	}
}

func TestControlFlakes(t *testing.T) {
	var c Control
	first := ""
	c.add(1, Verdict{Outcome: Clean}, "ok []", &first)
	c.add(2, Verdict{Outcome: Clean}, "ok []", &first)
	c.add(3, Verdict{Outcome: FalseAlarm, Detail: "warning x"}, "findings [x]", &first)
	c.add(4, Verdict{Outcome: Failed, Detail: "inconclusive"}, "error []", &first)
	c.Rounds = 4
	if c.FalseAlarms != 1 || c.Failed != 1 || c.Flakes != 2 {
		t.Errorf("control = %+v", c)
	}
	if !strings.Contains(strings.Join(c.Details, "\n"), "round 3 differs from round 1: ok [], then findings [x]") {
		t.Errorf("details = %q", c.Details)
	}
	sig := signature(Observed{Env: fakeEnvelope(true, []consent.Issue{issue("b", "", model.Notice, ""), issue("a", "", model.Notice, "")})})
	if sig != "findings [a|/|none|, b|/|none|]" {
		t.Errorf("signature = %q", sig)
	}
	if sig := signature(Observed{Exit: 3}); sig != "no report, exit 3" {
		t.Errorf("signature of nothing = %q", sig)
	}
}

func TestStateArgs(t *testing.T) {
	c08 := scenarios.Scenario{ID: "c08", Args: []string{"--against", "good"}}
	if got, _ := stateArgs(c08, false, ""); strings.Join(got, " ") != "--save good" {
		t.Errorf("c08 good = %q", got)
	}
	if got, _ := stateArgs(c08, true, ""); strings.Join(got, " ") != "--against good" {
		t.Errorf("c08 broken = %q", got)
	}
	c18 := scenarios.Scenario{ID: "c18", Args: []string{"--echo", "{echo}"}, Runs: []string{"+gpc", "+withdraw"}}
	if _, ok := stateArgs(c18, true, ""); ok {
		t.Error("c18 without an Echo: ok")
	}
	if got, ok := stateArgs(c18, true, "http://127.0.0.1:1"); !ok || strings.Join(got, " ") != "--echo http://127.0.0.1:1 --runs +gpc,+withdraw" {
		t.Errorf("c18 = %q", got)
	}
}
