// Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
// SPDX-License-Identifier: MIT

package verdict

import (
	"fmt"
	"slices"
	"sort"
	"strings"
	"testing"

	"github.com/RhythmShahriar/echo/consent"
	"github.com/RhythmShahriar/echo/consent/consenttest"
	"github.com/RhythmShahriar/echo/model"
)

// v2Checks are the checks trackers.go raises.
var v2Checks = []string{consent.CheckBeforeChoice, consent.CheckAfterReject, consent.CheckAfterWithdraw, consent.CheckEmbed,
	consent.CheckScriptBefore, consent.CheckCookielessPing, consent.CheckGPCIgnored, consent.CheckMetaNoConsentAPI}

// keysOf lists the keys of r's issues of the given checks (all when
// checks is nil), sorted.
func keysOf(r *consent.Result, checks []string) []string {
	var keys []string
	for _, is := range r.Issues {
		if checks == nil || slices.Contains(checks, is.Check) {
			keys = append(keys, is.Key)
		}
	}
	sort.Strings(keys)
	return keys
}

// issue returns r's issue with key, failing t when there is none.
func issue(t *testing.T, r *consent.Result, key string) consent.Issue {
	t.Helper()
	for _, is := range r.Issues {
		if is.Key == key {
			return is
		}
	}
	t.Fatalf("no issue %s; have %v", key, keysOf(r, nil))
	return consent.Issue{}
}

func sameKeys(t *testing.T, got []string, want ...string) {
	t.Helper()
	sort.Strings(want)
	if !slices.Equal(got, want) {
		t.Errorf("issues\n got %q\nwant %q", got, want)
	}
}

func TestMetaIgnoresConsentMode(t *testing.T) {
	r := judged(t, "meta-ignores", "", "")
	sameKeys(t, keysOf(r, v2Checks),
		"consent/before-choice|/|none|meta-pixel",
		"consent/after-reject|/|reject|meta-pixel",
		"consent/meta-no-consent-api|/|none|meta-pixel")
	before := issue(t, r, "consent/before-choice|/|none|meta-pixel")
	if before.Title != "Meta Pixel fired before the visitor chose" || before.Level != model.Error || before.Policy != consent.PolicyAdvanced {
		t.Errorf("before-choice: %+v", before)
	}
	if want := "3 hits (PageView on /, /shop/, /); cookie _fbp written"; before.Detail != want {
		t.Errorf("before-choice detail %q, want %q", before.Detail, want)
	}
	if !slices.Equal(before.Evidence, []int64{14, 15, 16}) {
		t.Errorf("before-choice evidence %v", before.Evidence)
	}
	after := issue(t, r, "consent/after-reject|/|reject|meta-pixel")
	if after.Title != "Meta Pixel fired after Reject" || after.Detail != "3 hits (PageView on /, /shop/, /); cookie _fbp written" {
		t.Errorf("after-reject: %q %q", after.Title, after.Detail)
	}
	if after.Fix != checkFix(consent.CheckAfterReject) {
		t.Errorf("after-reject fix without a catalog: %q", after.Fix)
	}
	meta := issue(t, r, "consent/meta-no-consent-api|/|none|meta-pixel")
	if meta.Title != "Meta Pixel never received fbq('consent', ...)" || meta.Level != model.Notice ||
		meta.Detail != "no fbq('consent', 'revoke') or fbq('consent', 'grant') before the first Meta Pixel hit at 0.84s" ||
		!slices.Equal(meta.Evidence, []int64{14}) {
		t.Errorf("meta-no-consent-api: %+v", meta)
	}
}

func TestSingleHitDetail(t *testing.T) {
	r := judged(t, "compare-cur", "", "")
	sameKeys(t, keysOf(r, v2Checks),
		"consent/before-choice|/|none|tiktok-pixel",
		"consent/before-choice|/|reject|tiktok-pixel")
	want := consenttest.GoldenResult("compare-cur")
	for _, w := range want.Issues {
		got := issue(t, r, w.Key)
		if got.Title != w.Title || got.Detail != w.Detail || !slices.Equal(got.Evidence, w.Evidence) {
			t.Errorf("%s\n got %q %q %v\nwant %q %q %v", w.Key, got.Title, got.Detail, got.Evidence, w.Title, w.Detail, w.Evidence)
		}
	}
}

