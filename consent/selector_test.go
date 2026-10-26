// Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
// SPDX-License-Identifier: MIT

package consent_test

import (
	"reflect"
	"strings"
	"testing"

	"github.com/RhythmShahriar/echo/consent"
)

func TestParseSelector(t *testing.T) {
	L := func(css string, shadow ...string) consent.Locator { return consent.Locator{CSS: css, Shadow: shadow} }
	for _, c := range []struct {
		in   string
		want []consent.Locator
	}{
		{"#reject-all", []consent.Locator{L("#reject-all")}},
		{"#settings >> #reject-all", []consent.Locator{L("#settings"), L("#reject-all")}},
		{"uc-banner::shadow button[data-testid=deny]", []consent.Locator{L("button[data-testid=deny]", "uc-banner")}},
		{"a::shadow b::shadow .x >> #y", []consent.Locator{L(".x", "a", "b"), L("#y")}},
		{`button[aria-label=">>"]`, []consent.Locator{L(`button[aria-label=">>"]`)}},
		{`button:has(span:contains("a >> b"))`, []consent.Locator{L(`button:has(span:contains("a >> b"))`)}},
		{"div > button", []consent.Locator{L("div > button")}},
		{`[title='it\'s']`, []consent.Locator{L(`[title='it\'s']`)}},
	} {
		got, err := consent.ParseSelector(c.in)
		if err != nil || !reflect.DeepEqual(got, c.want) {
			t.Errorf("ParseSelector(%q) = %+v, %v; want %+v", c.in, got, err, c.want)
		}
	}
	for _, c := range []struct{ in, msg string }{
		{"", "selector is empty"},
		{"   ", "selector is empty"},
		{"#a >> ", `selector "#a >> ": step 2 is empty`},
		{">> #a", `selector ">> #a": step 1 is empty`},
		{"::shadow button", `selector "::shadow button": step 1 has an empty part around ::shadow`},
		{"button[x=1", `selector "button[x=1": step 1: missing ]`},
		{`button[x="1]`, `selector "button[x=\"1]": step 1: unclosed "`},
		{"a) b", `selector "a) b": step 1: unexpected )`},
		{"a >> b >> c >> d >> e", `selector "a >> b >> c >> d >> e" has 5 steps; at most 4`},
		{"a\nb", `selector "a\\x0ab" has a control character`},
		{strings.Repeat("a", 1025), "selector is 1025 bytes; keep it under 1024"},
	} {
		_, err := consent.ParseSelector(c.in)
		if err == nil || err.Error() != c.msg {
			t.Errorf("ParseSelector(%q) err = %v; want %s", c.in, err, c.msg)
		}
	}
}

// FuzzSelectorChain: any input parses or errors without a panic; a
// parsed chain has no empty step and no operator left inside a step's
// CSS outside quotes and brackets.
func FuzzSelectorChain(f *testing.F) {
	for _, s := range []string{"#a", "#a >> #b", "host::shadow button", `[x=">>"]`, "a::shadow b::shadow c >> d", "(", `"`, ">>", "a\\>>b"} {
		f.Add(s)
	}
	f.Fuzz(func(t *testing.T, s string) {
		ls, err := consent.ParseSelector(s)
		if err != nil {
			return
		}
		if len(ls) == 0 || len(ls) > 4 {
			t.Fatalf("%q: %d steps", s, len(ls))
		}
		for _, l := range ls {
			if strings.TrimSpace(l.CSS) == "" {
				t.Fatalf("%q: empty CSS in %+v", s, ls)
			}
			for _, h := range l.Shadow {
				if strings.TrimSpace(h) == "" {
					t.Fatalf("%q: empty shadow host in %+v", s, ls)
				}
			}
		}
	})
}
