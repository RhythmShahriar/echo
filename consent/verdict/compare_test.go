// Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
// SPDX-License-Identifier: MIT

package verdict

import (
	"encoding/json"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/RhythmShahriar/echo/consent"
	"github.com/RhythmShahriar/echo/consent/consenttest"
	"github.com/RhythmShahriar/echo/model"
)

// compared judges both compare goldens again and compares them.
func compared(t *testing.T, accept ...string) (*consent.Result, *consent.Result, *consent.Comparison) {
	t.Helper()
	base, cur := judged(t, "compare-base", "", ""), judged(t, "compare-cur", "", "")
	return base, cur, Comparer{}.Compare(base, cur, accept)
}

// changeLines writes changes as "class key title" lines, for diffs that
// read.
func changeLines(cs []consent.Change) []string {
	var out []string
	for _, c := range cs {
		line := c.Class + " " + c.Key + " " + c.Title
		if c.Accepted {
			line += " (accepted)"
		}
		out = append(out, line)
	}
	return out
}

func TestComparerRegistered(t *testing.T) {
	if _, ok := consent.Linked().Comparer.(Comparer); !ok {
		t.Fatalf("Comparer not registered: %T", consent.Linked().Comparer)
	}
}

func TestCompareTikTok(t *testing.T) {
	base, _, got := compared(t)
	want := consenttest.GoldenResult("compare-cur").Comparison
	if !slices.Equal(changeLines(got.Changes), changeLines(want.Changes)) {
		t.Errorf("changes\n got %q\nwant %q", changeLines(got.Changes), changeLines(want.Changes))
	}
	for i := range min(len(got.Changes), len(want.Changes)) {
		if got.Changes[i] != want.Changes[i] {
			t.Errorf("change %d\n got %+v\nwant %+v", i, got.Changes[i], want.Changes[i])
		}
	}
	if got.BaseID != base.ID || !got.BaseAt.Equal(base.Started) {
		t.Errorf("base %s %s", got.BaseID, got.BaseAt)
	}
	r := judged(t, "compare-cur", "", "")
	r.Comparison = got
	if s := r.Summary(); s.Broke != 2 || s.New != 1 || s.Changed != 0 || s.Fixed != 0 {
		t.Errorf("summary %+v", s)
	}
	if r.ExitCode("broke") != 1 || r.ExitCode("none") != 0 {
		t.Errorf("exit %d at broke", r.ExitCode("broke"))
	}
}

func TestCompareSame(t *testing.T) {
	base := judged(t, "compare-base", "", "")
	got := Comparer{}.Compare(base, judged(t, "compare-base", "", ""), nil)
	if got == nil || got.Changes == nil || len(got.Changes) != 0 {
		t.Errorf("same result: %+v", got)
	}
	if (Comparer{}).Compare(base, nil, nil) != nil {
		t.Error("nil cur gives a comparison")
	}
	if c := (Comparer{}).Compare(nil, judged(t, "clean", "", ""), nil); c == nil || len(c.Changes) != 0 {
		t.Errorf("nil base against a clean result: %+v", c)
	}
}

func TestCompareFixed(t *testing.T) {
	// Swapped: the baseline has TikTok, the current result doesn't.
	cur, base := judged(t, "compare-base", "", ""), judged(t, "compare-cur", "", "")
	got := Comparer{}.Compare(base, cur, nil)
	want := []string{
		"fixed consent/before-choice|/|none|tiktok-pixel TikTok Pixel no longer fires before the choice",
		"fixed consent/before-choice|/|reject|tiktok-pixel TikTok Pixel no longer fires before the choice",
	}
	if !slices.Equal(changeLines(got.Changes), want) {
		t.Errorf("changes\n got %q\nwant %q", changeLines(got.Changes), want)
	}
	if got.Changes[0].Before != "error" || got.Changes[0].After != "" {
		t.Errorf("before %q after %q", got.Changes[0].Before, got.Changes[0].After)
	}
	r := cur
	r.Comparison = got
	if s := r.Summary(); s.Fixed != 2 || r.ExitCode("broke") != 0 {
		t.Errorf("summary %+v exit %d", s, r.ExitCode("broke"))
	}
	// A run the current result didn't make isn't fixed: it wasn't looked at.
	cur.Pages[0].Runs = slices.DeleteFunc(cur.Pages[0].Runs, func(r consent.Run) bool { return r.Kind == consent.RunReject })
	got = Comparer{}.Compare(base, cur, nil)
	if lines := changeLines(got.Changes); len(lines) != 1 || !strings.Contains(lines[0], "|none|") {
		t.Errorf("without the reject run: %q", lines)
	}
}

