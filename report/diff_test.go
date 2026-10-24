// Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
// SPDX-License-Identifier: MIT

package report

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"

	"github.com/RhythmShahriar/echo/model"
)

// receiptChange is the s11 receipt with the line the broken template
// adds plus a second edit far below it, so the change has two hunks.
func receiptChange() model.Change {
	lines := []string{"Hello echo-test bb5c41,", "", "Thanks for your wholesale order W-7462 for Acme.", "",
		"  1 x Espresso blend, 5 kg  $89.00", "", "  2 x Decaf Peru, 1 kg  $48.00", "", "", "We'll invoice you when it ships.",
		"", "Bean & Leaf Wholesale", "a", "b", "c", "d", "e", "Questions? Reply to this email."}
	before := strings.Join(lines, "\n")
	after := strings.Replace(before, "  2 x Decaf Peru, 1 kg  $48.00\n\n", "  2 x Decaf Peru, 1 kg  $48.00\n\nOrder total: $42.00\n", 1)
	after = strings.Replace(after, "Questions? Reply to this email.", "Questions? Call us `now`.", 1)
	after = strings.Replace(after, "Hello echo-test bb5c41", "Hello echo-test 03ce4d", 1)
	return model.Change{Class: model.Changed, Check: "message/text", Channel: model.Email,
		Title: "Email to the test submitter: text changed", Actions: []string{"Wholesale order"}, Before: before, After: after,
		Fix: "Read the new text; if the change is intended, take a new snapshot."}
}

func diffResult() Result {
	c := sampleComparison()
	c.Changes = []model.Change{receiptChange()}
	return Result{Run: comparedRun(), Comparison: c}
}

func TestPlainShowsTheFirstHunk(t *testing.T) {
	var buf bytes.Buffer
	NewPretty(&buf, &buf, PrettyOptions{Plain: true}).Summary(diffResult(), "")
	out := buf.String()
	for _, want := range []string{
		"[CHANGED] Email to the test submitter: text changed - Wholesale order\n  line 9 added: Order total: $42.00\n",
		"    + Order total: $42.00\n",
		"      2 x Decaf Peru, 1 kg  $48.00\n",
		"    We'll invoice you when it ships.\n",
		"  +1 more place changed (-v shows them all)\n",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("missing %q in:\n%s", want, out)
		}
	}
	if strings.Contains(out, "Call us") || strings.Contains(out, " → ") {
		t.Errorf("only the first hunk without -v; no before → after:\n%s", out)
	}
}

func TestVerboseShowsEveryHunk(t *testing.T) {
	for _, plain := range []bool{true, false} {
		var buf bytes.Buffer
		NewPretty(&buf, &buf, PrettyOptions{Plain: plain, Verbose: true}).Summary(diffResult(), "")
		out := buf.String()
		for _, want := range []string{"line 9 added: Order total: $42.00", "line 19 changed: Questions? Reply to this email. →",
			"- Questions? Reply to this email.", "+ Questions? Call us `now`."} {
			if !strings.Contains(out, want) {
				t.Errorf("plain=%v: missing %q in:\n%s", plain, want, out)
			}
		}
		if strings.Contains(out, "more place") {
			t.Errorf("plain=%v: -v shows them all:\n%s", plain, out)
		}
	}
}

func TestMarkdownHasADiffBlock(t *testing.T) {
	var buf bytes.Buffer
	if err := Markdown(&buf, diffResult()); err != nil {
		t.Fatal(err)
	}
	md := buf.String()
	for _, want := range []string{
		"| line 9 added: Order total: $42.00 (+1 more) |",
		"<details><summary>Diff: Email to the test submitter: text changed</summary>\n\n```diff\n@@ line 9 added: Order total: $42.00 @@\n",
		"\n+ Order total: $42.00\n",
		"\n- Questions? Reply to this email.\n+ Questions? Call us `now`.\n",
		"\n```\n\n</details>",
	} {
		if !strings.Contains(md, want) {
			t.Errorf("missing %q in:\n%s", want, md)
		}
	}
}

func TestHTMLMarksInsertionsAndDeletions(t *testing.T) {
	var buf bytes.Buffer
	if err := HTML(&buf, diffResult()); err != nil {
		t.Fatal(err)
	}
	h := buf.String()
	for _, want := range []string{
		`<div class="hunk">line 9 added: Order total: $42.00</div>`,
		`<ins>+ Order total: $42.00</ins>`,
		`<del>- Questions? Reply to this email.</del>`,
		`<span>  Bean &amp; Leaf Wholesale</span>`,
	} {
		if !strings.Contains(h, want) {
			t.Errorf("missing %q in the HTML report", want)
		}
	}
}

func TestJSONKeepsTheFullTexts(t *testing.T) {
	var buf bytes.Buffer
	r := diffResult()
	if err := JSON(&buf, r); err != nil {
		t.Fatal(err)
	}
	var back model.Comparison
	if err := json.Unmarshal(buf.Bytes(), &back); err != nil || back.Changes[0].Before != r.Comparison.Changes[0].Before ||
		back.Changes[0].After != r.Comparison.Changes[0].After {
		t.Errorf("JSON must keep the full before and after: %v", err)
	}
}

func TestOtherChangesKeepBeforeAfter(t *testing.T) {
	c := model.Change{Class: model.Changed, Check: "message/subject", Before: "Thanks", After: "Thank you"}
	if got := beforeAfter(c); got != "Thanks → Thank you" {
		t.Errorf("beforeAfter = %q", got)
	}
}
