// Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
// SPDX-License-Identifier: MIT

package verdict

import (
	"bytes"
	"encoding/json"
	"flag"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/RhythmShahriar/echo/consent"
	"github.com/RhythmShahriar/echo/consent/consenttest"
)

var updateGoldens = flag.Bool("update", false, "rewrite consent/consenttest/testdata from the verdict's output")

// goldenNow is the day the consenttest goldens are judged on.
var goldenNow = time.Date(2026, 10, 26, 9, 30, 0, 0, time.UTC)

// judgedGolden judges a consenttest golden as a check would: under its
// own policy, geo and hold, compare-cur against compare-base.
func judgedGolden(t *testing.T, name string) *consent.Result {
	t.Helper()
	judge := func(n string) *consent.Result {
		r := consenttest.GoldenResult(n)
		p := consenttest.NewPlan(t, r.Pages[0].URL)
		p.Policy, p.Geo, p.Hold = r.Policy, r.Geo, r.Held
		Evaluator{NowFunc: func() time.Time { return goldenNow }}.Evaluate(r, p)
		return r
	}
	r := judge(name)
	if name == "compare-cur" {
		r.Comparison = Comparer{}.Compare(judge("compare-base"), r, nil)
		r.Comparison.Against = "baseline"
	}
	return r
}

// TestConsenttestGoldensAreJudged: the results every lane's tests start
// from (consenttest.GoldenResult) hold what the verdict writes for them,
// issues, inconclusive runs, notes and comparison word for word, so a
// writer or CLI test never asserts a text Echo doesn't print. -update
// rewrites them.
func TestConsenttestGoldensAreJudged(t *testing.T) {
	for _, name := range consenttest.Goldens {
		r := judgedGolden(t, name)
		var buf bytes.Buffer
		enc := json.NewEncoder(&buf)
		enc.SetIndent("", "  ")
		enc.SetEscapeHTML(false)
		if err := enc.Encode(r); err != nil {
			t.Fatal(err)
		}
		path := filepath.Join("..", "consenttest", "testdata", name+".json")
		if *updateGoldens {
			if err := os.WriteFile(path, buf.Bytes(), 0o600); err != nil {
				t.Fatal(err)
			}
		}
		want, err := os.ReadFile(path) //nolint:gosec // test fixture
		if err != nil {
			t.Fatal(err)
		}
		if strings.ReplaceAll(string(want), "\r\n", "\n") != buf.String() {
			t.Errorf("%s: the golden isn't what the verdict writes (go test ./consent/verdict -run TestConsenttestGoldensAreJudged -update)", name)
		}
	}
}