func TestCompareChangedDefault(t *testing.T) {
	base, cur := judged(t, "compare-base", "", ""), judged(t, "compare-base", "", "")
	for i := range cur.Pages[0].Runs {
		run := &cur.Pages[0].Runs[i]
		for j := range run.Signals {
			if s := &run.Signals[j]; s.Command == "default" && run.Kind == consent.RunNone {
				s.State = map[string]consent.Value{"ad_storage": consent.Denied, "analytics_storage": consent.Granted}
			}
		}
	}
	got := Comparer{}.Compare(base, cur, nil)
	if len(got.Changes) != 1 {
		t.Fatalf("changes %q", changeLines(got.Changes))
	}
	c := got.Changes[0]
	want := consent.Change{Class: consent.ClassChanged, Key: "consent-mode-default|/|none|", Page: "/", Run: consent.RunNone,
		Title:  "The Consent Mode default changed",
		Before: "ad_personalization=denied ad_storage=denied ad_user_data=denied analytics_storage=denied",
		After:  "ad_storage=denied analytics_storage=granted"}
	if c != want {
		t.Errorf("change\n got %+v\nwant %+v", c, want)
	}
}

func TestCompareChangedSignals(t *testing.T) {
	base := judged(t, "compare-base", "", "")
	cur := consenttest.CloneResult(base)
	pg := &cur.Pages[0]
	pg.CMP.ID, pg.CMP.Name = "cookiebot", "Cookiebot"
	accept := runOf(pg, consent.RunAccept)
	for i := range accept.Hits {
		if h := &accept.Hits[i]; googleHit(h) && h.Google != nil {
			h.Google.GCD = "13p3p3p3p5"
		}
	}
	runOf(pg, consent.RunReject).Choice.Layer = 2
	tcf := func(p ...int) consent.Signal {
		return consent.Signal{Source: "tcf", Command: "useractioncomplete", TCF: &consent.TCF{Version: 2, PurposesConsent: p, Valid: true}}
	}
	runOf(&base.Pages[0], consent.RunReject).Signals = append(runOf(&base.Pages[0], consent.RunReject).Signals, tcf())
	runOf(pg, consent.RunReject).Signals = append(runOf(pg, consent.RunReject).Signals, tcf(3, 1))
	got := Comparer{}.Compare(base, cur, nil)
	want := []string{
		"changed cmp|/|| The CMP changed from Complianz to Cookiebot",
		"changed gcd|/|accept| gcd on the first Google hit changed",
		"changed reject-layer|/|reject| Reject moved from layer 1 to layer 2",
		"changed tcf|/|reject| The TCF purposes with consent changed",
	}
	if !slices.Equal(changeLines(got.Changes), want) {
		t.Errorf("changes\n got %q\nwant %q", changeLines(got.Changes), want)
	}
	for _, c := range got.Changes {
		switch c.Key {
		case "tcf|/|reject|":
			if c.Before != "none" || c.After != "1, 3" {
				t.Errorf("tcf %q -> %q", c.Before, c.After)
			}
		case "gcd|/|accept|":
			if c.Before != "13r3r3r3r5" || c.After != "13p3p3p3p5" {
				t.Errorf("gcd %q -> %q", c.Before, c.After)
			}
		}
	}
}

func TestCompareNewCMPAndPageMissing(t *testing.T) {
	base := judged(t, "compare-base", "", "")
	cur := consenttest.CloneResult(base)
	base.Pages[0].CMP.ID = ""
	extra := consenttest.CloneResult(base).Pages[0]
	extra.URL = "https://c08.consent.test/shop/"
	base.Pages = append(base.Pages, extra)
	got := Comparer{}.Compare(base, cur, nil)
	want := []string{
		"new cmp|/||complianz Complianz banner is new on the page",
		"changed page|/shop/|| page not checked",
	}
	if !slices.Equal(changeLines(got.Changes), want) {
		t.Errorf("changes\n got %q\nwant %q", changeLines(got.Changes), want)
	}
	// A CMP that is gone is a change.
	base.Pages[0].CMP.ID, cur.Pages[0].CMP.ID = "complianz", ""
	got = Comparer{}.Compare(base, cur, nil)
	if lines := changeLines(got.Changes); !slices.Contains(lines, "changed cmp|/|| The CMP Complianz is no longer found") {
		t.Errorf("CMP gone: %q", lines)
	}
}

