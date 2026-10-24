// Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
// SPDX-License-Identifier: MIT

package compare

import (
	"encoding/json"
	"math/rand/v2"
	"strings"
	"testing"

	"github.com/RhythmShahriar/echo/model"
)

// s11 is the testbed's plain PHP receipt (testbed/php/site/templates):
// receipt-2.php adds an "Order total" line that prints $total, which the
// page never sets, so PHP's warning lands in the mail.
func s11(name, order string, broken bool) string {
	b := "Hello " + name + ",\n\nThanks for your wholesale order " + order + " for Acme.\n\n" +
		"  1 x Espresso blend, 5 kg  $89.00\n\n  2 x Decaf Peru, 1 kg  $48.00\n\n"
	if broken {
		b += "Order total: $<br />\n<b>Warning</b>:  Undefined variable $total in <b>/srv/templates/receipt-2.php</b> on line <b>9</b><br />\n0.00\n"
	}
	return b + "\nWe'll invoice you when it ships.\n\nBean & Leaf Wholesale\nhttps://shop.example.com/wholesale/\n"
}

func textChanges(b, a model.Message) []model.Change {
	var out []model.Change
	for _, c := range Messages([]model.Message{b}, []model.Message{a}) {
		if c.Check == "message/text" || c.Check == "message/payload" {
			out = append(out, c)
		}
	}
	return out
}

func TestReceiptDiffIsOneHunk(t *testing.T) {
	b := email("echo-test+bb5c41@example.com", "Your order", s11("echo-test bb5c41", "W-7462", false))
	a := email("echo-test+03ce4d@example.com", "Your order", s11("echo-test 03ce4d", "W-1601", true))
	cs := textChanges(b, a)
	if len(cs) != 1 {
		t.Fatalf("want one text change, got %q", titles(cs))
	}
	hs := ChangeDiff(cs[0])
	if len(hs) != 1 {
		t.Fatalf("want one hunk, got %d: %+v", len(hs), hs)
	}
	if got := hs[0].Summary(); !strings.HasPrefix(got, "lines 9-11 added: Order total: $") {
		t.Errorf("summary = %q", got)
	}
	var ctx, add int
	for _, l := range hs[0] {
		switch l.Op {
		case OpCtx:
			ctx++
		case OpAdd:
			add++
		}
	}
	if add != 3 || ctx != 4 {
		t.Errorf("hunk has %d added and %d context lines, want 3 and 4: %+v", add, ctx, hs[0])
	}
	// The full texts are kept (they were cut at 120 characters).
	if cs[0].Before != b.Text || cs[0].After != a.Text {
		t.Error("Before and After must be the full texts")
	}
	var back model.Change
	if js, err := json.Marshal(cs[0]); err != nil || json.Unmarshal(js, &back) != nil || back.After != a.Text {
		t.Errorf("JSON must carry the full after text: %v", err)
	}
}

func TestOneAddedLine(t *testing.T) {
	hs := Diff("a\nb\nc\nd\ne\nf\n", "a\nb\nc\nd\nOrder total: $42.00\ne\nf\n")
	if len(hs) != 1 || hs[0].Summary() != "line 5 added: Order total: $42.00" {
		t.Fatalf("hunks = %+v", hs)
	}
	want := []DiffLine{{OpCtx, 3, "c"}, {OpCtx, 4, "d"}, {OpAdd, 5, "Order total: $42.00"}, {OpCtx, 6, "e"}, {OpCtx, 7, "f"}}
	if len(hs[0]) != len(want) {
		t.Fatalf("hunk = %+v", hs[0])
	}
	for i := range want {
		if hs[0][i] != want[i] {
			t.Errorf("line %d = %+v, want %+v", i, hs[0][i], want[i])
		}
	}
}

