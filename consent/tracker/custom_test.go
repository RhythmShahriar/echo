// Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
// SPDX-License-Identifier: MIT

package tracker

import (
	"flag"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/RhythmShahriar/echo/consent"
)

var update = flag.Bool("update", false, "rewrite the golden files")

// shopPixel is PRD 8.1's example of consent: trackers:.
const shopPixel = `- id: shop-pixel
  name: Shop pixel
  category: advertising
  hits:
    - host: px.shop.example
      path: /collect
  cookies: [_shpx]
  fixtures:
    match: ["https://px.shop.example/collect?e=view"]
    no_match: ["https://px.shop.example/pixel.js"]
`

// fresh is a catalog of its own, so a test can add custom trackers
// without touching the registered one other tests read.
func fresh(t testing.TB) *Catalog {
	t.Helper()
	c, err := load(catalogFS, catalogDir)
	if err != nil {
		t.Fatal(err)
	}
	return c
}

func TestAddCustomOK(t *testing.T) {
	c := fresh(t)
	info, n := c.Info(), len(c.Trackers())
	if err := c.AddCustom([]byte(shopPixel)); err != nil {
		t.Fatal(err)
	}
	h := c.Request(&consent.Request{Method: "GET", URL: "https://px.shop.example/collect?e=view"}, nil, nil)
	if h == nil || h.Tracker != "shop-pixel" || h.Kind != consent.HitBeacon || h.Need != consent.NeedNeeded || h.Category != consent.CatAdvertising {
		t.Fatalf("hit = %+v", h)
	}
	if !c.Hold(&consent.Request{Method: "GET", URL: "https://px.shop.example/collect"}) {
		t.Error("a custom beacon that needs consent isn't held")
	}
	if h := c.Cookie(&consent.Cookie{Name: "_shpx"}); h == nil || h.Tracker != "shop-pixel" {
		t.Errorf("cookie = %+v", h)
	}
	if c.Request(&consent.Request{URL: "https://px.shop.example/pixel.js"}, nil, nil) != nil {
		t.Error("the no_match fixture matches")
	}
	list := c.Trackers()
	i := slices.IndexFunc(list, func(t consent.TrackerInfo) bool { return t.ID == "shop-pixel" })
	if len(list) != n+1 || i < 0 {
		t.Fatalf("%d trackers, shop-pixel at %d", len(list), i)
	}
	want := consent.TrackerInfo{ID: "shop-pixel", Name: "Shop pixel", Vendor: "Shop pixel", Category: consent.CatAdvertising,
		Need: consent.NeedNeeded, Matches: "px.shop.example/collect", Fix: defaultCustomFix, Sources: []string{"config"}}
	if got := list[i]; got.ID != want.ID || got.Name != want.Name || got.Vendor != want.Vendor || got.Category != want.Category ||
		got.Need != want.Need || got.Matches != want.Matches || got.Fix != want.Fix || got.Docs != "" || !slices.Equal(got.Sources, want.Sources) {
		t.Errorf("Trackers() = %+v, want %+v", got, want)
	}
	if c.Info() != info {
		t.Errorf("Info() changed: %+v, was %+v", c.Info(), info)
	}
	// GA4 still works beside it.
	if h := c.Request(&consent.Request{URL: "https://www.google-analytics.com/g/collect?v=2&tid=G-TESTBED001"}, nil, nil); h == nil || h.Tracker != "ga4" {
		t.Errorf("ga4 = %+v", h)
	}

	// Loading again replaces; an empty list removes them.
	for _, b := range []string{shopPixel, "", "[]", "null\n"} {
		if err := c.AddCustom([]byte(b)); err != nil {
			t.Fatalf("%q: %v", b, err)
		}
	}
	if len(c.Trackers()) != n || c.Request(&consent.Request{URL: "https://px.shop.example/collect"}, nil, nil) != nil || c.Cookie(&consent.Cookie{Name: "_shpx"}) != nil {
		t.Error("an empty list left the custom tracker in")
	}
}

