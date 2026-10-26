// Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
// SPDX-License-Identifier: MIT

package cli

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/RhythmShahriar/echo/consent"
	"github.com/RhythmShahriar/echo/consent/consenttest"
	"github.com/RhythmShahriar/echo/consent/evidence"
	"github.com/RhythmShahriar/echo/report"
)

// TestMain keeps the tests off this machine's Rhythm settings: the
// once-only notices and tips are recorded in a folder of their own.
func TestMain(m *testing.M) {
	dir, err := os.MkdirTemp("", "consent-cli-config-")
	if err != nil {
		panic(err)
	}
	_ = os.Setenv("RHYTHM_CONFIG_DIR", dir)
	_ = os.Setenv("RHYTHM_OFFLINE", "1")
	code := m.Run()
	_ = os.RemoveAll(dir)
	os.Exit(code)
}

// written is what the stand-in writers were given, by format.
type written struct {
	json []*consent.Result
	text []report.ConsentTextOptions
}

// fakeWriters swaps the report writers for stand-ins that print what
// they were handed, for the rest of t.
func fakeWriters(t *testing.T) *written {
	t.Helper()
	old := writers
	t.Cleanup(func() { writers = old })
	var got written
	writers.text = func(w io.Writer, r *consent.Result, o report.ConsentTextOptions) error {
		got.text = append(got.text, o)
		fmt.Fprintf(w, "VIEW %s report=%s\n", r.Verdict(o.FailOn), o.ReportPath)
		if r.Comparison != nil {
			for _, c := range r.Comparison.Changes {
				fmt.Fprintf(w, "  %s  %s\n", strings.ToUpper(c.Class), c.Title)
			}
		}
		return nil
	}
	writers.md = func(w io.Writer, _ *consent.Result, o report.ConsentMarkdownOptions) error {
		_, err := fmt.Fprintf(w, "MD sha=%s\n", o.ConsentSHA256)
		return err
	}
	writers.html = func(w io.Writer, _ *consent.Result, o report.ConsentHTMLOptions) error {
		_, err := fmt.Fprintf(w, "HTML sha=%s\n", o.ConsentSHA256)
		return err
	}
	writers.junit = func(w io.Writer, _ *consent.Result, sha string) error {
		_, err := fmt.Fprintf(w, "JUNIT sha=%s\n", sha)
		return err
	}
	writers.json = func(w io.Writer, r *consent.Result, failOn string) error {
		got.json = append(got.json, r)
		_, err := fmt.Fprintf(w, "{\"kind\":\"consent\",\"verdict\":%q,\"complete\":%v}\n", r.Verdict(failOn), r.Complete)
		return err
	}
	writers.verify = func(w io.Writer, v *report.ConsentVerify, asJSON bool, _ report.ConsentTextOptions) error {
		if asJSON {
			return json.NewEncoder(w).Encode(map[string]any{"kind": "consent-verify", "data": v})
		}
		for _, f := range v.Files {
			state := "ok"
			switch {
			case f.Got == "":
				state = "missing"
			case !f.OK:
				state = "changed"
			}
			fmt.Fprintf(w, "%s  %s\n", state, f.Path)
		}
		_, err := fmt.Fprintf(w, "signature present=%v ok=%v key=%s\n", v.Signature.Present, v.Signature.OK, v.Signature.Key)
		return err
	}
	writers.detect = func(w io.Writer, d *consent.DetectResult, asJSON bool, _ report.ConsentTextOptions) error {
		if asJSON {
			return json.NewEncoder(w).Encode(map[string]any{"kind": "consent-detect", "data": d})
		}
		_, err := fmt.Fprintf(w, "DETECT %s found=%v accept=%v reject=%v screenshot=%s\n", d.URL, d.CMP.Found, d.Accept != nil, d.Reject != nil, d.Screenshot)
		return err
	}
	writers.catalog = func(w io.Writer, cmps []consent.CMPRuleInfo, trackers []consent.TrackerInfo, kind string, asJSON bool, _ report.ConsentTextOptions) error {
		if asJSON {
			return json.NewEncoder(w).Encode(map[string]any{"kind": "consent-catalog", "cmps": cmps, "trackers": trackers})
		}
		fmt.Fprintf(w, "CATALOG %s\n", kind)
		for _, c := range cmps {
			fmt.Fprintf(w, "cmp %s\n", c.ID)
		}
		for _, t := range trackers {
			fmt.Fprintf(w, "tracker %s %s\n", t.ID, strings.Join(t.Sources, ","))
		}
		return nil
	}
	return &got
}