func TestSummaries(t *testing.T) {
	for _, tc := range []struct{ before, after, want string }{
		{"one\ntwo\nthree", "one\nthree", "line 2 removed: two"},
		{"Hi there\nbye", "Hello there\nbye", "line 1 changed: Hi there → Hello there"},
		{"x", "x\ny\nz", "lines 2-3 added: y"},
		{"a\nb\nc", "a\nB2 is new\nC3 too", "lines 2-3 changed: B2 is new"},
	} {
		hs := Diff(tc.before, tc.after)
		if len(hs) != 1 || hs[0].Summary() != tc.want {
			t.Errorf("Diff(%q, %q) = %+v, want %q", tc.before, tc.after, hs, tc.want)
		}
	}
}

func TestFarApartChangesAreSeparateHunks(t *testing.T) {
	var a, b []string
	for i := range 20 {
		a = append(a, "line "+string(rune('a'+i)))
	}
	b = append(b, a...)
	b[2], b[15] = "changed one", "changed two"
	hs := Diff(strings.Join(a, "\n"), strings.Join(b, "\n"))
	if len(hs) != 2 {
		t.Fatalf("want 2 hunks, got %d", len(hs))
	}
	// Two changes 4 lines apart share their context: one hunk.
	b = append([]string(nil), a...)
	b[2], b[7] = "changed one", "changed two"
	if hs := Diff(strings.Join(a, "\n"), strings.Join(b, "\n")); len(hs) != 1 {
		t.Errorf("want 1 hunk, got %d", len(hs))
	}
}

func TestBlankLinesAndSpacingDontCount(t *testing.T) {
	if hs := Diff("Hello,\n\nThanks  for your order.\n", "Hello,\nThanks for your order.\n\n\n"); hs != nil {
		t.Errorf("hunks = %+v", hs)
	}
}