func TestAddCustomEveryKey(t *testing.T) {
	c := fresh(t)
	yml := `- id: shop-stats
  name: Shop stats
  vendor: Shop Ltd
  category: analytics
  consent: exempt
  any_host: true
  hits:
    - paths: [/stats/hit]
      methods: [POST]
      params_all: [sid]
      params_match: {sid: '^S-'}
      not_params: {dnt: '1'}
  scripts:
    - host: cdn.shop.example
      path: /stats.js
  storage: ["shopstats_*"]
  params:
    event: [ev]
    id: [sid]
  docs: https://shop.example/stats
  fixtures:
    match:
      - POST https://shop.test/stats/hit?sid=S-1234&ev=view
      - https://cdn.shop.example/stats.js
    no_match:
      - https://shop.test/stats/hit?sid=S-1234
      - POST https://shop.test/stats/hit?sid=S-1234&dnt=1
    storage: {match: [shopstats_v], no_match: [shop_cart]}
- id: shop-stripe
  name: Shop payments
  category: necessary
  cookies: [{name: shop_pay, domain: pay.shop.example}]
  fixtures:
    cookies:
      match: [{name: shop_pay, domain: pay.shop.example}]
      no_match: [{name: shop_pay, domain: shop.example}]
`
	if err := c.AddCustom([]byte(yml)); err != nil {
		t.Fatal(err)
	}
	h := c.Request(&consent.Request{Method: "POST", URL: "https://shop.test/stats/hit?sid=S-1234&ev=view"}, nil, nil)
	if h == nil || h.Tracker != "shop-stats" || h.Need != consent.NeedExempt || h.ID != consent.MaskID("S-1234") || !slices.Equal(h.Events, []string{"view"}) {
		t.Fatalf("hit = %+v", h)
	}
	if h := c.Cookie(&consent.Cookie{Name: "shop_pay", Domain: ".pay.shop.example"}); h == nil || h.Need != consent.NeedNo {
		t.Errorf("necessary cookie = %+v", h)
	}
	list := c.Trackers()
	i := slices.IndexFunc(list, func(t consent.TrackerInfo) bool { return t.ID == "shop-stats" })
	if i < 0 || list[i].Vendor != "Shop Ltd" || list[i].Docs != "https://shop.example/stats" || list[i].Fix != "" {
		t.Errorf("shop-stats = %+v", list[i])
	}
}

func TestAddCustomCollides(t *testing.T) {
	c := fresh(t)
	ga := strings.Replace(shopPixel, "id: shop-pixel", "id: ga4", 1)
	err := c.AddCustom([]byte(ga))
	if err == nil || err.Error() != `consent: trackers: "ga4" is a built-in tracker; pick another id` {
		t.Fatalf("err = %v", err)
	}
	err = c.AddCustom([]byte(shopPixel + shopPixel))
	if err == nil || err.Error() != `consent: trackers: "shop-pixel" is listed twice; give each tracker its own id` {
		t.Fatalf("err = %v", err)
	}
	// Two custom entries can't claim one cookie either.
	other := strings.NewReplacer("id: shop-pixel", "id: shop-pixel-2", "px.shop.example", "px2.shop.example").Replace(shopPixel)
	err = c.AddCustom([]byte(shopPixel + other))
	if err == nil || err.Error() != `consent: trackers: shop-pixel-2: cookies: "_shpx" is already claimed by shop-pixel` {
		t.Fatalf("err = %v", err)
	}
	if slices.ContainsFunc(c.Trackers(), func(t consent.TrackerInfo) bool { return slices.Contains(t.Sources, "config") }) {
		t.Fatal("a refused list left entries behind")
	}
}

func TestAddCustomNoFixtures(t *testing.T) {
	c := fresh(t)
	for _, tc := range []struct{ yml, want string }{
		{strings.Split(shopPixel, "  fixtures:")[0], "consent: trackers: shop-pixel needs fixtures: match and no_match, each with at least one URL"},
		{strings.Replace(shopPixel, `    no_match: ["https://px.shop.example/pixel.js"]`+"\n", "", 1), "consent: trackers: shop-pixel needs fixtures: match and no_match, each with at least one URL"},
		{"- id: shop-cookie\n  name: Shop cookie\n  category: analytics\n  cookies: [_shc]\n  fixtures: {cookies: {match: [_shc]}}\n",
			"consent: trackers: shop-cookie needs fixtures: cookies: match and no_match, each with at least one name"},
		{"- id: shop-store\n  name: Shop store\n  category: analytics\n  storage: [shs_*]\n",
			"consent: trackers: shop-store needs fixtures: storage: match and no_match, each with at least one name"},
	} {
		err := c.AddCustom([]byte(tc.yml))
		if err == nil || err.Error() != tc.want {
			t.Errorf("err = %v, want %s", err, tc.want)
		}
	}
	// Cookie fixtures are optional beside URL ones (PRD 8.1's example).
	if err := c.AddCustom([]byte(shopPixel)); err != nil {
		t.Fatal(err)
	}
}