// useRunner registers every stage as a fake, the runner returning r
// and err, with the stand-in writers.
func useRunner(t *testing.T, r *consent.Result, err error) (*consenttest.FakeRunner, *written) {
	t.Helper()
	runner := &consenttest.FakeRunner{Result: r, Err: err}
	s := consenttest.Fakes()
	s.Runner = runner
	consenttest.Use(t, s)
	return runner, fakeWriters(t)
}

// resultDir is the one result folder a check made.
func resultDir(t *testing.T) string {
	t.Helper()
	dirs, err := filepath.Glob(filepath.Join(".echo", "consent", "2026-10-26T09-20-41-*"))
	if err != nil || len(dirs) != 1 {
		t.Fatalf("result folders: %v %v", dirs, err)
	}
	return dirs[0]
}

// readResultFile reads a folder's consent.json.
func readResultFile(t *testing.T, dir string) *consent.Result {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(dir, "consent.json")) //nolint:gosec // the test's own folder
	if err != nil {
		t.Fatal(err)
	}
	var r consent.Result
	if err := json.Unmarshal(data, &r); err != nil {
		t.Fatal(err)
	}
	return &r
}

func TestExitCodes(t *testing.T) {
	withError := consenttest.GoldenResult("inconclusive")
	withError.Issues = append(withError.Issues, consenttest.GoldenResult("meta-ignores").Issues[0])
	for _, tc := range []struct {
		name   string
		result *consent.Result
		args   []string
		want   int
	}{
		{"clean", consenttest.GoldenResult("clean"), nil, 0},
		{"no trackers", consenttest.GoldenResult("no-trackers"), nil, 0},
		{"meta ignores", consenttest.GoldenResult("meta-ignores"), nil, 1},
		{"advanced notice", consenttest.GoldenResult("advanced"), nil, 0},
		{"advanced notice, --fail-on notice", consenttest.GoldenResult("advanced"), []string{"--fail-on", "notice"}, 1},
		{"inconclusive", consenttest.GoldenResult("inconclusive"), nil, 3},
		{"inconclusive and an error", withError, nil, 1},
		{"--fail-on none, an error", consenttest.GoldenResult("meta-ignores"), []string{"--fail-on", "none"}, 0},
		{"--fail-on none, inconclusive", consenttest.GoldenResult("inconclusive"), []string{"--fail-on", "none"}, 3},
	} {
		t.Run(tc.name, func(t *testing.T) {
			inDir(t, "")
			useRunner(t, tc.result, nil)
			code, _, errOut := runMain(t, append(tc.args, "https://shop.test/")...)
			if code != tc.want {
				t.Errorf("exit %d; want %d\n%s", code, tc.want, errOut)
			}
		})
	}
}

// Ctrl-C mid-check exits 130 and keeps what finished, marked
// incomplete.
func TestInterrupted(t *testing.T) {
	inDir(t, "")
	_, got := useRunner(t, consenttest.GoldenResult("meta-ignores"), context.Canceled)
	code, _, errOut := runMain(t, "-o", "r.json", "https://shop.test/")
	if code != exitInterrupted || !strings.Contains(errOut, "rhythm echo: interrupted; the runs that finished are kept in .echo/consent/2026-10-26T09-20-41-") {
		t.Fatalf("exit %d\n%s", code, errOut)
	}
	r := readResultFile(t, resultDir(t))
	if len(r.Pages) != 1 || !strings.Contains(strings.Join(r.Notes, "\n"), noteInterrupted) {
		t.Errorf("consent.json: %d pages, notes %v", len(r.Pages), r.Notes)
	}
	if len(got.json) != 1 || got.json[0].Complete {
		t.Error("the JSON report isn't complete: false")
	}
	if _, err := os.Stat(filepath.Join(resultDir(t), "manifest.json")); err != nil {
		t.Errorf("no manifest after an interrupt: %v", err)
	}
	// Interrupted before any run finished: an empty result, still
	// written.
	inDir(t, "")
	useRunner(t, nil, context.Canceled)
	if code, _, _ := runMain(t, "https://shop.test/"); code != exitInterrupted {
		t.Errorf("no result: exit %d", code)
	}
	if r := readResultFile(t, resultDir(t)); len(r.Pages) != 0 || r.ID == "" || r.Echo != "1.2.0" {
		t.Errorf("empty result: %+v", r)
	}
}

