// Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
// SPDX-License-Identifier: MIT

package report

import (
	"cmp"
	"embed"
	"encoding/json"
	"fmt"
	"io"
	"strings"
	"time"

	"github.com/RhythmShahriar/echo/consent"
	"github.com/RhythmShahriar/echo/dns"
	"github.com/RhythmShahriar/echo/headers"
	"github.com/RhythmShahriar/echo/model"
)

// Kind says what an Envelope holds and which schema its data follows.
type Kind string

// The kinds of JSON output, one per command family.
const (
	KindRun        Kind = "run"        // run, snapshot: the run with its actions
	KindComparison Kind = "comparison" // compare, report on a comparison
	KindCheck      Kind = "check"      // check <domain>: the DNS report
	KindHeaders    Kind = "headers"    // headers <file.eml>
	KindMessages   Kind = "messages"   // ls, show: captured messages
	KindSnapshots  Kind = "snapshots"  // snapshots: the stored snapshots
	KindDoctor     Kind = "doctor"     // doctor: what was checked and how it went

	KindConsent        Kind = "consent"         // consent: the consent check's result (ConsentEnvelope)
	KindConsentCatalog Kind = "consent-catalog" // consent cmps, consent trackers
	KindConsentDetect  Kind = "consent-detect"  // consent detect: the banner and its controls
	KindConsentVerify  Kind = "consent-verify"  // consent verify: an evidence folder checked
)

// Kinds lists every kind, in the order the docs give them.
var Kinds = []Kind{KindRun, KindComparison, KindCheck, KindHeaders, KindMessages, KindSnapshots, KindDoctor,
	KindConsent, KindConsentCatalog, KindConsentDetect, KindConsentVerify}

// The verdicts of an Envelope.
const (
	VerdictOK       = "ok"       // nothing found
	VerdictFindings = "findings" // changes or issues; --fail-on decides whether that fails the build
	VerdictError    = "error"    // the result is missing parts: an action failed, DNS didn't answer
)

// Envelope is the one JSON shape every command's --json writes: what it
// is, counts and a verdict that read the same for every kind plus the
// command's own data. Schema stays rhythm-echo/v1; fields are only added.
type Envelope struct {
	Schema   string  `json:"schema"`   // model.Schema
	Kind     Kind    `json:"kind"`     // what Data holds
	Tool     string  `json:"tool"`     // rhythm-echo
	Version  string  `json:"version"`  // the Echo that wrote it
	Complete bool    `json:"complete"` // false when part of it couldn't be checked or a payload was cut to size
	Summary  Summary `json:"summary"`
	Verdict  string  `json:"verdict"` // VerdictOK, VerdictFindings or VerdictError
	Data     any     `json:"data"`    // the command's own result, shaped by Kind

	// failed says a part is missing (Verdict "error"). A payload cut to
	// size leaves Complete false but not failed: everything else was
	// compared, so the verdict is still ok or findings.
	failed bool
}

// Summary counts what an Envelope holds. A count that doesn't apply to
// the kind is 0.
type Summary struct {
	Broke         int `json:"broke"` // changes by class, in a comparison
	New           int `json:"new"`
	Changed       int `json:"changed"`
	Fixed         int `json:"fixed"`
	Errors        int `json:"errors"` // issues by level
	Warnings      int `json:"warnings"`
	Notices       int `json:"notices"`
	Actions       int `json:"actions"` // run: actions run; comparison: actions compared
	Messages      int `json:"messages"`
	FailedActions int `json:"failed_actions"` // actions that errored themselves, not the site's messages
}

