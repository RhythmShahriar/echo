// Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
// SPDX-License-Identifier: MIT

package consent_test

import (
	"testing"

	"github.com/RhythmShahriar/echo/consent"
	"github.com/RhythmShahriar/echo/consent/consenttest"
	"github.com/RhythmShahriar/echo/model"
)

func TestIssueKey(t *testing.T) {
	got := consent.IssueKey(consent.CheckBeforeChoice, "/", consent.RunNone, "meta-pixel")
	if want := "consent/before-choice|/|none|meta-pixel"; got != want {
		t.Fatalf("IssueKey = %q; want %q", got, want)
	}
	if got := consent.IssueKey(consent.CheckNoBanner, "/shop/", consent.RunNone, ""); got != "consent/no-banner|/shop/|none|" {
		t.Fatalf("IssueKey without tracker = %q", got)
	}
}

func TestRunInconclusive(t *testing.T) {
	if (consent.Run{Status: consent.StatusOK}).Inconclusive() {
		t.Error("an ok run is inconclusive")
	}
	if !(consent.Run{Status: consent.StatusClickFailed, Reason: consent.ReasonClickMissed}).Inconclusive() {
		t.Error("a click_missed run is conclusive")
	}
}

// Each golden result's summary, verdict and exit code, as PRD 9.3 and
// 10.1 have them.
func TestGoldenSummaries(t *testing.T) {
	for _, c := range []struct {
		name     string
		sum      consent.Summary
		verdict  string
		exit     int
		complete bool
	}{
		{"clean", consent.Summary{Pages: 1, Runs: 3, Trackers: 3, Held: 9}, "ok", 0, true},
		{"meta-ignores", consent.Summary{Errors: 2, Notices: 1, Pages: 1, Runs: 3, Trackers: 3, BeforeChoice: 1, AfterReject: 1, Held: 15}, "findings", 1, true},
		{"advanced", consent.Summary{Notices: 1, Pages: 1, Runs: 3, Trackers: 3, Held: 11}, "findings", 0, true},
		{"inconclusive", consent.Summary{Pages: 1, Runs: 3, FailedRuns: 0, InconclusiveRuns: 2}, "error", 3, false},
		{"no-trackers", consent.Summary{Pages: 1, Runs: 3}, "ok", 0, true},
		{"compare-cur", consent.Summary{Broke: 2, New: 1, Errors: 2, Pages: 1, Runs: 3, Trackers: 4, BeforeChoice: 1, Held: 14}, "findings", 1, true},
	} {
		r := consenttest.GoldenResult(c.name)
		if got := r.Summary(); got != c.sum {
			t.Errorf("%s: Summary = %+v; want %+v", c.name, got, c.sum)
		}
		if got := r.Verdict("error"); got != c.verdict {
			t.Errorf("%s: Verdict = %q; want %q", c.name, got, c.verdict)
		}
		if got := r.ExitCode(""); got != c.exit {
			t.Errorf("%s: ExitCode = %d; want %d", c.name, got, c.exit)
		}
		if r.Complete != c.complete {
			t.Errorf("%s: Complete = %v", c.name, r.Complete)
		}
	}
}

