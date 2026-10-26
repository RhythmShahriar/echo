// Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
// SPDX-License-Identifier: MIT

package browse

import (
	"net/url"
	"slices"
	"strings"
	"testing"

	"github.com/RhythmShahriar/echo/consent"
	"github.com/RhythmShahriar/echo/consent/consenttest"
)

// The geo table covers the 27 EU states, GB, NO, IS, CH, US, CA, BR, AU,
// JP and IN, each with a language tag and an IANA zone.
func TestGeoTable(t *testing.T) {
	eu := []string{"AT", "BE", "BG", "HR", "CY", "CZ", "DK", "EE", "FI", "FR", "DE", "GR", "HU", "IE", "IT", "LV", "LT", "LU",
		"MT", "NL", "PL", "PT", "RO", "SK", "SI", "ES", "SE"}
	if len(eu) != 27 {
		t.Fatalf("%d EU states in the test", len(eu))
	}
	want := append(eu, "GB", "NO", "IS", "CH", "US", "CA", "BR", "AU", "JP", "IN")
	slices.Sort(want)
	if got := GeoCountries(); !slices.Equal(got, want) {
		t.Errorf("GeoCountries = %v", got)
	}
	for _, cc := range want {
		g, ok := GeoFor(strings.ToLower(cc))
		if !ok || !localeTag.MatchString(g.Locale) || !strings.Contains(g.Timezone, "/") {
			t.Errorf("%s: %+v %v", cc, g, ok)
		}
	}
	for in, want := range map[string]string{"de": "Europe/Berlin", " UK ": "Europe/London", "el": "Europe/Athens"} {
		if g, ok := GeoFor(in); !ok || g.Timezone != want {
			t.Errorf("GeoFor(%q) = %+v, %v", in, g, ok)
		}
	}
	for _, in := range []string{"", "XX", "ZZ", "deu"} {
		if _, ok := GeoFor(in); ok {
			t.Errorf("GeoFor(%q) found a country", in)
		}
	}
}

// --locale and --timezone win over --geo; --geo wins over the machine.
func TestPlanLocaleAndTimezone(t *testing.T) {
	t.Setenv("LC_ALL", "")
	t.Setenv("LC_MESSAGES", "")
	t.Setenv("LANG", "en_GB.UTF-8")
	p := consenttest.NewPlan(t)
	for _, c := range []struct{ geo, locale, tz, wantLocale, wantTZ string }{
		{"de", "", "", "de-DE", "Europe/Berlin"},
		{"de", "fr-FR", "", "fr-FR", "Europe/Berlin"},
		{"de", "", "Asia/Tokyo", "de-DE", "Asia/Tokyo"},
		{"", "", "", "en-GB", ""},
		{"xx", "", "", "en-GB", ""},
	} {
		p.Geo, p.Locale, p.Timezone = c.geo, c.locale, c.tz
		if l, tz := planLocale(p), planTimezone(p); l != c.wantLocale || tz != c.wantTZ {
			t.Errorf("%+v: %s %s", c, l, tz)
		}
	}
}

func TestUserAgent(t *testing.T) {
	p := consenttest.NewPlan(t)
	p.Version = "1.2.0"
	headless := "Mozilla/5.0 (X11; Linux x86_64) AppleWebKit/537.36 (KHTML, like Gecko) HeadlessChrome/153.0.0.0 Safari/537.36"
	if got := userAgent(p, headless, false); got != "Mozilla/5.0 (X11; Linux x86_64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/153.0.0.0 Safari/537.36 rhythm-echo-consent/1.2.0" {
		t.Errorf("desktop: %q", got)
	}
	if got := userAgent(p, headless, true); got != "Mozilla/5.0 (Linux; Android 10; K) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/153.0.0.0 Mobile Safari/537.36 rhythm-echo-consent/1.2.0" {
		t.Errorf("mobile: %q", got)
	}
	p.Version = ""
	if got := userAgent(p, "X", false); got != "X rhythm-echo-consent/dev" {
		t.Errorf("no version: %q", got)
	}
	p.UserAgent = " custom/1 "
	if got := userAgent(p, headless, true); got != "custom/1" {
		t.Errorf("-A: %q", got)
	}
}

