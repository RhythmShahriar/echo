// Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
// SPDX-License-Identifier: MIT

package cli

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/RhythmShahriar/echo/consent"
	"github.com/RhythmShahriar/echo/consent/consenttest"
)

// useDetect registers the fakes with a runner whose Detect returns d.
func useDetect(t *testing.T, d *consent.DetectResult) *consenttest.FakeRunner {
	t.Helper()
	runner := &consenttest.FakeRunner{Detected: d}
	s := consenttest.Fakes()
	s.Runner = runner
	consenttest.Use(t, s)
	fakeWriters(t)
	return runner
}

// detected is a detect result: a banner when found, with the buttons
// named.
func detected(found bool, buttons ...consent.RunKind) *consent.DetectResult {
	d := &consent.DetectResult{URL: "https://shop.test/?token=" + "abcdef" + "123456", Screenshot: "2026-10-26T09-20-41-a1b2c3.png",
		CMP: consent.CMPInfo{ID: "cookiebot", Name: "Cookiebot", Found: found, DetectedBy: "selector:#CybotCookiebotDialog"}}
	for _, k := range buttons {
		c := &consent.Choice{How: "button", Selector: "#" + string(k), Layer: 1}
		if k == consent.RunAccept {
			d.Accept = c
		} else {
			d.Reject = c
		}
	}
	return d
}

func TestDetectExitCodes(t *testing.T) {
	for _, tc := range []struct {
		name string
		d    *consent.DetectResult
		want int
	}{
		{"both buttons", detected(true, consent.RunAccept, consent.RunReject), exitOK},
		{"no reject", detected(true, consent.RunAccept), exitFindings},
		{"no accept", detected(true, consent.RunReject), exitFindings},
		{"no banner", detected(false), exitUnavailable},
	} {
		t.Run(tc.name, func(t *testing.T) {
			inDir(t, "")
			runner := useDetect(t, tc.d)
			code, out, errOut := runMain(t, "detect", "shop.test", "--geo", "de", "--cmp", "cookiebot")
			if code != tc.want {
				t.Errorf("exit %d, want %d\n%s%s", code, tc.want, out, errOut)
			}
			if len(runner.Plans) != 1 {
				t.Fatalf("plans: %d", len(runner.Plans))
			}
			p := runner.Plans[0]
			if filepath.Base(p.Dir) != "detect" || !strings.HasPrefix(p.ID, "2026-10-26T09-20-41-") || len(p.ID) != len("2026-10-26T09-20-41-a1b2c3") ||
				p.Geo != "de" || p.CMP != "cookiebot" || !p.Hold || p.Targets[0].URL.String() != "https://shop.test/" {
				t.Errorf("plan: dir %s id %s geo %s cmp %s hold %v", p.Dir, p.ID, p.Geo, p.CMP, p.Hold)
			}
			// The screenshot is shown under the store, the URL masked.
			if !strings.Contains(out, "screenshot=.echo/consent/detect/2026-10-26T09-20-41-a1b2c3.png") || strings.Contains(out, "abcdef123456") {
				t.Errorf("output: %s", out)
			}
		})
	}
}

func TestDetectJSON(t *testing.T) {
	inDir(t, "")
	useDetect(t, detected(true, consent.RunAccept, consent.RunReject))
	code, out, _ := runMain(t, "detect", "--json", "https://shop.test/")
	if code != exitOK || !strings.HasPrefix(out, `{"data":{"url":"https://shop.test/`) || !strings.Contains(out, `"kind":"consent-detect"`) {
		t.Errorf("exit %d\n%s", code, out)
	}
}

// detect takes one page and only its own flags; a fatal error is the
// check's message.
func TestDetectUsage(t *testing.T) {
	inDir(t, "")
	runner := useDetect(t, detected(true))
	for _, args := range [][]string{{"detect"}, {"detect", "a.test", "b.test"}} {
		if code, _, errOut := runMain(t, args...); code != exitUsage || errOut != "rhythm echo: consent detect needs one page: rhythm echo consent detect https://staging.shop.test\n" {
			t.Errorf("%v: exit %d %q", args, code, errOut)
		}
	}
	if code, _, errOut := runMain(t, "detect", "--runs", "accept", "shop.test"); code != exitUsage ||
		errOut != "rhythm echo: --runs has no effect on consent detect (rhythm echo consent detect --help lists its flags)\n" {
		t.Errorf("--runs: exit %d %q", code, errOut)
	}
	if code, _, _ := runMain(t, "detect", "file:///etc/passwd"); code != exitUsage {
		t.Errorf("file URL: exit %d", code)
	}
	if len(runner.Plans) != 0 {
		t.Error("the browser was started")
	}
	s := consenttest.Fakes()
	s.Runner = &fatalDetect{}
	consenttest.Use(t, s)
	if code, _, errOut := runMain(t, "detect", "https://shop.test/"); code != exitUnavailable || !strings.Contains(errOut, "redirects to https://shop.example.com/; check that one, or pass --follow-host") {
		t.Errorf("fatal: exit %d %q", code, errOut)
	}
}

