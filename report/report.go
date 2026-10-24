// Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
// SPDX-License-Identifier: MIT

// Package report renders Echo's results: a live line per captured
// message (`rhythm echo watch`), the terminal summary of a run or a
// comparison, plain text for logs and screen readers, JSON, Markdown,
// JUnit XML and a standalone HTML inbox with message previews.
package report

import (
	"cmp"
	"fmt"
	"slices"
	"strings"

	"github.com/RhythmShahriar/echo/compare"
	"github.com/RhythmShahriar/echo/model"
)

// Result is what a command produced: a run, and a comparison when it was
// compared with a snapshot.
type Result struct {
	Run        *model.Run
	Comparison *model.Comparison
	Dir        string
}

// Compared reports whether the result is a comparison.
func (r Result) Compared() bool { return r.Comparison != nil }

// Levels in report order.
var Levels = []model.Level{model.Error, model.Warning, model.Notice}

var classInfo = map[string]struct{ Label, Glyph, Blurb string }{
	model.Broke:   {"BROKE", "✗", "sent in the snapshot, missing or failing now"},
	model.New:     {"NEW", "▲", "messages or problems that weren't there before"},
	model.Changed: {"CHANGED", "■", "the same message, but different; check it's intended"},
	model.Fixed:   {"FIXED", "✓", "problems from the snapshot that are gone"},
}

var levelGlyph = map[model.Level]string{model.Error: "✗", model.Warning: "▲", model.Notice: "·"}

// channelGlyph is a one-cell marker per channel, for the live view.
var channelGlyph = map[model.Channel]string{
	model.Email: "✉", model.SMS: "◆", model.WhatsApp: "◆", model.Chat: "#", model.Webhook: "↗",
	model.Analytics: "∿", model.Payment: "$", model.Push: "◉", model.HTTP: "→",
}

// FailOn reports whether r should fail a build at threshold: a change
// class (broke, new, changed) for comparisons, an issue level for runs.
// An action that failed fails at every threshold but "none": what it sent
// was never checked. The threshold should come from ParseFailOn; a plain
// string is still accepted (in any case) for callers that pass the flag
// as typed. "" never fails.
func FailOn[T ~string](r Result, threshold T) bool {
	t := Threshold(strings.ToLower(strings.TrimSpace(string(threshold))))
	if t == "" || t == FailNone {
		return false
	}
	if failed, _ := failedActions(r.Run); failed > 0 {
		return true
	}
	if r.Compared() {
		limit := slices.Index(model.Classes, string(t))
		for _, c := range r.Comparison.Changes {
			if i := slices.Index(model.Classes, c.Class); i >= 0 && i <= limit && c.Class != model.Fixed {
				return true
			}
		}
		return false
	}
	limit := t.levelRank()
	for _, i := range Issues(r.Run) {
		if i.Level.Rank() <= limit {
			return true
		}
	}
	return false
}

// AllFailed reports whether every action of the run (the compared run, for
// a comparison) failed, so there is no result at all. A run with no
// actions (check, headers) hasn't failed.
func AllFailed(r Result) bool {
	failed, total := failedActions(r.Run)
	return total > 0 && failed == total
}

// failedActions counts the actions that failed.
func failedActions(run *model.Run) (failed, total int) {
	if run == nil {
		return 0, 0
	}
	for _, a := range run.Actions {
		if a.Error != "" {
			failed++
		}
	}
	return failed, len(run.Actions)
}

// Threshold is a parsed --fail-on value: "none", a change class (broke,
// new, changed) for comparisons or an issue level (error, warning,
// notice) for runs. ParseFailOn makes one.
type Threshold string

// FailNone never fails a build, whatever was found.
const FailNone Threshold = "none"

// levelRank is the issue rank at or above which a run fails: -1, so
// nothing fails, for none or a value that isn't a level.
func (t Threshold) levelRank() int {
	if !slices.Contains(Levels, model.Level(t)) {
		return -1
	}
	return model.Level(t).Rank()
}

