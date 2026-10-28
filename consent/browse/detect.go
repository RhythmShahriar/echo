// Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
// SPDX-License-Identifier: MIT

package browse

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/go-rod/rod/lib/proto"

	"github.com/RhythmShahriar/echo/consent"
)

// defaultDetectTimeout bounds consent detect when the plan sets no
// --timeout.
const defaultDetectTimeout = 90 * time.Second

// Detect loads the plan's first target once (hits held as in any run),
// waits for the banner, finds both controls without clicking and saves
// one viewport screenshot as Plan.Dir/<Plan.ID>.png. A page that can't
// be checked comes back as a *consent.FatalError.
func (Runner) Detect(ctx context.Context, p *consent.Plan) (*consent.DetectResult, error) {
	if len(p.Targets) == 0 {
		return nil, errors.New("consent: detect needs a page")
	}
	stages := consent.Linked()
	if err := consent.Need(consent.StageCMP); err != nil {
		return nil, err
	}
	e, err := Start(ctx, p)
	if err != nil {
		return nil, err
	}
	defer e.Close()
	if engineStarted != nil {
		engineStarted(e)
	}
	timeout := p.Timeout
	if timeout <= 0 {
		timeout = defaultDetectTimeout
	}
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	t := p.Targets[0]
	run, err := e.Open(ctx, t, consent.RunNone, openWait)
	if err != nil {
		return nil, err
	}
	defer run.Close()
	out := newRun(consent.RunNone)
	a := &attempt{c: newCheck(e, p, stages), e: e, plan: p, target: t, page: 1, kind: consent.RunNone, run: run, tab: run.Tab(1), out: &out}
	start := *t.URL
	a.startURL = start.String()
	if f, err := a.checkSelectors(ctx); err != nil {
		return nil, err
	} else if f != nil {
		return nil, f
	}
	if cmp := p.CMP; cmp != "" && cmp != "auto" {
		a.addGeo(cmp, "")
	}
	watch := a.watchBanner(ctx)
	defer watch.stop()
	l, err := a.load(ctx, a.startURL)
	if err != nil {
		return nil, err
	}
	if f := a.startFatal(ctx, &start, l); f != nil {
		return nil, f
	}
	info, err := a.banner(ctx, watch)
	if err != nil {
		return nil, err
	}
	res := &consent.DetectResult{URL: a.mask(t.URL.String()), CMP: info, Browser: e.Browser()}
	if info.Found {
		for _, k := range []consent.RunKind{consent.RunAccept, consent.RunReject} {
			ch, _, err := stages.CMP.Find(ctx, a.tab, p, info, k)
			if err != nil && ctx.Err() != nil {
				return nil, ctx.Err()
			}
			if k == consent.RunAccept {
				res.Accept = ch
			} else {
				res.Reject = ch
			}
		}
	}
	shot, err := detectShot(ctx, run, p)
	if err != nil {
		return nil, err
	}
	res.Screenshot = shot
	return res, nil
}

// detectShot saves the viewport as Plan.Dir/<Plan.ID>.png (0600) and
// returns that name relative to Plan.Dir, or "" without a folder or with
// --no-screenshots. It is written directly, not as run evidence.
func detectShot(ctx context.Context, run *Run, p *consent.Plan) (string, error) {
	if p.Dir == "" || !p.Screenshots {
		return "", nil
	}
	id := filepath.Base(p.ID)
	if id == "" || id == "." || id == ".." || strings.ContainsAny(id, `/\`) {
		return "", fmt.Errorf("consent: detect: %q is not a file name", consent.SafeText(p.ID, 80))
	}
	main := run.s.main
	main.ctx = ctx
	shot, err := proto.PageCaptureScreenshot{Format: proto.PageCaptureScreenshotFormatPng}.Call(main)
	if err != nil {
		return "", fmt.Errorf("taking a screenshot: %w", err)
	}
	if err := os.MkdirAll(p.Dir, 0o700); err != nil {
		return "", err
	}
	name := id + ".png"
	if err := os.WriteFile(filepath.Join(p.Dir, name), shot.Data, 0o600); err != nil {
		return "", err
	}
	return name, nil
}
