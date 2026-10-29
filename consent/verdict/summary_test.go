// Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
// SPDX-License-Identifier: MIT

package verdict

import (
	"reflect"
	"testing"

	"github.com/RhythmShahriar/echo/consent"
	"github.com/RhythmShahriar/echo/consent/consenttest"
)

// judged loads a golden result, clears what the verdict fills and
// judges it again under the CLI's default plan with policy and geo.
func judged(t *testing.T, name string, policy consent.Policy, geo string) *consent.Result {
	t.Helper()
	r := consenttest.GoldenResult(name)
	for i := range r.Pages {
		r.Pages[i].ConsentMode, r.Pages[i].NeedsNone = nil, false
	}
	r.Issues, r.Inconclusive = nil, nil
	p := consenttest.NewPlan(t)
	if policy != "" {
		p.Policy = policy
	}
	p.Geo = geo
	Evaluator{}.Evaluate(r, p)
	return r
}

func TestRegistered(t *testing.T) {
	if _, ok := consent.Linked().Evaluator.(Evaluator); !ok {
		t.Fatalf("Evaluator not registered: %T", consent.Linked().Evaluator)
	}
}

func TestConsentModeMatchesGoldens(t *testing.T) {
	for _, name := range []string{"clean", "meta-ignores", "advanced", "inconclusive", "no-trackers", "compare-cur"} {
		want := consenttest.GoldenResult(name)
		got := judged(t, name, "", "")
		for i := range want.Pages {
			if !reflect.DeepEqual(got.Pages[i].ConsentMode, want.Pages[i].ConsentMode) {
				t.Errorf("%s page %d: consent mode\n got %+v\nwant %+v", name, i, got.Pages[i].ConsentMode, want.Pages[i].ConsentMode)
			}
			if got.Pages[i].NeedsNone != want.Pages[i].NeedsNone {
				t.Errorf("%s page %d: NeedsNone %v, want %v", name, i, got.Pages[i].NeedsNone, want.Pages[i].NeedsNone)
			}
		}
	}
}

func TestNeedsNone(t *testing.T) {
	r := consenttest.GoldenResult("no-trackers")
	// As browse leaves it: no banner, so Accept and Reject are no_banner.
	for i := range r.Pages[0].Runs {
		run := &r.Pages[0].Runs[i]
		if run.Kind != consent.RunNone {
			run.Reason, run.Detail = consent.ReasonNoBanner, "Accept: no banner showed within 10s"
		}
	}
	Evaluator{}.Evaluate(r, consenttest.NewPlan(t))
	pg := r.Pages[0]
	if !pg.NeedsNone {
		t.Fatal("no-trackers: NeedsNone false")
	}
	for _, run := range pg.Runs {
		if run.Inconclusive() {
			t.Errorf("%s: still inconclusive (%s)", run.Kind, run.Reason)
		}
		if run.Kind != consent.RunNone && run.Detail != NothingToConsent {
			t.Errorf("%s: detail %q", run.Kind, run.Detail)
		}
	}
	if len(r.Inconclusive) != 0 {
		t.Errorf("inconclusive: %+v", r.Inconclusive)
	}
}