// TestJSONOnlyOnStdout: --json puts the envelope alone on stdout and
// the terminal view on stderr; nothing else reaches stdout.
func TestJSONOnlyOnStdout(t *testing.T) {
	inDir(t, "")
	t.Setenv("CI", "")
	useRunner(t, consenttest.GoldenResult("clean"), nil)
	code, out, errOut := runMain(t, "--json", "--live", "https://shop.test/")
	if code != exitOK || out != "{\"kind\":\"consent\",\"verdict\":\"ok\",\"complete\":true}\n" {
		t.Errorf("exit %d stdout %q", code, out)
	}
	if !strings.Contains(errOut, "VIEW ok report=.echo/consent/2026-10-26T09-20-41-") || strings.Contains(errOut, "Note:") || strings.Contains(errOut, "Tip:") {
		t.Errorf("stderr %q", errOut)
	}
	// Without a format the view is on stdout.
	inDir(t, "")
	if _, out, _ := runMain(t, "https://shop.test/"); !strings.HasPrefix(out, "VIEW ok ") {
		t.Errorf("no format: stdout %q", out)
	}
	// --md to stdout: the view moves to stderr.
	inDir(t, "")
	if _, out, errOut := runMain(t, "--md", "https://shop.test/"); !strings.HasPrefix(out, "MD sha=") || strings.Contains(out, "VIEW") || !strings.Contains(errOut, "VIEW") {
		t.Errorf("--md: stdout %q stderr %q", out, errOut)
	}
}

func TestFirstRunNoticeOnce(t *testing.T) {
	t.Setenv("RHYTHM_CONFIG_DIR", t.TempDir())
	t.Setenv("CI", "")
	inDir(t, "")
	_, got := useRunner(t, consenttest.GoldenResult("clean"), nil)
	// --json and -q never show it (nor use it up).
	for _, args := range [][]string{{"--json"}, {"-q"}} {
		if _, _, errOut := runMain(t, append(args, "https://shop.test/")...); strings.Contains(errOut, "Note:") {
			t.Errorf("%v showed the notice", args)
		}
	}
	_, _, errOut := runMain(t, "https://shop.test/")
	if !strings.HasPrefix(errOut, notice) {
		t.Errorf("first run: %q", errOut)
	}
	if !got.text[len(got.text)-1].FirstRun {
		t.Error("the view wasn't told it's the first run")
	}
	golden(t, "first-run-notice", notice)
	if _, _, errOut := runMain(t, "https://shop.test/"); strings.Contains(errOut, "Note:") {
		t.Error("the notice showed twice")
	}
	// In CI it never shows.
	t.Setenv("RHYTHM_CONFIG_DIR", t.TempDir())
	t.Setenv("CI", "true")
	if _, _, errOut := runMain(t, "https://shop.test/"); strings.Contains(errOut, "Note:") {
		t.Error("CI showed the notice")
	}
}

func TestTipsOnce(t *testing.T) {
	t.Setenv("RHYTHM_CONFIG_DIR", t.TempDir())
	t.Setenv("CI", "")
	inDir(t, "")
	useRunner(t, consenttest.GoldenResult("clean"), nil)
	tip := "Tip: --save baseline keeps this as the baseline for the next check.\n"
	if _, out, _ := runMain(t, "https://shop.test/"); !strings.HasSuffix(out, tip) {
		t.Errorf("first clean run: %q", out)
	}
	// Each tip once: the next clean run gets the next one that fits,
	// then none.
	if _, out, _ := runMain(t, "https://shop.test/"); !strings.HasSuffix(out, "Tip: --runs +gpc also checks Global Privacy Control.\n") {
		t.Errorf("second clean run: %q", out)
	}
	if _, out, _ := runMain(t, "https://shop.test/"); strings.Contains(out, "Tip:") {
		t.Errorf("a tip showed twice: %q", out)
	}
	useRunner(t, consenttest.GoldenResult("inconclusive"), nil)
	if _, out, _ := runMain(t, "https://shop.test/"); !strings.HasSuffix(out, "Tip: banners that show only in some countries need --geo or an EU --proxy.\n") {
		t.Errorf("no banner: %q", out)
	}
	noButton := consenttest.GoldenResult("inconclusive")
	noButton.Pages[0].Runs[2].Reason = consent.ReasonNoButton
	useRunner(t, noButton, nil)
	if _, out, _ := runMain(t, "https://shop.test/"); !strings.HasSuffix(out, "Tip: rhythm echo consent detect <url> shows the buttons Echo sees.\n") {
		t.Errorf("no button: %q", out)
	}
	// Advertising trackers and no gpc run, on a fresh machine.
	t.Setenv("RHYTHM_CONFIG_DIR", t.TempDir())
	useRunner(t, consenttest.GoldenResult("meta-ignores"), nil)
	if _, out, _ := runMain(t, "https://shop.test/"); !strings.HasSuffix(out, "Tip: --runs +gpc also checks Global Privacy Control.\n") {
		t.Errorf("advertising trackers: %q", out)
	}
	if _, out, _ := runMain(t, "--runs", "+gpc", "https://shop.test/"); strings.Contains(out, "Tip:") {
		t.Errorf("with a gpc run: %q", out)
	}
	// Never in CI.
	t.Setenv("RHYTHM_CONFIG_DIR", t.TempDir())
	t.Setenv("CI", "1")
	useRunner(t, consenttest.GoldenResult("clean"), nil)
	if _, out, _ := runMain(t, "https://shop.test/"); strings.Contains(out, "Tip:") {
		t.Errorf("CI: %q", out)
	}
}

