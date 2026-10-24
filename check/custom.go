// Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
// SPDX-License-Identifier: MIT

package check

import (
	"cmp"
	"fmt"
	"io"
	"regexp"
	"strings"
	"unicode/utf8"

	"github.com/RhythmShahriar/echo/model"
)

// Custom is a rule a project adds in its config (`rules:`): text that must
// not appear in what it sends, such as an old phone number or a
// competitor's name. It looks at the subject, the visible text and the
// body of each message on its channels.
type Custom struct {
	ID       string          // "brand/old-phone": no spaces, not a built-in check
	Channels []model.Channel // none means all
	Match    string          // a literal, case-sensitive; or
	Regex    string          // a Go regular expression ((?i) ignores case)
	Level    model.Level     // notice, warning or error; "" is warning
	Message  string          // what the issue says was found; "" is "Matched <id>"
	Fixtures Fixtures        // examples `rhythm echo checks --test` checks the rule against
}

// Fixtures are texts a custom rule must match and must not match.
type Fixtures struct {
	Match   []string
	NoMatch []string
}

// matcher returns where the rule's pattern is first found in s, or -1.
func (c Custom) matcher() (func(s string) (start, end int), error) {
	switch {
	case (c.Match == "") == (c.Regex == ""):
		return nil, fmt.Errorf("rule %q: set exactly one of match or regex", c.ID)
	case c.Match != "":
		lit := c.Match
		return func(s string) (int, int) {
			i := strings.Index(s, lit)
			if i < 0 {
				return -1, -1
			}
			return i, i + len(lit)
		}, nil
	}
	re, err := regexp.Compile(c.Regex)
	if err != nil {
		return nil, fmt.Errorf("rule %q: regex: %w", c.ID, err)
	}
	return func(s string) (int, int) {
		loc := re.FindStringIndex(s)
		if loc == nil || loc[0] == loc[1] { // an empty match finds nothing
			return -1, -1
		}
		return loc[0], loc[1]
	}, nil
}

// Rule compiles c into a rule for Options.Rules.
func (c Custom) Rule() (Rule, error) {
	if c.ID == "" || strings.ContainsAny(c.ID, " \t\n") {
		return Rule{}, fmt.Errorf("rule %q: needs an id without spaces, like brand/old-phone", c.ID)
	}
	if _, builtin := Lookup(c.ID); builtin {
		return Rule{}, fmt.Errorf("rule %q: that is a built-in check; give the rule its own id", c.ID)
	}
	level := cmp.Or(c.Level, model.Warning)
	if level != model.Error && level != model.Warning && level != model.Notice {
		return Rule{}, fmt.Errorf("rule %q: level %q isn't notice, warning or error", c.ID, c.Level)
	}
	find, err := c.matcher()
	if err != nil {
		return Rule{}, err
	}
	say := cmp.Or(c.Message, "Matched "+c.ID)
	return Rule{ID: c.ID, Level: level, Channels: c.Channels, Doc: say + " (custom rule)",
		Run: func(m model.Message, _ Options) []model.Issue {
			// The body last: it is the raw MIME of an email, which holds
			// the subject and text again.
			for _, s := range []string{m.Subject, visibleText(m), m.Body} {
				if i, j := find(s); i >= 0 {
					return []model.Issue{{Title: fmt.Sprintf("%s in %s", say, Describe(m)),
						Detail: fmt.Sprintf("found %q", clip(s[i:j], 80)),
						Fix:    fmt.Sprintf("Change the message, or the rule %s in .echo/config.yaml if the text is fine now.", c.ID)}}
				}
			}
			return nil
		}}, nil
}

