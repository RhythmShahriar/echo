// Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
// SPDX-License-Identifier: MIT

package consent_test

import (
	"net/url"
	"regexp"
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
// Text over 64 KiB is cut before it is masked: what is kept is still
// masked and nothing past the cut comes back; short text is untouched.
func TestMaskURLCap(t *testing.T) {
	mail := "a" + "@" + "shop.test"
	long := "https://x.test/?em=" + mail + "&" + strings.Repeat("q=v&", 20<<10) + "secret=" + strings.Repeat("z", 10)
	got := consent.MaskURL(long, false)
	if len(got) > 70<<10 || !strings.HasSuffix(got, "…") {
		t.Errorf("not cut: %d bytes", len(got))
	}
	if strings.Contains(got, mail) || strings.Contains(got, "secret=") {
		t.Error("the cut text isn't masked, or text past the cut came back")
	}
	if !strings.HasSuffix(got, "q=v…") {
		t.Errorf("not cut at a separator: %q", got[len(got)-20:])
	}
	short := "https://x.test/?q=v"
	if got := consent.MaskURL(short, false); got != short {
		t.Errorf("short URL changed: %q", got)
	}
}

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

// A phone number is masked however it arrives: %2B-encoded, with a
// literal '+' (which a query reads as a space) or nested in a beacon's
// page URL once or twice.
func TestMaskURLPhone(t *testing.T) {
	num := "4917" + "012345678"
	page := "https://shop.test/?phone=%2B" + num
	literal := "https://shop.test/?phone=+" + num
	for _, raw := range []string{
		"https://shop.test/?phone=%2B" + num,
		"https://shop.test/?phone=+" + num,
		"https://shop.test/?phone=%20" + num,
		"https://region1.google-analytics.com/g/collect?v=2&dl=" + url.QueryEscape(page),
		"https://region1.google-analytics.com/g/collect?v=2&dl=" + url.QueryEscape(url.QueryEscape(page)),
		"https://region1.google-analytics.com/g/collect?v=2&dl=" + url.QueryEscape(literal),
		"https://region1.google-analytics.com/g/collect?v=2&page_location=" + url.QueryEscape(url.QueryEscape(literal)),
	} {
		got := consent.MaskURL(raw, false)
		if strings.Contains(got, num) || strings.Contains(got, num[4:]) {
			t.Errorf("phone kept:\n in %s\nout %s", raw, got)
		}
		if !strings.Contains(got, "phone") {
			t.Errorf("the phone param went missing: %s", got)
		}
	}
	// Numbers that aren't phone shaped stay.
	if got := consent.MaskURL("https://shop.test/?page=+2&qty=12", false); got != "https://shop.test/?page=+2&qty=12" {
		t.Errorf("short numbers masked: %s", got)
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

// The catalog's own ID params mask on any host; a rule's short params
// and path IDs mask only on its hosts, so a site's own ?a=1 is kept.
func TestMaskURLCatalogIDs(t *testing.T) {
	consent.AddIDRule(consent.IDRule{Hosts: []string{"px.masktest.test"}, Suffixes: []string{".cdn.masktest.test"},
		Params: []string{"Acct"}, Path: regexp.MustCompile(`^/conv/([0-9]+)`)})
	for _, c := range []struct{ name, in, want string }{
		{"TikTok sdkid anywhere", "https://analytics.tiktok.com/i18n/pixel/events.js?sdkid=TESTBEDTT0001&lib=ttq",
			"https://analytics.tiktok.com/i18n/pixel/events.js?sdkid=••••0001&lib=ttq"},
		{"named ID params", "https://x.test/?pixel_code=PX12345678&idsite=1234567&marketerId=00ab12cd34&company_id=AbCdEf&txn_id=o1x2y3",
			"https://x.test/?pixel_code=••••5678&idsite=••••4567&marketerId=••••cd34&company_id=••••CdEf&txn_id=••••x2y3"},
		{"rule param on its host", "https://px.masktest.test/c?acct=987654321&e=1", "https://px.masktest.test/c?acct=••••4321&e=1"},
		{"rule param on a subdomain", "https://a.cdn.masktest.test/c?ACCT=987654321", "https://a.cdn.masktest.test/c?ACCT=••••4321"},
		{"rule param elsewhere kept", "https://shop.test/?acct=987654321", "https://shop.test/?acct=987654321"},
		{"rule path ID", "https://px.masktest.test/conv/100000001/?x=1", "https://px.masktest.test/conv/••••0001/?x=1"},
		{"rule path elsewhere kept", "https://shop.test/conv/100000001/", "https://shop.test/conv/100000001/"},
	} {
		if got := consent.MaskURL(c.in, false); got != c.want {
			t.Errorf("%s:\n got %s\nwant %s", c.name, got, c.want)
		}
	}
	// A path without exactly one group, or a rule without hosts, adds nothing.
	consent.AddIDRule(consent.IDRule{AnyHost: true, Path: regexp.MustCompile(`^/(a)/(b)`)})
	consent.AddIDRule(consent.IDRule{Params: []string{"zzq"}})
	if got := consent.MaskURL("https://y.test/a/b?zzq=123456789", false); got != "https://y.test/a/b?zzq=123456789" {
		t.Errorf("an ignored rule masked: %s", got)
	}
}

func TestMaskCookieNames(t *testing.T) {
	id := "TESTBED" + "001"
	for in, want := range map[string]string{
		"_ga_" + id:                         "_ga_••••D001",
		"_ga":                               "_ga",
		"_gac_x":                            "_gac_x",
		"_ga_" + id + "=GS1.1; _ga=GA1.1.1": "_ga_••••D001=GS1.1; _ga=GA1.1.1",
		"_ga_••••D001":                      "_ga_••••D001",
	} {
		if got := consent.MaskCookieNames(in, false); got != want {
			t.Errorf("MaskCookieNames(%q) = %q, want %q", in, got, want)
		}
	}
	if got := consent.MaskCookieNames("_ga_"+id, true); got != "_ga_"+id {
		t.Errorf("--show-ids: %q", got)
	}
}
