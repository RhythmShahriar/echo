// Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
// SPDX-License-Identifier: MIT

//go:build browser && unix

package browse

import (
	"context"
	"os"
	"os/signal"
	"strconv"
	"syscall"
	"testing"
	"time"

	"github.com/RhythmShahriar/echo/consent"
	"github.com/RhythmShahriar/echo/consent/consenttest"
)

// processAlive reports whether p still runs: signal 0 checks without
// sending anything. A zombie Chrome its parent hasn't reaped yet still
// counts as gone.
func processAlive(p *os.Process) bool {
	if p.Signal(syscall.Signal(0)) != nil {
		return false
	}
	b, err := os.ReadFile("/proc/" + strconv.Itoa(p.Pid) + "/stat")
	if err != nil {
		return true // no procfs (macOS): signal 0 is all there is
	}
	return !zombie(string(b))
}

// zombie reads the state field of /proc/<pid>/stat.
func zombie(stat string) bool {
	for i := len(stat) - 1; i > 0; i-- {
		if stat[i] == ')' {
			return i+2 < len(stat) && stat[i+2] == 'Z'
		}
	}
	return false
}

// TestConsentProfileRemovedOnSIGINT: SIGINT during a run (as Ctrl-C
// sends it) ends the check with Chrome and its profile gone.
func TestConsentProfileRemovedOnSIGINT(t *testing.T) {
	st := shopSite(t, "")
	p := shopPlan(t, st, consent.RunAccept, consent.RunReject)
	p.Parallel = 1
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()
	cmp := &pageCMP{onChoose: func(ctx context.Context, k consent.RunKind, _ int) (*consent.Choice, consent.Reason, bool) {
		if k != consent.RunReject {
			return nil, "", false
		}
		_ = syscall.Kill(os.Getpid(), syscall.SIGINT)
		select {
		case <-ctx.Done():
		case <-time.After(30 * time.Second):
		}
		return nil, consent.ReasonCanceled, true
	}}
	engine := watchEngine(t)
	needChrome(t)
	consenttest.Use(t, consent.Stages{Classifier: &consenttest.FakeClassifier{}, Signals: consenttest.FakeSignals{}, CMP: cmp})
	res, err := Runner{}.Run(ctx, p)
	if err == nil || res == nil || runOf(t, res.Pages[0], consent.RunReject).Status != consent.StatusCanceled {
		t.Fatalf("Run = %+v, %v", res, err)
	}
	pid, profile := engine()
	gone(t, pid, profile)
}
