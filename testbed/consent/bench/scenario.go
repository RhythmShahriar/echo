// Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
// SPDX-License-Identifier: MIT

package main

import (
	"context"
	"fmt"
	"os"
	"path"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/RhythmShahriar/echo/consent"
	"github.com/RhythmShahriar/echo/testbed/consent/scenarios"
	"github.com/RhythmShahriar/echo/testbed/consent/server"
)

// Bench runs scenarios against a Bed and collects what every run left
// for the timing, click and memory tables.
type Bench struct {
	bed     Bed
	work    string        // the work folder on this machine
	timeout time.Duration // per rhythm-echo command
	log     func(string)
	tools   *Tools // the other tools; nil judges none

	mu      sync.Mutex
	states  map[string]*sync.Mutex // one per state file: a scenario and its Docker variant share one
	pages   []time.Duration        // per page, from each report
	clicks  map[string]*ClickRow
	rssMax  int64
	leaks   int
	browser string
}

// newBench returns a Bench over bed with its work folder.
func newBench(bed Bed, work string, timeout time.Duration, log func(string)) *Bench {
	if log == nil {
		log = func(string) {}
	}
	return &Bench{bed: bed, work: work, timeout: timeout, log: log, clicks: map[string]*ClickRow{}, states: map[string]*sync.Mutex{}}
}

// siteURL is a scenario's start page.
func siteURL(id string) string { return "https://" + id + server.SiteSuffix + "/" }

// againstName is the baseline a comparison scenario's args name, or "".
func againstName(args []string) string {
	for i, a := range args {
		if a == "--against" && i+1 < len(args) {
			return args[i+1]
		}
		if v, ok := strings.CutPrefix(a, "--against="); ok {
			return v
		}
	}
	return ""
}

// stateArgs are a scenario's own flags for one state. The good state
// of a comparison scenario saves the baseline its broken state is
// compared with, so it drops --against (no baseline exists yet) and adds
// --save. {echo} becomes echo; ok is false when the scenario needs an
// Echo and none runs.
func stateArgs(s scenarios.Scenario, broken bool, echo string) (args []string, ok bool) {
	base := againstName(s.Args)
	for i := 0; i < len(s.Args); i++ {
		a := s.Args[i]
		if !broken && base != "" {
			if a == "--against" {
				i++
				continue
			}
			if strings.HasPrefix(a, "--against=") {
				continue
			}
		}
		if strings.Contains(a, "{echo}") {
			if echo == "" {
				return nil, false
			}
			a = strings.ReplaceAll(a, "{echo}", echo)
		}
		args = append(args, a)
	}
	if !broken && base != "" {
		args = append(args, "--save", base)
	}
	if len(s.Runs) > 0 {
		args = append(args, "--runs", strings.Join(s.Runs, ","))
	}
	return args, true
}

// job is one thing the benchmark checks: a scenario's expectations on
// a site. Site is the host label (c01, or c01-wp for a Docker variant
// on the real plugin) and the sink's scenario; State is the scenario
// whose state file switches it. Only a site the Go server renders (Site
// equal to State) has its stand-ins listed.
type job struct {
	S     scenarios.Scenario
	Site  string
	State string
}

// jobOf is the job of a scenario on its own site.
func jobOf(s scenarios.Scenario) job { return job{S: s, Site: s.ID, State: s.ID} }

// variantJob is the job of a Docker variant: the base scenario's
// expectations on the variant's site.
func variantJob(base scenarios.Scenario, v scenarios.Variant) job {
	s := base
	s.ID, s.CMP, s.Needs = v.ID, v.CMP, v.Needs
	s.Name = base.Name + " (" + v.CMP + ")"
	return job{S: s, Site: v.Site, State: v.Of}
}

// check runs rhythm-echo consent on j in one state, in the work
// subfolder rel; it returns what it left. It switches the state, clears
// the sink, lists the page's stand-ins, runs and reads the sink.
func (b *Bench) check(ctx context.Context, j job, broken bool, rel string) (Observed, Exec) {
	s := j.S
	if err := b.bed.SetBroken(j.State, broken); err != nil {
		return Observed{Exit: -1, Output: "switching the state: " + err.Error()}, Exec{}
	}
	if err := b.bed.ResetSink(j.Site); err != nil {
		return Observed{Exit: -1, Output: "clearing the sink: " + err.Error()}, Exec{}
	}
	var want []string
	if j.Site == j.State {
		var err error
		if want, err = pageStandIns(b.bed.Render(), j.Site); err != nil {
			return Observed{Exit: -1, Output: err.Error()}, Exec{}
		}
	}
	args, ok := stateArgs(s, broken, b.bed.EchoURL())
	if !ok {
		return Observed{Exit: -1, Output: "needs an Echo for --echo and none runs"}, Exec{}
	}
	if err := os.MkdirAll(filepath.Join(b.work, filepath.FromSlash(rel)), 0o750); err != nil {
		return Observed{Exit: -1, Output: err.Error()}, Exec{}
	}
	report := "result.json"
	args = slices.Concat([]string{"consent", siteURL(j.Site), "--json", "-o", report, "--fail-on", "none"}, args)
	ctx, cancel := context.WithTimeout(ctx, b.timeout)
	defer cancel()
	ex := run(b.bed.Command(ctx, rel, args))
	o := Observed{Exit: ex.Exit, Output: ex.Stderr}
	if env, err := readEnvelope(filepath.Join(b.work, filepath.FromSlash(rel), report)); err == nil {
		o.Env = env
	} else if ex.Exit >= 0 {
		o.Output += "\nbench: " + err.Error()
	}
	if c, err := b.bed.Count(j.Site); err == nil {
		o.Sink = c
	} else {
		o.Output += "\nbench: reading the sink: " + err.Error()
	}
	o.Missing = missingStandIns(want, o.Env)
	b.record(s, o, ex, broken)
	return o, ex
}

