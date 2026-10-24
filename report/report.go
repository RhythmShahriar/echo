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
// was never checked.
func FailOn(r Result, threshold string) bool {
	if threshold == "" || threshold == "none" {
		return false
	}
	if failed, _ := failedActions(r.Run); failed > 0 {
		return true
	}
	if r.Compared() {
		limit := slices.Index(model.Classes, threshold)
		for _, c := range r.Comparison.Changes {
			if i := slices.Index(model.Classes, c.Class); i >= 0 && i <= limit && c.Class != model.Fixed {
				return true
			}
		}
		return false
	}
	limit := model.Level(threshold).Rank()
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

// ValidFailOn checks a --fail-on value.
func ValidFailOn(threshold string, compared bool) error {
	if compared {
		if !slices.Contains(model.Classes[:3], threshold) {
			return fmt.Errorf("--fail-on %q: want broke, new or changed", threshold)
		}
		return nil
	}
	if !slices.Contains(Levels, model.Level(threshold)) {
		return fmt.Errorf("--fail-on %q: want error, warning or notice (or broke, new, changed when comparing)", threshold)
	}
	return nil
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

func beforeAfter(c model.Change) string {
	if c.Before == "" && c.After == "" {
		return ""
	}
	return cmp.Or(c.Before, "–") + " → " + cmp.Or(c.After, "–")
}

func firstLine(s string) string {
	s, _, _ = strings.Cut(strings.TrimSpace(s), "\n")
	return s
}

func capitalize(s string) string {
	if s == "" {
		return s
	}
	return strings.ToUpper(s[:1]) + s[1:]
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
