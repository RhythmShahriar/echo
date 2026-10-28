// Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
// SPDX-License-Identifier: MIT

package report

import (
	"bytes"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/RhythmShahriar/echo/dns"
	"github.com/RhythmShahriar/echo/headers"
	"github.com/RhythmShahriar/echo/model"
)

var update = flag.Bool("update", false, "rewrite the golden files in testdata")

var stamp = time.Date(2026, 10, 24, 9, 0, 0, 0, time.UTC)

// longText is longer than any line a report shows, so the goldens prove
// before and after are kept whole.
var longText = strings.Repeat("Thanks for your wholesale order; we'll invoice you when it ships. ", 4)

// envelopeSamples is one value per kind, as the commands produce them.
func envelopeSamples() map[Kind]any {
	run := comparedRun()
	c := &model.Comparison{Schema: model.Schema, Tool: "rhythm-echo", Version: "1.0.0", Actions: 2, Created: stamp,
		Before: model.RunRef{Name: "before-update", Dir: ".echo/snapshots/before-update", Started: stamp.Add(-2 * time.Hour)},
		After:  model.RunRef{Dir: ".echo/runs/20261024-090000", Started: stamp},
		Changes: []model.Change{
			{Class: model.Broke, Check: "message/missing", Title: "Email to admin@shop.test is no longer sent", Actions: []string{"Contact form"},
				Before: "sent", After: "not sent", Fix: "Check the mail plugin."},
			{Class: model.Changed, Check: "message/text", Title: "Email to the test submitter: text changed", Actions: []string{"Contact form"},
				Before: longText, After: longText + "\nOrder total: $42.00", Fix: "Read the new text; if the change is intended, take a new snapshot."},
		}}
	dnsRep := dns.Report{Domain: "shop.test", OrgDomain: "shop.test", MX: []string{"mx.shop.test"},
		Issues: []model.Issue{{Check: "dns/dmarc", Level: model.Warning, Title: "No DMARC record", Key: "dns/dmarc|No DMARC record"}}}
	hdr := headers.Result{From: "orders@shop.test", FromDomain: "shop.test", Subject: "Your order",
		Auth: headers.Auth{SPF: "pass", DKIM: "pass", DMARC: "pass"}}
	return map[Kind]any{
		KindRun:        Result{Run: sampleRun()},
		KindComparison: Result{Run: run, Comparison: c},
		KindCheck:      dnsRep,
		KindHeaders:    hdr,
		KindMessages:   run.Actions[0].Messages[:2],
		KindSnapshots:  []Snapshot{{Name: "before-update", Started: stamp, Actions: 2, Messages: 3, Latest: true}},
		KindDoctor: []DoctorCheck{{Name: "Echo", Status: "ok", Detail: "listening on 127.0.0.1:8025"},
			{Name: "WordPress", Status: "warning", Detail: "the Echo plugin isn't active", Fix: "wp plugin activate rhythm-echo"}},
	}
}

func TestEnvelopeGoldens(t *testing.T) {
	ToolVersion = "1.0.0"
	defer func() { ToolVersion = "dev" }()
	for kind, v := range envelopeSamples() {
		var buf bytes.Buffer
		if err := WriteJSON(&buf, kind, v); err != nil {
			t.Fatal(err)
		}
		path := filepath.Join("testdata", "envelope", string(kind)+".json")
		if *update {
			if err := os.MkdirAll(filepath.Dir(path), 0o750); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(path, buf.Bytes(), 0o600); err != nil {
				t.Fatal(err)
			}
		}
		want, err := os.ReadFile(path) //nolint:gosec // test fixture
		if err != nil {
			t.Fatal(err)
		}
		if buf.String() != string(want) {
			t.Errorf("%s: envelope changed (go test ./report -run EnvelopeGoldens -update to accept):\n%s", kind, buf.String())
		}
	}
}

