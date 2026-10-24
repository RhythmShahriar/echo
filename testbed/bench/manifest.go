// Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
// SPDX-License-Identifier: MIT

//go:build testbed

package main

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"

	"gopkg.in/yaml.v3"

	"github.com/RhythmShahriar/echo/model"
)

// Manifest is testbed/scenarios.yaml.
type Manifest struct {
	Sites       map[string]string `yaml:"sites"`
	Reset       []string          `yaml:"reset"`
	Scenarios   []Scenario        `yaml:"scenarios"`
	Environment []EnvScenario     `yaml:"environment"`
}

// Scenario is one case of a site that stops saying what it should.
type Scenario struct {
	ID      string   `yaml:"id" json:"id"`
	Name    string   `yaml:"name" json:"name"`
	Area    string   `yaml:"area" json:"area"`
	Stack   string   `yaml:"stack" json:"stack"`
	Action  string   `yaml:"action" json:"action"`
	Channel string   `yaml:"channel" json:"channel"`
	Mode    string   `yaml:"mode" json:"mode"`
	Toggle  string   `yaml:"toggle" json:"toggle"`
	DNS     *DNS     `yaml:"dns" json:"dns,omitempty"`
	Expect  []Expect `yaml:"expect" json:"expect"`
	Mailpit string   `yaml:"mailpit" json:"mailpit,omitempty"` // what a reader of the inbox would see, when the problem is in an email
	// Known is an Echo bug that keeps this scenario from being caught,
	// reported upstream; the scoreboard shows it next to the miss.
	Known string `yaml:"known_issue" json:"known_issue,omitempty"`
}

// DNS asks the benchmark to also check the From domains of the emails the
// action sent, against the fixture DNS, with what's known of the sending
// service (rhythm-echo check --provider --return-path).
type DNS struct {
	Provider   string `yaml:"provider" json:"provider"`
	ReturnPath string `yaml:"return_path" json:"return_path"`
}

// Expect is one acceptable finding: a change of Class whose check starts
// with Check and whose title contains Title (case-insensitive).
type Expect struct {
	Class string `yaml:"class" json:"class"`
	Check string `yaml:"check" json:"check"`
	Title string `yaml:"title" json:"title"`
}

// Match reports whether c is the expected finding.
func (e Expect) Match(c model.Change) bool {
	return c.Class == e.Class && e.about(c)
}

// about reports whether c is about the expected problem, whatever its class.
func (e Expect) about(c model.Change) bool {
	return strings.HasPrefix(c.Check, e.Check) && strings.Contains(strings.ToLower(c.Title), strings.ToLower(e.Title))
}

func (e Expect) String() string {
	s := strings.ToUpper(e.Class) + " " + e.Check
	if e.Title != "" {
		s += " “" + e.Title + "”"
	}
	return s
}

// Areas in scoreboard order.
var areas = []string{"lost", "content", "integrations", "deliverability", "staging", "inbound"}

var areaNames = map[string]string{"lost": "Lost messages", "content": "Wrong content", "integrations": "Integrations",
	"deliverability": "Deliverability", "staging": "Staging safety", "inbound": "Inbound"}

// LoadManifest reads and checks scenarios.yaml.
func LoadManifest(path string) (*Manifest, error) {
	data, err := os.ReadFile(path) //nolint:gosec // the testbed's own manifest
	if err != nil {
		return nil, err
	}
	var m Manifest
	if err := yaml.Unmarshal(data, &m); err != nil {
		return nil, fmt.Errorf("%s: %w", path, err)
	}
	seen := map[string]bool{}
	for i := range m.Scenarios {
		s := &m.Scenarios[i]
		if s.Mode == "" {
			s.Mode = "seal"
		}
		switch {
		case s.ID == "" || seen[s.ID]:
			return nil, fmt.Errorf("%s: missing or duplicate id %q", path, s.ID)
		case m.Sites[s.Stack] == "":
			return nil, fmt.Errorf("%s: %s: unknown stack %q", path, s.ID, s.Stack)
		case s.Action == "":
			return nil, fmt.Errorf("%s: %s: no action", path, s.ID)
		case !slices.Contains(areas, s.Area):
			return nil, fmt.Errorf("%s: %s: unknown area %q", path, s.ID, s.Area)
		case s.Mode != "seal" && s.Mode != "observe":
			return nil, fmt.Errorf("%s: %s: mode %q isn't seal or observe", path, s.ID, s.Mode)
		case !slices.Contains(model.Channels, model.Channel(s.Channel)) && s.Channel != "dns":
			return nil, fmt.Errorf("%s: %s: unknown channel %q", path, s.ID, s.Channel)
		case len(s.Expect) == 0:
			return nil, fmt.Errorf("%s: %s: no expect", path, s.ID)
		}
		for _, e := range s.Expect {
			if !slices.Contains(model.Classes, e.Class) {
				return nil, fmt.Errorf("%s: %s: unknown class %q", path, s.ID, e.Class)
			}
		}
		seen[s.ID] = true
	}
	for _, s := range m.Environment {
		switch {
		case s.ID == "" || seen[s.ID]:
			return nil, fmt.Errorf("%s: missing or duplicate id %q", path, s.ID)
		case s.Name == "" || s.Toggle == "":
			return nil, fmt.Errorf("%s: %s: no name or toggle", path, s.ID)
		case probes[s.Probe] == nil:
			return nil, fmt.Errorf("%s: %s: no probe named %q", path, s.ID, s.Probe)
		case !slices.Contains(needs, s.Needs):
			return nil, fmt.Errorf("%s: %s: needs %q isn't one of %s", path, s.ID, s.Needs, strings.Join(needs, ", "))
		case len(s.Good) == 0 || len(s.Broken) == 0:
			return nil, fmt.Errorf("%s: %s: needs checks for both the good and the broken state", path, s.ID)
		}
		for _, c := range append(slices.Clone(s.Good), s.Broken...) {
			if c.Step == "" {
				return nil, fmt.Errorf("%s: %s: a check without a step", path, s.ID)
			}
			if c.Change != nil && !slices.Contains(model.Classes, c.Change.Class) {
				return nil, fmt.Errorf("%s: %s: unknown class %q", path, s.ID, c.Change.Class)
			}
			for _, cl := range c.NoClass {
				if !slices.Contains(model.Classes, cl) {
					return nil, fmt.Errorf("%s: %s: unknown class %q", path, s.ID, cl)
				}
			}
		}
		seen[s.ID] = true
	}
	return &m, nil
}

