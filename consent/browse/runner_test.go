// Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
// SPDX-License-Identifier: MIT

package browse

import (
	"net/url"
	"slices"
	"testing"

	"github.com/RhythmShahriar/echo/consent"
	"github.com/RhythmShahriar/echo/consent/consenttest"
)

// TestPickLinksSkipsLogoutCart: links are taken in DOM order, on the
// same host, pages only, never twice and never one a visitor would
// regret following.
func TestPickLinksSkipsLogoutCart(t *testing.T) {
	base, _ := url.Parse("https://shop.test/start")
	links := [][2]string{
		{"/logout", "Bye"},
		{"/log-out", ""},
		{"/sign-out/", ""},
		{"/wp-login.php", ""},
		{"/wp-admin/", ""},
		{"/cart/", "Cart"},
		{"/basket", ""},
		{"/checkout?step=1", ""},
		{"/my-account/", ""},
		{"/account", ""},
		{"/?add-to-cart=12", ""},
		{"/products", "Log out"},
		{"mailto:shop@shop.test", ""},
		{"tel:+4930123", ""},
		{"javascript:void(0)", ""},
		{"/files/price.pdf", ""},
		{"/files/sheet.XLSX", ""},
		{"https://other.test/blog", ""},
		{"/start", "Home"},
		{"#top", ""},
		{"/cartography", "Maps"},
		{"/about#team", "About"},
		{"/about", "About again"},
		{"https://shop.test/blog?utm_source=x", "Blog"},
		{"/contact", "Contact"},
	}
	got := pickLinks(base, links, 3)
	want := []string{"https://shop.test/cartography", "https://shop.test/about", "https://shop.test/blog"}
	if !slices.Equal(got, want) {
		t.Errorf("pickLinks = %q; want %q", got, want)
	}
	if got := pickLinks(base, links, 0); got != nil {
		t.Errorf("--pages 0 picked %q", got)
	}
	if got := pickLinks(base, [][2]string{{"/x", "Login"}, {"/y", "Sign out"}, {"/z", "My account"}}, 5); len(got) != 0 {
		t.Errorf("links by their text: %q", got)
	}
}

// TestBotWallVendors: each vendor's challenge is named; a normal page that
// loads the same vendors' scripts, or an error page without them, is
// not a bot wall.
func TestBotWallVendors(t *testing.T) {
	for _, c := range []struct {
		name    string
		status  int
		headers map[string]string
		title   string
		srcs    []string
		want    string
	}{
		{"cf header", 403, map[string]string{"cf-mitigated": "challenge"}, "", nil, "Cloudflare"},
		{"cf title", 503, nil, "Just a moment...", nil, "Cloudflare"},
		{"cf title 200", 200, nil, "Just a moment...", nil, "Cloudflare"},
		{"cf script", 403, nil, "", []string{"https://shop.test/cdn-cgi/challenge-platform/h/b/orchestrate/jsch/v1"}, "Cloudflare"},
		{"datadome header", 403, map[string]string{"x-datadome": "protected"}, "", nil, "DataDome"},
		{"datadome frame", 403, nil, "shop.test", []string{"https://geo.captcha-delivery.com/captcha/?initialCid=x"}, "DataDome"},
		{"perimeterx title", 403, nil, "Access to this page has been denied", nil, "PerimeterX"},
		{"perimeterx script", 403, nil, "", []string{"https://client.perimeterx.net/PXabc/main.min.js"}, "PerimeterX"},
		{"akamai", 403, map[string]string{"server": "AkamaiGHost"}, "Access Denied", nil, "Akamai"},
		{"akamai challenge", 429, nil, "", []string{"https://shop.test/_sec/cp_challenge/ak-challenge-3-4.js"}, "Akamai"},
		// Negative fixtures.
		{"plain page", 200, map[string]string{"server": "cloudflare"}, "Shop", []string{"https://shop.test/cdn-cgi/challenge-platform/scripts/jsd/main.js"}, ""},
		{"turnstile form", 200, nil, "Contact", []string{"https://challenges.cloudflare.com/turnstile/v0/api.js"}, ""},
		{"datadome tag", 200, nil, "Shop", []string{"https://js.datadome.co/tags.js", "https://geo.captcha-delivery.com/x"}, ""},
		{"login wall", 401, nil, "Sign in", nil, ""},
		{"forbidden", 403, map[string]string{"server": "nginx"}, "403 Forbidden", nil, ""},
		{"akamai 200", 200, map[string]string{"server": "AkamaiGHost"}, "Access Denied", nil, ""},
		{"cf-mitigated other", 200, map[string]string{"cf-mitigated": "none"}, "Shop", nil, ""},
	} {
		if got := botWall(c.status, c.headers, c.title, c.srcs); got != c.want {
			t.Errorf("%s: botWall = %q; want %q", c.name, got, c.want)
		}
	}
}

