// Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
// SPDX-License-Identifier: MIT

package action

import (
	"flag"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

var update = flag.Bool("update", false, "rewrite the golden summaries in testdata")

// private is what a captured message carries that a public job summary
// must never show: recipients, numbers, subjects and bodies.
var private = []string{"jane.doe", "Jane", "412", "W-1234", "4411", "bob@", "Welcome"}

func TestSummaryReport(t *testing.T) {
	needBash(t, "jq")
	sh := filepath.Join(root(t), "integrations", "action", "summary.sh")
	cases := []struct {
		json string
		want []string // must be in the summary
	}{
		{"comparison", []string{"**Something broke.** 4 actions compared with snapshot \"main\".", "| 1 | 1 | 1 | 1 |",
			"### ✗ BROKE (1)", "| Email to ***@customer.example is no longer sent | Order form |",
			"SMS to +*** (\"***\") is now sent | Order form, Sign-up, Reset +1 more", "Subject changed \\| escaped", "1 note in the full report."}},
		{"same", []string{"**Everything is still sent the same way.** 1 action compared with the earlier run.", "| 0 | 0 | 0 | 0 |"}},
		{"run", []string{"**Snapshot \"ci\" saved; 1 action failed.** 2 actions run; 2 emails, 1 SMS sent.",
			"| Contact form | failed: POST http://127.0.0.1:20519/contact: connection refused | 1 |",
			"| error | Unfilled placeholder {first_name} in email to ***@customer.example | Order form |",
			"| notice | Email has no plain-text part | Order form, Contact form |"}},
	}
	for _, c := range cases {
		t.Run(c.json, func(t *testing.T) {
			out, errOut, code := script(t, sh, nil, "report", filepath.Join("testdata", c.json+".json"))
			if code != 0 {
				t.Fatalf("exit %d: %s", code, errOut)
			}
			for _, w := range append(c.want, "## ♪ Echo", "Set `summary-details: true` to show them") {
				if !strings.Contains(out, w) {
					t.Errorf("summary lacks %q:\n%s", w, out)
				}
			}
			for _, p := range private {
				if strings.Contains(out, p) {
					t.Errorf("summary shows %q:\n%s", p, out)
				}
			}
			golden(t, filepath.Join("testdata", c.json+".md"), out)
		})
	}
}

func TestSummaryMask(t *testing.T) {
	needBash(t, "jq")
	sh := filepath.Join(root(t), "integrations", "action", "summary.sh")
	cases := []struct{ in, want string }{
		{"12:00:01 email to jane@x.example, bob@y.example: Your order for Jane [sealed]", "12:00:01 email to ***@x.example, ***@y.example: *** [sealed]"},
		{"12:00:02 SMS to +61 412 345 678: Hi Jane, your code [sent]", "12:00:02 SMS to +***: *** [sent]"},
		{`Welcome email ("Hi Jane") is no longer sent`, `Welcome email ("***") is no longer sent`},
		{"Echo listening on 127.0.0.1:8025 with token tok3n", "Echo listening on 127.0.0.1:8025 with token ***"},
		{"nothing private on 2026-10-24 12:00:01", "nothing private on 2026-10-24 12:00:01"},
	}
	for _, c := range cases {
		cmd := exec.Command("bash", sh, "mask") //nolint:gosec // our own script
		cmd.Env = append(os.Environ(), "RHYTHM_ECHO_TOKEN=tok3n")
		cmd.Stdin = strings.NewReader(c.in + "\n")
		out, err := cmd.Output()
		if err != nil {
			t.Fatal(err)
		}
		if got := strings.TrimSuffix(string(out), "\n"); got != c.want {
			t.Errorf("mask %q\n got %q\nwant %q", c.in, got, c.want)
		}
	}
}

// golden compares got with the file at path, or rewrites it with -update.
func golden(t *testing.T, path, got string) {
	t.Helper()
	if *update {
		if err := os.WriteFile(path, []byte(got), 0o600); err != nil {
			t.Fatal(err)
		}
		return
	}
	want, err := os.ReadFile(path) //nolint:gosec // testdata
	if err != nil {
		t.Fatal(err)
	}
	if got != string(want) {
		t.Errorf("%s differs (go test -update rewrites it):\n%s", path, got)
	}
}

func TestSummaryComment(t *testing.T) {
	needBash(t, "jq")
	sh := filepath.Join(root(t), "integrations", "action", "summary.sh")
	env := []string{"GITHUB_RUN_ID=9", "GITHUB_REPOSITORY=o/r", "GITHUB_SERVER_URL=https://github.com"}
	cases := []struct {
		name    string
		args    []string
		want    []string
		private bool // must hide what a message said
	}{
		{"comparison", []string{"testdata/comparison.json"}, []string{"**Something broke.** Compared with snapshot \"main\": 1 broke, 1 new, 1 changed.",
			"**✗ BROKE (1)**\n\n- Email to ***@customer.example is no longer sent (Order form)", "**▲ NEW (1)**",
			"[this run](https://github.com/o/r/actions/runs/9)"}, true},
		{"details", []string{"--details", "testdata/comparison.json"}, []string{"- Email to jane.doe@customer.example is no longer sent (Order form)"}, false},
		{"same", []string{"testdata/same.json"}, []string{"**Everything is still sent the same way.** Compared with the earlier run: 0 broke, 0 new, 0 changed."}, true},
		{"run", []string{"testdata/run.json"}, []string{"**Something is wrong.** 2 actions run: 1 failed, 1 with errors.",
			"- Action fails: POST http://127.0.0.1:20519/contact: connection refused (Contact form)",
			"- Unfilled placeholder {first_name} in email to ***@customer.example (Order form)"}, true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			out, errOut, code := script(t, sh, env, append([]string{"comment"}, c.args...)...)
			if code != 0 {
				t.Fatalf("exit %d: %s", code, errOut)
			}
			for _, w := range c.want {
				if !strings.Contains(out, w) {
					t.Errorf("comment lacks %q:\n%s", w, out)
				}
			}
			if strings.Contains(out, "CHANGED") || strings.Contains(out, "FIXED") {
				t.Errorf("comment lists more than broke and new:\n%s", out)
			}
			if c.private {
				for _, p := range private {
					if strings.Contains(out, p) {
						t.Errorf("comment shows %q:\n%s", p, out)
					}
				}
			}
		})
	}
}