func TestAddCustomFailingFixture(t *testing.T) {
	c := fresh(t)
	wide := strings.Replace(shopPixel, "path: /collect", "path: /**", 1)
	err := c.AddCustom([]byte(wide))
	if want := `consent: trackers: shop-pixel: fixture "https://px.shop.example/pixel.js" should not match but does`; err == nil || err.Error() != want {
		t.Fatalf("err = %v, want %s", err, want)
	}
	narrow := strings.Replace(shopPixel, "path: /collect", "path: /track", 1)
	err = c.AddCustom([]byte(narrow))
	if want := `consent: trackers: shop-pixel: fixture "https://px.shop.example/collect?e=view" should match but doesn't`; err == nil || err.Error() != want {
		t.Fatalf("err = %v, want %s", err, want)
	}
	cookie := strings.Replace(shopPixel, "    no_match:", "    cookies: {match: [_shpx_other], no_match: [_shop]}\n    no_match:", 1)
	err = c.AddCustom([]byte(cookie))
	if want := `consent: trackers: shop-pixel: cookie fixture "_shpx_other" should match but doesn't`; err == nil || err.Error() != want {
		t.Fatalf("err = %v, want %s", err, want)
	}
	if c.Request(&consent.Request{URL: "https://px.shop.example/collect"}, nil, nil) != nil {
		t.Fatal("a refused list changed the catalog")
	}
}

