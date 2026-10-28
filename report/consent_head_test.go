// Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
// SPDX-License-Identifier: MIT

package report

import (
	"encoding/xml"
	"strings"
	"testing"
	"time"

	"github.com/RhythmShahriar/echo/consent"
	"github.com/RhythmShahriar/echo/consent/consenttest"
	consentverdict "github.com/RhythmShahriar/echo/consent/verdict"
)

// --fail-on none changes only the exit code: a result with errors still
// reads Findings on the screen, in report.md and in report.html.
func TestConsentHeadFailOnNone(t *testing.T) {
	r := judgedResult(t, "meta-ignores")
	if got := r.ExitCode("none"); got != 0 {
		t.Fatalf("exit code with --fail-on none: %d", got)
	}
	golden(t, "text/meta-ignores-failon-none.txt", consentTextOf(t, r, ConsentTextOptions{FailOn: "none", Plain: true}))
	if out := consentTextOf(t, r, ConsentTextOptions{FailOn: "none", Quiet: true}); !strings.HasPrefix(out, "Findings - 2 errors, 0 warnings, 1 notice") {
		t.Errorf("text with --fail-on none:\n%s", out)
	}
	if out := consentMDOf(t, r, ConsentMarkdownOptions{FailOn: "none"}); !strings.Contains(out, "**Findings.** ") || strings.Contains(out, "All clear") {
		t.Errorf("markdown with --fail-on none:\n%s", out)
	}
	if out := consentHTMLOf(t, r, ConsentHTMLOptions{FailOn: "none"}); !strings.Contains(out, ">Findings<") || strings.Contains(out, ">All clear<") {
		t.Errorf("html with --fail-on none reads All clear")
	}
	// A notice alone stays All clear unless --fail-on counts it.
	adv := judgedResult(t, "advanced")
	for failOn, want := range map[string]string{"": consent.HeadClear, "none": consent.HeadClear, "notice": consent.HeadFindings} {
		if got := adv.Head(failOn); got != want {
			t.Errorf("advanced, --fail-on %q: %q, want %q", failOn, got, want)
		}
	}
	inc := judgedResult(t, "inconclusive")
	if got := inc.Head("none"); got != consent.HeadInconclusive {
		t.Errorf("inconclusive with --fail-on none: %q", got)
	}
}

// When the pages of a check span hosts, the Markdown matrix names each
// page by host and path, as the issue tables do.
func TestConsentMDMatrixHosts(t *testing.T) {
	r := consenttest.GoldenResult("meta-ignores")
	other := r.Pages[0]
	other.URL = strings.Replace(other.URL, "meta-ignores.consent.test", "other.consent.test", 1)
	other.FinalURL = other.URL
	r.Pages = append(r.Pages, other)
	p := consenttest.NewPlan(t, r.Pages[0].URL)
	p.Policy, p.Geo, p.Hold = r.Policy, r.Geo, r.Held
	consentverdict.Evaluator{NowFunc: func() time.Time { return consentNow }}.Evaluate(r, p)
	out := consentMDOf(t, r, ConsentMarkdownOptions{})
	for _, want := range []string{"\n| meta-ignores.consent.test/ | Complianz |", "\n| other.consent.test/ | Complianz |",
		"\nmeta-ignores.consent.test/: Default: v2", "\nother.consent.test/: Default: v2"} {
		if !strings.Contains(out, want) {
			t.Errorf("missing %q in\n%s", want, out)
		}
	}
}

