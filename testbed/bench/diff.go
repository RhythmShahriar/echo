// Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
// SPDX-License-Identifier: MIT

//go:build testbed

package main

import (
	"encoding/json"
	"fmt"
	"io"
	"maps"
	"os"
	"slices"
	"strings"
)

// outcomes is what must come out the same in two runs of the benchmark:
// each scenario's result for Echo and each tool, by scenario ID. Timings,
// dates and the wording of details may differ.
func outcomes(r Results) map[string]string {
	out := map[string]string{}
	for _, o := range r.Sites {
		parts := []string{"echo " + o.Echo.Outcome}
		for _, name := range slices.Sorted(maps.Keys(o.Tools)) {
			parts = append(parts, name+" "+o.Tools[name].Outcome)
		}
		out[o.Scenario.ID] = strings.Join(parts, ", ")
	}
	for _, o := range r.Environment {
		out[o.Scenario.ID] = "echo " + o.Echo.Outcome
	}
	return out
}

// diffResults compares the outcomes in two results.json files, writes
// each difference to w and reports whether there were none. A file with
// no site or environment outcomes is an error: two empty or aborted runs
// prove nothing.
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
			return false, fmt.Errorf("%s: no scenario outcomes; was the run aborted?", f.path)
		}
	}
	ids := slices.Sorted(maps.Keys(oa))
	for id := range ob {
		if !slices.Contains(ids, id) {
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
		fmt.Fprintf(w, "%d scenarios, the same outcomes in both runs\n", len(ids))
	}
	return same, nil
}

func readResults(path string) (Results, error) {
	var r Results
	data, err := os.ReadFile(path) //nolint:gosec // a results.json the user names
	if err != nil {
		return r, err
	}
	if err := json.Unmarshal(data, &r); err != nil {
		return r, fmt.Errorf("%s: %w", path, err)
	}
	return r, nil
}
