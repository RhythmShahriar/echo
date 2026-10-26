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
	Scenarios   []Scenario `yaml:"scenarios"`
	Variants    []Variant  `yaml:"variants"`
	Environment []Probe    `yaml:"environment"`
	Fixtures    []Fixture  `yaml:"fixtures"`
}

// Variant is a scenario run a second way in Docker mode: on the real
// WordPress plugin instead of the Go-mode replica. It keeps the
// scenario's expectations; the benchmark runs it only with -mode docker.
type Variant struct {
	ID  string `yaml:"id" json:"id"`   // c01-wp
	Of  string `yaml:"of" json:"of"`   // c01
	CMP string `yaml:"cmp" json:"cmp"` // complianz-wp
	// Site is the host the variant is served on, under .shop.test: the
	// WordPress site of Docker mode with the plugin and the scenario's
	// state turned on.
	Site  string `yaml:"site" json:"site"`
	Needs string `yaml:"needs" json:"needs"` // docker
}

// ProbeNeeds are the values a probe's needs: takes: local (the probe's
// own servers on 127.0.0.1:23410-23439) or docker (the sealed testbed).
var ProbeNeeds = []string{"local", "docker"}

// Probe is an environment scenario (e01 to e18): the site is what it
// always was; what is around Echo goes wrong. Its probe function
// (testbed/consent/bench/probes.go) sets up the good or the broken state
// and runs rhythm-echo as named steps; Good and Broken are checked on
// those steps.
type Probe struct {
	ID     string  `yaml:"id" json:"id"`
	Name   string  `yaml:"name" json:"name"`
	Probe  string  `yaml:"probe" json:"probe"`
	Needs  string  `yaml:"needs" json:"needs"`
	Toggle string  `yaml:"toggle" json:"toggle"`
	Good   []Check `yaml:"good" json:"good"`
	Broken []Check `yaml:"broken" json:"broken"`
}

// Check is one thing a probe step must show. Every field set must hold;
// Says and Never compare case-insensitively with what the step printed
// plus the notes its probe added (such as "chrome left: 0").
type Check struct {
	Step     string   `yaml:"step" json:"step"`
	Exit     *int     `yaml:"exit" json:"exit,omitempty"`
	Verdict  string   `yaml:"verdict" json:"verdict,omitempty"`
	Complete *bool    `yaml:"complete" json:"complete,omitempty"`
	Says     []string `yaml:"says" json:"says,omitempty"`
	Never    []string `yaml:"never" json:"never,omitempty"`
}

// String is the check as the scoreboard lists it.
func (c Check) String() string {
	var parts []string
	if c.Exit != nil {
		parts = append(parts, fmt.Sprintf("exit %d", *c.Exit))
	}
	if c.Verdict != "" {
		parts = append(parts, "verdict "+c.Verdict)
	}
	if c.Complete != nil {
		parts = append(parts, fmt.Sprintf("complete %v", *c.Complete))
	}
	for _, s := range c.Says {
		parts = append(parts, fmt.Sprintf("says %q", s))
	}
	for _, s := range c.Never {
		parts = append(parts, fmt.Sprintf("never %q", s))
	}
	return c.Step + ": " + strings.Join(parts, ", ")
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
	return Parse(data)
}

// Parse decodes and checks a manifest's bytes, as Load does for the file.
func Parse(data []byte) (*Manifest, error) {
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
	for _, v := range m.Variants {
		_, ok := m.Find(v.Of)
		switch {
		case v.ID == "" || seen[v.ID]:
			return fmt.Errorf("variant id %q is empty or listed twice", v.ID)
		case !ok:
			return fmt.Errorf("%s: of %q: no such scenario", v.ID, v.Of)
		case v.Needs != "docker":
			return fmt.Errorf("%s: a variant runs on the real plugins: needs docker", v.ID)
		case v.CMP == "" || v.Site == "":
			return fmt.Errorf("%s: cmp and site are required", v.ID)
		}
		seen[v.ID] = true
	}
	for _, p := range m.Environment {
		switch {
		case p.ID == "" || seen[p.ID]:
			return fmt.Errorf("probe id %q is empty or listed twice", p.ID)
		case p.Probe == "" || p.Name == "" || p.Toggle == "":
			return fmt.Errorf("%s: name, probe and toggle are required", p.ID)
		case !slices.Contains(ProbeNeeds, p.Needs):
			return fmt.Errorf("%s: needs %q: want local or docker", p.ID, p.Needs)
		case len(p.Good) == 0 || len(p.Broken) == 0:
			return fmt.Errorf("%s: a probe needs good and broken checks", p.ID)
		}
		for _, c := range slices.Concat(p.Good, p.Broken) {
			if c.Step == "" {
				return fmt.Errorf("%s: every check names its step", p.ID)
			}
		}
		seen[p.ID] = true
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

// FindProbe returns the environment probe with id.
func (m *Manifest) FindProbe(id string) (Probe, bool) {
	for _, p := range m.Environment {
		if p.ID == id {
			return p, true
		}
	}
	return Probe{}, false
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