func TestNeedsNoneNegative(t *testing.T) {
	cases := map[string]func(pg *consent.Page){
		"cmp present": func(pg *consent.Page) { pg.CMP.Present = true },
		"banner on a visit": func(pg *consent.Page) {
			pg.Runs[0].Visits[1].Banner = true
		},
		"tracker cookie": func(pg *consent.Page) {
			pg.Runs[0].Cookies = append(pg.Runs[0].Cookies, consent.Cookie{Name: "_ga", Tracker: "ga4", Need: consent.NeedNeeded, Phase: consent.PhaseBefore})
		},
		"tracker storage key": func(pg *consent.Page) {
			pg.Runs[0].Storage = append(pg.Runs[0].Storage, consent.StorageKey{Key: "_hjSession", Tracker: "hotjar", Need: consent.NeedNeeded, Phase: consent.PhaseBefore})
		},
		"tracker script": func(pg *consent.Page) {
			pg.Runs[0].Scripts = append(pg.Runs[0].Scripts, consent.Script{Tracker: "meta-pixel", Phase: consent.PhaseBefore})
		},
		"none run stopped at the request limit": func(pg *consent.Page) {
			pg.Runs[0].Status, pg.Runs[0].Reason = consent.StatusLimit, consent.ReasonLimit
		},
		"none run timed out": func(pg *consent.Page) {
			pg.Runs[0].Status, pg.Runs[0].Reason = consent.StatusTimeout, consent.ReasonTimeout
		},
		"none run cut short": func(pg *consent.Page) { pg.Runs[0].Truncated = true },
		"no none run and one stopped at the limit": func(pg *consent.Page) {
			pg.Runs = pg.Runs[1:]
			pg.Runs[0].Status, pg.Runs[0].Reason = consent.StatusLimit, consent.ReasonLimit
		},
		"tracker hit": func(pg *consent.Page) {
			pg.Runs[0].Hits = append(pg.Runs[0].Hits, consent.HitRef{Phase: consent.PhaseBefore,
				Hit: consent.Hit{Tracker: "tiktok-pixel", Need: consent.NeedNeeded, Kind: consent.HitBeacon}})
		},
	}
	for name, change := range cases {
		t.Run(name, func(t *testing.T) {
			r := consenttest.GoldenResult("no-trackers")
			for i := range r.Pages[0].Runs[1:] {
				run := &r.Pages[0].Runs[i+1]
				run.Reason = consent.ReasonNoBanner
			}
			change(&r.Pages[0])
			Evaluator{}.Evaluate(r, consenttest.NewPlan(t))
			if r.Pages[0].NeedsNone {
				t.Fatal("NeedsNone true")
			}
			for _, run := range r.Pages[0].Runs[1:] {
				if run.Reason != consent.ReasonNoBanner {
					t.Errorf("%s: reason %q, want no_banner kept", run.Kind, run.Reason)
				}
			}
		})
	}
}

func TestNeedsNoneExempt(t *testing.T) {
	r := consenttest.GoldenResult("no-trackers")
	r.Pages[0].Runs[0].Hits = append(r.Pages[0].Runs[0].Hits, consent.HitRef{Phase: consent.PhaseBefore,
		Hit: consent.Hit{Tracker: "plausible", Need: consent.NeedExempt, Kind: consent.HitBeacon}})
	r.Pages[0].Runs[0].Cookies = append(r.Pages[0].Runs[0].Cookies, consent.Cookie{Name: "ajs_anonymous_id", Tracker: "segment", Need: consent.NeedNeeded, Phase: consent.PhaseBefore})
	p := consenttest.NewPlan(t)
	p.Exempt = []string{"segment"}
	Evaluator{}.Evaluate(r, p)
	if !r.Pages[0].NeedsNone {
		t.Fatal("exempt trackers made the page need consent")
	}
}

func TestInconclusiveKeepsNoBanner(t *testing.T) {
	r := judged(t, "inconclusive", "", "")
	pg := r.Pages[0]
	if pg.NeedsNone {
		t.Fatal("a page with Cookiebot's script needs no consent")
	}
	n := 0
	for _, run := range pg.Runs {
		if run.Reason == consent.ReasonNoBanner {
			n++
		}
	}
	if n != 2 {
		t.Errorf("%d no_banner runs, want 2", n)
	}
}

func TestCookielessGolden(t *testing.T) {
	r := consenttest.GoldenResult("advanced")
	for i := range r.Pages[0].Runs {
		for k := range r.Pages[0].Runs[i].Hits {
			r.Pages[0].Runs[i].Hits[k].Cookieless = false
		}
	}
	Evaluator{}.Evaluate(r, consenttest.NewPlan(t))
	for _, run := range r.Pages[0].Runs {
		for _, h := range run.Hits {
			want := run.Kind == consent.RunNone
			if h.Cookieless != want {
				t.Errorf("%s hit %d (%s): cookieless %v, want %v", run.Kind, h.Seq, h.Tracker, h.Cookieless, want)
			}
		}
	}
}

// pingRun is a none run with one GA4 hit sent with gcs G100 at 620 ms.
func pingRun(gcs consent.GCS) consent.Run {
	return consent.Run{Kind: consent.RunNone, Status: consent.StatusOK,
		Visits: []consent.Visit{{URL: "https://shop.test/", Step: "load"}},
		Hits: []consent.HitRef{{Seq: 7, AtMS: 620, Phase: consent.PhaseBefore, URL: "https://region1.google-analytics.com/g/collect?v=2&tid=G-••••7Q2X&gcs=G100",
			Hit: consent.Hit{Tracker: "ga4", Name: "GA4", Category: consent.CatAnalytics, Need: consent.NeedNeeded, Kind: consent.HitBeacon,
				Google: &consent.GoogleConsent{GCS: "G100", GCSDecoded: &gcs}}}},
		Events: []consent.Event{{Seq: 7, AtMS: 620, Kind: consent.EvRequest, Hit: &consent.Hit{Tracker: "ga4"}}},
	}
}

