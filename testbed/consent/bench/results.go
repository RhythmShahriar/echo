// Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
// SPDX-License-Identifier: MIT

package main

import (
	"encoding/json"
	"fmt"
	"os"
	"time"

	"github.com/RhythmShahriar/echo/testbed/consent/scenarios"
)

// Results is results.json: everything one benchmark run found, for the
// scoreboard and for -diff.
type Results struct {
	Echo        string            `json:"echo"`    // the version the binary reports
	Mode        string            `json:"mode"`    // go or docker
	Machine     string            `json:"machine"` // OS, architecture and CPUs
	Browser     string            `json:"browser"` // from the first report
	Started     time.Time         `json:"started"`
	Seconds     float64           `json:"seconds"`
	Partial     bool              `json:"partial"` // -only picked some scenarios
	Rounds      int               `json:"rounds"`
	Tools       []string          `json:"tools"`            // the other tools' columns, in order
	ToolNotes   map[string]string `json:"tool_notes"`       // why a tool wasn't run, by name
	Scenarios   []Outcome         `json:"scenarios"`        // c01 to c25 and g01 to g15
	Control     []Control         `json:"control"`          // the control pass, one per good state
	Environment []ProbeOutcome    `json:"environment"`      // e01 to e18
	Clicks      []ClickRow        `json:"clicks"`           // per CMP
	Timing      Timing            `json:"timing"`           // pages and detect
	Measures    []Measure         `json:"measures"`         // every PRD 18.2 and 19 target
	Leaks       int               `json:"leaks"`            // tracker hits at the sink in held mode, every run
	Replicas    []Replica         `json:"replicas"`         // capture dates
	Errors      []string          `json:"errors,omitempty"` // what stopped part of the run
}

// Outcome is one scenario: how Echo did on the broken state (bad
// scenarios) or the good one (good scenarios) plus each other tool.
type Outcome struct {
	Scenario  scenarios.Scenario `json:"scenario"`
	Echo      Verdict            `json:"echo"`
	GoodState *Verdict           `json:"good_state,omitempty"` // a bad scenario's good state, which must be clean
	Tools     map[string]Verdict `json:"tools,omitempty"`
	Seconds   float64            `json:"seconds"`
}

// Control is one good state over every round of the control pass.
type Control struct {
	ID          string   `json:"id"`
	Rounds      int      `json:"rounds"`
	FalseAlarms int      `json:"false_alarms"` // rounds with an error or warning outside allow
	Failed      int      `json:"failed"`       // rounds that couldn't be judged
	Flakes      int      `json:"flakes"`       // rounds whose verdict differs from round 1
	Details     []string `json:"details,omitempty"`
}

// ClickRow is click success for one CMP: verified choices over the runs
// that needed one, Accept and Reject apart.
type ClickRow struct {
	CMP          string `json:"cmp"`
	AcceptNeeded int    `json:"accept_needed"`
	AcceptOK     int    `json:"accept_ok"`
	RejectNeeded int    `json:"reject_needed"`
	RejectOK     int    `json:"reject_ok"`
	Reference    string `json:"reference,omitempty"` // autoconsent's result on the same replica
}

// Timing is the time measures, in seconds.
type Timing struct {
	Pages     int     `json:"pages"`      // page samples
	PageP50   float64 `json:"page_p50"`   // per page, from the reports' duration_ms
	PageP95   float64 `json:"page_p95"`   //
	Detects   int     `json:"detects"`    // consent detect samples
	DetectP50 float64 `json:"detect_p50"` // wall time of consent detect
	DetectMax float64 `json:"detect_max"` //
	RSSMax    int64   `json:"rss_max"`    // the largest peak resident memory of a run, bytes; 0 unknown
}

// The states of a Measure.
const (
	MeasureMet     = "met"
	MeasureMissed  = "missed"
	MeasureUnknown = "not measured"
)

// Measure is one target of PRD 18.2 or 19 with what this run measured.
type Measure struct {
	Name   string `json:"name"`
	Value  string `json:"value"`
	Target string `json:"target"`
	State  string `json:"state"` // met, missed, not measured
	Note   string `json:"note,omitempty"`
}

// Replica is a CMP replica's capture date, from its SOURCE.md.
type Replica struct {
	Name     string `json:"name"`
	Captured string `json:"captured"`
}

// write saves r as indented JSON at path.
func (r *Results) write(path string) error {
	data, err := json.MarshalIndent(r, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, append(data, '\n'), 0o600)
}

// readResults reads a results.json.
func readResults(path string) (*Results, error) {
	data, err := os.ReadFile(path) //nolint:gosec // a results.json the user names
	if err != nil {
		return nil, err
	}
	var r Results
	if err := json.Unmarshal(data, &r); err != nil {
		return nil, fmt.Errorf("%s: %w", path, err)
	}
	return &r, nil
}
