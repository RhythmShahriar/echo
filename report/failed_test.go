// Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
// SPDX-License-Identifier: MIT

package report

import (
	"bytes"
	"strings"
	"testing"

	"github.com/RhythmShahriar/echo/compare"
	"github.com/RhythmShahriar/echo/model"
)

func downRun(labels ...string) *model.Run {
	r := &model.Run{Schema: model.Schema, Mode: "seal"}
	for _, l := range labels {
		r.Actions = append(r.Actions, model.Action{ID: l, Label: l, Error: "connection refused"})
	}
	return r
}

func TestDownAtBothEnds(t *testing.T) {
	before, after := downRun("Contact form"), downRun("Contact form")
	before.Name = "before"
	r := Result{Run: after, Comparison: compare.Compare(before, after, "", "", "test")}
	for _, th := range []string{"broke", "new", "changed"} {
		if !FailOn(r, th) {
			t.Errorf("FailOn(%q) = false for a site that's down at both ends", th)
		}
	}
	if FailOn(r, "none") || FailOn(r, "") {
		t.Error("none (and no threshold) never fails")
	}
	if !AllFailed(r) {
		t.Error("AllFailed: every action failed")
	}
	var buf bytes.Buffer
	NewPretty(&buf, &buf, PrettyOptions{Plain: true}).Summary(r, "")
	if out := buf.String(); !strings.Contains(out, "Could not compare: 1 of 1 actions failed in this run - 1 broke") ||
		strings.Contains(out, "Everything is still sent the same way") {
		t.Errorf("summary:\n%s", out)
	}
	buf.Reset()
	if err := Markdown(&buf, r); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(buf.String(), "**Could not compare: 1 of 1 actions failed in this run.**") {
		t.Errorf("markdown:\n%s", buf.String())
	}
}

func TestSomeActionsFailed(t *testing.T) {
	after := downRun("Contact form")
	after.Actions = append(after.Actions, model.Action{ID: "order", Label: "Order"})
	before := &model.Run{Schema: model.Schema, Mode: "seal", Actions: []model.Action{{ID: "Contact form", Label: "Contact form"}, {ID: "order", Label: "Order"}}}
	r := Result{Run: after, Comparison: compare.Compare(before, after, "", "", "test")}
	if AllFailed(r) {
		t.Error("one of two failed is not all")
	}
	if !FailOn(r, "changed") || verdict(r) != "Could not compare: 1 of 2 actions failed in this run" {
		t.Errorf("FailOn = %v, verdict = %q", FailOn(r, "changed"), verdict(r))
	}
	// The summary doesn't then say the actions were compared.
	var buf bytes.Buffer
	NewPretty(&buf, &buf, PrettyOptions{Plain: true}).Summary(r, "")
	if out := buf.String(); !strings.Contains(out, "failed in this run - 1 broke, 0 new, 0 changed, 0 fixed - 2 actions run, ") || strings.Contains(out, "actions compared") {
		t.Errorf("summary:\n%s", out)
	}
	buf.Reset()
	if err := Markdown(&buf, r); err != nil || !strings.Contains(buf.String(), "2 actions run against the earlier run") {
		t.Errorf("markdown: %v\n%s", err, buf.String())
	}
}

func TestFixedFailureDoesNotFail(t *testing.T) {
	before := downRun("Contact form")
	after := &model.Run{Schema: model.Schema, Mode: "seal", Actions: []model.Action{{ID: "Contact form", Label: "Contact form"}}}
	r := Result{Run: after, Comparison: compare.Compare(before, after, "", "", "test")}
	if FailOn(r, "changed") || AllFailed(r) {
		t.Error("a failure fixed in the compared run fails nothing")
	}
	if r.Comparison.Count(model.Fixed) != 1 || verdict(r) != "Everything is still sent the same way" {
		t.Errorf("fixed = %d, verdict = %q", r.Comparison.Count(model.Fixed), verdict(r))
	}
}

func TestAllFailedRuns(t *testing.T) {
	if AllFailed(Result{Run: &model.Run{Mode: "check"}}) {
		t.Error("a run without actions hasn't failed")
	}
	if !AllFailed(Result{Run: downRun("a", "b")}) || AllFailed(Result{Run: comparedRun()}) {
		t.Error("AllFailed for runs")
	}
	if !FailOn(Result{Run: downRun("a")}, "notice") || FailOn(Result{Run: downRun("a")}, "none") {
		t.Error("a failed action fails a run at every level but none")
	}
}