// Pick returns the site and environment scenarios whose IDs are in only
// (all when empty). "env" picks every environment scenario.
func (m *Manifest) Pick(only []string) ([]Scenario, []EnvScenario, error) {
	if len(only) == 0 {
		return m.Scenarios, m.Environment, nil
	}
	var sites []Scenario
	var env []EnvScenario
	for _, id := range only {
		if id == "env" {
			env = append(env, m.Environment...)
			continue
		}
		if i := slices.IndexFunc(m.Scenarios, func(s Scenario) bool { return s.ID == id }); i >= 0 {
			sites = append(sites, m.Scenarios[i])
			continue
		}
		i := slices.IndexFunc(m.Environment, func(s EnvScenario) bool { return s.ID == id })
		if i < 0 {
			return nil, nil, fmt.Errorf("unknown scenario %q", id)
		}
		env = append(env, m.Environment[i])
	}
	return sites, env, nil
}

// Outcomes of a scenario for one tool.
const (
	Caught     = "caught"
	OtherClass = "other class" // found the problem but classed differently
	Missed     = "missed"
	CantSee    = "can't see" // outside what the tool captures
	Failed     = "error"     // the tool didn't produce a result
	NotRun     = "not run"
)

// Result is how one tool did on one scenario.
type Result struct {
	Outcome string   `json:"outcome"`
	Found   string   `json:"found,omitempty"`  // the change that matched, or the tool's evidence
	Detail  string   `json:"detail,omitempty"` // why it errored, wasn't run or missed
	Others  []string `json:"others,omitempty"` // other changes the comparison reported
}

// Evaluate checks an Echo comparison against the scenario.
func Evaluate(s Scenario, cmp *model.Comparison) Result {
	var r Result
	var about *model.Change
	for i, c := range cmp.Changes {
		matched := false
		for _, e := range s.Expect {
			if e.Match(c) && r.Outcome != Caught {
				r.Outcome, r.Found, matched = Caught, describe(c), true
			}
			if e.about(c) {
				matched = true
				if about == nil {
					about = &cmp.Changes[i]
				}
			}
		}
		if !matched {
			r.Others = append(r.Others, describe(c))
		}
	}
	switch {
	case r.Outcome == Caught:
	case about != nil:
		r.Outcome, r.Found = OtherClass, describe(*about)
	default:
		r.Outcome = Missed
	}
	return r
}

func describe(c model.Change) string {
	s := strings.ToUpper(c.Class) + " " + c.Check + ": " + c.Title
	if c.Before != "" || c.After != "" {
		s += fmt.Sprintf(" (%s → %s)", orDash(c.Before), orDash(c.After))
	}
	return s
}

func orDash(s string) string {
	if s == "" {
		return "–"
	}
	return s
}

// Testbed locates the testbed directory (holding scenarios.yaml and
// toggle.sh) from the working directory: the module root, testbed/ or
// testbed/bench.
func Testbed() (string, error) {
	wd, err := os.Getwd()
	if err != nil {
		return "", err
	}
	for _, d := range []string{filepath.Join(wd, "testbed"), wd, filepath.Dir(wd)} {
		if _, err := os.Stat(filepath.Join(d, "scenarios.yaml")); err == nil {
			if _, err := os.Stat(filepath.Join(d, "toggle.sh")); err == nil {
				return d, nil
			}
		}
	}
	return "", errors.New("can't find testbed/scenarios.yaml; run from the echo module root")
}

// Toggle switches a scenario (or "all") to good or broken.
func Toggle(testbed, id, state string) error {
	out, err := exec.Command(filepath.Join(testbed, "toggle.sh"), id, state).CombinedOutput() //nolint:gosec,noctx // our own script
	if err != nil {
		return fmt.Errorf("toggle.sh %s %s: %w: %s", id, state, err, strings.TrimSpace(string(out)))
	}
	return nil
}
