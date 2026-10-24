// Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
// SPDX-License-Identifier: MIT

//go:build testbed

// Command bench runs Echo's public benchmark against the testbed: for each
// scenario in testbed/scenarios.yaml it sets the good state, runs the
// scenario's action with `rhythm-echo snapshot`, breaks the scenario, runs
// it again with `rhythm-echo compare` and checks the comparison against
// the scenario's expectation. Deliverability scenarios also run
// `rhythm-echo check` on the From domains against a fixture DNS server.
// Other tools (Mailpit, Buggregator, Staging Shield) are judged on the
// same states. Then the environment scenarios check Echo when what's
// around it goes wrong (see environment.go). The scoreboard is written
// to docs/benchmark.md.
//
// Run from the module root with the testbed up (make -C testbed up):
//
//	make build
//	go run -tags testbed ./testbed/bench [-bin bin/rhythm-echo] [-only s01,e05,env] [-tools "mailpit buggregator"]
//	go run -tags testbed ./testbed/bench -diff run1.json run2.json
//
// The local environment scenarios need no testbed: -only env -tools none
// runs them anywhere.
package main

import (
	"cmp"
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"runtime"
	"slices"
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
	var only, diff string
	flag.StringVar(&o.bin, "bin", "bin/rhythm-echo", "the rhythm-echo binary to benchmark")
	flag.StringVar(&o.out, "out", "docs/benchmark.md", "where to write the scoreboard")
	flag.StringVar(&o.work, "work", "", "working folder for runs (default testbed/bench/work)")
	flag.StringVar(&only, "only", "", "comma-separated scenario IDs to run, env for every environment scenario (default all)")
	flag.StringVar(&o.tools, "tools", "auto", "other tools to judge: auto (each that answers), none, or names such as \"mailpit buggregator staging-shield\"")
	flag.DurationVar(&o.timeout, "timeout", 3*time.Minute, "time limit for each command")
	flag.StringVar(&diff, "diff", "", "compare the outcomes in two results.json files (this flag and one argument) and exit 1 if they differ")
	flag.Parse()
	if diff != "" {
		if flag.NArg() != 1 {
			fmt.Fprintln(os.Stderr, "bench: -diff takes two files: -diff a.json b.json")
			os.Exit(2)
		}
		same, err := diffResults(diff, flag.Arg(0), os.Stdout)
		if err != nil {
			fmt.Fprintln(os.Stderr, "bench:", err)
			os.Exit(2)
		}
		if !same {
			os.Exit(1)
		}
		return
	}
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
	Tools    []Tool           // judged, in column order
	ToolErr  map[string]error // why a tool wasn't judged
	Outcomes []Outcome
	Env      []EnvOutcome
}

