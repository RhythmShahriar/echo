// Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
// SPDX-License-Identifier: MIT

package main

import (
	"cmp"
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/signal"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/RhythmShahriar/echo/testbed/consent/judge"
	"github.com/RhythmShahriar/echo/testbed/consent/scenarios"
	"github.com/RhythmShahriar/echo/testbed/consent/server"
)

// options are the benchmark's flags.
type options struct {
	bin, mode, out, work string
	root                 string // testbed/consent; "" finds it from the working folder
	only                 []string
	skip                 []string
	rounds, jobs         int
	timeout              time.Duration
	listen               string
	probePort            int
}

// skipWords are what -skip takes.
var skipWords = []string{"control", "probes", "detect", "fleet", "tools"}

func main() { os.Exit(cli(os.Args[1:], os.Stdout, os.Stderr)) }

// cli runs the benchmark with args and returns the exit code: 0 when
// every target held, 1 when one didn't (in Go mode: a scenario missed or
// failed, a false alarm, a flake or a leak), 2 for usage and 3 when the
// benchmark itself couldn't run.
func cli(args []string, stdout, stderr io.Writer) int {
	var o options
	var only, skip, diff string
	var list bool
	fs := flag.NewFlagSet("bench", flag.ContinueOnError)
	fs.SetOutput(stderr)
	fs.StringVar(&o.bin, "bin", filepath.Join("bin", "rhythm-echo"), "the rhythm-echo binary to benchmark")
	fs.StringVar(&o.mode, "mode", "go", "go (the testbed server in this process) or docker (the sealed Compose testbed, make -C testbed/consent up)")
	fs.IntVar(&o.rounds, "rounds", 10, "control pass rounds over every good state")
	fs.IntVar(&o.jobs, "jobs", 2, "scenarios checked at once")
	fs.StringVar(&only, "only", "", "comma-separated scenario and probe IDs to run (default all)")
	fs.StringVar(&skip, "skip", "", "comma-separated parts to leave out: "+strings.Join(skipWords, ", "))
	fs.StringVar(&o.out, "out", filepath.Join("docs", "benchmark-consent.md"), "where to write the scoreboard")
	fs.StringVar(&o.work, "work", "", "working folder (default testbed/consent/bench/work)")
	fs.StringVar(&o.root, "root", "", "the testbed/consent folder (default: found from the working folder)")
	fs.DurationVar(&o.timeout, "timeout", 5*time.Minute, "time limit for each rhythm-echo command")
	fs.StringVar(&o.listen, "listen", server.DefaultListen, "Go mode: the testbed server's address")
	fs.IntVar(&o.probePort, "probe-port", DefaultProbePort, "the first of the local probes' ports (30 are used)")
	fs.BoolVar(&list, "list", false, "print the scenario tables of testbed/consent/README.md from scenarios.yaml and exit")
	fs.StringVar(&diff, "diff", "", "compare the outcomes in two results.json files (this flag and one argument) and exit 1 if they differ")
	if err := fs.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return 0
		}
		return 2
	}
	if list {
		return listScenarios(o.root, stdout, stderr)
	}
	if diff != "" {
		if fs.NArg() != 1 {
			fmt.Fprintln(stderr, "bench: -diff takes two files: -diff a.json b.json")
			return 2
		}
		same, err := diffResults(diff, fs.Arg(0), stdout)
		if err != nil {
			fmt.Fprintln(stderr, "bench:", err)
			return 3
		}
		if !same {
			return 1
		}
		return 0
	}
	if o.mode != "go" && o.mode != "docker" {
		fmt.Fprintf(stderr, "bench: -mode %q: want go or docker\n", o.mode)
		return 2
	}
	if only != "" {
		o.only = strings.Split(only, ",")
	}
	if skip != "" {
		o.skip = strings.Split(skip, ",")
		for _, w := range o.skip {
			if !slices.Contains(skipWords, w) {
				fmt.Fprintf(stderr, "bench: -skip %q: want %s\n", w, strings.Join(skipWords, ", "))
				return 2
			}
		}
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()
	r, err := benchmark(ctx, o, stderr)
	if err != nil {
		fmt.Fprintln(stderr, "bench:", err)
		return 3
	}
	if fails := failures(r); len(fails) > 0 {
		fmt.Fprintln(stderr, "bench: targets missed:\n  "+strings.Join(fails, "\n  "))
		return 1
	}
	return 0
}

