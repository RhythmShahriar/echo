// Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
// SPDX-License-Identifier: MIT

package report

import (
	"bytes"
	"encoding/xml"
	"strings"
	"testing"

	"github.com/RhythmShahriar/echo/model"
)

func TestParseFailOn(t *testing.T) {
	for _, tc := range []struct {
		in       string
		compared bool
		want     Threshold
		err      string // part of the error, "" for none
	}{
		{"", true, "broke", ""},
		{"", false, "error", ""},
		{"none", true, "none", ""},
		{"NONE", false, "none", ""},
		{"Broke", true, "broke", ""},
		{" new ", true, "new", ""},
		{"CHANGED", true, "changed", ""},
		{"Error", false, "error", ""},
		{"warning", false, "warning", ""},
		{"NoTiCe", false, "notice", ""},
		{"error", true, "", "--fail-on error is for runs; when comparing use broke, new, changed or none"},
		{"broke", false, "", "--fail-on broke is for comparisons; for a run use error, warning, notice or none"},
		{"fixed", true, "", `--fail-on "fixed": want none, broke, new or changed when comparing, or none, error, warning or notice for a run`},
		{"high", false, "", `--fail-on "high": want none`},
	} {
		got, err := ParseFailOn(tc.in, tc.compared)
		if tc.err == "" && (err != nil || got != tc.want) {
			t.Errorf("ParseFailOn(%q, %v) = %q, %v; want %q", tc.in, tc.compared, got, err, tc.want)
		}
		if tc.err != "" && (err == nil || !strings.Contains(err.Error(), tc.err)) {
			t.Errorf("ParseFailOn(%q, %v) error = %v; want %q", tc.in, tc.compared, err, tc.err)
		}
		if (ValidFailOn(tc.in, tc.compared) == nil) != (tc.err == "") {
			t.Errorf("ValidFailOn(%q, %v) disagrees with ParseFailOn", tc.in, tc.compared)
		}
	}
	if DefaultFailOn(true) != "broke" || DefaultFailOn(false) != "error" {
		t.Error("DefaultFailOn")
	}
}

func TestNoneNeverFails(t *testing.T) {
	down := sampleRun() // one failed action, an error issue
	for _, r := range []Result{{Run: down}, {Run: down, Comparison: sampleComparison()}, {Run: comparedRun(), Comparison: sampleComparison()}} {
		if FailOn(r, FailNone) || FailOn(r, "None") {
			t.Errorf("none failed a build (compared %v)", r.Compared())
		}
		var buf bytes.Buffer
		if err := JUnit(&buf, r, FailNone); err != nil {
			t.Fatal(err)
		}
		var s junitSuites
		if err := xml.Unmarshal(buf.Bytes(), &s); err != nil || s.Failures != 0 {
			t.Errorf("JUnit none: %d failures (compared %v), %v", s.Failures, r.Compared(), err)
		}
	}
}

func TestFailOnTakesTheParsedThreshold(t *testing.T) {
	c := Result{Run: comparedRun(), Comparison: sampleComparison()}
	for in, want := range map[string]bool{"broke": true, "BROKE": true, "changed": true} {
		th, err := ParseFailOn(in, true)
		if err != nil || FailOn(c, th) != want {
			t.Errorf("FailOn(%q) = %v, %v", in, FailOn(c, th), err)
		}
	}
	// Only a CHANGED change: the default (broke) passes, changed fails.
	c.Comparison.Changes = []model.Change{{Class: model.Changed, Check: "message/subject", Title: "Subject changed"}}
	if FailOn(c, DefaultFailOn(true)) || !FailOn(c, Threshold("changed")) {
		t.Error("default broke must pass a CHANGED-only comparison")
	}
	// A level for a comparison or a class for a run fails nothing of its own.
	if FailOn(c, "error") {
		t.Error("a level means nothing to a comparison")
	}
	clean := comparedRun()
	if FailOn(Result{Run: clean}, "broke") || !FailOn(Result{Run: clean}, "notice") {
		t.Error("run thresholds")
	}
}
