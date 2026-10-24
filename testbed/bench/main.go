// Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
// SPDX-License-Identifier: MIT

//go:build testbed

// Command bench runs Echo's public benchmark against the testbed: for each
// scenario in testbed/scenarios.yaml it sets the good state, runs the
// scenario's action with `rhythm-echo snapshot`, breaks the scenario, runs
// it again with `rhythm-echo compare` and checks the comparison against
// the scenario's expectation. Deliverability scenarios also run
// `rhythm-echo check` on the From domains against a fixture DNS server.
// Mailpit, the usual staging mail catcher, is judged on the same states.
// The scoreboard is written to docs/benchmark.md.
//
// Run from the module root with the testbed up (make -C testbed up):
//
//	make build
//	go run -tags testbed ./testbed/bench [-bin bin/rhythm-echo] [-only s01,s05] [-tools auto|none|mailpit]
package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"time"

	"github.com/RhythmShahriar/echo/model"
	"github.com/RhythmShahriar/echo/store"
)

type options struct {
	bin, out, work string
	only           []string
	tools          string
	timeout        time.Duration
}

func main() {
	var o options
	var only string
	flag.StringVar(&o.bin, "bin", "bin/rhythm-echo", "the rhythm-echo binary to benchmark")
	flag.StringVar(&o.out, "out", "docs/benchmark.md", "where to write the scoreboard")
	flag.StringVar(&o.work, "work", "", "working folder for runs (default testbed/bench/work)")
	flag.StringVar(&only, "only", "", "comma-separated scenario IDs to run (default all)")
	flag.StringVar(&o.tools, "tools", "auto", "comparison tools: auto (Mailpit when the testbed's answers), none, or mailpit")
	flag.DurationVar(&o.timeout, "timeout", 3*time.Minute, "time limit for each command")
	flag.Parse()
	if only != "" {
		o.only = strings.Split(only, ",")
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()
	if err := run(ctx, o); err != nil {
		fmt.Fprintln(os.Stderr, "bench:", err)
		os.Exit(1)
	}
}

// Outcome is everything recorded for one scenario.
type Outcome struct {
	Scenario Scenario          `json:"scenario"`
	Echo     Result            `json:"echo"`
	Tools    map[string]Result `json:"tools"`
	Seconds  float64           `json:"seconds"`
}

// Run is the whole benchmark, for the scoreboard.
type Run struct {
	Version  string
	Started  time.Time
	Duration time.Duration
	Partial  bool
	BinErr   error
	Mailpit  error // why Mailpit wasn't judged; nil when it was
	Outcomes []Outcome
}

func run(ctx context.Context, o options) error {
	dir, err := Testbed()
	if err != nil {
		return err
	}
	m, err := LoadManifest(filepath.Join(dir, "scenarios.yaml"))
	if err != nil {
		return err
	}
	scenarios, err := m.Pick(o.only)
	if err != nil {
		return err
	}
	if o.work == "" {
		o.work = filepath.Join(dir, "bench", "work")
	}
	if o.work, err = filepath.Abs(o.work); err != nil {
		return err
	}
	if err := os.MkdirAll(o.work, 0o750); err != nil {
		return err
	}
	res := &Run{Started: time.Now(), Partial: len(o.only) > 0}
	bin, version, binErr := echoBinary(ctx, o.bin)
	res.Version, res.BinErr = version, binErr
	switch o.tools {
	case "none":
		res.Mailpit = errors.New("-tools none")
	case "auto", "mailpit", "":
		res.Mailpit = mailpitReady(ctx)
		if res.Mailpit != nil && o.tools == "mailpit" {
			return res.Mailpit
		}
	default:
		return fmt.Errorf("-tools: unknown %q (auto, none, mailpit)", o.tools)
	}

	zone, err := LoadZone(filepath.Join(dir, "dns", "zone.yaml"))
	if err != nil {
		return err
	}
	dnsServer := &DNSServer{Zone: zone, State: filepath.Join(dir, "state")}
	if err := dnsServer.Start(DNSAddr); err != nil {
		return err
	}
	defer dnsServer.Close()

	echo := &Echo{Bin: bin, Testbed: dir, Work: o.work}
	defer echo.Stop()
	if err := Toggle(dir, "all", "good"); err != nil {
		return err
	}
	defer func() { _ = Toggle(dir, "all", "good") }()
	stamp := res.Started.UTC().Format("150405")

	for _, s := range scenarios {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		start := time.Now()
		fmt.Fprintf(os.Stderr, "%s %s\n", s.ID, s.Name)
		out := Outcome{Scenario: s, Tools: map[string]Result{}}
		wd := filepath.Join(o.work, s.ID)
		if err := os.RemoveAll(wd); err != nil {
			return err
		}
		if err := os.MkdirAll(wd, 0o750); err != nil {
			return err
		}
		var good, broken *model.Run
		if binErr != nil {
			out.Echo = Result{Outcome: NotRun, Detail: binErr.Error()}
		} else {
			out.Echo, good, broken = benchScenario(ctx, o, echo, dir, m, s, wd)
		}
		switch {
		case res.Mailpit != nil:
		case good == nil || broken == nil:
			out.Tools["mailpit"] = Result{Outcome: Failed, Detail: "Echo's runs, which Mailpit is fed from, are missing"}
		default:
			out.Tools["mailpit"] = judgeMailpit(ctx, s, good, broken, stamp)
		}
		out.Seconds = time.Since(start).Seconds()
		fmt.Fprintf(os.Stderr, "    echo: %s %s\n", out.Echo.Outcome, out.Echo.Found+out.Echo.Detail)
		if r, ok := out.Tools["mailpit"]; ok {
			fmt.Fprintf(os.Stderr, "    mailpit: %s %s\n", r.Outcome, r.Found+r.Detail)
		}
		res.Outcomes = append(res.Outcomes, out)
	}
	echo.Stop()
	res.Duration = time.Since(res.Started)

	data, err := json.MarshalIndent(res.Outcomes, "", "  ")
	if err != nil {
		return err
	}
	if err := os.WriteFile(filepath.Join(o.work, "results.json"), data, 0o600); err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(o.out), 0o750); err != nil {
		return err
	}
	if err := os.WriteFile(o.out, []byte(scoreboard(m, res)), 0o600); err != nil {
		return err
	}
	if binErr != nil {
		fmt.Fprintf(os.Stderr, "Echo wasn't run: %v. Scoreboard: %s\n", binErr, o.out)
	} else {
		fmt.Fprintf(os.Stderr, "Echo caught %d of %d in %s. Scoreboard: %s\n", res.caught(), len(res.Outcomes), res.Duration.Round(time.Second), o.out)
	}
	return nil
}