// fatalDetect is a runner whose Detect stops on a redirect off the host.
type fatalDetect struct{ consenttest.FakeRunner }

func (*fatalDetect) Detect(_ context.Context, p *consent.Plan) (*consent.DetectResult, error) {
	return nil, &consent.FatalError{Code: consent.FatalRedirectHost, URL: p.Targets[0].URL.String(), To: "https://shop.example.com/"}
}

// jsonIndent is v as consent.json holds it.
func jsonIndent(v any) (string, error) {
	data, err := json.MarshalIndent(v, "", "  ")
	return string(data), err
}

// A subcommand whose stage isn't linked names it and exits 3.
func TestStageNotLinkedSubcommands(t *testing.T) {
	inDir(t, "")
	for args, want := range map[string]string{
		"detect https://shop.test/": "rhythm echo: consent: tracker, cmp, browse not linked into this build\n",
		"cmps":                      "rhythm echo: consent: cmp not linked into this build\n",
		"trackers":                  "rhythm echo: consent: tracker not linked into this build\n",
	} {
		if code, _, errOut := runMain(t, strings.Fields(args)...); code != exitUnavailable || errOut != want {
			t.Errorf("%s: exit %d %q", args, code, errOut)
		}
	}
}

// putResult writes a result folder's consent.json under the store.
func putResult(t *testing.T, id, golden string) string {
	t.Helper()
	dir := filepath.Join(".echo", "consent", id)
	r := consenttest.GoldenResult(golden)
	data, err := jsonIndent(r)
	if err != nil {
		t.Fatal(err)
	}
	putFile(t, filepath.Join(dir, "consent.json"), data)
	putFile(t, filepath.Join(dir, "report.html"), "<html>")
	return dir
}

func TestReportLatest(t *testing.T) {
	inDir(t, "")
	fakeWriters(t)
	if code, _, errOut := runMain(t, "report"); code != exitUsage || errOut != "rhythm echo: consent report: no consent result in .echo/consent yet; run rhythm echo consent <url> first\n" {
		t.Errorf("none: exit %d %q", code, errOut)
	}
	putResult(t, "2026-10-25T08-00-00-aaaaaa", "meta-ignores")
	putResult(t, "2026-10-26T08-00-00-bbbbbb", "clean")
	putFile(t, filepath.Join(".echo", "consent", "baselines", "zzz", "consent.json"), "{}")
	code, out, _ := runMain(t, "report")
	if code != exitOK || out != "VIEW ok report=.echo/consent/2026-10-26T08-00-00-bbbbbb/report.html\n" {
		t.Errorf("latest: exit %d %q", code, out)
	}
	if code, out, _ := runMain(t, "report", "2026-10-25T08-00-00-aaaaaa"); code != exitFindings || !strings.HasPrefix(out, "VIEW findings") {
		t.Errorf("by id: exit %d %q", code, out)
	}
	if code, _, _ := runMain(t, "report", "2026-10-25T08-00-00-aaaaaa", "--fail-on", "none"); code != exitOK {
		t.Errorf("--fail-on none: exit %d", code)
	}
	if code, _, _ := runMain(t, "report", filepath.Join(".echo", "consent", "2026-10-25T08-00-00-aaaaaa", "consent.json")); code != exitFindings {
		t.Errorf("by path: exit %d", code)
	}
	// --json alone on stdout; the view goes to stderr. -o writes a file;
	// the evidence folder isn't touched.
	code, out, errOut := runMain(t, "report", "latest", "--json", "-o", "out.md")
	if code != exitOK || out != "{\"kind\":\"consent\",\"verdict\":\"ok\",\"complete\":true}\n" || !strings.HasPrefix(errOut, "VIEW ok") {
		t.Errorf("--json: exit %d %q %q", code, out, errOut)
	}
	if data, err := os.ReadFile("out.md"); err != nil || !strings.HasPrefix(string(data), "MD sha=") || len(data) != len("MD sha=")+64+1 {
		t.Errorf("out.md: %q %v", data, err)
	}
	if _, err := os.Stat(filepath.Join(".echo", "consent", "2026-10-26T08-00-00-bbbbbb", "report.md")); err == nil {
		t.Error("report wrote into the evidence folder")
	}
	// An inconclusive result exits 3 again.
	putResult(t, "2026-10-27T08-00-00-cccccc", "inconclusive")
	if code, _, _ := runMain(t, "report"); code != exitUnavailable {
		t.Errorf("inconclusive: exit %d", code)
	}
	// An interrupted check's result exits 130, as the check did, even
	// with --fail-on none.
	dir := putResult(t, "2026-10-28T08-00-00-dddddd", "clean")
	r := consenttest.GoldenResult("clean")
	r.Notes = append(r.Notes, noteInterrupted)
	data, err := jsonIndent(r)
	if err != nil {
		t.Fatal(err)
	}
	putFile(t, filepath.Join(dir, "consent.json"), data)
	for _, args := range [][]string{{"report"}, {"report", "--fail-on", "none"}} {
		if code, _, _ := runMain(t, args...); code != exitInterrupted {
			t.Errorf("%v on an interrupted result: exit %d", args, code)
		}
	}
	if code, out, _ := runMain(t, "report", "--json"); code != exitInterrupted || !strings.Contains(out, `"complete":false`) {
		t.Errorf("interrupted --json: exit %d %q", code, out)
	}
	for args, want := range map[string]string{
		"report nope":        "rhythm echo: consent report \"nope\": no consent result by that id in .echo/consent; rhythm echo consent report latest shows the newest\n",
		"report baselines":   "rhythm echo: consent report \"baselines\": no consent result by that id in .echo/consent; rhythm echo consent report latest shows the newest\n",
		"report a b":         "rhythm echo: consent report takes one result: an id from .echo/consent, a folder or latest\n",
		"report --har":       "rhythm echo: --har has no effect on consent report (rhythm echo consent report --help lists its flags)\n",
		"report --json --md": "rhythm echo: pick one output format, not --json and --md\n",
	} {
		if code, _, errOut := runMain(t, strings.Fields(args)...); code != exitUsage || errOut != want {
			t.Errorf("%s: exit %d %q", args, code, errOut)
		}
	}
}

