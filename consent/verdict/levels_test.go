// Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
// SPDX-License-Identifier: MIT

package verdict

import (
	"slices"
	"testing"
	"time"

	"github.com/RhythmShahriar/echo/consent"
	"github.com/RhythmShahriar/echo/consent/consenttest"
	"github.com/RhythmShahriar/echo/model"
)

// today is the date the ignore tests run on.
var today = time.Date(2026, 10, 26, 23, 30, 0, 0, time.UTC)

// judgedWith judges a golden result again under the default plan
// changed by set, on the date today.
func judgedWith(t *testing.T, name string, set func(p *consent.Plan)) *consent.Result {
	t.Helper()
	r := consenttest.GoldenResult(name)
	p := consenttest.NewPlan(t)
	if set != nil {
		set(p)
	}
	Evaluator{NowFunc: func() time.Time { return today }}.Evaluate(r, p)
	return r
}

func TestLevelOff(t *testing.T) {
	r := judgedWith(t, "meta-ignores", func(p *consent.Plan) {
		p.CheckLevels = map[string]model.Level{consent.CheckMetaNoConsentAPI: "off", consent.CheckAfterReject: "OFF"}
	})
	sameKeys(t, keysOf(r, nil), "consent/before-choice|/|none|meta-pixel")
	if s := r.Summary(); s.Errors != 1 || s.Notices != 0 {
		t.Errorf("summary %+v", s)
	}
}

func TestLevelRaise(t *testing.T) {
	r := judgedWith(t, "advanced", nil)
	if r.ExitCode("warning") != 0 || r.Verdict("warning") != "findings" {
		t.Fatalf("default: exit %d verdict %s", r.ExitCode("warning"), r.Verdict("warning"))
	}
	r = judgedWith(t, "advanced", func(p *consent.Plan) {
		p.CheckLevels = map[string]model.Level{consent.CheckCookielessPing: model.Warning}
	})
	if len(r.Issues) != 1 || r.Issues[0].Level != model.Warning {
		t.Fatalf("issues %+v", r.Issues)
	}
	if r.ExitCode("warning") != 1 || r.Verdict("warning") != "findings" || r.ExitCode("error") != 0 {
		t.Errorf("raised: exit %d at warning, %d at error", r.ExitCode("warning"), r.ExitCode("error"))
	}
	// A level for a check that raised nothing changes nothing.
	r = judgedWith(t, "clean", func(p *consent.Plan) {
		p.CheckLevels = map[string]model.Level{consent.CheckBeforeChoice: model.Notice}
	})
	if len(r.Issues) != 0 {
		t.Errorf("clean: %+v", r.Issues)
	}
}

