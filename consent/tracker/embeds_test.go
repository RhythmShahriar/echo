// Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
// SPDX-License-Identifier: MIT

package tracker

import (
	"testing"

	"github.com/RhythmShahriar/echo/consent"
)

func TestYouTubeNoCookieNeverCounts(t *testing.T) {
	for _, c := range []struct{ url, typ, want string }{
		{"https://www.youtube.com/embed/abc", "document", "youtube"},
		{"https://www.youtube.com/api/stats/qoe?event=streamingstats", "xhr", "youtube"},
		{"https://www.youtube-nocookie.com/embed/abc", "document", ""},
		{"https://www.youtube-nocookie.com/api/stats/qoe?event=streamingstats", "xhr", ""},
		{"https://www.youtube-nocookie.com/youtubei/v1/log_event?alt=json", "fetch", ""},
	} {
		h := builtin.Request(get(c.url, c.typ), nil, shop)
		switch {
		case c.want == "" && h != nil:
			t.Errorf("%s = %+v, want no tracker", c.url, h)
		case c.want != "" && (h == nil || h.Tracker != c.want || h.Category != consent.CatEmbed || h.Need != consent.NeedNeeded):
			t.Errorf("%s = %+v, want %s embed", c.url, h, c.want)
		}
	}
	if h := builtin.Request(get("https://www.youtube.com/embed/abc", "document"), nil, shop); h == nil || h.Kind != consent.HitFrame {
		t.Errorf("youtube embed kind = %+v, want frame", h)
	}
}

func TestVimeoDNT(t *testing.T) {
	if h := builtin.Request(get("https://player.vimeo.com/video/1", "document"), nil, shop); h == nil || h.Tracker != "vimeo" || h.Kind != consent.HitFrame {
		t.Errorf("vimeo = %+v", h)
	}
	for _, u := range []string{"https://player.vimeo.com/video/1?dnt=1", "https://player.vimeo.com/video/1?autoplay=1&dnt=1"} {
		if h := builtin.Request(get(u, "document"), nil, shop); h != nil {
			t.Errorf("%s = %+v, want no tracker", u, h)
		}
	}
}

func TestStripeIsNotATracker(t *testing.T) {
	for _, r := range []*consent.Request{
		get("https://js.stripe.com/v3/", "script"),
		get("https://m.stripe.network/inner.html", "document"),
		{Method: "POST", URL: "https://m.stripe.com/6", Type: "xhr"},
	} {
		h := builtin.Request(r, nil, shop)
		if h == nil || h.Tracker != "stripe" || h.Need != consent.NeedNo || h.Category != consent.CatNecessary {
			t.Errorf("%s = %+v, want stripe, need no", r.URL, h)
		}
		if builtin.Hold(r) {
			t.Errorf("%s is held", r.URL)
		}
	}
	for _, n := range []string{"__stripe_mid", "__stripe_sid"} {
		if h := builtin.Cookie(&consent.Cookie{Name: n, Domain: "shop.test"}); h == nil || h.Need != consent.NeedNo {
			t.Errorf("cookie %s = %+v, want need no", n, h)
		}
	}
}

// TestNecessaryNeverConsentNeeded holds every necessary entry and
// Google Fonts to consent no: they are listed as seen, never judged
// and never held.
func TestNecessaryNeverConsentNeeded(t *testing.T) {
	necessary := 0
	for _, e := range builtin.entries {
		if e.info.Category != consent.CatNecessary && e.info.ID != "google-fonts" {
			continue
		}
		necessary++
		if e.info.Need != consent.NeedNo {
			t.Errorf("%s is %s but needs consent %s", e.info.ID, e.info.Category, e.info.Need)
		}
		ff := readFixtures(t, e.info.ID)
		for _, set := range []reqFixtures{ff.Hits, ff.Scripts, ff.Frames} {
			for _, f := range set.Match {
				if builtin.Hold(f.request()) {
					t.Errorf("%s: %s is held", e.info.ID, f.URL)
				}
			}
		}
	}
	if necessary != 6 {
		t.Fatalf("%d necessary entries, want stripe, paypal, recaptcha, hcaptcha, turnstile and google-fonts", necessary)
	}
	// The loader refuses a necessary entry that asks for consent.
	entry := "id: pay\nname: Pay\nvendor: Pay\ncategory: necessary\nconsent: needed\nscripts: [{host: js.pay.example}]\nfix: x\ndocs: https://pay.example\nsources: [\"https://pay.example\"]\n"
	_, err := load(catalogOf(map[string]string{"pay.yaml": entry, "pay.fixtures.yaml": "scripts:\n  match: [\"https://js.pay.example/a.js\"]\n"}), "catalog")
	if err == nil || err.Error() != "catalog/pay.yaml:5: a necessary entry has consent: no" {
		t.Fatalf("err = %v", err)
	}
}

func TestGoogleFontsAndRecaptchaLookalikes(t *testing.T) {
	for _, c := range []struct{ url, typ, want string }{
		{"https://fonts.googleapis.com/css2?family=Inter", "stylesheet", "google-fonts"},
		{"https://www.gstatic.com/recaptcha/releases/abc/recaptcha__en.js", "script", "recaptcha"},
		{"https://www.google.com/recaptcha/api2/anchor?k=x", "document", "recaptcha"},
		{"https://maps.googleapis.com/maps/api/js?key=x", "script", "google-maps"},
	} {
		h := builtin.Request(get(c.url, c.typ), nil, shop)
		if h == nil || h.Tracker != c.want {
			t.Errorf("%s = %+v, want %s", c.url, h, c.want)
			continue
		}
		if builtin.Hold(get(c.url, c.typ)) {
			t.Errorf("%s is held", c.url)
		}
	}
}