func TestMetaConsentAPISeen(t *testing.T) {
	r := consenttest.GoldenResult("meta-ignores")
	for i := range r.Pages[0].Runs {
		run := &r.Pages[0].Runs[i]
		run.Signals = append(run.Signals, consent.Signal{Source: "fbq", Command: "revoke", AtMS: 590})
	}
	Evaluator{}.Evaluate(r, consenttest.NewPlan(t))
	if keys := keysOf(r, []string{consent.CheckMetaNoConsentAPI}); len(keys) != 0 {
		t.Errorf("fbq revoke seen, still %v", keys)
	}
	// A revoke after the first hit is too late.
	r = consenttest.GoldenResult("meta-ignores")
	r.Pages[0].Runs[0].Signals = append(r.Pages[0].Runs[0].Signals, consent.Signal{Source: "fbq", Command: "revoke", AtMS: 900})
	Evaluator{}.Evaluate(r, consenttest.NewPlan(t))
	sameKeys(t, keysOf(r, []string{consent.CheckMetaNoConsentAPI}), "consent/meta-no-consent-api|/|none|meta-pixel")
}

func TestCookielessByPolicy(t *testing.T) {
	adv := judged(t, "advanced", consent.PolicyAdvanced, "")
	sameKeys(t, keysOf(adv, v2Checks), "consent/cookieless-ping|/|none|ga4")
	ping := issue(t, adv, "consent/cookieless-ping|/|none|ga4")
	if ping.Level != model.Notice ||
		ping.Title != "Google Analytics 4 sent 2 cookieless pings before the choice (allowed under --consent-mode advanced)" ||
		ping.Detail != "region1.google-analytics.com/g/collect?gcs=G100 at 0.62s and 9.90s; no Google cookie written" ||
		!slices.Equal(ping.Evidence, []int64{14, 15}) {
		t.Errorf("cookieless-ping: %+v", ping)
	}

	basic := judged(t, "advanced", consent.PolicyBasic, "")
	sameKeys(t, keysOf(basic, v2Checks), "consent/before-choice|/|none|ga4")
	if is := issue(t, basic, "consent/before-choice|/|none|ga4"); is.Policy != consent.PolicyBasic || is.Detail != "2 hits (page_view on /, /shop/)" {
		t.Errorf("basic before-choice: %+v", is)
	}

	strict := judged(t, "advanced", consent.PolicyStrict, "")
	sameKeys(t, keysOf(strict, v2Checks),
		"consent/before-choice|/|none|ga4",
		"consent/script-before|/|none|google-tag-manager",
		"consent/script-before|/|none|meta-pixel",
		"consent/script-before|/|accept|google-tag-manager",
		"consent/script-before|/|reject|google-tag-manager")
	gtm := issue(t, strict, "consent/script-before|/|none|google-tag-manager")
	if gtm.Title != "google-tag-manager script loaded before the visitor chose (strict policy)" ||
		gtm.Detail != "googletagmanager.com/gtm.js?id=GTM-••••XK2 at 0.30s" || gtm.Level != model.Error {
		t.Errorf("script-before: %+v", gtm)
	}
}

func TestStrictScriptAfterReject(t *testing.T) {
	run := consent.Run{Kind: consent.RunReject, Scripts: []consent.Script{
		{Tracker: "hotjar", URL: "https://static.hotjar.com/c/hotjar-1.js?sv=6", AtMS: 2500, Phase: consent.PhaseAfterReject, Seq: 9},
		{Tracker: "stripe", URL: "https://js.stripe.com/v3/", AtMS: 2500, Phase: consent.PhaseAfterReject, Seq: 10},
	}}
	r := result(run)
	consenttest.Use(t, consent.Stages{Classifier: &catalog{}})
	p := consenttest.NewPlan(t)
	p.Policy = consent.PolicyStrict
	Evaluator{}.Evaluate(r, p)
	sameKeys(t, keysOf(r, v2Checks), "consent/script-before|/|reject|hotjar")
	if is := issue(t, r, "consent/script-before|/|reject|hotjar"); is.Title != "Hotjar script loaded after Reject (strict policy)" {
		t.Errorf("title %q", is.Title)
	}
}