func TestGoldensMatchTheirSchemas(t *testing.T) {
	for _, kind := range Kinds {
		raw, err := Schema(kind)
		if err != nil {
			t.Fatal(err)
		}
		var schema map[string]any
		if err := json.Unmarshal(raw, &schema); err != nil {
			t.Fatalf("%s schema: %v", kind, err)
		}
		path := filepath.Join("testdata", "envelope", string(kind)+".json")
		consentKind := strings.HasPrefix(string(kind), "consent")
		if consentKind {
			// The consent writers keep their goldens with their own testdata.
			path = filepath.Join("testdata", "consent", string(kind)+".envelope.json")
		}
		data, err := os.ReadFile(path) //nolint:gosec // test fixture
		if consentKind && errors.Is(err, fs.ErrNotExist) {
			t.Logf("%s: no golden envelope yet (%s)", kind, path)
			continue
		}
		if err != nil {
			t.Fatal(err)
		}
		var doc any
		if err := json.Unmarshal(data, &doc); err != nil {
			t.Fatal(err)
		}
		for _, problem := range validate(schema, schema, doc, "$") {
			t.Errorf("%s: %s", kind, problem)
		}
		// The validator itself must catch a broken document.
		broken := map[string]any{"schema": "rhythm-echo/v1", "kind": "nope", "summary": map[string]any{"broke": "1"}}
		if len(validate(schema, schema, broken, "$")) < 3 {
			t.Errorf("%s: the validator lets a broken document through", kind)
		}
	}
	if _, err := Schema("nope"); err == nil {
		t.Error("an unknown kind has no schema")
	}
}

func TestEnvelopeSummaryAndVerdict(t *testing.T) {
	s := envelopeSamples()
	for _, tc := range []struct {
		kind     Kind
		v        any
		verdict  string
		complete bool
		summary  Summary
	}{
		{KindRun, s[KindRun], VerdictError, false, Summary{Errors: 1, Actions: 2, Messages: 3, FailedActions: 1}},
		{KindRun, Result{Run: comparedRun()}, VerdictFindings, true, Summary{Errors: 1, Actions: 2, Messages: 3}},
		{KindComparison, s[KindComparison], VerdictFindings, true, Summary{Broke: 1, Changed: 1, Errors: 1, Actions: 2, Messages: 3}},
		{KindCheck, s[KindCheck], VerdictFindings, true, Summary{Warnings: 1}},
		{KindCheck, dns.Report{Unreachable: true}, VerdictError, false, Summary{}},
		{KindHeaders, s[KindHeaders], VerdictOK, true, Summary{}},
		{KindMessages, s[KindMessages], VerdictOK, true, Summary{Messages: 2}},
		{KindDoctor, s[KindDoctor], VerdictFindings, true, Summary{Warnings: 1}},
		{KindSnapshots, []Snapshot(nil), VerdictOK, true, Summary{}},
	} {
		e := NewEnvelope(tc.kind, tc.v)
		if e.Verdict != tc.verdict || e.Complete != tc.complete || e.Summary != tc.summary {
			t.Errorf("%s: verdict %s complete %v summary %+v; want %s %v %+v", tc.kind, e.Verdict, e.Complete, e.Summary, tc.verdict, tc.complete, tc.summary)
		}
	}
	// A cut payload can't be fully checked while everything else was: the
	// result is incomplete and its verdict is still the findings'.
	r := comparedRun()
	r.Actions[0].Messages[2].Body = `{"a":` + "\n…(truncated)"
	if e := NewEnvelope(KindRun, r); e.Complete || e.Verdict == VerdictError {
		t.Errorf("truncated: complete %v verdict %s", e.Complete, e.Verdict)
	}
	// A failed action next to it still makes the verdict error.
	r.Actions[0].Error = "timeout"
	if e := NewEnvelope(KindRun, r); e.Complete || e.Verdict != VerdictError {
		t.Errorf("truncated and failed: complete %v verdict %s", e.Complete, e.Verdict)
	}
}

