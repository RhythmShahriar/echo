// Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
// SPDX-License-Identifier: MIT

package verdict

import (
	"slices"
	"strings"
	"testing"

	"github.com/RhythmShahriar/echo/consent"
	"github.com/RhythmShahriar/echo/model"
)

// v3Checks are the checks consentmode.go raises.
var v3Checks = []string{consent.CheckNoDefault, consent.CheckDefaultLate, consent.CheckV2Signals,
	consent.CheckAcceptNotGranted, consent.CheckRejectGranted}

var (
	g100 = consent.GCS{Known: true, AdStorage: consent.Denied, AnalyticsStorage: consent.Denied}
	g111 = consent.GCS{Known: true, AdStorage: consent.Granted, AnalyticsStorage: consent.Granted}
	g101 = consent.GCS{Known: true, AdStorage: consent.Denied, AnalyticsStorage: consent.Granted}
)

// ga4 is a GA4 hit; gcs "" sends none.
func ga4(seq, at int64, ph consent.Phase, gcs string, dec *consent.GCS) consent.HitRef {
	u := "https://region1.google-analytics.com/g/collect?v=2&tid=G-••••7Q2X&en=page_view"
	h := hit(seq, at, ph, "ga4", 0, u)
	if gcs != "" {
		h.URL += "&gcs=" + gcs
		d := *dec
		h.Google = &consent.GoogleConsent{GCS: gcs, GCSDecoded: &d}
	}
	return h
}

// defaultCmd is a gtag Consent Mode default with every signal denied
// (v2 drops ad_user_data and ad_personalization when false).
func defaultCmd(at int64, v2 bool) consent.Signal {
	st := map[string]consent.Value{"ad_storage": consent.Denied, "analytics_storage": consent.Denied}
	if v2 {
		st["ad_user_data"], st["ad_personalization"] = consent.Denied, consent.Denied
	}
	return consent.Signal{Source: "gtag", Command: "default", AtMS: at, State: st}
}

func update(at int64, v consent.Value, keys ...string) consent.Signal {
	st := map[string]consent.Value{}
	for _, k := range keys {
		st[k] = v
	}
	return consent.Signal{Source: "gtag", Command: "update", AtMS: at, State: st}
}

func TestNoDefault(t *testing.T) { // c01
	r := result(consent.Run{Kind: consent.RunNone, Hits: []consent.HitRef{ga4(5, 620, consent.PhaseBefore, "", nil)}})
	Evaluator{}.Evaluate(r, nil)
	sameKeys(t, keysOf(r, append(slices.Clone(v3Checks), consent.CheckBeforeChoice)),
		"consent/no-default|/|none|", "consent/before-choice|/|none|ga4")
	is := issue(t, r, "consent/no-default|/|none|")
	if is.Level != model.Error || is.Title != "Google tags ran with no Consent Mode default" ||
		is.Detail != "Google Analytics 4 sent region1.google-analytics.com/g/collect?tid=G-••••7Q2X&en=page_view at 0.62s with no gcs; no gtag('consent', 'default', ...) was seen in any run" ||
		!slices.Equal(is.Evidence, []int64{5}) {
		t.Errorf("%+v", is)
	}
	// A gcs on the hit says the tags ran with a state: no no-default.
	r = result(consent.Run{Kind: consent.RunNone, Hits: []consent.HitRef{ga4(5, 620, consent.PhaseBefore, "G100", &g100)}})
	Evaluator{}.Evaluate(r, nil)
	if keys := keysOf(r, v3Checks); len(keys) != 0 {
		t.Errorf("gcs present: %v", keys)
	}
	// A default only in another run doesn't cover the none run's hit:
	// no-default is raised and the detail names the run that had one.
	r = result(consent.Run{Kind: consent.RunNone, Hits: []consent.HitRef{ga4(5, 620, consent.PhaseBefore, "", nil)}},
		consent.Run{Kind: consent.RunAccept, Signals: []consent.Signal{defaultCmd(100, true)}})
	Evaluator{}.Evaluate(r, nil)
	sameKeys(t, keysOf(r, []string{consent.CheckNoDefault, consent.CheckDefaultLate}), "consent/no-default|/|none|")
	if is := issue(t, r, "consent/no-default|/|none|"); !strings.HasSuffix(is.Detail,
		"no gtag('consent', 'default', ...) was seen in this run (the Accept run saw one)") {
		t.Errorf("detail: %q", is.Detail)
	}
	// A default in the hit's own run is seen: nothing from no-default.
	r = result(consent.Run{Kind: consent.RunNone, Hits: []consent.HitRef{ga4(5, 620, consent.PhaseBefore, "", nil)},
		Signals: []consent.Signal{defaultCmd(100, true)}})
	Evaluator{}.Evaluate(r, nil)
	if keys := keysOf(r, []string{consent.CheckNoDefault, consent.CheckDefaultLate}); len(keys) != 0 {
		t.Errorf("default in the none run: %v", keys)
	}
}

