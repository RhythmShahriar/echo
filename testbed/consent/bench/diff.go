// Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
// SPDX-License-Identifier: MIT

package main

import (
	"fmt"
	"io"
	"maps"
	"slices"
	"strings"
)

// outcomes is what must come out the same in two runs of the benchmark,
// by ID: each scenario's outcome for Echo and each tool, each control's
// counts and each probe's outcome. Timings, dates and the wording of
// details may differ.
func outcomes(r *Results) map[string]string {
	out := map[string]string{}
	for _, o := range r.Scenarios {
		parts := []string{"echo " + o.Echo.Outcome}
		if o.GoodState != nil {
			parts = append(parts, "good state "+o.GoodState.Outcome)
		}
		for _, name := range slices.Sorted(maps.Keys(o.Tools)) {
			parts = append(parts, name+" "+o.Tools[name].Outcome)
		}
		out[o.Scenario.ID] = strings.Join(parts, ", ")
	}
	for _, c := range r.Control {
		out["control "+c.ID] = fmt.Sprintf("%d false alarms, %d flakes, %d failed", c.FalseAlarms, c.Flakes, c.Failed)
	}
	for _, e := range r.Environment {
		out[e.Probe.ID] = "echo " + e.Echo.Outcome
	}
	return out
}

// diffResults compares the outcomes in two results.json files, writes
// each difference to w and reports whether there were none. A file with
// no outcomes is an error: two empty or aborted runs prove nothing.
func diffResults(a, b string, w io.Writer) (bool, error) {
	ra, err := readResults(a)
	if err != nil {
		return false, err
	}
	rb, err := readResults(b)
	if err != nil {
		return false, err
	}
	oa, ob := outcomes(ra), outcomes(rb)
	for _, f := range []struct {
		path string
		n    int
	}{{a, len(oa)}, {b, len(ob)}} {
		if f.n == 0 {
			return false, fmt.Errorf("%s: no outcomes; was the run aborted?", f.path)
		}
	}
	ids := slices.Sorted(maps.Keys(oa))
	for id := range ob {
		if _, ok := oa[id]; !ok {
			ids = append(ids, id)
		}
	}
	slices.Sort(ids)
	same := true
	for _, id := range ids {
		if oa[id] != ob[id] {
			same = false
			fmt.Fprintf(w, "%s: %s | %s\n", id, orDash(oa[id]), orDash(ob[id]))
		}
	}
	if same {
		fmt.Fprintf(w, "%d outcomes, the same in both runs\n", len(ids))
	}
	return same, nil
}

// orDash is s, or "-" for an outcome only one run has.
func orDash(s string) string {
	if s == "" {
		return "-"
	}
	return s
}
