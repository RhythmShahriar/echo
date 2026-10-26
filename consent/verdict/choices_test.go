// Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
// SPDX-License-Identifier: MIT

package verdict

import (
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/RhythmShahriar/echo/consent"
	"github.com/RhythmShahriar/echo/consent/consenttest"
	"github.com/RhythmShahriar/echo/model"
)

// v4Checks are the checks choices.go raises.
var v4Checks = []string{consent.CheckRejectNotKept, consent.CheckRejectBroken, consent.CheckNoFirstLayerReject, consent.CheckNoBanner,
	consent.CheckClickFallback, consent.CheckTCFMismatch, consent.CheckServerSide}

// rejected is a page whose Reject was clicked and confirmed at 2 s, with
// the stored state denying marketing and statistics; it visits /,
// /blog/ and the reload.
func rejected() *consent.Result {
	state := &consent.CMPState{CMP: "borlabs", Source: "cookie:borlabs-cookie", Decided: true,
		Categories: map[string]bool{"marketing": false, "statistics": false, "essential": true}}
	run := consent.Run{Kind: consent.RunReject,
		Choice: &consent.Choice{How: "button", Selector: "a._brlbs-refuse", Text: "Only essential", Layer: 1, Clicks: 1, AtMS: 2000, Verified: true, State: state},
		Visits: []consent.Visit{{URL: "https://shop.test/", Step: "load", Banner: true}, {URL: "https://shop.test/blog/", Step: "page", AtMS: 9000},
			{URL: "https://shop.test/", Step: "reload", AtMS: 12000}},
		FinalState: &consent.CMPState{CMP: "borlabs", Source: "cookie:borlabs-cookie", Decided: true,
			Categories: map[string]bool{"marketing": false, "statistics": false, "essential": true}},
	}
	r := result(run)
	r.Pages[0].CMP = consent.CMPInfo{ID: "borlabs", Name: "Borlabs Cookie", Found: true, Present: true, RejectLayer: 1}
	return r
}

func TestRejectNotKept(t *testing.T) { // c07
	r := rejected()
	r.Pages[0].Runs[0].Visits[1].Banner = true
	Evaluator{}.Evaluate(r, nil)
	sameKeys(t, keysOf(r, v4Checks), "consent/reject-not-kept|/|reject|")
	is := issue(t, r, "consent/reject-not-kept|/|reject|")
	if is.Level != model.Error || is.Title != "After Reject, the banner came back on /blog/" ||
		is.Detail != "the banner showed again on the next page (/blog/) at 9.00s" {
		t.Errorf("%+v", is)
	}

	r = rejected()
	r.Pages[0].Runs[0].FinalState.Decided = false
	Evaluator{}.Evaluate(r, nil)
	if is := issue(t, r, "consent/reject-not-kept|/|reject|"); is.Title != "After Reject, the CMP forgot the choice on /" ||
		is.Detail != "after the reload cookie:borlabs-cookie has no choice" {
		t.Errorf("%+v", is)
	}

	r = rejected()
	r.Pages[0].Runs[0].FinalState.Categories["marketing"] = true
	r.Pages[0].Runs[0].Visits[2].Banner = true
	Evaluator{}.Evaluate(r, nil)
	if is := issue(t, r, "consent/reject-not-kept|/|reject|"); is.Title != "After Reject, the banner came back on /" ||
		is.Detail != "the banner showed again on the reload (/) at 12.00s; after the reload cookie:borlabs-cookie grants marketing" {
		t.Errorf("%+v", is)
	}
}