// listScenarios prints the README's scenario tables for the manifest in
// root (default: found from the working folder).
func listScenarios(root string, stdout, stderr io.Writer) int {
	if root == "" {
		var err error
		if root, err = testbedDir(); err != nil {
			fmt.Fprintln(stderr, "bench:", err)
			return 3
		}
	}
	m, err := scenarios.Load(root)
	if err != nil {
		fmt.Fprintln(stderr, "bench:", err)
		return 3
	}
	writeList(stdout, m)
	return 0
}

// testbedDir finds testbed/consent from the module root or inside it.
func testbedDir() (string, error) {
	for _, d := range []string{filepath.Join("testbed", "consent"), "."} {
		if _, err := os.Stat(filepath.Join(d, scenarios.File)); err == nil {
			return filepath.Abs(d)
		}
	}
	return "", errors.New("no testbed/consent/scenarios.yaml here; run from the module root")
}

// picked splits the manifest into what -only names: scenarios, Docker
// variants (in Docker mode) and probes. Unknown IDs are an error.
func picked(m *scenarios.Manifest, only []string, mode string) ([]job, []scenarios.Probe, error) {
	want := func(id string) bool { return len(only) == 0 || slices.Contains(only, id) }
	for _, id := range only {
		_, isScenario := m.Find(id)
		_, isProbe := m.FindProbe(id)
		isVariant := slices.ContainsFunc(m.Variants, func(v scenarios.Variant) bool { return v.ID == id })
		if !isScenario && !isProbe && !isVariant {
			return nil, nil, fmt.Errorf("-only %s: no scenario, variant or probe by that id", id)
		}
	}
	var jobs []job
	for _, s := range m.Scenarios {
		if want(s.ID) && (s.Needs == "go" || mode == "docker") {
			jobs = append(jobs, jobOf(s))
		}
	}
	if mode == "docker" {
		for _, v := range m.Variants {
			if base, ok := m.Find(v.Of); ok && want(v.ID) {
				jobs = append(jobs, variantJob(base, v))
			}
		}
	}
	var probes []scenarios.Probe
	for _, p := range m.Environment {
		if want(p.ID) {
			probes = append(probes, p)
		}
	}
	return jobs, probes, nil
}

// benchmark runs everything o asks for and writes results.json and the
// scoreboard.
func benchmark(ctx context.Context, o options, stderr io.Writer) (*Results, error) {
	root := o.root
	if root == "" {
		var err error
		if root, err = testbedDir(); err != nil {
			return nil, err
		}
	}
	m, err := scenarios.Load(root)
	if err != nil {
		return nil, err
	}
	jobs, probes, err := picked(m, o.only, o.mode)
	if err != nil {
		return nil, err
	}
	if o.bin, err = filepath.Abs(o.bin); err != nil {
		return nil, err
	}
	if _, err := os.Stat(o.bin); err != nil {
		return nil, fmt.Errorf("-bin %s: %w (make build)", o.bin, err)
	}
	if o.work == "" {
		o.work = filepath.Join(root, "bench", "work")
	}
	if o.work, err = filepath.Abs(o.work); err != nil {
		return nil, err
	}
	if err := os.RemoveAll(o.work); err != nil {
		return nil, err
	}
	if err := os.MkdirAll(o.work, 0o750); err != nil {
		return nil, err
	}
	if err := fenceSettings(o.work); err != nil {
		return nil, err
	}
	var logMu sync.Mutex
	logf := func(s string) {
		logMu.Lock()
		defer logMu.Unlock()
		fmt.Fprintln(stderr, "bench:", s)
	}
	r := &Results{Mode: o.mode, Started: time.Now().UTC(), Partial: len(o.only) > 0, Rounds: o.rounds,
		Machine: fmt.Sprintf("%s/%s, %d CPUs", runtime.GOOS, runtime.GOARCH, runtime.NumCPU()), Replicas: replicas()}
	r.Echo = echoVersion(ctx, o.bin)
	skipped := func(w string) bool { return slices.Contains(o.skip, w) }

	bed, echo, err := openBed(ctx, o, root, jobs, logf)
	if err != nil {
		return nil, err
	}
	defer bed.Close()
	defer echo.stop()
	// Leave every scenario good, whatever happens.
	defer func() {
		for _, j := range jobs {
			_ = bed.SetBroken(j.State, false)
		}
	}()

	b := newBench(bed, o.work, o.timeout, logf)
	if !skipped("tools") {
		b.tools = setupTools(root, o.work, os.Getenv)
		defer b.tools.close()
		r.ToolNotes = b.tools.notes
		if b.tools.wec != nil {
			r.Tools = append(r.Tools, toolWEC)
		}
	}

	// The scenarios, jobs at once; each leaves its scenario good.
	r.Scenarios = make([]Outcome, len(jobs))
	sem := make(chan struct{}, max(o.jobs, 1))
	var wg sync.WaitGroup
	for i, j := range jobs {
		wg.Add(1)
		go func() {
			defer wg.Done()
			sem <- struct{}{}
			defer func() { <-sem }()
			r.Scenarios[i] = b.runScenario(ctx, j)
		}()
	}
	wg.Wait()

	var goodStates []scenarios.Scenario
	for _, j := range jobs {
		if j.Site == j.State {
			goodStates = append(goodStates, j.S)
		}
	}
	if !skipped("control") && o.rounds > 0 {
		r.Control = b.controlPass(ctx, goodStates, o.rounds, o.jobs)
	}
	var detect []time.Duration
	var detectNotes []string
	if !skipped("detect") {
		detect, detectNotes = b.detectTimes(ctx, goodStates)
	}
	var fleet time.Duration
	fleetNote := "partial run or -skip fleet"
	if !skipped("fleet") && !r.Partial {
		fleet, fleetNote = b.fleet(ctx, goodStates)
	}
	if !skipped("probes") {
		p := &ProbeEnv{Bin: o.bin, Root: root, Work: filepath.Join(o.work, "env"), Timeout: o.timeout, Port: o.probePort}
		if o.mode == "docker" {
			p.Bed = bed
		}
		for _, pr := range probes {
			out := runProbe(ctx, p, pr)
			logf(fmt.Sprintf("%s %s %s", pr.ID, out.Echo.Outcome, out.Echo.Detail))
			r.Environment = append(r.Environment, out)
		}
	}
	r.Clicks = b.clickRows()
	if b.tools != nil && b.tools.auto != nil {
		b.autoconsentColumn(ctx, r, goodStates)
	}
	r.Browser, r.Leaks = b.browser, b.leaks
	r.Timing = Timing{Pages: len(b.pages), PageP50: seconds(percentile(b.pages, 50)), PageP95: seconds(percentile(b.pages, 95)),
		Detects: len(detect), DetectP50: seconds(percentile(detect, 50)), DetectMax: seconds(maxDuration(detect)), RSSMax: b.rssMax}
	r.Measures = measures(r, o, b.pages, detect, detectNotes, fleet, fleetNote, root)
	if ctx.Err() != nil {
		r.Errors = append(r.Errors, "interrupted: the results are partial")
		r.Partial = true
	}
	r.Seconds = seconds(time.Since(r.Started))
	if err := r.write(filepath.Join(o.work, "results.json")); err != nil {
		return r, err
	}
	f, err := os.Create(o.out) //nolint:gosec // the scoreboard path the user gives
	if err != nil {
		return r, err
	}
	defer func() { _ = f.Close() }()
	return r, writeScoreboard(f, r)
}

