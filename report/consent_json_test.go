// Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
// SPDX-License-Identifier: MIT

package report

import (
	"bytes"
	"encoding/json"
	"reflect"
	"slices"
	"strings"
	"testing"

	"github.com/RhythmShahriar/echo/consent"
	"github.com/RhythmShahriar/echo/consent/consenttest"
)

// schemaOf parses kind's embedded schema.
func schemaOf(t *testing.T, kind Kind) map[string]any {
	t.Helper()
	raw, err := Schema(kind)
	if err != nil {
		t.Fatal(err)
	}
	var s map[string]any
	if err := json.Unmarshal(raw, &s); err != nil {
		t.Fatalf("%s schema: %v", kind, err)
	}
	return s
}

// decoded parses JSON output for the validator.
func decoded(t *testing.T, b []byte) any {
	t.Helper()
	var doc any
	if err := json.Unmarshal(b, &doc); err != nil {
		t.Fatalf("%v\n%s", err, b)
	}
	return doc
}

func TestConsentJSONValidates(t *testing.T) {
	schema := schemaOf(t, KindConsent)
	for _, name := range consenttest.Goldens {
		var buf bytes.Buffer
		if err := WriteConsentJSON(&buf, judgedResult(t, name), ""); err != nil {
			t.Fatal(err)
		}
		for _, p := range validate(schema, schema, decoded(t, buf.Bytes()), "$") {
			t.Errorf("%s: %s", name, p)
		}
		golden(t, "json/"+name+".json", buf.String())
		if name == "meta-ignores" {
			golden(t, "consent.envelope.json", buf.String())
		}
	}
	// The schema catches what the types can't hold.
	var buf bytes.Buffer
	r := judgedResult(t, "meta-ignores")
	r.Pages[0].Runs[0].Status = "exploded"
	r.Issues[0].Level = "fatal"
	if err := WriteConsentJSON(&buf, r, ""); err != nil {
		t.Fatal(err)
	}
	doc := decoded(t, buf.Bytes())
	doc.(map[string]any)["data"].(map[string]any)["pages"].([]any)[0].(map[string]any)["url"] = 7
	if got := validate(schema, schema, doc, "$"); len(got) != 3 {
		t.Errorf("broken result: %q", got)
	}
}

func TestConsentJSONEnvelope(t *testing.T) {
	for _, c := range []struct {
		name, failOn, verdict string
		complete              bool
		summary               ConsentSummary
	}{
		{"clean", "", VerdictOK, true, ConsentSummary{Pages: 1, Runs: 3, Trackers: 3, Held: 9}},
		{"meta-ignores", "", VerdictFindings, true, ConsentSummary{Errors: 2, Notices: 1, Pages: 1, Runs: 3, Trackers: 3, BeforeChoice: 1, AfterReject: 1, Held: 15}},
		{"inconclusive", "", VerdictError, false, ConsentSummary{Pages: 1, Runs: 3, InconclusiveRuns: 2}},
		{"compare-cur", "broke", VerdictFindings, true, ConsentSummary{Broke: 2, New: 1, Errors: 2, Pages: 1, Runs: 3, Trackers: 4, BeforeChoice: 1, Held: 9}},
	} {
		r := judgedResult(t, c.name)
		e := NewConsentEnvelope(r, c.failOn)
		e.Summary.Held = r.Network.Held // the network count is the golden's own
		c.summary.Held = r.Network.Held
		if e.Verdict != c.verdict || e.Complete != c.complete || e.Summary != c.summary {
			t.Errorf("%s: verdict %s complete %v summary %+v\nwant %s %v %+v", c.name, e.Verdict, e.Complete, e.Summary, c.verdict, c.complete, c.summary)
		}
		if e.Version != "1.2.0" || e.Kind != KindConsent || e.Data != any(r) {
			t.Errorf("%s: %s %s", c.name, e.Version, e.Kind)
		}
	}
	var buf bytes.Buffer
	if err := WriteConsentJSON(&buf, nil, ""); err != nil || !strings.Contains(buf.String(), `"kind": "consent"`) {
		t.Errorf("nil result: %v %s", err, buf.String())
	}
}

