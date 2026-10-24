// Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
// SPDX-License-Identifier: MIT

package action

import (
	"flag"
	"os"
	"os/exec"
	"path/filepath"
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