// ConsentSummary counts what a consent result holds (PRD 9.3). The
// consent kind has its own summary, so its counts are always written
// (0 included) while every other kind's summary stays as it was, byte
// for byte. The first seven counts mean what they mean in Summary.
type ConsentSummary struct {
	Broke            int `json:"broke"` // changes by class, with --against
	New              int `json:"new"`
	Changed          int `json:"changed"`
	Fixed            int `json:"fixed"`
	Errors           int `json:"errors"` // issues by level
	Warnings         int `json:"warnings"`
	Notices          int `json:"notices"`
	Pages            int `json:"pages"`
	Runs             int `json:"runs"`
	FailedRuns       int `json:"failed_runs"`
	InconclusiveRuns int `json:"inconclusive_runs"`
	Trackers         int `json:"trackers"`
	BeforeChoice     int `json:"before_choice" desc:"How many different consent-needing trackers sent a hit before the visitor chose (trackers, not hits)."`
	AfterReject      int `json:"after_reject" desc:"How many different consent-needing trackers sent a hit after Reject (trackers, not hits)."`
	Held             int `json:"held"`
}

// NewConsentSummary maps a consent result's summary to the envelope's
// field names.
func NewConsentSummary(s consent.Summary) ConsentSummary {
	return ConsentSummary{
		Broke: s.Broke, New: s.New, Changed: s.Changed, Fixed: s.Fixed,
		Errors: s.Errors, Warnings: s.Warnings, Notices: s.Notices,
		Pages: s.Pages, Runs: s.Runs, FailedRuns: s.FailedRuns, InconclusiveRuns: s.InconclusiveRuns,
		Trackers: s.Trackers, BeforeChoice: s.BeforeChoice, AfterReject: s.AfterReject, Held: s.Held,
	}
}

// ConsentEnvelope is the Envelope of the consent kind: the same fields
// in the same order, with ConsentSummary as the summary. WriteConsentJSON
// writes it.
type ConsentEnvelope struct {
	Schema   string         `json:"schema"`   // model.Schema
	Kind     Kind           `json:"kind"`     // KindConsent
	Tool     string         `json:"tool"`     // rhythm-echo
	Version  string         `json:"version"`  // the Echo that wrote it
	Complete bool           `json:"complete"` // false when a run was inconclusive, cut short or canceled
	Summary  ConsentSummary `json:"summary"`
	Verdict  string         `json:"verdict"` // VerdictOK, VerdictFindings or VerdictError
	Data     any            `json:"data"`    // the *consent.Result
	// Error says why a check stopped before it had a result (a usage
	// error, a site that didn't answer); the verdict is then error,
	// complete false and data null.
	Error *ConsentError `json:"error,omitempty"`
}

// ConsentError is why a consent check stopped before it had a result:
// the exit code (2 usage error, 3 couldn't check) and the message Echo
// printed on stderr.
type ConsentError struct {
	Exit    int    `json:"exit"`
	Message string `json:"message"`
}

// Snapshot is one entry of the snapshots kind.
type Snapshot struct {
	Name     string    `json:"name"`
	Started  time.Time `json:"started"`
	Actions  int       `json:"actions"`
	Messages int       `json:"messages"`
	Latest   bool      `json:"latest"` // the snapshot compare uses when none is named
}

// DoctorCheck is one line of the doctor kind.
type DoctorCheck struct {
	Name   string `json:"name"`
	Status string `json:"status"` // ok, warning or error
	Detail string `json:"detail,omitempty"`
	Fix    string `json:"fix,omitempty"`
}

// ToolVersion is the version envelopes give when their data has none
// (check, headers, messages). The CLI sets it at start.
var ToolVersion = "dev"

// comparisonData is a comparison as an envelope holds it: "actions" is
// the compared run's actions, as in a run. Their count is in the
// summary.
type comparisonData struct {
	*model.Comparison
	Actions []model.Action `json:"actions"`
}

