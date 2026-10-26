// Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
// SPDX-License-Identifier: MIT

package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"

	"github.com/RhythmShahriar/echo/consent"
	"github.com/RhythmShahriar/echo/report"
)

// Envelope is the consent envelope rhythm-echo consent --json writes,
// with its data read back as a consent result.
type Envelope struct {
	Schema   string                `json:"schema"`
	Kind     string                `json:"kind"`
	Version  string                `json:"version"`
	Complete bool                  `json:"complete"`
	Summary  report.ConsentSummary `json:"summary"`
	Verdict  string                `json:"verdict"`
	Data     consent.Result        `json:"data"`
}

// readEnvelope reads a consent envelope from path. Another kind is an
// error: the harness must never judge a scenario on the wrong report.
func readEnvelope(path string) (*Envelope, error) {
	data, err := os.ReadFile(path) //nolint:gosec // a report the harness asked rhythm-echo to write
	if err != nil {
		return nil, err
	}
	return parseEnvelope(data)
}

// parseEnvelope decodes a consent envelope.
func parseEnvelope(data []byte) (*Envelope, error) {
	var e Envelope
	if err := json.Unmarshal(data, &e); err != nil {
		return nil, fmt.Errorf("the JSON report: %w", err)
	}
	if e.Kind != string(report.KindConsent) {
		if e.Kind == "" {
			return nil, errors.New("the JSON report has no kind; not a consent envelope")
		}
		return nil, fmt.Errorf("the JSON report is kind %q, not consent", e.Kind)
	}
	return &e, nil
}

// runs calls f for every run of every page in e.
func (e *Envelope) runs(f func(p consent.Page, r consent.Run)) {
	for _, p := range e.Data.Pages {
		for _, r := range p.Runs {
			f(p, r)
		}
	}
}

// titles lists every issue and change title in e, for a miss's detail.
func (e *Envelope) titles() []string {
	var out []string
	for _, is := range e.Data.Issues {
		out = append(out, is.Check+": "+is.Title)
	}
	if c := e.Data.Comparison; c != nil {
		for _, ch := range c.Changes {
			out = append(out, ch.Class+": "+ch.Title)
		}
	}
	return out
}