// DefaultFailOn is the threshold when --fail-on isn't given, the same as
// the GitHub Action's: broke for a comparison, error for a run.
func DefaultFailOn(compared bool) Threshold {
	if compared {
		return Threshold(model.Broke)
	}
	return Threshold(model.Error)
}

// ParseFailOn reads a --fail-on value in any case. "" gives the default.
// A class only fits a comparison and a level only fits a run. The error
// for either mistake or an unknown word lists what fits.
func ParseFailOn(s string, compared bool) (Threshold, error) {
	t := strings.ToLower(strings.TrimSpace(s))
	classes := []string{model.Broke, model.New, model.Changed}
	levels := []string{string(model.Error), string(model.Warning), string(model.Notice)}
	switch {
	case t == "":
		return DefaultFailOn(compared), nil
	case t == string(FailNone):
		return FailNone, nil
	case compared && slices.Contains(classes, t), !compared && slices.Contains(levels, t):
		return Threshold(t), nil
	case compared && slices.Contains(levels, t):
		return "", fmt.Errorf("--fail-on %s is for runs; when comparing use broke, new, changed or none", t)
	case !compared && slices.Contains(classes, t):
		return "", fmt.Errorf("--fail-on %s is for comparisons; for a run use error, warning, notice or none", t)
	}
	return "", fmt.Errorf("--fail-on %q: want none, broke, new or changed when comparing, or none, error, warning or notice for a run", s)
}

// ValidFailOn checks a --fail-on value. It is ParseFailOn without the
// result, kept for callers that only validate; unlike before, "none" and
// any case are accepted.
func ValidFailOn(threshold string, compared bool) error {
	_, err := ParseFailOn(threshold, compared)
	return err
}

// Issues returns a run's issues with the same problem in several actions
// merged, most serious first.
func Issues(run *model.Run) []model.Issue {
	var out []model.Issue
	at := map[string]int{}
	add := func(i model.Issue, action string) {
		if n, ok := at[i.Key]; ok {
			if !slices.Contains(out[n].Actions, action) {
				out[n].Actions = append(out[n].Actions, action)
			}
			return
		}
		at[i.Key] = len(out)
		i.Actions = []string{action}
		out = append(out, i)
	}
	for _, a := range run.Actions {
		for _, i := range a.Issues {
			add(i, a.Label)
		}
	}
	for _, i := range run.Issues {
		add(i, "site-wide")
	}
	slices.SortStableFunc(out, func(a, b model.Issue) int {
		return cmp.Or(cmp.Compare(a.Level.Rank(), b.Level.Rank()), cmp.Compare(len(b.Actions), len(a.Actions)))
	})
	return out
}

// Counts tallies issues by level.
func Counts(is []model.Issue) map[model.Level]int {
	m := map[model.Level]int{}
	for _, i := range is {
		m[i.Level]++
	}
	return m
}

// Tally counts a run's messages by channel, in report order: "3 emails,
// 1 SMS, 2 webhooks".
func Tally(run *model.Run) string {
	n := map[model.Channel]int{}
	total := 0
	for _, a := range run.Actions {
		for _, m := range a.Messages {
			n[m.Channel]++
			total++
		}
	}
	if total == 0 {
		return "no messages"
	}
	var parts []string
	for _, c := range model.Channels {
		if n[c] > 0 {
			parts = append(parts, ChannelCount(c, n[c]))
		}
	}
	return strings.Join(parts, ", ")
}

// ChannelCount is "1 email", "3 emails", "2 SMS".
func ChannelCount(c model.Channel, n int) string {
	nouns := map[model.Channel][2]string{
		model.Email: {"email", "emails"}, model.SMS: {"SMS", "SMS"}, model.WhatsApp: {"WhatsApp message", "WhatsApp messages"},
		model.Chat: {"chat message", "chat messages"}, model.Webhook: {"webhook", "webhooks"}, model.Analytics: {"analytics event", "analytics events"},
		model.Payment: {"payment call", "payment calls"}, model.Push: {"push notification", "push notifications"}, model.HTTP: {"request", "requests"},
	}
	w := nouns[c]
	if n == 1 {
		return "1 " + w[0]
	}
	return fmt.Sprintf("%d %s", n, w[1])
}

