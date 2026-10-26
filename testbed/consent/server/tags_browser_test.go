// Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
// SPDX-License-Identifier: MIT

//go:build browser

package server

import (
	"encoding/json"
	"strings"
	"testing"
	"time"
)

const tagSite = "https://tagtest.shop.test"

func ga4Hit(h Hit) bool { return strings.HasSuffix(h.Path, "/g/collect") }

func TestGoogleStandInDefaultDeniedThenGranted(t *testing.T) {
	l := startLive(t, testRoot(t))
	p := l.open(tagSite + "/denied/")
	if !l.waitTrue(p, `() => !!(window.__tb_loaded && window.__tb_loaded.gtag)`, 10*time.Second) {
		t.Fatal("the gtag stand-in didn't load")
	}
	if got := l.waitHits("tagtest", 1, func(Hit) bool { return true }, 1500*time.Millisecond); len(got) != 0 {
		t.Fatalf("hits while denied in basic mode: %+v", got)
	}
	if c := l.eval(p, `() => document.cookie`); strings.Contains(c, "_ga") || strings.Contains(c, "_gcl_au") {
		t.Errorf("cookies while denied: %q", c)
	}
	l.eval(p, `() => { gtag('event', 'add_to_cart', {value: 3}); gtag('consent', 'update', {ad_storage: 'granted', analytics_storage: 'granted', ad_user_data: 'granted', ad_personalization: 'granted'}); }`)
	hits := l.waitHits("tagtest", 1, ga4Hit, 10*time.Second)
	if len(hits) != 1 {
		t.Fatalf("GA4 hits after the update: %+v", hits)
	}
	h := hits[0]
	// Two queued events go out as one POST body.
	if h.Method != "POST" || h.Body != "en=page_view\nen=add_to_cart&ep.value=3" {
		t.Errorf("batch = %s %q", h.Method, h.Body)
	}
	if param(h, "gcs") != "G111" || param(h, "gcd") != "13r3r3r3r5" || param(h, "tid") != "G-TESTBED001" || param(h, "cid") == "" {
		t.Errorf("GA4 hit query = %s", h.Query)
	}
	if ads := l.waitHits("tagtest", 1, pathIs("googleads.g.doubleclick.net/pagead/viewthroughconversion/100000001/"), 5*time.Second); len(ads) != 1 || param(ads[0], "gcs") != "G111" {
		t.Errorf("Ads conversion pings = %+v", ads)
	}
	if c := l.eval(p, `() => document.cookie`); !strings.Contains(c, "_ga=GA1.1.") || !strings.Contains(c, "_ga_TESTBED001=") || !strings.Contains(c, "_gcl_au=") {
		t.Errorf("cookies after grant: %q", c)
	}
}

func TestGoogleStandInAdvancedSendsCookielessPings(t *testing.T) {
	l := startLive(t, testRoot(t))
	p := l.open(tagSite + "/advanced/")
	hits := l.waitHits("tagtest", 1, ga4Hit, 10*time.Second)
	if len(hits) != 1 {
		t.Fatalf("pings = %+v", hits)
	}
	h := hits[0]
	if h.Method != "GET" || param(h, "en") != "page_view" || param(h, "gcs") != "G100" || param(h, "gcd") != "13p3p3p3p5" || param(h, "cid") != "" {
		t.Errorf("ping = %s %s", h.Method, h.Query)
	}
	if c := l.eval(p, `() => document.cookie`); strings.Contains(c, "_ga") {
		t.Errorf("a cookieless ping wrote %q", c)
	}
}

func TestGoogleStandInWithoutConsentMode(t *testing.T) {
	l := startLive(t, testRoot(t))
	p := l.open(tagSite + "/nocm/")
	hits := l.waitHits("tagtest", 1, ga4Hit, 10*time.Second)
	if len(hits) != 1 || param(hits[0], "gcs") != "" || param(hits[0], "gcd") != "13l3l3l3l5" {
		t.Fatalf("hits = %+v", hits)
	}
	if c := l.eval(p, `() => document.cookie`); !strings.Contains(c, "_ga=") {
		t.Errorf("no _ga without Consent Mode: %q", c)
	}
}