func TestNoGoogleNoChecks(t *testing.T) { // g12, a site without Google tags
	r := result(consent.Run{Kind: consent.RunNone, Signals: []consent.Signal{defaultCmd(100, false)}})
	Evaluator{}.Evaluate(r, nil)
	if keys := keysOf(r, v3Checks); len(keys) != 0 {
		t.Errorf("no Google hits: %v", keys)
	}
	if slices.Contains(r.Notes, GCDNote) {
		t.Error("gcd note without gcd")
	}
}

func TestDefaultLate(t *testing.T) { // c05
	r := result(consent.Run{Kind: consent.RunNone,
		Hits:    []consent.HitRef{ga4(5, 620, consent.PhaseBefore, "", nil)},
		Signals: []consent.Signal{defaultCmd(900, true)},
		Events:  []consent.Event{{Seq: 6, AtMS: 900, Kind: consent.EvSignal, Signal: &consent.Signal{Source: "gtag", Command: "default", AtMS: 900}}},
	})
	Evaluator{}.Evaluate(r, nil)
	sameKeys(t, keysOf(r, v3Checks), "consent/default-late|/|none|")
	is := issue(t, r, "consent/default-late|/|none|")
	if is.Level != model.Warning || is.Title != "The Consent Mode default came 280 ms after the first Google hit" ||
		is.Detail != "first Google hit Google Analytics 4 (region1.google-analytics.com/g/collect?tid=G-••••7Q2X&en=page_view) at 0.62s; default at 0.90s" ||
		!slices.Equal(is.Evidence, []int64{5, 6}) {
		t.Errorf("%+v", is)
	}
	// At the same ms is in time.
	r.Pages[0].Runs[0].Signals[0].AtMS = 620
	Evaluator{}.Evaluate(r, nil)
	if keys := keysOf(r, v3Checks); len(keys) != 0 {
		t.Errorf("default with the hit: %v", keys)
	}
}

func TestV2Signals(t *testing.T) { // c06
	r := result(consent.Run{Kind: consent.RunNone,
		Hits:    []consent.HitRef{ga4(5, 620, consent.PhaseBefore, "G100", &g100)},
		Signals: []consent.Signal{defaultCmd(100, false)},
	})
	Evaluator{}.Evaluate(r, nil)
	sameKeys(t, keysOf(r, v3Checks), "consent/v2-signals|/|none|")
	is := issue(t, r, "consent/v2-signals|/|none|")
	if is.Level != model.Warning || is.Title != "Consent Mode v2 signals ad_user_data and ad_personalization were never set" ||
		is.Detail != "the default at 0.10s doesn't set ad_user_data and ad_personalization" {
		t.Errorf("%+v", is)
	}

	// The default is v2 but the hits' gcd says both unset.
	r = result(consent.Run{Kind: consent.RunNone,
		Hits:    []consent.HitRef{ga4(5, 620, consent.PhaseBefore, "G100", &g100)},
		Signals: []consent.Signal{defaultCmd(100, true)},
	})
	h := &r.Pages[0].Runs[0].Hits[0]
	h.Google.GCD = "13p3p3l3l5"
	h.Google.GCDDecoded = &consent.GCD{Known: true, AdUserData: consent.GCDSignal{Letter: "l", Known: true}, AdPersonalization: consent.GCDSignal{Letter: "l", Known: true}}
	Evaluator{}.Evaluate(r, nil)
	sameKeys(t, keysOf(r, v3Checks), "consent/v2-signals|/|none|")
	if is := issue(t, r, "consent/v2-signals|/|none|"); is.Detail != "gcd 13p3p3l3l5 on Google Analytics 4 at 0.62s shows ad_user_data and ad_personalization unset (l)" ||
		!slices.Equal(is.Evidence, []int64{5}) {
		t.Errorf("%+v", is)
	}
	if !slices.Contains(r.Notes, GCDNote) {
		t.Errorf("notes %q lack the gcd note", r.Notes)
	}
	// Evaluating again doesn't repeat the note.
	Evaluator{}.Evaluate(r, nil)
	n := 0
	for _, note := range r.Notes {
		if note == GCDNote {
			n++
		}
	}
	if n != 1 {
		t.Errorf("gcd note %d times", n)
	}
}

