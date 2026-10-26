// Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
// SPDX-License-Identifier: MIT

package consent_test

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/RhythmShahriar/echo/consent"
	"github.com/RhythmShahriar/echo/consent/consenttest"
)

// No test in this package runs in parallel: they swap the registry.

func TestSwapRestores(t *testing.T) {
	before := consent.Linked()
	fakes := consenttest.Fakes()
	restore := consent.Swap(fakes)
	if consent.Linked().Classifier != fakes.Classifier || consent.Linked().Runner != fakes.Runner {
		t.Fatal("Swap didn't register the fakes")
	}
	if err := consent.Need(consent.RunStages...); err != nil {
		t.Fatalf("Need with every fake: %v", err)
	}
	// Nil fields keep the current stage.
	cmp := &consenttest.FakeCMP{}
	inner := consent.Swap(consent.Stages{CMP: cmp})
	if l := consent.Linked(); l.CMP != cmp || l.Classifier != fakes.Classifier {
		t.Fatal("a partial Swap replaced more than it named")
	}
	inner()
	if consent.Linked().CMP != fakes.CMP {
		t.Fatal("the inner restore didn't bring the outer CMP back")
	}
	restore()
	if consent.Linked() != before {
		t.Fatal("restore didn't bring the registry back")
	}
}

func TestNeed(t *testing.T) {
	consenttest.Use(t, consent.Stages{Classifier: &consenttest.FakeClassifier{}})
	if err := consent.Need(consent.StageClassifier); err != nil {
		t.Fatalf("Need(tracker) = %v", err)
	}
	if consent.Linked().CMP != nil || consent.Linked().Runner != nil {
		t.Skip("this build links real stages; the missing-stage message is tested where they aren't")
	}
	err := consent.Need(consent.StageCMP)
	if err == nil || err.Error() != "consent: cmp not linked into this build" {
		t.Fatalf("Need(cmp) = %v", err)
	}
	err = consent.Need(consent.StageClassifier, consent.StageCMP, consent.StageRunner)
	if err == nil || err.Error() != "consent: cmp, browse not linked into this build" {
		t.Fatalf("Need(three) = %v", err)
	}
}

func TestSetTwicePanics(t *testing.T) {
	consenttest.Use(t, consent.Stages{Comparer: &consenttest.FakeComparer{}})
	defer func() {
		if r := recover(); r == nil || !strings.Contains(r.(string), "compare registered twice") {
			t.Fatalf("recover() = %v", r)
		}
	}()
	consent.SetComparer(&consenttest.FakeComparer{})
}

func TestRunPlanJoinsTheStages(t *testing.T) {
	fakes := consenttest.Fakes()
	runner := &consenttest.FakeRunner{Result: consenttest.GoldenResult("meta-ignores")}
	eval := &consenttest.FakeEvaluator{}
	fakes.Runner, fakes.Evaluator = runner, eval
	consenttest.Use(t, fakes)
	p := consenttest.NewPlan(t)
	p.ID, p.Policy, p.Hold, p.Limits = "run-1", consent.PolicyBasic, false, consent.Limits{}
	r, err := consent.RunPlan(t.Context(), p)
	if err != nil {
		t.Fatal(err)
	}
	if r.ID != "run-1" || r.Policy != consent.PolicyBasic || r.Held || !r.Complete {
		t.Errorf("result = id %q policy %q held %v complete %v", r.ID, r.Policy, r.Held, r.Complete)
	}
	if eval.Calls != 1 || len(runner.Plans) != 1 || runner.Plans[0].Limits != consent.DefaultLimits {
		t.Errorf("evaluator calls %d, plans %d", eval.Calls, len(runner.Plans))
	}
	if r.ExitCode("error") != 1 {
		t.Errorf("exit %d", r.ExitCode("error"))
	}
}

func TestRunPlanRefusesABadPlan(t *testing.T) {
	consenttest.Use(t, consenttest.Fakes())
	p := consenttest.NewPlan(t)
	p.Pages = 11
	if _, err := consent.RunPlan(t.Context(), p); err == nil || err.Error() != "--pages 11: want 0 to 10" {
		t.Fatalf("err = %v", err)
	}
}

// A canceled check keeps the runs that finished: they are judged and
// returned with the error; the result is incomplete.
func TestRunPlanCanceledKeepsRuns(t *testing.T) {
	fakes := consenttest.Fakes()
	fakes.Runner = &consenttest.FakeRunner{Result: consenttest.GoldenResult("clean"), Err: context.Canceled}
	eval := &consenttest.FakeEvaluator{}
	fakes.Evaluator = eval
	consenttest.Use(t, fakes)
	r, err := consent.RunPlan(t.Context(), consenttest.NewPlan(t))
	if !errors.Is(err, context.Canceled) || r == nil || r.Complete || eval.Calls != 1 {
		t.Fatalf("r = %v, err = %v, evaluated %d", r, err, eval.Calls)
	}
	// Any other runner error comes back unjudged.
	fatal := &consent.FatalError{Code: consent.FatalNoBrowser}
	fakes.Runner = &consenttest.FakeRunner{Err: fatal}
	consenttest.Use(t, fakes)
	if _, err := consent.RunPlan(t.Context(), consenttest.NewPlan(t)); !errors.Is(err, fatal) {
		t.Fatalf("err = %v", err)
	}
}

func TestCompareUsesTheComparer(t *testing.T) {
	consenttest.Use(t, consent.Stages{Comparer: &consenttest.FakeComparer{}})
	c := consent.Compare(consenttest.GoldenResult("compare-base"), consenttest.GoldenResult("compare-cur"), nil)
	if c == nil || len(c.Changes) != 3 || c.Changes[0].Class != consent.ClassBroke {
		t.Fatalf("Compare = %+v", c)
	}
}

func TestFatalError(t *testing.T) {
	inner := errors.New("connection refused")
	e := &consent.FatalError{Code: consent.FatalUnreachable, URL: "https://shop.test/", Err: inner}
	if !errors.Is(e, inner) {
		t.Error("FatalError doesn't unwrap")
	}
	if got := e.Error(); got != "consent: unreachable: https://shop.test/: connection refused" {
		t.Errorf("Error() = %q", got)
	}
	e = &consent.FatalError{Code: consent.FatalHTTPStatus, URL: "https://shop.test/", Status: 401, Detail: "401 Unauthorized"}
	if got := e.Error(); got != "consent: http-status: https://shop.test/: status 401: 401 Unauthorized" {
		t.Errorf("Error() = %q", got)
	}
}
