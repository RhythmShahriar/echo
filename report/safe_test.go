// Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
// SPDX-License-Identifier: MIT

package report

import (
	"bytes"
	"strings"
	"testing"
	"time"

	"github.com/charmbracelet/lipgloss"
	"github.com/muesli/termenv"

	"github.com/RhythmShahriar/echo/model"
)

// hostile is the subject from the security review: it sets the window
// title, clears the screen and plants an OSC 8 link.
const hostile = "Order \x1b]0;PWNED-TITLE\x07\x1b[2J\x1b]8;;https://evil.example/\x07click\x1b]8;;\x07 done"

func TestSafe(t *testing.T) {
	cases := map[string]string{
		hostile:                 `Order \x1b]0;PWNED-TITLE\x07\x1b[2J\x1b]8;;https://evil.example/\x07click\x1b]8;;\x07 done`,
		"a\r\nb":                `a\x0d\x0ab`,
		"tab\tkept":             "tab\tkept",
		"del\x7f":               `del\x7f`,
		"c1 \u009b31m":          `c1 \u009b31m`,
		"lone \xed\xa0\x80 sur": `lone \xed\xa0\x80 sur`,
		"bad \xff byte":         `bad \xff byte`,
		"BROKE \u202edexif":     `BROKE \u202edexif`, // RLO shows "fixed" backwards
		"\u2066isolate\u2069":   `\u2066isolate\u2069`,
		"":                      "",
	}
	for in, want := range cases {
		if got := Safe(in); got != want {
			t.Errorf("Safe(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestSafeKeepsText(t *testing.T) {
	for _, s := range []string{
		"Your order 🎉🛒 is on its way",
		"مرحبا بك في متجرنا",     // Arabic, right to left
		"שלום, ההזמנה שלך נשלחה", // Hebrew
		"Grüße aus Köln · ✓ 日本語", // accents, symbols, CJK
		"👩‍👩‍👧 family (ZWJ)",
		"left \u200f mark", // RLM only nudges neutrals
	} {
		if got := Safe(s); got != s {
			t.Errorf("Safe changed %q to %q", s, got)
		}
	}
}

// controls reports a byte below 0x20 other than newline and tab, or DEL.
func controls(s string) (byte, bool) {
	for i := 0; i < len(s); i++ {
		if c := s[i]; (c < 0x20 && c != '\n' && c != '\t') || c == 0x7f {
			return c, true
		}
	}
	return 0, false
}

func hostileRun() *model.Run {
	at := time.Date(2026, 10, 24, 9, 0, 0, 0, time.UTC)
	m := model.Message{ID: "m1", Channel: model.Email, Status: model.Faked, To: []string{"b@y.test\x1b[2J"}, Subject: hostile,
		Error: "smtp: " + hostile, At: at}
	i := model.Issue{Check: "content/x", Level: model.Error, Title: "Placeholder in " + hostile, Detail: hostile + "\nsecond " + hostile, Fix: hostile}
	i.Identify()
	return &model.Run{Schema: model.Schema, Version: "1.0.0", Mode: "seal", Started: at, Target: "https://x.test/\x1b]0;t\x07",
		Actions:  []model.Action{{ID: "a", Label: "Form " + hostile, Error: hostile, Messages: []model.Message{m}, Issues: []model.Issue{i}}},
		Warnings: []string{hostile}}
}

func hostileComparison() *model.Comparison {
	return &model.Comparison{Schema: model.Schema, Version: "1.0.0", Actions: 1, Before: model.RunRef{Name: "snap\x1b[2J"},
		Changes: []model.Change{
			{Class: model.Changed, Check: "message/subject", Title: "Email: subject changed", Actions: []string{"Form " + hostile}, Before: "Order", After: hostile},
			{Class: model.Broke, Check: "message/missing", Title: "Email " + hostile + " is no longer sent", Actions: []string{"a"}, Detail: hostile, Fix: hostile},
			{Class: model.Fixed, Check: "content/x", Title: hostile, Actions: []string{"a"}},
		},
		Warnings: []string{hostile}}
}

func TestPrettyEscapesCapturedText(t *testing.T) {
	for _, color := range []bool{false, true} {
		var buf bytes.Buffer
		p := NewPretty(&buf, &buf, PrettyOptions{Plain: !color, Verbose: true})
		if color {
			prev := lipgloss.ColorProfile()
			lipgloss.SetColorProfile(termenv.TrueColor)
			t.Cleanup(func() { lipgloss.SetColorProfile(prev) })
		}
		r := hostileRun()
		p.Header("1.0.0", r.Target)
		p.Step("Snapshot", hostile)
		p.Log(hostile)
		p.Message(r.Actions[0].Messages[0])
		p.Action(&r.Actions[0], 1, 1)
		p.Summary(Result{Run: r}, "")
		p.Summary(Result{Run: r, Comparison: hostileComparison()}, "")
		out := buf.String()
		if color && !strings.Contains(out, "\x1b[") {
			t.Fatal("the color profile wasn't applied: the test would prove nothing")
		}
		if !color {
			if c, bad := controls(out); bad {
				t.Errorf("plain output has control byte %#x:\n%s", c, out)
			}
		}
		// In color the only escapes are Echo's own SGR colors: no OSC (title,
		// links), no BEL, no screen clearing.
		for _, bad := range []string{"\x1b]", "\x07", "\x1b[2J"} {
			if strings.Contains(out, bad) {
				t.Errorf("color=%v output has %q:\n%q", color, bad, out)
			}
		}
		if !strings.Contains(out, `\x1b]0;PWNED-TITLE\x07`) {
			t.Errorf("color=%v: the escaped subject should still be shown:\n%s", color, out)
		}
	}
}

func TestMarkdownAndJUnitEscapeCapturedText(t *testing.T) {
	for _, r := range []Result{{Run: hostileRun()}, {Run: hostileRun(), Comparison: hostileComparison()}} {
		var md, ju bytes.Buffer
		if err := Markdown(&md, r); err != nil {
			t.Fatal(err)
		}
		if err := JUnit(&ju, r, "changed"); err != nil {
			t.Fatal(err)
		}
		for name, out := range map[string]string{"markdown": md.String(), "junit": ju.String()} {
			if c, bad := controls(out); bad {
				t.Errorf("%s (compared=%v) has control byte %#x:\n%s", name, r.Compared(), c, out)
			}
		}
	}
}

func TestMarkdownCellEscapes(t *testing.T) {
	got := cell("a|b `code` [link](https://evil.example) <b>\nnext")
	want := "a\\|b \\`code\\` \\[link\\](https://evil.example) &lt;b&gt; next"
	if got != want {
		t.Errorf("cell = %q, want %q", got, want)
	}
	if got := cell("Grüße 🎉 مرحبا"); got != "Grüße 🎉 مرحبا" {
		t.Errorf("cell changed plain text: %q", got)
	}
}
