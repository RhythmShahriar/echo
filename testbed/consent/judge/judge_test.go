// Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
// SPDX-License-Identifier: MIT

package judge

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/RhythmShahriar/echo/consent"
	"github.com/RhythmShahriar/echo/model"
	"github.com/RhythmShahriar/echo/report"
	"github.com/RhythmShahriar/echo/testbed/consent/scenarios"
	"github.com/RhythmShahriar/echo/testbed/consent/server"
)

// allScripts lists a script at every stand-in's address.
func allScripts() []consent.Script {
	var out []consent.Script
	for _, s := range StandIns {
		out = append(out, consent.Script{Tracker: s.Name, URL: "https://" + s.Prefix + "x.js"})
	}
	return out
}

// fakeEnvelope is a consent envelope with issues and complete set, a
// default three-run page with every stand-in loaded.
func fakeEnvelope(complete bool, issues []consent.Issue, mods ...func(*consent.Result)) *Envelope {
	r := consent.Result{ID: "r1", DurationMS: 20000, Issues: issues,
		Pages: []consent.Page{{URL: "https://x.shop.test/", CMP: consent.CMPInfo{ID: "complianz", Found: true},
			Runs: []consent.Run{
				{Kind: consent.RunNone, Status: consent.StatusOK, Scripts: allScripts()},
				{Kind: consent.RunAccept, Status: consent.StatusOK, Choice: &consent.Choice{How: "button", Verified: true}},
				{Kind: consent.RunReject, Status: consent.StatusOK, Choice: &consent.Choice{How: "button", Verified: true}},
			}}}}
	for _, m := range mods {
		m(&r)
	}
	e := &Envelope{Schema: model.Schema, Kind: string(report.KindConsent), Complete: complete, Data: r, Verdict: "ok"}
	if len(issues) > 0 {
		e.Verdict = "findings"
	}
	return e
}

// issue is an issue of check on tracker at level with title.
func issue(check, tracker string, level model.Level, title string) consent.Issue {
	return consent.Issue{Check: check, Tracker: tracker, Level: level, Title: title, Run: consent.RunNone,
		Key: consent.IssueKey(check, "/", consent.RunNone, tracker)}
}

// testRoot is a private copy of the testbed's sites, so a test that
// switches a state never touches the real state folder.
func testRoot(t *testing.T) string {
	t.Helper()
	root := filepath.Join(t.TempDir(), "consent")
	if err := os.CopyFS(filepath.Join(root, "sites"), os.DirFS(filepath.Join("..", "sites"))); err != nil {
		t.Fatal(err)
	}
	return root
}
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
			v := Bad(c02(), Observed{Env: fakeEnvelope(true, c.issues)})
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
	if v := Bad(s, Observed{Env: fakeEnvelope(true, nil, change("broke", key, "BROKE: tiktok pixel NOW fires before the choice on /"))}); v.Outcome != Caught {
		t.Errorf("a matching change: %+v", v)
	}
	// A reworded title is a miss; the miss shows the actual title
	// next to the expected one.
	v := Bad(s, Observed{Env: fakeEnvelope(true, nil, change("broke", key, "TikTok sends before consent"))})
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
		if v := Bad(s, Observed{Env: fakeEnvelope(true, nil, mod)}); v.Outcome != Missed {
			t.Errorf("a change that doesn't match: %+v", v)
		}
	}
}

// TestJudgeFirstParty: c12 is caught only when a hit behind the GA4
// issue was recognized on the site's own host.
func TestJudgeFirstParty(t *testing.T) {
	yes := true
	s := scenarios.Scenario{ID: "c12", Expect: []scenarios.Match{
		{Check: "consent/before-choice", Tracker: "ga4", Level: "error", FirstParty: &yes}}}
	ga4 := issue("consent/before-choice", "ga4", model.Error, "GA4 fired before the visitor chose")
	hits := func(firstParty ...bool) func(*consent.Result) {
		return func(r *consent.Result) {
			for i, fp := range firstParty {
				r.Pages[0].Runs[0].Hits = append(r.Pages[0].Runs[0].Hits, consent.HitRef{Seq: int64(i + 1),
					Hit: consent.Hit{Tracker: "ga4", FirstParty: fp}})
			}
		}
	}
	withEvidence := ga4
	withEvidence.Evidence = []int64{2}
	for _, c := range []struct {
		name  string
		issue consent.Issue
		mod   func(*consent.Result)
		want  string
	}{
		{"first-party hit", ga4, hits(true), Caught},
		{"vendor host only", ga4, hits(false), Missed},
		{"no hits", ga4, hits(), Missed},
		{"evidence names the first-party hit", withEvidence, hits(false, true), Caught},
		{"evidence names a vendor hit", func() consent.Issue { i := withEvidence; i.Evidence = []int64{1}; return i }(), hits(false, true), Missed},
		{"first-party hit in another run", ga4, func(r *consent.Result) {
			r.Pages[0].Runs[2].Hits = []consent.HitRef{{Seq: 1, Hit: consent.Hit{Tracker: "ga4", FirstParty: true}}}
		}, Missed},
	} {
		t.Run(c.name, func(t *testing.T) {
			v := Bad(s, Observed{Env: fakeEnvelope(true, []consent.Issue{c.issue}, c.mod)})
			if v.Outcome != c.want {
				t.Errorf("verdict = %+v, want %s", v, c.want)
			}
		})
	}
	if got := s.Expect[0].String(); !strings.Contains(got, "first_party=true") {
		t.Errorf("String() = %q", got)
	}
}