func TestSummaryError(t *testing.T) {
	needBash(t, "jq")
	sh := filepath.Join(root(t), "integrations", "action", "summary.sh")
	dir := t.TempDir()
	write := func(name, s string) string {
		p := filepath.Join(dir, name)
		if err := os.WriteFile(p, []byte(s), 0o600); err != nil {
			t.Fatal(err)
		}
		return p
	}
	var log strings.Builder
	for i := 1; i <= 40; i++ {
		log.WriteString("line " + strconv.Itoa(i) + "\n")
	}
	log.WriteString("12:00:01 email to jane@x.example: Your order for Jane [sealed]\n```\n")
	logf := write("echo.log", log.String())
	errf := write("stderr", "rhythm echo dev - http://shop.test\nrhythm echo: --fail-on \"often\": want broke, new or changed\n")

	out, _, code := script(t, sh, nil, "error", "2", errf, logf)
	if code != 0 {
		t.Fatalf("exit %d", code)
	}
	for _, w := range []string{"**Echo didn't run: the command line or the config has a mistake (exit 2).**",
		"```\nrhythm echo: --fail-on \"often\": want broke, new or changed\n```", // quotes kept: they say what was wrong
		"<summary>The last 30 lines of echo.log</summary>", "line 13\n", "email to ***@x.example: *** [sealed]", "` ` `"} {
		if !strings.Contains(out, w) {
			t.Errorf("summary lacks %q:\n%s", w, out)
		}
	}
	for _, p := range []string{"rhythm echo dev", "line 12\n", "jane@", "Your order"} {
		if strings.Contains(out, p) {
			t.Errorf("summary shows %q:\n%s", p, out)
		}
	}

	// Output without an error line: its end. No log: no log section.
	out, _, _ = script(t, sh, nil, "error", "3", write("plain", "dial tcp 127.0.0.1:1: connection refused\n"), filepath.Join(dir, "none"))
	if !strings.Contains(out, "**Echo couldn't run (exit 3).**") || !strings.Contains(out, "connection refused") || strings.Contains(out, "echo.log") {
		t.Errorf("exit 3 summary:\n%s", out)
	}
}