// TestFatalMessages pins every message a stopped check prints (PRD
// 10.2 messages 9 to 16): exit 3 and no result folder left behind.
func TestFatalMessages(t *testing.T) {
	// CI keeps the first-run notice out, which would otherwise lead the
	// first message when this test runs on its own.
	t.Setenv("CI", "true")
	cases := []*consent.FatalError{
		{Code: consent.FatalNoBrowser},
		{Code: consent.FatalSandboxRoot},
		{Code: consent.FatalUnreachable, URL: "https://staging.shop.test/", Detail: "dial tcp 127.0.0.1:443: connection refused"},
		{Code: consent.FatalUnreachable, URL: "https://staging.shop.test/", Err: errors.New("dial tcp: lookup staging.shop.test: no such host")},
		{Code: consent.FatalHTTPStatus, URL: "https://staging.shop.test/", Status: 401},
		{Code: consent.FatalHTTPStatus, URL: "https://staging.shop.test/", Status: 503},
		{Code: consent.FatalBotWall, URL: "https://shop.example.com/", Detail: "Cloudflare"},
		{Code: consent.FatalBotWall, URL: "https://shop.example.com/", Detail: "a bot check"},
		{Code: consent.FatalRedirectHost, URL: "https://shop.test/", To: "https://shop.example.com/"},
		{Code: consent.FatalTLS, URL: "https://shop.example/", Detail: "x509: certificate signed by unknown authority"},
		{Code: consent.FatalImage},
		{Code: consent.FatalRedirectLoop, URL: "https://shop.test/"},
		{Code: consent.FatalWrite, Detail: ".echo/consent/x/events/page-1-none.ndjson", Err: errors.New("no space left on device")},
		{Code: "something-new", URL: "https://shop.test/", Detail: "what happened"},
	}
	var b strings.Builder
	for _, fe := range cases {
		inDir(t, "")
		useRunner(t, nil, fe)
		code, out, errOut := runMain(t, "https://shop.test/")
		if code != exitUnavailable || out != "" {
			t.Errorf("%s: exit %d stdout %q", fe.Code, code, out)
		}
		if entries, _ := os.ReadDir(filepath.Join(".echo", "consent")); len(entries) != 0 {
			t.Errorf("%s left %d folders", fe.Code, len(entries))
		}
		b.WriteString(string(fe.Code) + ": " + errOut)
	}
	// In Echo's container image a missing Chrome is message 16.
	inDir(t, "")
	t.Setenv("RHYTHM_ECHO_IMAGE", "1")
	useRunner(t, nil, &consent.FatalError{Code: consent.FatalNoBrowser})
	_, _, errOut := runMain(t, "https://shop.test/")
	b.WriteString("no-browser in the image: " + errOut)
	golden(t, "fatal-messages", b.String())
}