// result is a one-page result at https://shop.test/ with two visits.
func result(runs ...consent.Run) *consent.Result {
	for i := range runs {
		if runs[i].Visits == nil {
			runs[i].Visits = []consent.Visit{{URL: "https://shop.test/", Step: "load"}, {URL: "https://shop.test/blog/", Step: "page", AtMS: 9000}}
		}
		if runs[i].Status == "" {
			runs[i].Status = consent.StatusOK
		}
	}
	return &consent.Result{Policy: consent.PolicyAdvanced, Pages: []consent.Page{{URL: "https://shop.test/",
		CMP: consent.CMPInfo{ID: "cookiebot", Name: "Cookiebot", Found: true, Present: true}, Runs: runs}}}
}

// hit is a tracker hit of the small catalog below.
func hit(seq, at int64, ph consent.Phase, tracker string, visit int, rawURL string) consent.HitRef {
	t := catalogInfo(tracker)
	return consent.HitRef{Seq: seq, AtMS: at, Phase: ph, Visit: visit, URL: rawURL, Held: true,
		Hit: consent.Hit{Tracker: tracker, Name: t.Name, Category: t.Category, Need: t.Need, Kind: consent.HitBeacon}}
}

// catalog is a classifier with a few catalog entries and fix lines, for
// names, needs and fixes the result itself doesn't carry.
type catalog struct{ consenttest.FakeClassifier }

var catalogEntries = []consent.TrackerInfo{
	{ID: "hotjar", Name: "Hotjar", Category: consent.CatSessionReplay, Need: consent.NeedNeeded, Fix: "Load Hotjar only after Accept."},
	{ID: "stripe", Name: "Stripe.js", Category: consent.CatNecessary, Need: consent.NeedNo},
	{ID: "plausible", Name: "Plausible", Category: consent.CatAnalytics, Need: consent.NeedExempt},
	{ID: "youtube", Name: "YouTube embed", Category: consent.CatEmbed, Need: consent.NeedNeeded, Fix: "Use youtube-nocookie.com."},
	{ID: "meta-pixel", Name: "Meta Pixel", Category: consent.CatAdvertising, Need: consent.NeedNeeded},
	{ID: "ga4", Name: "Google Analytics 4", Category: consent.CatAnalytics, Need: consent.NeedNeeded},
	{ID: "tiktok-pixel", Name: "TikTok Pixel", Category: consent.CatAdvertising, Need: consent.NeedNeeded},
}

func catalogInfo(id string) consent.TrackerInfo {
	for _, t := range catalogEntries {
		if t.ID == id {
			return t
		}
	}
	panic("no catalog entry " + id)
}

// Trackers lists the small catalog.
func (c *catalog) Trackers() []consent.TrackerInfo { return slices.Clone(catalogEntries) }

func TestExemptTrackers(t *testing.T) {
	r := result(consent.Run{Kind: consent.RunNone,
		Hits:    []consent.HitRef{hit(3, 400, consent.PhaseBefore, "plausible", 0, "https://plausible.io/api/event")},
		Storage: []consent.StorageKey{{Area: "local", Key: "_hjSession_123", Tracker: "hotjar", Category: consent.CatSessionReplay, Need: consent.NeedNeeded, Phase: consent.PhaseBefore, AtMS: 500}},
		Cookies: []consent.Cookie{{Name: "_hjSessionUser_123", Tracker: "hotjar", Category: consent.CatSessionReplay, Need: consent.NeedNeeded, Phase: consent.PhaseBefore, AtMS: 510}},
	})
	r.Pages[0].Runs[0].Hits = append(r.Pages[0].Runs[0].Hits, hit(4, 450, consent.PhaseBefore, "hotjar", 0, "https://in.hotjar.com/api/v2/client/sites/123/visit-data?sv=7"))
	plain := result(r.Pages[0].Runs[0])
	consenttest.Use(t, consent.Stages{Classifier: &catalog{}})
	Evaluator{}.Evaluate(plain, consenttest.NewPlan(t))
	sameKeys(t, keysOf(plain, v2Checks), "consent/before-choice|/|none|hotjar")
	is := issue(t, plain, "consent/before-choice|/|none|hotjar")
	if is.Detail != "in.hotjar.com/api/v2/client/sites/123/visit-data?sv=7 at 0.45s; cookie _hjSessionUser_123 written; local storage key _hjSession_123 written" {
		t.Errorf("detail %q", is.Detail)
	}
	if is.Fix != "Load Hotjar only after Accept." {
		t.Errorf("fix %q, want the catalog's", is.Fix)
	}

	p := consenttest.NewPlan(t)
	p.Exempt = []string{"hotjar"}
	Evaluator{}.Evaluate(r, p)
	if keys := keysOf(r, nil); len(keys) != 0 {
		t.Errorf("--exempt hotjar: %v", keys)
	}
	if r.Pages[0].Runs[0].Cookies[0].Need != consent.NeedExempt || r.Pages[0].Runs[0].Hits[1].Need != consent.NeedExempt {
		t.Error("exempt entries keep need needed")
	}
}