func TestJudgeRunOutcomesAndAny(t *testing.T) {
	s := scenarios.Scenario{ID: "c21", Any: true, Expect: []scenarios.Match{
		{Run: "reject", Reason: "state_unchanged"},
		{Check: "consent/after-reject", Level: "error"}}}
	inconclusive := func(r *consent.Result) {
		r.Inconclusive = []consent.Inconclusive{{Page: "/", Run: consent.RunReject, Reason: consent.ReasonStateUnchanged}}
	}
	if v := Bad(s, Observed{Env: fakeEnvelope(false, nil, inconclusive)}); v.Outcome != Caught || !strings.Contains(v.Found, "reason=state_unchanged") {
		t.Errorf("any with the inconclusive reason: %+v", v)
	}
	runReason := func(r *consent.Result) { r.Pages[0].Runs[2].Reason = consent.ReasonStateUnchanged }
	if v := Bad(s, Observed{Env: fakeEnvelope(false, nil, runReason)}); v.Outcome != Caught {
		t.Errorf("any with the run's reason: %+v", v)
	}
	if v := Bad(s, Observed{Env: fakeEnvelope(true, nil)}); v.Outcome != Missed {
		t.Errorf("any with nothing: %+v", v)
	}
	status := scenarios.Scenario{ID: "c10", Expect: []scenarios.Match{{Run: "reject", Status: "ok"}}}
	if v := Bad(status, Observed{Env: fakeEnvelope(true, nil)}); v.Outcome != Caught {
		t.Errorf("run status ok: %+v", v)
	}
	failed := func(r *consent.Result) { r.Pages[0].Runs[2].Status = consent.StatusClickFailed }
	if v := Bad(status, Observed{Env: fakeEnvelope(true, nil, failed)}); v.Outcome != Missed {
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
			v := Bad(c02(), c.o)
			if v.Outcome != c.want || !strings.Contains(v.Detail, c.says) {
				t.Errorf("verdict = %+v, want %s with %q", v, c.want, c.says)
			}
		})
	}
	// A scenario that sends hits on purpose may leave them at the sink.
	off := false
	s := c02()
	s.SinkZero = &off
	if v := Bad(s, Observed{Env: good, Sink: server.Count{Hits: 5}}); v.Outcome != Caught {
		t.Errorf("sink_zero false: %+v", v)
	}
}

func TestJudgeGood(t *testing.T) {
	g02 := scenarios.Scenario{ID: "g02", Allow: []string{"consent/cookieless-ping"}}
	ping := issue("consent/cookieless-ping", "ga4", model.Notice, "GA4 sent cookieless pings")
	if v := Good(g02, Observed{Env: fakeEnvelope(true, []consent.Issue{ping})}); v.Outcome != Clean {
		t.Errorf("an allowed notice: %+v", v)
	}
	g01 := scenarios.Scenario{ID: "g01", Allow: []string{}}
	// A notice outside allow isn't an error or warning: not a false alarm.
	if v := Good(g01, Observed{Env: fakeEnvelope(true, []consent.Issue{ping})}); v.Outcome != Clean {
		t.Errorf("a notice: %+v", v)
	}
	warn := issue("consent/default-late", "", model.Warning, "The Consent Mode default came late")
	if v := Good(g01, Observed{Env: fakeEnvelope(true, []consent.Issue{warn})}); v.Outcome != FalseAlarm || !strings.Contains(v.Detail, "warning consent/default-late") {
		t.Errorf("a warning: %+v", v)
	}
	ignored := warn
	ignored.Ignored = &consent.Ignore{Check: warn.Check, Reason: "known", Until: "2099-01-01"}
	if v := Good(g01, Observed{Env: fakeEnvelope(true, []consent.Issue{ignored})}); v.Outcome != Clean {
		t.Errorf("an ignored warning: %+v", v)
	}
	noBanner := func(r *consent.Result) {
		r.Inconclusive = []consent.Inconclusive{{Run: consent.RunAccept, Reason: consent.ReasonNoBanner}}
	}
	if v := Good(g01, Observed{Env: fakeEnvelope(false, nil, noBanner)}); v.Outcome != Failed || !strings.Contains(v.Detail, "accept no_banner") {
		t.Errorf("an inconclusive good state: %+v", v)
	}
	if v := Good(g01, Observed{Env: fakeEnvelope(true, nil), Sink: server.Count{Hits: 1}}); v.Outcome != Failed {
		t.Errorf("a leak in a good state: %+v", v)
	}
}

