// Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
// SPDX-License-Identifier: MIT

package action

import (
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"testing"
)

// consentID is the result folder testdata/consent.json names.
const consentID = "2026-10-26T09-20-41-c4d5e6"

// fakeConsent stands in for rhythm-echo consent: it records its
// arguments one per line, writes the JSON and Markdown it is given to
// its -o files, makes the evidence folder testdata/consent.json names
// and exits with code. Its stdout names a recipient, which must stay
// out of the public step log. Echo's report writers are built by
// another lane; these tests check the Action's own steps.
type fakeConsent struct {
	bin  string // put first on the runner's PATH
	args string // the file its arguments go to
}

func newFakeConsent(t *testing.T, code int, jsonFile, mdFile string) fakeConsent {
	t.Helper()
	f := fakeConsent{bin: t.TempDir()}
	f.args = filepath.Join(f.bin, "args")
	abs := func(p string) string {
		if p == "" {
			return ""
		}
		a, err := filepath.Abs(p)
		if err != nil {
			t.Fatal(err)
		}
		return a
	}
	script := `#!/usr/bin/env bash
printf '%s\n' "$@" > ` + strconv.Quote(f.args) + `
[ "$1" = consent ] || exit 9
json=` + strconv.Quote(abs(jsonFile)) + `
md=` + strconv.Quote(abs(mdFile)) + `
while [ $# -gt 0 ]; do
  if [ "$1" = -o ]; then
    shift
    case "$1" in
      *.json) [ -z "$json" ] || cp "$json" "$1" ;;
      *.md) [ -z "$md" ] || cp "$md" "$1" ;;
    esac
  fi
  shift
done
if [ -n "$json" ]; then
  mkdir -p .echo/consent/` + consentID + `
  cp "$json" .echo/consent/` + consentID + `/consent.json
fi
echo "Meta Pixel fired: hit for jane.doe@customer.example"
echo "rhythm echo: consent finished" >&2
exit ` + strconv.Itoa(code) + "\n"
	cosign := "#!/usr/bin/env bash\nprintf '%s\\n' \"$PWD\" \"$@\" > " + strconv.Quote(filepath.Join(f.bin, "cosign-args")) + "\n"
	for name, body := range map[string]string{"rhythm-echo": script, "cosign": cosign} {
		if err := os.WriteFile(filepath.Join(f.bin, name), []byte(body), 0o700); err != nil { //nolint:gosec // a test executable
			t.Fatal(err)
		}
	}
	return f
}

// called returns the arguments rhythm-echo got, or nil when it didn't run.
func (f fakeConsent) called(t *testing.T) []string {
	t.Helper()
	b, err := os.ReadFile(f.args)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		t.Fatal(err)
	}
	return strings.Split(strings.TrimSuffix(string(b), "\n"), "\n")
}

// consentRunner is a job whose rhythm-echo is fake.
func consentRunner(t *testing.T, code int, jsonFile, mdFile string) (*runner, fakeConsent) {
	t.Helper()
	needBash(t, "jq")
	r := newRunner(t)
	r.skip = []string{"Install Echo"}
	f := newFakeConsent(t, code, jsonFile, mdFile)
	r.path = []string{f.bin}
	return r, f
}

// consentPrivate is what testdata/consent.json carries that a public
// summary, comment or step log must never show: personal values, IDs,
// query strings and page-supplied Markdown, HTML and mentions.
var consentPrivate = []string{"jane.doe", "4411", "W-1234", "1234565678", "5678", "ABCD", "Welcome", "412", "bob@",
	"<img", "@octocat", "@team", "](", "<!--", "evil.example", "session=", "fbq("}