func TestStorageOnlyDetail(t *testing.T) {
	r := result(consent.Run{Kind: consent.RunNone,
		Storage: []consent.StorageKey{{Area: "local", Key: "_clsk", Tracker: "clarity", Category: consent.CatSessionReplay, Need: consent.NeedNeeded, Phase: consent.PhaseBefore, AtMS: 1500}},
	})
	Evaluator{}.Evaluate(r, nil)
	is := issue(t, r, "consent/before-choice|/|none|clarity")
	if is.Title != "clarity fired before the visitor chose" || is.Detail != "local storage key _clsk written at 1.50s" {
		t.Errorf("%q %q", is.Title, is.Detail)
	}
}

func TestEmbedAfterReject(t *testing.T) {
	yt := hit(12, 2600, consent.PhaseAfterReject, "youtube", 0, "https://www.youtube.com/embed/abc123?rel=0")
	yt.Kind = consent.HitFrame
	r := result(consent.Run{Kind: consent.RunReject, Choice: &consent.Choice{How: "button", Verified: true, AtMS: 2000},
		Hits:    []consent.HitRef{yt},
		Cookies: []consent.Cookie{{Name: "VISITOR_INFO1_LIVE", Tracker: "youtube", Category: consent.CatEmbed, Need: consent.NeedNeeded, Phase: consent.PhaseAfterReject, AtMS: 2700}},
	})
	consenttest.Use(t, consent.Stages{Classifier: &catalog{}})
	Evaluator{}.Evaluate(r, nil)
	sameKeys(t, keysOf(r, v2Checks), "consent/embed|/|reject|youtube")
	is := issue(t, r, "consent/embed|/|reject|youtube")
	if is.Level != model.Warning || is.Title != "YouTube embed loaded after Reject" ||
		is.Detail != "youtube.com/embed/abc123?rel=0 at 2.60s; cookie VISITOR_INFO1_LIVE written" || is.Fix != "Use youtube-nocookie.com." {
		t.Errorf("embed: %+v", is)
	}
	// Before the choice the title says so.
	r.Pages[0].Runs[0].Kind = consent.RunNone
	Evaluator{}.Evaluate(r, nil)
	if is := issue(t, r, "consent/embed|/|none|youtube"); is.Title != "YouTube embed loaded before the visitor chose" {
		t.Errorf("title %q", is.Title)
	}
}

func TestGPCRun(t *testing.T) {
	r := result(consent.Run{Kind: consent.RunGPC,
		Hits: []consent.HitRef{
			hit(5, 800, consent.PhaseGPC, "meta-pixel", 0, "https://www.facebook.com/tr?id=••••5678&ev=PageView"),
			hit(6, 820, consent.PhaseGPC, "ga4", 0, "https://region1.google-analytics.com/g/collect?v=2&tid=G-••••7Q2X&en=page_view"),
		},
		Cookies: []consent.Cookie{{Name: "_fbp", Tracker: "meta-pixel", Category: consent.CatAdvertising, Need: consent.NeedNeeded, Phase: consent.PhaseGPC, AtMS: 830}},
		Storage: []consent.StorageKey{{Area: "local", Key: "_hjSession", Tracker: "hotjar", Category: consent.CatAdvertising, Need: consent.NeedNeeded, Phase: consent.PhaseGPC}},
	})
	Evaluator{}.Evaluate(r, nil)
	sameKeys(t, keysOf(r, v2Checks), "consent/gpc-ignored|/|gpc|meta-pixel")
	is := issue(t, r, "consent/gpc-ignored|/|gpc|meta-pixel")
	if is.Level != model.Warning || is.Title != "Meta Pixel fired with Global Privacy Control on" ||
		is.Detail != "facebook.com/tr?id=••••5678&ev=PageView at 0.80s; cookie _fbp written" {
		t.Errorf("gpc-ignored: %+v", is)
	}
}

