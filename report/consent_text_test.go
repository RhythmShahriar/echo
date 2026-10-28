// Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
// SPDX-License-Identifier: MIT

package report

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"
	"time"
	"unicode"

	"github.com/RhythmShahriar/echo/consent"
	"github.com/RhythmShahriar/echo/consent/consenttest"
	consentverdict "github.com/RhythmShahriar/echo/consent/verdict"
	"github.com/RhythmShahriar/echo/model"
)

// consentText runs WriteConsentText into a string.
func consentTextOf(t *testing.T, r *consent.Result, o ConsentTextOptions) string {
	t.Helper()
	var buf bytes.Buffer
	if err := WriteConsentText(&buf, r, o); err != nil {
		t.Fatal(err)
	}
	return buf.String()
}

// textModes are the option sets every golden is written in.
var textModes = map[string]ConsentTextOptions{
	"default": {ReportPath: ".echo/consent/2026-10-26T09-20-41-c4d5e6/report.html"},
	"plain":   {Plain: true},
	"quiet":   {Quiet: true},
	"verbose": {Verbose: true},
	"first":   {FirstRun: true, ReportPath: ".echo/consent/2026-10-26T09-20-41-c4d5e6/report.html"},
}

func TestConsentTextGoldens(t *testing.T) {
	for _, name := range consenttest.Goldens {
		for mode, o := range textModes {
			golden(t, "text/"+name+"-"+mode+".txt", consentTextOf(t, judgedResult(t, name), o))
		}
	}
}

// The PRD 9.1 lines, for the results the PRD shows.
func TestConsentTextLines(t *testing.T) {
	out := consentTextOf(t, judgedResult(t, "meta-ignores"), ConsentTextOptions{ReportPath: "r.html"})
	for _, want := range []string{
		"rhythm echo 1.2.0 - consent check of https://meta-ignores.consent.test/\n\n",
		"\n" + hitsHeldLine + "\n",
		"\nhttps://meta-ignores.consent.test/\n  Banner: Complianz, found after 0.4s, Reject on the banner\n",
		"\n  Consent Mode: v2 default, all four signals denied at 0.12s\n",
		"\n  [1/3] No choice   ✗ Meta Pixel fired before the choice\n",
		"\n  [3/3] Reject      ✗ Meta Pixel fired after Reject on 3 of 3 pages",
		"\nFindings - 2 errors, 0 warnings, 1 notice - 3 runs, 3 trackers, policy advanced\n",
		"\n  error   consent/before-choice   Meta Pixel fired before the visitor chose (No choice)\n          3 hits (PageView on /, /shop/, /); cookie _fbp written\n          fix: ",
		"\n  notice  consent/meta-no-consent-api  Meta Pixel never received fbq('consent', ...) (No choice)\n",
		"\n\nEvidence, not legal advice.\nfull report: r.html\n",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("missing %q in\n%s", want, out)
		}
	}
	if strings.Contains(out, "Browser:") || strings.Contains(out, noteGCD) {
		t.Errorf("browser line or gcd note without -v or a first run:\n%s", out)
	}
	out = consentTextOf(t, judgedResult(t, "advanced"), ConsentTextOptions{FirstRun: true})
	for _, want := range []string{
		"\nBrowser: Chrome for Testing 156.0.8078.47\n",
		"[1/3] No choice   Google Analytics 4: 2 cookieless pings (gcs G100); no tracker cookies; Meta Pixel held back\n",
		"\nAll clear - 0 errors, 0 warnings, 1 notice - 3 runs, 3 trackers, policy advanced\n",
		"\n  notice  consent/cookieless-ping  Google Analytics 4 sent 2 cookieless pings before the choice (allowed under --consent-mode advanced) (No choice)\n",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("advanced: missing %q in\n%s", want, out)
		}
	}
	out = consentTextOf(t, judgedResult(t, "inconclusive"), ConsentTextOptions{})
	for _, want := range []string{
		"  Banner: none found within 10s (Cookiebot's script loaded)\n",
		"  [2/3] Accept      ? couldn't choose: no banner\n",
		"\nCouldn't check - 0 errors, 0 warnings, 0 notices - 3 runs, 0 trackers, policy advanced\n",
		"\n  ? /  Accept: no banner showed within 10s (Cookiebot's script loaded)\n",
		"\n  Many banners show only to visitors in some countries: try --geo de, or an EU --proxy.\n  A banner Echo doesn't know: --accept-selector and --reject-selector (consent detect helps).\n",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("inconclusive: missing %q in\n%s", want, out)
		}
	}
	out = consentTextOf(t, judgedResult(t, "no-trackers"), ConsentTextOptions{})
	if !strings.Contains(out, "  No banner and no trackers: nothing to consent to\n") || !strings.Contains(out, "\nAll clear - 0 errors") {
		t.Errorf("no-trackers:\n%s", out)
	}
}