// TestConsentOutputs runs mode consent on two pages: the check runs with
// both reports and the pages after --, Echo isn't started or stopped, the
// counts and paths come out as outputs and the step fails with the
// consent message.
func TestConsentOutputs(t *testing.T) {
	r, f := consentRunner(t, 1, filepath.Join("testdata", "consent.json"), "")
	res := r.use(map[string]string{"mode": "consent", "site": "https://shop.test/account/\n  https://shop.test/"})
	if res.ok {
		t.Fatalf("exit 1 passed:\n%s", res.log)
	}
	folder := filepath.Join(r.workspace, ".echo", "consent", consentID)
	want := map[string]string{
		"exit-code": "1", "before-choice": "1", "after-reject": "2", "inconclusive": "2",
		"json": filepath.Join(folder, "consent.json"), "report": filepath.Join(folder, "report.html"),
	}
	for k, v := range want {
		if res.outputs[k] != v {
			t.Errorf("output %s = %q, want %q", k, res.outputs[k], v)
		}
	}
	wantArgs := []string{"consent", "-o", filepath.Join(r.temp, "echo-consent.json"), "-o", filepath.Join(r.temp, "echo-consent.md"),
		"--fail-on", "error", "--plain", "--", "https://shop.test/account/", "https://shop.test/"}
	if got := f.called(t); !slices.Equal(got, wantArgs) {
		t.Errorf("rhythm-echo got %q\nwant %q", got, wantArgs)
	}
	for _, s := range []string{"Start Echo", "Run Echo", "Stop Echo", "Sign consent.json", "Upload the evidence"} {
		if slices.Contains(res.ran, s) {
			t.Errorf("mode consent ran %q: %q", s, res.ran)
		}
	}
	if !slices.Contains(res.ran, "Fail on consent issues") {
		t.Errorf("steps = %q", res.ran)
	}
	contains(t, "log", res.log, "::error::Echo found consent issues at or above --fail-on error")
	contains(t, "summary", res.summary, "## ♪ Echo consent", "**Findings.** 2 pages, 6 runs")
	if got, _ := os.ReadFile(res.outputs["summary"]); string(got) != res.summary {
		t.Error("the summary output differs from the job summary")
	}

	// A summary that leaves a count out (an older or partial envelope)
	// reads as 0; exit 0 doesn't fail.
	dir := t.TempDir()
	bare := filepath.Join(dir, "bare.json")
	if err := os.WriteFile(bare, []byte(`{"schema": "rhythm-echo/v1", "kind": "consent", "summary": {"pages": 1}, "data": {}}`), 0o600); err != nil {
		t.Fatal(err)
	}
	r, _ = consentRunner(t, 0, bare, "")
	res = r.use(map[string]string{"mode": "consent", "site": "https://shop.test/"})
	if !res.ok {
		t.Fatalf("exit 0 failed:\n%s", res.log)
	}
	for _, k := range []string{"before-choice", "after-reject", "inconclusive"} {
		if res.outputs[k] != "0" {
			t.Errorf("output %s = %q, want 0", k, res.outputs[k])
		}
	}
	if res.outputs["json"] != "" || slices.Contains(res.ran, "Fail on consent issues") {
		t.Errorf("json %q, steps %q", res.outputs["json"], res.ran)
	}
	contains(t, "summary", res.summary, "**All clear.** 1 page, 0 runs")
}