func TestCookielessCookieWindow(t *testing.T) {
	denied := consent.GCS{Known: true, AdStorage: consent.Denied, AnalyticsStorage: consent.Denied}
	cases := []struct {
		name    string
		gcs     consent.GCS
		cookies []consent.Cookie
		want    bool
	}{
		{"no cookie", denied, nil, true},
		{"_ga 200 ms later", denied, []consent.Cookie{{Name: "_ga", Phase: consent.PhaseBefore, AtMS: 820}}, false},
		{"_ga_ABC before", denied, []consent.Cookie{{Name: "_ga_ABC123", Phase: consent.PhaseBefore, AtMS: 100}}, false},
		{"_gcl_au at 1 s", denied, []consent.Cookie{{Name: "_gcl_au", Phase: consent.PhaseBefore, AtMS: 1620}}, false},
		{"FPID", denied, []consent.Cookie{{Name: "FPID", Phase: consent.PhaseBefore, AtMS: 700}}, false},
		{"_ga after 1 s", denied, []consent.Cookie{{Name: "_ga", Phase: consent.PhaseBefore, AtMS: 1621}}, true},
		{"_ga in another phase", denied, []consent.Cookie{{Name: "_ga", Phase: consent.PhaseAfterAccept, AtMS: 700}}, true},
		{"_ga refused", denied, []consent.Cookie{{Name: "_ga", Phase: consent.PhaseBefore, AtMS: 700, Blocked: "ThirdPartyPhaseout"}}, true},
		{"site cookie", denied, []consent.Cookie{{Name: "ga_banner_closed", Phase: consent.PhaseBefore, AtMS: 700}}, true},
		{"gcs G110", consent.GCS{Known: true, AdStorage: consent.Granted, AnalyticsStorage: consent.Denied}, nil, false},
		{"gcs unknown", consent.GCS{Known: false, AdStorage: consent.Denied, AnalyticsStorage: consent.Denied}, nil, false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			run := pingRun(c.gcs)
			run.Cookies = c.cookies
			r := &consent.Result{Policy: consent.PolicyAdvanced, Pages: []consent.Page{{URL: "https://shop.test/", Runs: []consent.Run{run}}}}
			Evaluator{}.Evaluate(r, consenttest.NewPlan(t))
			got := r.Pages[0].Runs[0]
			if got.Hits[0].Cookieless != c.want {
				t.Errorf("cookieless %v, want %v", got.Hits[0].Cookieless, c.want)
			}
			if got.Events[0].Hit.Cookieless != c.want {
				t.Errorf("event cookieless %v, want %v", got.Events[0].Hit.Cookieless, c.want)
			}
		})
	}
}

func TestCookielessOnlyGoogle(t *testing.T) {
	run := pingRun(consent.GCS{Known: true, AdStorage: consent.Denied, AnalyticsStorage: consent.Denied})
	run.Hits[0].Tracker = "meta-pixel"
	r := &consent.Result{Pages: []consent.Page{{URL: "https://shop.test/", Runs: []consent.Run{run}}}}
	Evaluator{}.Evaluate(r, nil)
	if r.Pages[0].Runs[0].Hits[0].Cookieless {
		t.Error("a Meta hit marked cookieless")
	}
}

func TestRegionDefault(t *testing.T) {
	signals := []consent.Signal{
		{Source: "gtag", Command: "default", AtMS: 100, Region: []string{"DE", "FR"}, WaitForUpdateMS: 500,
			State: map[string]consent.Value{"ad_storage": "denied", "analytics_storage": "denied", "ad_user_data": "denied", "ad_personalization": "denied"}},
		{Source: "gtag", Command: "default", AtMS: 110,
			State: map[string]consent.Value{"ad_storage": "granted", "analytics_storage": "granted"}},
	}
	cases := []struct {
		geo     string
		at      int64
		v2      bool
		region  []string
		storage consent.Value
	}{
		{"de", 100, true, []string{"DE", "FR"}, consent.Denied},
		{"us", 110, false, nil, consent.Granted},
		{"", 110, false, nil, consent.Granted},
	}
	for _, c := range cases {
		t.Run("geo "+c.geo, func(t *testing.T) {
			run := consent.Run{Kind: consent.RunNone, Signals: signals}
			r := &consent.Result{Pages: []consent.Page{{URL: "https://shop.test/", Runs: []consent.Run{run}}}}
			p := consenttest.NewPlan(t)
			p.Geo = c.geo
			Evaluator{}.Evaluate(r, p)
			cm := r.Pages[0].ConsentMode
			if !cm.Present || cm.DefaultAtMS != c.at || cm.V2 != c.v2 || !reflect.DeepEqual(cm.Region, c.region) || cm.Default["ad_storage"] != c.storage {
				t.Errorf("got %+v", cm)
			}
			if !cm.DefaultBeforeFirstHit {
				t.Error("no Google hit: the default is before it")
			}
		})
	}
}