func TestConsentTextComparison(t *testing.T) {
	out := consentTextOf(t, judgedResult(t, "compare-cur"), ConsentTextOptions{})
	for _, want := range []string{
		"rhythm echo 1.2.0 - consent check of https://c08.consent.test/ against baseline from 2026-10-19 08:02 UTC\n",
		"\nBroke - 2 broke, 1 new, 0 changed, 0 fixed - 3 runs, 4 trackers\n",
		"\n  BROKE   TikTok Pixel now fires before the choice            / (No choice, Reject)\n",
		"\n  NEW     TikTok Pixel fires after Accept                     /\n",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("missing %q in\n%s", want, out)
		}
	}
	if strings.Contains(out, "consent/before-choice") {
		t.Errorf("a comparison lists the issues:\n%s", out)
	}
	out = consentTextOf(t, judgedResult(t, "compare-cur"), ConsentTextOptions{Verbose: true, Quiet: true})
	if !strings.HasPrefix(out, "Broke - ") || !strings.Contains(out, "(key: tracker|/|accept|tiktok-pixel)") {
		t.Errorf("quiet verbose:\n%s", out)
	}
	r := judgedResult(t, "compare-cur")
	r.Comparison.Changes = []consent.Change{{Class: consent.ClassChanged, Key: "gcd|/|accept|", Page: "/", Run: consent.RunAccept,
		Title: "gcd on the first Google hit changed", Before: "13r3r3r3r5", After: "13p3p3p3p5", Accepted: true}}
	out = consentTextOf(t, r, ConsentTextOptions{})
	if !strings.Contains(out, "\nNo changes - 0 broke, 0 new, 0 changed, 0 fixed") ||
		!strings.Contains(out, "CHANGED gcd on the first Google hit changed: was 13r3r3r3r5, now 13p3p3p3p5 (accepted)  / (Accept)") {
		t.Errorf("accepted change:\n%s", out)
	}
}

func TestConsentTextQuiet(t *testing.T) {
	out := consentTextOf(t, judgedResult(t, "meta-ignores"), ConsentTextOptions{Quiet: true, ReportPath: "r.html"})
	lines := strings.Split(strings.TrimRight(out, "\n"), "\n")
	if !strings.HasPrefix(lines[0], "Findings - 2 errors") || strings.Contains(out, "Banner:") || strings.Contains(out, "full report") ||
		!strings.HasPrefix(lines[1], "  error   consent/before-choice") {
		t.Errorf("quiet:\n%s", out)
	}
	if out := consentTextOf(t, judgedResult(t, "clean"), ConsentTextOptions{Quiet: true}); out != "All clear - 0 errors, 0 warnings, 0 notices - 3 runs, 3 trackers, policy advanced\n" {
		t.Errorf("quiet clean: %q", out)
	}
}

func TestConsentTextPlain(t *testing.T) {
	out := consentTextOf(t, judgedResult(t, "meta-ignores"), ConsentTextOptions{Plain: true, Color: true})
	for _, want := range []string{"[1/3] No choice   FAIL Meta Pixel", "[2/3] Accept      ok ", "[3/3] Reject      FAIL Meta Pixel"} {
		if !strings.Contains(out, want) {
			t.Errorf("missing %q in\n%s", want, out)
		}
	}
	out += consentTextOf(t, judgedResult(t, "inconclusive"), ConsentTextOptions{Plain: true})
	if !strings.Contains(out, "[2/3] Accept      INCONCLUSIVE couldn't choose: no banner") || !strings.Contains(out, "  INCONCLUSIVE /  Accept:") {
		t.Errorf("plain inconclusive:\n%s", out)
	}
	if strings.ContainsAny(out, "✗\x1b") {
		t.Errorf("glyphs or color in plain output:\n%s", out)
	}
}