func TestCompareNewWarningAndIgnored(t *testing.T) {
	base := judged(t, "compare-base", "", "")
	cur := consenttest.CloneResult(base)
	cur.Issues = append(cur.Issues,
		consent.Issue{Check: consent.CheckEmbed, Level: model.Warning, Key: "consent/embed|/|none|youtube", Page: "/", Run: consent.RunNone,
			Tracker: "youtube", Title: "YouTube loaded before the visitor chose"},
		consent.Issue{Check: consent.CheckAfterReject, Level: model.Error, Key: "consent/after-reject|/|reject|hotjar", Page: "/", Run: consent.RunReject,
			Tracker: "hotjar", Title: "Hotjar fired after Reject", Ignored: &consent.Ignore{Check: consent.CheckAfterReject}})
	got := Comparer{}.Compare(base, cur, nil)
	if want := []string{"new consent/embed|/|none|youtube YouTube loaded before the visitor chose"}; !slices.Equal(changeLines(got.Changes), want) {
		t.Errorf("changes %q", changeLines(got.Changes))
	}
	if got.Changes[0].After != "warning" {
		t.Errorf("after %q", got.Changes[0].After)
	}
}

func TestBrokeTitles(t *testing.T) {
	c := &comparison{jc: newJudge(&consent.Result{}, nil), jb: newJudge(&consent.Result{}, nil)}
	for check, want := range map[string]string{
		consent.CheckBeforeChoice:   "tiktok-pixel now fires before the choice",
		consent.CheckAfterReject:    "tiktok-pixel now fires after Reject",
		consent.CheckRejectGranted:  "Reject is now reported to Google as granted",
		consent.CheckRejectNotKept:  "Reject is no longer kept",
		consent.CheckDefaultLate:    "Now: the title",
		consent.CheckAfterWithdraw:  "tiktok-pixel now fires after consent was withdrawn",
		consent.CheckRejectBroken:   "The Reject button no longer works",
		consent.CheckNoDefault:      "Google tags now run with no Consent Mode default",
		consent.CheckScriptBefore:   "tiktok-pixel script now loads before the choice",
		consent.CheckServerSide:     "tiktok-pixel now receives events from the server after Reject",
		consent.CheckCookielessPing: "Now: the title",
	} {
		if got := c.brokeTitle(&consent.Issue{Check: check, Tracker: "tiktok-pixel", Title: "the title"}); got != want {
			t.Errorf("%s: %q, want %q", check, got, want)
		}
	}
	if got := c.fixedTitle(&consent.Issue{Check: consent.CheckV2Signals, Title: "the title"}); got != "Fixed: the title" {
		t.Errorf("fixed: %q", got)
	}
}

func TestAcceptKey(t *testing.T) {
	_, cur, got := compared(t, "tracker|/|accept|tiktok-pixel", " consent/before-choice|/|none|tiktok-pixel ")
	var accepted []string
	for _, c := range got.Changes {
		if c.Accepted {
			accepted = append(accepted, c.Key)
		}
	}
	if want := []string{"consent/before-choice|/|none|tiktok-pixel", "tracker|/|accept|tiktok-pixel"}; !slices.Equal(accepted, want) {
		t.Errorf("accepted %q", accepted)
	}
	cur.Comparison = got
	if s := cur.Summary(); s.Broke != 1 || s.New != 0 {
		t.Errorf("summary %+v", s)
	}
	// Every change accepted: nothing fails at broke or new.
	_, cur, got = compared(t, "tracker|/|accept|tiktok-pixel", "consent/before-choice|/|none|tiktok-pixel", "consent/before-choice|/|reject|tiktok-pixel")
	cur.Comparison = got
	if cur.ExitCode("new") != 0 {
		t.Errorf("all accepted: exit %d", cur.ExitCode("new"))
	}
}