func TestEmulation(t *testing.T) {
	p := consenttest.NewPlan(t)
	p.Locale, p.Timezone = "de-DE", "Europe/Berlin"
	e := newEmulation(p, consent.RunGPC, "HeadlessChrome/153.0.1.2", "153.0.1.2")
	if e.width != 1366 || e.height != 900 || e.dpr != 1 || e.mobile || !e.gpc || e.acceptLang != "de-DE,de;q=0.9" || e.timezone != "Europe/Berlin" {
		t.Errorf("desktop gpc: %+v", e)
	}
	if e.meta == nil || e.meta.Brands[0].Brand != "Chromium" || e.meta.Brands[0].Version != "153" || e.meta.FullVersion != "153.0.1.2" {
		t.Errorf("client hints: %+v", e.meta)
	}
	for _, b := range e.meta.Brands {
		if strings.Contains(b.Brand, "Headless") {
			t.Errorf("brand %q", b.Brand)
		}
	}
	p.Viewport = "Mobile"
	m := newEmulation(p, consent.RunAccept, "x", "153.0.1.2")
	if m.width != 390 || m.height != 844 || m.dpr != 3 || !m.mobile || m.gpc || m.platform != "Linux armv8l" || !m.meta.Mobile || m.meta.Platform != "Android" {
		t.Errorf("mobile: %+v %+v", m, m.meta)
	}
	if got := acceptLanguage("en"); got != "en" {
		t.Errorf("acceptLanguage(en) = %q", got)
	}
}

// --cookie pairs become host-only cookies for the target; anything that
// isn't name=value is skipped.
func TestCookieParams(t *testing.T) {
	u, _ := url.Parse("https://shop.test:8443/start?x=1")
	got := cookieParams([]string{"a=1", " b = two ; Path=/x", "novalue", "=nameless", "c="}, u)
	if len(got) != 3 {
		t.Fatalf("%d cookies: %+v", len(got), got)
	}
	for _, c := range got {
		if c.URL != "https://shop.test:8443/" || !c.Secure || c.Domain != "" {
			t.Errorf("%s: url %s secure %v domain %q", c.Name, c.URL, c.Secure, c.Domain)
		}
	}
	if got[1].Name != "b" || got[1].Value != "two" || got[2].Value != "" {
		t.Errorf("cookies %+v %+v", got[1], got[2])
	}
	if cookieParams([]string{"a=1"}, nil) != nil {
		t.Error("cookies without a target")
	}
}

// --map-host prefers an exact host over a wildcard and the longest
// wildcard over a shorter one; addresses are host:port or a URL.
func TestMapper(t *testing.T) {
	m := newMapper(map[string]string{
		"*.shop.test":     "127.0.0.1:35071",
		"*.cdn.shop.test": "http://127.0.0.1:35072/base/",
		"www.shop.test":   "127.0.0.1:35073",
		"bad.test":        "ftp://x",
		"empty.test":      "",
	})
	for host, want := range map[string]string{
		"www.shop.test": "http://127.0.0.1:35073", "a.shop.test": "http://127.0.0.1:35071",
		"img.cdn.shop.test": "http://127.0.0.1:35072/base", "WWW.SHOP.TEST": "http://127.0.0.1:35073",
	} {
		ok, base := m.lookup(host)
		if !ok || base.String() != want {
			t.Errorf("lookup(%s) = %v %v; want %s", host, ok, base, want)
		}
	}
	for _, host := range []string{"shop.test", "bad.test", "empty.test", "other.test"} {
		if ok, _ := m.lookup(host); ok {
			t.Errorf("lookup(%s) mapped", host)
		}
	}
	if !m.any() || newMapper(nil).any() {
		t.Error("any")
	}
	var nilMapper *mapper
	if ok, _ := nilMapper.lookup("x"); ok {
		t.Error("nil mapper mapped")
	}
}