// PRD 10.1: 1 wins over 3, 3 over 0; --fail-on moves the line.
func TestExitCodePrecedence(t *testing.T) {
	inc := consenttest.GoldenResult("inconclusive")
	inc.Issues = append(inc.Issues, consent.Issue{Check: consent.CheckNoBanner, Level: model.Warning})
	for failOn, want := range map[string]int{"": 3, "error": 3, "warning": 1, "notice": 1, "none": 3} {
		if got := inc.ExitCode(failOn); got != want {
			t.Errorf("inconclusive with a warning, --fail-on %q: exit %d; want %d", failOn, got, want)
		}
	}
	if got := inc.Verdict("warning"); got != "findings" {
		t.Errorf("Verdict(warning) = %q; want findings", got)
	}
	if got := inc.Verdict("error"); got != "error" {
		t.Errorf("Verdict(error) = %q; want error", got)
	}

	adv := consenttest.GoldenResult("advanced") // one notice
	for failOn, want := range map[string]int{"error": 0, "warning": 0, "notice": 1, "none": 0} {
		if got := adv.ExitCode(failOn); got != want {
			t.Errorf("advanced, --fail-on %q: exit %d; want %d", failOn, got, want)
		}
	}

	// An ignored error doesn't count.
	mi := consenttest.GoldenResult("meta-ignores")
	for i := range mi.Issues {
		mi.Issues[i].Ignored = &consent.Ignore{Check: mi.Issues[i].Check, Reason: "known", Until: "2026-11-30"}
	}
	if got := mi.ExitCode("notice"); got != 0 {
		t.Errorf("all issues ignored: exit %d; want 0", got)
	}
	if got := mi.Verdict("error"); got != "ok" {
		t.Errorf("all issues ignored: verdict %q; want ok", got)
	}
}

// Comparing, the change classes decide: broke < new < changed; fixed
// never fails; an accepted change doesn't count.
func TestExitCodeComparison(t *testing.T) {
	cur := consenttest.GoldenResult("compare-cur")
	for failOn, want := range map[string]int{"": 1, "broke": 1, "new": 1, "changed": 1, "none": 0} {
		if got := cur.ExitCode(failOn); got != want {
			t.Errorf("--fail-on %q: exit %d; want %d", failOn, got, want)
		}
	}
	only := func(class string, accepted bool) *consent.Result {
		r := consenttest.GoldenResult("clean")
		r.Comparison = &consent.Comparison{Changes: []consent.Change{{Class: class, Accepted: accepted}}}
		return r
	}
	for _, c := range []struct {
		class, failOn string
		accepted      bool
		want          int
	}{
		{consent.ClassNew, "broke", false, 0},
		{consent.ClassNew, "new", false, 1},
		{consent.ClassChanged, "new", false, 0},
		{consent.ClassChanged, "changed", false, 1},
		{consent.ClassFixed, "changed", false, 0},
		{consent.ClassBroke, "broke", true, 0},
	} {
		if got := only(c.class, c.accepted).ExitCode(c.failOn); got != c.want {
			t.Errorf("%s (accepted %v), --fail-on %s: exit %d; want %d", c.class, c.accepted, c.failOn, got, c.want)
		}
	}
	if got := only(consent.ClassFixed, false).Summary().Fixed; got != 1 {
		t.Errorf("Fixed = %d", got)
	}
	if got := only(consent.ClassBroke, true).Summary().Broke; got != 0 {
		t.Errorf("an accepted broke change counted: %d", got)
	}
}

// A no-banner run on a page that needs no consent is complete; the same
// run on a page that does is not.
func TestNoBannerNeedsNone(t *testing.T) {
	r := consenttest.GoldenResult("no-trackers")
	if r.ExitCode("error") != 0 || r.Summary().InconclusiveRuns != 0 {
		t.Fatalf("no-trackers: exit %d, %+v", r.ExitCode("error"), r.Summary())
	}
	r.Pages[0].NeedsNone = false
	r.Pages[0].Runs[1].Reason = consent.ReasonNoBanner
	if r.ExitCode("error") != 3 || r.Summary().InconclusiveRuns != 1 {
		t.Fatalf("no_banner on a page that needs consent: exit %d, %+v", r.ExitCode("error"), r.Summary())
	}
}

// A truncated or canceled run leaves the result incomplete even with
// no reason set.
func TestTruncatedIsIncomplete(t *testing.T) {
	r := consenttest.GoldenResult("clean")
	r.Pages[0].Runs[0].Truncated = true
	if got := r.ExitCode("error"); got != 3 {
		t.Errorf("truncated: exit %d; want 3", got)
	}
	r = consenttest.GoldenResult("clean")
	r.Pages[0].Runs[2].Status = consent.StatusCanceled
	if got := r.Summary(); got.FailedRuns != 1 || got.InconclusiveRuns != 1 {
		t.Errorf("canceled: %+v", got)
	}
}