// With no Consent Mode default, the line says what the Google hits
// carried: consent state only when one has a gcs.
func TestGoogleHitState(t *testing.T) {
	l := consent.GCDSignal{Letter: "l"}
	unset := &consent.GCD{AdStorage: l, AnalyticsStorage: l, AdUserData: l, AdPersonalization: l}
	page := func(g ...*consent.GoogleConsent) *consent.Page {
		run := consent.Run{Kind: consent.RunNone}
		for _, x := range g {
			run.Hits = append(run.Hits, consent.HitRef{Phase: consent.PhaseBefore, Tracker: "google-analytics", Google: x})
		}
		return &consent.Page{ConsentMode: &consent.ConsentMode{Present: true}, Runs: []consent.Run{run}}
	}
	for _, c := range []struct {
		pg   *consent.Page
		want string
	}{
		{page(&consent.GoogleConsent{GCD: "13l3l3l3l5", GCDDecoded: unset}), "Consent Mode: no default; Google hits carry gcd only, every signal unset"},
		{page(&consent.GoogleConsent{GCD: "13l3l3l3l5", GCDDecoded: unset}, &consent.GoogleConsent{GCS: "G100"}), "Consent Mode: no default; Google hits carry consent state"},
		{page(&consent.GoogleConsent{GCD: "13p3p3p3p5", GCDDecoded: &consent.GCD{}}), "Consent Mode: no default; Google hits carry gcd only, no gcs"},
		{page(&consent.GoogleConsent{}), "Consent Mode: no default; Google hits carry no consent state"},
	} {
		// A hit after the choice carries the choice's update: it doesn't
		// count.
		if c.pg.Runs[0].Hits[0].Google.GCS == "" {
			c.pg.Runs = append(c.pg.Runs, consent.Run{Kind: consent.RunAccept, Hits: []consent.HitRef{
				{Phase: consent.PhaseAfterAccept, Tracker: "google-analytics", Google: &consent.GoogleConsent{GCS: "G111"}}}})
		}
		if got := consentModeLine(c.pg); got != c.want {
			t.Errorf("got %q, want %q", got, c.want)
		}
	}
}

// A masked address and phone number in the start URL read as bullets on
// every output, like the token beside them, not as %E2%80%A2.
func TestConsentMaskedQueryReadable(t *testing.T) {
	token := "tok" + "_live_" + strings.Repeat("9f3K", 6)
	raw := "https://secret-url.shop.test/?token=" + token + "&email=buyer%40shop.test&phone=%2B" + "1555" + "7014" + "29"
	r := judgedResult(t, "clean")
	r.Pages[0].URL = consent.MaskURL(raw, false)
	r.Pages[0].FinalURL = r.Pages[0].URL
	want := "token=•••••&email=••••@shop.test&phone=••••(phone)"
	out := consentTextOf(t, r, ConsentTextOptions{Plain: true})
	golden(t, "text/masked-query-plain.txt", out)
	if !strings.Contains(out, want) {
		t.Errorf("text lacks %q:\n%s", want, out)
	}
	for name, s := range map[string]string{"text": out, "html": consentHTMLOf(t, r, ConsentHTMLOptions{})} {
		if strings.Contains(s, "%E2%80%A2") || strings.Contains(s, token) {
			t.Errorf("%s shows encoded bullets or the token", name)
		}
	}
}

// The JUnit <testsuites> time is the sum of its suites' times, the same
// measure as each suite's, never the check's shorter wall time.
func TestConsentJUnitTimes(t *testing.T) {
	r := judgedResult(t, "meta-ignores")
	r.Pages = append(r.Pages, r.Pages[0])
	r.DurationMS = 1000
	var b strings.Builder
	if err := WriteConsentJUnit(&b, r, ""); err != nil {
		t.Fatal(err)
	}
	var doc struct {
		Time   float64 `xml:"time,attr"`
		Suites []struct {
			Time float64 `xml:"time,attr"`
		} `xml:"testsuite"`
	}
	if err := xml.Unmarshal([]byte(b.String()[len(xml.Header):]), &doc); err != nil {
		t.Fatal(err)
	}
	sum := 0.0
	for _, s := range doc.Suites {
		sum += s.Time
	}
	if len(doc.Suites) != 2 || doc.Time != sum || doc.Time < doc.Suites[0].Time {
		t.Errorf("testsuites time %.3f, suites %+v", doc.Time, doc.Suites)
	}
}