// TestConsentSummaryMasked: the summary, the step log and the pull
// request comment of a result full of personal and hostile values show
// none of them; summary-details puts the check's own output in the log.
func TestConsentSummaryMasked(t *testing.T) {
	r, _ := consentRunner(t, 1, filepath.Join("testdata", "consent.json"), "")
	gh := fakeGH(t, `[]`, "201")
	r.path = append([]string{gh.bin}, r.path...)
	r.github["event_name"] = "pull_request"
	r.github["event.pull_request.number"] = "7"
	res := r.use(map[string]string{"mode": "consent", "site": "https://shop.test/", "pr-comment": "true"})
	if res.outputs["comment-id"] != "201" {
		t.Fatalf("comment-id %q\n%s", res.outputs["comment-id"], res.log)
	}
	calls := gh.calls(t)
	if len(calls) != 3 || !strings.HasPrefix(calls[2], "api -X POST repos/RhythmShahriar/echo/issues/7/comments -f body=<!-- rhythm-echo comment: . -->") {
		t.Fatalf("gh calls: %q", calls)
	}
	comment := strings.TrimPrefix(calls[2], "api -X POST repos/RhythmShahriar/echo/issues/7/comments -f body=<!-- rhythm-echo comment: . -->")
	for what, s := range map[string]string{"summary": res.summary, "comment": comment, "step log": res.log} {
		contains(t, what, s, "## ♪ Echo consent", "| /account/ |", "✗ Meta Pixel", "Meta Pixel fired before the visitor chose", "? no&#95;banner")
		lacks(t, what, s, consentPrivate...)
	}
	// The ignored issue is left out.
	lacks(t, "summary", res.summary, "Hotjar")

	details, _ := consentRunner(t, 0, filepath.Join("testdata", "consent.json"), "")
	got := details.use(map[string]string{"mode": "consent", "site": "https://shop.test/", "summary-details": "true"})
	contains(t, "detailed step log", got.log, "hit for jane.doe@customer.example")
}