func TestComparisonEnvelopeKeepsFullTexts(t *testing.T) {
	var buf bytes.Buffer
	if err := WriteJSON(&buf, KindComparison, envelopeSamples()[KindComparison]); err != nil {
		t.Fatal(err)
	}
	var got struct {
		Data struct {
			Actions []model.Action `json:"actions"`
			Changes []model.Change `json:"changes"`
		} `json:"data"`
	}
	if err := json.Unmarshal(buf.Bytes(), &got); err != nil {
		t.Fatal(err)
	}
	if len(got.Data.Actions) != 2 || got.Data.Actions[0].Label != "Contact form" {
		t.Errorf("data.actions must be the action list: %+v", got.Data.Actions)
	}
	if ch := got.Data.Changes[1]; ch.Before != longText || !strings.HasSuffix(ch.After, "Order total: $42.00") {
		t.Errorf("before and after must be whole: %q / %q", ch.Before, ch.After)
	}
}

// validate checks doc against a JSON Schema, for the keywords Echo's
// schemas use: type, required, properties, items, additionalProperties,
// enum, const, anyOf and $ref to #/$defs. It returns one line per
// problem; a doc that matches no anyOf branch gets the first branch's.
func validate(root, schema map[string]any, doc any, at string) []string {
	if branches, ok := schema["anyOf"].([]any); ok {
		var first []string
		for i, b := range branches {
			got := validate(root, b.(map[string]any), doc, at)
			if len(got) == 0 {
				return nil
			}
			if i == 0 {
				first = got
			}
		}
		return first
	}
	if ref, ok := schema["$ref"].(string); ok {
		name := strings.TrimPrefix(ref, "#/$defs/")
		def, _ := root["$defs"].(map[string]any)[name].(map[string]any)
		if def == nil {
			return []string{at + ": unknown $ref " + ref}
		}
		return validate(root, def, doc, at)
	}
	var out []string
	if c, ok := schema["const"]; ok && fmt.Sprint(c) != fmt.Sprint(doc) {
		out = append(out, fmt.Sprintf("%s: %v, want %v", at, doc, c))
	}
	if enum, ok := schema["enum"].([]any); ok && !slices.ContainsFunc(enum, func(e any) bool { return fmt.Sprint(e) == fmt.Sprint(doc) }) {
		out = append(out, fmt.Sprintf("%s: %v isn't one of %v", at, doc, enum))
	}
	if t, ok := schema["type"]; ok {
		var types []string
		switch t := t.(type) {
		case string:
			types = []string{t}
		case []any:
			for _, x := range t {
				types = append(types, x.(string))
			}
		}
		got := jsonType(doc)
		if got == "integer" && slices.Contains(types, "number") {
			got = "number" // every integer is a number
		}
		if !slices.Contains(types, got) {
			return append(out, fmt.Sprintf("%s: %s, want %s", at, jsonType(doc), strings.Join(types, " or ")))
		}
	}
	switch d := doc.(type) {
	case map[string]any:
		props, _ := schema["properties"].(map[string]any)
		if req, ok := schema["required"].([]any); ok {
			for _, k := range req {
				if _, has := d[k.(string)]; !has {
					out = append(out, fmt.Sprintf("%s: %s is missing", at, k))
				}
			}
		}
		extra, _ := schema["additionalProperties"].(map[string]any)
		for k, v := range d {
			if p, ok := props[k].(map[string]any); ok {
				out = append(out, validate(root, p, v, at+"."+k)...)
			} else if extra != nil {
				out = append(out, validate(root, extra, v, at+"."+k)...)
			}
		}
	case []any:
		if items, ok := schema["items"].(map[string]any); ok {
			for i, v := range d {
				out = append(out, validate(root, items, v, fmt.Sprintf("%s[%d]", at, i))...)
			}
		}
	}
	return out
}

func jsonType(v any) string {
	switch t := v.(type) {
	case nil:
		return "null"
	case bool:
		return "boolean"
	case string:
		return "string"
	case float64:
		if t == float64(int64(t)) {
			return "integer"
		}
		return "number"
	case []any:
		return "array"
	case map[string]any:
		return "object"
	}
	return fmt.Sprintf("%T", v)
}
