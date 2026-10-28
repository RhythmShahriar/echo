// Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
// SPDX-License-Identifier: MIT

package report

import (
	"strings"
	"testing"

	"github.com/RhythmShahriar/echo/consent"
	"github.com/RhythmShahriar/echo/model"
)

// An issue line names its run; with more than one page or site it names
// the site and the page too, so three runs' issues and a --sites check's
// issues can be told apart. The Markdown tables have a Run column.
func TestConsentIssueWhere(t *testing.T) {
	r := judgedResult(t, "meta-ignores")
	r.Issues = []consent.Issue{
		{Check: consent.CheckBeforeChoice, Level: model.Error, Page: "/", Run: consent.RunNone, Title: "Meta Pixel fired before the visitor chose"},
		{Check: consent.CheckBeforeChoice, Level: model.Error, Page: "/", Run: consent.RunReject, Title: "Meta Pixel fired before the visitor chose"},
	}
	out := consentTextOf(t, r, ConsentTextOptions{Plain: true})
	for _, want := range []string{"Meta Pixel fired before the visitor chose (No choice)\n", "Meta Pixel fired before the visitor chose (Reject)\n"} {
		if !strings.Contains(out, want) {
			t.Errorf("missing %q in\n%s", want, out)
		}
	}

	two := *r
	two.Pages = []consent.Page{r.Pages[0], r.Pages[0]}
	two.Pages[0].Site, two.Pages[1].Site = "alpha", "bravo"
	two.Pages[1].URL = "https://bravo.test/checkout"
	two.Issues = []consent.Issue{
		{Check: consent.CheckBeforeChoice, Level: model.Error, Site: "alpha", Page: "/", Run: consent.RunNone, Title: "Meta Pixel fired before the visitor chose"},
		{Check: consent.CheckBeforeChoice, Level: model.Error, Site: "bravo", Page: "/checkout", Run: consent.RunNone, Title: "Meta Pixel fired before the visitor chose"},
		{Check: consent.CheckNoBanner, Level: model.Warning, Site: "bravo", Page: "/checkout", Title: "Meta Pixel is on the page but no consent banner showed"},
	}
	out = consentTextOf(t, &two, ConsentTextOptions{Plain: true})
	for _, want := range []string{"(No choice, alpha /)\n", "(No choice, bravo /checkout)\n", "no consent banner showed (bravo /checkout)\n"} {
		if !strings.Contains(out, want) {
			t.Errorf("missing %q in\n%s", want, out)
		}
	}

	md := consentMDOf(t, &two, ConsentMarkdownOptions{})
	for _, want := range []string{"| Check | Page | Run | What | Fix |", "| consent/before-choice | alpha / | No choice | Meta Pixel fired before the visitor chose |",
		"| consent/no-banner | bravo /checkout |  | Meta Pixel is on the page"} {
		if !strings.Contains(md, want) {
			t.Errorf("Markdown lacks %q:\n%s", want, md)
		}
	}
}

// A result's browser never names a path in a shared report: an older
// result that kept one has it cut in the Markdown and the HTML.
func TestConsentBrowserNoPath(t *testing.T) {
	for in, want := range map[string]string{
		"Chrome 153.0.8010.52 (/home/someone/chrome/chrome)": "Chrome 153.0.8010.52",
		`Chrome 153.0.8010.52 (C:\Users\someone\chrome.exe)`: "Chrome 153.0.8010.52",
		"Chrome for Testing 156.0.1.2 (downloaded)":          "Chrome for Testing 156.0.1.2 (downloaded)",
		"Chrome 156.0.8078.47":                               "Chrome 156.0.8078.47",
	} {
		if got := browserName(in); got != want {
			t.Errorf("browserName(%q) = %q, want %q", in, got, want)
		}
	}
	r := judgedResult(t, "meta-ignores")
	r.Browser = "Chrome 153.0.8010.52 (/home/someone/chrome/chrome)"
	if md := consentMDOf(t, r, ConsentMarkdownOptions{}); strings.Contains(md, "someone") || !strings.Contains(md, "Chrome 153.0.8010.52, ") {
		t.Errorf("Markdown footer:\n%s", md)
	}
}
