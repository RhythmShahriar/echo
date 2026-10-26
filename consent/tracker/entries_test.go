// Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
// SPDX-License-Identifier: MIT

package tracker

import (
	"net/url"
	"slices"
	"testing"

	"github.com/RhythmShahriar/echo/consent"
)

var shop, _ = url.Parse("https://shop.test/")

// get is a GET request of the given type.
func get(u, typ string) *consent.Request { return &consent.Request{Method: "GET", URL: u, Type: typ} }

// TestCatalogFixtures runs every fixture of every entry against the
// whole catalog, raw and masked as the recorder hands URLs over: a
// match must come back as this entry and kind, so no other entry's
// pattern steals it.
func TestCatalogFixtures(t *testing.T) {
	for _, e := range builtin.entries {
		id := e.info.ID
		ff := readFixtures(t, id)
		var spec entrySpec
		for _, r := range e.rules {
			switch r.kind {
			case consent.HitBeacon:
				spec.Hits = append(spec.Hits, ruleSpec{})
			case consent.HitScript:
				spec.Scripts = append(spec.Scripts, ruleSpec{})
			case consent.HitFrame:
				spec.Frames = append(spec.Frames, ruleSpec{})
			}
		}
		for _, k := range ff.kinds(&spec) {
			for _, f := range k.fix.Match {
				for _, u := range []string{f.URL, consent.MaskURL(f.URL, false)} {
					r := f.request()
					r.URL = u
					h := builtin.Request(r, f.body(), f.site())
					if h == nil || h.Tracker != id || h.Kind != k.kind {
						t.Errorf("%s %s.match %s: got %+v", id, k.key, u, h)
					}
				}
			}
			for _, f := range k.fix.NoMatch {
				if h := builtin.Request(f.request(), f.body(), f.site()); h != nil && h.Tracker == id && h.Kind == k.kind {
					t.Errorf("%s %s.no_match %s matched", id, k.key, f.URL)
				}
			}
		}
		for _, n := range ff.Cookies.Match {
			if h := builtin.Cookie(&consent.Cookie{Name: n.Name, Domain: n.Domain}); h == nil || h.Tracker != id {
				t.Errorf("%s cookies.match %s: got %+v", id, n.Name, h)
			}
		}
		for _, n := range ff.Cookies.NoMatch {
			if h := builtin.Cookie(&consent.Cookie{Name: n.Name, Domain: n.Domain}); h != nil && h.Tracker == id {
				t.Errorf("%s cookies.no_match %s matched", id, n.Name)
			}
		}
		for _, n := range ff.Storage.Match {
			if h := builtin.Storage(&consent.StorageKey{Key: n.Name}); h == nil || h.Tracker != id {
				t.Errorf("%s storage.match %s: got %+v", id, n.Name, h)
			}
		}
		for _, n := range ff.Storage.NoMatch {
			if h := builtin.Storage(&consent.StorageKey{Key: n.Name}); h != nil && h.Tracker == id {
				t.Errorf("%s storage.no_match %s matched", id, n.Name)
			}
		}
	}
}

// TestFixtureCoverage holds every entry to the T3 bar: two hit
// fixtures, a lookalike on the site's own host, a script fixture and a
// cookie fixture for every cookie and storage pattern it lists.
func TestFixtureCoverage(t *testing.T) {
	for _, e := range builtin.entries {
		id := e.info.ID
		ff := readFixtures(t, id)
		one, err := newCatalog([]*entry{e})
		if err != nil {
			t.Fatal(err)
		}
		if slices.ContainsFunc(e.rules, func(r *rule) bool { return r.kind == consent.HitBeacon }) {
			if len(ff.Hits.Match) < 2 {
				t.Errorf("%s: %d hits.match fixtures, want at least 2", id, len(ff.Hits.Match))
			}
			if !slices.ContainsFunc(ff.Hits.NoMatch, func(f reqFixture) bool {
				u, _ := url.Parse(f.URL)
				return u.Hostname() == "shop.test"
			}) {
				t.Errorf("%s: no hits.no_match lookalike on shop.test", id)
			}
		}
		for _, g := range []struct {
			key   string
			pats  []nameSpec
			fixes []nameSpec
			names *names
		}{{"cookies", e.cookies, ff.Cookies.Match, &one.cookies}, {"storage", e.storage, ff.Storage.Match, &one.storage}} {
			for _, p := range g.pats {
				covered := slices.ContainsFunc(g.fixes, func(n nameSpec) bool {
					probe := newNames()
					_ = probe.add(e, p)
					return probe.find(n.Name, n.Domain) != nil
				})
				if !covered {
					t.Errorf("%s: no %s.match fixture for %q", id, g.key, p.Name)
				}
			}
		}
	}
}