func TestAcceptNotGranted(t *testing.T) { // c16
	accept := func(hits ...consent.HitRef) *consent.Result {
		return result(consent.Run{Kind: consent.RunNone, Signals: []consent.Signal{defaultCmd(100, true)}},
			consent.Run{Kind: consent.RunAccept, Choice: &consent.Choice{Verified: true, AtMS: 2000},
				Signals: []consent.Signal{defaultCmd(100, true)}, Hits: hits})
	}
	r := accept(ga4(7, 2300, consent.PhaseAfterAccept, "G100", &g100), ga4(8, 9300, consent.PhaseAfterAccept, "G100", &g100))
	Evaluator{}.Evaluate(r, nil)
	sameKeys(t, keysOf(r, v3Checks), "consent/accept-not-granted|/|accept|")
	is := issue(t, r, "consent/accept-not-granted|/|accept|")
	if is.Level != model.Warning || is.Title != "After Accept, Google was never told storage is granted" ||
		is.Detail != "2 Google hits after Accept, the first at 2.30s, carried gcs G100; no Consent Mode update granted ad_storage or analytics_storage" ||
		!slices.Equal(is.Evidence, []int64{7, 8}) {
		t.Errorf("%+v", is)
	}

	negatives := map[string]func(r *consent.Result){
		"update granted": func(r *consent.Result) {
			run := &r.Pages[0].Runs[1]
			run.Signals = append(run.Signals, update(2010, consent.Granted, "analytics_storage"))
		},
		"choice not confirmed": func(r *consent.Result) { r.Pages[0].Runs[1].Choice.Verified = false },
		"a hit granted": func(r *consent.Result) {
			run := &r.Pages[0].Runs[1]
			run.Hits = append(run.Hits, ga4(9, 9400, consent.PhaseAfterAccept, "G111", &g111))
		},
		"a gcs Echo can't read": func(r *consent.Result) {
			run := &r.Pages[0].Runs[1]
			run.Hits = append(run.Hits, ga4(9, 9400, consent.PhaseAfterAccept, "G2xx", &consent.GCS{}))
		},
		"no hit after Accept": func(r *consent.Result) {
			r.Pages[0].Runs[1].Hits[0].Phase, r.Pages[0].Runs[1].Hits[1].Phase = consent.PhaseBefore, consent.PhaseBefore
		},
	}
	for name, change := range negatives {
		t.Run(name, func(t *testing.T) {
			r := accept(ga4(7, 2300, consent.PhaseAfterAccept, "G100", &g100), ga4(8, 9300, consent.PhaseAfterAccept, "G100", &g100))
			change(r)
			Evaluator{}.Evaluate(r, nil)
			if keys := keysOf(r, []string{consent.CheckAcceptNotGranted}); len(keys) != 0 {
				t.Errorf("raised %v", keys)
			}
		})
	}
}