func TestConsentTextVerbose(t *testing.T) {
	out := consentTextOf(t, judgedResult(t, "advanced"), ConsentTextOptions{Verbose: true})
	for _, want := range []string{
		"\n     0.00s  load   https://g02.consent.test/\n",
		"\n    12.90s  reload https://g02.consent.test/\n",
		"  gtag   consent default  all four denied",
		"  hit    Google Analytics 4 page_view gcs=G100 gcd=",
		" cookieless (held)\n",
		"(key: consent/cookieless-ping|/|none|ga4)",
		noteGCD,
	} {
		if !strings.Contains(out, want) {
			t.Errorf("missing %q in\n%s", want, out)
		}
	}
	out = consentTextOf(t, judgedResult(t, "meta-ignores"), ConsentTextOptions{Verbose: true})
	for _, want := range []string{"  click  ", "  banner Complianz shown\n", "  page   /shop/ (link 1 of 1)\n", "  reload https://", "  cookie _fbp (Meta Pixel)\n", "  script "} {
		if !strings.Contains(out, want) {
			t.Errorf("missing %q in\n%s", want, out)
		}
	}
}

// With --sites, the site with the worst result comes first.
func TestSitesWorstFirst(t *testing.T) {
	r := &consent.Result{Echo: "1.2.0", Held: true, TLSVerified: true, Sandbox: true, Policy: consent.PolicyAdvanced}
	page := func(site, u string, inconclusive bool) consent.Page {
		run := consent.Run{Kind: consent.RunNone, Status: consent.StatusOK}
		if inconclusive {
			run.Kind, run.Status, run.Reason = consent.RunAccept, consent.StatusNoBanner, consent.ReasonNoBanner
		}
		return consent.Page{Site: site, URL: u, Runs: []consent.Run{run}}
	}
	r.Pages = []consent.Page{page("alpha", "https://alpha.test/", false), page("bravo", "https://bravo.test/", true),
		page("charlie", "https://charlie.test/", false), page("delta", "https://delta.test/", false), page("echo", "https://echo.test/", false)}
	r.Issues = []consent.Issue{
		{Check: consent.CheckEmbed, Level: model.Warning, Site: "delta", Page: "/", Run: consent.RunNone, Title: "YouTube loaded before the visitor chose"},
		{Check: consent.CheckBeforeChoice, Level: model.Error, Site: "charlie", Page: "/", Run: consent.RunNone, Title: "Meta Pixel fired before the visitor chose"},
		{Check: consent.CheckBeforeChoice, Level: model.Error, Site: "echo", Page: "/", Run: consent.RunNone, Title: "x", Ignored: &consent.Ignore{}},
	}
	out := consentTextOf(t, r, ConsentTextOptions{})
	var order []string
	for _, l := range strings.Split(out, "\n") {
		if l != "" && unicode.IsLower(rune(l[0])) && !strings.ContainsAny(l, " :") {
			order = append(order, l)
		}
	}
	if got := strings.Join(order, " "); got != "charlie delta bravo alpha echo" {
		t.Errorf("order %q\n%s", got, out)
	}
}

// Page-supplied text with terminal controls never reaches the output
// raw, in any mode.
func TestConsentTerminalSafe(t *testing.T) {
	r := judgedResult(t, "meta-ignores")
	evil := "\x1b]8;;https://evil.test\x07click\x1b]8;;\x07\u202eevil\u0085\x9b2J"
	r.Pages[0].URL = "https://meta-ignores.consent.test/" + evil
	r.Pages[0].CMP.Name = "Complianz" + evil
	for i := range r.Pages[0].Runs {
		run := &r.Pages[0].Runs[i]
		if run.Choice != nil {
			run.Choice.Text, run.Choice.Selector = "Deny"+evil, "#deny"+evil
		}
		for j := range run.Cookies {
			run.Cookies[j].Name += evil
		}
		for j := range run.Visits {
			run.Visits[j].URL += evil
		}
		for j := range run.Hits {
			run.Hits[j].Name += evil
		}
	}
	r.Issues[0].Title += evil
	r.Issues[0].Detail += evil
	r.Inconclusive = []consent.Inconclusive{{Page: "/" + evil, Run: consent.RunAccept, Reason: consent.ReasonNoButton, Detail: evil}}
	r.Notes = append(r.Notes, "note"+evil)
	for mode, o := range textModes {
		out := consentTextOf(t, r, o)
		if bad := rawControl(out); bad != "" {
			t.Errorf("%s: raw %q in\n%s", mode, bad, out)
		}
	}
}

// rawControl returns the first control a terminal would act on, or "".
func rawControl(s string) string {
	for _, r := range s {
		switch {
		case r == '\n' || r == '\t':
		case r < 0x20, r == 0x7f, r >= 0x80 && r <= 0x9f, isBidiControl(r):
			return string(r)
		}
	}
	return ""
}