// TestSaveAgainst: a saved baseline, then a check against it that
// shows what broke; saving again replaces it and says so.
func TestSaveAgainst(t *testing.T) {
	inDir(t, "")
	useRunner(t, consenttest.GoldenResult("compare-base"), nil)
	code, out, _ := runMain(t, "--save", "baseline", "https://shop.test/")
	if code != exitOK || !strings.Contains(out, "Saved baseline baseline.\n") {
		t.Fatalf("--save: %d %q", code, out)
	}
	if _, err := os.Stat(filepath.Join(".echo", "consent", "baselines", "baseline", "consent.json")); err != nil {
		t.Fatal(err)
	}
	_, out, _ = runMain(t, "--save", "baseline", "https://shop.test/")
	started := consenttest.GoldenResult("compare-base").Started.UTC().Format("2006-01-02 15:04")
	if !strings.Contains(out, "Saved baseline baseline (replaced the one from "+started+").\n") {
		t.Errorf("--save again: %q", out)
	}
	_, got := useRunner(t, consenttest.GoldenResult("compare-cur"), nil)
	code, out, errOut := runMain(t, "--against", "baseline", "-o", "r.json", "https://shop.test/")
	if code != exitFindings || !strings.Contains(out, "BROKE  TikTok Pixel now fires before the choice") {
		t.Errorf("--against: exit %d\n%s%s", code, out, errOut)
	}
	if c := got.json[0].Comparison; c == nil || c.Against != "baseline" {
		t.Errorf("comparison: %+v", c)
	}
	// --fail-on new and changed count the same; none passes.
	if code, _, _ := runMain(t, "--against", "baseline", "--fail-on", "none", "https://shop.test/"); code != exitOK {
		t.Errorf("--fail-on none: %d", code)
	}
	// last, a folder and a file name a result too.
	for _, against := range []string{"last", resultDirAny(t), filepath.Join(resultDirAny(t), "consent.json")} {
		if code, _, errOut := runMain(t, "--against", against, "https://shop.test/"); code == exitUsage {
			t.Errorf("--against %s: %s", against, errOut)
		}
	}
	// The saved baseline wins over a folder of the same name here; that
	// folder is still reached as ./baseline.
	if err := os.Mkdir("baseline", 0o700); err != nil {
		t.Fatal(err)
	}
	if code, _, errOut := runMain(t, "--against", "baseline", "https://shop.test/"); code == exitUsage {
		t.Errorf("--against baseline next to ./baseline: %s", errOut)
	}
	dot := "." + string(filepath.Separator) + "baseline"
	if code, _, errOut := runMain(t, "--against", dot, "https://shop.test/"); code != exitUsage || errOut != "rhythm echo: --against "+dot+": no consent.json in that folder\n" {
		t.Errorf("--against %s: %d %q", dot, code, errOut)
	}
	// An incomplete check is never saved as a baseline.
	useRunner(t, consenttest.GoldenResult("inconclusive"), nil)
	_, out, errOut = runMain(t, "--save", "partial", "https://shop.test/")
	if strings.Contains(out, "Saved") || !strings.Contains(errOut, "rhythm echo: --save partial: not saved: a baseline needs a complete check and some runs couldn't be checked") {
		t.Errorf("incomplete --save: %q %q", out, errOut)
	}
	if code, _, errOut := runMain(t, "--save", "../x", "https://shop.test/"); code != exitUsage || !strings.HasPrefix(errOut, "rhythm echo: --save: \"../x\" isn't a valid name") {
		t.Errorf("--save ../x: %d %q", code, errOut)
	}
}

// resultDirAny is any result folder of the store.
func resultDirAny(t *testing.T) string {
	t.Helper()
	dirs, _ := filepath.Glob(filepath.Join(".echo", "consent", "2026-*"))
	if len(dirs) == 0 {
		t.Fatal("no result folder")
	}
	return dirs[0]
}

// TestAgainstMissing: a baseline that isn't there is message 5, before
// any browser starts.
func TestAgainstMissing(t *testing.T) {
	inDir(t, "")
	runner, _ := useRunner(t, consenttest.GoldenResult("clean"), nil)
	code, _, errOut := runMain(t, "--against", "baseline", "https://shop.test/")
	want := "rhythm echo: --against \"baseline\": no saved consent result by that name in .echo/consent/baselines; rhythm echo consent <url> --save baseline makes one\n"
	if code != exitUsage || errOut != want || len(runner.Plans) != 0 {
		t.Errorf("exit %d\n got %q\nwant %q", code, errOut, want)
	}
	if code, _, errOut := runMain(t, "--against", "last", "https://shop.test/"); code != exitUsage ||
		errOut != "rhythm echo: --against last: no consent result in .echo/consent yet; run rhythm echo consent <url> first\n" {
		t.Errorf("last with none: %d %q", code, errOut)
	}
	putFile(t, "bad.json", "{not json")
	if code, _, errOut := runMain(t, "--against", "bad.json", "https://shop.test/"); code != exitUsage || !strings.HasPrefix(errOut, "rhythm echo: --against bad.json: bad.json isn't a consent result: ") {
		t.Errorf("bad.json: %d %q", code, errOut)
	}
	if err := os.Mkdir("empty", 0o700); err != nil {
		t.Fatal(err)
	}
	if code, _, errOut := runMain(t, "--against", "empty", "https://shop.test/"); code != exitUsage || errOut != "rhythm echo: --against empty: no consent.json in that folder\n" {
		t.Errorf("empty folder: %d %q", code, errOut)
	}
	if len(runner.Plans) != 0 {
		t.Error("the browser was started")
	}
}