func (r *Run) caught() int {
	n := 0
	for _, o := range r.Outcomes {
		if o.Echo.Outcome == Caught {
			n++
		}
	}
	return n
}

// benchScenario runs one scenario: reset, good → snapshot, broken →
// compare (plus the DNS checks) and returns Echo's result with the good
// and broken runs, for Mailpit.
func benchScenario(ctx context.Context, o options, e *Echo, dir string, m *Manifest, s Scenario, wd string) (Result, *model.Run, *model.Run) {
	fail := func(format string, a ...any) (Result, *model.Run, *model.Run) {
		return Result{Outcome: Failed, Detail: fmt.Sprintf(format, a...)}, nil, nil
	}
	defer func() { _ = Toggle(dir, s.ID, "good") }()
	if err := Toggle(dir, s.ID, "good"); err != nil {
		return fail("%v", err)
	}
	if err := e.Start(ctx, s.Mode); err != nil {
		return fail("starting Echo: %v", err)
	}
	// Leftovers from the scenario before (a queue a broken flush didn't
	// drain) go out now, tied to no action.
	for _, line := range m.Reset {
		if err := e.Shell(ctx, o.timeout, line); err != nil {
			fmt.Fprintf(os.Stderr, "    reset: %v\n", err)
		}
	}

	if _, err := e.Run(ctx, o.timeout, wd, "snapshot", "--config", e.Config(), "--action", s.Action,
		"--name", "good", "--json", "-o", "good.json"); err != nil {
		return fail("snapshot: %v", err)
	}
	good, err := store.LoadRun(filepath.Join(wd, ".echo", "snapshots", "good"))
	if err != nil {
		return fail("snapshot: %v", err)
	}
	var goodDNS []model.Issue
	if s.DNS != nil {
		if goodDNS, _, err = checkRun(ctx, e, o.timeout, wd, "good", s.DNS, good); err != nil {
			return fail("check: %v", err)
		}
	}

	if err := Toggle(dir, s.ID, "broken"); err != nil {
		return fail("%v", err)
	}
	_, cerr := e.Run(ctx, o.timeout, wd, "compare", "--config", e.Config(), "--action", s.Action,
		"--snapshot", "good", "--json", "-o", "compare.json")
	cmp, err := readComparison(filepath.Join(wd, "compare.json"))
	switch {
	case err != nil && cerr != nil:
		return fail("compare: %v", cerr)
	case err != nil:
		return fail("compare.json: %v", err)
	}
	broken, err := store.LoadRun(under(wd, cmp.After.Dir))
	if err != nil {
		return fail("compare: %v", err)
	}
	if s.DNS != nil {
		brokenDNS, domains, err := checkRun(ctx, e, o.timeout, wd, "broken", s.DNS, broken)
		if err != nil {
			return Result{Outcome: Failed, Detail: "check: " + err.Error()}, good, broken
		}
		deliverability(cmp, goodDNS, brokenDNS, domains)
		if data, err := json.MarshalIndent(cmp, "", "  "); err == nil {
			_ = os.WriteFile(filepath.Join(wd, "compare+check.json"), data, 0o600)
		}
	}
	r := Evaluate(s, cmp)
	if r.Outcome != Caught {
		// What the runs saw, to tell a testbed problem from a blind spot.
		r.Detail = strings.TrimSpace(r.Detail + " good run: " + sent(good) + "; broken run: " + sent(broken))
	}
	return r, good, broken
}