// openBed starts the bed for o.mode; in Go mode also the Echo that c18
// needs, when a picked scenario uses {echo}.
func openBed(ctx context.Context, o options, root string, jobs []job, logf func(string)) (Bed, *echoUp, error) {
	if o.mode == "docker" {
		b, err := newDockerBed(root, o.work)
		return b, nil, err
	}
	var echo *echoUp
	needsEcho := slices.ContainsFunc(jobs, func(j job) bool {
		return slices.ContainsFunc(j.S.Args, func(a string) bool { return strings.Contains(a, "{echo}") })
	})
	echoURL, proxy, ca := "", "", ""
	if needsEcho {
		var err error
		echoAPI, echoProxy := echoAddrs(o.probePort)
		echo, err = startEchoUp(ctx, o.bin, filepath.Join(o.work, "echo"), echoAPI, echoProxy)
		if err != nil {
			logf("no Echo for --echo, so c18 fails: " + err.Error())
		} else {
			echoURL, proxy, ca = "http://"+echoAPI, "http://"+echoProxy, echo.ca
		}
	}
	env := append(os.Environ(), "RHYTHM_OFFLINE=1", "CI=true", "NO_COLOR=1")
	b, err := newGoBed(root, o.listen, o.bin, o.work, env, echoURL, proxy, ca)
	if err != nil {
		echo.stop()
		return nil, nil, err
	}
	return b, echo, nil
}

// echoVersion is what bin --version prints, first line.
func echoVersion(ctx context.Context, bin string) string {
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	ex := run(execCommand(ctx, bin, "--version"))
	if ex.Exit != 0 {
		return "unknown"
	}
	return strings.TrimSpace(lastLine(strings.SplitN(ex.Stdout, "\n", 2)[0]))
}