// NewEnvelope wraps v as kind. v is a Result (run, comparison or check),
// a *model.Run, a dns.Report, a headers.Result, []model.Message,
// []Snapshot or []DoctorCheck; anything else is wrapped with no counts.
func NewEnvelope(kind Kind, v any) Envelope {
	e := Envelope{Schema: model.Schema, Kind: kind, Tool: "rhythm-echo", Version: ToolVersion, Complete: true, Data: v}
	var issues []model.Issue
	switch t := v.(type) {
	case Result:
		if t.Compared() {
			c := t.Comparison
			e.Version = cmp.Or(c.Version, e.Version)
			e.Summary.Broke, e.Summary.New = c.Count(model.Broke), c.Count(model.New)
			e.Summary.Changed, e.Summary.Fixed = c.Count(model.Changed), c.Count(model.Fixed)
			e.Summary.Actions = c.Actions
			var actions []model.Action
			if t.Run != nil {
				actions = t.Run.Actions
			}
			e.Data = comparisonData{Comparison: c, Actions: nonNil(actions)}
		}
		if t.Run != nil {
			e.addRun(t.Run, !t.Compared())
			issues = Issues(t.Run)
			if !t.Compared() {
				e.Data = t.Run
			}
		}
	case *model.Run:
		e.addRun(t, true)
		issues = Issues(t)
	case dns.Report:
		issues = t.Issues
		e.failed = t.Unreachable
	case *dns.Report:
		issues = t.Issues
		e.failed = t.Unreachable
	case headers.Result:
		issues = t.Issues
	case []model.Message:
		e.Summary.Messages = len(t)
		e.Data = nonNil(t)
	case []Snapshot:
		e.Data = nonNil(t)
	case []DoctorCheck:
		for _, c := range t {
			switch c.Status {
			case string(model.Error):
				e.Summary.Errors++
			case string(model.Warning):
				e.Summary.Warnings++
			}
		}
		e.Data = nonNil(t)
	}
	if len(issues) > 0 {
		n := Counts(issues)
		e.Summary.Errors, e.Summary.Warnings, e.Summary.Notices = n[model.Error], n[model.Warning], n[model.Notice]
	}
	if e.failed {
		e.Complete = false
	}
	s := e.Summary
	switch {
	case e.failed:
		e.Verdict = VerdictError
	case s.Broke+s.New+s.Changed+s.Errors+s.Warnings+s.Notices > 0:
		e.Verdict = VerdictFindings
	default:
		e.Verdict = VerdictOK
	}
	return e
}

// addRun counts a run's actions and messages. A failed action makes the
// result fail; a payload cut to size only makes it incomplete. actions says whether
// the run's action count is the summary's (a comparison counts the
// actions it compared instead).
func (e *Envelope) addRun(r *model.Run, actions bool) {
	e.Version = cmp.Or(r.Version, e.Version)
	if actions && r.Mode != "check" { // check and headers run no actions
		e.Summary.Actions = len(r.Actions)
	}
	failed, _ := failedActions(r)
	e.Summary.FailedActions = failed
	for _, a := range r.Actions {
		e.Summary.Messages += len(a.Messages)
		for _, m := range a.Messages {
			if strings.HasSuffix(m.Body, "…(truncated)") {
				e.Complete = false
			}
		}
	}
	if failed > 0 {
		e.failed = true
	}
}

// WriteJSON writes v in an Envelope of kind, indented, with HTML left
// unescaped so captured text reads as sent.
func WriteJSON(w io.Writer, kind Kind, v any) error {
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	enc.SetEscapeHTML(false)
	return enc.Encode(NewEnvelope(kind, v))
}

//go:embed schema/*.schema.json
var schemas embed.FS

// Schema returns the JSON Schema (draft 2020-12) of kind's envelope, as
// published with the docs.
func Schema(kind Kind) ([]byte, error) {
	data, err := schemas.ReadFile("schema/" + string(kind) + ".schema.json")
	if err != nil {
		return nil, fmt.Errorf("no schema for kind %q", kind)
	}
	return data, nil
}

// nonNil makes an empty list encode as [] rather than null.
func nonNil[T any](s []T) []T {
	if s == nil {
		return []T{}
	}
	return s
}