func TestIgnoreMatches(t *testing.T) {
	ig := consent.Ignore{Check: consent.CheckAfterReject, Tracker: "meta-pixel", Reason: "fixed in the next release", Until: "2026-10-26"}
	r := judgedWith(t, "meta-ignores", func(p *consent.Plan) { p.Ignore = []consent.Ignore{ig} })
	want := []string{"consent/before-choice|/|none|meta-pixel", "consent/after-reject|/|reject|meta-pixel", "consent/meta-no-consent-api|/|none|meta-pixel"}
	sameKeys(t, keysOf(r, nil), want...)
	is := issue(t, r, "consent/after-reject|/|reject|meta-pixel")
	if is.Ignored == nil || *is.Ignored != ig {
		t.Errorf("after-reject ignored = %+v", is.Ignored)
	}
	if is := issue(t, r, "consent/before-choice|/|none|meta-pixel"); is.Ignored != nil {
		t.Errorf("before-choice ignored too")
	}
	if s := r.Summary(); s.Errors != 1 || s.Notices != 1 {
		t.Errorf("summary %+v", s)
	}
	for _, c := range []struct {
		name string
		ig   consent.Ignore
		hit  bool
	}{
		{"page and run", consent.Ignore{Check: consent.CheckAfterReject, Page: "/", Run: consent.RunReject, Until: "2027-01-01"}, true},
		{"page as a URL", consent.Ignore{Check: consent.CheckAfterReject, Page: "https://meta-ignores.consent.test/", Until: "2027-01-01"}, true},
		{"tracker case", consent.Ignore{Check: consent.CheckAfterReject, Tracker: "Meta-Pixel", Until: "2027-01-01"}, true},
		{"other tracker", consent.Ignore{Check: consent.CheckAfterReject, Tracker: "ga4", Until: "2027-01-01"}, false},
		{"other page", consent.Ignore{Check: consent.CheckAfterReject, Page: "/shop/", Until: "2027-01-01"}, false},
		{"other run", consent.Ignore{Check: consent.CheckAfterReject, Run: consent.RunNone, Until: "2027-01-01"}, false},
		{"other check", consent.Ignore{Check: consent.CheckEmbed, Tracker: "meta-pixel", Until: "2027-01-01"}, false},
	} {
		r := judgedWith(t, "meta-ignores", func(p *consent.Plan) { p.Ignore = []consent.Ignore{c.ig} })
		got := issue(t, r, "consent/after-reject|/|reject|meta-pixel").Ignored != nil
		if got != c.hit {
			t.Errorf("%s: ignored %v, want %v", c.name, got, c.hit)
		}
		if want := 2; c.hit && r.Summary().Errors != 1 || !c.hit && r.Summary().Errors != want {
			t.Errorf("%s: errors %d", c.name, r.Summary().Errors)
		}
	}
}

func TestIgnoreExpired(t *testing.T) {
	ig := consent.Ignore{Check: consent.CheckAfterReject, Tracker: "meta-pixel", Reason: "waiting on the agency", Until: "2026-10-25"}
	r := judgedWith(t, "meta-ignores", func(p *consent.Plan) { p.Ignore = []consent.Ignore{ig} })
	if is := issue(t, r, "consent/after-reject|/|reject|meta-pixel"); is.Ignored != nil {
		t.Error("an expired ignore still hides its issue")
	}
	if r.Summary().Errors != 2 || r.ExitCode("") != 1 {
		t.Errorf("summary %+v", r.Summary())
	}
	var expired []consent.Issue
	for _, is := range r.Issues {
		if is.Check == consent.CheckIgnoreExpired {
			expired = append(expired, is)
		}
	}
	if len(expired) != 1 {
		t.Fatalf("expired notices: %+v", expired)
	}
	e := expired[0]
	if want := "The ignore for consent/after-reject on meta-pixel expired on 2026-10-25; the issue counts again"; e.Title != want {
		t.Errorf("title %q", e.Title)
	}
	if want := "ignore: consent/after-reject until 2026-10-25, reason: waiting on the agency"; e.Detail != want {
		t.Errorf("detail %q", e.Detail)
	}
	if e.Level != model.Notice || e.Fix == "" || e.Key != consent.IssueKey(consent.CheckIgnoreExpired, "", "", "meta-pixel") {
		t.Errorf("notice %+v", e)
	}
	// The until day itself still applies; a page entry names the page.
	r = judgedWith(t, "meta-ignores", func(p *consent.Plan) {
		p.Ignore = []consent.Ignore{{Check: consent.CheckAfterReject, Until: "2026-10-26"}, {Check: consent.CheckBeforeChoice, Page: "/", Until: "2026-01-31"}}
	})
	var titles []string
	for _, is := range r.Issues {
		if is.Check == consent.CheckIgnoreExpired {
			titles = append(titles, is.Title)
		}
	}
	if want := []string{"The ignore for consent/before-choice on / expired on 2026-01-31; the issue counts again"}; !slices.Equal(titles, want) {
		t.Errorf("titles %q", titles)
	}
	if issue(t, r, "consent/after-reject|/|reject|meta-pixel").Ignored == nil {
		t.Error("an ignore until today no longer applies")
	}
	// An until that isn't a date never hides anything.
	r = judgedWith(t, "meta-ignores", func(p *consent.Plan) {
		p.Ignore = []consent.Ignore{{Check: consent.CheckAfterReject, Until: "next week"}}
	})
	if issue(t, r, "consent/after-reject|/|reject|meta-pixel").Ignored != nil {
		t.Error("an until that isn't a date hides an issue")
	}
}