// record adds a run to the timing, click, memory and leak tallies.
// Clicks count on good states only: a broken state may break a click on
// purpose (c20, c21).
func (b *Bench) record(s scenarios.Scenario, o Observed, ex Exec, broken bool) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.rssMax = max(b.rssMax, ex.MaxRSS)
	if s.WantSinkZero() {
		b.leaks += o.Sink.Hits
	}
	if o.Env == nil {
		return
	}
	if b.browser == "" {
		b.browser = o.Env.Data.Browser
	}
	if n := len(o.Env.Data.Pages); n > 0 && o.Env.Data.DurationMS > 0 {
		b.pages = append(b.pages, time.Duration(o.Env.Data.DurationMS/int64(n))*time.Millisecond)
	}
	if s.CMP == "none" || broken {
		return
	}
	row := b.clicks[s.CMP]
	if row == nil {
		row = &ClickRow{CMP: s.CMP}
		b.clicks[s.CMP] = row
	}
	o.Env.runs(func(_ consent.Page, r consent.Run) {
		count := func(c *consent.Choice, accept bool) {
			if accept {
				row.AcceptNeeded++
				if c != nil && c.Verified {
					row.AcceptOK++
				}
				return
			}
			row.RejectNeeded++
			if c != nil && c.Verified {
				row.RejectOK++
			}
		}
		switch r.Kind {
		case consent.RunAccept:
			count(r.Choice, true)
		case consent.RunReject:
			count(r.Choice, false)
		case consent.RunWithdraw:
			count(r.Choice, true)
			count(r.Withdraw, false)
		}
	})
}

// runScenario runs one scenario: a bad one's good state (which must be
// clean) then its broken state, judged against expect and forbid; a
// good one's only state. It leaves the scenario good.
func (b *Bench) runScenario(ctx context.Context, j job) Outcome {
	s := j.S
	unlock := b.lockState(j.State)
	defer unlock()
	start := time.Now()
	out := Outcome{Scenario: s, Tools: map[string]Verdict{}}
	defer func() { _ = b.bed.SetBroken(j.State, false) }()
	good, _ := b.check(ctx, j, false, path.Join(s.ID, "good"))
	gv := judgeGood(s, good)
	if s.Good() {
		out.Echo = gv
		b.judgeTools(ctx, j, &out)
		out.Seconds = seconds(time.Since(start))
		b.log(fmt.Sprintf("%s %s %s", s.ID, gv.Outcome, gv.Detail))
		return out
	}
	out.GoodState = &gv
	broken, _ := b.check(ctx, j, true, path.Join(s.ID, "broken"))
	out.Echo = judgeBad(s, broken)
	b.judgeTools(ctx, j, &out)
	if out.Echo.Outcome == Caught && gv.Outcome != Clean {
		// A scenario whose good state already fails proves nothing about
		// the bug the broken state adds.
		out.Echo = Verdict{Outcome: Failed, Detail: "good state " + gv.Outcome + ": " + gv.Detail}
	}
	out.Seconds = seconds(time.Since(start))
	b.log(fmt.Sprintf("%s %s %s", s.ID, out.Echo.Outcome, out.Echo.Detail))
	return out
}

// judgeTools runs the other tools on j in the state it is in now (the
// state Echo was just judged on).
func (b *Bench) judgeTools(ctx context.Context, j job, out *Outcome) {
	if b.tools == nil || b.tools.wec == nil || j.Site != j.State {
		return
	}
	out.Tools[toolWEC] = b.tools.wec.judge(ctx, j.S)
}

// lockState holds state file id for one scenario's run, so a scenario
// and its Docker variant never switch it under each other.
func (b *Bench) lockState(id string) (unlock func()) {
	b.mu.Lock()
	m := b.states[id]
	if m == nil {
		m = &sync.Mutex{}
		b.states[id] = m
	}
	b.mu.Unlock()
	m.Lock()
	return m.Unlock
}