func FuzzTerminalLine(f *testing.F) {
	f.Add("Cookiebot", "Reject all", "https://shop.test/", "_ga")
	f.Add("\x1b[2J", "\x07\u202e", "https://x/\x9b", "\xff\xfe")
	f.Add("\u2066evil\u2069", "a\u0085b", "javascript:alert(1)", "\x1b]0;title\x07")
	base := judgedResult(f, "meta-ignores")
	f.Fuzz(func(t *testing.T, cmpName, button, rawURL, cookie string) {
		r := consenttestClone(t, base)
		r.Pages[0].CMP.Name, r.Pages[0].URL = cmpName, rawURL
		for i := range r.Pages[0].Runs {
			run := &r.Pages[0].Runs[i]
			if run.Choice != nil {
				run.Choice.Text = button
			}
			for j := range run.Cookies {
				run.Cookies[j].Name = cookie
			}
		}
		r.Issues[0].Detail = button + cookie
		for _, o := range []ConsentTextOptions{{}, {Verbose: true, Plain: true}} {
			var buf bytes.Buffer
			if err := WriteConsentText(&buf, r, o); err != nil {
				t.Fatal(err)
			}
			if bad := rawControl(buf.String()); bad != "" {
				t.Fatalf("raw %q", bad)
			}
		}
	})
}

// consenttestClone deep-copies a result for one fuzz case.
func consenttestClone(t *testing.T, r *consent.Result) *consent.Result {
	t.Helper()
	b, err := json.Marshal(r)
	if err != nil {
		t.Fatal(err)
	}
	var out consent.Result
	if err := json.Unmarshal(b, &out); err != nil {
		t.Fatal(err)
	}
	return &out
}

func TestReportRecordsSandboxOff(t *testing.T) {
	r := judgedResult(t, "clean")
	r.Sandbox = false
	consentEvaluate(t, r)
	out := consentTextOf(t, r, ConsentTextOptions{})
	if !strings.Contains(out, "\nChrome ran without its sandbox.\n") {
		t.Errorf("terminal:\n%s", out)
	}
	var buf bytes.Buffer
	if err := WriteConsentJSON(&buf, r, ""); err != nil || !strings.Contains(buf.String(), `"Chrome ran without its sandbox."`) || !strings.Contains(buf.String(), `"sandbox": false`) {
		t.Errorf("json: %v", err)
	}
	if out := consentMDOf(t, r, ConsentMarkdownOptions{}); !strings.Contains(out, " Chrome ran without its sandbox. ") {
		t.Errorf("markdown:\n%s", out)
	}
	if out := consentHTMLOf(t, r, ConsentHTMLOptions{}); !strings.Contains(out, `<li class="flag warn">sandbox off</li>`) || !strings.Contains(out, "<dt>Sandbox</dt><dd>off</dd>") {
		t.Errorf("html doesn't mark the sandbox off")
	}
}

func TestConsentInsecureMarked(t *testing.T) {
	r := judgedResult(t, "clean")
	r.TLSVerified = false
	consentEvaluate(t, r)
	for mode, o := range textModes {
		if mode == "quiet" {
			continue // -q keeps only the verdict and the issues
		}
		if out := consentTextOf(t, r, o); !strings.Contains(out, "\nTLS not verified.\n") {
			t.Errorf("%s:\n%s", mode, out)
		}
	}
	var buf bytes.Buffer
	if err := WriteConsentJSON(&buf, r, ""); err != nil || !strings.Contains(buf.String(), `"TLS not verified."`) {
		t.Errorf("json: %v", err)
	}
	if out := consentMDOf(t, r, ConsentMarkdownOptions{}); !strings.Contains(out, " TLS not verified. ") {
		t.Errorf("markdown:\n%s", out)
	}
	if out := consentHTMLOf(t, r, ConsentHTMLOptions{}); !strings.Contains(out, `<li class="flag warn">TLS not verified</li>`) || !strings.Contains(out, "<dt>TLS</dt><dd>not verified</dd>") {
		t.Errorf("html doesn't mark TLS not verified")
	}
}