func TestAcceptRunAfterChoice(t *testing.T) {
	// Hits queued while denied and flushed by the update after Accept are
	// after-accept: allowed.
	r := result(consent.Run{Kind: consent.RunAccept, Choice: &consent.Choice{Verified: true, AtMS: 2000},
		Hits:    []consent.HitRef{hit(4, 2001, consent.PhaseAfterAccept, "meta-pixel", 0, "https://www.facebook.com/tr?id=1&ev=PageView")},
		Cookies: []consent.Cookie{{Name: "_fbp", Tracker: "meta-pixel", Need: consent.NeedNeeded, Phase: consent.PhaseAfterAccept, AtMS: 2010}},
	})
	Evaluator{}.Evaluate(r, nil)
	if keys := keysOf(r, nil); len(keys) != 0 {
		t.Errorf("after Accept: %v", keys)
	}
	// One ms before the click is before the choice.
	r.Pages[0].Runs[0].Hits[0].Phase = consent.PhaseBefore
	Evaluator{}.Evaluate(r, nil)
	sameKeys(t, keysOf(r, v2Checks), "consent/before-choice|/|accept|meta-pixel", "consent/meta-no-consent-api|/|accept|meta-pixel")
}

func TestAfterRejectGrantedGoesElsewhere(t *testing.T) {
	g := hit(8, 2300, consent.PhaseAfterReject, "ga4", 0, "https://region1.google-analytics.com/g/collect?v=2&tid=G-1&gcs=G111")
	g.Google = &consent.GoogleConsent{GCS: "G111", GCSDecoded: &consent.GCS{Known: true, AdStorage: consent.Granted, AnalyticsStorage: consent.Granted}}
	unknown := hit(9, 2400, consent.PhaseAfterReject, "ga4", 1, "https://region1.google-analytics.com/g/collect?v=2&tid=G-1&gcs=G2xx")
	unknown.Google = &consent.GoogleConsent{GCS: "G2xx", GCSDecoded: &consent.GCS{Known: false, AdStorage: consent.Granted}}
	r := result(consent.Run{Kind: consent.RunReject, Choice: &consent.Choice{Verified: true, AtMS: 2000}, Hits: []consent.HitRef{g}})
	Evaluator{}.Evaluate(r, nil)
	if keys := keysOf(r, []string{consent.CheckAfterReject}); len(keys) != 0 {
		t.Errorf("granted gcs after Reject raised %v", keys)
	}
	r.Pages[0].Runs[0].Hits = append(r.Pages[0].Runs[0].Hits, unknown)
	Evaluator{}.Evaluate(r, nil)
	sameKeys(t, keysOf(r, []string{consent.CheckAfterReject}), "consent/after-reject|/|reject|ga4")
	if is := issue(t, r, "consent/after-reject|/|reject|ga4"); !slices.Equal(is.Evidence, []int64{9}) {
		t.Errorf("evidence %v", is.Evidence)
	}
}

func TestAfterWithdraw(t *testing.T) {
	r := result(consent.Run{Kind: consent.RunWithdraw, Choice: &consent.Choice{Verified: true, AtMS: 2000},
		Withdraw: &consent.Choice{Verified: true, AtMS: 5000},
		Hits: []consent.HitRef{
			hit(4, 2500, consent.PhaseAfterAccept, "tiktok-pixel", 0, "https://analytics.tiktok.com/api/v2/pixel?sdkid=1&event=Pageview"),
			hit(7, 9100, consent.PhaseAfterWithdraw, "tiktok-pixel", 1, "https://analytics.tiktok.com/api/v2/pixel?sdkid=1&event=Pageview"),
		}})
	consenttest.Use(t, consent.Stages{Classifier: &catalog{}})
	Evaluator{}.Evaluate(r, nil)
	sameKeys(t, keysOf(r, nil), "consent/after-withdraw|/|withdraw|tiktok-pixel")
	if is := issue(t, r, "consent/after-withdraw|/|withdraw|tiktok-pixel"); is.Title != "TikTok Pixel fired after consent was withdrawn" ||
		is.Detail != "analytics.tiktok.com/api/v2/pixel?sdkid=1&event=Pageview at 9.10s" {
		t.Errorf("%+v", is)
	}
}

