// Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
// SPDX-License-Identifier: MIT

package tracker

import (
	"net/url"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/RhythmShahriar/echo/consent"
)

// TestHoldNeverScripts runs every scripts.match and frames.match
// fixture of every entry through Hold: none is ever held, whatever
// type the recorder gives it.
func TestHoldNeverScripts(t *testing.T) {
	n := 0
	for _, e := range builtin.entries {
		ff := readFixtures(t, e.info.ID)
		for _, set := range []struct {
			kind consent.HitKind
			fix  []reqFixture
		}{{consent.HitScript, ff.Scripts.Match}, {consent.HitFrame, ff.Frames.Match}} {
			for _, f := range set.fix {
				n++
				if h := builtin.Request(f.request(), nil, f.site()); h == nil || h.Kind != set.kind {
					t.Errorf("%s: %s = %+v, want kind %s", e.info.ID, f.URL, h, set.kind)
				}
				for _, typ := range []string{"", "script", "document", "image", "fetch", "other"} {
					r := f.request()
					r.Type = typ
					if builtin.Hold(r) {
						t.Errorf("%s: %s (type %q) is held", e.info.ID, f.URL, typ)
					}
				}
			}
		}
	}
	if n < 40 {
		t.Fatalf("only %d script and frame fixtures", n)
	}
}

// TestHoldBodyOnlyBeacon holds a POST whose proving parameters are only
// in the body, which Hold never sees, on its vendor endpoint alone.
func TestHoldBodyOnlyBeacon(t *testing.T) {
	post := func(u, typ string) *consent.Request { return &consent.Request{Method: "POST", URL: u, Type: typ} }
	// The meta-pixel ping fixture: id and ev are only in the body.
	ping := post("https://www.facebook.com/tr/", "ping")
	if h := builtin.Request(ping, []byte("id=1234567890&ev=Purchase"), shop); h == nil || h.Tracker != "meta-pixel" || h.Kind != consent.HitBeacon {
		t.Fatalf("meta-pixel ping with body = %+v", h)
	}
	if h := builtin.Request(ping, nil, shop); h != nil {
		t.Fatalf("meta-pixel ping without body = %+v, want nil (its rule needs id and ev)", h)
	}
	for _, r := range []*consent.Request{
		ping,
		post("https://www.facebook.com/tr", "beacon"),
		post("https://www.google-analytics.com/collect", "beacon"),
		post("https://shop.matomo.cloud/matomo.php", "fetch"),
		{Method: "put", URL: "https://in.hotjar.com/api/v2/client/sites/1/visit-data", Type: "xhr"},
	} {
		if !builtin.Hold(r) {
			t.Errorf("%s %s (type %q) is not held", r.Method, r.URL, r.Type)
		}
	}
	for _, r := range []*consent.Request{
		// GET carries no body: its parameters must prove the hit.
		get("https://www.facebook.com/tr", "image"),
		// Another path on the vendor host.
		post("https://www.facebook.com/login/", "fetch"),
		// Loaded as a script or a page.
		post("https://www.facebook.com/tr", "script"),
		post("https://www.facebook.com/tr", "document"),
		// On any host only the parameters prove a hit.
		post("https://shop.test/api/event", "fetch"),
		post("https://shop.test/matomo.php", "beacon"),
		// A necessary service is never held.
		post("https://api.hcaptcha.com/checkcaptcha/abc", "xhr"),
		// Not a tracker.
		post("https://shop.test/cart", "fetch"),
	} {
		if builtin.Hold(r) {
			t.Errorf("%s %s (type %q) is held", r.Method, r.URL, r.Type)
		}
	}
}

