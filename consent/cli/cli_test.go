// Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
// SPDX-License-Identifier: MIT

package cli

import (
	"bytes"
	"flag"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"
)

var update = flag.Bool("update", false, "rewrite the golden files in testdata")

// testdata is the package's testdata folder, found before a test moves
// to a folder of its own.
var testdata = func() string {
	wd, err := os.Getwd()
	if err != nil {
		panic(err)
	}
	return filepath.Join(wd, "testdata")
}()

// runMain runs Main with args and returns the exit code and both
// streams.
func runMain(t *testing.T, args ...string) (code int, stdout, stderr string) {
	t.Helper()
	var out, errOut bytes.Buffer
	code = Main(args, Env{Version: "1.2.0", Stdout: &out, Stderr: &errOut, Stdin: strings.NewReader(""),
		Now: func() time.Time { return time.Date(2026, 10, 26, 9, 20, 41, 0, time.UTC) }})
	return code, out.String(), errOut.String()
}

// golden compares got with testdata/<name>.golden, or rewrites it
// with -update. Line ends are normalised, so a CRLF checkout passes.
func golden(t *testing.T, name, got string) {
	t.Helper()
	path := filepath.Join(testdata, name+".golden")
	if *update {
		if err := os.MkdirAll(testdata, 0o750); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(got), 0o600); err != nil {
			t.Fatal(err)
		}
		return
	}
	want, err := os.ReadFile(path) //nolint:gosec // a test's own golden file
	if err != nil {
		t.Fatalf("%v (run go test -update to write it)", err)
	}
	if w := strings.ReplaceAll(string(want), "\r\n", "\n"); w != got {
		t.Errorf("%s differs from the golden file (go test -update rewrites it)\ngot:\n%s\nwant:\n%s", name, got, w)
	}
}

// Every help text is pinned, prints on stdout for --help and fits 80
// columns below its first line.
func TestHelpGolden(t *testing.T) {
	for _, s := range append([]sub{subCheck}, subcommands...) {
		name := "help-consent"
		args := []string{"--help"}
		if s != subCheck {
			name += "-" + string(s)
			args = []string{string(s), "--help"}
		}
		code, out, errOut := runMain(t, args...)
		if code != exitOK || errOut != "" {
			t.Errorf("%v: exit %d, stderr %q", args, code, errOut)
		}
		if out != usage(s) {
			t.Errorf("%v printed something other than its usage", args)
		}
		golden(t, name, out)
		for i, l := range strings.Split(out, "\n") {
			if i > 0 && len([]rune(l)) > 80 {
				t.Errorf("%s line %d is %d columns: %q", name, i+1, len([]rune(l)), l)
			}
		}
	}
	if !strings.HasPrefix(Usage, "rhythm echo consent: ") || Usage != usage(subCheck) {
		t.Error("Usage isn't the check's help")
	}
	for _, want := range []string{"What to check:", "Compare:", "Requests and browser:", "Output:", "Settings:", "Subcommands", "Exit codes:", "Examples:",
		"rhythm echo consent https://staging.shop.test\n", "rhythm echo consent --runs +gpc --geo de https://shop.example\n",
		"rhythm echo consent --against baseline --fail-on broke\n", "What a pass means.", "Keyless signing in the Action"} {
		if !strings.Contains(Usage, want) {
			t.Errorf("the help lacks %q", want)
		}
	}
	// -h works anywhere on the line, for the subcommand it names.
	if code, out, _ := runMain(t, "--json", "cmps", "-h"); code != exitOK || out != usage(subCmps) {
		t.Errorf("--json cmps -h: %d", code)
	}
}

// The help of the check lists every flag it reads and only those.
func TestHelpListsFlags(t *testing.T) {
	for _, d := range flagDefs {
		line := regexp.MustCompile(`(?m)^ +(-[A-Za-z], )?--` + regexp.QuoteMeta(d.name) + `\b`)
		in := line.MatchString(Usage) || isFormatFlag(d.name) && strings.Contains(Usage, "--json --md --junit --html")
		if reads := d.subs&inCheck != 0; reads != in {
			t.Errorf("--%s: read by consent %v, in its help %v", d.name, reads, in)
		}
		for _, s := range subcommands {
			if d.subs&s.bit() != 0 && !line.MatchString(usage(s)) {
				t.Errorf("consent %s reads --%s but its help doesn't list it", s, d.name)
			}
		}
	}
}