func TestAddCustomRefuses(t *testing.T) {
	c := fresh(t)
	for _, tc := range []struct{ name, yml, want string }{
		{"not a list", "id: shop-pixel\n", "consent: trackers: want a list of trackers, each with id, name, category, what it matches and fixtures"},
		{"bad YAML", "- [\n", "consent: trackers: did not find expected node content"},
		{"unknown key", strings.Replace(shopPixel, "  hits:", "  hitz:", 1), `consent: trackers: shop-pixel: unknown key "hitz"`},
		{"unknown fixture key", strings.Replace(shopPixel, "    no_match:", "    nomatch:", 1), `consent: trackers: shop-pixel: unknown key "nomatch"`},
		{"bad id", strings.Replace(shopPixel, "id: shop-pixel", "id: Shop Pixel", 1), `consent: trackers: entry 1: id "Shop Pixel" must be lowercase letters, digits and dashes`},
		{"not a mapping", "- shop-pixel\n", "consent: trackers: entry 1: want a mapping of keys"},
		{"sources", strings.Replace(shopPixel, "  cookies:", "  sources: [https://x.example]\n  cookies:", 1), "consent: trackers: shop-pixel: leave sources out: your own trackers are listed with the source config"},
		{"category", strings.Replace(shopPixel, "advertising", "ads", 1), `consent: trackers: shop-pixel: category "ads": want analytics, advertising, session-replay, social, embed, functional or necessary`},
		{"docs", strings.Replace(shopPixel, "  cookies:", "  docs: http://x.example\n  cookies:", 1), "consent: trackers: shop-pixel: docs must be an https:// URL"},
		{"any host", strings.Replace(shopPixel, "    - host: px.shop.example\n", "    - ", 1), "consent: trackers: shop-pixel: a rule without hosts needs any_host: true on the entry"},
		{"denied cookie", strings.Replace(shopPixel, "[_shpx]", "[PHPSESSID]", 1), `consent: trackers: shop-pixel: cookies: "PHPSESSID" is listed in _not_trackers.yaml as never a tracker`},
		{"script looks like a hit", "- id: shop-tag\n  name: Shop tag\n  category: analytics\n  scripts: [{host: www.google-analytics.com}]\n  fixtures:\n    match: [https://www.google-analytics.com/shop.js]\n    no_match: [https://www.shop.example/]\n",
			"consent: trackers: shop-tag: a script of shop-tag and a hit of ga4 (catalog/ga4.yaml:"},
		{"hit looks like a script", "- id: shop-gtm\n  name: Shop GTM\n  category: analytics\n  hits: [{host: www.googletagmanager.com}]\n  fixtures:\n    match: [https://www.googletagmanager.com/x]\n    no_match: [https://www.shop.example/]\n",
			"consent: trackers: shop-gtm: a hit of shop-gtm and a script of google-tag-manager (catalog/google-tag-manager.yaml:"},
		{"fixture URL", strings.Replace(shopPixel, `["https://px.shop.example/pixel.js"]`, `["ftp://px.shop.example/"]`, 1), `consent: trackers: shop-pixel: "ftp://px.shop.example/" is not an http or https URL`},
		{"long fixture", strings.Replace(shopPixel, "pixel.js", "pixel.js?q="+strings.Repeat("a", 2100), 1), "consent: trackers: shop-pixel: a fixture URL is longer than 2048 bytes"},
		{"control text", strings.Replace(shopPixel, "id: shop-pixel", "id: shop-pixel\n  vendor: \"\\e[31m\"\n  oops: 1", 1), `consent: trackers: shop-pixel: unknown key "oops"`},
	} {
		err := c.AddCustom([]byte(tc.yml))
		if err == nil || !strings.HasPrefix(err.Error(), tc.want) {
			t.Errorf("%s: err = %v, want %s", tc.name, err, tc.want)
		}
	}
	many := strings.Repeat("- {}\n", maxCustomTrackers+1)
	if err := c.AddCustom([]byte(many)); err == nil || err.Error() != "consent: trackers: 201 trackers is more than 200" {
		t.Errorf("many: %v", err)
	}
	if err := c.AddCustom(make([]byte, maxCustomYAML+1)); err == nil || err.Error() != "consent: trackers: the list is more than 1024 KiB" {
		t.Errorf("big: %v", err)
	}
	if len(c.Trackers()) != len(c.base) {
		t.Error("a refused list changed the catalog")
	}
}

func TestCustomBeforeBuiltin(t *testing.T) {
	c := fresh(t)
	yml := `- id: shop-ga
  name: Shop's own GA4 proxy
  category: analytics
  hits:
    - host: www.google-analytics.com
      path: /g/collect
  cookies: [_ga]
  fixtures:
    match: ["https://www.google-analytics.com/g/collect?v=2&tid=G-TESTBED001"]
    no_match: ["https://www.google-analytics.com/g/s/collect?v=2&tid=G-TESTBED001"]
`
	ga := &consent.Request{URL: "https://www.google-analytics.com/g/collect?v=2&tid=G-TESTBED001"}
	if h := c.Request(ga, nil, nil); h == nil || h.Tracker != "ga4" {
		t.Fatalf("before: %+v", h)
	}
	if err := c.AddCustom([]byte(yml)); err != nil {
		t.Fatal(err)
	}
	if h := c.Request(ga, nil, nil); h == nil || h.Tracker != "shop-ga" {
		t.Errorf("custom entry didn't win: %+v", h)
	}
	if h := c.Request(&consent.Request{URL: "https://www.google-analytics.com/g/s/collect?v=2&tid=G-TESTBED001"}, nil, nil); h == nil || h.Tracker != "ga4" {
		t.Errorf("what the custom entry doesn't claim stays ga4: %+v", h)
	}
	if h := c.Cookie(&consent.Cookie{Name: "_ga"}); h == nil || h.Tracker != "shop-ga" {
		t.Errorf("_ga = %+v", h)
	}
	if h := c.Cookie(&consent.Cookie{Name: "_ga_TESTBED001"}); h == nil || h.Tracker != "ga4" {
		t.Errorf("_ga_TESTBED001 = %+v", h)
	}
}