func TestPagePathDropsQuery(t *testing.T) {
	r := rejected()
	r.Pages[0].URL = "https://shop.test/?utm_source=mail&token=abc"
	r.Pages[0].Runs[0].Visits[1].URL = "https://shop.test/blog/?ref=x"
	r.Pages[0].Runs[0].Visits[1].Banner = true
	Evaluator{}.Evaluate(r, nil)
	is := issue(t, r, "consent/reject-not-kept|/|reject|")
	if is.Page != "/" || is.Title != "After Reject, the banner came back on /blog/" || strings.Contains(is.Detail, "?") {
		t.Errorf("%+v", is)
	}
	for in, want := range map[string]string{
		"https://shop.test/a?b=1": "/a", "https://shop.test": "/", "https://shop.test/?": "/", "not a url": "not a url",
	} {
		if got := pagePath(in); got != want {
			t.Errorf("pagePath(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestRejectNotKeptNegative(t *testing.T) { // g09
	cases := map[string]func(r *consent.Result){
		"kept":                      func(*consent.Result) {},
		"no final state":            func(r *consent.Result) { r.Pages[0].Runs[0].FinalState = nil },
		"banner only before choice": func(r *consent.Result) { r.Pages[0].Runs[0].Visits[0].Banner = true },
		"choice never confirmed": func(r *consent.Result) {
			r.Pages[0].Runs[0].Choice.Verified = false
			r.Pages[0].Runs[0].Visits[1].Banner = true
		},
	}
	for name, change := range cases {
		t.Run(name, func(t *testing.T) {
			r := rejected()
			change(r)
			Evaluator{}.Evaluate(r, nil)
			if keys := keysOf(r, v4Checks); len(keys) != 0 {
				t.Errorf("raised %v", keys)
			}
		})
	}
	if keys := keysOf(judged(t, "clean", "", ""), nil); len(keys) != 0 {
		t.Errorf("clean: %v", keys)
	}
}

func TestRejectBroken(t *testing.T) {
	r := rejected()
	c := r.Pages[0].Runs[0].Choice
	c.How, c.Clicks, c.APIWorked, c.Fallback = "api", 3, true, true
	Evaluator{}.Evaluate(r, nil)
	sameKeys(t, keysOf(r, v4Checks), "consent/reject-broken|/|reject|", "consent/click-fallback|/|reject|")
	is := issue(t, r, "consent/reject-broken|/|reject|")
	if is.Level != model.Error || is.Title != "The Reject button doesn't work; only the CMP's API could reject" ||
		is.Detail != `clicked "Only essential" (a._brlbs-refuse) 3 times and the choice wasn't confirmed; Borlabs Cookie's API reject worked` {
		t.Errorf("%+v", is)
	}
}

func TestNoFirstLayerReject(t *testing.T) { // c10, g04
	r := rejected()
	r.Pages[0].Runs[0].Choice.Layer = 2
	Evaluator{}.Evaluate(r, nil)
	sameKeys(t, keysOf(r, v4Checks), "consent/no-first-layer-reject|/|reject|")
	is := issue(t, r, "consent/no-first-layer-reject|/|reject|")
	if is.Level != model.Warning || is.Title != "Reject isn't on the banner: Echo had to open settings" ||
		is.Detail != `Reject was on layer 2: Echo clicked "Only essential" (a._brlbs-refuse)` {
		t.Errorf("%+v", is)
	}
	// A selector chain given by the user still had to open settings.
	p := consenttest.NewPlan(t)
	p.RejectSelector = []consent.Locator{{CSS: "#settings"}, {CSS: "#reject"}}
	Evaluator{}.Evaluate(r, p)
	sameKeys(t, keysOf(r, v4Checks), "consent/no-first-layer-reject|/|reject|")
	// One user-given step is a first-layer button.
	p.RejectSelector = p.RejectSelector[1:]
	Evaluator{}.Evaluate(r, p)
	if keys := keysOf(r, v4Checks); len(keys) != 0 {
		t.Errorf("single --reject-selector: %v", keys)
	}
	r.Pages[0].Runs[0].Choice.Layer = 1
	Evaluator{}.Evaluate(r, nil)
	if keys := keysOf(r, v4Checks); len(keys) != 0 {
		t.Errorf("layer 1: %v", keys)
	}
}

func TestNoBanner(t *testing.T) { // c17
	r := result(consent.Run{Kind: consent.RunNone,
		Visits: []consent.Visit{{URL: "https://shop.test/", Step: "load"}},
		Hits: []consent.HitRef{
			ga4(3, 600, consent.PhaseBefore, "", nil),
			hit(4, 700, consent.PhaseBefore, "meta-pixel", 0, "https://www.facebook.com/tr?id=1&ev=PageView"),
		},
		Signals: []consent.Signal{{Source: "fbq", Command: "revoke"}},
	}, consent.Run{Kind: consent.RunReject, Status: consent.StatusNoBanner, Reason: consent.ReasonNoBanner})
	r.Pages[0].CMP = consent.CMPInfo{}
	p := consenttest.NewPlan(t)
	Evaluator{}.Evaluate(r, p)
	sameKeys(t, keysOf(r, []string{consent.CheckNoBanner}), "consent/no-banner|/|none|")
	is := issue(t, r, "consent/no-banner|/|none|")
	if is.Level != model.Warning || is.Title != "Google Analytics 4 and Meta Pixel are on the page but no consent banner showed" ||
		is.Detail != "Google Analytics 4 and Meta Pixel are on the page but no consent banner showed within 10s. Many banners show only to visitors in some countries: try --geo de, or an EU --proxy. A banner Echo doesn't know: --accept-selector and --reject-selector" ||
		!slices.Equal(is.Evidence, []int64{3, 4}) {
		t.Errorf("%+v", is)
	}
	if r.Pages[0].NeedsNone {
		t.Error("trackers fired: the page needs consent")
	}

	// One tracker reads "is"; --banner-wait shows in the detail.
	r.Pages[0].Runs[0].Hits = r.Pages[0].Runs[0].Hits[1:]
	p.BannerWait = 25 * time.Second
	Evaluator{}.Evaluate(r, p)
	if is := issue(t, r, "consent/no-banner|/|none|"); is.Title != "Meta Pixel is on the page but no consent banner showed" ||
		is.Detail[:68] != "Meta Pixel is on the page but no consent banner showed within 25s. M" {
		t.Errorf("%+v", is)
	}

	// A banner on any visit: no no-banner.
	r.Pages[0].Runs[0].Visits[0].Banner = true
	Evaluator{}.Evaluate(r, p)
	if keys := keysOf(r, []string{consent.CheckNoBanner}); len(keys) != 0 {
		t.Errorf("banner shown: %v", keys)
	}
}

func TestNoBannerNegative(t *testing.T) { // g12
	r := judged(t, "no-trackers", "", "")
	if keys := keysOf(r, nil); len(keys) != 0 {
		t.Errorf("no-trackers: %v", keys)
	}
	r = judged(t, "inconclusive", "", "")
	if keys := keysOf(r, nil); len(keys) != 0 {
		t.Errorf("inconclusive: %v", keys)
	}
}

func TestClickFallback(t *testing.T) { // c20 with --click button,api
	r := rejected()
	r.Pages[0].Runs[0].Choice.How, r.Pages[0].Runs[0].Choice.Fallback = "api", true
	Evaluator{}.Evaluate(r, nil)
	sameKeys(t, keysOf(r, v4Checks), "consent/click-fallback|/|reject|")
	is := issue(t, r, "consent/click-fallback|/|reject|")
	if is.Level != model.Notice || is.Title != "Echo couldn't click the button and used Borlabs Cookie's API, so the button itself wasn't tested" ||
		is.Detail != "Reject: no Reject button was found with --click button,api" {
		t.Errorf("%+v", is)
	}
	// The withdraw Reject falling back counts too; no CMP name reads "the CMP".
	r = result(consent.Run{Kind: consent.RunWithdraw, Choice: &consent.Choice{Verified: true}, Withdraw: &consent.Choice{How: "api", Fallback: true, Verified: true}})
	r.Pages[0].CMP.Name = ""
	Evaluator{}.Evaluate(r, nil)
	if is := issue(t, r, "consent/click-fallback|/|withdraw|"); is.Title != "Echo couldn't click the button and used the CMP's API, so the button itself wasn't tested" {
		t.Errorf("%q", is.Title)
	}
}

// tcfPage is a Didomi TCF page with Accept and Reject runs, each with the
// TC string read after its choice.
func tcfPage(acceptPurposes, rejectPurposes []int) *consent.Result {
	tcf := func(at int64, purposes []int) consent.Signal {
		return consent.Signal{Source: "tcf", Command: "useractioncomplete", AtMS: at,
			TCF: &consent.TCF{Version: 2, CMPID: 7, PurposesConsent: purposes, VendorsConsent: len(purposes) * 10, Valid: true}}
	}
	r := result(
		consent.Run{Kind: consent.RunAccept, Choice: &consent.Choice{Verified: true, AtMS: 2000},
			Signals: []consent.Signal{tcf(100, nil), tcf(2100, acceptPurposes)}},
		consent.Run{Kind: consent.RunReject, Choice: &consent.Choice{Verified: true, AtMS: 2000},
			Signals: []consent.Signal{tcf(100, []int{1, 2, 3}), tcf(2100, rejectPurposes)}},
	)
	r.Pages[0].CMP = consent.CMPInfo{ID: "didomi", Name: "Didomi", Found: true, Present: true, TCF: true}
	return r
}

func TestTCFMismatch(t *testing.T) { // c19, g07
	r := tcfPage([]int{1, 2, 3, 4}, []int{1, 3})
	Evaluator{}.Evaluate(r, nil)
	sameKeys(t, keysOf(r, v4Checks), "consent/tcf-mismatch|/|reject|")
	is := issue(t, r, "consent/tcf-mismatch|/|reject|")
	if is.Level != model.Warning || is.Title != "The TCF string grants purposes 1 and 3 after Reject" ||
		is.Detail != "TC string read at 2.10s (CMP 7, 20 vendors with consent)" {
		t.Errorf("%+v", is)
	}

	r = tcfPage(nil, nil)
	Evaluator{}.Evaluate(r, nil)
	sameKeys(t, keysOf(r, v4Checks), "consent/tcf-mismatch|/|accept|")
	if is := issue(t, r, "consent/tcf-mismatch|/|accept|"); is.Title != "The TCF string grants no purpose after Accept" {
		t.Errorf("%q", is.Title)
	}

	// g07: the strings match the choices.
	r = tcfPage([]int{1, 2}, nil)
	Evaluator{}.Evaluate(r, nil)
	if keys := keysOf(r, v4Checks); len(keys) != 0 {
		t.Errorf("matching TCF: %v", keys)
	}
	// Not a TCF CMP: nothing is read.
	r = tcfPage(nil, []int{1})
	r.Pages[0].CMP.TCF = false
	Evaluator{}.Evaluate(r, nil)
	if keys := keysOf(r, v4Checks); len(keys) != 0 {
		t.Errorf("no TCF CMP: %v", keys)
	}
	// An unreadable string is a note.
	r = tcfPage([]int{1}, []int{1})
	r.Pages[0].Runs[1].Signals[1].TCF.Valid = false
	Evaluator{}.Evaluate(r, nil)
	if keys := keysOf(r, v4Checks); len(keys) != 0 {
		t.Errorf("unreadable TCF: %v", keys)
	}
	if want := "Reject on /: the TCF string after the choice couldn't be read, so consent/tcf-mismatch wasn't checked"; !slices.Contains(r.Notes, want) {
		t.Errorf("notes %q", r.Notes)
	}
}

func TestServerSide(t *testing.T) { // c18, g01 with --echo
	r := rejected()
	r.Pages[0].Runs[0].Server = []consent.EchoMessage{
		{ID: "m1", Provider: "meta-capi", Event: "PageView", AtMS: 1500, Phase: consent.PhaseBefore},
		{ID: "m2", Provider: "meta-capi", Event: "PageView", AtMS: 2500, Phase: consent.PhaseAfterReject},
		{ID: "m3", Provider: "meta-capi", Event: "ViewContent", AtMS: 9500, Phase: consent.PhaseAfterReject},
		{ID: "m4", Provider: "ga4-mp", Event: "page_view", AtMS: 2600, Phase: consent.PhaseAfterReject},
	}
	Evaluator{}.Evaluate(r, nil)
	sameKeys(t, keysOf(r, v4Checks), "consent/server-side|/|reject|meta-capi", "consent/server-side|/|reject|ga4-mp")
	is := issue(t, r, "consent/server-side|/|reject|meta-capi")
	if is.Level != model.Error || is.Title != "Meta Conversions API received PageView and ViewContent from the server after Reject" ||
		is.Detail != "2 messages caught by Echo, the first at 2.50s (m2, m3)" {
		t.Errorf("%+v", is)
	}
	if is := issue(t, r, "consent/server-side|/|reject|ga4-mp"); is.Title != "GA4 Measurement Protocol received page_view from the server after Reject" {
		t.Errorf("%q", is.Title)
	}

	// Server events before the click or in the accept run are allowed.
	r = result(consent.Run{Kind: consent.RunAccept, Server: []consent.EchoMessage{{ID: "a", Provider: "meta-capi", Event: "Purchase", Phase: consent.PhaseAfterAccept}}})
	Evaluator{}.Evaluate(r, nil)
	if keys := keysOf(r, v4Checks); len(keys) != 0 {
		t.Errorf("after Accept: %v", keys)
	}
	r = result(consent.Run{Kind: consent.RunWithdraw, Server: []consent.EchoMessage{{ID: "w", Provider: "x-api", Phase: consent.PhaseAfterWithdraw}}})
	Evaluator{}.Evaluate(r, nil)
	if is := issue(t, r, "consent/server-side|/|withdraw|x-api"); is.Title != "x-api received an event from the server after consent was withdrawn" {
		t.Errorf("%q", is.Title)
	}
}

func TestInconclusiveGolden(t *testing.T) {
	r := judged(t, "inconclusive", "", "")
	want := []consent.Inconclusive{
		{Page: "/", Run: consent.RunAccept, Reason: consent.ReasonNoBanner, Detail: "Accept: no banner showed within 10s (Cookiebot's script loaded)"},
		{Page: "/", Run: consent.RunReject, Reason: consent.ReasonNoBanner, Detail: "Reject: no banner showed within 10s (Cookiebot's script loaded)"},
	}
	if !slices.Equal(r.Inconclusive, want) {
		t.Errorf("inconclusive\n got %+v\nwant %+v", r.Inconclusive, want)
	}
	if r.ExitCode("") != 3 {
		t.Errorf("exit %d, want 3", r.ExitCode(""))
	}
	if r := judged(t, "no-trackers", "", ""); len(r.Inconclusive) != 0 || r.ExitCode("") != 0 {
		t.Errorf("no-trackers: %+v exit %d", r.Inconclusive, r.ExitCode(""))
	}
}

func TestInconclusiveTexts(t *testing.T) {
	onetrust := consent.CMPInfo{ID: "onetrust", Name: "OneTrust", Found: true, Present: true}
	cookiebot := consent.CMPInfo{ID: "cookiebot", Name: "Cookiebot", Found: true, Present: true}
	click := &consent.Choice{How: "button", Text: "Accept all", Selector: "#onetrust-accept-btn-handler", Clicks: 3}
	unchanged := &consent.Choice{How: "button", Text: "Reject all", Selector: "#onetrust-reject-all-handler", Clicks: 1,
		State: &consent.CMPState{CMP: "onetrust", Source: "cookie:OptanonConsent"}}
	slow := []consent.Event{
		{Seq: 1, Kind: consent.EvRequest, Request: &consent.Request{URL: "https://shop.test/", Status: 200}},
		{Seq: 2, Kind: consent.EvRequest, Request: &consent.Request{URL: "https://cdn.example/slow.js"}},
		{Seq: 3, Kind: consent.EvRequest, Request: &consent.Request{URL: "https://www.facebook.com/tr?id=1", Held: true}},
		{Seq: 4, Kind: consent.EvRequest, Request: &consent.Request{URL: "https://10.0.0.1/", Refused: "private address"}},
	}
	cases := []struct {
		name string
		cmp  consent.CMPInfo
		run  consent.Run
		plan func(p *consent.Plan)
		want string
	}{
		{"no_button", cookiebot, consent.Run{Kind: consent.RunReject, Reason: consent.ReasonNoButton}, nil,
			"Reject: Cookiebot's banner showed but Echo found no Reject on it or in its settings. Set --reject-selector (rhythm echo consent detect shows what Echo sees), or --click button,api to use Cookiebot's API"},
		{"no_button accept", cookiebot, consent.Run{Kind: consent.RunAccept, Reason: consent.ReasonNoButton}, nil,
			"Accept: Cookiebot's banner showed but Echo found no Accept on it or in its settings. Set --accept-selector (rhythm echo consent detect shows what Echo sees), or --click button,api to use Cookiebot's API"},
		{"click_missed", onetrust, consent.Run{Kind: consent.RunAccept, Reason: consent.ReasonClickMissed, Choice: click}, nil,
			`Accept: clicked "Accept all" (#onetrust-accept-btn-handler) 3 times and the banner stayed. Something may cover it (a chat bubble, a newsletter popup); --show-browser shows the page`},
		{"state_unchanged", onetrust, consent.Run{Kind: consent.RunReject, Reason: consent.ReasonStateUnchanged, Choice: unchanged}, nil,
			`Reject: clicked "Reject all" and the banner closed but OneTrust's stored state didn't change (OptanonConsent still has no choice). Hiding a banner is not rejecting`},
		{"timeout", onetrust, consent.Run{Kind: consent.RunReject, Reason: consent.ReasonTimeout, Events: slow}, nil,
			"Reject: the run took longer than --timeout 90s; the last request still open was https://cdn.example/slow.js"},
		{"timeout 2m nothing open", onetrust, consent.Run{Kind: consent.RunNone, Reason: consent.ReasonTimeout}, func(p *consent.Plan) { p.Timeout = 2 * time.Minute },
			"No choice: the run took longer than --timeout 2m"},
		{"no_banner", consent.CMPInfo{}, consent.Run{Kind: consent.RunAccept, Reason: consent.ReasonNoBanner}, nil,
			"Accept: no banner showed within 10s"},
		{"no_banner cmp", consent.CMPInfo{Name: "Cookiebot", Present: true}, consent.Run{Kind: consent.RunReject, Reason: consent.ReasonNoBanner}, func(p *consent.Plan) { p.BannerWait = 20 * time.Second },
			"Reject: no banner showed within 20s (Cookiebot's script loaded)"},
		{"cmp_not_found", consent.CMPInfo{}, consent.Run{Kind: consent.RunReject, Reason: consent.ReasonCMPNotFound}, func(p *consent.Plan) { p.CMP = "onetrust" },
			"Reject: --cmp onetrust was named but its banner wasn't found"},
		{"navigation_failed", onetrust, consent.Run{Kind: consent.RunReject, Reason: consent.ReasonNavigation, Detail: "net::ERR_CONNECTION_RESET\x1b[2J"}, nil,
			`net::ERR_CONNECTION_RESET\x1b[2J`},
		{"navigation_failed bare", onetrust, consent.Run{Kind: consent.RunAccept, Reason: consent.ReasonNavigation}, nil,
			"Accept: the page didn't load"},
		{"limit", onetrust, consent.Run{Kind: consent.RunNone, Reason: consent.ReasonLimit}, nil, "stopped after 20000 requests"},
		{"canceled", onetrust, consent.Run{Kind: consent.RunGPC, Reason: consent.ReasonCanceled}, nil, "interrupted"},
		{"unknown reason", onetrust, consent.Run{Kind: consent.RunGPC, Reason: "odd"}, nil, "GPC: odd"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			r := result(c.run)
			r.Pages[0].CMP = c.cmp
			p := consenttest.NewPlan(t)
			if c.plan != nil {
				c.plan(p)
			}
			Evaluator{}.Evaluate(r, p)
			if len(r.Inconclusive) != 1 {
				t.Fatalf("inconclusive %+v", r.Inconclusive)
			}
			got := r.Inconclusive[0]
			if got.Detail != c.want || got.Page != "/" || got.Run != c.run.Kind || got.Reason != c.run.Reason {
				t.Errorf("got  %+v\nwant %q", got, c.want)
			}
		})
	}
}

func TestDurationText(t *testing.T) {
	for d, want := range map[time.Duration]string{10 * time.Second: "10s", 90 * time.Second: "90s", 2 * time.Minute: "2m",
		60 * time.Second: "60s", 1500 * time.Millisecond: "1.5s", 10 * time.Minute: "10m"} {
		if got := durationText(d); got != want {
			t.Errorf("durationText(%s) = %q, want %q", d, got, want)
		}
	}
}

func TestGoldensEndToEnd(t *testing.T) {
	cases := map[string][]string{
		"clean":        nil,
		"no-trackers":  nil,
		"inconclusive": nil,
		"advanced":     {"consent/cookieless-ping|/|none|ga4"},
		"meta-ignores": {"consent/before-choice|/|none|meta-pixel", "consent/after-reject|/|reject|meta-pixel", "consent/meta-no-consent-api|/|none|meta-pixel"},
		"compare-cur":  {"consent/before-choice|/|none|tiktok-pixel", "consent/before-choice|/|reject|tiktok-pixel"},
	}
	for name, want := range cases {
		r := judged(t, name, "", "")
		got := keysOf(r, nil)
		if len(want) == 0 && len(got) == 0 {
			continue
		}
		sameKeys(t, got, want...)
	}
}