func TestLiveLines(t *testing.T) {
	hit := func(phase consent.Phase, held bool) consent.Event {
		return consent.Event{Kind: consent.EvRequest, AtMS: 840, Phase: phase,
			Request: &consent.Request{URL: "https://www.facebook.com/tr?id=••••5678&ev=PageView", Host: "www.facebook.com", Held: held},
			Hit:     &consent.Hit{Name: "Meta Pixel", Kind: consent.HitBeacon}}
	}
	for ev, want := range map[*consent.Event]string{
		ptr(hit(consent.PhaseBefore, true)):       "hit  0.84s  before the choice  Meta Pixel  www.facebook.com/tr (held)",
		ptr(hit(consent.PhaseAfterReject, true)):  "hit  0.84s  Reject  Meta Pixel  www.facebook.com/tr (held)",
		ptr(hit(consent.PhaseAfterAccept, false)): "hit  0.84s  Accept  Meta Pixel  www.facebook.com/tr",
	} {
		if got := liveLine(*ev); got != want {
			t.Errorf("got  %q\nwant %q", got, want)
		}
	}
	script := hit(consent.PhaseBefore, false)
	script.Hit.Kind = consent.HitScript
	for _, ev := range []consent.Event{script, {Kind: consent.EvCookie}, {Kind: consent.EvRequest, Request: &consent.Request{}}} {
		if got := liveLine(ev); got != "" {
			t.Errorf("not a hit: %q", got)
		}
	}
	// Page text is made safe for the terminal.
	odd := hit(consent.PhaseBefore, true)
	odd.Hit.Name = "Pixel\x1b[31m\u202e"
	if got := liveLine(odd); strings.ContainsAny(got, "\x1b\u202e") {
		t.Errorf("control characters reached the terminal: %q", got)
	}
	var b bytes.Buffer
	live := liveWriter(&b, true)
	live(hit(consent.PhaseBefore, true))
	live(consent.Event{Kind: consent.EvCookie})
	if b.String() != "hit  0.84s  before the choice  Meta Pixel  www.facebook.com/tr (held)\n" {
		t.Errorf("--live wrote %q", b.String())
	}
	if liveWriter(&b, false) != nil {
		t.Error("--live off still has a writer")
	}
	// The plan carries it to the browser only with --live.
	inDir(t, "")
	runner, _ := useRunner(t, consenttest.GoldenResult("clean"), nil)
	runMain(t, "--live", "https://shop.test/")
	runMain(t, "https://shop.test/")
	if len(runner.Plans) != 2 || runner.Plans[0].Live == nil || runner.Plans[1].Live != nil {
		t.Errorf("plans: %d", len(runner.Plans))
	}
}

func ptr[T any](v T) *T { return &v }

// TestRunWarnings: the sandbox and --send-hits warnings, exact and
// once per command.
func TestRunWarnings(t *testing.T) {
	inDir(t, "")
	useRunner(t, consenttest.GoldenResult("clean"), nil)
	_, _, errOut := runMain(t, "--no-sandbox", "--send-hits", "https://shop.test/", "https://shop.test/menu/")
	for _, w := range []string{
		"rhythm echo: Chrome is running without its sandbox (--no-sandbox); only check sites you trust\n",
		"rhythm echo: --send-hits: this run sends real tracking hits to Google, Meta and the other trackers on the page\n",
	} {
		if strings.Count(errOut, w) != 1 {
			t.Errorf("%q not once in\n%s", w, errOut)
		}
	}
	t.Setenv("RHYTHM_ECHO_NO_SANDBOX", "1")
	if _, _, errOut := runMain(t, "https://shop.test/"); !strings.Contains(errOut, "without its sandbox") || strings.Contains(errOut, "--send-hits:") {
		t.Errorf("RHYTHM_ECHO_NO_SANDBOX=1: %q", errOut)
	}
	t.Setenv("RHYTHM_ECHO_NO_SANDBOX", "")
	if _, _, errOut := runMain(t, "https://shop.test/"); strings.Contains(errOut, "rhythm echo:") {
		t.Errorf("a plain run warned: %q", errOut)
	}
}

// filesRunner writes the attachments a real run would, then returns
// the golden result.
type filesRunner struct{ consenttest.FakeRunner }

func (f *filesRunner) Run(ctx context.Context, p *consent.Plan) (*consent.Result, error) {
	for name, data := range map[string]string{"events/page-1-none.ndjson": "{\"seq\":1}\n", "evidence/page-1/none-banner.png": "\x89PNG fake"} {
		path := filepath.Join(p.Dir, filepath.FromSlash(name))
		if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
			return nil, err
		}
		if err := os.WriteFile(path, []byte(data), 0o600); err != nil {
			return nil, err
		}
	}
	return f.FakeRunner.Run(ctx, p)
}

