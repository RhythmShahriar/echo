// Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
// SPDX-License-Identifier: MIT

package report

import (
	"bytes"
	"regexp"
	"strings"
	"testing"

	"github.com/RhythmShahriar/echo/consent"
	"github.com/RhythmShahriar/echo/consent/consenttest"
)

// consentMDOf runs WriteConsentMarkdown into a string.
func consentMDOf(t testing.TB, r *consent.Result, o ConsentMarkdownOptions) string {
	t.Helper()
	var buf bytes.Buffer
	if err := WriteConsentMarkdown(&buf, r, o); err != nil {
		t.Fatal(err)
	}
	return buf.String()
}

// testHash is a consent.json hash for the footers.
const testHash = "e3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b855"

func TestConsentMarkdownGoldens(t *testing.T) {
	for _, name := range consenttest.Goldens {
		golden(t, "md/"+name+".md", consentMDOf(t, judgedResult(t, name), ConsentMarkdownOptions{ConsentSHA256: testHash}))
		golden(t, "md/"+name+"-details.md", consentMDOf(t, judgedResult(t, name), ConsentMarkdownOptions{ConsentSHA256: testHash, Details: true}))
	}
}

// The PRD 9.5 lines, for the result the PRD shows.
func TestConsentMarkdownLines(t *testing.T) {
	out := consentMDOf(t, judgedResult(t, "meta-ignores"), ConsentMarkdownOptions{ConsentSHA256: testHash})
	for _, want := range []string{
		"## ♪ Echo consent\n\n",
		"**Findings.** 1 page, 3 runs, 3 trackers, policy advanced: Meta Pixel fired before the choice and after Reject.\n",
		"| Page | Banner | No choice | Accept | Reject |\n|---|---|---|---|---|\n",
		"| / | Complianz | ✗ Meta Pixel | Google Analytics 4, Google Ads, Meta Pixel | ✗ Meta Pixel (3 pages) |\n",
		"### Errors (2)\n\n| Check | Page | What | Fix |\n|---|---|---|---|\n| consent/before-choice | / | Meta Pixel fired before the visitor chose |",
		"### Notices (1)\n",
		"### Consent Mode\n\nDefault: v2, all four signals denied at 0.12s. Accept: update granted at 2.0s. Reject: update denied at 2.0s. gcd decoded with an unofficial mapping.\n",
		"_Evidence, not legal advice. Echo 1.2.0, Chrome 156.0.8078.47, 2026-10-26 09:20 UTC. Hits held in the browser. Hosts mapped with --map-host. consent.json sha256 e3b0c44298fc..._\n",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("missing %q in\n%s", want, out)
		}
	}
	// Without details: no IDs, no query strings, no hit URLs.
	for _, bad := range []string{"••••", "?", "facebook.com", "<details>"} {
		if strings.Contains(out, bad) {
			t.Errorf("%q without details:\n%s", bad, out)
		}
	}
	out = consentMDOf(t, judgedResult(t, "meta-ignores"), ConsentMarkdownOptions{Details: true})
	if !strings.Contains(out, "<details><summary>/ · Reject</summary>") || !strings.Contains(out, "3 hits \\(PageView on /, /shop/, /\\)") {
		t.Errorf("details:\n%s", out)
	}
	if strings.Contains(out, "consent.json sha256") {
		t.Errorf("a hash with none given:\n%s", out)
	}

	out = consentMDOf(t, judgedResult(t, "inconclusive"), ConsentMarkdownOptions{})
	for _, want := range []string{
		"**Couldn't check.** 1 page, 3 runs, 0 trackers, policy advanced: Accept and Reject couldn't be tested on 1 page.\n",
		"| / | none |  | ? no banner | ? no banner |\n",
		"### Couldn't check (2)\n\n| Page | Run | Why |\n|---|---|---|\n| / | Accept | no banner |\n",
		"\n" + noBannerHints[0] + "\n",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("inconclusive: missing %q in\n%s", want, out)
		}
	}
	out = consentMDOf(t, judgedResult(t, "compare-cur"), ConsentMarkdownOptions{})
	for _, want := range []string{
		"**Broke.** 1 page, 3 runs, 4 trackers, policy advanced: 2 broke, 1 new, 0 changed, 0 fixed against baseline.\n",
		"### Changes against baseline (3)\n\n| Change | What | Page | Run | Before and after |\n",
		"| BROKE | TikTok Pixel now fires before the choice | / | No choice |  |\n",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("compare: missing %q in\n%s", want, out)
		}
	}
	out = consentMDOf(t, judgedResult(t, "clean"), ConsentMarkdownOptions{})
	if !strings.Contains(out, "**All clear.** 1 page, 3 runs,") || strings.Contains(out, "### Errors") {
		t.Errorf("clean:\n%s", out)
	}
}