// TestDocNavLooping: more than 10 redirects or a URL a third time is a
// loop; a URL seen twice (a cookie check) is not.
func TestDocNavLooping(t *testing.T) {
	chain := func(urls ...string) docNav { return docNav{hops: urls, nhops: len(urls)} }
	if chain("/", "/check", "/").looping() {
		t.Error("a cookie check read as a loop")
	}
	if !chain("/", "/a", "/", "/a", "/").looping() {
		t.Error("a loop wasn't seen")
	}
	var long []string
	for i := range 12 {
		long = append(long, "/r/"+string(rune('a'+i)))
	}
	if !chain(long...).looping() || chain(long[:11]...).looping() {
		t.Error("the 10 redirect limit is wrong")
	}
	d := docNav{hops: []string{"https://shop.test/a"}, nhops: 30}
	if got := redirectDetail(d, func(s string) string { return s }); got != "more than 10 redirects, last to https://shop.test/a" {
		t.Errorf("detail %q", got)
	}
}

// TestStatusFor maps every CMP reason to its run status.
func TestStatusFor(t *testing.T) {
	for r, want := range map[consent.Reason]consent.RunStatus{
		consent.ReasonNoButton:       consent.StatusNoButton,
		consent.ReasonClickMissed:    consent.StatusClickFailed,
		consent.ReasonStateUnchanged: consent.StatusClickFailed,
		consent.ReasonNavigation:     consent.StatusNavigatedAway,
		consent.ReasonLimit:          consent.StatusLimit,
		consent.ReasonCanceled:       consent.StatusCanceled,
		consent.ReasonTimeout:        consent.StatusTimeout,
		consent.ReasonNoBanner:       consent.StatusNoBanner,
		consent.ReasonNone:           consent.StatusOK,
	} {
		if got := statusFor(r); got != want {
			t.Errorf("statusFor(%q) = %s; want %s", r, got, want)
		}
	}
}

// TestHasFinding: a consent-needing hit, cookie or storage key before
// the choice or after Reject is a finding; exempt trackers and other
// phases are not.
func TestHasFinding(t *testing.T) {
	needed := consent.Hit{Tracker: "ga4", Need: consent.NeedNeeded, Kind: consent.HitBeacon}
	exempt := consent.Hit{Tracker: "plausible", Need: consent.NeedExempt, Kind: consent.HitBeacon}
	for _, c := range []struct {
		name string
		run  consent.Run
		want bool
	}{
		{"hit before", consent.Run{Hits: []consent.HitRef{{Phase: consent.PhaseBefore, Hit: needed}}}, true},
		{"hit after reject", consent.Run{Hits: []consent.HitRef{{Phase: consent.PhaseAfterReject, Hit: needed}}}, true},
		{"cookie before", consent.Run{Cookies: []consent.Cookie{{Name: "_ga", Tracker: "ga4", Need: consent.NeedNeeded, Phase: consent.PhaseBefore}}}, true},
		{"storage after reject", consent.Run{Storage: []consent.StorageKey{{Key: "k", Tracker: "x", Need: consent.NeedNeeded, Phase: consent.PhaseAfterReject}}}, true},
		{"hit after accept", consent.Run{Hits: []consent.HitRef{{Phase: consent.PhaseAfterAccept, Hit: needed}}}, false},
		{"exempt before", consent.Run{Hits: []consent.HitRef{{Phase: consent.PhaseBefore, Hit: exempt}}}, false},
		{"other cookie", consent.Run{Cookies: []consent.Cookie{{Name: "PHPSESSID", Phase: consent.PhaseBefore}}}, false},
		{"empty", consent.Run{}, false},
	} {
		if got := hasFinding(c.run); got != c.want {
			t.Errorf("%s: hasFinding = %v", c.name, got)
		}
	}
}

// TestLastOpen: the newest request without an answer is named; held,
// refused and answered ones never are.
func TestLastOpen(t *testing.T) {
	ev := func(at int64, r consent.Request) consent.Event {
		return consent.Event{Kind: consent.EvRequest, AtMS: at, Request: &r}
	}
	events := []consent.Event{
		ev(10, consent.Request{URL: "https://shop.test/a.js"}),
		ev(40, consent.Request{URL: "https://shop.test/b.js"}),
		ev(30, consent.Request{URL: "https://shop.test/c.js"}),
		ev(50, consent.Request{URL: "https://shop.test/ok.js", Status: 200}),
		ev(60, consent.Request{URL: "https://t.test/g/collect", Held: true}),
		ev(70, consent.Request{URL: "https://x.test/", Refused: "not mapped"}),
		{Kind: consent.EvNote, AtMS: 80, Note: "n"},
	}
	if got := lastOpen(events); got != "https://shop.test/b.js" {
		t.Errorf("lastOpen = %q", got)
	}
	if got := lastOpen(events[3:]); got != "" {
		t.Errorf("lastOpen with none open = %q", got)
	}
}

