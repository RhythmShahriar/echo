// Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
// SPDX-License-Identifier: MIT

// Package scenarios reads testbed/consent/scenarios.yaml, the manifest of
// the consent testbed: the 25 bad and 15 good scenario sites, what each
// broken state must make Echo find and what it must not, plus the security
// fixtures the environment probes use. The header of scenarios.yaml
// describes every field.
package scenarios

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"gopkg.in/yaml.v3"
)

// File is the manifest's name in the testbed folder.
const File = "scenarios.yaml"

// Areas and Needs are the values the area: and needs: fields take.
var (
	Areas = []string{"consent-mode", "trackers", "cmp", "choice", "server", "environment"}
	Needs = []string{"go", "docker"}
)

// Manifest is scenarios.yaml.
type Manifest struct {
	Scenarios []Scenario `yaml:"scenarios"`
	Fixtures  []Fixture  `yaml:"fixtures"`
}

// Scenario is one site with a good and a broken state.
type Scenario struct {
	ID     string   `yaml:"id" json:"id"`
	Name   string   `yaml:"name" json:"name"`
	Area   string   `yaml:"area" json:"area"`
	CMP    string   `yaml:"cmp" json:"cmp"`
	Needs  string   `yaml:"needs" json:"needs"`
	Runs   []string `yaml:"runs" json:"runs,omitempty"`
	Args   []string `yaml:"args" json:"args,omitempty"`
	Toggle string   `yaml:"toggle" json:"toggle"`
	Expect []Match  `yaml:"expect" json:"expect,omitempty"`
	// Any is set when one Expect entry is enough.
	Any    bool     `yaml:"any" json:"any,omitempty"`
	Forbid []Match  `yaml:"forbid" json:"forbid,omitempty"`
	Allow  []string `yaml:"allow" json:"allow,omitempty"`
	// SinkZero is false only when the scenario sends hits on purpose
	// (--send-hits); nil means true.
	SinkZero *bool `yaml:"sink_zero" json:"sink_zero,omitempty"`
}

// Good reports whether s is one of the good scenarios (g01 to g15),
// which have no broken state.
func (s Scenario) Good() bool { return strings.HasPrefix(s.ID, "g") }

// WantSinkZero reports whether the sink must count no hit for s.
func (s Scenario) WantSinkZero() bool { return s.SinkZero == nil || *s.SinkZero }

// Match names a finding: an issue (check, tracker, level, title, run), a
// comparison change (class, check, tracker, title) or a run outcome
// (run, status, reason). Empty fields match anything.
type Match struct {
	Check   string `yaml:"check" json:"check,omitempty"`
	Tracker string `yaml:"tracker" json:"tracker,omitempty"`
	Level   string `yaml:"level" json:"level,omitempty"`
	Class   string `yaml:"class" json:"class,omitempty"`
	Title   string `yaml:"title" json:"title,omitempty"`
	Run     string `yaml:"run" json:"run,omitempty"`
	Status  string `yaml:"status" json:"status,omitempty"`
	Reason  string `yaml:"reason" json:"reason,omitempty"`
}

// IsRun reports whether m is about a run's outcome rather than a finding.
func (m Match) IsRun() bool {
	return m.Check == "" && m.Class == "" && (m.Status != "" || m.Reason != "")
}

func (m Match) String() string {
	var parts []string
	add := func(k, v string) {
		if v != "" {
			parts = append(parts, k+"="+v)
		}
	}
	add("class", m.Class)
	add("check", m.Check)
	add("tracker", m.Tracker)
	add("level", m.Level)
	add("run", m.Run)
	add("status", m.Status)
	add("reason", m.Reason)
	if m.Title != "" {
		parts = append(parts, fmt.Sprintf("title~%q", m.Title))
	}
	return strings.Join(parts, " ")
}

// Fixture is a security fixture site and what it is for.
type Fixture struct {
	ID  string `yaml:"id" json:"id"`
	Use string `yaml:"use" json:"use"`
}

// Load reads and checks dir/scenarios.yaml. Unknown keys are errors, so
// a misspelled field can't silently drop an expectation.
func Load(dir string) (*Manifest, error) {
	data, err := os.ReadFile(filepath.Join(dir, File)) //nolint:gosec // the testbed's own manifest
	if err != nil {
		return nil, err
	}
	var m Manifest
	dec := yaml.NewDecoder(bytes.NewReader(data))
	dec.KnownFields(true)
	if err := dec.Decode(&m); err != nil {
		return nil, fmt.Errorf("%s: %w", File, err)
	}
	if err := m.check(); err != nil {
		return nil, fmt.Errorf("%s: %w", File, err)
	}
	return &m, nil
}

// check finds what yaml can't: duplicate ids, values outside their sets,
// a bad scenario with nothing to expect.
func (m *Manifest) check() error {
	seen := map[string]bool{}
	for _, s := range m.Scenarios {
		switch {
		case s.ID == "" || seen[s.ID]:
			return fmt.Errorf("scenario id %q is empty or listed twice", s.ID)
		case !slices.Contains(Areas, s.Area):
			return fmt.Errorf("%s: area %q: want one of %s", s.ID, s.Area, strings.Join(Areas, ", "))
		case !slices.Contains(Needs, s.Needs):
			return fmt.Errorf("%s: needs %q: want go or docker", s.ID, s.Needs)
		case s.Name == "" || s.Toggle == "" || s.CMP == "":
			return fmt.Errorf("%s: name, cmp and toggle are required", s.ID)
		case !s.Good() && len(s.Expect) == 0:
			return fmt.Errorf("%s: a bad scenario needs at least one expect", s.ID)
		case s.Good() && s.Allow == nil:
			return fmt.Errorf("%s: a good scenario lists what it allows (allow: [] for nothing)", s.ID)
		case s.Good() && len(s.Expect) > 0:
			return fmt.Errorf("%s: a good scenario expects nothing; list notices under allow", s.ID)
		}
		seen[s.ID] = true
	}
	for _, f := range m.Fixtures {
		if f.ID == "" || seen[f.ID] {
			return fmt.Errorf("fixture id %q is empty or listed twice", f.ID)
		}
		seen[f.ID] = true
	}
	return nil
}

// Find returns the scenario with id.
func (m *Manifest) Find(id string) (Scenario, bool) {
	for _, s := range m.Scenarios {
		if s.ID == id {
			return s, true
		}
	}
	return Scenario{}, false
}

// Sites lists every site the manifest names: scenarios and fixtures.
func (m *Manifest) Sites() []string {
	var ids []string
	for _, s := range m.Scenarios {
		ids = append(ids, s.ID)
	}
	for _, f := range m.Fixtures {
		ids = append(ids, f.ID)
	}
	return ids
}