func TestSortOrder(t *testing.T) {
	r := &consent.Result{Policy: consent.PolicyAdvanced, Held: true, TLSVerified: true, Sandbox: true, Pages: []consent.Page{
		{URL: "https://shop.test/"}, {URL: "https://shop.test/blog/"},
	}}
	mk := func(check string, lvl model.Level, page string, run consent.RunKind, tracker string) consent.Issue {
		return consent.Issue{Check: check, Level: lvl, Page: page, Run: run, Tracker: tracker, Key: consent.IssueKey(check, page, run, tracker)}
	}
	r.Issues = []consent.Issue{
		mk(consent.CheckCookielessPing, model.Notice, "/", consent.RunNone, "ga4"),
		mk(consent.CheckAfterReject, model.Error, "/blog/", consent.RunReject, "meta-pixel"),
		mk(consent.CheckEmbed, model.Warning, "/", consent.RunReject, "youtube"),
		mk(consent.CheckAfterWithdraw, model.Error, "/", consent.RunWithdraw, "meta-pixel"),
		mk(consent.CheckBeforeChoice, model.Error, "/", consent.RunNone, "tiktok-pixel"),
		mk(consent.CheckBeforeChoice, model.Error, "/", consent.RunNone, "meta-pixel"),
		mk(consent.CheckGPCIgnored, model.Warning, "/", consent.RunGPC, "meta-pixel"),
		mk(consent.CheckBeforeChoice, model.Error, "/", consent.RunAccept, "meta-pixel"),
	}
	j := newJudge(r, nil)
	j.sortIssues()
	want := []string{
		"consent/before-choice|/|none|meta-pixel",
		"consent/before-choice|/|none|tiktok-pixel",
		"consent/before-choice|/|accept|meta-pixel",
		"consent/after-withdraw|/|withdraw|meta-pixel",
		"consent/after-reject|/blog/|reject|meta-pixel",
		"consent/embed|/|reject|youtube",
		"consent/gpc-ignored|/|gpc|meta-pixel",
		"consent/cookieless-ping|/|none|ga4",
	}
	var got []string
	for _, is := range r.Issues {
		got = append(got, is.Key)
	}
	if !slices.Equal(got, want) {
		t.Errorf("order\n got %q\nwant %q", got, want)
	}
}

func TestNotesOrder(t *testing.T) {
	r := consenttest.GoldenResult("advanced")
	r.Held, r.TLSVerified, r.Sandbox, r.Mapped = false, false, false, true
	r.Notes = []string{"A note browse added.", "Hits held in the browser.", GCDNote}
	p := consenttest.NewPlan(t)
	p.Geo = "DE"
	Evaluator{}.Evaluate(r, p)
	want := []string{
		"Evidence, not legal advice.",
		"Tracker hits were sent for real (--send-hits).",
		"TLS not verified.",
		"Chrome ran without its sandbox.",
		"Hosts mapped with --map-host.",
		"--geo de set the language and time zone, not the IP address.",
		"gcd decoded with an unofficial, community-documented mapping.",
		"A note browse added.",
	}
	if !slices.Equal(r.Notes, want) {
		t.Errorf("notes\n got %q\nwant %q", r.Notes, want)
	}
	// Judged again, nothing repeats.
	Evaluator{}.Evaluate(r, p)
	if !slices.Equal(r.Notes, want) {
		t.Errorf("notes again\n got %q", r.Notes)
	}
	// The plain case: held, verified, sandboxed, nothing else.
	r = consenttest.GoldenResult("no-trackers")
	r.Mapped = false
	Evaluator{}.Evaluate(r, consenttest.NewPlan(t))
	if want := []string{"Evidence, not legal advice.", "Hits held in the browser."}; !slices.Equal(r.Notes, want) {
		t.Errorf("plain notes %q", r.Notes)
	}
}