// TestRepeatedQueryName checks a repeated parameter name classifies the
// same in any order: a rule passes when any value fits and the hit
// reports the smallest.
func TestRepeatedQueryName(t *testing.T) {
	for _, c := range []struct {
		a, b string
		want string // tracker, "" for none
	}{
		{"v=2&v=1&tid=G-ABCD1234", "v=1&v=2&tid=G-ABCD1234", "ga4"},
		{"v=1&v=3&tid=G-ABCD1234", "v=3&v=1&tid=G-ABCD1234", ""},
		{"v=2&tid=G-ABCD1234&en=b&en=a", "en=a&v=2&en=b&tid=G-ABCD1234", "ga4"},
	} {
		var hits []*consent.Hit
		for _, q := range []string{c.a, c.b} {
			hits = append(hits, builtin.Request(get("https://metrics.shop.test/g/collect?"+q, "ping"), nil, shop))
		}
		if !reflect.DeepEqual(hits[0], hits[1]) {
			t.Errorf("%s and %s differ: %+v and %+v", c.a, c.b, hits[0], hits[1])
		}
		got := ""
		if hits[0] != nil {
			got = hits[0].Tracker
		}
		if got != c.want {
			t.Errorf("%s = %q, want %q", c.a, got, c.want)
		}
	}
	h := builtin.Request(get("https://www.google-analytics.com/g/collect?v=2&tid=G-ABCD1234&en=b&en=a", "ping"), nil, shop)
	if h == nil || len(h.Events) != 1 || h.Events[0] != "a" || h.Params["en"] != "a" {
		t.Errorf("repeated en = %+v, want the smallest value a", h)
	}
	// A frame's not_params fails when any value is the excluded one.
	for _, q := range []string{"dnt=0&dnt=1", "dnt=1&dnt=0"} {
		if h := builtin.Request(get("https://player.vimeo.com/video/1?"+q, "document"), nil, shop); h != nil {
			t.Errorf("vimeo ?%s = %+v, want nil", q, h)
		}
	}
	if h := builtin.Request(get("https://player.vimeo.com/video/1?dnt=0&dnt=2", "document"), nil, shop); h == nil || h.Tracker != "vimeo" {
		t.Errorf("vimeo without dnt=1 = %+v", h)
	}
}

func TestNoScriptMatchesHit(t *testing.T) {
	if err := checkOverlap(builtin.entries); err != nil {
		t.Fatalf("built-in catalog: %v", err)
	}
	// Within one entry: the script pattern also fits the hit path.
	self := strings.Replace(minimalEntry, "cookies:", "scripts:\n  - host: px.shop.example\n    path: \"/c*\"\ncookies:", 1)
	selfFix := minimalFixtures + "scripts:\n  match: [\"https://px.shop.example/c.js\"]\n"
	_, err := load(catalogOf(map[string]string{"shop-pixel.yaml": self, "shop-pixel.fixtures.yaml": selfFix}), "catalog")
	want := "catalog/shop-pixel.yaml:10: a script of shop-pixel and a hit of shop-pixel (catalog/shop-pixel.yaml:7) both fit px.shop.example (script path /c*, hit path /collect); a script must never look like a hit"
	if err == nil || !strings.Contains(err.Error(), want) {
		t.Fatalf("one entry: err = %v\nwant %s", err, want)
	}
	// Across entries: another entry's script host covers every path.
	other := `id: shop-tag
name: Shop tag
vendor: Shop
category: advertising
consent: needed
scripts:
  - hosts: ["*.shop.example"]
fix: Load the shop tag after consent
docs: https://shop.example/docs
sources: ["https://shop.example/docs"]
`
	otherFix := "scripts:\n  match: [\"https://cdn.shop.example/tag.js\"]\n"
	_, err = load(catalogOf(map[string]string{
		"shop-pixel.yaml": minimalEntry, "shop-pixel.fixtures.yaml": minimalFixtures,
		"shop-tag.yaml": other, "shop-tag.fixtures.yaml": otherFix,
	}), "catalog")
	if err == nil || !strings.Contains(err.Error(), "catalog/shop-tag.yaml:7: a script of shop-tag and a hit of shop-pixel (catalog/shop-pixel.yaml:7) both fit px.shop.example (script path (any), hit path /collect)") {
		t.Fatalf("two entries: err = %v", err)
	}
	// A script that only takes paths the hit can't reach loads fine.
	apart := strings.Replace(other, "  - hosts: [\"*.shop.example\"]\n", "  - hosts: [\"*.shop.example\"]\n    paths: [\"/*.js\"]\n", 1)
	if _, err := load(catalogOf(map[string]string{
		"shop-pixel.yaml": minimalEntry, "shop-pixel.fixtures.yaml": minimalFixtures,
		"shop-tag.yaml": apart, "shop-tag.fixtures.yaml": otherFix,
	}), "catalog"); err != nil {
		t.Fatalf("scripts apart from hits: %v", err)
	}
}