// clickRows is the click tally as rows, sorted by CMP.
func (b *Bench) clickRows() []ClickRow {
	b.mu.Lock()
	defer b.mu.Unlock()
	rows := make([]ClickRow, 0, len(b.clicks))
	for _, c := range b.clicks {
		rows = append(rows, *c)
	}
	slices.SortFunc(rows, func(a, c ClickRow) int { return cmp.Compare(a.CMP, c.CMP) })
	return rows
}

// autoconsentColumn runs autoconsent on the first good state of each
// CMP in r's click table.
func (b *Bench) autoconsentColumn(ctx context.Context, r *Results, list []scenarios.Scenario) {
	for i := range r.Clicks {
		idx := slices.IndexFunc(list, func(s scenarios.Scenario) bool { return s.CMP == r.Clicks[i].CMP })
		if idx < 0 {
			continue
		}
		_ = b.bed.SetBroken(list[idx].ID, false)
		r.Clicks[i].Reference = list[idx].ID + ": " + b.tools.auto.judge(ctx, list[idx])
	}
}

// measures builds the targets table of PRD 18.2 and 19.
func measures(r *Results, o options, pages, detect []time.Duration, detectNotes []string, fleet time.Duration, fleetNote, root string) []Measure {
	var out []Measure
	caught, bad := 0, 0
	for _, s := range r.Scenarios {
		if s.Scenario.Good() || strings.Contains(s.Scenario.ID, "-") {
			continue
		}
		bad++
		if s.Echo.Outcome == judge.Caught {
			caught++
		}
	}
	out = append(out, measure("Catches", fmt.Sprintf("%d of %d", caught, bad), "25 of 25", bad > 0, caught == bad && bad == 25, partialNote(r)))
	alarms, flakes, checks, failed := 0, 0, 0, 0
	for _, c := range r.Control {
		alarms += c.FalseAlarms
		flakes += c.Flakes
		checks += c.Rounds
		failed += c.Failed
	}
	for _, s := range r.Scenarios {
		if s.Scenario.Good() && s.Echo.Outcome == judge.FalseAlarm {
			alarms++
		}
	}
	controlNote := ""
	if failed > 0 {
		controlNote = fmt.Sprintf("%d control rounds couldn't be judged", failed)
	}
	out = append(out,
		measure("False alarms (control pass)", fmt.Sprintf("%d in %d checks", alarms, checks), "0", checks > 0, alarms == 0 && failed == 0, controlNote),
		measure("Flakes (control pass)", fmt.Sprint(flakes), "0", checks > 0, flakes == 0, ""))
	clicksOK, clicksKnown := true, false
	for _, c := range r.Clicks {
		clicksKnown = clicksKnown || c.AcceptNeeded+c.RejectNeeded > 0
		clicksOK = clicksOK && c.AcceptOK == c.AcceptNeeded && c.RejectOK == c.RejectNeeded
	}
	out = append(out, measure("Click success per CMP", clickValue(r.Clicks), "100% on fixtures", clicksKnown, clicksOK, ""))
	out = append(out, measure("Leaked hits at the sink (held mode)", fmt.Sprint(r.Leaks), "0", len(r.Scenarios) > 0, r.Leaks == 0, ""))
	e10 := slices.IndexFunc(r.Environment, func(e ProbeOutcome) bool { return e.Probe.ID == "e10" })
	if e10 >= 0 && r.Environment[e10].Echo.Outcome != judge.NotRun {
		e := r.Environment[e10].Echo
		out = append(out, measure("e10 completeness (sink vs reported)", e.Outcome, "exact match", true, e.Outcome == judge.Caught, e.Detail))
	} else {
		out = append(out, measure("e10 completeness (sink vs reported)", "", "exact match", false, false, "e10 not run"))
	}
	p50, p95 := percentile(pages, 50), percentile(pages, 95)
	out = append(out,
		measure("Time per page, p50", secs(p50), "under 25 s", len(pages) > 0, p50 < targetPageP50, ""),
		measure("Time per page, p95", secs(p95), "under 45 s", len(pages) > 0, p95 < targetPageP95, ""),
		measure("consent detect, slowest", secs(maxDuration(detect)), "under 15 s", len(detect) > 0, maxDuration(detect) < targetDetect, strings.Join(detectNotes, "; ")))
	fleetKnown := fleet > 0 && fleetNote == ""
	out = append(out, measure(fmt.Sprintf("Fleet: %d sites, --parallel %d", fleetSites, fleetParallel), fmt.Sprintf("%.1f min", fleet.Minutes()),
		"under 15 minutes on a 4-core runner", fleetKnown, fleet < targetFleet, fleetNote))
	out = append(out, measure("Echo resident memory (Chrome excluded)", mib(r.Timing.RSSMax), "under 200 MB",
		r.Timing.RSSMax > 0, r.Timing.RSSMax < targetRSS, ""))
	out = append(out, sizeMeasure(o, root))
	out = append(out, measure(reproduceMeasure, "", "no outcome differs", false, false, "run make -C testbed/consent reproduce"))
	return out
}