func TestGoogleStandInRegionAndWaitForUpdate(t *testing.T) {
	l := startLive(t, testRoot(t))
	p := l.open(tagSite + "/region/")
	l.waitTrue(p, `() => !!window.__tb_google`, 10*time.Second)
	// The DE default beats the global one; the US one isn't for us.
	if v := l.eval(p, `() => window.__tb_google.value('analytics_storage')`); v != "denied" {
		t.Errorf("analytics_storage in DE = %s", v)
	}
	if got := l.waitHits("tagtest", 1, ga4Hit, time.Second); len(got) != 0 {
		t.Errorf("hits with the DE default denied: %+v", got)
	}

	p = l.open(tagSite + "/wait/")
	hits := l.waitHits("tagtest", 1, ga4Hit, 10*time.Second)
	if len(hits) != 1 {
		t.Fatalf("no hit after wait_for_update: %+v", hits)
	}
	t0 := l.eval(p, `() => String(window.__tb_t0)`)
	var start int64
	if err := json.Unmarshal([]byte(t0), &start); err != nil {
		t.Fatal(err)
	}
	if waited := hits[0].At.UnixMilli() - start; waited < 1300 {
		t.Errorf("the hit came %d ms after the default, before wait_for_update (1500 ms)", waited)
	}
}

func TestGTMStandInContainer(t *testing.T) {
	l := startLive(t, testRoot(t))
	p := l.open(tagSite + "/gtm/")
	if !l.waitTrue(p, `() => !!(window.__tb_loaded && window.__tb_loaded.gtm)`, 10*time.Second) {
		t.Fatal("gtm.js stand-in didn't load")
	}
	if got := l.waitHits("tagtest", 1, func(Hit) bool { return true }, time.Second); len(got) != 0 {
		t.Fatalf("hits while denied: %+v", got)
	}
	// A dataLayer array push is a gtag() call too.
	l.eval(p, `() => dataLayer.push(['consent', 'update', {analytics_storage: 'granted', ad_storage: 'denied'}])`)
	hits := l.waitHits("tagtest", 1, ga4Hit, 10*time.Second)
	if len(hits) != 1 || param(hits[0], "gcs") != "G101" || param(hits[0], "gcd") != "13q3r3p3p5" {
		t.Fatalf("hits = %+v", hits)
	}
	if ads := l.waitHits("tagtest", 1, pathIs("googleads.g.doubleclick.net/"), time.Second); len(ads) != 0 {
		t.Errorf("an Ads ping with ad_storage denied: %+v", ads)
	}
	// An object push is a GTM event, not Consent Mode.
	l.eval(p, `() => dataLayer.push({event: 'consent', ad_storage: 'granted'})`)
	if v := l.eval(p, `() => window.__tb_google.value('ad_storage')`); v != "denied" {
		t.Errorf("an object push changed ad_storage to %s", v)
	}
}

func TestMetaStandInRevoke(t *testing.T) {
	l := startLive(t, testRoot(t))
	meta := pathIs("www.facebook.com/tr")
	p := l.open(tagSite + "/meta-before/")
	if !l.waitTrue(p, `() => !!(window.__tb_loaded && window.__tb_loaded.fbevents)`, 10*time.Second) {
		t.Fatal("fbevents stand-in didn't load")
	}
	if got := l.waitHits("tagtest", 1, meta, 1500*time.Millisecond); len(got) != 0 {
		t.Fatalf("hits after revoke before init: %+v", got)
	}
	if c := l.eval(p, `() => document.cookie`); strings.Contains(c, "_fbp") {
		t.Errorf("_fbp while revoked: %q", c)
	}
	// Google Consent Mode doesn't move it.
	l.eval(p, `() => { window.dataLayer = window.dataLayer || []; dataLayer.push(['consent', 'update', {ad_storage: 'granted'}]); }`)
	if got := l.waitHits("tagtest", 1, meta, time.Second); len(got) != 0 {
		t.Fatalf("Consent Mode released the pixel: %+v", got)
	}
	l.eval(p, `() => fbq('consent', 'grant')`)
	hits := l.waitHits("tagtest", 1, meta, 10*time.Second)
	if len(hits) != 1 || param(hits[0], "ev") != "PageView" || param(hits[0], "id") != "100000000000001" ||
		param(hits[0], "ud[em]") != "tbhashedemail" || !strings.HasPrefix(param(hits[0], "dl"), tagSite+"/meta-before/") {
		t.Fatalf("after grant: %+v", hits)
	}

	l.open(tagSite + "/meta-after/")
	if hits := l.waitHits("tagtest", 2, meta, 10*time.Second); len(hits) != 2 {
		t.Errorf("a revoke after init held the hit: %+v", hits)
	}
}