func TestUnknownFlag(t *testing.T) {
	for args, want := range map[string]string{
		"--rnus none https://shop.test/": "rhythm echo: unknown flag --rnus; did you mean --runs?\n",
		"cmps --jsno":                    "rhythm echo: unknown flag --jsno; did you mean --json?\n",
		"--zzzzzzz https://shop.test/":   "rhythm echo: unknown flag --zzzzzzz\n",
		"--pages":                        "rhythm echo: --pages needs a value\n",
		"--settle soon https://a.test/":  "rhythm echo: --settle \"soon\": want a duration like 10m or 5s\n",
		"--pages many https://a.test/":   "rhythm echo: --pages \"many\": want a whole number\n",
		"--echo=ftp://x https://a.test/": "rhythm echo: --echo \"ftp://x\": want Echo's address, e.g. --echo=http://127.0.0.1:8025\n",
	} {
		code, out, errOut := runMain(t, strings.Fields(args)...)
		if code != exitUsage || out != "" || errOut != want {
			t.Errorf("%s: exit %d\n got %q\nwant %q", args, code, errOut, want)
		}
	}
}

// A flag a subcommand doesn't read is a usage error in Echo's words.
func TestFlagNotRead(t *testing.T) {
	for args, want := range map[string]string{
		"cmps --har":                       "rhythm echo: --har has no effect on consent cmps (rhythm echo consent cmps --help lists its flags)\n",
		"prune --keep 3 --json":            "rhythm echo: --json has no effect on consent prune (rhythm echo consent prune --help lists its flags)\n",
		"--key team.pub https://shop.test": "rhythm echo: --key has no effect on consent (rhythm echo consent --help lists its flags)\n",
		"detect -o x.json --runs none u":   "rhythm echo: --output and --runs have no effect on consent detect (rhythm echo consent detect --help lists its flags)\n",
		"verify -H a:b dir":                "rhythm echo: --header has no effect on consent verify (rhythm echo consent verify --help lists its flags)\n",
	} {
		code, _, errOut := runMain(t, strings.Fields(args)...)
		if code != exitUsage || errOut != want {
			t.Errorf("%s: exit %d\n got %q\nwant %q", args, code, errOut, want)
		}
	}
	// A short flag is named by its long name, as Echo's other commands do.
	if code, _, errOut := runMain(t, "detect", "-o", "x.json", "u"); code != exitUsage || !strings.Contains(errOut, "--output has no effect on consent detect") {
		t.Errorf("--output on detect: %d %q", code, errOut)
	}
}

// --fail-on has the parser and the words of Echo's other commands
// (cmd/rhythm-echo checkFailOn).
func TestFailOnMessage(t *testing.T) {
	code, _, errOut := runMain(t, "--fail-on", "high", "https://shop.test/")
	if want := "rhythm echo: --fail-on \"high\": want none, error, warning or notice (or broke, new, changed when comparing)\n"; code != exitUsage || errOut != want {
		t.Errorf("exit %d %q", code, errOut)
	}
	code, _, errOut = runMain(t, "--fail-on", "warning", "--against", "baseline", "https://shop.test/")
	if want := "rhythm echo: --fail-on \"warning\": want none, broke, new or changed\n"; code != exitUsage || errOut != want {
		t.Errorf("comparing: exit %d %q", code, errOut)
	}
	for _, tc := range []struct {
		in       string
		compared bool
		want     string
	}{{"", false, "error"}, {"", true, "broke"}, {"WARNING", false, "warning"}, {" none ", true, "none"}, {"changed", true, "changed"}, {"notice", false, "notice"}} {
		got, err := checkFailOn(tc.in, tc.compared)
		if err != nil || got != tc.want {
			t.Errorf("checkFailOn(%q, %v) = %q, %v; want %q", tc.in, tc.compared, got, err, tc.want)
		}
	}
	if _, err := checkFailOn("broke", false); err == nil {
		t.Error("broke without --against was taken")
	}
}

