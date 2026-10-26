// Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
// SPDX-License-Identifier: MIT

package main

import (
	"context"
	"encoding/json"
	"fmt"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/RhythmShahriar/echo/testbed/consent/judge"
	"github.com/RhythmShahriar/echo/testbed/consent/scenarios"
	"github.com/RhythmShahriar/echo/testbed/consent/server"
)

// DefaultProbePort is the first of the local probes' ports: their own
// testbed server, admin listener and fixture servers sit on
// 127.0.0.1:23410-23439, apart from Go mode's 23400 and 23401.
const DefaultProbePort = 23410

// The local probes' ports, as offsets from ProbeEnv.Port.
const (
	portSite    = 0 // the probe's testbed server
	portAdmin   = 1 // its admin listener (the ssrf-probe target)
	portBotWall = 2 // e03's bot wall
	portTLS     = 3 // e12's self-signed site
)

// ProbeEnv is what a probe works with.
type ProbeEnv struct {
	Bin     string        // the rhythm-echo under test
	Root    string        // the testbed/consent folder
	Work    string        // a fresh folder for this probe and state
	Timeout time.Duration // per command
	Port    int           // the first local port, DefaultProbePort
	// Bed is the Docker testbed for probes that need it; nil in Go mode.
	Bed Bed
}

// Step is what one step of a probe saw: the command's exit code, the
// verdict and completeness of its JSON report and everything it printed,
// plus the notes the probe added (such as "chrome left: 0").
type Step struct {
	Name     string `json:"step"`
	Exit     int    `json:"exit"`
	Verdict  string `json:"verdict,omitempty"`
	Complete *bool  `json:"complete,omitempty"`
	Output   string `json:"output"`
	env      *judge.Envelope
}

// note adds a line to what the step shows to says and never.
func (s *Step) note(format string, args ...any) {
	s.Output += "\n" + fmt.Sprintf(format, args...)
}

// ProbeFunc sets up one state of an environment probe, runs its steps
// and returns what each saw. An error means the probe itself couldn't
// run (a fixture didn't start), not that Echo got it wrong.
type ProbeFunc func(ctx context.Context, p *ProbeEnv, broken bool) ([]Step, error)

// probeFuncs are the probes by the name scenarios.yaml gives them.
var probeFuncs = map[string]ProbeFunc{}

// ProbeOutcome is everything recorded for one environment probe.
type ProbeOutcome struct {
	Probe   scenarios.Probe `json:"probe"`
	Echo    judge.Verdict   `json:"echo"`
	Good    []Step          `json:"good,omitempty"`
	Broken  []Step          `json:"broken,omitempty"`
	Seconds float64         `json:"seconds"`
}

// forState returns p for one state of probe id, in a new empty folder.
func (p *ProbeEnv) forState(id, state string) *ProbeEnv {
	q := *p
	q.Work = filepath.Join(p.Work, id, state)
	_ = os.RemoveAll(q.Work)
	_ = os.MkdirAll(q.Work, 0o750)
	return &q
}

// addr is the local address at offset off from the first probe port.
func (p *ProbeEnv) addr(off int) string { return fmt.Sprintf("127.0.0.1:%d", p.Port+off) }

// env is the environment every rhythm-echo step gets: no update check
// and the CI behavior (no tips, no prompts, no color).
func (p *ProbeEnv) env(extra ...string) []string {
	return append(os.Environ(), append([]string{"RHYTHM_OFFLINE=1", "CI=true", "NO_COLOR=1"}, extra...)...)
}

// localSite is a probe's own testbed server over a private copy of the
// sites, so its states never touch the benchmark's state folder.
type localSite struct {
	srv   *server.Server
	addr  string
	sites net.Listener
	admin net.Listener
}

// site starts the probe's testbed server on its ports.
func (p *ProbeEnv) site() (*localSite, error) {
	root := filepath.Join(p.Work, "testbed")
	if err := os.CopyFS(filepath.Join(root, "sites"), os.DirFS(filepath.Join(p.Root, "sites"))); err != nil {
		return nil, fmt.Errorf("copying the sites: %w", err)
	}
	srv, err := server.New(server.Options{Root: root, Admin: p.addr(portAdmin)})
	if err != nil {
		return nil, err
	}
	sl, err := net.Listen("tcp", p.addr(portSite))
	if err != nil {
		return nil, err
	}
	al, err := net.Listen("tcp", p.addr(portAdmin))
	if err != nil {
		_ = sl.Close()
		return nil, err
	}
	go func() { _ = srv.Serve(sl) }()
	go func() { _ = srv.ServeAdmin(al) }()
	return &localSite{srv: srv, addr: sl.Addr().String(), sites: sl, admin: al}, nil
}

// stop closes the site's listeners; requests to its address are then
// refused, as a site that is down.
func (l *localSite) stop() {
	_ = l.sites.Close()
	_ = l.admin.Close()
}

// mapArgs sends every testbed name to the site.
func (l *localSite) mapArgs() []string { return server.MapArgs(l.addr) }

// consent runs rhythm-echo consent with args in the probe's folder as
// the named step, with --json -o <step>.json and the site's map flags.
func (p *ProbeEnv) consent(ctx context.Context, name string, site *localSite, env []string, args ...string) Step {
	full := append([]string{"consent"}, args...)
	if site != nil {
		full = append(full, site.mapArgs()...)
	}
	return p.step(ctx, name, env, full...)
}

