// Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
// SPDX-License-Identifier: MIT

package main

import (
	"bytes"
	"flag"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/RhythmShahriar/echo/testbed/consent/judge"
	"github.com/RhythmShahriar/echo/testbed/consent/scenarios"
)

var update = flag.Bool("update", false, "rewrite the golden files")

// goldenResults is a fixed benchmark run for the scoreboard golden.
func goldenResults() *Results {
	two := 2
	c01 := scenarios.Scenario{ID: "c01", Name: "GA4 before consent", CMP: "complianz"}
	c02 := scenarios.Scenario{ID: "c02", Name: "Meta Pixel ignores Consent Mode", CMP: "complianz"}
	g01 := scenarios.Scenario{ID: "g01", Name: "The correct setup", CMP: "complianz", Allow: []string{}}
	return &Results{
		Echo: "rhythm-echo 1.2.0", Mode: "docker", Machine: "linux/amd64, 4 CPUs", Browser: "Chrome for Testing 156.0.8078.47",
		Started: time.Date(2026, 10, 26, 9, 0, 0, 0, time.UTC), Seconds: 5400, Rounds: 10,
		Tools: []string{toolWEC}, ToolNotes: map[string]string{toolAutoconsent: "not run: AUTOCONSENT_DIR isn't set"},
		Scenarios: []Outcome{
			{Scenario: c01, Echo: judge.Verdict{Outcome: judge.Caught, Found: "check=consent/before-choice"}, GoodState: &judge.Verdict{Outcome: judge.Clean},
				Tools: map[string]judge.Verdict{toolWEC: {Outcome: judge.Caught}}},
			{Scenario: c02, Echo: judge.Verdict{Outcome: judge.Missed, Detail: `no title~"Meta | Pixel"; found: consent/before-choice: Meta fired early`}, GoodState: &judge.Verdict{Outcome: judge.Clean},
				Tools: map[string]judge.Verdict{toolWEC: {Outcome: judge.Caught}}},
			{Scenario: g01, Echo: judge.Verdict{Outcome: judge.Clean}, Tools: map[string]judge.Verdict{toolWEC: {Outcome: judge.FalseAlarm}}},
		},
		Control: []Control{{ID: "c01", Rounds: 10}, {ID: "g01", Rounds: 10, Flakes: 1, Details: []string{"round 4 differs from round 1: ok [], then error []"}}},
		Environment: []ProbeOutcome{
			{Probe: scenarios.Probe{ID: "e01", Name: "The site is down"}, Echo: judge.Verdict{Outcome: judge.Caught, Found: "check: exit 3"}},
			{Probe: scenarios.Probe{ID: "e16", Name: "Private addresses on the page", Good: []scenarios.Check{{Step: "check", Exit: &two}}}, Echo: judge.Verdict{Outcome: judge.NotRun, Detail: "needs the sealed Docker testbed (-mode docker)"}},
		},
		Clicks:   []ClickRow{{CMP: "complianz", AcceptNeeded: 20, AcceptOK: 20, RejectNeeded: 20, RejectOK: 19}},
		Timing:   Timing{Pages: 60, PageP50: 18.2, PageP95: 31.5, Detects: 39, DetectP50: 4.1, DetectMax: 9.8, RSSMax: 96 << 20},
		Measures: []Measure{{Name: "Catches", Value: "1 of 2", Target: "25 of 25", State: MeasureMissed}, {Name: "Flakes (control pass)", Value: "1", Target: "0", State: MeasureMissed}},
		Replicas: []Replica{{Name: "cookiebot", Captured: "2026-10-26"}},
	}
}