func TestFixtureReportLines(t *testing.T) {
	c := fresh(t)
	yml := shopPixel + `- id: shop-wide
  name: Shop wide
  category: analytics
  hits:
    - host: stats.shop.example
      path: /**
  storage: ["sw_*"]
  fixtures:
    match: ["POST https://stats.shop.example/e?u=a@b.example"]
    no_match: ["https://stats.shop.example/sw.js"]
    storage: {match: [sw_id], no_match: [sw]}
- id: ga4
  name: Mine
  category: analytics
  cookies: [_mine]
  fixtures: {cookies: {match: [_mine], no_match: [_yours]}}
- id: Bad
`
	lines, ok := c.FixtureReport([]byte(yml))
	if ok {
		t.Error("ok with failing fixtures")
	}
	got := strings.Join(lines, "\n") + "\n"
	golden := filepath.Join("testdata", "fixture-report.golden")
	if *update {
		if err := os.WriteFile(golden, []byte(got), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	want, err := os.ReadFile(golden)
	if err != nil {
		t.Fatal(err)
	}
	if got != string(want) {
		t.Errorf("FixtureReport (go test ./consent/tracker -run FixtureReport -update to accept):\n%s", got)
	}
	if len(c.Trackers()) != len(c.base) {
		t.Error("FixtureReport changed the catalog")
	}

	lines, ok = c.FixtureReport([]byte(shopPixel))
	want2 := []string{
		"ok   shop-pixel  match    https://px.shop.example/collect?e=view",
		"ok   shop-pixel  no_match https://px.shop.example/pixel.js",
	}
	if !ok || !slices.Equal(lines, want2) {
		t.Errorf("FixtureReport = %v, %q", ok, lines)
	}
	if lines, ok := c.FixtureReport(nil); !ok || lines != nil {
		t.Errorf("empty: %v %q", ok, lines)
	}
	// Passing fixtures can still fail as a whole catalog.
	lines, ok = c.FixtureReport([]byte(strings.Replace(shopPixel, "[_shpx]", "[PHPSESSID]", 1)))
	if ok || len(lines) != 3 || lines[2] != `FAIL consent: trackers: shop-pixel: cookies: "PHPSESSID" is listed in _not_trackers.yaml as never a tracker` {
		t.Errorf("denied: %v %q", ok, lines)
	}
	if lines, ok := c.FixtureReport([]byte("x: 1\n")); ok || len(lines) != 1 || !strings.HasPrefix(lines[0], "FAIL consent: trackers: want a list") {
		t.Errorf("not a list: %v %q", ok, lines)
	}
	if _, ok := consent.Classifier(builtin).(interface {
		FixtureReport([]byte) ([]string, bool)
	}); !ok {
		t.Error("the registered Classifier has no FixtureReport")
	}
}

func FuzzCustomTrackers(f *testing.F) {
	c := fresh(f)
	f.Add([]byte(shopPixel))
	f.Add([]byte(shopPixel + shopPixel))
	f.Add([]byte(strings.Replace(shopPixel, "path: /collect", "path: /**", 1)))
	f.Add([]byte("- id: ga4\n"))
	f.Add([]byte("- {id: x, fixtures: {match: [\"\\e]2;x\\a\"]}}\n"))
	f.Add([]byte("- &a [*a]\n"))
	f.Add([]byte("x: 1\n"))
	f.Add([]byte(""))
	f.Fuzz(func(t *testing.T, b []byte) {
		n, err := c.withCustom(b)
		lines, ok := c.FixtureReport(b)
		if (err == nil) != ok {
			t.Fatalf("AddCustom err %v but FixtureReport ok %v: %q", err, ok, lines)
		}
		if err != nil {
			if !strings.HasPrefix(err.Error(), customPrefix) {
				t.Fatalf("error without the prefix: %q", err)
			}
		} else if len(n.Trackers()) < len(c.base) || n.Info() != c.Info() {
			t.Fatal("custom trackers changed the built-in ones")
		}
		for _, l := range lines {
			if !strings.HasPrefix(l, "ok   ") && !strings.HasPrefix(l, "FAIL ") {
				t.Fatalf("line %q", l)
			}
			if strings.ContainsFunc(l, func(r rune) bool { return r < 0x20 && r != '\t' || r == 0x7f }) {
				t.Fatalf("control character in %q", l)
			}
		}
	})
}