// rejectRun is a page whose Reject run clicked at 2 s with a v2 default.
func rejectRun(signals []consent.Signal, hits ...consent.HitRef) *consent.Result {
	return result(consent.Run{Kind: consent.RunNone, Signals: []consent.Signal{defaultCmd(100, true)}, Hits: []consent.HitRef{ga4(2, 620, consent.PhaseBefore, "G100", &g100)}},
		consent.Run{Kind: consent.RunReject, Choice: &consent.Choice{Verified: true, AtMS: 2000},
			Signals: append([]consent.Signal{defaultCmd(100, true)}, signals...), Hits: hits})
}

func TestRejectGranted(t *testing.T) { // c04
	r := rejectRun([]consent.Signal{update(2010, consent.Granted, "analytics_storage")})
	Evaluator{}.Evaluate(r, nil)
	sameKeys(t, keysOf(r, v3Checks), "consent/reject-granted|/|reject|")
	is := issue(t, r, "consent/reject-granted|/|reject|")
	if is.Level != model.Error || is.Title != "After Reject, Google was told analytics_storage is granted" ||
		is.Detail != "gtag('consent', 'update') at 2.01s granted analytics_storage" {
		t.Errorf("%+v", is)
	}

	r = rejectRun(nil, ga4(9, 2300, consent.PhaseAfterReject, "G111", &g111))
	Evaluator{}.Evaluate(r, nil)
	sameKeys(t, keysOf(r, append(slices.Clone(v3Checks), consent.CheckAfterReject)), "consent/reject-granted|/|reject|")
	is = issue(t, r, "consent/reject-granted|/|reject|")
	if is.Title != "After Reject, Google was told ad_storage and analytics_storage are granted" ||
		is.Detail != "Google Analytics 4 hit with gcs G111 at 2.30s" || !slices.Equal(is.Evidence, []int64{9}) {
		t.Errorf("%+v", is)
	}

	dl := update(2020, consent.Granted, "ad_storage")
	dl.Source = "datalayer"
	r = rejectRun([]consent.Signal{dl}, ga4(9, 2300, consent.PhaseAfterReject, "G101", &g101), ga4(10, 9300, consent.PhaseAfterReject, "G101", &g101))
	Evaluator{}.Evaluate(r, nil)
	is = issue(t, r, "consent/reject-granted|/|reject|")
	if is.Title != "After Reject, Google was told ad_storage and analytics_storage are granted" ||
		is.Detail != "dataLayer.push(['consent', 'update']) at 2.02s granted ad_storage; 2 Google hits with a granted gcs, the first Google Analytics 4 with gcs G101 at 2.30s" {
		t.Errorf("%+v", is)
	}
}

func TestRejectGrantedNegative(t *testing.T) {
	cases := map[string]*consent.Result{
		"update denied":            rejectRun([]consent.Signal{update(2010, consent.Denied, "ad_storage", "analytics_storage")}),
		"granted before the click": rejectRun([]consent.Signal{update(1500, consent.Granted, "analytics_storage")}),
		"only ad_user_data":        rejectRun([]consent.Signal{update(2010, consent.Granted, "ad_user_data")}),
		"unknown gcs":              rejectRun(nil, ga4(9, 2300, consent.PhaseAfterReject, "G2xx", &consent.GCS{Known: false, AdStorage: consent.Granted, AnalyticsStorage: consent.Granted})),
		"denied gcs":               rejectRun(nil, ga4(9, 2300, consent.PhaseAfterReject, "G100", &g100)),
	}
	for name, r := range cases {
		t.Run(name, func(t *testing.T) {
			Evaluator{}.Evaluate(r, nil)
			if keys := keysOf(r, v3Checks); len(keys) != 0 {
				t.Errorf("raised %v", keys)
			}
		})
	}
}

func TestGoldensRaiseNoConsentModeIssue(t *testing.T) { // g01, g02 and the rest
	for _, name := range []string{"clean", "advanced", "meta-ignores", "compare-cur", "inconclusive", "no-trackers"} {
		r := judged(t, name, "", "")
		if keys := keysOf(r, v3Checks); len(keys) != 0 {
			t.Errorf("%s: %v", name, keys)
		}
	}
	r := judged(t, "advanced", "", "")
	if !slices.Contains(r.Notes, GCDNote) {
		t.Errorf("advanced: notes %q", r.Notes)
	}
}
