// Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
// SPDX-License-Identifier: MIT

package main

import (
	"strings"
	"testing"
	"time"

	"github.com/RhythmShahriar/echo/consent"
	"github.com/RhythmShahriar/echo/model"
	"github.com/RhythmShahriar/echo/testbed/consent/judge"
	"github.com/RhythmShahriar/echo/testbed/consent/scenarios"
)

// c02 is the manifest's c02 as the judge sees it: three expects and a
// forbid.
func TestPercentile(t *testing.T) {
	s := func(n ...int) []time.Duration {
		var out []time.Duration
		for _, x := range n {
			out = append(out, time.Duration(x)*time.Second)
		}
		return out
	}
	for _, c := range []struct {
		xs   []time.Duration
		p    float64
		want time.Duration
	}{
		{nil, 50, 0},
		{s(7), 95, 7 * time.Second},
		{s(5, 1, 3, 2, 4), 50, 3 * time.Second},
		{s(1, 2, 3, 4), 50, 2 * time.Second},
		{s(1, 2, 3, 4, 5, 6, 7, 8, 9, 10, 11, 12, 13, 14, 15, 16, 17, 18, 19, 20), 95, 19 * time.Second},
		{s(1, 2, 3, 4, 5, 6, 7, 8, 9, 10, 11, 12, 13, 14, 15, 16, 17, 18, 19, 20), 100, 20 * time.Second},
	} {
		if got := percentile(c.xs, c.p); got != c.want {
			t.Errorf("percentile(%v, %v) = %v, want %v", c.xs, c.p, got, c.want)
		}
	}
	if got := maxDuration(s(3, 9, 2)); got != 9*time.Second {
		t.Errorf("max = %v", got)
	}
}

func TestControlFlakes(t *testing.T) {
	var c Control
	first := ""
	c.add(1, judge.Verdict{Outcome: judge.Clean}, "ok []", &first)
	c.add(2, judge.Verdict{Outcome: judge.Clean}, "ok []", &first)
	c.add(3, judge.Verdict{Outcome: judge.FalseAlarm, Detail: "warning x"}, "findings [x]", &first)
	c.add(4, judge.Verdict{Outcome: judge.Failed, Detail: "inconclusive"}, "error []", &first)
	c.Rounds = 4
	if c.FalseAlarms != 1 || c.Failed != 1 || c.Flakes != 2 {
		t.Errorf("control = %+v", c)
	}
	if !strings.Contains(strings.Join(c.Details, "\n"), "round 3 differs from round 1: ok [], then findings [x]") {
		t.Errorf("details = %q", c.Details)
	}
	sig := judge.Signature(judge.Observed{Env: fakeEnvelope(true, []consent.Issue{issue("b", "", model.Notice, ""), issue("a", "", model.Notice, "")})})
	if sig != "findings [a|/|none|, b|/|none|]" {
		t.Errorf("signature = %q", sig)
	}
	if sig := judge.Signature(judge.Observed{Exit: 3}); sig != "no report, exit 3" {
		t.Errorf("signature of nothing = %q", sig)
	}
}

func TestStateArgs(t *testing.T) {
	c08 := scenarios.Scenario{ID: "c08", Args: []string{"--against", "good"}}
	if got, _ := stateArgs(c08, false, ""); strings.Join(got, " ") != "--save good" {
		t.Errorf("c08 good = %q", got)
	}
	if got, _ := stateArgs(c08, true, ""); strings.Join(got, " ") != "--against good" {
		t.Errorf("c08 broken = %q", got)
	}
	c18 := scenarios.Scenario{ID: "c18", Args: []string{"--echo", "{echo}"}, Runs: []string{"+gpc", "+withdraw"}}
	if _, ok := stateArgs(c18, true, ""); ok {
		t.Error("c18 without an Echo: ok")
	}
	if got, ok := stateArgs(c18, true, "http://127.0.0.1:1"); !ok || strings.Join(got, " ") != "--echo http://127.0.0.1:1 --runs +gpc,+withdraw" {
		t.Errorf("c18 = %q", got)
	}
}