// partialNote says a catch count covers only the picked scenarios.
func partialNote(r *Results) string {
	if r.Partial {
		return "partial run"
	}
	return ""
}

// clickValue sums the click table.
func clickValue(rows []ClickRow) string {
	ok, n := 0, 0
	for _, c := range rows {
		ok += c.AcceptOK + c.RejectOK
		n += c.AcceptNeeded + c.RejectNeeded
	}
	return pct(ok, n)
}

// sizeMeasure compares the binary with v1.1.2's. Only Docker mode's
// binary (runtime/rhythm-echo, the one the runner runs) is built with
// the release flags; a plain go build carries symbols and would look
// several MiB bigger than it ships, so Go mode doesn't judge it.
func sizeMeasure(o options, root string) Measure {
	const name, target = "Binary size growth over v1.1.2", "at most 8 MiB"
	base, err := baselineSize(filepath.Join(root, "bench", "size-v1.1.2.txt"))
	if err != nil {
		return measure(name, "", target, false, false, err.Error())
	}
	bin := o.bin
	if o.mode == "docker" {
		bin = filepath.Join(root, "runtime", "rhythm-echo")
	}
	fi, err := os.Stat(bin)
	if err != nil {
		return measure(name, "", target, false, false, err.Error())
	}
	growth := fi.Size() - base
	value := fmt.Sprintf("%s (%s, v1.1.2 %s)", mib(growth), mib(fi.Size()), mib(base))
	if o.mode != "docker" {
		return measure(name, value, target, false, false, "measured in Docker mode, whose binary is built like a release")
	}
	return measure(name, value, target, true, growth <= targetSizeGrowth, "")
}

// reproduceMeasure is the one target a single run can't measure: make
// reproduce judges it with -diff over two runs.
const reproduceMeasure = "make reproduce (two full runs, -diff)"

// failures lists why r fails: in Docker mode every target missed plus, on
// a full run, every target not measured (but reproduceMeasure), since
// a benchmark of record that couldn't measure a target hasn't met it; in
// Go mode the correctness ones (catches, false alarms, flakes, leaks,
// probes), since a developer machine's timings aren't the record.
func failures(r *Results) []string {
	var out []string
	for _, s := range r.Scenarios {
		switch s.Echo.Outcome {
		case judge.Caught, judge.Clean:
		default:
			out = append(out, s.Scenario.ID+" "+s.Echo.Outcome)
		}
		if s.GoodState != nil && s.GoodState.Outcome != judge.Clean {
			out = append(out, s.Scenario.ID+" good state "+s.GoodState.Outcome)
		}
	}
	for _, c := range r.Control {
		if c.FalseAlarms+c.Flakes+c.Failed > 0 {
			out = append(out, fmt.Sprintf("control %s: %d false alarms, %d flakes, %d failed", c.ID, c.FalseAlarms, c.Flakes, c.Failed))
		}
	}
	for _, e := range r.Environment {
		if e.Echo.Outcome != judge.Caught && e.Echo.Outcome != judge.NotRun {
			out = append(out, e.Probe.ID+" "+e.Echo.Outcome)
		}
	}
	if r.Leaks > 0 {
		out = append(out, fmt.Sprintf("%d hits leaked to the sink", r.Leaks))
	}
	if r.Mode == "docker" {
		for _, m := range r.Measures {
			switch {
			case m.State == MeasureMissed:
				out = append(out, m.Name+": "+m.Value+", target "+m.Target)
			case m.State == MeasureUnknown && !r.Partial && m.Name != reproduceMeasure:
				out = append(out, m.Name+": not measured ("+orDash(m.Note)+"), target "+m.Target)
			}
		}
	}
	return out
}

// fenceSettings writes an empty .echo/config.yaml at the top of work.
// rhythm-echo looks for its settings and store in the nearest .echo up
// from where it runs, as git finds .git; the default work folder sits
// under testbed/, whose .echo belongs to Echo's own testbed, so without
// the fence every run read that config and filled that store.
func fenceSettings(work string) error {
	dir := filepath.Join(work, ".echo")
	if err := os.MkdirAll(dir, 0o750); err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(dir, "config.yaml"), []byte(fenceConfig), 0o600)
}

// fenceConfig is the fence's whole config: defaults only.
const fenceConfig = "# The consent benchmark's own settings: Echo's defaults. This file stops\n" +
	"# rhythm-echo from reading a .echo above the benchmark's work folder.\n"
