// Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
// SPDX-License-Identifier: MIT

package compare

import (
	"fmt"
	"math"
	"strings"
	"testing"
	"time"

	"github.com/RhythmShahriar/echo/model"
)

// bigPayloads are two webhooks with n fields each, a tenth of them renamed,
// so the field diff has work on both sides.
func bigPayloads(n int) (b, a model.Message) {
	var bf, af []string
	for i := range n {
		bf = append(bf, fmt.Sprintf(`"field_%05d":%d`, i, i))
		name := fmt.Sprintf("field_%05d", i)
		if i%10 == 0 {
			name = fmt.Sprintf("renamed_%05d", i)
		}
		af = append(af, fmt.Sprintf(`%q:%d`, name, i))
	}
	hook := func(body string) model.Message {
		return model.Message{Channel: model.Webhook, Method: "POST", URL: "https://crm.shop.test/api/leads", Status: model.Faked, Body: body}
	}
	return hook("{" + strings.Join(bf, ",") + "}"), hook("{" + strings.Join(af, ",") + "}")
}

func BenchmarkFieldDiff10k(b *testing.B) {
	before, after := bigPayloads(10000)
	for b.Loop() {
		content(before, after, covered{})
	}
}

// TestFieldDiffIsLinear: the field diff was quadratic (280 ms for these
// 10,000 keys, 11.8 s for a real 10,000-key payload). It now takes about
// 20 ms; the bound leaves room for a slow CI machine.
func TestFieldDiffIsLinear(t *testing.T) {
	before, after := bigPayloads(10000)
	// The best of three: one slow round on a busy machine (other suites
	// running beside this one) isn't a quadratic diff.
	var cs []model.Change
	took := time.Duration(math.MaxInt64)
	for range 3 {
		start := time.Now()
		cs, _ = content(before, after, covered{})
		took = min(took, time.Since(start))
	}
	if took > 150*time.Millisecond && !raceEnabled {
		t.Errorf("field diff of 10,000 keys took %s at best, want well under 50 ms", took)
	}
	got := titles(cs)
	if !strings.Contains(got, "payload lost fields") || !strings.Contains(got, "payload has new fields") {
		t.Errorf("changes:\n%s", got)
	}
	for _, c := range cs {
		if c.Check == "message/fields-lost" && !strings.HasPrefix(c.Detail, "field_00000, field_00010, ") {
			t.Errorf("lost fields = %.80s", c.Detail)
		}
	}
}

func TestTruncatedPayloadsAreNotFieldsLost(t *testing.T) {
	full, _ := bigPayloads(50)
	cut := full
	cut.Body = full.Body[:len(full.Body)/2] + "\n…(truncated)"
	for _, p := range [][2]model.Message{{full, cut}, {cut, full}, {cut, cut}} {
		cs, note := content(p[0], p[1], covered{})
		for _, c := range cs {
			if strings.HasPrefix(c.Check, "message/fields-") {
				t.Errorf("a cut payload read as a field change: %s", c.Title)
			}
		}
		if note != "Webhook to crm.shop.test: payload too large to compare fields (cut at 256 KiB)" {
			t.Errorf("note = %q", note)
		}
	}
	// A whole payload on both sides is still compared, with no note.
	_, lost := bigPayloads(50)
	if cs, note := content(full, lost, covered{}); note != "" || !strings.Contains(titles(cs), "payload lost fields") {
		t.Errorf("full payloads: note %q, changes:\n%s", note, titles(cs))
	}
	// Through Compare the note is a warning of the comparison, once.
	act := func(m ...model.Message) model.Action { return model.Action{ID: "a", Label: "Order", Messages: m} }
	c := Compare(run(act(full, full)), run(act(cut, cut)), "", "", "test")
	if len(c.Changes) != 0 || len(c.Warnings) != 1 || !strings.Contains(c.Warnings[0], "payload too large to compare fields") {
		t.Errorf("changes:\n%s\nwarnings: %q", titles(c.Changes), c.Warnings)
	}
}

func TestDiffSets(t *testing.T) {
	for _, tc := range []struct{ a, b, lost, added string }{
		{"a,b,c", "a,b,c", "", ""},
		{"a,b,c", "b", "a,c", ""},
		{"b", "a,b,c", "", "a,c"},
		{"", "x,y", "", "x,y"},
		{"x,y", "", "x,y", ""},
		{"a,c,e", "b,c,d,f", "a,e", "b,d,f"},
	} {
		split := func(s string) []string {
			if s == "" {
				return nil
			}
			return strings.Split(s, ",")
		}
		lost, added := diffSets(split(tc.a), split(tc.b))
		if strings.Join(lost, ",") != tc.lost || strings.Join(added, ",") != tc.added {
			t.Errorf("diffSets(%s; %s) = %v, %v", tc.a, tc.b, lost, added)
		}
	}
}

// TestPairingKeysOnce: a message's pair key runs several regular
// expressions over its subject. It used to be worked out for every pair of
// messages, so 2,000 emails to one address took seconds; now it's once
// per message.
func TestPairingKeysOnce(t *testing.T) {
	calls := 0
	keyOf = func(m model.Message) string { calls++; return pairKey(m) }
	t.Cleanup(func() { keyOf = pairKey })
	var b, a []model.Message
	for i := range 2000 {
		m := email("admin@shop.com", fmt.Sprintf("Lead from form %c%c", 'a'+i%26, 'a'+i/26%26), "x")
		b, a = append(b, m), append(a, m)
	}
	a = append(a, email("admin@shop.com", "Something else", "x"))
	cs := Messages(b, a)
	if got := titles(cs); got != `new: Email to admin@shop.com ("Something else") is now sent` {
		t.Errorf("changes:\n%s", got)
	}
	if calls != len(b)+len(a) {
		t.Errorf("pair key worked out %d times for %d messages", calls, len(b)+len(a))
	}
}
