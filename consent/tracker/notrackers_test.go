// Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
// SPDX-License-Identifier: MIT

package tracker

import (
	"io/fs"
	"path"
	"regexp"
	"slices"
	"strings"
	"testing"

	"github.com/RhythmShahriar/echo/consent"
)

// specNotTrackers are the names the lane spec (T8) says are never
// trackers, one example per pattern.
var specNotTrackers = []string{
	"CookieConsent", "OptanonConsent", "OptanonAlertBoxClosed", "cookieyes-consent", "cmplz_marketing", "borlabs-cookie",
	"euconsent-v2", "didomi_token", "_iub_cs-12345678", "uc_settings", "CookieScriptConsent", "cookielawinfo-checkbox-analytics",
	"PHPSESSID", "JSESSIONID", "laravel_session", "XSRF-TOKEN", "wordpress_logged_in_0123456789abcdef", "wp-settings-1",
	"wordpress_test_cookie", "woocommerce_cart_hash", "wp_woocommerce_session_0123456789abcdef", "__cf_bm", "cf_clearance", "__cfruid",
}

// denyFixtures reads _not_trackers.fixtures.yaml.
func denyFixtures(t *testing.T) notFixtures {
	t.Helper()
	b, err := fs.ReadFile(catalogFS, path.Join(catalogDir, notTrackersFile+".fixtures.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	var f notFixtures
	if _, err := decodeStrict(b, &f); err != nil {
		t.Fatal(err)
	}
	return f
}

func TestNotTrackers(t *testing.T) {
	f := denyFixtures(t)
	for _, n := range specNotTrackers {
		if !slices.ContainsFunc(f.Cookies.NoMatch, func(s nameSpec) bool { return s.Name == n }) {
			t.Errorf("%s is not in cookies.no_match of %s.fixtures.yaml", n, notTrackersFile)
		}
	}
	for _, n := range f.Cookies.NoMatch {
		for _, domain := range []string{"", ".shop.test", "shop.test"} {
			if h := builtin.Cookie(&consent.Cookie{Name: n.Name, Domain: domain}); h != nil {
				t.Errorf("cookie %s on %q = %+v", n.Name, domain, h)
			}
		}
		// No entry claims it on its own either: the deny list is a
		// second line, not the only thing keeping it out.
		for _, e := range builtin.entries {
			one, err := newCatalog([]*entry{e})
			if err != nil {
				t.Fatal(err)
			}
			if h := one.Cookie(&consent.Cookie{Name: n.Name}); h != nil {
				t.Errorf("%s claims %s", e.info.ID, n.Name)
			}
		}
	}
	for _, n := range f.Storage.NoMatch {
		if h := builtin.Storage(&consent.StorageKey{Key: n.Name}); h != nil {
			t.Errorf("storage %s = %+v", n.Name, h)
		}
	}
	for _, n := range f.Cookies.Match {
		if builtin.Cookie(&consent.Cookie{Name: n.Name}) == nil {
			t.Errorf("cookie %s is no longer a tracker", n.Name)
		}
	}
}

func TestPrefixLongestWins(t *testing.T) {
	if h := builtin.Cookie(&consent.Cookie{Name: "_hjSessionUser_123"}); h == nil || h.Tracker != "hotjar" {
		t.Fatalf("_hjSessionUser_123 = %+v", h)
	}
	// Two entries: a generic prefix and a longer one, in both id orders.
	for _, ids := range [][2]string{{"aa-generic", "zz-specific"}, {"zz-generic", "aa-specific"}} {
		files := map[string]string{}
		for i, pat := range []string{"_hj*", "_hjSessionUser_*"} {
			id := ids[i]
			e := strings.ReplaceAll(minimalEntry, "shop-pixel", id)
			e = strings.Replace(e, "px.shop.example", "px"+id+".example", 1)
			e = strings.Replace(e, "cookies: [_shpx]", `cookies: ["`+pat+`"]`, 1)
			fx := strings.ReplaceAll(minimalFixtures, "px.shop.example", "px"+id+".example")
			fx = strings.Replace(fx, "match: [_shpx]", "match: ["+strings.Replace(pat, "*", "1", 1)+"]", 1)
			files[id+".yaml"], files[id+".fixtures.yaml"] = e, fx
		}
		c, err := load(catalogOf(files), "catalog")
		if err != nil {
			t.Fatal(err)
		}
		for name, want := range map[string]string{"_hjSessionUser_123": ids[1], "_hjid": ids[0], "_hj": ids[0]} {
			if h := c.Cookie(&consent.Cookie{Name: name}); h == nil || h.Tracker != want {
				t.Errorf("%v: %s = %+v, want %s", ids, name, h, want)
			}
		}
	}
}

func TestUnknownCookieNil(t *testing.T) {
	k := &consent.Cookie{Name: "my_shop_pref", Domain: ".shop.test"}
	if h := builtin.Cookie(k); h != nil {
		t.Fatalf("my_shop_pref = %+v", h)
	}
	if h := builtin.Storage(&consent.StorageKey{Key: "my_shop_pref"}); h != nil {
		t.Fatalf("storage my_shop_pref = %+v", h)
	}
	if builtin.Cookie(nil) != nil || builtin.Storage(nil) != nil {
		t.Fatal("nil classified")
	}
	// A tracker cookie comes back as a new Hit; the cookie is unchanged.
	k = &consent.Cookie{Name: "_ga", Domain: ".shop.test"}
	before := *k
	h := builtin.Cookie(k)
	if h == nil || h.Tracker != "ga4" || h.Need != consent.NeedNeeded || h.Category != consent.CatAnalytics || *k != before {
		t.Fatalf("_ga = %+v, cookie now %+v", h, k)
	}
}

// denyCatalog is the minimal entry with a deny list.
func denyCatalog(entry, deny, denyFix string) map[string]string {
	m := map[string]string{"shop-pixel.yaml": entry, "shop-pixel.fixtures.yaml": minimalFixtures}
	if deny != "" {
		m["_not_trackers.yaml"] = deny
	}
	if denyFix != "" {
		m["_not_trackers.fixtures.yaml"] = denyFix
	}
	return m
}

func TestDenyList(t *testing.T) {
	const deny = "cookies: [PHPSESSID, \"cmplz_*\", _shpx_banner]\nstorage: [didomi_token]\n"
	const denyFix = "cookies:\n  match: [_shpx]\n  no_match: [PHPSESSID, cmplz_marketing, _shpx_banner]\nstorage:\n  no_match: [didomi_token]\n"
	// A broad entry pattern loads; the deny list still wins over it.
	broad := strings.Replace(minimalEntry, "cookies: [_shpx]", `cookies: ["_shpx*"]`, 1)
	c, err := load(catalogOf(denyCatalog(broad, deny, denyFix)), "catalog")
	if err != nil {
		t.Fatal(err)
	}
	if h := c.Cookie(&consent.Cookie{Name: "_shpx_banner"}); h != nil {
		t.Errorf("_shpx_banner = %+v, want none (denied)", h)
	}
	if h := c.Cookie(&consent.Cookie{Name: "_shpx_id"}); h == nil || h.Tracker != "shop-pixel" {
		t.Errorf("_shpx_id = %+v", h)
	}
	for _, c := range []struct {
		name, entry, deny, denyFix, want string
	}{
		{"entry claims a denied name", strings.Replace(minimalEntry, "cookies: [_shpx]", "cookies: [_shpx, PHPSESSID]", 1), deny, denyFix,
			`catalog/shop-pixel.yaml:9: cookies: "PHPSESSID" is listed in _not_trackers.yaml as never a tracker`},
		{"entry pattern inside a denied one", strings.Replace(minimalEntry, "cookies: [_shpx]", `cookies: [_shpx, "cmplz_m*"]`, 1), deny, denyFix,
			`cookies: "cmplz_m*" is listed in _not_trackers.yaml`},
		{"no_match is a tracker", minimalEntry, deny, strings.Replace(denyFix, "no_match: [PHPSESSID", "no_match: [_shpx, PHPSESSID", 1),
			`catalog/_not_trackers.fixtures.yaml:3: cookies.no_match "_shpx" is classified as shop-pixel`},
		{"match is no tracker", minimalEntry, deny, strings.Replace(denyFix, "match: [_shpx]", "match: [_shop]", 1),
			`catalog/_not_trackers.fixtures.yaml:2: cookies.match "_shop" is no tracker: the deny list reaches too far`},
		{"denied match", minimalEntry, "cookies: [\"_shp*\"]\n", "cookies:\n  match: [_shpx]\n  no_match: [_shpa]\n",
			`cookies: "_shpx" is listed in _not_trackers.yaml`},
		{"domain", minimalEntry, "cookies: [{name: PHPSESSID, domain: shop.test}]\n", denyFix,
			`catalog/_not_trackers.yaml:1: cookies: "PHPSESSID": the deny list holds names only, never a domain`},
		{"twice", minimalEntry, "cookies: [PHPSESSID, PHPSESSID]\n", denyFix, `cookies: "PHPSESSID" is listed twice`},
		{"unknown key", minimalEntry, "cookies: [PHPSESSID]\nhosts: [x]\n", denyFix, `catalog/_not_trackers.yaml:2: unknown key "hosts"`},
		{"no fixtures", minimalEntry, deny, "", "catalog/_not_trackers.yaml: no fixtures: add _not_trackers.fixtures.yaml"},
		{"no no_match", minimalEntry, deny, "cookies:\n  match: [_shpx]\n", "needs at least one cookies.match and one cookies.no_match fixture"},
		{"no storage no_match", minimalEntry, deny, "cookies:\n  match: [_shpx]\n  no_match: [PHPSESSID]\n", "needs at least one storage.no_match fixture"},
		{"empty", minimalEntry, "storage: [x]\n", denyFix, "the deny list needs cookies"},
	} {
		_, err := load(catalogOf(denyCatalog(c.entry, c.deny, c.denyFix)), "catalog")
		if err == nil || !strings.Contains(err.Error(), c.want) {
			t.Errorf("%s: err = %v\nwant %s", c.name, err, c.want)
		}
	}
	files := denyCatalog(minimalEntry, "", "")
	files["_other.yaml"] = deny
	if _, err := load(catalogOf(files), "catalog"); err == nil || !strings.Contains(err.Error(), "catalog/_other.yaml: only _not_trackers.yaml may start with _") {
		t.Errorf("_other.yaml: err = %v", err)
	}
}

// denyLineRe is how every deny list error starts.
var denyLineRe = regexp.MustCompile(`^catalog/_not_trackers(\.fixtures)?\.yaml:[0-9]+: `)

func FuzzNotTrackersYAML(f *testing.F) {
	b, _ := fs.ReadFile(catalogFS, path.Join(catalogDir, notTrackersFile+".yaml"))
	fb, _ := fs.ReadFile(catalogFS, path.Join(catalogDir, notTrackersFile+".fixtures.yaml"))
	f.Add(b, fb)
	f.Add([]byte("cookies: [PHPSESSID, PHPSESSID]\n"), fb)
	f.Add([]byte("cookies: [{name: x, domain: y}]\n"), []byte("cookies: {match: [a]}"))
	f.Add([]byte(""), []byte(""))
	f.Fuzz(func(t *testing.T, deny, fixtures []byte) {
		d, err := loadDeny("catalog/_not_trackers.yaml", deny, "catalog/_not_trackers.fixtures.yaml", fixtures)
		if err != nil {
			if !denyLineRe.MatchString(err.Error()) {
				t.Fatalf("error without a line: %q", err)
			}
			return
		}
		if len(d.fixtures.Cookies.Match) == 0 || len(d.fixtures.Cookies.NoMatch) == 0 {
			t.Fatal("a deny list loaded without cookie fixtures")
		}
		// Whatever it holds, the built-in entries and the list together
		// either load or refuse with a line; they never panic.
		c := &Catalog{entries: builtin.entries, cookies: builtin.cookies, storage: builtin.storage, deny: d}
		if err := c.checkDeny(); err != nil && !regexp.MustCompile(`^catalog/[^:]+:[0-9]+: `).MatchString(err.Error()) {
			t.Fatalf("error without a line: %q", err)
		}
	})
}