// Results is results.json: every outcome, for -diff and for a reader.
type Results struct {
	Sites       []Outcome    `json:"sites"`
	Environment []EnvOutcome `json:"environment"`
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
	scenarios, envs, err := m.Pick(o.only)
	if err != nil {
		return err
	}
	toolNames, required, err := parseTools(o.tools)
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
	res := &Run{Started: time.Now(), Partial: len(o.only) > 0, ToolErr: map[string]error{}}
	bin, version, binErr := echoBinary(ctx, o.bin)
	res.Version, res.BinErr = version, binErr

	echo := &Echo{Bin: bin, Testbed: dir, Work: o.work}
	defer echo.Stop()
	// The Docker testbed is needed by the site scenarios and some
	// environment ones; the rest run anywhere.
	testbedUp := len(scenarios) > 0 || slices.ContainsFunc(envs, func(s EnvScenario) bool { return s.Needs == "testbed" })
	if testbedUp {
		testbedUp = sitesAnswer(ctx, m)
		if !testbedUp && len(scenarios) > 0 {
			return errors.New("the testbed's sites don't answer; start them with make -C testbed up")
		}
	}
	if testbedUp {
		if echo.Token, err = TestbedToken(dir); err != nil {
			return err
		}
		for _, name := range toolNames {
			t := newTool(name, dir, echo, o)
			if err := t.Ready(ctx); err != nil {
				if required {
					return err
				}
				res.ToolErr[name] = err
				continue
			}
			res.Tools = append(res.Tools, t)
		}
		if len(scenarios) > 0 {
			zone, err := LoadZone(filepath.Join(dir, "dns", "zone.yaml"))
			if err != nil {
				return err
			}
			dnsServer := &DNSServer{Zone: zone, State: filepath.Join(dir, "state")}
			if err := dnsServer.Start(DNSAddr); err != nil {
				return err
			}
			defer dnsServer.Close()
		}
		if err := Toggle(dir, "all", "good"); err != nil {
			return err
		}
		defer func() { _ = Toggle(dir, "all", "good") }()
	}
	stamp := res.Started.UTC().Format("150405")

	for _, s := range scenarios {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		res.Outcomes = append(res.Outcomes, siteScenario(ctx, o, res, echo, dir, m, s, stamp))
	}

	probe := &ProbeEnv{Bin: bin, Root: filepath.Dir(dir), Testbed: dir, Work: filepath.Join(o.work, "env"), Timeout: o.timeout,
		Windows: runtime.GOOS == "windows"}
	if testbedUp {
		probe.Site = &SiteBench{Echo: echo, Manifest: m, Options: o}
	}
	for _, s := range envs {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		start := time.Now()
		fmt.Fprintf(os.Stderr, "%s %s\n", s.ID, s.Name)
		var out EnvOutcome
		if binErr != nil {
			out = EnvOutcome{Scenario: s, Echo: Result{Outcome: NotRun, Detail: binErr.Error()}}
		} else {
			out = runEnv(ctx, probe, s)
		}
		out.Seconds = time.Since(start).Seconds()
		fmt.Fprintf(os.Stderr, "    echo: %s %s\n", out.Echo.Outcome, out.Echo.Found+out.Echo.Detail)
		res.Env = append(res.Env, out)
	}
	echo.Stop()
	res.Duration = time.Since(res.Started)

	data, err := json.MarshalIndent(Results{Sites: res.Outcomes, Environment: res.Env}, "", "  ")
	if err != nil {
		return err
	}
	if err := os.WriteFile(filepath.Join(o.work, "results.json"), []byte(mask(string(data))), 0o600); err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(o.out), 0o750); err != nil {
		return err
	}
	if err := os.WriteFile(o.out, []byte(mask(scoreboard(m, res))), 0o600); err != nil {
		return err
	}
	if binErr != nil {
		fmt.Fprintf(os.Stderr, "Echo wasn't run: %v. Scoreboard: %s\n", binErr, o.out)
	} else {
		fmt.Fprintf(os.Stderr, "Echo caught %d of %d site scenarios and %d of %d environment scenarios in %s. Scoreboard: %s\n",
			res.caught(), len(res.Outcomes), res.envCaught(), len(res.Env), res.Duration.Round(time.Second), o.out)
	}
	return nil
}

// siteScenario runs one site scenario for Echo and every tool.
func siteScenario(ctx context.Context, o options, res *Run, echo *Echo, dir string, m *Manifest, s Scenario, stamp string) Outcome {
	start := time.Now()
	fmt.Fprintf(os.Stderr, "%s %s\n", s.ID, s.Name)
	out := Outcome{Scenario: s, Tools: map[string]Result{}}
	wd := filepath.Join(o.work, s.ID)
	if err := os.RemoveAll(wd); err == nil {
		_ = os.MkdirAll(wd, 0o750)
	}
	var good, broken *model.Run
	if res.BinErr != nil {
		out.Echo = Result{Outcome: NotRun, Detail: res.BinErr.Error()}
	} else {
		out.Echo, good, broken = benchScenario(ctx, o, echo, dir, m, s, wd)
	}
	for _, t := range res.Tools {
		if good == nil || broken == nil {
			out.Tools[t.Name()] = Result{Outcome: Failed, Detail: "Echo's runs, which " + t.Title() + " is judged from, are missing"}
			continue
		}
		out.Tools[t.Name()] = t.Judge(ctx, s, good, broken, stamp)
	}
	out.Seconds = time.Since(start).Seconds()
	fmt.Fprintf(os.Stderr, "    echo: %s %s\n", out.Echo.Outcome, out.Echo.Found+out.Echo.Detail)
	for _, t := range res.Tools {
		r := out.Tools[t.Name()]
		fmt.Fprintf(os.Stderr, "    %s: %s %s\n", t.Name(), r.Outcome, r.Found+r.Detail)
	}
	return out
}

