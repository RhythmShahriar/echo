// Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
// SPDX-License-Identifier: MIT

package report

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/RhythmShahriar/echo/consent"
	"github.com/RhythmShahriar/echo/consent/consenttest"
	consentverdict "github.com/RhythmShahriar/echo/consent/verdict"
)

// consentNow is the day the consent writer goldens are judged on.
var consentNow = time.Date(2026, 10, 26, 9, 30, 0, 0, time.UTC)

// judgedResult loads a consenttest golden and judges it with the real
// verdict, so the writers' goldens show what Echo prints rather than
// the skeleton's guesses. compare-cur gets its comparison from the real
// Comparer against compare-base, under the name "baseline".
func judgedResult(t testing.TB, name string) *consent.Result {
	t.Helper()
	judge := func(n string) *consent.Result {
		r := consenttest.GoldenResult(n)
		p := consenttest.NewPlan(t, r.Pages[0].URL)
		p.Policy, p.Geo, p.Hold = r.Policy, r.Geo, r.Held
		consentverdict.Evaluator{NowFunc: func() time.Time { return consentNow }}.Evaluate(r, p)
		r.Complete = len(r.Inconclusive) == 0 && r.Summary().InconclusiveRuns == 0
		return r
	}
	r := judge(name)
	if name == "compare-cur" {
		r.Comparison = consentverdict.Comparer{}.Compare(judge("compare-base"), r, nil)
		r.Comparison.Against = "baseline"
	}
	return r
}

// golden compares got with testdata/consent/<name>, rewriting it with
// -update. Line ends are normalised, so a checkout with CRLF passes.
func golden(t *testing.T, name, got string) {
	t.Helper()
	path := filepath.Join("testdata", "consent", name)
	if *update {
		if err := os.MkdirAll(filepath.Dir(path), 0o750); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(got), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	want, err := os.ReadFile(path) //nolint:gosec // test fixture
	if err != nil {
		t.Fatalf("%v (go test ./report -run %s -update writes it)", err, t.Name())
	}
	norm := func(s string) string { return strings.ReplaceAll(s, "\r\n", "\n") }
	if norm(got) != norm(string(want)) {
		t.Errorf("%s changed (go test ./report -run %s -update to accept):\n%s", path, t.Name(), got)
	}
}