// A run a page didn't make reads "-"; --sites puts each site's matrix
// under its name, the worst site first.
func TestConsentMarkdownSites(t *testing.T) {
	r := judgedResult(t, "meta-ignores")
	clean := judgedResult(t, "clean")
	clean.Pages[0].Site = "aaa-clean"
	clean.Pages[0].Runs = clean.Pages[0].Runs[:2]
	r.Pages[0].Site = "zzz-broken"
	for i := range r.Issues {
		r.Issues[i].Site = "zzz-broken"
	}
	r.Pages = append(r.Pages, clean.Pages[0])
	out := consentMDOf(t, r, ConsentMarkdownOptions{})
	broken, cleanAt := strings.Index(out, "**zzz-broken**"), strings.Index(out, "**aaa-clean**")
	if broken < 0 || cleanAt < 0 || broken > cleanAt {
		t.Fatalf("site order:\n%s", out)
	}
	if !regexp.MustCompile(`\| / \| [^|]+ \| [^|]* \| [^|]* \| - \|\n`).MatchString(out[cleanAt:]) {
		t.Errorf("no - for the run the clean site didn't make:\n%s", out[cleanAt:])
	}
	if !strings.Contains(out, "| consent/before-choice | zzz-broken / |") {
		t.Errorf("issue page without its site:\n%s", out)
	}
}

func TestConsentMarkdownEscapes(t *testing.T) {
	r := judgedResult(t, "meta-ignores")
	hostile := "![x](https://evil/p.gif) @org/team <!-- hide"
	r.Pages[0].CMP.Name = hostile
	r.Issues[0].Title = hostile
	r.Issues[0].Fix = "click `x` | then <script>alert(1)</script>\nnew row"
	r.Pages[0].Runs[2].Choice.Text = "Deny | `all` [x](javascript:alert(1))"
	for _, o := range []ConsentMarkdownOptions{{}, {Details: true}} {
		out := consentMDOf(t, r, o)
		if strings.Contains(out, "![") || strings.Contains(out, "](") {
			t.Errorf("an image or link survived:\n%s", out)
		}
		if strings.Contains(out, "<!--") || strings.Contains(out, "hide") {
			t.Errorf("an HTML comment survived:\n%s", out)
		}
		if regexp.MustCompile(`@[A-Za-z]`).MatchString(out) {
			t.Errorf("a mention survived:\n%s", out)
		}
		if bad := unescapedScript(out); bad {
			t.Errorf("a script tag survived:\n%s", out)
		}
		checkTables(t, out)
	}
}

// unescapedScript reports a "<script" that no backslash or entity
// neutralizes.
func unescapedScript(s string) bool {
	for i := strings.Index(s, "<script"); i >= 0; {
		if i == 0 || s[i-1] != '\\' {
			return true
		}
		j := strings.Index(s[i+1:], "<script")
		if j < 0 {
			break
		}
		i += j + 1
	}
	return strings.Contains(strings.ToLower(s), "](javascript:")
}