func TestAfterWithdrawEmbedAndCookieless(t *testing.T) {
	// consent/embed covers before the choice and after Reject only: an
	// embed after the withdraw is after-withdraw like any tracker.
	yt := hit(7, 9100, consent.PhaseAfterWithdraw, "youtube", 1, "https://www.youtube.com/embed/abc123?rel=0")
	yt.Kind = consent.HitFrame
	ga := hit(8, 9200, consent.PhaseAfterWithdraw, "ga4", 1, "https://region1.google-analytics.com/g/collect?v=2&tid=G-1&gcs=G100")
	ga.Google = &consent.GoogleConsent{GCS: "G100", GCSDecoded: &consent.GCS{Known: true, AdStorage: consent.Denied, AnalyticsStorage: consent.Denied}}
	r := result(consent.Run{Kind: consent.RunWithdraw, Choice: &consent.Choice{Verified: true, AtMS: 2000},
		Withdraw: &consent.Choice{Verified: true, AtMS: 5000}, Hits: []consent.HitRef{yt, ga},
		Cookies: []consent.Cookie{{Name: "VISITOR_INFO1_LIVE", Tracker: "youtube", Category: consent.CatEmbed, Need: consent.NeedNeeded,
			Phase: consent.PhaseAfterWithdraw, AtMS: 9150}}})
	consenttest.Use(t, consent.Stages{Classifier: &catalog{}})
	Evaluator{}.Evaluate(r, nil)
	sameKeys(t, keysOf(r, v2Checks), "consent/after-withdraw|/|withdraw|youtube", "consent/cookieless-ping|/|withdraw|ga4")
	if is := issue(t, r, "consent/after-withdraw|/|withdraw|youtube"); is.Title != "YouTube embed fired after consent was withdrawn" ||
		is.Detail != "youtube.com/embed/abc123?rel=0 at 9.10s; cookie VISITOR_INFO1_LIVE written" {
		t.Errorf("embed after withdraw: %+v", is)
	}
	if is := issue(t, r, "consent/cookieless-ping|/|withdraw|ga4"); is.Title !=
		"Google Analytics 4 sent 1 cookieless ping after consent was withdrawn (allowed under --consent-mode advanced)" {
		t.Errorf("cookieless after withdraw: %q", is.Title)
	}
	// Under basic the same ping is an after-withdraw hit.
	r.Policy = consent.PolicyBasic
	Evaluator{}.Evaluate(r, nil)
	sameKeys(t, keysOf(r, v2Checks), "consent/after-withdraw|/|withdraw|youtube", "consent/after-withdraw|/|withdraw|ga4")
}

func TestEvidenceNotCapped(t *testing.T) {
	run := consent.Run{Kind: consent.RunNone, Hits: []consent.HitRef{
		hit(1, 100, consent.PhaseBefore, "meta-pixel", 0, "https://www.facebook.com/tr?id=1&ev=PageView")}}
	r := result(run)
	Evaluator{}.Evaluate(r, nil)
	is := issue(t, r, "consent/before-choice|/|none|meta-pixel")
	if len(is.Evidence) != 1 || strings.Contains(is.Detail, "events listed") {
		t.Errorf("evidence %v detail %q", is.Evidence, is.Detail)
	}
}

func TestEvidenceCapped(t *testing.T) {
	run := consent.Run{Kind: consent.RunNone}
	for i := range 60 {
		run.Hits = append(run.Hits, hit(int64(i+1), int64(100*i), consent.PhaseBefore, "meta-pixel", i%2, "https://www.facebook.com/tr?id=1&ev=PageView"))
		run.Hits[i].Events = []string{"PageView"}
	}
	run.Signals = []consent.Signal{{Source: "fbq", Command: "revoke"}}
	r := result(run)
	Evaluator{}.Evaluate(r, nil)
	is := issue(t, r, "consent/before-choice|/|none|meta-pixel")
	if len(is.Evidence) != maxEvidence {
		t.Errorf("%d evidence entries, want %d", len(is.Evidence), maxEvidence)
	}
	if !strings.HasPrefix(is.Detail, "60 hits (PageView on /, /blog/, /, /blog/, /, ...)") ||
		!strings.HasSuffix(is.Detail, "; 50 of 60 events listed") {
		t.Errorf("detail %q", is.Detail)
	}
}

