// Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
// SPDX-License-Identifier: MIT

package consent

import (
	"context"
	"errors"
)

// RunStages are the stages RunPlan needs: everything but the Comparer.
var RunStages = []Stage{StageClassifier, StageSignals, StageState, StageCMP, StageRunner, StageEvaluator}

// RunPlan checks every stage is linked, validates p, calls Runner.Run then
// Evaluator.Evaluate, sets Result.ID, Policy, Held and Complete
// (false when any run is inconclusive, truncated or canceled). A plan
// with zero Limits gets DefaultLimits. When the context is canceled
// mid-way, the runs that finished are still judged and returned with the
// error, so the CLI can keep them (complete: false). (It can't be named
// Run: that is the type of one run.)
func RunPlan(ctx context.Context, p *Plan) (*Result, error) {
	if err := Need(RunStages...); err != nil {
		return nil, err
	}
	if err := p.Validate(); err != nil {
		return nil, err
	}
	if p.Limits == (Limits{}) {
		p.Limits = DefaultLimits
	}
	if p.Policy == "" {
		p.Policy = PolicyAdvanced
	}
	l := Linked()
	r, err := l.Runner.Run(ctx, p)
	if r == nil {
		if err == nil {
			err = errors.New("consent: the runner returned no result")
		}
		return nil, err
	}
	if err != nil && !errors.Is(err, context.Canceled) && !errors.Is(err, context.DeadlineExceeded) {
		return r, err
	}
	r.ID, r.Policy, r.Held = p.ID, p.Policy, p.Hold
	l.Evaluator.Evaluate(r, p)
	r.Complete = err == nil && !r.incomplete()
	return r, err
}

// Compare calls the registered Comparer; nil when none is linked.
func Compare(base, cur *Result, accept []string) *Comparison {
	c := Linked().Comparer
	if c == nil {
		return nil
	}
	return c.Compare(base, cur, accept)
}