// CustomRules compiles the config's rules for Options.Rules, stopping at
// the first one that is wrong. Two rules with one ID are an error too.
func CustomRules(cs []Custom) ([]Rule, error) {
	out := make([]Rule, 0, len(cs))
	seen := map[string]bool{}
	for _, c := range cs {
		if seen[c.ID] {
			return nil, fmt.Errorf("rule %q is listed twice", c.ID)
		}
		seen[c.ID] = true
		r, err := c.Rule()
		if err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, nil
}

// FixtureResult is one fixture of a custom rule, run.
type FixtureResult struct {
	Rule string
	Text string // "" when the rule has no fixture of this kind
	Want bool   // true for a match fixture, false for a no_match one
	Got  bool
	Err  error // the rule doesn't compile
}

// OK reports whether the fixture came out as declared.
func (f FixtureResult) OK() bool { return f.Err == nil && f.Text != "" && f.Want == f.Got }

// String is the line `rhythm echo checks --test` prints.
func (f FixtureResult) String() string {
	kind := "no_match"
	if f.Want {
		kind = "match"
	}
	switch {
	case f.Err != nil:
		return fmt.Sprintf("FAIL  %s: %v", f.Rule, f.Err)
	case f.Text == "":
		return fmt.Sprintf("FAIL  %s: has no %s fixture; every rule needs one that matches and one that doesn't", f.Rule, kind)
	case f.OK() && f.Want:
		return fmt.Sprintf("ok    %s matches %q", f.Rule, clip(f.Text, 60))
	case f.OK():
		return fmt.Sprintf("ok    %s leaves %q alone", f.Rule, clip(f.Text, 60))
	case f.Want:
		return fmt.Sprintf("FAIL  %s should match %q but doesn't", f.Rule, clip(f.Text, 60))
	}
	return fmt.Sprintf("FAIL  %s shouldn't match %q but does", f.Rule, clip(f.Text, 60))
}

// TestFixtures runs every custom rule against its fixtures. A rule
// without a match or a no_match fixture fails, as a built-in check would.
func TestFixtures(cs []Custom) []FixtureResult {
	var out []FixtureResult
	for _, c := range cs {
		find, err := c.matcher()
		if err == nil {
			_, err = c.Rule()
		}
		if err != nil {
			out = append(out, FixtureResult{Rule: c.ID, Err: err})
			continue
		}
		for _, want := range []bool{true, false} {
			texts := c.Fixtures.NoMatch
			if want {
				texts = c.Fixtures.Match
			}
			if len(texts) == 0 {
				out = append(out, FixtureResult{Rule: c.ID, Want: want})
			}
			for _, t := range texts {
				i, _ := find(t)
				out = append(out, FixtureResult{Rule: c.ID, Text: t, Want: want, Got: i >= 0})
			}
		}
	}
	return out
}

// WriteFixtures prints TestFixtures' results and a summary line, and
// reports whether every fixture passed.
func WriteFixtures(w io.Writer, cs []Custom) (bool, error) {
	rs := TestFixtures(cs)
	failed, fixtures := 0, 0
	for _, r := range rs {
		if !r.OK() {
			failed++
		}
		if r.Text != "" {
			fixtures++
		}
		if _, err := fmt.Fprintln(w, r.String()); err != nil {
			return false, err
		}
	}
	rules := "rules"
	if len(cs) == 1 {
		rules = "rule"
	}
	var err error
	switch {
	case len(cs) == 0:
		_, err = fmt.Fprintln(w, "No custom rules in .echo/config.yaml (add them under rules:).")
	case failed == 0:
		_, err = fmt.Fprintf(w, "%d %s, %d fixtures, all pass.\n", len(cs), rules, fixtures)
	default:
		_, err = fmt.Fprintf(w, "%d %s, %d fixtures, %d failed.\n", len(cs), rules, fixtures, failed)
	}
	return failed == 0, err
}

// clip cuts s to n runes, on one line.
func clip(s string, n int) string {
	s = strings.Join(strings.Fields(s), " ")
	if utf8.RuneCountInString(s) <= n {
		return s
	}
	return string([]rune(s)[:n-1]) + "…"
}
