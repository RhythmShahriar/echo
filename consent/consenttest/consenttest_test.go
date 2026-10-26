// Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
// SPDX-License-Identifier: MIT

package consenttest

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/RhythmShahriar/echo/consent"
)

func TestGoldenResults(t *testing.T) {
	for _, name := range Goldens {
		if r := GoldenResult(name); r == nil || len(r.Pages) != 1 {
			t.Errorf("%s: %+v", name, r)
		}
	}
	if !GoldenResult("clean").Complete || GoldenResult("inconclusive").Complete || !GoldenResult("no-trackers").Complete {
		t.Error("Complete is wrong on a golden result")
	}
	a, b := GoldenResult("clean"), GoldenResult("clean")
	a.Pages[0].Runs = nil
	if len(b.Pages[0].Runs) != 3 {
		t.Error("GoldenResult handed out a shared copy")
	}
	defer func() {
		if recover() == nil {
			t.Error("an unknown golden didn't panic")
		}
	}()
	GoldenResult("nope")
}

func TestCloneResultKeepsEvents(t *testing.T) {
	r := GoldenResult("clean")
	r.Pages[0].Runs[0].Events = []consent.Event{{Seq: 1, Kind: consent.EvRequest, Request: &consent.Request{URL: "https://shop.test/"}}}
	c := CloneResult(r)
	if len(c.Pages[0].Runs[0].Events) != 1 || c.Pages[0].Runs[0].Events[0].Request.URL != "https://shop.test/" {
		t.Fatalf("events = %+v", c.Pages[0].Runs[0].Events)
	}
	c.Pages[0].Runs[0].Events[0].Request.URL = "changed"
	if r.Pages[0].Runs[0].Events[0].Request.URL != "https://shop.test/" {
		t.Fatal("the clone shares events with the original")
	}
}

func TestFakeClassifier(t *testing.T) {
	c := &FakeClassifier{}
	for url, want := range map[string]string{
		"https://region1.google-analytics.com/g/collect?v=2": "ga4",
		"https://www.facebook.com/tr?id=1":                   "meta-pixel",
		"https://www.googletagmanager.com/gtag/js?id=G-1":    "",
		"https://shop.test/collect":                          "",
		"::not a url":                                        "",
	} {
		got := ""
		if h := c.Request(&consent.Request{URL: url}, nil, nil); h != nil {
			got = h.Tracker
		}
		if got != want {
			t.Errorf("Request(%s) = %q; want %q", url, got, want)
		}
		if c.Hold(&consent.Request{URL: url}) != (want != "") {
			t.Errorf("Hold(%s) wrong", url)
		}
	}
	for name, want := range map[string]string{"_ga": "ga4", "_ga_ABC": "ga4", "_fbp": "meta-pixel", "ga_banner_closed": "", "PHPSESSID": ""} {
		got := ""
		if h := c.Cookie(&consent.Cookie{Name: name}); h != nil {
			got = h.Tracker
		}
		if got != want {
			t.Errorf("Cookie(%s) = %q; want %q", name, got, want)
		}
	}
	if c.Request(nil, nil, nil) != nil || c.Cookie(nil) != nil || c.Storage(&consent.StorageKey{Key: "_ga"}) != nil {
		t.Error("nil or storage input matched")
	}
}

func TestFakeSignals(t *testing.T) {
	var s FakeSignals
	if g := s.GCS("G101"); !g.Known || g.AdStorage != consent.Denied || g.AnalyticsStorage != consent.Granted {
		t.Errorf("GCS(G101) = %+v", g)
	}
	if g := s.GCS("G1x1"); g.Known {
		t.Errorf("GCS(G1x1) = %+v", g)
	}
	rec, _ := json.Marshal(consent.HookRecord{Src: "gtag", Args: []json.RawMessage{json.RawMessage(`"consent"`), json.RawMessage(`"default"`),
		json.RawMessage(`{"ad_storage":"denied","analytics_storage":"maybe"}`)}})
	sig, err := s.Hook(rec)
	if err != nil || sig == nil || sig.Command != "default" || sig.State["ad_storage"] != consent.Denied || sig.State["analytics_storage"] != consent.Unknown {
		t.Fatalf("Hook = %+v, %v", sig, err)
	}
	other, _ := json.Marshal(consent.HookRecord{Src: "datalayer", Args: []json.RawMessage{json.RawMessage(`{"event":"consent"}`)}})
	if sig, err := s.Hook(other); sig != nil || err != nil {
		t.Errorf("a dataLayer event read as Consent Mode: %+v, %v", sig, err)
	}
	if _, err := s.Hook(make([]byte, consent.MaxHookRecord+1)); err == nil {
		t.Error("an oversized record was read")
	}
}