// TestWriteOrder: consent.json lists every attachment's hash, the
// reports state consent.json's hash and the manifest covers them all;
// files are 0600 in 0700 folders.
func TestWriteOrder(t *testing.T) {
	inDir(t, "")
	s := consenttest.Fakes()
	s.Runner = &filesRunner{consenttest.FakeRunner{Result: consenttest.GoldenResult("clean")}}
	consenttest.Use(t, s)
	fakeWriters(t)
	if code, _, errOut := runMain(t, "--md", "-o", "out/junit.xml", "-o", "out/r.json", "https://shop.test/"); code != exitOK {
		t.Fatalf("exit %d %s", code, errOut)
	}
	dir := resultDir(t)
	r := readResultFile(t, dir)
	sum := func(path string) string {
		data, err := os.ReadFile(path) //nolint:gosec // the test's own folder
		if err != nil {
			t.Fatal(err)
		}
		s := sha256.Sum256(data)
		return hex.EncodeToString(s[:])
	}
	if len(r.Evidence.Files) != 2 || r.Evidence.Files[0].Path != "events/page-1-none.ndjson" || r.Evidence.Files[1].Path != "evidence/page-1/none-banner.png" ||
		r.Evidence.Files[0].SHA256 != sum(filepath.Join(dir, "events", "page-1-none.ndjson")) || r.Evidence.Files[1].Bytes != 9 {
		t.Errorf("evidence files: %+v", r.Evidence.Files)
	}
	if r.Evidence.Folder != filepath.ToSlash(dir) {
		t.Errorf("evidence folder %q; want %q", r.Evidence.Folder, dir)
	}
	sha := sum(filepath.Join(dir, "consent.json"))
	for name, want := range map[string]string{"report.html": "HTML sha=" + sha + "\n", "report.md": "MD sha=" + sha + "\n", "report.xml": "JUNIT sha=" + sha + "\n"} {
		if data, err := os.ReadFile(filepath.Join(dir, name)); err != nil || string(data) != want { //nolint:gosec // the test's own folder
			t.Errorf("%s: %q %v", name, data, err)
		}
	}
	if data, err := os.ReadFile(filepath.Join("out", "junit.xml")); err != nil || string(data) != "JUNIT sha="+sha+"\n" {
		t.Errorf("-o out/junit.xml: %q %v", data, err)
	}
	var m evidence.Manifest
	data, err := os.ReadFile(filepath.Join(dir, "manifest.json")) //nolint:gosec // the test's own folder
	if err != nil || json.Unmarshal(data, &m) != nil || m.Schema != "rhythm-echo-consent-manifest/v1" {
		t.Fatalf("manifest: %s %v", data, err)
	}
	var paths []string
	for _, f := range m.Files {
		paths = append(paths, f.Path)
		if f.SHA256 != sum(filepath.Join(dir, filepath.FromSlash(f.Path))) {
			t.Errorf("manifest hash of %s is wrong", f.Path)
		}
	}
	if got := strings.Join(paths, " "); got != "consent.json events/page-1-none.ndjson evidence/page-1/none-banner.png report.html report.md report.xml" {
		t.Errorf("manifest lists %s", got)
	}
	if runtime.GOOS != "windows" {
		for _, p := range []string{dir, filepath.Join(".echo", "consent")} {
			if fi, err := os.Stat(p); err != nil || fi.Mode().Perm() != 0o700 {
				t.Errorf("%s: %v %v", p, fi.Mode().Perm(), err)
			}
		}
		for _, name := range []string{"consent.json", "manifest.json", "report.html", "report.md"} {
			if fi, err := os.Stat(filepath.Join(dir, name)); err != nil || fi.Mode().Perm() != 0o600 {
				t.Errorf("%s: %v %v", name, fi.Mode().Perm(), err)
			}
		}
	}
}

// -o a.json -o b.md writes both files, nothing on stdout but the view.
func TestOutputFiles(t *testing.T) {
	inDir(t, "")
	useRunner(t, consenttest.GoldenResult("meta-ignores"), nil)
	code, out, _ := runMain(t, "-o", "a.json", "-o", "b.md", "-o", "c.html", "https://shop.test/")
	if code != exitFindings || !strings.HasPrefix(out, "VIEW findings ") {
		t.Errorf("exit %d %q", code, out)
	}
	for name, prefix := range map[string]string{"a.json": `{"kind":"consent","verdict":"findings"`, "b.md": "MD sha=", "c.html": "HTML sha="} {
		if data, err := os.ReadFile(name); err != nil || !strings.HasPrefix(string(data), prefix) {
			t.Errorf("%s: %q %v", name, data, err)
		}
	}
	// report.md is in the folder because Markdown was asked for.
	if _, err := os.Stat(filepath.Join(resultDir(t), "report.md")); err != nil {
		t.Errorf("report.md: %v", err)
	}
}

