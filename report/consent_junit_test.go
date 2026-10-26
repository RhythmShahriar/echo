// Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
// SPDX-License-Identifier: MIT

package report

import (
	"bytes"
	"encoding/json"
	"encoding/xml"
	"strings"
	"testing"

	"github.com/RhythmShahriar/echo/consent"
	"github.com/RhythmShahriar/echo/consent/consenttest"
)

// consentJUnitOf runs WriteConsentJUnit into a string and checks it is
// well-formed XML.
func consentJUnitOf(t testing.TB, r *consent.Result, hash string) (string, consentJUnitSuites) {
	t.Helper()
	var buf bytes.Buffer
	if err := WriteConsentJUnit(&buf, r, hash); err != nil {
		t.Fatal(err)
	}
	var doc consentJUnitSuites
	if err := xml.Unmarshal(buf.Bytes(), &doc); err != nil {
		t.Fatalf("not XML: %v\n%s", err, buf.String())
	}
	return buf.String(), doc
}

func TestConsentJUnitGoldens(t *testing.T) {
	for _, name := range consenttest.Goldens {
		out, _ := consentJUnitOf(t, judgedResult(t, name), testHash)
		golden(t, "junit/"+name+".xml", out)
	}
}

func TestConsentJUnitNames(t *testing.T) {
	_, doc := consentJUnitOf(t, judgedResult(t, "meta-ignores"), testHash)
	if len(doc.Suites) != 1 {
		t.Fatalf("%d suites", len(doc.Suites))
	}
	s := doc.Suites[0]
	if s.Name != "/" || s.Tests != 3 || s.Failures != 2 || s.Properties == nil ||
		s.Properties.Property[0] != (junitProperty{"consent.json.sha256", testHash}) {
		t.Errorf("suite %+v", s)
	}
	want := []string{"/ · No choice", "/ · Accept", "/ · Reject"}
	for i, c := range s.Cases {
		if c.Name != want[i] || c.Classname != "rhythm-echo.consent" || c.Time != "14.100" {
			t.Errorf("case %d: %+v", i, c)
		}
	}
	if f := s.Cases[0].Failures; len(f) != 1 || f[0].Message != "consent/before-choice: Meta Pixel fired before the visitor chose" {
		t.Errorf("no choice failures %+v", f)
	}
	// The notice (meta-no-consent-api) isn't a failure; accept passes.
	if len(s.Cases[1].Failures) != 0 || s.Cases[1].Out == "" {
		t.Errorf("accept %+v", s.Cases[1])
	}
	if doc.Tests != 3 || doc.Failures != 2 || doc.Skipped != 0 {
		t.Errorf("totals %+v", doc)
	}
	// An ignored error doesn't fail.
	r := judgedResult(t, "meta-ignores")
	r.Issues[0].Ignored = &consent.Ignore{Check: r.Issues[0].Check, Until: "2099-01-01", Reason: "known"}
	if _, doc := consentJUnitOf(t, r, ""); doc.Failures != 1 || doc.Suites[0].Properties != nil {
		t.Errorf("ignored issue or empty hash: %+v", doc)
	}
}

func TestJUnitSkipped(t *testing.T) {
	_, doc := consentJUnitOf(t, judgedResult(t, "inconclusive"), testHash)
	s := doc.Suites[0]
	if s.Skipped != 2 || doc.Skipped != 2 {
		t.Fatalf("skipped %d, %d", s.Skipped, doc.Skipped)
	}
	for _, c := range s.Cases[1:] {
		if c.Skipped == nil || c.Skipped.Message != "inconclusive: no_banner" || !strings.Contains(c.Skipped.Text, "no banner showed") {
			t.Errorf("%s: %+v", c.Name, c.Skipped)
		}
	}
	if s.Cases[0].Skipped != nil {
		t.Errorf("the no-choice run was skipped")
	}
	// A page that needs no consent has no skipped run.
	if _, doc := consentJUnitOf(t, judgedResult(t, "no-trackers"), ""); doc.Skipped != 0 || doc.Failures != 0 {
		t.Errorf("no-trackers: %+v", doc)
	}
}

func TestConsentJUnitEscapes(t *testing.T) {
	r := judgedResult(t, "meta-ignores")
	r.Issues[0].Title = "]]><x/>\x1b[2J \"quoted\""
	r.Pages[0].Site = "<site>&"
	r.Issues[0].Site = "<site>&"
	out, doc := consentJUnitOf(t, r, testHash)
	if strings.Contains(out, "<x/>") || strings.Contains(out, "\x1b") {
		t.Errorf("unescaped:\n%s", out)
	}
	if doc.Suites[0].Name != "<site>& /" {
		t.Errorf("suite name %q", doc.Suites[0].Name)
	}
}

// catalogOf runs WriteConsentCatalog into a string.
func catalogOf(t testing.TB, kind string, asJSON bool, o ConsentTextOptions) string {
	t.Helper()
	var buf bytes.Buffer
	if err := WriteConsentCatalog(&buf, catalogCMPs, catalogTrackers, kind, asJSON, o); err != nil {
		t.Fatal(err)
	}
	return buf.String()
}