// The consent envelope keeps the 1.1 envelope's field names, order and
// types; its summary only adds counts after the shared seven.
func TestEnvelopeCompat(t *testing.T) {
	var buf bytes.Buffer
	if err := WriteConsentJSON(&buf, judgedResult(t, "meta-ignores"), ""); err != nil {
		t.Fatal(err)
	}
	keys := func(b []byte) []string {
		dec := json.NewDecoder(bytes.NewReader(b))
		var out []string
		depth := 0
		for {
			tok, err := dec.Token()
			if err != nil {
				return out
			}
			switch v := tok.(type) {
			case json.Delim:
				if v == '{' || v == '[' {
					depth++
				} else {
					depth--
				}
			case string:
				if depth == 1 && dec.More() {
					out = append(out, v)
					var skip json.RawMessage
					if err := dec.Decode(&skip); err != nil {
						return out
					}
				}
			}
		}
	}
	want := []string{"schema", "kind", "tool", "version", "complete", "summary", "verdict", "data"}
	if got := keys(buf.Bytes()); !slices.Equal(got, want) {
		t.Errorf("top-level keys %v", got)
	}
	var old bytes.Buffer
	if err := WriteJSON(&old, KindRun, Result{Run: sampleRun()}); err != nil {
		t.Fatal(err)
	}
	if got := keys(old.Bytes()); !slices.Equal(got, want) {
		t.Errorf("run envelope keys %v", got)
	}
	var env struct {
		Schema   string         `json:"schema"`
		Complete bool           `json:"complete"`
		Summary  map[string]any `json:"summary"`
	}
	if err := json.Unmarshal(buf.Bytes(), &env); err != nil {
		t.Fatal(err)
	}
	if env.Schema != "rhythm-echo/v1" {
		t.Errorf("schema %q", env.Schema)
	}
	for _, k := range []string{"broke", "new", "changed", "fixed", "errors", "warnings", "notices"} {
		if _, ok := env.Summary[k].(float64); !ok {
			t.Errorf("summary.%s is %T", k, env.Summary[k])
		}
	}
	shared := reflect.TypeFor[Summary]()
	cs := reflect.TypeFor[ConsentSummary]()
	for i := range 7 {
		if shared.Field(i).Tag != cs.Field(i).Tag || shared.Field(i).Type != cs.Field(i).Type {
			t.Errorf("summary field %d: %s vs %s", i, shared.Field(i).Tag, cs.Field(i).Tag)
		}
	}
}

// detectSamples are the three outcomes of consent detect.
func detectSamples() map[string]*consent.DetectResult {
	cookiebot := consent.CMPInfo{ID: "cookiebot", Name: "Cookiebot", Found: true, Present: true,
		DetectedBy: "selector:#CybotCookiebotDialog,global:Cookiebot", BannerAtMS: 810, RejectLayer: 1}
	accept := &consent.Choice{How: "button", Text: "Allow all", Selector: "#CybotCookiebotDialogBodyLevelButtonLevelOptinAllowAll", Layer: 1}
	reject := &consent.Choice{How: "button", Text: "Use necessary cookies only", Selector: "#CybotCookiebotDialogBodyButtonDecline", Layer: 1}
	shot := ".echo/consent/detect/2026-10-26T09-14-03-a1b2c3.png"
	browser := "Chrome for Testing 156.0.8078.47"
	return map[string]*consent.DetectResult{
		"found": {URL: "https://staging.shop.test/", CMP: cookiebot, Accept: accept, Reject: reject, Screenshot: shot, Browser: browser},
		"no-reject": {URL: "https://staging.shop.test/", CMP: consent.CMPInfo{ID: "onetrust", Name: "OneTrust", Found: true, Present: true,
			DetectedBy: "script:cdn.cookielaw.org", BannerAtMS: 1430, RejectLayer: 2},
			Accept:     &consent.Choice{How: "button", Text: "Accept all", Selector: "#onetrust-accept-btn-handler", Layer: 2},
			Screenshot: shot, Browser: browser},
		"no-banner": {URL: "https://shop.example/", CMP: consent.CMPInfo{ID: "cookiebot", Name: "Cookiebot", Present: true, DetectedBy: "global:Cookiebot"},
			Screenshot: shot, Browser: browser},
	}
}

