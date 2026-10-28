// Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
// SPDX-License-Identifier: MIT

package tracker

import (
	"strings"
	"testing"

	"github.com/RhythmShahriar/echo/consent"
)

// Every ID the catalog names (params: id: and id_path:) is masked in
// any URL Echo shows, not only in a hit's id: TikTok's sdkid, LinkedIn's
// pid, Microsoft's ti, the Google Ads conversion ID in the path. The same
// short names on the site's own URLs are kept.
func TestCatalogIDsMaskedInURLs(t *testing.T) {
	for _, c := range []struct{ in, want string }{
		{"https://analytics.tiktok.com/i18n/pixel/events.js?sdkid=TESTBEDTT0001&lib=ttq",
			"https://analytics.tiktok.com/i18n/pixel/events.js?sdkid=••••0001&lib=ttq"},
		{"https://googleads.g.doubleclick.net/pagead/viewthroughconversion/100000001/?random=1",
			"https://googleads.g.doubleclick.net/pagead/viewthroughconversion/••••0001/?random=1"},
		{"https://www.google.com/pagead/1p-user-list/100000001/?random=1",
			"https://www.google.com/pagead/1p-user-list/••••0001/?random=1"},
		{"https://px.ads.linkedin.com/collect?pid=1234567&fmt=js", "https://px.ads.linkedin.com/collect?pid=••••4567&fmt=js"},
		{"https://bat.bing.com/action/0?ti=56781234&evt=pageLoad", "https://bat.bing.com/action/0?ti=••••1234&evt=pageLoad"},
		{"https://shop.test/?pid=1234567&a=55555&ti=9", "https://shop.test/?pid=1234567&a=55555&ti=9"},
	} {
		if got := consent.MaskURL(c.in, false); got != c.want {
			t.Errorf("MaskURL(%s)\n got %s\nwant %s", c.in, got, c.want)
		}
	}
}

// A custom tracker's params: id: is masked on its host too.
func TestCustomIDsMaskedInURLs(t *testing.T) {
	c := fresh(t)
	yml := strings.Replace(shopPixel, "  cookies: [_shpx]\n", "  cookies: [_shpx]\n  params:\n    id: [shop_acct]\n", 1)
	if err := c.AddCustom([]byte(yml)); err != nil {
		t.Fatal(err)
	}
	if got, want := consent.MaskURL("https://px.shop.example/collect?shop_acct=SHOP98765&e=view", false),
		"https://px.shop.example/collect?shop_acct=••••8765&e=view"; got != want {
		t.Errorf("got %s\nwant %s", got, want)
	}
}

// --show-ids keeps the IDs a path carries, as it keeps tid and the GA4
// cookie name: the Google Ads conversion ID shows whole. A credential in
// the same URL stays masked.
func TestShowIDsKeepsPathIDs(t *testing.T) {
	for _, c := range []struct{ in, want string }{
		{"https://googleads.g.doubleclick.net/pagead/viewthroughconversion/100000001/?random=1",
			"https://googleads.g.doubleclick.net/pagead/viewthroughconversion/100000001/?random=1"},
		{"https://www.google.com/pagead/1p-user-list/100000001/?random=1",
			"https://www.google.com/pagead/1p-user-list/100000001/?random=1"},
		{"https://www.google-analytics.com/g/collect?v=2&tid=G-TESTBED001", "https://www.google-analytics.com/g/collect?v=2&tid=G-TESTBED001"},
	} {
		if got := consent.MaskURL(c.in, true); got != c.want {
			t.Errorf("MaskURL(%s, show IDs)\n got %s\nwant %s", c.in, got, c.want)
		}
	}
	if got := consent.MaskCookieNames("_ga_TESTBED001", true); got != "_ga_TESTBED001" {
		t.Errorf("cookie name with show IDs: %s", got)
	}
}
