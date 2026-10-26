// Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
// SPDX-License-Identifier: MIT

package main

import (
	"context"
	"fmt"
	"path"
	"sync"

	"github.com/RhythmShahriar/echo/testbed/consent/scenarios"
)

// controlPass runs the good state of every scenario in list, rounds
// times, jobs at once. Each round's verdict is the report's verdict and
// issue keys; a round whose verdict differs from round 1 is a flake, a
// round with an error or warning outside allow a false alarm.
func (b *Bench) controlPass(ctx context.Context, list []scenarios.Scenario, rounds, jobs int) []Control {
	out := make([]Control, len(list))
	sem := make(chan struct{}, max(jobs, 1))
	var wg sync.WaitGroup
	for i, s := range list {
		wg.Add(1)
		go func() {
			defer wg.Done()
			sem <- struct{}{}
			defer func() { <-sem }()
			out[i] = b.control(ctx, s, rounds)
		}()
	}
	wg.Wait()
	return out
}

// control runs one good state rounds times.
func (b *Bench) control(ctx context.Context, s scenarios.Scenario, rounds int) Control {
	c := Control{ID: s.ID}
	first := ""
	unlock := b.lockState(s.ID)
	defer unlock()
	for r := 1; r <= rounds && ctx.Err() == nil; r++ {
		o, _ := b.check(ctx, jobOf(s), false, path.Join("control", s.ID, fmt.Sprint(r)))
		c.Rounds++
		v := judgeGood(s, o)
		c.add(r, v, signature(o), &first)
	}
	b.log(fmt.Sprintf("control %s: %d rounds, %d false alarms, %d failed, %d flakes", s.ID, c.Rounds, c.FalseAlarms, c.Failed, c.Flakes))
	return c
}

// add counts round r of a control with verdict v and signature sig;
// first holds round 1's signature.
func (c *Control) add(r int, v Verdict, sig string, first *string) {
	switch v.Outcome {
	case FalseAlarm:
		c.FalseAlarms++
		c.Details = append(c.Details, fmt.Sprintf("round %d: %s", r, v.Detail))
	case Clean:
	default:
		c.Failed++
		c.Details = append(c.Details, fmt.Sprintf("round %d %s: %s", r, v.Outcome, v.Detail))
	}
	if r == 1 {
		*first = sig
		return
	}
	if sig != *first {
		c.Flakes++
		c.Details = append(c.Details, fmt.Sprintf("round %d differs from round 1: %s, then %s", r, *first, sig))
	}
}