func TestDetectTextGolden(t *testing.T) {
	for name, d := range detectSamples() {
		var buf bytes.Buffer
		if err := WriteConsentDetect(&buf, d, false, ConsentTextOptions{}); err != nil {
			t.Fatal(err)
		}
		golden(t, "detect-"+name+".txt", buf.String())
	}
	want := "Banner:  Cookiebot, found after 0.8s (by #CybotCookiebotDialog and window.Cookiebot)\n" +
		"Accept:  \"Allow all\" (#CybotCookiebotDialogBodyLevelButtonLevelOptinAllowAll), first layer\n" +
		"Reject:  \"Use necessary cookies only\" (#CybotCookiebotDialogBodyButtonDecline), first layer\n" +
		"Screenshot: .echo/consent/detect/2026-10-26T09-14-03-a1b2c3.png\n"
	var buf bytes.Buffer
	_ = WriteConsentDetect(&buf, detectSamples()["found"], false, ConsentTextOptions{})
	if buf.String() != want {
		t.Errorf("PRD 6.1:\n%s", buf.String())
	}
	buf.Reset()
	_ = WriteConsentDetect(&buf, &consent.DetectResult{}, false, ConsentTextOptions{BannerWait: 20e9})
	if buf.String() != "Banner:  none found within 20s\n" {
		t.Errorf("empty: %q", buf.String())
	}
	// Page text never reaches the terminal raw.
	buf.Reset()
	hostile := detectSamples()["found"]
	hostile.Accept.Text = "Allow\x1b[2J all\u202e"
	hostile.CMP.Name = "Evil\x07"
	_ = WriteConsentDetect(&buf, hostile, false, ConsentTextOptions{})
	if strings.ContainsAny(buf.String(), "\x1b\x07\u202e") {
		t.Errorf("raw controls: %q", buf.String())
	}
}

// verifySamples are the outcomes of consent verify.
func verifySamples() map[string]*ConsentVerify {
	h := func(c byte) string { return strings.Repeat(string(c), 56) + "0123b855" }
	files := func() []VerifyFile {
		return []VerifyFile{
			{Path: "consent.json", OK: true, Want: h('e'), Got: h('e')},
			{Path: "report.html", OK: true, Want: h('5'), Got: h('5')},
			{Path: "evidence/page-1/reject-banner.png", OK: true, Want: h('a'), Got: h('a')},
		}
	}
	changed := files()
	changed[1] = VerifyFile{Path: "report.html", Want: h('5'), Got: h('9')}
	missing := files()
	missing[2] = VerifyFile{Path: "evidence/page-1/reject-banner.png", Want: h('a')}
	dir := ".echo/consent/2026-10-26T09-20-41-c4d5e6"
	return map[string]*ConsentVerify{
		"good":    {Folder: dir, Files: files(), Signature: VerifySignature{Present: true, OK: true, Key: "team.pub"}},
		"changed": {Folder: dir, Files: changed, Signature: VerifySignature{Present: true, OK: true, Key: "team.pub"}},
		"missing": {Folder: dir, Files: missing},
		"no-key":  {Folder: dir, Files: files(), Signature: VerifySignature{Present: true}},
		"bad-sig": {Folder: dir, Files: files(), Signature: VerifySignature{Present: true, Key: "team.pub"}},
	}
}

func TestVerifyTextGolden(t *testing.T) {
	for name, v := range verifySamples() {
		var buf bytes.Buffer
		if err := WriteConsentVerify(&buf, v, false, ConsentTextOptions{}); err != nil {
			t.Fatal(err)
		}
		golden(t, "verify-"+name+".txt", buf.String())
	}
	var buf bytes.Buffer
	_ = WriteConsentVerify(&buf, verifySamples()["good"], false, ConsentTextOptions{})
	lines := strings.Split(strings.TrimSpace(buf.String()), "\n")
	if lines[0] != "ok  consent.json                       sha256 eeeeeeee...b855" ||
		lines[len(lines)-1] != "Verified: 3 files match, signed by team.pub." ||
		lines[len(lines)-2] != "Signature: good (ed25519, key team.pub)" {
		t.Errorf("good:\n%s", buf.String())
	}
	buf.Reset()
	_ = WriteConsentVerify(&buf, verifySamples()["changed"], false, ConsentTextOptions{})
	if !strings.Contains(buf.String(), "changed  report.html  want 55555555... got 99999999...\n") {
		t.Errorf("changed:\n%s", buf.String())
	}
	buf.Reset()
	_ = WriteConsentVerify(&buf, verifySamples()["missing"], false, ConsentTextOptions{})
	if !strings.Contains(buf.String(), "missing  evidence/page-1/reject-banner.png\n") ||
		!strings.HasSuffix(buf.String(), "Not verified: 1 missing; 2 files match.\n") {
		t.Errorf("missing:\n%s", buf.String())
	}
}