// newTool returns the tool named name, set up for the testbed.
func newTool(name, dir string, echo *Echo, o options) Tool {
	mp := mailpit{api: mailpitAPI, smtp: mailpitSMTP}
	switch name {
	case "buggregator":
		return buggregator{api: buggregatorAPI, smtp: buggregatorSMTP}
	case "staging-shield":
		probe := &ProbeEnv{Timeout: o.timeout}
		return stagingShield{testbed: dir, mail: mp, work: o.work,
			run: func(ctx context.Context, dir string, args ...string) (int, string) {
				return probe.run(ctx, dir, os.Environ(), args[0], args[1:]...)
			},
			start: func(ctx context.Context, mode string) error { return echo.Start(ctx, mode) },
			action: func(ctx context.Context, dir, action string) error {
				_, err := echo.Run(ctx, o.timeout, dir, append([]string{"snapshot", "--config", echo.Config(), "--action", action,
					"--name", "staging-shield", "--fail-on", "none", "--json", "-o", "run.json"}, Store(dir)...)...)
				return err
			}}
	}
	return mp
}

// sitesAnswer reports whether every site of the testbed answers.
func sitesAnswer(ctx context.Context, m *Manifest) bool {
	for _, u := range m.Sites {
		ctx, cancel := context.WithTimeout(ctx, 3*time.Second)
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, u+"/", nil)
		if err != nil {
			cancel()
			return false
		}
		resp, err := http.DefaultClient.Do(req)
		cancel()
		if err != nil {
			return false
		}
		_ = resp.Body.Close()
	}
	return true
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

func (r *Run) envCaught() int {
	n := 0
	for _, o := range r.Env {
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

	// --fail-on none: the exit code says only whether the command worked.
	// A good state with error issues, or a compare that finds changes,
	// isn't a failure here; Evaluate judges what was found.
	if _, err := e.Run(ctx, o.timeout, wd, append([]string{"snapshot", "--config", e.Config(), "--action", s.Action,
		"--name", "good", "--fail-on", "none", "--json", "-o", "good.json"}, Store(wd)...)...); err != nil {
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
	_, cerr := e.Run(ctx, o.timeout, wd, append([]string{"compare", "--config", e.Config(), "--action", s.Action,
		"--snapshot", "good", "--fail-on", "none", "--json", "-o", "compare.json"}, Store(wd)...)...)
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

// readComparison reads the file `compare --json -o` writes: the
// comparison is the envelope's data, beside the compared run's actions,
// and the number of actions compared is in its summary. A bare
// comparison, as Echo wrote before the envelope, is read too.
func readComparison(path string) (*model.Comparison, error) {
	data, err := os.ReadFile(path) //nolint:gosec // our own output
	if err != nil {
		return nil, err
	}
	var env struct {
		Kind    string                `json:"kind"`
		Summary struct{ Actions int } `json:"summary"`
		Data    json.RawMessage       `json:"data"`
	}
	if err := json.Unmarshal(data, &env); err != nil {
		return nil, err
	}
	if env.Data == nil {
		var c model.Comparison
		if err := json.Unmarshal(data, &c); err != nil {
			return nil, err
		}
		if c.Schema == "" {
			return nil, errors.New("no comparison in the JSON output")
		}
		return &c, nil
	}
	if env.Kind != "comparison" {
		return nil, fmt.Errorf("the JSON output holds a %s, not a comparison", cmp.Or(env.Kind, "result of no kind"))
	}
	// "actions" in the data is the run's actions; it hides the count.
	var d struct {
		model.Comparison
		Actions []model.Action `json:"actions"`
	}
	if err := json.Unmarshal(env.Data, &d); err != nil {
		return nil, err
	}
	if d.Schema == "" {
		return nil, errors.New("no comparison in the JSON output")
	}
	c := d.Comparison
	c.Actions = env.Summary.Actions
	return &c, nil
}