// report --against compares the saved result with a baseline and fails
// on the change.
func TestReportAgainst(t *testing.T) {
	inDir(t, "")
	consenttest.Use(t, consenttest.Fakes())
	got := fakeWriters(t)
	putResult(t, "2026-10-26T08-00-00-bbbbbb", "compare-cur")
	base := consenttest.GoldenResult("compare-base")
	data, err := jsonIndent(base)
	if err != nil {
		t.Fatal(err)
	}
	putFile(t, filepath.Join(".echo", "consent", "baselines", "baseline", "consent.json"), data)
	code, out, errOut := runMain(t, "report", "--against", "baseline", "--json")
	if code != exitFindings || !strings.Contains(errOut, "BROKE  TikTok Pixel now fires before the choice") || !strings.Contains(out, `"kind":"consent"`) {
		t.Errorf("exit %d\n%s%s", code, out, errOut)
	}
	if c := got.json[0].Comparison; c == nil || c.Against != "baseline" {
		t.Errorf("comparison: %+v", c)
	}
	if code, _, errOut := runMain(t, "report", "--against", "missing"); code != exitUsage || !strings.HasPrefix(errOut, "rhythm echo: --against \"missing\": no saved consent result") {
		t.Errorf("missing baseline: exit %d %q", code, errOut)
	}
}

// customClassifier lists a config tracker after AddCustom, as the real
// catalog does, with its source "config".
type customClassifier struct{ consenttest.FakeClassifier }

func (c *customClassifier) Trackers() []consent.TrackerInfo {
	out := c.FakeClassifier.Trackers()
	if len(c.Custom) > 0 {
		out = append(out, consent.TrackerInfo{ID: "acme-pixel", Name: "Acme Pixel", Sources: []string{"config"}})
	}
	return out
}