// checkTables checks every table row has as many cells as its header,
// counting only pipes that no backslash escapes.
func checkTables(t testing.TB, out string) {
	t.Helper()
	want := -1
	for line := range strings.SplitSeq(out, "\n") {
		if !strings.HasPrefix(line, "|") {
			want = -1
			continue
		}
		n := cellCount(line)
		if want < 0 {
			want = n
			continue
		}
		if n != want {
			t.Errorf("row has %d cells, want %d: %q", n, want, line)
		}
	}
}

// cellCount counts the cells of a table row.
func cellCount(line string) int {
	pipes := 0
	for i := 0; i < len(line); i++ {
		switch line[i] {
		case '\\':
			i++
		case '|':
			pipes++
		}
	}
	return pipes - 1
}

func TestMarkdownCut(t *testing.T) {
	r := judgedResult(t, "meta-ignores")
	base := r.Issues[0]
	for i := range 400 {
		is := base
		is.Title = strings.Repeat("long title ", 10) + string(rune('a'+i%26))
		is.Key += string(rune('a' + i%26))
		r.Issues = append(r.Issues, is)
	}
	full := consentMDOf(t, r, ConsentMarkdownOptions{MaxBytes: 1 << 30})
	for _, limit := range []int{4096, 20000, 30001} {
		out := consentMDOf(t, r, ConsentMarkdownOptions{MaxBytes: limit, Details: true})
		if len(out) > limit {
			t.Errorf("limit %d: %d bytes", limit, len(out))
		}
		if !strings.HasSuffix(out, "\n"+markdownCutLine+"\n") {
			t.Errorf("limit %d: no cut line at the end:\n%s", limit, out[max(0, len(out)-300):])
		}
		body := strings.TrimSuffix(out, "\n"+markdownCutLine+"\n")
		body = strings.TrimSuffix(body, "</details>\n")
		if !strings.HasPrefix(full, body) && !strings.HasPrefix(consentMDOf(t, r, ConsentMarkdownOptions{MaxBytes: 1 << 30, Details: true}), body) {
			t.Errorf("limit %d: not cut at a line", limit)
		}
		if strings.Count(out, "<details>") != strings.Count(out, "</details>") {
			t.Errorf("limit %d: a details block left open", limit)
		}
		checkTables(t, out)
	}
	if strings.Contains(full, markdownCutLine) {
		t.Errorf("cut under the limit")
	}
	// The default is 900 KiB.
	if out := consentMDOf(t, r, ConsentMarkdownOptions{}); strings.Contains(out, markdownCutLine) || len(out) > defaultMarkdownMax {
		t.Errorf("default cut: %d bytes", len(out))
	}
}

func FuzzMarkdownReport(f *testing.F) {
	for _, s := range []string{"![x](https://evil/p.gif)", "<script>alert(1)</script>", "a|b|c", "`code`", "@org/team", "<!-- x", "[x](javascript:alert(1))", "\\|", "line\nbreak", "<scr<!-- -->ipt>"} {
		f.Add(s)
	}
	base := judgedResult(f, "meta-ignores")
	f.Fuzz(func(t *testing.T, s string) {
		r := consenttest.CloneResult(base)
		r.Pages[0].CMP.Name = s
		r.Pages[0].Site = s
		r.Issues[0].Title, r.Issues[0].Detail, r.Issues[0].Fix, r.Issues[0].Page, r.Issues[0].Site = s, s, s, s, s
		r.Issues[1].Tracker = s
		r.Pages[0].Runs[0].Hits[0].Name = s
		r.Pages[0].Runs[2].Choice.Text = s
		r.Inconclusive = append(r.Inconclusive, consent.Inconclusive{Page: s, Run: consent.RunAccept, Reason: consent.Reason(s), Detail: s})
		for _, o := range []ConsentMarkdownOptions{{}, {Details: true}} {
			out := consentMDOf(t, r, o)
			if unescapedScript(out) {
				t.Fatalf("unescaped script or javascript: link for %q:\n%s", s, out)
			}
			checkTables(t, out)
		}
	})
}