func TestWorkerStandInSendsFromTheWorker(t *testing.T) {
	l := startLive(t, testRoot(t))
	l.open(tagSite + "/worker/")
	hits := l.waitHits("tagtest", 1, ga4Hit, 10*time.Second)
	if len(hits) != 1 || param(hits[0], "dl") != tagSite+"/worker/" {
		t.Fatalf("worker hits = %+v", hits)
	}
}

func TestOtherStandIns(t *testing.T) {
	l := startLive(t, testRoot(t))
	p := l.open(tagSite + "/others/")
	for _, name := range []string{"tiktok", "linkedin", "uet", "clarity", "hotjar", "plausible", "recaptcha"} {
		if !l.waitTrue(p, `() => !!(window.__tb_loaded && window.__tb_loaded['`+name+`'])`, 10*time.Second) {
			t.Errorf("%s stand-in didn't load", name)
		}
	}
	for _, f := range []string{"yt", "ytnc"} {
		if !l.waitTrue(p, `() => { try { return !!document.getElementById('`+f+`').contentWindow; } catch (e) { return false; } }`, 10*time.Second) {
			t.Errorf("frame %s didn't load", f)
		}
	}
	// Storage before any hit: Clarity and Hotjar write theirs at load.
	c := l.eval(p, `() => document.cookie`)
	st := l.eval(p, `() => Object.keys(localStorage).concat(Object.keys(sessionStorage)).join(',')`)
	for _, want := range []string{"_clck=", "_clsk=", "_hjSessionUser_1000001=", "_hjSession_1000001=", "_ttp=", "_uetsid="} {
		if !strings.Contains(c, want) {
			t.Errorf("cookie %s missing: %q", want, c)
		}
	}
	for _, want := range []string{"_clck", "_hjLocalStorageTest", "_hjSessionStorageTest"} {
		if !strings.Contains(st, want) {
			t.Errorf("storage %s missing: %q", want, st)
		}
	}
	for _, hp := range []string{"analytics.tiktok.com/api/v2/pixel", "bat.bing.com/action/0", "plausible.io/api/event",
		"www.clarity.ms/collect", "in.hotjar.com/api/v2/client/sites/1000001/visit-data", "www.youtube.com/api/stats/qoe"} {
		if got := l.waitHits("tagtest", 1, pathIs(hp), 10*time.Second); len(got) != 1 {
			t.Errorf("%s: %d hits", hp, len(got))
		}
	}
	// LinkedIn waits for the first scroll.
	li := pathIs("px.ads.linkedin.com/collect")
	if got := l.waitHits("tagtest", 1, li, 500*time.Millisecond); len(got) != 0 {
		t.Errorf("LinkedIn fired before the scroll: %+v", got)
	}
	l.eval(p, `() => window.scrollTo(0, 1500)`)
	if got := l.waitHits("tagtest", 1, li, 10*time.Second); len(got) != 1 {
		t.Errorf("LinkedIn after the scroll: %d hits", len(got))
	}
	// Nothing reached a host the server doesn't serve.
	if u := l.srv.Count("tagtest").Unmapped; len(u) != 0 {
		t.Errorf("unmapped hosts: %v", u)
	}
	if n := l.srv.Count("unknown").Hits; n != 0 {
		t.Errorf("%d hits without a scenario", n)
	}
}

// TestGCDEncodingInTheBrowser runs the stand-in's own encoder for every
// state of the table and compares with the test's expectations.
func TestGCDEncodingInTheBrowser(t *testing.T) {
	l := startLive(t, testRoot(t))
	p := l.open(tagSite + "/nocm/")
	l.waitTrue(p, `() => !!window.__tb_google`, 10*time.Second)
	for _, c := range gcdCases {
		in, _ := json.Marshal([]any{c.defaults, c.updates})
		got := l.eval(p, `(a) => JSON.stringify(window.__tb_google.encode(a[0], a[1]))`, json.RawMessage(in))
		var out struct{ GCS, GCD string }
		if err := json.Unmarshal([]byte(got), &out); err != nil {
			t.Fatal(err)
		}
		if out.GCS != c.gcs || out.GCD != c.gcd {
			t.Errorf("%s: gcs %q gcd %q, want %q %q", c.name, out.GCS, out.GCD, c.gcs, c.gcd)
		}
		checkDecodes(t, c)
	}
}