func TestCatalogDetectVerifySchemas(t *testing.T) {
	ToolVersion = "1.2.0"
	defer func() { ToolVersion = "dev" }()
	catalog := ConsentCatalog{
		CMPs: []consent.CMPRuleInfo{{ID: "cookiebot", Name: "Cookiebot", DetectedBy: "#CybotCookiebotDialog, window.Cookiebot",
			Accept: "#CybotCookiebotDialogBodyLevelButtonLevelOptinAllowAll", Reject: "#CybotCookiebotDialogBodyButtonDecline",
			StateFrom: "cookie CookieConsent", Sources: []string{"https://www.cookiebot.com/en/developer/"}}},
		Trackers: []consent.TrackerInfo{{ID: "meta-pixel", Name: "Meta Pixel", Vendor: "Meta", Category: consent.CatAdvertising,
			Need: consent.NeedNeeded, Matches: "facebook.com/tr, connect.facebook.net/*/fbevents.js", Fix: "Block the pixel until Accept.",
			Docs: "https://developers.facebook.com/docs/meta-pixel/implementation/gdpr", Sources: []string{"https://developers.facebook.com/docs/meta-pixel/"}}},
		Catalog: consent.DataInfo{Version: "1.2.0", SHA256: strings.Repeat("4b", 32), Count: 47},
		Rules:   consent.DataInfo{Version: "1.2.0", SHA256: strings.Repeat("9c", 32), Count: 9},
	}
	var cat bytes.Buffer
	if err := encodeJSON(&cat, NewEnvelope(KindConsentCatalog, catalog)); err != nil {
		t.Fatal(err)
	}
	outputs := map[Kind][]byte{KindConsentCatalog: cat.Bytes()}
	var det, ver bytes.Buffer
	if err := WriteConsentDetect(&det, detectSamples()["no-reject"], true, ConsentTextOptions{}); err != nil {
		t.Fatal(err)
	}
	if err := WriteConsentVerify(&ver, verifySamples()["changed"], true, ConsentTextOptions{}); err != nil {
		t.Fatal(err)
	}
	outputs[KindConsentDetect], outputs[KindConsentVerify] = det.Bytes(), ver.Bytes()
	for kind, out := range outputs {
		schema := schemaOf(t, kind)
		for _, p := range validate(schema, schema, decoded(t, out), "$") {
			t.Errorf("%s: %s", kind, p)
		}
		golden(t, string(kind)+".envelope.json", string(out))
	}
	// Every detect and verify outcome validates, with its verdict.
	for name, d := range detectSamples() {
		e := detectEnvelope(d)
		want := map[string]string{"found": VerdictOK, "no-reject": VerdictFindings, "no-banner": VerdictError}[name]
		if e.Verdict != want || e.Complete != (name != "no-banner") {
			t.Errorf("detect %s: %s %v", name, e.Verdict, e.Complete)
		}
		b, _ := json.Marshal(e)
		s := schemaOf(t, KindConsentDetect)
		if p := validate(s, s, decoded(t, b), "$"); len(p) > 0 {
			t.Errorf("detect %s: %q", name, p)
		}
	}
	for name, v := range verifySamples() {
		e := verifyEnvelope(v)
		want := map[string]string{"good": VerdictOK, "no-key": VerdictOK, "changed": VerdictFindings, "bad-sig": VerdictFindings, "missing": VerdictError}[name]
		if e.Verdict != want || e.Complete != (name != "missing") {
			t.Errorf("verify %s: %s %v", name, e.Verdict, e.Complete)
		}
		b, _ := json.Marshal(e)
		s := schemaOf(t, KindConsentVerify)
		if p := validate(s, s, decoded(t, b), "$"); len(p) > 0 {
			t.Errorf("verify %s: %q", name, p)
		}
	}
}