// The eight schemes PRD 14.3 names and a URL with a login are usage
// errors with message 2 (or the login message), masked.
func TestConsentRejectsSchemes(t *testing.T) {
	for _, u := range []string{"file:///etc/passwd", "data:text/html,<b>x</b>", "javascript:alert(1)", "chrome://settings",
		"devtools://devtools/bundled/inspector.html", "view-source:https://shop.test/", "about:blank", "ftp://shop.test",
		"FILE:///etc/hosts", "gopher://shop.test/", "ws://shop.test/"} {
		code, out, errOut := runMain(t, u)
		if want := "rhythm echo: consent \"" + u + "\": want an http or https URL\n"; code != exitUsage || out != "" || errOut != want {
			t.Errorf("%s: exit %d %q", u, code, errOut)
		}
	}
	pass := "hunter" + "2secret"
	code, _, errOut := runMain(t, "https://user:"+pass+"@shop.test/")
	if code != exitUsage || strings.Contains(errOut, pass) || strings.Contains(errOut, "user:") ||
		!strings.HasSuffix(errOut, "@shop.test/\": a URL with a login in it isn't allowed; pass it with -H \"Authorization: env:RHYTHM_ECHO_NAME\"\n") {
		t.Errorf("login in the URL: exit %d %q", code, errOut)
	}
}

func TestURLRules(t *testing.T) {
	for _, tc := range []struct{ raw, site, want string }{
		{"shop.test", "", "https://shop.test/"},
		{"shop.test/menu?x=1", "", "https://shop.test/menu?x=1"},
		{"http://shop.test", "", "http://shop.test/"},
		{"HTTPS://Shop.test/a", "", "https://Shop.test/a"},
		{"localhost:8443", "", "https://localhost:8443/"},
		{"127.0.0.1:18950/good/", "", "https://127.0.0.1:18950/good/"},
		{"//shop.test/x", "", "https://shop.test/x"},
		{"/shop/", "https://staging.shop.test", "https://staging.shop.test/shop/"},
		{"/shop/", "https://staging.shop.test/base/", "https://staging.shop.test/shop/"},
		{"http://report/", "", "http://report/"},
	} {
		u, err := target(tc.raw, tc.site)
		if err != nil || u.String() != tc.want {
			t.Errorf("target(%q, %q) = %v, %v; want %s", tc.raw, tc.site, u, err, tc.want)
		}
	}
	for _, tc := range []struct{ raw, site, want string }{
		{"/shop/", "", `consent "/shop/": a path needs site: in .echo/config.yaml; give the whole URL, e.g. https://staging.shop.test/shop/`},
		{"/shop/", "ftp://x", `consent "/shop/": site: "ftp://x" isn't an http or https URL`},
		{"", "", "consent: an empty URL; give a page like https://staging.shop.test/"},
		{"mailto:shop", "", `consent "mailto:shop": want an http or https URL`},
		{"https://", "", `consent "https://": want an http or https URL`},
	} {
		if _, err := target(tc.raw, tc.site); err == nil || err.Error() != tc.want {
			t.Errorf("target(%q, %q): %v; want %s", tc.raw, tc.site, err, tc.want)
		}
	}
	// A subcommand's name is the subcommand only as the first word; a
	// host of that name is written as a URL.
	if s := subOf(newFlagSet(&options{}), []string{"--json", "report"}); s != subReport {
		t.Errorf("--json report picks %q", s)
	}
	if s := subOf(newFlagSet(&options{}), []string{"--geo", "de", "http://report/"}); s != subCheck {
		t.Errorf("http://report/ picks %q", s)
	}
	if s := subOf(newFlagSet(&options{}), []string{"--runs", "report"}); s != subCheck {
		t.Errorf("--runs report picks %q (report is the flag's value)", s)
	}
	code, _, errOut := runMain(t, "https://shop.test/", "report")
	if want := "rhythm echo: consent \"report\": report is a subcommand; put it first (rhythm echo consent report), or write the host as a URL (http://report/)\n"; code != exitUsage || errOut != want {
		t.Errorf("a subcommand word after a URL: %d %q", code, errOut)
	}
}