// TestConsentSummaryMarkdown: Echo's own Markdown is the summary when it
// wrote one, with addresses and numbers masked once more and code in
// quotes kept.
func TestConsentSummaryMarkdown(t *testing.T) {
	needBash(t, "jq")
	sh := filepath.Join(root(t), "integrations", "action", "summary.sh")
	dir := t.TempDir()
	mdFile := filepath.Join(dir, "report.md")
	md := "## ♪ Echo consent\n\n**Findings.** Meta Pixel fired before the choice.\n\n" +
		"| consent/before-choice | / | sent for ann" + "@shop.example, to +49 151 1234 5678 | Call fbq('consent', 'revoke') |\n"
	if err := os.WriteFile(mdFile, []byte(md), 0o600); err != nil {
		t.Fatal(err)
	}
	out, errOut, code := script(t, sh, nil, "consent", filepath.Join("testdata", "consent.json"), mdFile)
	if code != 0 {
		t.Fatalf("exit %d: %s", code, errOut)
	}
	contains(t, "summary", out, "**Findings.** Meta Pixel fired before the choice.", "***@shop.example", "+***", "fbq('consent', 'revoke')")
	lacks(t, "summary", out, "ann@", "1234 5678", "| Page | Banner |")

	// An empty Markdown file is no Markdown: the table from the JSON.
	if err := os.WriteFile(mdFile, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	out, _, _ = script(t, sh, nil, "consent", filepath.Join("testdata", "consent.json"), mdFile)
	contains(t, "summary", out, "| Page | Banner | No choice | Accept | Reject |")
}

// TestConsentSummaryCut keeps the summary under GitHub's limit for one
// step, cut at a line with a note.
func TestConsentSummaryCut(t *testing.T) {
	needBash(t, "jq")
	sh := filepath.Join(root(t), "integrations", "action", "summary.sh")
	mdFile := filepath.Join(t.TempDir(), "report.md")
	line := "| consent/before-choice | /shop/ | No choice | Meta Pixel fired before the visitor chose |\n"
	if err := os.WriteFile(mdFile, []byte(strings.Repeat(line, 2*900*1024/len(line))), 0o600); err != nil {
		t.Fatal(err)
	}
	out, errOut, code := script(t, sh, nil, "consent", filepath.Join("testdata", "consent.json"), mdFile)
	if code != 0 {
		t.Fatalf("exit %d: %s", code, errOut)
	}
	if len(out) > 900*1024 {
		t.Errorf("summary is %d bytes, over 900 KiB", len(out))
	}
	contains(t, "summary", out, "_The summary was cut at 900 KiB: the evidence folder (upload: true) has the whole report._")
	body, _, _ := strings.Cut(out, "\n\n_The summary was cut")
	for i, l := range strings.Split(body, "\n") {
		if l+"\n" != line {
			t.Fatalf("line %d was cut in the middle: %q", i+1, l)
		}
	}

	// A short one is left as it is.
	if err := os.WriteFile(mdFile, []byte(line), 0o600); err != nil {
		t.Fatal(err)
	}
	if out, _, _ := script(t, sh, nil, "consent", filepath.Join("testdata", "consent.json"), mdFile); out != line {
		t.Errorf("a short summary changed: %q", out)
	}
}

// TestConsentShowIDsRefused: --show-ids would put unmasked IDs in a
// shared summary, so the Action refuses it before anything runs.
func TestConsentShowIDsRefused(t *testing.T) {
	for _, args := range []string{"--show-ids", "--runs +gpc --show-ids=true"} {
		r, f := consentRunner(t, 0, filepath.Join("testdata", "consent.json"), "")
		res := r.use(map[string]string{"mode": "consent", "site": "https://shop.test/", "args": args})
		if res.ok || res.outputs["exit-code"] != "2" {
			t.Fatalf("%s: ok %v exit-code %q\n%s", args, res.ok, res.outputs["exit-code"], res.log)
		}
		contains(t, "summary", res.summary, "(exit 2).**", "Echo: --show-ids is not allowed in the Action: its output is shared")
		if got := f.called(t); got != nil {
			t.Errorf("%s: rhythm-echo ran with %q", args, got)
		}
	}
}

// TestConsentExit3Message: runs that couldn't choose (exit 3 with a
// result) show the consent summary and fail with the number of runs; a
// check that couldn't run at all (exit 3, no result) shows why.
func TestConsentExit3Message(t *testing.T) {
	r, _ := consentRunner(t, 3, filepath.Join("testdata", "consent.json"), "")
	res := r.use(map[string]string{"mode": "consent", "site": "https://shop.test/"})
	if res.ok || res.outputs["exit-code"] != "3" {
		t.Fatalf("ok %v exit-code %q\n%s", res.ok, res.outputs["exit-code"], res.log)
	}
	contains(t, "log", res.log, "::error::Echo couldn't check consent on 2 runs (see the summary)")
	contains(t, "summary", res.summary, "### Couldn't check (2)")

	r, _ = consentRunner(t, 3, "", "")
	res = r.use(map[string]string{"mode": "consent", "site": "https://shop.test/"})
	if res.ok || res.outputs["exit-code"] != "3" {
		t.Fatalf("no result: ok %v exit-code %q\n%s", res.ok, res.outputs["exit-code"], res.log)
	}
	contains(t, "summary", res.summary, "**Echo couldn't run (exit 3).**", "rhythm echo: consent finished")
	lacks(t, "log", res.log, "couldn't check consent on")
}

// TestConsentFailOnDefault: without fail-on the level is error, or broke
// when args compare with --against, as in the CLI; a level of the other
// kind is a usage error.
func TestConsentFailOnDefault(t *testing.T) {
	cases := []struct {
		failOn, args string
		want         []string // rhythm-echo's arguments from --fail-on on; nil: usage error
	}{
		{"", "--against baseline", []string{"--fail-on", "broke"}},
		{"", "--against=baseline", []string{"--fail-on", "broke"}},
		{"", "--runs +gpc", []string{"--fail-on", "error"}},
		{"NONE", "", []string{"--fail-on", "none"}},
		{"Changed", "--against baseline", []string{"--fail-on", "changed"}},
		{"warning", "--against baseline", nil},
		{"broke", "", nil},
	}
	for _, c := range cases {
		r, f := consentRunner(t, 0, filepath.Join("testdata", "consent.json"), "")
		res := r.use(map[string]string{"mode": "consent", "site": "https://shop.test/", "fail-on": c.failOn, "args": c.args})
		got := f.called(t)
		if c.want == nil {
			if res.outputs["exit-code"] != "2" || got != nil {
				t.Errorf("fail-on %q args %q: exit-code %q, ran %q", c.failOn, c.args, res.outputs["exit-code"], got)
			}
			contains(t, "summary", res.summary, `fail-on "`+c.failOn+`": want`)
			continue
		}
		i := slices.Index(got, "--fail-on")
		if i < 0 || !slices.Equal(got[i:i+2], c.want) {
			t.Errorf("fail-on %q args %q: rhythm-echo got %q", c.failOn, c.args, got)
		}
	}
}

// TestConsentInjection: a site or an argument with $(...) or backticks
// reaches rhythm-echo as plain text and runs nothing; a quote left open
// is a usage error.
func TestConsentInjection(t *testing.T) {
	r, f := consentRunner(t, 0, filepath.Join("testdata", "consent.json"), "")
	pwned := filepath.Join(r.workspace, "pwned")
	site := "http://127.0.0.1:18990/$(touch " + pwned + ") http://127.0.0.1:18990/`touch " + pwned + "`"
	res := r.use(map[string]string{"mode": "consent", "site": site, "args": "--geo $(touch " + pwned + ")"})
	if !res.ok {
		t.Fatalf("failed:\n%s", res.log)
	}
	if _, err := os.Stat(pwned); err == nil {
		t.Fatal("a site ran a shell command")
	}
	got := f.called(t)
	for _, w := range []string{"http://127.0.0.1:18990/$(touch", "http://127.0.0.1:18990/`touch", "$(touch"} {
		if !slices.Contains(got, w) {
			t.Errorf("rhythm-echo got %q, want %q as one argument", got, w)
		}
	}

	r, f = consentRunner(t, 0, filepath.Join("testdata", "consent.json"), "")
	res = r.use(map[string]string{"mode": "consent", "site": "https://shop.test/ 'open"})
	if res.outputs["exit-code"] != "2" || f.called(t) != nil {
		t.Fatalf("an open quote: exit-code %q\n%s", res.outputs["exit-code"], res.log)
	}
	contains(t, "summary", res.summary, "rhythm echo: site: a quote isn't closed")
}

// TestConsentSignUpload: sign signs consent.json in the evidence folder
// with a notice about Rekor; upload uploads the folder and warns in a
// public repository; a bad retention-days is a usage error.
func TestConsentSignUpload(t *testing.T) {
	r, f := consentRunner(t, 0, filepath.Join("testdata", "consent.json"), "")
	r.github["event.repository.private"] = "false"
	res := r.use(map[string]string{"mode": "consent", "site": "https://shop.test/", "sign": "true", "upload": "true", "retention-days": "30"})
	if !res.ok {
		t.Fatalf("failed:\n%s", res.log)
	}
	b, err := os.ReadFile(filepath.Join(f.bin, "cosign-args"))
	if err != nil {
		t.Fatalf("cosign didn't run: %v\n%s", err, res.log)
	}
	folder := filepath.Join(r.workspace, ".echo", "consent", consentID)
	if want := folder + "\nsign-blob\n--yes\n--bundle\nconsent.json.sigstore.json\nconsent.json\n"; string(b) != want {
		t.Errorf("cosign got %q, want %q", b, want)
	}
	contains(t, "log", res.log, "the repository and workflow name go to the public Rekor transparency log",
		"::warning::upload is on in a public repository: anyone can download the echo-consent artifact")
	if !slices.Contains(res.ran, "Upload the evidence") || slices.Contains(res.ran, "Upload reports") {
		t.Errorf("steps = %q", res.ran)
	}

	r, f = consentRunner(t, 0, filepath.Join("testdata", "consent.json"), "")
	res = r.use(map[string]string{"mode": "consent", "site": "https://shop.test/", "retention-days": "365"})
	if res.outputs["exit-code"] != "2" || f.called(t) != nil {
		t.Fatalf("retention-days 365: exit-code %q\n%s", res.outputs["exit-code"], res.log)
	}
	contains(t, "summary", res.summary, `retention-days "365": want a number of days from 1 to 90`)
}