func TestBaseline120(t *testing.T) {
	data, err := os.ReadFile(filepath.Join("testdata", "runs", "1.2.0", "consent.json"))
	if err != nil {
		t.Fatal(err)
	}
	base, err := ReadBaseline(data)
	if err != nil {
		t.Fatal(err)
	}
	if base.Echo != "1.2.0" || len(base.Pages) != 1 {
		t.Fatalf("read %s with %d pages", base.Echo, len(base.Pages))
	}
	got := Comparer{}.Compare(base, judged(t, "compare-cur", "", ""), nil)
	if want := consenttest.GoldenResult("compare-cur").Comparison; !slices.Equal(changeLines(got.Changes), changeLines(want.Changes)) {
		t.Errorf("changes %q", changeLines(got.Changes))
	}
}

func TestReadBaseline(t *testing.T) {
	data, err := os.ReadFile(filepath.Join("testdata", "runs", "1.2.0", "consent.json"))
	if err != nil {
		t.Fatal(err)
	}
	// A later 1.x adds fields: they are ignored.
	var doc map[string]any
	if err := json.Unmarshal(data, &doc); err != nil {
		t.Fatal(err)
	}
	doc["echo"], doc["fingerprints"] = "1.4.2", []string{"canvas"}
	later, _ := json.Marshal(doc)
	if r, err := ReadBaseline(later); err != nil || r.Echo != "1.4.2" {
		t.Errorf("1.4.2: %v", err)
	}
	// The --json envelope around a result reads too.
	env, _ := json.Marshal(map[string]any{"schema": "rhythm-echo/v1", "kind": "consent", "version": "1.2.0", "data": json.RawMessage(data)})
	if r, err := ReadBaseline(env); err != nil || r.ID != "2026-10-19T08-02-00-bbbbbb" {
		t.Errorf("envelope: %v", err)
	}
	for name, c := range map[string]struct {
		data []byte
		want string
	}{
		"newer major": {func() []byte { doc["echo"] = "2.0.1"; b, _ := json.Marshal(doc); return b }(),
			"--against: that result was written by a newer Echo (2.0.1); update Echo"},
		"newer schema": {[]byte(`{"schema":"rhythm-echo/v2","kind":"consent","version":"2.0.0","data":{}}`),
			"--against: that result was written by a newer Echo (2.0.0); update Echo"},
		"run envelope": {[]byte(`{"schema":"rhythm-echo/v1","kind":"run","version":"1.1.0","data":{"actions":[]}}`), ErrNotConsent.Error()},
		"not json":     {[]byte("<html>"), ErrNotConsent.Error()},
		"other json":   {[]byte(`{"name":"x"}`), ErrNotConsent.Error()},
		"bad types":    {[]byte(`{"echo":"1.2.0","pages":"x"}`), "--against: the result doesn't read as a consent result: "},
	} {
		_, err := ReadBaseline(c.data)
		if err == nil || !strings.HasPrefix(err.Error(), c.want) {
			t.Errorf("%s: %v, want %q", name, err, c.want)
		}
	}
	// A dev build's result has no major to compare; it reads.
	doc["echo"] = "dev"
	dev, _ := json.Marshal(doc)
	if _, err := ReadBaseline(dev); err != nil {
		t.Errorf("dev: %v", err)
	}
}

func FuzzConsentJSON(f *testing.F) {
	data, err := os.ReadFile(filepath.Join("testdata", "runs", "1.2.0", "consent.json"))
	if err != nil {
		f.Fatal(err)
	}
	f.Add(data)
	f.Add([]byte(`{"pages":[{"url":"https://x/","runs":[{"run":"reject","choice":{"layer":-1},"signals":[{"source":"tcf","tcf":{"valid":true}}]}]}],"issues":[{"level":"error"}]}`))
	f.Add([]byte(`{"pages":[{"url":"%zz","cmp":{"id":"a"},"runs":[{"run":"none","hits":[{"tracker":"ga4","consent":{}}]}]}]}`))
	f.Add([]byte(`null`))
	cur := consenttest.GoldenResult("compare-cur")
	f.Fuzz(func(t *testing.T, b []byte) {
		var base consent.Result
		if json.Unmarshal(b, &base) != nil {
			return
		}
		got := Comparer{}.Compare(&base, cur, []string{"x"})
		if got == nil {
			t.Fatal("nil comparison")
		}
		for _, c := range got.Changes {
			if c.Key == "" || classRank[c.Class] < 0 || c.Title == "" {
				t.Fatalf("bad change %+v", c)
			}
		}
		// Comparing the other way round must not panic either.
		_ = Comparer{}.Compare(cur, &base, nil)
		_, _ = ReadBaseline(b)
	})
}
