// Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
// SPDX-License-Identifier: MIT

package consent_test

import (
	"net/url"
	"strings"
	"testing"

	"github.com/RhythmShahriar/echo/consent"
)

// fakeToken is assembled at run time so no scanner mistakes the test for
// a leaked key.
var fakeToken = "tok" + "_live_" + strings.Repeat("9f3K", 6)

func TestMaskURL(t *testing.T) {
	hash := strings.Repeat("ab12", 16) // 64 hex
	for _, c := range []struct{ name, in, want string }{
		{"plain URL kept", "https://shop.test/menu?page=2", "https://shop.test/menu?page=2"},
		{"measurement and client IDs keep their last 4", "https://region1.google-analytics.com/g/collect?v=2&tid=G-AB12CD7Q2X&cid=1234567890.1700003021&en=page_view",
			"https://region1.google-analytics.com/g/collect?v=2&tid=••••7Q2X&cid=••••3021&en=page_view"},
		{"pixel id", "https://www.facebook.com/tr?id=123412345678&ev=PageView", "https://www.facebook.com/tr?id=••••5678&ev=PageView"},
		{"personal data", "https://www.facebook.com/tr?id=1&ud[em]=" + hash + "&em=a%40b.test&ph=%2B4915112345678&fn=ann",
			"https://www.facebook.com/tr?id=••••&ud[em]=" + consent.Hashed + "&em=" + consent.Hashed + "&ph=" + consent.Hashed + "&fn=" + consent.Hashed},
		{"a hash anywhere", "https://px.test/c?h=" + hash, "https://px.test/c?h=" + url.QueryEscape(consent.Hashed)},
		{"an email anywhere", "https://px.test/c?x=ann%40example.com", "https://px.test/c?x=" + url.QueryEscape("••••@example.com")},
		{"login in the URL", "https://ann:hunter2@shop.test/", "https://•••••@shop.test/"},
		{"token in the query", "https://shop.test/?token=" + fakeToken + "&a=1", "https://shop.test/?token=•••••&a=1"},
		{"short id hidden whole", "https://px.test/?uid=42", "https://px.test/?uid=••••"},
		{"not a URL", "no url here", "no url here"},
		{"empty", "", ""},
	} {
		if got := consent.MaskURL(c.in, false); got != c.want {
			t.Errorf("%s:\n got %s\nwant %s", c.name, got, c.want)
		}
	}
}

// The page URL a tag copies into dl (or dr, page_location...) carries
// the site's own token, encoded once or twice: none of its forms may be
// left.
func TestMaskURLNested(t *testing.T) {
	page := "https://shop.test/account?token=" + fakeToken + "&cid=99887766"
	once := url.QueryEscape(page)
	twice := url.QueryEscape(once)
	for _, param := range []string{"dl", "dr", "ref", "rl", "url", "u", "page_location"} {
		for _, enc := range []string{once, twice} {
			raw := "https://region1.google-analytics.com/g/collect?v=2&" + param + "=" + enc + "&en=page_view"
			got := consent.MaskURL(raw, false)
			for _, leak := range []string{fakeToken, url.QueryEscape(fakeToken), url.QueryEscape(url.QueryEscape(fakeToken)), "99887766"} {
				if strings.Contains(got, leak) {
					t.Errorf("%s: %q survived in %s", param, leak, got)
				}
			}
			if !strings.Contains(got, url.QueryEscape("https://shop.test/account?token=")) {
				t.Errorf("%s: the page URL went missing: %s", param, got)
			}
		}
	}
}

func TestMaskURLShowIDs(t *testing.T) {
	raw := "https://region1.google-analytics.com/g/collect?tid=G-AB12CD7Q2X&cid=1234.5678&uid=user-991&em=x&token=" + fakeToken
	got := consent.MaskURL(raw, true)
	want := "https://region1.google-analytics.com/g/collect?tid=G-AB12CD7Q2X&cid=1234.5678&uid=••••-991&em=" + consent.Hashed + "&token=•••••"
	if got != want {
		t.Errorf("show ids:\n got %s\nwant %s", got, want)
	}
}

func TestMaskID(t *testing.T) {
	for in, want := range map[string]string{"": "", "1234": "••••", "12345678": "••••5678", "G-AB12CD7Q2X": "••••7Q2X", "••••5678": "••••5678", "ünïcødé": "••••cødé"} {
		if got := consent.MaskID(in); got != want {
			t.Errorf("MaskID(%q) = %q; want %q", in, got, want)
		}
	}
}

func TestSafeText(t *testing.T) {
	for _, c := range []struct {
		in   string
		max  int
		want string
	}{
		{"Accept all", 0, "Accept all"},
		{"Reject\x1b[2J", 0, `Reject\x1b[2J`},
		{"a\u202eb", 0, `a\u202eb`},
		{"قبول الكل", 0, "قبول الكل"},
		{"Accept all cookies", 6, "Accept…"},
		{"bad\xffutf8", 0, `bad\xffutf8`},
		{"tab\tkept", 0, "tab\tkept"},
	} {
		if got := consent.SafeText(c.in, c.max); got != c.want {
			t.Errorf("SafeText(%q, %d) = %q; want %q", c.in, c.max, got, c.want)
		}
	}
}

// FuzzMask: any input masks without a panic; a token planted in a
// page URL nested in a beacon never survives, whatever surrounds it.
func FuzzMask(f *testing.F) {
	for _, s := range []string{"https://shop.test/?a=1", "https://u:p@h/?dl=https%253A%252F%252Fx", "%", "https://x/?ud[em]=a&em=", "#a=b", "http://[::1]:80/?id=1"} {
		f.Add(s, "&x=1")
	}
	f.Fuzz(func(t *testing.T, raw, extra string) {
		_ = consent.MaskURL(raw, false)
		_ = consent.MaskURL(raw, true)
		page := "https://shop.test/p?token=" + fakeToken + extra
		got := consent.MaskURL("https://px.test/c?dl="+url.QueryEscape(page), false)
		if strings.Contains(got, fakeToken) || strings.Contains(got, url.QueryEscape(fakeToken)) {
			t.Fatalf("token survived: %s", got)
		}
	})
}