func TestGlobsMeet(t *testing.T) {
	for _, c := range []struct {
		a, b string
		want bool
	}{
		{"", "/collect", true},
		{"/collect", "/collect", true},
		{"/collect", "/collect/", true}, // a trailing slash is forgiven
		{"/c*", "/collect", true},
		{"/*.js", "/collect", false},
		{"/*.js", "/api/**", false},
		{"/api/*.js", "/api/**", true},
		{"/*/*.js", "/collect", false},
		{"/tag/*", "/collect", false},
		{"/s/**", "/s", true},
		{"/a?c", "/abc", true},
		{"/a?c", "/a/c", false},
		{"/[ab]x", "/zx", true}, // classes count as any byte: an overlap reported, never one missed
		{`/\*x`, "/*x", true},
	} {
		if got := globsMeet(c.a, c.b); got != c.want {
			t.Errorf("globsMeet(%q, %q) = %v, want %v", c.a, c.b, got, c.want)
		}
		if got := globsMeet(c.b, c.a); got != c.want {
			t.Errorf("globsMeet(%q, %q) = %v, want %v", c.b, c.a, got, c.want)
		}
	}
}

// permutations calls fn with every order of s.
func permutations(s []string, fn func([]string)) {
	var rec func(int)
	rec = func(k int) {
		if k == len(s) {
			fn(s)
			return
		}
		for i := k; i < len(s); i++ {
			s[k], s[i] = s[i], s[k]
			rec(k + 1)
			s[k], s[i] = s[i], s[k]
		}
	}
	rec(0)
}

func TestQueryOrderStable(t *testing.T) {
	for _, c := range []struct{ base, site string }{
		{"https://www.google-analytics.com/g/collect", "https://shop.test/"},
		{"https://metrics.shop.test/g/collect", "https://shop.test/"},
	} {
		pairs := []string{"v=2", "tid=G-TESTBED001", "en=page_view", "gcs=G100", "dl=https%3A%2F%2Fshop.test%2F%3Femail%3Dx", "cid=1.2"}
		site, _ := url.Parse(c.site)
		want := builtin.Request(get(c.base+"?"+strings.Join(pairs, "&"), "ping"), nil, site)
		if want == nil || want.Tracker != "ga4" || want.Params["dl"] == "" {
			t.Fatalf("%s: %+v", c.base, want)
		}
		n := 0
		permutations(pairs, func(p []string) {
			n++
			got := builtin.Request(get(c.base+"?"+strings.Join(p, "&"), "ping"), nil, site)
			if !reflect.DeepEqual(got, want) {
				t.Fatalf("%s?%s = %+v, want %+v", c.base, strings.Join(p, "&"), got, want)
			}
		})
		if n != 720 {
			t.Fatalf("%d orders, want 720", n)
		}
	}
}

func TestFirstPartyLabel(t *testing.T) {
	for _, c := range []struct {
		url, site string
		want      bool
	}{
		{"https://metrics.shop.test/g/collect?v=2&tid=G-TESTBED001", "https://shop.test", true},
		{"https://metrics.shop.test/g/collect?v=2&tid=G-TESTBED001", "https://www.shop.test/cart", true},
		{"https://shop.test/g/collect?v=2&tid=G-TESTBED001", "https://shop.test/", true},
		{"https://www.google-analytics.com/g/collect?v=2&tid=G-TESTBED001", "https://shop.test", false},
		{"https://metrics.othershop.test/g/collect?v=2&tid=G-TESTBED001", "https://shop.test", false},
		{"https://127.0.0.1:35000/g/collect?v=2&tid=G-TESTBED001", "https://127.0.0.1:35000/", true},
		{"https://127.0.0.2/g/collect?v=2&tid=G-TESTBED001", "https://127.0.0.1/", false},
	} {
		site, _ := url.Parse(c.site)
		h := builtin.Request(get(c.url, "ping"), nil, site)
		if h == nil || h.Tracker != "ga4" || h.FirstParty != c.want {
			t.Errorf("%s on %s: %+v, want first party %v", c.url, c.site, h, c.want)
		}
	}
	if h := builtin.Request(get("https://metrics.shop.test/g/collect?v=2&tid=G-TESTBED001", "ping"), nil, nil); h == nil || h.FirstParty {
		t.Errorf("no site: %+v", h)
	}
}