func TestEvidenceFromEvents(t *testing.T) {
	c := consent.Cookie{Name: "_fbp", Tracker: "meta-pixel", Need: consent.NeedNeeded, Phase: consent.PhaseBefore, AtMS: 860}
	r := result(consent.Run{Kind: consent.RunNone, Cookies: []consent.Cookie{c},
		Events: []consent.Event{{Seq: 21, AtMS: 860, Kind: consent.EvCookie, Cookie: &c}}})
	Evaluator{}.Evaluate(r, nil)
	if is := issue(t, r, "consent/before-choice|/|none|meta-pixel"); !slices.Equal(is.Evidence, []int64{21}) || is.Detail != "cookie _fbp written at 0.86s" {
		t.Errorf("%+v", is)
	}
}

func TestShortURL(t *testing.T) {
	cases := map[string]string{
		"https://www.facebook.com/tr?id=%E2%80%A2%E2%80%A2%E2%80%A2%E2%80%A25678&ev=PageView&dl=https%3A%2F%2Fshop.test%2F": "facebook.com/tr?id=••••5678&ev=PageView",
		"https://region1.google-analytics.com/g/collect?v=2&tid=G-1&gtm=45&_p=1&cid=99&en=page_view":                        "region1.google-analytics.com/g/collect?tid=G-1&en=page_view",
		"https://bat.bing.com/action/0?ti=1234&Ver=2&mid=x":                                                                 "bat.bing.com/action/0?ti=1234&Ver=2",
		"https://js.stripe.com/v3/":   "js.stripe.com/v3/",
		"not a url\x1b[31m":           `not a url\x1b[31m`,
		"https://x.test/p?a&b=1&=2&c": "x.test/p?a=&b=1",
	}
	for in, want := range cases {
		if got := shortURL(in); got != want {
			t.Errorf("shortURL(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestPlural(t *testing.T) {
	if plural(1, "cookieless ping") != "1 cookieless ping" || plural(3, "cookieless ping") != "3 cookieless pings" {
		t.Error(plural(1, "cookieless ping"), plural(3, "cookieless ping"))
	}
}

// TestIssuesWellFormed is the property every issue keeps: a check of
// the catalog, its level, the key built from its fields, a title, a
// detail, a fix and the policy.
func TestIssuesWellFormed(t *testing.T) {
	for _, name := range consenttest.Goldens {
		for _, pol := range []consent.Policy{consent.PolicyAdvanced, consent.PolicyBasic, consent.PolicyStrict} {
			r := judged(t, name, pol, "")
			for _, is := range r.Issues {
				c, ok := consent.Check(is.Check)
				where := fmt.Sprintf("%s/%s %s", name, pol, is.Key)
				switch {
				case !ok:
					t.Errorf("%s: unknown check", where)
				case is.Key == "" || is.Key != consent.IssueKey(is.Check, is.Page, is.Run, is.Tracker):
					t.Errorf("%s: key doesn't match its fields", where)
				case is.Level != c.Level:
					t.Errorf("%s: level %s", where, is.Level)
				case is.Title == "" || is.Detail == "" || is.Fix == "":
					t.Errorf("%s: empty title, detail or fix: %+v", where, is)
				case is.Policy != pol:
					t.Errorf("%s: policy %s", where, is.Policy)
				case is.Evidence == nil:
					t.Errorf("%s: nil evidence", where)
				}
			}
		}
	}
}

func TestEvaluateTwiceSame(t *testing.T) {
	r := judged(t, "meta-ignores", consent.PolicyStrict, "")
	first := keysOf(r, nil)
	Evaluator{}.Evaluate(r, consenttest.NewPlan(t))
	Evaluator{}.Evaluate(r, consenttest.NewPlan(t))
	p := consenttest.NewPlan(t)
	p.Policy = consent.PolicyStrict
	Evaluator{}.Evaluate(r, p)
	if !slices.Equal(first, keysOf(r, nil)) {
		t.Errorf("second judgement differs:\n%v\n%v", first, keysOf(r, nil))
	}
}
