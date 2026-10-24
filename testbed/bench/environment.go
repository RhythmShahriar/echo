// Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
// SPDX-License-Identifier: MIT

//go:build testbed

package main

import (
	"context"
	"fmt"
	"slices"
	"strings"

	"github.com/RhythmShahriar/echo/model"
)

// EnvScenario is one case of the environment going wrong around Echo: the
// site is down, two runs share one Echo, Echo restarts, the resolver is
// gone. The site sends what it always did; the question is whether Echo
// still tells the truth about it. Each has a good state (the environment
// as it should be) and a broken one, set up by its probe.
type EnvScenario struct {
	ID    string `yaml:"id" json:"id"`
	Name  string `yaml:"name" json:"name"`
	Probe string `yaml:"probe" json:"probe"`
	// Needs says what the probe runs against: local (Echo and fixtures on
	// 127.0.0.1:23200-23239, nothing else), testbed (the Docker sites too)
	// or windows (a Windows machine; elsewhere it is not run).
	Needs  string  `yaml:"needs" json:"needs"`
	Toggle string  `yaml:"toggle" json:"toggle"`
	Good   []Check `yaml:"good" json:"good"`
	Broken []Check `yaml:"broken" json:"broken"`
	Known  string  `yaml:"known_issue" json:"known_issue,omitempty"`
}

// Places an environment scenario can need.
var needs = []string{"local", "testbed", "windows"}

// Check is one thing a step of a probe must show. Every field set must
// hold; Says and Never compare case-insensitively with the step's output.
type Check struct {
	Step    string   `yaml:"step" json:"step"`
	Exit    *int     `yaml:"exit" json:"exit,omitempty"`       // the command's exit code
	Verdict string   `yaml:"verdict" json:"verdict,omitempty"` // the --json envelope's verdict
	Says    []string `yaml:"says" json:"says,omitempty"`       // each appears in the output
	Never   []string `yaml:"never" json:"never,omitempty"`     // none appears in the output
	Change  *Expect  `yaml:"change" json:"change,omitempty"`   // the comparison has this change
	NoClass []string `yaml:"no_class" json:"no_class,omitempty"`
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
	for _, s := range c.Says {
		parts = append(parts, "says “"+s+"”")
	}
	for _, s := range c.Never {
		parts = append(parts, "never “"+s+"”")
	}
	if c.Change != nil {
		parts = append(parts, c.Change.String())
	}
	if len(c.NoClass) > 0 {
		parts = append(parts, "no "+strings.ToUpper(strings.Join(c.NoClass, "/")))
	}
	return c.Step + ": " + strings.Join(parts, ", ")
}

// Observation is what one step of a probe saw.
type Observation struct {
	Step    string         `json:"step"`
	Exit    int            `json:"exit"`
	Verdict string         `json:"verdict,omitempty"`
	Output  string         `json:"output"` // what the command printed, plus what the probe noted
	Changes []model.Change `json:"changes,omitempty"`
}

// Probe sets up a scenario's state, good or broken, runs its steps and
// returns what each saw. An error means the probe itself couldn't run
// (a fixture didn't start), not that Echo got it wrong.
type Probe func(ctx context.Context, p *ProbeEnv, broken bool) ([]Observation, error)

// probes are the environment probes by the name the manifest uses.
var probes = map[string]Probe{}

// Verify reports the first check in checks that obs fails, or "".
func Verify(checks []Check, obs []Observation) string {
	for _, c := range checks {
		i := slices.IndexFunc(obs, func(o Observation) bool { return o.Step == c.Step })
		if i < 0 {
			return fmt.Sprintf("%s: the probe has no such step", c.Step)
		}
		if why := c.failed(obs[i]); why != "" {
			return c.Step + ": " + why
		}
	}
	return ""
}

// failed says how o falls short of c, or "" when it doesn't.
func (c Check) failed(o Observation) string {
	out := strings.ToLower(o.Output)
	switch {
	case c.Exit != nil && o.Exit != *c.Exit:
		return fmt.Sprintf("exit %d, want %d", o.Exit, *c.Exit)
	case c.Verdict != "" && o.Verdict != c.Verdict:
		return fmt.Sprintf("verdict %q, want %q", o.Verdict, c.Verdict)
	}
	for _, s := range c.Says {
		if !strings.Contains(out, strings.ToLower(s)) {
			return fmt.Sprintf("doesn't say %q", s)
		}
	}
	for _, s := range c.Never {
		if strings.Contains(out, strings.ToLower(s)) {
			return fmt.Sprintf("says %q", s)
		}
	}
	if c.Change != nil && !slices.ContainsFunc(o.Changes, c.Change.Match) {
		return "no " + c.Change.String()
	}
	for _, ch := range o.Changes {
		if slices.Contains(c.NoClass, ch.Class) {
			return "reports " + describe(ch)
		}
	}
	return ""
}

// EvaluateEnv judges a scenario from what its probe saw in each state.
// A good state that fails its checks is an error: the scenario can't
// tell anything then. A broken state that passes every check is caught.
func EvaluateEnv(s EnvScenario, good, broken []Observation) Result {
	if why := Verify(s.Good, good); why != "" {
		return Result{Outcome: Failed, Detail: "good state: " + why}
	}
	if why := Verify(s.Broken, broken); why != "" {
		return Result{Outcome: Missed, Detail: why}
	}
	var seen []string
	for _, c := range s.Broken {
		seen = append(seen, c.String())
	}
	return Result{Outcome: Caught, Found: strings.Join(seen, "; ")}
}

// EnvOutcome is everything recorded for one environment scenario.
type EnvOutcome struct {
	Scenario EnvScenario   `json:"scenario"`
	Echo     Result        `json:"echo"`
	Good     []Observation `json:"good,omitempty"`
	Broken   []Observation `json:"broken,omitempty"`
	Seconds  float64       `json:"seconds"`
}

// runEnv runs one environment scenario: the good state, then the broken.
func runEnv(ctx context.Context, p *ProbeEnv, s EnvScenario) EnvOutcome {
	out := EnvOutcome{Scenario: s}
	probe := probes[s.Probe]
	switch {
	case probe == nil:
		out.Echo = Result{Outcome: Failed, Detail: "no probe named " + s.Probe}
		return out
	case s.Needs == "windows" && !p.Windows:
		out.Echo = Result{Outcome: NotRun, Detail: "needs a Windows machine; this probe runs only there (CI's go-windows job tests snapshot and compare in ./cmd/..., not this probe)"}
		return out
	case s.Needs == "testbed" && p.Site == nil:
		out.Echo = Result{Outcome: NotRun, Detail: "needs the Docker testbed (make -C testbed up)"}
		return out
	}
	var err error
	if out.Good, err = probe(ctx, p.For(s.ID, "good"), false); err != nil {
		out.Echo = Result{Outcome: Failed, Detail: "good state: " + err.Error()}
		return out
	}
	if out.Broken, err = probe(ctx, p.For(s.ID, "broken"), true); err != nil {
		out.Echo = Result{Outcome: Failed, Detail: "broken state: " + err.Error()}
		return out
	}
	out.Echo = EvaluateEnv(s, out.Good, out.Broken)
	return out
}