func TestNormalize(t *testing.T) {
	for in, want := range map[string]string{
		"Placed on 2026-10-24T09:00:00Z":                        "Placed on {date}",
		"Date: Sat, 24 Oct 2026 09:00:00 +0000":                 "Date: {date} {time}",
		"Ships 24 October 2026":                                 "Ships {date}",
		"Ships October 24th, 2026":                              "Ships {date}",
		"Due 10/24/2026 at 9:30 pm":                             "Due {date} at {time}",
		"Opened 24.10.2026 14:30 CEST":                          "Opened {date} {time}",
		"Thanks for your order W-7462":                          "Thanks for your order {number}",
		"Invoice #1042 is attached":                             "Invoice #{number} is attached",
		"Your reference: AB-1234":                               "Your reference: {number}",
		"Order total: $42.00":                                   "Order total: $#.#",
		"Use coupon SAVE-X7K2 at checkout":                      "Use coupon {code} at checkout",
		"Your discount code: WELCOME10":                         "Your discount code: {code}",
		"Use the code below":                                    "Use the code below",
		"Tracking 1b4e28ba-2fa1-11d2-883f-0016d3cca427":         "Tracking {uuid}",
		"Reset: https://shop.test/r?k=Zx81kQ0pLmN2bV7cX4sR9tYw": "Reset: https://shop.test/r?k={token}",
		"Hello echo-test 3f2a1b, thanks":                        "Hello {name}, thanks",
	} {
		if got := Normalize(in); got != want {
			t.Errorf("Normalize(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestVolatileValuesAreNotChanges(t *testing.T) {
	base := "Hello {who},\n\nOrder {order} placed on {date}.\nUse coupon {coupon} next time.\nTracking: {uuid}\n"
	fill := func(who, order, date, coupon, uuid string) string {
		return strings.NewReplacer("{who}", who, "{order}", order, "{date}", date, "{coupon}", coupon, "{uuid}", uuid).Replace(base)
	}
	b := fill("echo-test 3f2a1b", "#1042", "24 October 2026", "SAVE-X7K2", "1b4e28ba-2fa1-11d2-883f-0016d3cca427")
	for name, a := range map[string]string{
		"date":         fill("echo-test 3f2a1b", "#1042", "3 November 2026", "SAVE-X7K2", "1b4e28ba-2fa1-11d2-883f-0016d3cca427"),
		"order number": fill("echo-test 3f2a1b", "#7731", "24 October 2026", "SAVE-X7K2", "1b4e28ba-2fa1-11d2-883f-0016d3cca427"),
		"coupon":       fill("echo-test 3f2a1b", "#1042", "24 October 2026", "SAVE-PQ9Z", "1b4e28ba-2fa1-11d2-883f-0016d3cca427"),
		"uuid":         fill("echo-test 3f2a1b", "#1042", "24 October 2026", "SAVE-X7K2", "9f8e7d6c-5b4a-4321-8765-0123456789ab"),
		"test data":    fill("echo-test 77aa01", "#1042", "24 October 2026", "SAVE-X7K2", "1b4e28ba-2fa1-11d2-883f-0016d3cca427"),
	} {
		if cs := textChanges(email("a@shop.com", "Hi", b), email("a@shop.com", "Hi", a)); len(cs) > 0 {
			t.Errorf("%s alone is not a change: %q", name, titles(cs))
		}
	}
	// A real edit still is.
	a := strings.Replace(b, "Use coupon", "Save 10% with coupon", 1)
	if cs := textChanges(email("a@shop.com", "Hi", b), email("a@shop.com", "Hi", a)); len(cs) != 1 {
		t.Errorf("want the edited line as a change, got %q", titles(cs))
	}
}

func TestHTMLDiffUsesVisibleText(t *testing.T) {
	page := func(extra string) string {
		return `<html><head><title>x</title><style>p{color:red}</style></head><body><!-- id 1 --><p>Hi&nbsp;there,</p><p>Your order is on its way.</p>` +
			extra + `<table><tr><td>Total</td><td>$42.00</td></tr></table><script>track()</script></body></html>`
	}
	b := model.Message{Channel: model.Email, Status: model.Faked, To: []string{"a@shop.com"}, Subject: "Hi", HTML: page("")}
	a := b
	a.HTML = page(`<p>Leave us a review!</p>`)
	if got := visibleText(b.HTML); got != "Hi there,\nYour order is on its way.\nTotal $42.00" {
		t.Errorf("visibleText = %q", got)
	}
	cs := textChanges(b, a)
	if len(cs) != 1 {
		t.Fatalf("changes = %q", titles(cs))
	}
	if hs := ChangeDiff(cs[0]); len(hs) != 1 || hs[0].Summary() != "line 3 added: Leave us a review!" {
		t.Errorf("hunks = %+v", hs)
	}
}

func TestPayloadValueDiff(t *testing.T) {
	hook := func(body string) model.Message {
		return model.Message{Channel: model.Webhook, Method: "POST", URL: "https://crm.shop.test/hook", Status: model.Faked, Body: body}
	}
	b := hook(`{"event":"order.created","plan":"pro","id":"1b4e28ba-2fa1-11d2-883f-0016d3cca427","at":"2026-10-24T09:00:00Z","amount":12345678901234567890}`)
	a := hook(`{"amount":12345678901234567890,"at":"2026-10-25T10:00:00Z","event":"order.created","id":"9f8e7d6c-5b4a-4321-8765-0123456789ab","plan":"basic"}`)
	cs := textChanges(b, a)
	if len(cs) != 1 || cs[0].Check != "message/payload" {
		t.Fatalf("changes = %q", titles(cs))
	}
	hs := ChangeDiff(cs[0])
	if len(hs) != 1 || hs[0].Summary() != `line 6 changed: "plan": "pro" → "plan": "basic"` {
		t.Errorf("hunks = %+v", hs)
	}
	if !strings.Contains(cs[0].Before, "12345678901234567890") {
		t.Errorf("big numbers must stay exact: %s", cs[0].Before)
	}
	// Only volatile values moved: no change.
	c := hook(`{"event":"order.created","plan":"pro","id":"9f8e7d6c-5b4a-4321-8765-0123456789ab","at":"2026-11-01T00:00:00Z","amount":12345678901234567890}`)
	if cs := textChanges(b, c); len(cs) != 0 {
		t.Errorf("changes = %q", titles(cs))
	}
	// A lost field is reported as such, not a second time as a value diff.
	d := hook(`{"event":"order.created","id":"x","at":"2026-10-24T09:00:00Z","amount":1}`)
	for _, ch := range Messages([]model.Message{b}, []model.Message{d}) {
		if ch.Check == "message/payload" {
			t.Error("fields-lost already covers this payload")
		}
	}
}

func TestSameTextEditGroupsAcrossActions(t *testing.T) {
	act := func(id, tag string, broken bool) model.Action {
		return model.Action{ID: id, Label: id, Messages: []model.Message{
			email("echo-test+"+tag+"@example.com", "Your order", s11("echo-test "+tag, "W-"+tag[:4], broken))}}
	}
	c := Compare(run(act("a", "1111aa", false), act("b", "2222bb", false)), run(act("a", "3333cc", true), act("b", "4444dd", true)), "", "", "test")
	var n int
	for _, ch := range c.Changes {
		if ch.Check == "message/text" {
			n++
			if len(ch.Actions) != 2 {
				t.Errorf("actions = %q", ch.Actions)
			}
		}
	}
	if n != 1 {
		t.Errorf("want one grouped text change, got %d:\n%s", n, titles(c.Changes))
	}
}

// TestMyersIsMinimal checks the edit script against a dynamic-programming
// LCS on random inputs: it must turn a into b with the fewest edits.
func TestMyersIsMinimal(t *testing.T) {
	r := rand.New(rand.NewPCG(1, 2))
	mk := func(n int) []line {
		out := make([]line, n)
		for i := range out {
			k := string(rune('a' + r.IntN(4)))
			out[i] = line{n: i + 1, text: k, key: k}
		}
		return out
	}
	for range 300 {
		a, b := mk(r.IntN(14)), mk(r.IntN(14))
		ops := script(a, b)
		var got []string
		i, j, edits := 0, 0, 0
		for _, op := range ops {
			switch op {
			case '=':
				if a[i].key != b[j].key {
					t.Fatalf("kept a line that differs")
				}
				got = append(got, a[i].key)
				i++
				j++
			case '-':
				i++
				edits++
			case '+':
				got = append(got, b[j].key)
				j++
				edits++
			}
		}
		if i != len(a) || j != len(b) || strings.Join(got, "") != keysOf(b) {
			t.Fatalf("script %s does not turn %s into %s", ops, keysOf(a), keysOf(b))
		}
		if want := len(a) + len(b) - 2*lcs(a, b); edits != want {
			t.Fatalf("%d edits for %s → %s, want %d", edits, keysOf(a), keysOf(b), want)
		}
	}
}

func keysOf(ls []line) string {
	var b strings.Builder
	for _, l := range ls {
		b.WriteString(l.key)
	}
	return b.String()
}

func lcs(a, b []line) int {
	dp := make([][]int, len(a)+1)
	for i := range dp {
		dp[i] = make([]int, len(b)+1)
	}
	for i := len(a) - 1; i >= 0; i-- {
		for j := len(b) - 1; j >= 0; j-- {
			if a[i].key == b[j].key {
				dp[i][j] = dp[i+1][j+1] + 1
			} else {
				dp[i][j] = max(dp[i+1][j], dp[i][j+1])
			}
		}
	}
	return dp[0][0]
}

func TestHugeRewriteFallsBack(t *testing.T) {
	var a, b []string
	for i := range 3000 {
		a = append(a, "old line "+strings.Repeat("x", i%7)+string(rune('a'+i%26)))
		b = append(b, "new text "+string(rune('a'+i%26))+strings.Repeat("y", i%5))
	}
	hs := Diff(strings.Join(a, "\n"), strings.Join(b, "\n"))
	if len(hs) != 1 || !strings.HasPrefix(hs[0].Summary(), "lines 1-3000 changed") {
		t.Errorf("want one whole-text hunk, got %d", len(hs))
	}
}