// catalogCMPs and catalogTrackers are rows as PRD 6.4 shows them.
var (
	catalogCMPs = []consent.CMPRuleInfo{
		{ID: "cookiebot", Name: "Cookiebot", DetectedBy: "#CybotCookiebotDialog, window.Cookiebot", Accept: "button, api", Reject: "first layer, api", StateFrom: "CookieConsent cookie",
			Sources: []string{"https://www.cookiebot.com/en/developer/"}},
		{ID: "onetrust", Name: "OneTrust", DetectedBy: "#onetrust-banner-sdk, window.OneTrust", Accept: "button, api", Reject: "first layer or settings, api", StateFrom: "OptanonConsent cookie"},
		{ID: "generic", Name: "Generic", DetectedBy: "buttons whose text means accept or reject (30 languages)", Accept: "button", Reject: "first layer or settings", StateFrom: "banner gone and stays gone"},
	}
	catalogTrackers = []consent.TrackerInfo{
		{ID: "ga4", Name: "Google Analytics 4", Category: consent.CatAnalytics, Need: consent.NeedNeeded, Matches: "/g/collect (any host with tid=G-), region*.google-analytics.com"},
		{ID: "plausible", Name: "Plausible", Category: consent.CatAnalytics, Need: consent.NeedExempt, Matches: "plausible.io/api/event (cookieless)"},
		{ID: "stripe", Name: "Stripe.js", Category: consent.CatNecessary, Need: consent.NeedNo, Matches: "js.stripe.com, m.stripe.network"},
	}
)

func TestConsentCatalogGoldens(t *testing.T) {
	consenttest.Use(t, consenttest.Fakes())
	for _, kind := range []string{"cmps", "trackers"} {
		golden(t, "catalog/"+kind+".txt", catalogOf(t, kind, false, ConsentTextOptions{}))
		golden(t, "catalog/"+kind+".json", catalogOf(t, kind, true, ConsentTextOptions{}))
	}
}

func TestConsentCatalogTables(t *testing.T) {
	consenttest.Use(t, consenttest.Fakes())
	out := catalogOf(t, "cmps", false, ConsentTextOptions{})
	lines := strings.Split(strings.TrimSuffix(out, "\n"), "\n")
	if !strings.HasPrefix(lines[0], "CMP        DETECTED BY") || !strings.HasSuffix(lines[0], "STATE FROM") {
		t.Errorf("header %q", lines[0])
	}
	gen := strings.Index(out, "generic    buttons whose text means accept or reject  ")
	next := strings.Index(out, "\n           (30 languages)\n")
	if gen < 0 || next < gen {
		t.Errorf("generic row:\n%s", out)
	}
	col := strings.Index(lines[0], "ACCEPT")
	for _, l := range lines[1:4] {
		if !strings.HasPrefix(l[col:], "button") {
			t.Errorf("ACCEPT column not aligned: %q", l)
		}
	}
	if want := "3 CMPs, rules version 1.2.0 (sha256 fake...). A banner none of these find: --accept-selector and --reject-selector."; lines[len(lines)-1] != want {
		t.Errorf("footer %q", lines[len(lines)-1])
	}
	out = catalogOf(t, "trackers", false, ConsentTextOptions{})
	for _, want := range []string{
		"TRACKER    NAME                CATEGORY   CONSENT  MATCHES\n",
		"\nplausible  Plausible           analytics  exempt   plausible.io/api/event (cookieless)\n",
		"\n3 trackers, catalogue version 1.2.0 (sha256 fake...). Add your own under consent: trackers:.\n", //nolint:misspell // PRD 6.4 wording
	} {
		if !strings.Contains(out, want) {
			t.Errorf("missing %q in\n%s", want, out)
		}
	}
	for _, l := range strings.Split(out, "\n") {
		if strings.HasSuffix(l, " ") {
			t.Errorf("trailing space: %q", l)
		}
	}
	var env struct {
		Kind string         `json:"kind"`
		Data ConsentCatalog `json:"data"`
	}
	if err := json.Unmarshal([]byte(catalogOf(t, "trackers", true, ConsentTextOptions{})), &env); err != nil || env.Kind != "consent-catalog" ||
		len(env.Data.Trackers) != 3 || env.Data.CMPs != nil || env.Data.Catalog.SHA256 != "fake" {
		t.Errorf("json %v %+v", err, env)
	}
	if err := WriteConsentCatalog(&bytes.Buffer{}, nil, nil, "rules", false, ConsentTextOptions{}); err == nil {
		t.Errorf("an unknown kind wrote")
	}
}

// Without linked stages the footer says the version is unknown; a
// hostile custom tracker can't move the terminal.
func TestConsentCatalogUnlinked(t *testing.T) {
	var buf bytes.Buffer
	evil := []consent.TrackerInfo{{ID: "x\x1b[2J", Name: "\u202eevil", Matches: "a\nb"}}
	if err := WriteConsentCatalog(&buf, nil, evil, "trackers", false, ConsentTextOptions{}); err != nil {
		t.Fatal(err)
	}
	out := buf.String()
	if strings.Contains(out, "x\x1b[2J") || strings.Contains(out, "\u202e") {
		t.Errorf("unsafe:\n%q", out)
	}
	if consent.Linked().Classifier == nil && !strings.Contains(out, "catalogue version unknown.") { //nolint:misspell // PRD 6.4 wording
		t.Errorf("footer:\n%s", out)
	}
}
