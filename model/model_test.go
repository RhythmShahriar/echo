// Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
// SPDX-License-Identifier: MIT

package model

import (
	"bytes"
	"encoding/json"
	"os"
	"strings"
	"testing"
	"time"
)

func TestNewIDIsShortAndStable(t *testing.T) {
	a, b := NewID("seed"), NewID("seed")
	if a != b || len(a) != 12 {
		t.Errorf("NewID(seed) = %q, %q: want the same 12 hex characters twice", a, b)
	}
	if NewID("other") == a {
		t.Error("different seeds gave the same ID")
	}
}

func TestRankOrdersMostSeriousFirst(t *testing.T) {
	if Error.Rank() >= Warning.Rank() || Warning.Rank() >= Notice.Rank() {
		t.Errorf("ranks: error %d, warning %d, notice %d", Error.Rank(), Warning.Rank(), Notice.Rank())
	}
	if Level("unknown").Rank() != Notice.Rank() {
		t.Error("an unknown level should rank as a notice")
	}
}

func TestIdentifyKeysOnCheckAndTitle(t *testing.T) {
	i := Issue{Check: "content/merge-tag", Title: "Unfilled {first_name}", Detail: "varies per run"}
	i.Identify()
	if i.Key != "content/merge-tag|Unfilled {first_name}" {
		t.Errorf("key = %q", i.Key)
	}
}

func TestRunActionFindsByID(t *testing.T) {
	r := Run{Actions: []Action{{ID: "a"}, {ID: "b", Label: "B"}}}
	if a := r.Action("b"); a == nil || a.Label != "B" {
		t.Errorf("Action(b) = %+v", a)
	}
	// It points into the run, so callers can fill the action in place.
	r.Action("a").Label = "A"
	if r.Actions[0].Label != "A" {
		t.Error("Action should return a pointer into Actions")
	}
	if r.Action("missing") != nil {
		t.Error("Action(missing) should be nil")
	}
}

func TestRunFailedCountsErroredActions(t *testing.T) {
	r := Run{Actions: []Action{{ID: "a"}, {ID: "b", Error: "connection refused"}, {ID: "c", Error: "exit status 7"}}}
	if f, n := r.Failed(); f != 2 || n != 3 {
		t.Errorf("Failed() = %d, %d; want 2, 3", f, n)
	}
	if f, n := (&Run{}).Failed(); f != 0 || n != 0 {
		t.Errorf("empty run: Failed() = %d, %d", f, n)
	}
}

func TestComparisonCount(t *testing.T) {
	c := Comparison{Changes: []Change{{Class: Broke}, {Class: New}, {Class: Broke}, {Class: Fixed}}}
	for class, want := range map[string]int{Broke: 2, New: 1, Changed: 0, Fixed: 1} {
		if got := c.Count(class); got != want {
			t.Errorf("Count(%s) = %d, want %d", class, got, want)
		}
	}
}

// TestV1RunLoadsUnchanged: a run.json written by v1.0.0 still loads, and
// writing it back gives the same bytes, so the new optional fields don't
// change the rhythm-echo/v1 schema.
func TestV1RunLoadsUnchanged(t *testing.T) {
	golden, err := os.ReadFile("testdata/run-v1.0.0.json")
	if err != nil {
		t.Fatal(err)
	}
	dec := json.NewDecoder(bytes.NewReader(golden))
	dec.DisallowUnknownFields()
	var r Run
	if err := dec.Decode(&r); err != nil {
		t.Fatal(err)
	}
	if r.Schema != Schema || len(r.Actions) != 2 || len(r.Actions[0].Messages) != 2 {
		t.Fatalf("loaded run = %+v", r)
	}
	if f, n := r.Failed(); f != 1 || n != 2 {
		t.Errorf("Failed() = %d, %d", f, n)
	}
	out, err := json.Marshal(r)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(out, golden) {
		t.Errorf("round trip changed the bytes:\n got %s\nwant %s", out, golden)
	}
}

// TestNewFieldsAreOptional: a message without the fields added after
// v1.0.0 encodes without any trace of them, and one with them reads back.
func TestNewFieldsAreOptional(t *testing.T) {
	m := Message{ID: "x", Channel: Email, Status: Faked, Source: Source{Via: "smtp"}, At: time.Unix(0, 0).UTC()}
	b, _ := json.Marshal(m)
	if want := `{"id":"x","channel":"email","status":"faked","source":{"via":"smtp"},"at":"1970-01-01T00:00:00Z"}`; string(b) != want {
		t.Errorf("plain message = %s\nwant %s", b, want)
	}
	for _, name := range []string{"truncated", "attachments", "issues", "attribution", "late_for", "site"} {
		if strings.Contains(string(b), name) {
			t.Errorf("empty %s was encoded", name)
		}
	}

	m.Truncated, m.Attribution, m.LateFor, m.Source.Site = true, Late, "checkout", "https://shop.test"
	m.Attachments = []Attachment{{Name: "invoice-42.pdf", Type: "application/pdf", Size: 84211, SHA256: "ab12"}}
	m.Issues = []Issue{{Check: "content/merge-tag", Level: Warning, Title: "t", Key: "content/merge-tag|t"}}
	b, _ = json.Marshal(m)
	var back Message
	if err := json.Unmarshal(b, &back); err != nil {
		t.Fatal(err)
	}
	if !back.Truncated || back.Attribution != Late || back.LateFor != "checkout" || back.Source.Site != "https://shop.test" ||
		len(back.Attachments) != 1 || back.Attachments[0] != m.Attachments[0] || len(back.Issues) != 1 {
		t.Errorf("round trip = %+v", back)
	}

	r := Run{BootIDs: []string{"b1", "b2"}}
	c := Change{Class: Changed, Diff: []DiffLine{{Op: DiffDel, Line: 9, Text: "Total: $40.00"}, {Op: DiffAdd, Line: 9, Text: "Total: $42.00"}}}
	rb, _ := json.Marshal(r)
	cb, _ := json.Marshal(c)
	if !strings.Contains(string(rb), `"boot_ids":["b1","b2"]`) ||
		!strings.Contains(string(cb), `"diff":[{"op":"del","line":9,"text":"Total: $40.00"},{"op":"add","line":9,"text":"Total: $42.00"}]`) {
		t.Errorf("run = %s\nchange = %s", rb, cb)
	}
}