func TestCmpsTrackers(t *testing.T) {
	inDir(t, "consent:\n  trackers:\n    - id: acme-pixel\n      name: Acme Pixel\n")
	s := consenttest.Fakes()
	cl := &customClassifier{}
	s.Classifier = cl
	consenttest.Use(t, s)
	fakeWriters(t)
	if code, out, _ := runMain(t, "cmps"); code != exitOK || out != "CATALOG cmps\ncmp fake\n" {
		t.Errorf("cmps: exit %d %q", code, out)
	}
	if code, out, _ := runMain(t, "cmps", "--json"); code != exitOK || !strings.Contains(out, `"kind":"consent-catalog"`) || !strings.Contains(out, `"ID":"fake"`) {
		t.Errorf("cmps --json: exit %d %q", code, out)
	}
	code, out, errOut := runMain(t, "trackers")
	if code != exitOK || out != "CATALOG trackers\ntracker ga4 \ntracker meta-pixel \ntracker acme-pixel config\n" {
		t.Errorf("trackers: exit %d %q %q", code, out, errOut)
	}
	if len(cl.Custom) == 0 || !strings.Contains(string(cl.Custom[0]), "acme-pixel") {
		t.Errorf("custom trackers: %q", cl.Custom)
	}
	for args, want := range map[string]string{
		"cmps x":          "rhythm echo: consent cmps takes no arguments\n",
		"trackers x":      "rhythm echo: consent trackers takes no arguments\n",
		"cmps --config c": "rhythm echo: --config has no effect on consent cmps (rhythm echo consent cmps --help lists its flags)\n",
	} {
		if code, _, errOut := runMain(t, strings.Fields(args)...); code != exitUsage || errOut != want {
			t.Errorf("%s: exit %d %q", args, code, errOut)
		}
	}
}

func TestPruneKeepsBaselines(t *testing.T) {
	inDir(t, "")
	if code, out, _ := runMain(t, "prune"); code != exitOK || out != "Removed 0 consent results, kept 0.\n" {
		t.Errorf("empty store: exit %d %q", code, out)
	}
	ids := []string{"2026-10-20T08-00-00-aaaaaa", "2026-10-21T08-00-00-bbbbbb", "2026-10-22T08-00-00-cccccc", "2026-10-23T08-00-00-dddddd"}
	for _, id := range ids {
		putResult(t, id, "clean")
	}
	putFile(t, filepath.Join(".echo", "consent", "baselines", "baseline", "consent.json"), "{}")
	putFile(t, filepath.Join(".echo", "consent", "detect", "2026-10-20T08-00-00-eeeeee.png"), "png")
	putFile(t, filepath.Join(".echo", "consent", "notes", "keep.txt"), "mine")
	code, out, _ := runMain(t, "prune", "--keep", "1")
	if code != exitOK || out != "Removed 3 consent results, kept 1.\n" {
		t.Errorf("--keep 1: exit %d %q", code, out)
	}
	for _, p := range []string{filepath.Join("baselines", "baseline", "consent.json"), filepath.Join("detect", "2026-10-20T08-00-00-eeeeee.png"), filepath.Join("notes", "keep.txt"), ids[3]} {
		if _, err := os.Stat(filepath.Join(".echo", "consent", p)); err != nil {
			t.Errorf("%s was removed", p)
		}
	}
	for _, id := range ids[:3] {
		if _, err := os.Stat(filepath.Join(".echo", "consent", id)); err == nil {
			t.Errorf("%s was kept", id)
		}
	}
	if code, out, _ := runMain(t, "prune", "-q"); code != exitOK || out != "" {
		t.Errorf("-q: exit %d %q", code, out)
	}
	if code, out, _ := runMain(t, "prune", "--keep", "0"); code != exitOK || out != "Removed 1 consent results, kept 0.\n" {
		t.Errorf("--keep 0: exit %d %q", code, out)
	}
	if code, _, errOut := runMain(t, "prune", "--keep", "-1"); code != exitUsage || errOut != "rhythm echo: --keep -1: want 0 or more\n" {
		t.Errorf("--keep -1: exit %d %q", code, errOut)
	}
	if code, _, errOut := runMain(t, "prune", "x"); code != exitUsage || errOut != "rhythm echo: consent prune takes no arguments; --keep <n> says how many results stay\n" {
		t.Errorf("prune x: exit %d %q", code, errOut)
	}
}

// The default keeps 20.
func TestPruneDefault(t *testing.T) {
	inDir(t, "")
	for i := range 22 {
		putResult(t, "2026-10-26T08-00-"+twoDigits(i)+"-abcdef", "clean")
	}
	if code, out, _ := runMain(t, "prune"); code != exitOK || out != "Removed 2 consent results, kept 20.\n" {
		t.Errorf("exit %d %q", code, out)
	}
	if _, err := os.Stat(filepath.Join(".echo", "consent", "2026-10-26T08-00-01-abcdef")); err == nil {
		t.Error("the second oldest was kept")
	}
}

func twoDigits(i int) string { return string(rune('0'+i/10)) + string(rune('0'+i%10)) }
