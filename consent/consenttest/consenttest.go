// Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
// SPDX-License-Identifier: MIT

// Package consenttest holds in-memory stand-ins for every consent stage,
// golden results and the helpers lane tests use to get another stage
// without importing its package: Need skips a test until that stage is
// linked, Use registers fakes for one test.
//
// Tests that call Use (or consent.Swap) change process-wide state, so
// they must not call t.Parallel.
package consenttest

import (
	"embed"
	"encoding/json"
	"fmt"
	"net/url"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/RhythmShahriar/echo/consent"
)

// RequireLinkedEnv names the variable that, set to 1, turns every "not
// linked yet" skip into a failure: make test-integrated, the nightly CI and the release run with
// it, so a stage missing from the build can't pass unnoticed.
const RequireLinkedEnv = "ECHO_CONSENT_REQUIRE_LINKED"

// Need skips t with "<stage> not linked yet" while a stage it needs is
// missing from this build, or fails it when RequireLinkedEnv is 1.
func Need(t testing.TB, stages ...consent.Stage) {
	t.Helper()
	l := consent.Linked()
	for _, s := range stages {
		if linked(l, s) {
			continue
		}
		if os.Getenv(RequireLinkedEnv) == "1" {
			t.Fatalf("%s not linked into this build (%s=1)", s, RequireLinkedEnv)
		}
		t.Skipf("%s not linked yet", s)
	}
}

// linked reports whether stage s is set in l, the way consent.Need
// reads it.
func linked(l consent.Stages, s consent.Stage) bool {
	switch s {
	case consent.StageClassifier:
		return l.Classifier != nil
	case consent.StageSignals:
		return l.Signals != nil
	case consent.StageState:
		return l.State != nil
	case consent.StageCMP:
		return l.CMP != nil
	case consent.StageRunner:
		return l.Runner != nil
	case consent.StageEvaluator:
		return l.Evaluator != nil
	case consent.StageComparer:
		return l.Comparer != nil
	}
	return false
}

// Use registers s (fakes, or a lane's own stage beside fakes) for the
// rest of t and restores the registry when t ends. Nil fields keep the
// stage already registered. A test that calls Use must not run in
// parallel.
func Use(t testing.TB, s consent.Stages) {
	t.Helper()
	restore := consent.Swap(s)
	t.Cleanup(restore)
}

// Fakes returns a fake for every stage: the set a test passes to Use
// when it needs the whole pipeline in memory.
func Fakes() consent.Stages {
	return consent.Stages{
		Classifier: &FakeClassifier{},
		Signals:    &FakeSignals{},
		State:      &FakeState{},
		CMP:        &FakeCMP{},
		Runner:     &FakeRunner{Result: GoldenResult("clean")},
		Evaluator:  &FakeEvaluator{},
		Comparer:   &FakeComparer{},
	}
}

//go:embed testdata/*.json
var goldens embed.FS

// Goldens names every golden result GoldenResult loads.
var Goldens = []string{"clean", "meta-ignores", "advanced", "inconclusive", "no-trackers", "compare-base", "compare-cur"}

// GoldenResult loads testdata/<name>.json, a fresh copy on every call:
//
//   - clean: a g01-like site, every run ok and no issue
//   - meta-ignores: Meta Pixel ignores Consent Mode (product.md 9.2)
//   - advanced: Consent Mode advanced, GA4 cookieless pings before the choice
//   - inconclusive: a CMP on the page, no banner, Accept and Reject no_banner
//   - no-trackers: a brochure site with nothing to consent to (g12)
//   - compare-base and compare-cur: TikTok added after a plugin update
//     (c08); compare-cur holds the expected Comparison
//
// Each is judged: issues, inconclusive runs, Consent Mode and NeedsNone
// are what the verdict stage should produce for it, so the verdict lane
// can clear them and check it gets the same; the writers get real
// input from day one. Events are not part of the files (they live in
// NDJSON). An unknown name or a file that doesn't decode into the core
// types panics: these files are the contract.
func GoldenResult(name string) *consent.Result {
	b, err := goldens.ReadFile("testdata/" + name + ".json")
	if err != nil {
		panic(fmt.Sprintf("consenttest: no golden result %q (have %s)", name, strings.Join(Goldens, ", ")))
	}
	var r consent.Result
	dec := json.NewDecoder(strings.NewReader(string(b)))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&r); err != nil {
		panic(fmt.Sprintf("consenttest: golden result %q: %v", name, err))
	}
	r.Complete = true
	for _, p := range r.Pages {
		for _, run := range p.Runs {
			if run.Inconclusive() && (!p.NeedsNone || run.Status != consent.StatusNoBanner) {
				r.Complete = false
			}
		}
	}
	if len(r.Inconclusive) > 0 {
		r.Complete = false
	}
	return &r
}

// CloneResult deep-copies r, events included, so a fake can hand out
// the same result to many tests without one changing another's.
func CloneResult(r *consent.Result) *consent.Result {
	if r == nil {
		return nil
	}
	b, err := json.Marshal(r)
	if err != nil {
		panic(fmt.Sprintf("consenttest: clone: %v", err))
	}
	var out consent.Result
	if err := json.Unmarshal(b, &out); err != nil {
		panic(fmt.Sprintf("consenttest: clone: %v", err))
	}
	out.Complete = r.Complete
	for i, p := range r.Pages {
		for j, run := range p.Runs {
			if run.Events != nil {
				ev, _ := json.Marshal(run.Events)
				_ = json.Unmarshal(ev, &out.Pages[i].Runs[j].Events)
			}
		}
	}
	return &out
}

// NewPlan returns a plan that passes Validate, with the CLI's defaults,
// for the given URLs (https://shop.test/ when none).
func NewPlan(t testing.TB, urls ...string) *consent.Plan {
	t.Helper()
	if len(urls) == 0 {
		urls = []string{"https://shop.test/"}
	}
	p := &consent.Plan{
		ID: "2026-10-26T09-20-41-c4d5e6", Dir: t.TempDir(), Runs: append([]consent.RunKind(nil), consent.DefaultRuns...),
		Pages: 1, Settle: 3 * time.Second, BannerWait: 10 * time.Second, Timeout: 90 * time.Second, Retries: 1, Parallel: 3,
		CMP: "auto", Click: consent.ClickButton, Policy: consent.PolicyAdvanced, Hold: true, Viewport: "desktop", Locale: "en-US",
		Screenshots: true, Limits: consent.DefaultLimits, Version: "1.2.0",
	}
	for _, raw := range urls {
		u, err := url.Parse(raw)
		if err != nil {
			t.Fatalf("consenttest: %v", err)
		}
		p.Targets = append(p.Targets, consent.Target{URL: u})
	}
	return p
}