func TestAnyHostNeverAScript(t *testing.T) {
	// A first-party loader can carry every parameter a hit rule on any
	// host asks for; it is still a script load.
	for _, c := range []struct{ url, typ string }{
		{"https://shop.test/g/collect?v=2&tid=G-TESTBED001", "script"},
		{"https://shop.test/g/collect?v=2&tid=G-TESTBED001", "document"},
		{"https://shop.test/tr/loader.js?id=1&ev=PageView", "image"},
		{"https://stats.shop.test/x/matomo.js?idsite=1&rec=1", ""},
	} {
		if h := builtin.Request(get(c.url, c.typ), nil, shop); h != nil && h.Kind == consent.HitBeacon {
			t.Errorf("%s (type %q) = %+v, want no beacon", c.url, c.typ, h)
		}
	}
	if h := builtin.Request(get("https://shop.test/g/collect?v=2&tid=G-TESTBED001", "ping"), nil, shop); h == nil || h.Kind != consent.HitBeacon {
		t.Errorf("first-party ga4 ping = %+v", h)
	}
}

// queryPairs lists the pairs of q the matcher reads, in order.
func queryPairs(q string) []string {
	var out []string
	for i, p := range strings.Split(q, "&") {
		if i == maxPairs {
			break
		}
		if p != "" {
			out = append(out, p)
		}
	}
	return out
}

func FuzzTrackerMatch(f *testing.F) {
	for _, e := range builtin.entries {
		ff := readFixtures(f, e.info.ID)
		for _, set := range [][]reqFixture{ff.Hits.Match, ff.Hits.NoMatch, ff.Scripts.Match, ff.Frames.Match} {
			for _, x := range set {
				f.Add(x.URL, x.Method, []byte(x.Body))
			}
		}
	}
	f.Add("https://xn--80ak6aa92e.com./g/collect?v=2&tid=G-1", "post", []byte("en=a\r\n\r\nen=b"))
	f.Add("https://[::1]:35000/tr?id=1&ev=x", "GET", []byte(`{"n":1,"u":[]}`))
	f.Add("http://%zz/", "", []byte(nil))
	f.Fuzz(func(t *testing.T, u, method string, body []byte) {
		r := &consent.Request{Method: method, URL: u}
		// The fastest of three calls is the cost; one call alone can land
		// on a GC pause or a busy fuzz worker.
		var a, b *consent.Hit
		took := time.Hour
		for i := range 3 {
			start := time.Now()
			h := builtin.Request(r, body, shop)
			took = min(took, time.Since(start))
			if i == 0 {
				a = h
			} else {
				b = h
			}
		}
		if !reflect.DeepEqual(a, b) {
			t.Fatalf("two calls differ: %+v and %+v", a, b)
		}
		// Hold may go by the endpoint alone only where a body could
		// carry the parameters.
		if h := builtin.Hold(r); h && (a == nil || a.Kind != consent.HitBeacon) && body == nil {
			if m := strings.ToUpper(method); m != "POST" && m != "PUT" {
				t.Fatalf("held a %s request that isn't a beacon: %+v", method, a)
			}
		}
		// A busy machine stretches single calls; only a request that is
		// over budget on every one of ten more tries is slow.
		for i := 0; i < 10 && took > matchBudget; i++ {
			start := time.Now()
			builtin.Request(r, body, shop)
			took = min(took, time.Since(start))
		}
		if took > matchBudget {
			t.Fatalf("classifying took %v", took)
		}
		// Reordering the parameters, repeated names included, never
		// changes the result.
		base, q, ok := strings.Cut(u, "?")
		if !ok || strings.ContainsAny(q, "#") {
			return
		}
		pairs := queryPairs(q)
		fwd := base + "?" + strings.Join(pairs, "&")
		for i, j := 0, len(pairs)-1; i < j; i, j = i+1, j-1 {
			pairs[i], pairs[j] = pairs[j], pairs[i]
		}
		rev := base + "?" + strings.Join(pairs, "&")
		x := builtin.Request(&consent.Request{Method: method, URL: fwd}, body, shop)
		y := builtin.Request(&consent.Request{Method: method, URL: rev}, body, shop)
		if !reflect.DeepEqual(x, y) {
			t.Fatalf("parameter order changed the result:\n%s = %+v\n%s = %+v", fwd, x, rev, y)
		}
	})
}