func TestFakeTab(t *testing.T) {
	tab := &FakeTab{
		Isolated: map[string]Answer{"banner-visible": {Result: json.RawMessage("true")}},
		Clicks:   map[string]consent.ClickResult{"#covered": {Covered: true}},
	}
	tab.OnClick = func(ft *FakeTab, l consent.Locator) {
		if l.CSS == "#reject" {
			ft.SetCookie(consent.Cookie{Name: "CookieConsent"}, "necessary:true")
		}
	}
	got, _ := tab.EvalIsolated(t.Context(), "// banner-visible\nreturn !!document.querySelector('#b')")
	if string(got) != "true" {
		t.Errorf("named script = %s", got)
	}
	if got, _ := tab.EvalMain(t.Context(), "1+1"); string(got) != "null" {
		t.Errorf("unscripted = %s", got)
	}
	if r, _ := tab.Click(t.Context(), consent.Locator{CSS: "#covered"}); r.Clicked || !r.Covered {
		t.Errorf("scripted click = %+v", r)
	}
	tab.Advance(1500 * time.Millisecond)
	if r, _ := tab.Click(t.Context(), consent.Locator{CSS: "#reject"}); !r.Clicked || r.AtMS != 1500 {
		t.Errorf("click = %+v", r)
	}
	if v, _ := tab.CookieValue(t.Context(), "CookieConsent"); v != "necessary:true" {
		t.Errorf("cookie value = %q", v)
	}
	jar, _ := tab.Cookies(t.Context())
	if len(jar) != 1 || jar[0].Length != len("necessary:true") || len(tab.Clicked) != 2 || len(tab.Evals) != 2 {
		t.Errorf("jar %+v, clicks %d, evals %d", jar, len(tab.Clicked), len(tab.Evals))
	}
}

func TestFakeCMP(t *testing.T) {
	c := &FakeCMP{Reasons: map[consent.RunKind]consent.Reason{consent.RunReject: consent.ReasonNoButton}}
	ch, r, _ := c.Choose(t.Context(), nil, nil, consent.CMPInfo{}, consent.RunAccept)
	if ch == nil || r != consent.ReasonNone || !ch.Verified {
		t.Errorf("accept = %+v, %q", ch, r)
	}
	ch, r, _ = c.Choose(t.Context(), nil, nil, consent.CMPInfo{}, consent.RunReject)
	if ch != nil || r != consent.ReasonNoButton {
		t.Errorf("reject = %+v, %q", ch, r)
	}
	ch, _, _ = c.Find(t.Context(), nil, nil, consent.CMPInfo{}, consent.RunAccept)
	if ch == nil || ch.Verified {
		t.Errorf("find = %+v", ch)
	}
	if len(c.Calls) != 3 {
		t.Errorf("calls = %v", c.Calls)
	}
}

// Need skips while a stage is missing; with every fake in place it lets
// the test run.
func TestNeedAndUse(t *testing.T) {
	ran := false
	t.Run("missing", func(t *testing.T) {
		if consent.Linked().Comparer != nil {
			t.Skip("a comparer is linked into this build")
		}
		t.Setenv(RequireLinkedEnv, "")
		Need(t, consent.StageComparer)
		ran = true
	})
	if ran {
		t.Error("Need didn't stop a test whose stage is missing")
	}
	Use(t, Fakes())
	Need(t, consent.RunStages...)
	if err := consent.Need(consent.RunStages...); err != nil {
		t.Fatal(err)
	}
}

func TestNewPlanValidates(t *testing.T) {
	if err := NewPlan(t, "https://a.test/", "http://b.test/x").Validate(); err != nil {
		t.Fatal(err)
	}
}