// A report that can't be written is exit 3 naming the file.
func TestWriteFailure(t *testing.T) {
	inDir(t, "")
	useRunner(t, consenttest.GoldenResult("clean"), nil)
	writers.html = func(io.Writer, *consent.Result, report.ConsentHTMLOptions) error { return errors.New("boom") }
	code, _, errOut := runMain(t, "https://shop.test/")
	if code != exitUnavailable || !strings.HasPrefix(errOut, "rhythm echo: couldn't write .echo/consent/2026-10-26T09-20-41-") || !strings.HasSuffix(errOut, "/report.html: boom\n") {
		t.Errorf("exit %d %q", code, errOut)
	}
	fakeWriters(t)
	if err := os.WriteFile("blocked", nil, 0o600); err != nil {
		t.Fatal(err)
	}
	if code, _, errOut := runMain(t, "-o", "blocked/r.json", "https://shop.test/"); code != exitUnavailable || !strings.HasPrefix(errOut, "rhythm echo: couldn't write blocked/r.json: ") {
		t.Errorf("-o in a file: %d %q", code, errOut)
	}
}

// Without the stages linked a check says which are missing and exits
// 3 before making anything.
func TestStageNotLinked(t *testing.T) {
	inDir(t, "")
	if consent.Linked().Runner != nil {
		t.Skip("stages are linked into this test binary")
	}
	code, _, errOut := runMain(t, "https://shop.test/")
	if code != exitUnavailable || errOut != "rhythm echo: consent: tracker, signal, state reader, cmp, browse, verdict not linked into this build\n" {
		t.Errorf("exit %d %q", code, errOut)
	}
	if _, err := os.Stat(".echo"); err == nil {
		t.Error("the store was made")
	}
}

// --open opens the HTML report through the opener cmd/rhythm-echo
// hands in; the update check runs only for a terminal.
func TestOpenAndUpdate(t *testing.T) {
	inDir(t, "")
	useRunner(t, consenttest.GoldenResult("clean"), nil)
	var opened string
	updated := false
	var out, errOut bytes.Buffer
	env := Env{Version: "1.2.0", Stdout: &out, Stderr: &errOut, Now: func() time.Time { return time.Date(2026, 10, 26, 9, 20, 41, 0, time.UTC) },
		Open:   func(p string) error { opened = p; return nil },
		Update: func(context.Context) func(context.Context) { updated = true; return func(context.Context) {} }}
	if code := Main([]string{"--open", "https://shop.test/"}, env); code != exitOK {
		t.Fatalf("exit %d %s", code, errOut.String())
	}
	if filepath.Base(opened) != "report.html" || !filepath.IsAbs(opened) {
		t.Errorf("opened %q", opened)
	}
	if updated {
		t.Error("the update check ran for a buffer, not a terminal")
	}
}

// secondFails finishes the first plan as filesRunner does and stops
// the check on the next one with Err.
type secondFails struct {
	filesRunner
	calls int
	err   error
}

func (s *secondFails) Run(ctx context.Context, p *consent.Plan) (*consent.Result, error) {
	if s.calls++; s.calls > 1 {
		return nil, s.err
	}
	return s.filesRunner.Run(ctx, p)
}

// A check that stops on a fatal error after an earlier --sites site
// finished leaves no result folder behind: there is no consent.json
// for it, so report and verify would only trip over it. A single site
// that finishes keeps its folder.
func TestFatalAfterFirstSite(t *testing.T) {
	inDir(t, "")
	putFile(t, "sites.yaml", "sites:\n  - {name: a, url: https://a.test}\n  - {name: b, url: https://b.test}\n")
	s := consenttest.Fakes()
	runner := &secondFails{filesRunner: filesRunner{consenttest.FakeRunner{Result: consenttest.GoldenResult("clean")}},
		err: &consent.FatalError{Code: consent.FatalNoBrowser}}
	s.Runner = runner
	consenttest.Use(t, s)
	fakeWriters(t)
	if code, _, errOut := runMain(t, "--sites", "sites.yaml"); code != exitUnavailable || errOut == "" {
		t.Errorf("exit %d %q", code, errOut)
	}
	if entries, _ := os.ReadDir(filepath.Join(".echo", "consent")); len(entries) != 0 {
		t.Errorf("left %d folders", len(entries))
	}
	// One site, no error: the folder stays with its consent.json.
	runner.calls = 0
	putFile(t, "one.yaml", "sites:\n  - {name: a, url: https://a.test}\n")
	if code, _, errOut := runMain(t, "--sites", "one.yaml"); code != exitOK {
		t.Fatalf("one site: exit %d %s", code, errOut)
	}
	if _, err := os.Stat(filepath.Join(resultDir(t), "consent.json")); err != nil {
		t.Error(err)
	}
}