// sent summarizes what a run's actions sent.
func sent(r *model.Run) string {
	var parts []string
	for _, a := range r.Actions {
		if a.Error != "" {
			parts = append(parts, "error: "+a.Error)
		}
		for _, m := range a.Messages {
			parts = append(parts, fmt.Sprintf("%s %s %s", m.Channel, strings.Join(m.To, ","), m.Status))
		}
	}
	if len(parts) == 0 {
		return "nothing"
	}
	return strings.Join(parts, ", ")
}

// echoBinary finds the binary and its version. A missing binary isn't
// fatal: the scoreboard says so.
func echoBinary(ctx context.Context, bin string) (string, string, error) {
	abs, err := filepath.Abs(bin)
	if err != nil {
		return "", "", err
	}
	if _, err := os.Stat(abs); err != nil {
		return "", "", fmt.Errorf("%s not found (make build)", bin)
	}
	out, err := command(ctx, 30*time.Second, "", nil, abs, "--version")
	if err != nil {
		return abs, "", nil //nolint:nilerr // the version is only shown in the scoreboard
	}
	return abs, strings.TrimSpace(out), nil
}

// readComparison reads `compare --json` output.
func readComparison(path string) (*model.Comparison, error) {
	data, err := os.ReadFile(path) //nolint:gosec // our own output
	if err != nil {
		return nil, err
	}
	var c model.Comparison
	if err := json.Unmarshal(data, &c); err != nil {
		return nil, err
	}
	if c.Schema == "" {
		return nil, errors.New("no comparison in the JSON output")
	}
	return &c, nil
}