func plural(n int, one, many string) string {
	if n == 1 {
		return "1 " + one
	}
	return fmt.Sprintf("%d %s", n, many)
}

func actionsLabel(actions []string, show int) string {
	switch len(actions) {
	case 0:
		return ""
	case 1:
		return actions[0]
	}
	if len(actions) <= show {
		return strings.Join(actions, ", ")
	}
	return fmt.Sprintf("%s +%d more", strings.Join(actions[:show], ", "), len(actions)-show)
}

func countLabel(actions []string) string {
	if len(actions) == 1 {
		return actions[0]
	}
	return plural(len(actions), "action", "actions")
}

// beforeAfter is a change's values in one line: "1 time → 2 times", or
// for a text or payload change what its first hunk changed ("line 9
// added: Order total: $42.00"), since the full texts don't fit a line.
func beforeAfter(c model.Change) string {
	if hs := compare.ChangeDiff(c); len(hs) > 0 {
		s := hs[0].Summary()
		if len(hs) > 1 {
			s += fmt.Sprintf(" (+%d more)", len(hs)-1)
		}
		return s
	}
	if c.Before == "" && c.After == "" {
		return ""
	}
	return cmp.Or(c.Before, "–") + " → " + cmp.Or(c.After, "–")
}

// diffMarks are the line prefixes of a diff, as in a unified diff.
var diffMarks = map[string]string{compare.OpAdd: "+ ", compare.OpDel: "- ", compare.OpCtx: "  "}

// hunkText is a hunk as plain lines: a "+ ", "- " or "  " mark, then the
// line made Safe.
func hunkText(h compare.Hunk) []string {
	out := make([]string, 0, len(h))
	for _, l := range h {
		out = append(out, diffMarks[l.Op]+Safe(l.Text))
	}
	return out
}

// attachmentList is the files an email carries, made safe to print:
// "invoice-42.pdf (application/pdf, 84 KB), logo.png (image/png, 2 KB)".
func attachmentList(m model.Message) string {
	var out []string
	for _, a := range compare.Attachments(m) {
		out = append(out, Safe(a.String()))
	}
	return strings.Join(out, ", ")
}

func firstLine(s string) string {
	s, _, _ = strings.Cut(strings.TrimSpace(s), "\n")
	return s
}

// verdict answers "is my site still saying what it should?". When
// actions failed in the compared run it can't be answered.
func verdict(r Result) string {
	c := r.Comparison
	if failed, total := failedActions(r.Run); failed > 0 {
		return fmt.Sprintf("Could not compare: %d of %d actions failed in this run", failed, total)
	}
	switch {
	case c.Count(model.Broke) > 0:
		return "Something broke"
	case c.Count(model.New) > 0:
		return "New messages or problems"
	case c.Count(model.Changed) > 0:
		return "Nothing lost; some messages changed"
	}
	return "Everything is still sent the same way"
}

func runVerdict(r *model.Run, counts map[model.Level]int) string {
	prefix := ""
	if r.Name != "" {
		prefix = fmt.Sprintf("Snapshot %q saved; ", r.Name)
	}
	failed := 0
	for _, a := range r.Actions {
		if a.Error != "" {
			failed++
		}
	}
	switch {
	case failed > 0:
		return prefix + plural(failed, "action failed", "actions failed")
	case counts[model.Error] > 0 && r.Mode == "check":
		return prefix + "delivery has errors"
	case counts[model.Error] > 0:
		return prefix + "messages have errors"
	case counts[model.Warning] > 0:
		return prefix + "a few warnings"
	}
	return prefix + "all clear"
}