func TestConsentTextWraps(t *testing.T) {
	r := judgedResult(t, "meta-ignores")
	out := consentTextOf(t, r, ConsentTextOptions{Width: 60})
	for _, l := range strings.Split(out, "\n") {
		if strings.HasPrefix(l, "          ") && len([]rune(l)) > 60 && strings.Count(strings.TrimSpace(l), " ") > 0 {
			t.Errorf("line over 60 columns: %q", l)
		}
	}
	if !strings.Contains(out, "          fix: ") {
		t.Errorf("no fix line:\n%s", out)
	}
	// A fix the issue before already gave reads "as above.".
	r.Issues[1].Fix = r.Issues[0].Fix
	if out := consentTextOf(t, r, ConsentTextOptions{}); !strings.Contains(out, "\n          fix: as above.\n") {
		t.Errorf("no as above:\n%s", out)
	}
	var buf bytes.Buffer
	if err := WriteConsentText(&buf, nil, ConsentTextOptions{}); err != nil || !strings.Contains(buf.String(), "All clear") {
		t.Errorf("nil result: %v %q", err, buf.String())
	}
}

func TestConsentIssuePathDropsQuery(t *testing.T) {
	r := &consent.Result{Pages: []consent.Page{{URL: "https://shop.test/shop/?utm_source=mail", Runs: []consent.Run{{Kind: consent.RunNone}}}},
		Issues: []consent.Issue{{Check: "consent/before-choice", Page: "/shop/", Run: consent.RunNone, Level: model.Error}}}
	ct := &consentText{w: &strings.Builder{}, r: r, tn: trackerNames(r)}
	if got := ct.runIssues(&r.Pages[0], &r.Pages[0].Runs[0]); len(got) != 1 {
		t.Errorf("issues of a page with a query: %+v", got)
	}
	r.Issues[0].Page = "/other/"
	if got := ct.runIssues(&r.Pages[0], &r.Pages[0].Runs[0]); len(got) != 0 {
		t.Errorf("issue of another page matched: %+v", got)
	}
	if p := consentPagePath("https://shop.test/a?b=1"); p != "/a?b=1" {
		t.Errorf("shown path %q", p)
	}
	h := &consentHTML{t: ct, r: r}
	if h.pageIndex("", "/shop/") != 0 || h.pageIndex("", "/shop/?utm_source=mail") != -1 {
		t.Error("pageIndex doesn't match on the path alone")
	}
}

// With --sites an inconclusive run names its site as issues do. A
// withdraw run whose banner couldn't be reopened says so, not "no
// button found" with a --reject-selector hint.
func TestInconclusiveSiteAndReopen(t *testing.T) {
	r := consenttest.GoldenResult("clean")
	r.Pages[0].Site = "brew"
	r.Pages[0].Runs = append(r.Pages[0].Runs, consent.Run{Kind: consent.RunWithdraw, Status: consent.StatusNoButton,
		Reason: consent.ReasonNoButton, Choice: &consent.Choice{How: "button", Verified: true},
		Detail: "consent: Cookiebot has no way to reopen the banner without its API; --click button,api allows it"})
	p := consenttest.NewPlan(t, r.Pages[0].URL)
	consentverdict.Evaluator{NowFunc: func() time.Time { return consentNow }}.Evaluate(r, p)
	if len(r.Inconclusive) != 1 || r.Inconclusive[0].Site != "brew" {
		t.Fatalf("inconclusive %+v", r.Inconclusive)
	}
	text := consentTextOf(t, r, ConsentTextOptions{Plain: true})
	if !strings.Contains(text, "INCONCLUSIVE brew /  Withdraw: Accept worked but the banner couldn't be reopened to withdraw: Cookiebot has no way") ||
		strings.Contains(text, "--reject-selector") {
		t.Errorf("text:\n%s", text)
	}
	md := consentMDOf(t, r, ConsentMarkdownOptions{})
	if !strings.Contains(md, "| brew / | Withdraw | the banner couldn't be reopened |") {
		t.Errorf("markdown:\n%s", md)
	}
}

// verify -q prints only what failed, the signature and the verdict.
func TestConsentVerifyQuiet(t *testing.T) {
	h := strings.Repeat("ab", 32)
	v := &ConsentVerify{Files: []VerifyFile{
		{Path: "consent.json", OK: true, Want: h, Got: h},
		{Path: "report.html", Want: h, Got: strings.Repeat("cd", 32)},
	}}
	var full, quiet bytes.Buffer
	if err := WriteConsentVerify(&full, v, false, ConsentTextOptions{}); err != nil {
		t.Fatal(err)
	}
	if err := WriteConsentVerify(&quiet, v, false, ConsentTextOptions{Quiet: true}); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(full.String(), "ok  consent.json") {
		t.Errorf("full:\n%s", full.String())
	}
	want := "changed  report.html  want abababab... got cdcdcdcd...\nSignature: none\nNot verified: 1 changed; 1 file match.\n"
	if quiet.String() != want {
		t.Errorf("quiet:\n%s\nwant\n%s", quiet.String(), want)
	}
}