func TestEnvelopeKind(t *testing.T) {
	if _, err := ParseEnvelope([]byte(`{"kind":"run","data":{}}`)); err == nil || !strings.Contains(err.Error(), `kind "run"`) {
		t.Errorf("a run envelope: %v", err)
	}
	if _, err := ParseEnvelope([]byte(`{}`)); err == nil {
		t.Error("no kind: no error")
	}
	if _, err := ParseEnvelope([]byte(`{"kind":`)); err == nil {
		t.Error("broken JSON: no error")
	}
	if e, err := ParseEnvelope([]byte(`{"kind":"consent","complete":true,"verdict":"ok","data":{"pages":[]}}`)); err != nil || !e.Complete {
		t.Errorf("a consent envelope: %v %+v", err, e)
	}
}

func TestStandIns(t *testing.T) {
	page := `<script src="https://www.googletagmanager.com/gtag/js?id=G-1"></script>
<script>r.src=t+h._hjSettings.hjid; t='https://static.hotjar.com/c/hotjar-'</script>
<script src="https://consent.cookiebot.com/uc.js"></script>
<link href="https://fonts.googleapis.com/css?family=x"><a href="https://www.facebook.com/shop">x</a>
<script>s.src='https://connect.facebook.net/en_US/fbevents.js'; s2='https://connect.facebook.net/en_US/fbevents.js'</script>`
	if got := StandInsIn(page); !slices.Equal(got, []string{"fbevents", "gtag", "hotjar"}) {
		t.Errorf("StandInsIn = %v", got)
	}
	if got := StandInsIn(`<p>no tags</p><script src="https://plausible.io/api/event"></script>`); len(got) != 0 {
		t.Errorf("a hit endpoint isn't a stand-in: %v", got)
	}
	e := fakeEnvelope(true, nil, func(r *consent.Result) {
		r.Pages[0].Runs[0].Scripts = r.Pages[0].Runs[0].Scripts[:1]
		r.Pages[0].Runs[1].Hits = []consent.HitRef{{URL: "https://static.hotjar.com/c/hotjar-1000001.js?sv=6", Hit: consent.Hit{Kind: consent.HitScript}},
			{URL: "https://connect.facebook.net/en_US/fbevents.js", Hit: consent.Hit{Kind: consent.HitBeacon}}}
	})
	if got := LoadedStandIns(e); !slices.Equal(got, []string{"gtag", "hotjar"}) {
		t.Errorf("LoadedStandIns = %v", got)
	}
	if got := MissingStandIns([]string{"fbevents", "gtag", "hotjar"}, e); !slices.Equal(got, []string{"fbevents"}) {
		t.Errorf("missing = %v", got)
	}
	if got := MissingStandIns([]string{"gtag"}, nil); got != nil {
		t.Errorf("no report: %v", got)
	}
}

func TestPageStandInsFromTheTestbed(t *testing.T) {
	root := testRoot(t)
	srv, err := server.New(server.Options{Root: root})
	if err != nil {
		t.Fatal(err)
	}
	got, err := PageStandIns(srv.Handler(), "c01")
	if err != nil || !slices.Contains(got, "gtag") {
		t.Errorf("c01 = %v, %v", got, err)
	}
	good, _ := PageStandIns(srv.Handler(), "c08")
	if err := srv.SetBroken("c08", true); err != nil {
		t.Fatal(err)
	}
	broken, _ := PageStandIns(srv.Handler(), "c08")
	if !slices.Contains(good, "tiktok") || !slices.Contains(broken, "tiktok") {
		t.Errorf("c08 good %v, broken %v", good, broken)
	}
	if got, _ := PageStandIns(srv.Handler(), "g12"); len(got) != 0 {
		t.Errorf("g12 has no tags: %v", got)
	}
	if _, err := PageStandIns(srv.Handler(), "nope"); err == nil {
		t.Error("an unknown site: no error")
	}
}