// TestFatalText writes each fatal error as the run's detail.
func TestFatalText(t *testing.T) {
	for _, c := range []struct {
		f    consent.FatalError
		want string
	}{
		{consent.FatalError{Code: consent.FatalUnreachable, URL: "https://shop.test/", Detail: "dial tcp: connection refused"}, "https://shop.test/ didn't answer: dial tcp: connection refused"},
		{consent.FatalError{Code: consent.FatalHTTPStatus, URL: "https://shop.test/", Status: 403}, "https://shop.test/ answered 403 Forbidden"},
		{consent.FatalError{Code: consent.FatalBotWall, URL: "https://shop.test/", Detail: "DataDome"}, "https://shop.test/ showed a bot check (DataDome) instead of the page"},
		{consent.FatalError{Code: consent.FatalRedirectHost, URL: "https://shop.test/", To: "https://shop.example/"}, "https://shop.test/ redirects to https://shop.example/"},
		{consent.FatalError{Code: consent.FatalTLS, URL: "https://shop.test/", Detail: "net::ERR_CERT_AUTHORITY_INVALID"}, "https://shop.test/ uses a certificate this machine doesn't trust (net::ERR_CERT_AUTHORITY_INVALID)"},
		{consent.FatalError{Code: consent.FatalRedirectLoop, URL: "https://shop.test/", Detail: "a redirect loop"}, "https://shop.test/: a redirect loop"},
		{consent.FatalError{Code: consent.FatalWrite, Detail: "disk full"}, "write: disk full"},
	} {
		if got := fatalText(&c.f); got != c.want {
			t.Errorf("fatalText(%s) = %q; want %q", c.f.Code, got, c.want)
		}
	}
}

// TestFillSortsEvents: request hits go to Hits, tracker scripts to
// Scripts; cookies, storage keys and signals go to their lists.
func TestFillSortsEvents(t *testing.T) {
	rec := &Recording{Truncated: true, Dropped: 2, Events: []consent.Event{
		{Seq: 1, Kind: consent.EvRequest, AtMS: 5, Request: &consent.Request{URL: "https://gtm.test/gtm.js"}, Hit: &consent.Hit{Tracker: "gtm", Kind: consent.HitScript}},
		{Seq: 2, Kind: consent.EvRequest, AtMS: 6, Phase: consent.PhaseBefore, Visit: 1, Request: &consent.Request{URL: "https://t.test/g/collect", Held: true}, Hit: &consent.Hit{Tracker: "ga4", Kind: consent.HitBeacon}},
		{Seq: 3, Kind: consent.EvRequest, Request: &consent.Request{URL: "https://shop.test/"}},
		{Seq: 4, Kind: consent.EvCookie, Cookie: &consent.Cookie{Name: "_ga"}},
		{Seq: 5, Kind: consent.EvStorage, Storage: &consent.StorageKey{Key: "k"}},
		{Seq: 6, Kind: consent.EvSignal, Signal: &consent.Signal{Source: "gtag", Command: "default"}},
	}}
	r := newRun(consent.RunNone)
	fill(&r, rec)
	if len(r.Scripts) != 1 || r.Scripts[0].Seq != 1 || len(r.Hits) != 1 || !r.Hits[0].Held || r.Hits[0].Visit != 1 || r.Hits[0].Tracker != "ga4" {
		t.Errorf("scripts %+v hits %+v", r.Scripts, r.Hits)
	}
	if len(r.Cookies) != 1 || len(r.Storage) != 1 || len(r.Signals) != 1 || !r.Truncated || r.Dropped != 2 || len(r.Events) != 6 {
		t.Errorf("run %+v", r)
	}
}

// TestPageNumberAfterEarlierPlans: --sites runs one plan per group of
// sites into one evidence folder, so a later plan's first page numbers
// on from the earlier ones' and never overwrites their files.
func TestPageNumberAfterEarlierPlans(t *testing.T) {
	c := newCheck(nil, &consent.Plan{}, consent.Stages{})
	if got := c.pageNumber(0); got != 1 {
		t.Errorf("first plan, first page: %d, want 1", got)
	}
	c = newCheck(nil, &consent.Plan{PageBase: 3}, consent.Stages{})
	if got := c.pageNumber(1); got != 5 {
		t.Errorf("after 3 pages, second page: %d, want 5", got)
	}
	if eventsPath(c.pageNumber(0), consent.RunNone) != "events/page-4-none.ndjson" {
		t.Errorf("events file %q", eventsPath(c.pageNumber(0), consent.RunNone))
	}
}

// customCatalog is a catalog with one tracker from the config on top of
// its built-in ones.
type customCatalog struct{ consenttest.FakeClassifier }

func (c *customCatalog) Trackers() []consent.TrackerInfo {
	return append(c.FakeClassifier.Trackers(), consent.TrackerInfo{ID: "acme", Sources: []string{"config"}})
}

// The result's catalog keeps Info's version and hash and counts the
// config's own trackers too.
func TestCatalogInfoCountsCustom(t *testing.T) {
	plain := &consenttest.FakeClassifier{}
	if got := catalogInfo(plain); got != plain.Info() {
		t.Errorf("built-in only: %+v, want %+v", got, plain.Info())
	}
	got := catalogInfo(&customCatalog{})
	if want := plain.Info(); got.Count != want.Count+1 || got.SHA256 != want.SHA256 || got.Version != want.Version {
		t.Errorf("with a custom tracker: %+v", got)
	}
}
