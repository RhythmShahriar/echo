// Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
// SPDX-License-Identifier: MIT

package consent_test

import (
	"testing"

	"github.com/RhythmShahriar/echo/consent"
)

func TestPageName(t *testing.T) {
	r := &consent.Result{Pages: []consent.Page{{URL: "https://a.test/x/?utm=1"}, {URL: "https://a.test/y/"}}}
	if got := consent.PageName(r, &r.Pages[0]); got != "/x/" {
		t.Errorf("one host: %q", got)
	}
	r.Pages = append(r.Pages, consent.Page{URL: "https://B.test/x/"})
	for i, want := range []string{"a.test/x/", "a.test/y/", "b.test/x/"} {
		if got := consent.PageName(r, &r.Pages[i]); got != want {
			t.Errorf("page %d: %q, want %q", i, got, want)
		}
	}
	// --sites names keep their pages apart; hosts count within a site.
	s := &consent.Result{Pages: []consent.Page{{Site: "a", URL: "https://a.test/"}, {Site: "b", URL: "https://b.test/"}}}
	if got := consent.PageName(s, &s.Pages[1]); got != "/" {
		t.Errorf("sites: %q", got)
	}
	if got := consent.SiteIssueKey("b", consent.CheckBeforeChoice, "/", consent.RunNone, "ga4"); got != "consent/before-choice|b:/|none|ga4" {
		t.Errorf("site key %q", got)
	}
}

func TestPageMatches(t *testing.T) {
	for _, c := range []struct {
		want, page string
		ok         bool
	}{
		{"", "/x/", true},
		{"/x/", "/x/", true},
		{"/x/", "/y/", false},
		{"https://a.test/x/?q=1", "/x/", true},
		{"/x/", "a.test/x/", true},
		{"/x/", "a.test/y/", false},
		{"a.test/x/", "a.test/x/", true},
		{"a.test/x/", "b.test/x/", false},
		{"https://a.test/x/", "a.test/x/", true},
		{"https://a.test/x/", "b.test/x/", false},
	} {
		if got := consent.PageMatches(c.want, c.page); got != c.ok {
			t.Errorf("PageMatches(%q, %q) = %v", c.want, c.page, got)
		}
	}
}