// step runs rhythm-echo with args and --json -o <step>.json in the
// probe's folder; the output is what it printed plus the JSON report.
func (p *ProbeEnv) step(ctx context.Context, name string, env []string, args ...string) Step {
	ctx, cancel := context.WithTimeout(ctx, p.Timeout)
	defer cancel()
	cmd := p.command(ctx, name, env, args...)
	return p.finish(name, run(cmd))
}

// command is the rhythm-echo command of step name.
func (p *ProbeEnv) command(ctx context.Context, name string, env []string, args ...string) *exec.Cmd {
	file := stepFile(name)
	_ = os.Remove(filepath.Join(p.Work, file))
	if !slices.Contains(args, "--json") {
		args = append(args, "--json", "-o", file)
	}
	cmd := exec.CommandContext(ctx, p.Bin, args...) //nolint:gosec // the binary under test
	cmd.Dir = p.Work
	if env == nil {
		env = p.env()
	}
	cmd.Env = env
	return cmd
}

// stepFile is the JSON report a step writes.
func stepFile(name string) string { return strings.ReplaceAll(name, " ", "-") + ".json" }

// finish reads the step's report into its Step.
func (p *ProbeEnv) finish(name string, ex Exec) Step {
	s := Step{Name: name, Exit: ex.Exit, Output: ex.Output()}
	if ex.MaxRSS >= targetRSS {
		s.note("rss over 200 MB: %s", mib(ex.MaxRSS))
	}
	data, err := os.ReadFile(filepath.Join(p.Work, stepFile(name))) //nolint:gosec // the probe's own report
	if err != nil {
		return s
	}
	s.Output += "\n" + string(data)
	if e, err := judge.ParseEnvelope(data); err == nil {
		s.env = e
		s.Verdict = e.Verdict
		c := e.Complete
		s.Complete = &c
	} else {
		var other struct {
			Verdict string `json:"verdict"`
		}
		if json.Unmarshal(data, &other) == nil {
			s.Verdict = other.Verdict
		}
	}
	return s
}

// verifyProbe reports the first check in checks that steps fail, or "".
func verifyProbe(checks []scenarios.Check, steps []Step) string {
	for _, c := range checks {
		i := slices.IndexFunc(steps, func(s Step) bool { return s.Name == c.Step })
		if i < 0 {
			return c.Step + ": the probe has no such step"
		}
		if why := checkFailed(c, steps[i]); why != "" {
			return c.Step + ": " + why
		}
	}
	return ""
}

// checkFailed says how s falls short of c, or "" when it doesn't.
func checkFailed(c scenarios.Check, s Step) string {
	out := strings.ToLower(s.Output)
	switch {
	case c.Exit != nil && s.Exit != *c.Exit:
		return fmt.Sprintf("exit %d, want %d (%s)", s.Exit, *c.Exit, judge.Tail(s.Output, 200))
	case c.Verdict != "" && s.Verdict != c.Verdict:
		return fmt.Sprintf("verdict %q, want %q", s.Verdict, c.Verdict)
	case c.Complete != nil && (s.Complete == nil || *s.Complete != *c.Complete):
		got := "no report"
		if s.Complete != nil {
			got = fmt.Sprint(*s.Complete)
		}
		return fmt.Sprintf("complete %s, want %v", got, *c.Complete)
	}
	for _, w := range c.Says {
		if !strings.Contains(out, strings.ToLower(w)) {
			return fmt.Sprintf("doesn't say %q", w)
		}
	}
	for _, w := range c.Never {
		if strings.Contains(out, strings.ToLower(w)) {
			return fmt.Sprintf("says %q", w)
		}
	}
	return ""
}

// evaluateProbe judges a probe from what it saw in each state. A good
// state that fails its checks fails the probe: it can't tell anything
// then. A broken state that passes every check is caught.
func evaluateProbe(pr scenarios.Probe, good, broken []Step) judge.Verdict {
	if why := verifyProbe(pr.Good, good); why != "" {
		return judge.Verdict{Outcome: judge.Failed, Detail: "good state: " + why}
	}
	if why := verifyProbe(pr.Broken, broken); why != "" {
		return judge.Verdict{Outcome: judge.Missed, Detail: why}
	}
	var seen []string
	for _, c := range pr.Broken {
		seen = append(seen, c.String())
	}
	return judge.Verdict{Outcome: judge.Caught, Found: strings.Join(seen, "; ")}
}

// runProbe runs one probe: the good state, then the broken.
func runProbe(ctx context.Context, p *ProbeEnv, pr scenarios.Probe) ProbeOutcome {
	start := time.Now()
	out := ProbeOutcome{Probe: pr}
	defer func() { out.Seconds = seconds(time.Since(start)) }()
	f := probeFuncs[pr.Probe]
	switch {
	case f == nil:
		out.Echo = judge.Verdict{Outcome: judge.Failed, Detail: "no probe named " + pr.Probe}
		return out
	case pr.Needs == "docker" && p.Bed == nil:
		out.Echo = judge.Verdict{Outcome: judge.NotRun, Detail: "needs the sealed Docker testbed (-mode docker)"}
		return out
	}
	var err error
	if out.Good, err = f(ctx, p.forState(pr.ID, "good"), false); err != nil {
		out.Echo = judge.Verdict{Outcome: judge.Failed, Detail: "good state: " + err.Error()}
		return out
	}
	if out.Broken, err = f(ctx, p.forState(pr.ID, "broken"), true); err != nil {
		out.Echo = judge.Verdict{Outcome: judge.Failed, Detail: "broken state: " + err.Error()}
		return out
	}
	out.Echo = evaluateProbe(pr, out.Good, out.Broken)
	return out
}