func TestScriptNeverHeld(t *testing.T) {
	for _, e := range builtin.entries {
		ff := readFixtures(t, e.info.ID)
		for _, f := range ff.Scripts.Match {
			for _, typ := range []string{"", "script", "other"} {
				if builtin.Hold(get(f.URL, typ)) {
					t.Errorf("%s: script %s (type %q) is held", e.info.ID, f.URL, typ)
				}
			}
		}
		for _, f := range ff.Hits.Match {
			r := f.request()
			// A necessary service is never held; every other hit is.
			if held := builtin.Hold(r); f.Body == "" && held != (e.info.Need != consent.NeedNo) {
				t.Errorf("%s: hit %s held %v, need %s", e.info.ID, f.URL, held, e.info.Need)
			}
			r.Type = "script"
			if builtin.Hold(r) {
				t.Errorf("%s: hit %s loaded as a script is held", e.info.ID, f.URL)
			}
			r.Type = "document"
			if builtin.Hold(r) {
				t.Errorf("%s: hit %s loaded as a document is held", e.info.ID, f.URL)
			}
		}
	}
	if builtin.Hold(nil) || builtin.Hold(get("https://shop.test/", "")) {
		t.Fatal("held a request that isn't a hit")
	}
}

func TestGoogleHits(t *testing.T) {
	h := builtin.Request(get("https://www.google-analytics.com/g/collect?v=2&tid=G-TESTBED001&en=page_view&gcs=G100&gcd=13p3p3p3p5&npa=1&dma=1&cid=1.2", "ping"), nil, shop)
	if h == nil || h.Tracker != "ga4" || h.Kind != consent.HitBeacon || h.Need != consent.NeedNeeded || h.Category != consent.CatAnalytics {
		t.Fatalf("ga4 hit = %+v", h)
	}
	if h.ID != "••••D001" || !slices.Equal(h.Events, []string{"page_view"}) || h.FirstParty {
		t.Errorf("ga4 ID %q, events %v, first party %v", h.ID, h.Events, h.FirstParty)
	}
	want := consent.GoogleConsent{GCS: "G100", GCD: "13p3p3p3p5", NPA: "1", DMA: "1"}
	if h.Google == nil || *h.Google != want {
		t.Errorf("ga4 Google = %+v, want %+v", h.Google, want)
	}
	if h.Params["gcs"] != "G100" || h.Params["tid"] != "••••D001" || h.Params["en"] != "page_view" || h.Params["cid"] != "" {
		t.Errorf("ga4 Params = %v", h.Params)
	}
	// The same hit through a server-side container on the site's host.
	h = builtin.Request(get("https://metrics.shop.test/g/collect?v=2&tid=G-TESTBED001", ""), nil, shop)
	if h == nil || h.Tracker != "ga4" || !h.FirstParty {
		t.Errorf("first-party ga4 = %+v", h)
	}
	// A POST batch: shared parameters in the query, one event per line.
	h = builtin.Request(&consent.Request{Method: "POST", URL: "https://region1.google-analytics.com/g/collect?v=2&tid=G-TESTBED001"},
		[]byte("en=page_view\nen=scroll"), shop)
	if h == nil || !slices.Equal(h.Events, []string{"page_view", "scroll"}) {
		t.Errorf("ga4 batch = %+v", h)
	}
	h = builtin.Request(get("https://googleads.g.doubleclick.net/pagead/viewthroughconversion/123456789/?random=1&gcs=G111", "image"), nil, shop)
	if h == nil || h.Tracker != "google-ads" || h.ID != "••••6789" || h.Google == nil || h.Google.GCS != "G111" {
		t.Errorf("google-ads = %+v", h)
	}
	h = builtin.Request(get("https://www.googletagmanager.com/gtag/js?id=G-TESTBED001", "script"), nil, shop)
	if h == nil || h.Tracker != "google-tag-manager" || h.Kind != consent.HitScript || h.Google != nil || h.Params != nil {
		t.Errorf("gtag.js = %+v", h)
	}
	for _, u := range []string{"https://shop.test/g/collect?q=shoes", "https://shop.test/api/collect", "https://shop.test/collect?v=2&tid=G-TESTBED001"} {
		if h := builtin.Request(get(u, ""), nil, shop); h != nil {
			t.Errorf("%s = %+v, want no tracker", u, h)
		}
	}
	for _, c := range []struct{ name, id string }{{"_ga", "ga4"}, {"_ga_TESTBED001", "ga4"}, {"_gcl_au", "google-ads"}, {"_gid", "universal-analytics"}} {
		if h := builtin.Cookie(&consent.Cookie{Name: c.name, Domain: ".shop.test"}); h == nil || h.Tracker != c.id {
			t.Errorf("cookie %s = %+v, want %s", c.name, h, c.id)
		}
	}
	for _, n := range []string{"ga_banner_closed", "_gallery"} {
		if h := builtin.Cookie(&consent.Cookie{Name: n}); h != nil {
			t.Errorf("cookie %s = %+v, want none", n, h)
		}
	}
}

func TestHostNormalized(t *testing.T) {
	for _, u := range []string{
		"https://WWW.Google-Analytics.COM/g/collect?v=2&tid=G-TESTBED001",
		"https://www.google-analytics.com./g/collect?v=2&tid=G-TESTBED001",
		"https://www.google-analytics.com:443/g/collect?v=2&tid=G-TESTBED001",
	} {
		if h := builtin.Request(get(u, ""), nil, nil); h == nil || h.Tracker != "ga4" || h.FirstParty {
			t.Errorf("%s = %+v", u, h)
		}
	}
}