func TestOutputFormats(t *testing.T) {
	resolve := func(args ...string) ([]output, error) {
		t.Helper()
		o, _, err := parse(subCheck, args)
		if err != nil {
			t.Fatal(err)
		}
		return o.reports()
	}
	outs, err := resolve("-o", "a.json", "-o", "b.md", "--output", "c.xml", "-o", "d.HTML")
	if err != nil || len(outs) != 4 || outs[0] != (output{fmtJSON, "a.json"}) || outs[1] != (output{fmtMD, "b.md"}) ||
		outs[2] != (output{fmtJUnit, "c.xml"}) || outs[3] != (output{fmtHTML, "d.HTML"}) || stdoutFormat(outs) != "" {
		t.Errorf("four files: %v %v", outs, err)
	}
	if outs, err := resolve("--json"); err != nil || len(outs) != 1 || stdoutFormat(outs) != fmtJSON {
		t.Errorf("--json: %v %v", outs, err)
	}
	// --json with an -o of that format writes the file only; another
	// format flag then has stdout to itself.
	if outs, err := resolve("--json", "-o", "a.json", "--md"); err != nil || stdoutFormat(outs) != fmtMD || !wants(outs, fmtJSON) {
		t.Errorf("--json -o a.json --md: %v %v", outs, err)
	}
	if outs, err := resolve("--md", "-o", "summary.txt"); err != nil || len(outs) != 1 || outs[0] != (output{fmtMD, "summary.txt"}) {
		t.Errorf("--md -o summary.txt: %v %v", outs, err)
	}
	if _, err := resolve("--json", "--md"); err == nil || err.Error() != "pick one output format, not --json and --md" {
		t.Errorf("--json --md: %v", err)
	}
	if _, err := resolve("--html", "--junit", "--json"); err == nil || err.Error() != "pick one output format, not --html and --json and --junit" {
		t.Errorf("three formats: %v", err)
	}
	if _, err := resolve("-o", "x.txt"); err == nil || err.Error() != "can't tell the format of x.txt; add --json, --md, --junit or --html" {
		t.Errorf("-o x.txt: %v", err)
	}
	if code, _, errOut := runMain(t, "--json", "--md", "https://shop.test/"); code != exitUsage || errOut != "rhythm echo: pick one output format, not --json and --md\n" {
		t.Errorf("--json --md: %d %q", code, errOut)
	}
}

// TestNoURLMessage: no URL, no --sites and no config is message 1.
func TestNoURLMessage(t *testing.T) {
	t.Chdir(t.TempDir())
	code, out, errOut := runMain(t)
	if code != exitUsage || out != "" || errOut != "rhythm echo: "+msgNoPage+"\n" {
		t.Errorf("exit %d %q", code, errOut)
	}
	if want := "rhythm echo: consent needs a page: rhythm echo consent https://staging.shop.test, or list pages under consent: urls in .echo/config.yaml\n"; errOut != want {
		t.Errorf("message 1 is %q", errOut)
	}
}

// TestUsageMessages pins every exit-2 message the check prints, PRD
// 10.2 and the Plan.Validate ranges among them.
func TestUsageMessages(t *testing.T) {
	cases := [][]string{
		{},
		{"ftp://shop.test"},
		{"--runs", "decline", "https://shop.test/"},
		{"--runs", "+gpc,nope", "https://shop.test/"},
		{"--fail-on", "high", "https://shop.test/"},
		{"--consent-mode", "loose", "https://shop.test/"},
		{"--click", "mouse", "https://shop.test/"},
		{"--cmp", "cookiebott", "https://shop.test/"},
		{"--cmp", "quantcast", "https://shop.test/"},
		{"--geo", "deu", "https://shop.test/"},
		{"--locale", "german!", "https://shop.test/"},
		{"--timezone", "Berlin time", "https://shop.test/"},
		{"--viewport", "tablet", "https://shop.test/"},
		{"--accept-selector", "a >> >> b", "https://shop.test/"},
		{"--reject-selector", "", "https://shop.test/"},
		{"--pages", "11", "https://shop.test/"},
		{"--pages", "-1", "https://shop.test/"},
		{"--settle", "45s", "https://shop.test/"},
		{"--settle", "-1s", "https://shop.test/"},
		{"--banner-wait", "2m", "https://shop.test/"},
		{"--banner-wait", "-1s", "https://shop.test/"},
		{"--timeout", "11m", "https://shop.test/"},
		{"--timeout", "0s", "https://shop.test/"},
		{"--retries", "5", "https://shop.test/"},
		{"--parallel", "9", "https://shop.test/"},
		{"--parallel", "0", "https://shop.test/"},
		{"--json", "--junit", "https://shop.test/"},
		{"-o", "report.pdf", "https://shop.test/"},
	}
	var b strings.Builder
	for _, args := range cases {
		code, out, errOut := runMain(t, args...)
		if code != exitUsage || out != "" {
			t.Errorf("%v: exit %d, stdout %q", args, code, out)
		}
		b.WriteString("$ consent " + strings.Join(args, " ") + "\n" + errOut)
	}
	golden(t, "usage-messages", b.String())
}