func TestConsentModeFromHitsOnly(t *testing.T) {
	run := pingRun(consent.GCS{Known: true, AdStorage: consent.Denied, AnalyticsStorage: consent.Denied})
	run.Kind = consent.RunAccept // no none run: the first run with Consent Mode is read
	r := &consent.Result{Pages: []consent.Page{{URL: "https://shop.test/", Runs: []consent.Run{{Kind: consent.RunReject}, run}}}}
	Evaluator{}.Evaluate(r, nil)
	cm := r.Pages[0].ConsentMode
	if !cm.Present || cm.FirstGoogleHitMS != 620 || cm.DefaultBeforeFirstHit || cm.V2 {
		t.Errorf("got %+v", cm)
	}
}

func TestEvaluateNil(t *testing.T) {
	Evaluator{}.Evaluate(nil, nil)
	r := &consent.Result{}
	Evaluator{}.Evaluate(r, nil)
	if r.Issues == nil || r.Inconclusive == nil {
		t.Error("Issues and Inconclusive should be empty, not nil")
	}
}

// TestRefusedTrackerScriptsAreInconclusive: a banner page whose tracker
// scripts --map-only refused never ran them, so no run may pass as
// "nothing fired": every run is inconclusive and names the trackers,
// and the check ends Couldn't check (exit 3), not All clear.
func TestRefusedTrackerScriptsAreInconclusive(t *testing.T) {
	r := judged(t, "map-refused", "", "")
	// The catalog isn't linked here, so trackers go by their ids.
	want := "the tracker scripts of google-tag-manager and meta-pixel were refused by --map-only, " +
		"so they never ran and weren't checked; drop --map-only to let them load"
	for _, run := range r.Pages[0].Runs {
		if run.Reason != consent.ReasonRefused || run.Detail != run.Kind.Label()+": "+want {
			t.Errorf("%s: reason %q detail %q", run.Kind, run.Reason, run.Detail)
		}
	}
	if len(r.Inconclusive) != 3 || r.Inconclusive[1].Detail != "Accept: "+want {
		t.Errorf("inconclusive %+v", r.Inconclusive)
	}
	if h := r.Head(""); h != consent.HeadInconclusive || r.ExitCode("") != 3 {
		t.Errorf("head %q exit %d; want Couldn't check, 3", h, r.ExitCode(""))
	}

	// Exempt trackers don't count; with both exempt the runs pass.
	r = consenttest.GoldenResult("map-refused")
	p := consenttest.NewPlan(t)
	p.Exempt = []string{"google-tag-manager", "meta-pixel"}
	Evaluator{}.Evaluate(r, p)
	for _, run := range r.Pages[0].Runs {
		if run.Reason != consent.ReasonNone {
			t.Errorf("exempt %s: reason %q", run.Kind, run.Reason)
		}
	}

	// One refused script names one tracker; a fetched one is checked.
	r = consenttest.GoldenResult("map-refused")
	for i := range r.Pages[0].Runs {
		r.Pages[0].Runs[i].Scripts[1].Refused = ""
	}
	Evaluator{}.Evaluate(r, consenttest.NewPlan(t))
	if d := r.Pages[0].Runs[0].Detail; d != "No choice: the tracker script of google-tag-manager was refused by --map-only, "+
		"so it never ran and wasn't checked; drop --map-only to let it load" {
		t.Errorf("one refused: %q", d)
	}
	for i := range r.Pages[0].Runs {
		r.Pages[0].Runs[i].Scripts[0].Refused = ""
	}
	Evaluator{}.Evaluate(r, consenttest.NewPlan(t))
	for _, run := range r.Pages[0].Runs {
		if run.Reason == consent.ReasonRefused {
			t.Errorf("nothing refused %s: still %q", run.Kind, run.Reason)
		}
	}
}
