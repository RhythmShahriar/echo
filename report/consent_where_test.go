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
	for _, want := range []string{"(No choice, alpha /)\n", "(No choice, bravo /checkout)\n", "no consent banner showed (bravo /checkout)\n",
		"consent check of 2 sites (2 pages)\n"} {
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

// Pages on two hosts that share a path are named by host and path in
// the issue lines and the Markdown, the way the verdict names them.
func TestConsentIssueWhereTwoHosts(t *testing.T) {
	r := judgedResult(t, "meta-ignores")
	two := *r
	two.Pages = []consent.Page{r.Pages[0], r.Pages[0]}
	two.Pages[0].URL, two.Pages[1].URL = "https://a.shop.test/", "https://b.shop.test/"
	two.Issues = []consent.Issue{
		{Check: consent.CheckBeforeChoice, Level: model.Error, Page: "a.shop.test/", Run: consent.RunReject, Title: "Meta Pixel fired after Reject"},
		{Check: consent.CheckBeforeChoice, Level: model.Error, Page: "b.shop.test/", Run: consent.RunReject, Title: "Meta Pixel fired after Reject"},
	}
	out := consentTextOf(t, &two, ConsentTextOptions{Plain: true})
	for _, want := range []string{"(Reject, a.shop.test/)\n", "(Reject, b.shop.test/)\n"} {
		if !strings.Contains(out, want) {
			t.Errorf("missing %q in\n%s", want, out)
		}
	}
	md := consentMDOf(t, &two, ConsentMarkdownOptions{})
	for _, want := range []string{"| consent/before-choice | a.shop.test/ | Reject |", "| consent/before-choice | b.shop.test/ | Reject |"} {
		if !strings.Contains(md, want) {
			t.Errorf("Markdown lacks %q:\n%s", want, md)
		}
	}
}

// A CMP change names both CMPs in its title; the line doesn't repeat
// them as "was x, now y". Other changes keep their values.
func TestConsentCMPChangeOnce(t *testing.T) {
	r := judgedResult(t, "meta-ignores")
	r.Comparison = &consent.Comparison{Against: "base", Changes: []consent.Change{
		{Class: consent.ClassChanged, Key: "cmp|/||", Page: "/", Title: "The CMP changed from Complianz to Generic banner", Before: "complianz", After: "generic"},
		{Class: consent.ClassChanged, Key: "gcd|/|accept|", Page: "/", Run: consent.RunAccept, Title: "gcd on the first Google hit changed", Before: "13r3r3r3r5", After: "13p3p3p3p5"},
	}}
	out := consentTextOf(t, r, ConsentTextOptions{Plain: true})
	md := consentMDOf(t, r, ConsentMarkdownOptions{})
	for name, text := range map[string]string{"text": out, "Markdown": md} {
		if strings.Contains(text, "was complianz") || !strings.Contains(text, "The CMP changed from Complianz to Generic banner") {
			t.Errorf("%s: CMP change\n%s", name, text)
		}
		if !strings.Contains(text, "was 13r3r3r3r5, now 13p3p3p3p5") {
			t.Errorf("%s: gcd change lost its values\n%s", name, text)
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

// Pages of one check on two hosts are told apart everywhere a report
// titles a page: the JUnit suites and cases, the Markdown and HTML run
// titles; the header and HTML title name the hosts' count rather
// than the first page alone.
func TestConsentPagesOnTwoHosts(t *testing.T) {
	r := judgedResult(t, "meta-ignores")
	two := *r
	two.Pages = []consent.Page{r.Pages[0], r.Pages[0]}
	two.Pages[0].URL, two.Pages[1].URL = "https://c01.shop.test/", "https://c02.shop.test/"
	two.Issues = nil

	_, doc := consentJUnitOf(t, &two, testHash)
	if len(doc.Suites) != 2 || doc.Suites[0].Name != "c01.shop.test/" || doc.Suites[1].Name != "c02.shop.test/" {
		t.Fatalf("suites %+v", doc.Suites)
	}
	if c := doc.Suites[1].Cases[0].Name; c != "c02.shop.test/ · No choice" {
		t.Errorf("case %q", c)
	}

	out := consentTextOf(t, &two, ConsentTextOptions{Plain: true})
	if !strings.Contains(out, " - consent check of 2 pages on 2 hosts\n") || strings.Contains(out, "consent check of https://c01") {
		t.Errorf("header:\n%s", out)
	}
	html := consentHTMLOf(t, &two, ConsentHTMLOptions{})
	for _, want := range []string{"<title>Echo consent check of 2 pages on 2 hosts</title>", "c02.shop.test/ · Reject"} {
		if !strings.Contains(html, want) {
			t.Errorf("HTML lacks %q", want)
		}
	}
	if md := consentMDOf(t, &two, ConsentMarkdownOptions{Details: true}); !strings.Contains(md, "<summary>c02.shop.test/ · Reject</summary>") {
		t.Errorf("Markdown run titles:\n%s", md)
	}

	// One host keeps the path alone and the first URL with the count.
	one := two
	one.Pages = []consent.Page{r.Pages[0], r.Pages[0]}
	one.Pages[1].URL = "https://c01.shop.test/shop/"
	one.Pages[0].URL = "https://c01.shop.test/"
	_, doc = consentJUnitOf(t, &one, testHash)
	if doc.Suites[0].Name != "/" || doc.Suites[1].Name != "/shop/" {
		t.Errorf("one host: suites %q %q", doc.Suites[0].Name, doc.Suites[1].Name)
	}
	if out := consentTextOf(t, &one, ConsentTextOptions{Plain: true}); !strings.Contains(out, "consent check of https://c01.shop.test/ (2 pages)\n") {
		t.Errorf("one host header:\n%s", out)
	}
}