func TestScoreboardGolden(t *testing.T) {
	var b bytes.Buffer
	if err := writeScoreboard(&b, goldenResults()); err != nil {
		t.Fatal(err)
	}
	golden := filepath.Join("testdata", "scoreboard.golden")
	if *update {
		if err := os.WriteFile(golden, b.Bytes(), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	want, err := os.ReadFile(golden)
	if err != nil {
		t.Fatal(err)
	}
	if got := b.String(); got != strings.ReplaceAll(string(want), "\r\n", "\n") {
		t.Errorf("the scoreboard differs from %s (go test -run Golden -update rewrites it):\n%s", golden, got)
	}
}

func TestScoreboardText(t *testing.T) {
	var b bytes.Buffer
	_ = writeScoreboard(&b, goldenResults())
	s := b.String()
	for _, want := range []string{
		GeneratedHeader + "\n",
		"Echo caught 1 of 2 scenarios. False alarms: 0 in 20 checks of unchanged good sites.",
		"nothing reached the internet (sink count 0, must be 0)",
		`no title~"Meta \| Pixel"; found: consent/before-choice: Meta fired early`, // the actual title next to the expected one
		"| c02 | Meta Pixel ignores Consent Mode | complianz | missed | caught |",
		"| complianz | 20 of 20 (100%) | 19 of 20 (95%) | not run |",
	} {
		if !strings.Contains(s, want) {
			t.Errorf("the scoreboard lacks %q", want)
		}
	}
	// The writing rules hold for the generated page too.
	for _, bad := range []string{"\u2014", ",\x20and ", ",\x20but "} {
		if strings.Contains(s, bad) {
			t.Errorf("the scoreboard has %q", bad)
		}
	}
}

func TestReplicas(t *testing.T) {
	rs := replicas()
	if len(rs) < 8 {
		t.Fatalf("replicas = %+v", rs)
	}
	for _, r := range rs {
		if r.Captured == "unknown" || r.Name == "vendor" {
			t.Errorf("replica %+v", r)
		}
	}
}

func TestDiff(t *testing.T) {
	dir := t.TempDir()
	a, b := filepath.Join(dir, "a.json"), filepath.Join(dir, "b.json")
	r := goldenResults()
	if err := r.write(a); err != nil {
		t.Fatal(err)
	}
	r.Timing.PageP50 = 99 // timings may differ
	r.Scenarios[0].Echo.Detail = "other words"
	if err := r.write(b); err != nil {
		t.Fatal(err)
	}
	var out bytes.Buffer
	if same, err := diffResults(a, b, &out); err != nil || !same || !strings.Contains(out.String(), "the same in both runs") {
		t.Errorf("same outcomes: %v %v %s", same, err, out.String())
	}
	r.Scenarios[0].Echo.Outcome = judge.Missed
	r.Control[1].Flakes = 0
	_ = r.write(b)
	out.Reset()
	same, err := diffResults(a, b, &out)
	if err != nil || same || !strings.Contains(out.String(), "c01: echo caught, good state clean, wec caught | echo missed, good state clean, wec caught") ||
		!strings.Contains(out.String(), "control g01: 0 false alarms, 1 flakes, 0 failed | 0 false alarms, 0 flakes, 0 failed") {
		t.Errorf("different outcomes: %v %v\n%s", same, err, out.String())
	}
	empty := filepath.Join(dir, "empty.json")
	_ = (&Results{}).write(empty)
	if _, err := diffResults(a, empty, &out); err == nil || !strings.Contains(err.Error(), "no outcomes") {
		t.Errorf("an empty run: %v", err)
	}
	if _, err := diffResults(a, filepath.Join(dir, "none.json"), &out); err == nil {
		t.Error("a missing file: no error")
	}
	// Through the command: exit 1 on a difference, 0 when the same.
	var stdout, stderr bytes.Buffer
	if code := cli([]string{"-diff", a, b}, &stdout, &stderr); code != 1 {
		t.Errorf("-diff different: exit %d", code)
	}
	if code := cli([]string{"-diff", a, a}, &stdout, &stderr); code != 0 {
		t.Errorf("-diff same: exit %d", code)
	}
}

func TestMeasures(t *testing.T) {
	r := goldenResults()
	r.Mode = "go"
	o := options{bin: "size-v1.1.2.txt", mode: "go"}
	ms := measures(r, o, []time.Duration{20 * time.Second, 50 * time.Second}, []time.Duration{16 * time.Second}, nil, 0, "partial run", "..")
	byName := map[string]Measure{}
	for _, m := range ms {
		byName[m.Name] = m
	}
	for name, state := range map[string]string{
		"Catches":                                MeasureMissed,
		"Time per page, p50":                     MeasureMet,
		"Time per page, p95":                     MeasureMissed,
		"consent detect, slowest":                MeasureMissed,
		"Fleet: 50 sites, --parallel 6":          MeasureUnknown,
		"Echo resident memory (Chrome excluded)": MeasureMet,
		"Binary size growth over v1.1.2":         MeasureUnknown,
		"e10 completeness (sink vs reported)":    MeasureUnknown,
		"Click success per CMP":                  MeasureMissed,
	} {
		if got := byName[name].State; got != state {
			t.Errorf("%s: %s, want %s (%+v)", name, got, state, byName[name])
		}
	}
	// Docker mode judges runtime/rhythm-echo; a small file is no growth.
	root := t.TempDir()
	_ = os.MkdirAll(filepath.Join(root, "bench"), 0o750)
	_ = os.MkdirAll(filepath.Join(root, "runtime"), 0o750)
	_ = os.WriteFile(filepath.Join(root, "bench", "size-v1.1.2.txt"), []byte("# size\n16519328\n"), 0o600)
	o.mode = "docker"
	if m := sizeMeasure(o, root); m.State != MeasureUnknown {
		t.Errorf("size with no runtime binary = %+v", m)
	}
	_ = os.WriteFile(filepath.Join(root, "runtime", "rhythm-echo"), []byte("tiny"), 0o600)
	if m := sizeMeasure(o, root); m.State != MeasureMet {
		t.Errorf("size in Docker mode = %+v", m)
	}
	big, _ := os.Create(filepath.Join(root, "runtime", "rhythm-echo"))
	_ = big.Truncate(16519328 + 9<<20)
	_ = big.Close()
	if m := sizeMeasure(o, root); m.State != MeasureMissed {
		t.Errorf("9 MiB growth = %+v", m)
	}
	// Go mode fails on correctness only, Docker mode on every target.
	if f := failures(r); len(f) == 0 || strings.Contains(strings.Join(f, "\n"), "Time per page") {
		t.Errorf("Go mode failures = %q", f)
	}
	r.Mode = "docker"
	r.Measures = ms
	if f := strings.Join(failures(r), "\n"); !strings.Contains(f, "Time per page, p95") {
		t.Errorf("Docker mode failures = %q", f)
	}
	// A full Docker run fails on a target it couldn't measure (the fleet
	// here) but never on the reproduce row, which -diff judges.
	r.Partial = false
	f := strings.Join(failures(r), "\n")
	if !strings.Contains(f, "Fleet: 50 sites, --parallel 6: not measured (partial run), target") {
		t.Errorf("Docker mode, unmeasured fleet: failures = %q", f)
	}
	if strings.Contains(f, reproduceMeasure) {
		t.Errorf("Docker mode fails on the reproduce row: %q", f)
	}
	// A partial Docker run (-only) measures only what it picked.
	r.Partial = true
	if f := strings.Join(failures(r), "\n"); strings.Contains(f, "not measured") {
		t.Errorf("partial Docker run fails on unmeasured targets: %q", f)
	}
	// Go mode never fails on a target it couldn't measure.
	r.Mode, r.Partial = "go", false
	if f := strings.Join(failures(r), "\n"); strings.Contains(f, "not measured") {
		t.Errorf("Go mode fails on unmeasured targets: %q", f)
	}
}
