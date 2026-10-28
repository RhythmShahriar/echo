// Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
// SPDX-License-Identifier: MIT

package browse

import (
	"context"
	"encoding/json"
	"fmt"
	"net/url"
	"strings"
	"time"

	"github.com/go-rod/rod/lib/proto"

	"github.com/RhythmShahriar/echo/consent"
)

// attempt is one try of one run: the steps of PRD 13.6 in its own
// context.
type attempt struct {
	c      *check
	e      *Engine
	plan   *consent.Plan
	target consent.Target
	page   int // the target's place in the plan, from 1
	kind   consent.RunKind
	run    *Run
	tab    *Tab
	out    *consent.Run
	info   *pageInfo

	startURL string // the start URL, with a CMP's geo parameter when one applies
	geoDone  bool
}

// geoParamer is the CMP driver's optional geolocation override: the
// query parameter (name and value) that makes its banner show as for a
// visitor from country, or "" when the rule has none.
type geoParamer interface {
	GeoParam(cmpID, country string) (string, string)
}

// steps runs the run's steps: load the start page, find the banner,
// make the choice, scroll, visit more pages, reload. A start page that
// can't be checked comes back as a fatal error; err is the context's
// or the browser's.
func (a *attempt) steps(ctx context.Context) (*consent.FatalError, error) {
	p, out := a.plan, a.out
	start := *a.target.URL
	a.startURL = start.String()
	if f, err := a.checkSelectors(ctx); f != nil || err != nil {
		return f, err
	}
	if cmp := p.CMP; cmp != "" && cmp != "auto" {
		a.addGeo(cmp, "")
	}
	a.run.AddVisit(0)
	l, err := a.load(ctx, a.startURL)
	if err != nil {
		return nil, err
	}
	if f := a.startFatal(ctx, &start, l); f != nil {
		return f, nil
	}
	final := l.final
	if final == "" {
		final = a.startURL
	}
	a.info = &pageInfo{final: a.mask(final), redirects: []string{}}
	for _, h := range l.doc.hops[min(1, len(l.doc.hops)):] {
		a.info.redirects = append(a.info.redirects, a.mask(h))
	}
	a.snapshot(ctx)
	out.Visits = append(out.Visits, consent.Visit{URL: a.mask(final), Step: "load", AtMS: 0})

	info, err := a.banner(ctx)
	if err != nil {
		return nil, err
	}
	a.info.cmp = info
	out.Visits[len(out.Visits)-1].Banner = info.Found
	a.shot(ctx, "banner")

	if stop, err := a.choose(ctx, info); stop || err != nil {
		return nil, err
	}
	a.shot(ctx, "after")

	if err := a.scroll(ctx); err != nil {
		return nil, err
	}
	finalURL, _ := url.Parse(final)
	if err := a.pages(ctx, finalURL, info); err != nil {
		return nil, err
	}
	if err := a.reload(ctx, info); err != nil {
		return nil, err
	}
	if a.kind == consent.RunAccept || a.kind == consent.RunReject || a.kind == consent.RunWithdraw {
		if info.Found || info.Present {
			if st, err := a.c.stages.CMP.State(ctx, a.tab, info); err == nil {
				out.FinalState = st
			}
		}
	}
	return nil, ctx.Err()
}

// mask masks a URL for the result.
func (a *attempt) mask(raw string) string { return consent.MaskURL(raw, a.plan.ShowIDs) }

// snapshot records the cookies and storage keys a step left. A failed
// snapshot loses nothing already recorded, so only the context's end
// matters.
func (a *attempt) snapshot(ctx context.Context) { _ = a.run.Snapshot(ctx) }

// shot saves the viewport as the run's screenshot name.
func (a *attempt) shot(ctx context.Context, name string) {
	if path, err := a.tab.Screenshot(ctx, name); err == nil && path != "" {
		a.out.Screenshots[name] = path
	}
}

// addGeo adds the CMP's geolocation override for --geo to the start URL,
// once; the result notes it. name is the CMP's name for the note
// ("" uses the id).
func (a *attempt) addGeo(cmpID, name string) bool {
	geo := strings.ToLower(strings.TrimSpace(a.plan.Geo))
	gp, ok := a.c.stages.CMP.(geoParamer)
	if a.geoDone || geo == "" || !ok || cmpID == "" {
		return false
	}
	key, value := gp.GeoParam(cmpID, geo)
	if key == "" {
		return false
	}
	u, err := url.Parse(a.startURL)
	if err != nil {
		return false
	}
	q := u.Query()
	q.Set(key, value)
	u.RawQuery = q.Encode()
	a.startURL, a.geoDone = u.String(), true
	if name == "" {
		name = cmpID
		for _, r := range a.c.stages.CMP.Rules() {
			if r.ID == cmpID {
				name = r.Name
			}
		}
	}
	a.c.note(consent.SafeText(name, 60) + " geolocation override " + consent.SafeText(key+"="+value, 60) + " (unverified)")
	return true
}

// banner waits up to --banner-wait for the CMP's banner. When --geo is
// set and the CMP found has a geolocation override, the start page is
// loaded again with it and the banner looked for again.
func (a *attempt) banner(ctx context.Context) (consent.CMPInfo, error) {
	cmp := a.c.stages.CMP
	info, err := cmp.Detect(ctx, a.tab, a.plan, a.plan.BannerWait)
	if err != nil {
		if ctx.Err() != nil {
			return info, ctx.Err()
		}
		a.out.Detail = consent.SafeText("finding the banner failed: "+consent.MaskURL(err.Error(), false), 300)
		return consent.CMPInfo{}, nil
	}
	if !a.addGeo(info.ID, info.Name) {
		return info, nil
	}
	at := a.run.Now()
	a.run.AddVisit(at)
	l, err := a.load(ctx, a.startURL)
	if err != nil {
		return info, err
	}
	a.snapshot(ctx)
	final := l.final
	if final == "" {
		final = a.startURL
	}
	again, err := cmp.Detect(ctx, a.tab, a.plan, a.plan.BannerWait)
	if err != nil {
		if ctx.Err() != nil {
			return info, ctx.Err()
		}
		again = info
	}
	a.out.Visits = append(a.out.Visits, consent.Visit{URL: a.mask(final), Step: "load", AtMS: at, Banner: again.Found})
	return again, nil
}

// choose makes the run's choice. Without a banner Accept, Reject and
// Withdraw can't choose: they say so and go on, so what loads later is
// still recorded. No-choice and GPC runs wait out the Consent Mode
// default's wait_for_update instead. stop is true when the run can't go
// on (the click took the tab off the site, or a limit).
func (a *attempt) choose(ctx context.Context, info consent.CMPInfo) (stop bool, err error) {
	out := a.out
	switch a.kind {
	case consent.RunNone, consent.RunGPC:
		return false, a.waitDefault(ctx)
	}
	if !info.Found {
		out.Status, out.Reason = consent.StatusNoBanner, consent.ReasonNoBanner
		if cmp := a.plan.CMP; cmp != "" && cmp != "auto" {
			out.Reason = consent.ReasonCMPNotFound
		}
		return false, nil
	}
	switch a.kind {
	case consent.RunAccept:
		out.Choice, err = a.click(ctx, info, consent.RunAccept, consent.PhaseAfterAccept)
	case consent.RunReject:
		out.Choice, err = a.click(ctx, info, consent.RunReject, consent.PhaseAfterReject)
	case consent.RunWithdraw:
		out.Choice, err = a.click(ctx, info, consent.RunAccept, consent.PhaseAfterAccept)
		if err == nil && out.Reason == consent.ReasonNone {
			_ = a.run.Settle(ctx)
			a.snapshot(ctx)
			if rerr := a.c.stages.CMP.Reopen(ctx, a.tab, a.plan, info); rerr != nil {
				if ctx.Err() != nil {
					return true, ctx.Err()
				}
				out.Status, out.Reason = consent.StatusNoButton, consent.ReasonNoButton
				out.Detail = consent.SafeText(consent.MaskURL(rerr.Error(), false), 300)
			} else {
				out.Withdraw, err = a.click(ctx, info, consent.RunReject, consent.PhaseAfterWithdraw)
			}
		}
	}
	if err != nil {
		return true, err
	}
	switch out.Reason {
	case consent.ReasonNavigation, consent.ReasonLimit, consent.ReasonCanceled:
		return true, nil
	}
	_ = a.run.Settle(ctx)
	a.snapshot(ctx)
	return false, ctx.Err()
}

// click asks the CMP driver for one choice and puts the run in phase
// from the click's time on. Events from the click's own task wait until
// that time is known. A driver error other than the context's reads as
// a click that missed.
func (a *attempt) click(ctx context.Context, info consent.CMPInfo, k consent.RunKind, ph consent.Phase) (*consent.Choice, error) {
	a.run.BeginChoice()
	ch, reason, err := a.c.stages.CMP.Choose(ctx, a.tab, a.plan, info, k)
	at := int64(-1)
	if ch != nil && (reason == consent.ReasonNone || ch.Clicks > 0 || ch.How == "api") {
		at = ch.AtMS
	}
	a.run.SetChoice(at, ph)
	if err != nil {
		if ctx.Err() != nil {
			return ch, ctx.Err()
		}
		reason = consent.ReasonClickMissed
		a.out.Detail = consent.SafeText(consent.MaskURL(err.Error(), false), 300)
	}
	if reason != consent.ReasonNone && a.out.Reason == consent.ReasonNone {
		a.out.Status, a.out.Reason = statusFor(reason), reason
	}
	return ch, nil
}

// statusFor is the run status a CMP driver's reason gives.
func statusFor(r consent.Reason) consent.RunStatus {
	switch r {
	case consent.ReasonNoButton:
		return consent.StatusNoButton
	case consent.ReasonClickMissed, consent.ReasonStateUnchanged:
		return consent.StatusClickFailed
	case consent.ReasonNavigation:
		return consent.StatusNavigatedAway
	case consent.ReasonLimit:
		return consent.StatusLimit
	case consent.ReasonCanceled:
		return consent.StatusCanceled
	case consent.ReasonTimeout:
		return consent.StatusTimeout
	case consent.ReasonNoBanner, consent.ReasonCMPNotFound:
		return consent.StatusNoBanner
	}
	return consent.StatusOK
}

// waitDefault keeps a no-choice or GPC run going until the Consent Mode
// default's wait_for_update plus --settle has passed since the default
// (PRD 13.3): tags that wait for an update send their hits then.
func (a *attempt) waitDefault(ctx context.Context) error {
	sigs := a.run.s.rec.signals()
	_, at, ok := consent.EffectiveDefault(sigs, strings.ToUpper(a.plan.Geo))
	if !ok {
		return nil
	}
	wait := 0
	for _, s := range sigs {
		if s.Command == "default" && s.AtMS == at && (s.Source == "gtag" || s.Source == "datalayer") {
			wait = max(wait, s.WaitForUpdateMS)
		}
	}
	until := at + int64(wait) + a.plan.Settle.Milliseconds()
	left := time.Duration(until-a.run.Now()) * time.Millisecond
	if left <= 0 {
		return nil
	}
	t := time.NewTimer(left)
	defer t.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-t.C:
		return nil
	}
}

// scrollJS measures the page and the viewport in Echo's isolated world.
const scrollJS = `() => ({h: Math.max(document.documentElement ? document.documentElement.scrollHeight : 0,
document.body ? document.body.scrollHeight : 0), vh: innerHeight, vw: innerWidth})`

// scrollPause is the pause between scroll steps, so a page's scroll
// handlers see three separate scrolls.
var scrollPause = 150 * time.Millisecond

// scroll scrolls to the bottom in three wheel steps with the mouse
// moving between them, as a reader would, then settles: tags that wait
// for a scroll fire now ("consent by scrolling" shows in the none run).
func (a *attempt) scroll(ctx context.Context) error {
	var m struct{ H, VH, VW float64 }
	if raw, err := a.tab.EvalIsolated(ctx, scrollJS); err == nil {
		_ = json.Unmarshal(raw, &m)
	}
	if m.VW <= 0 || m.VH <= 0 {
		env := a.run.s.rec.env
		m.VW, m.VH = 1366, 900
		if env != nil {
			m.VW, m.VH = float64(env.width), float64(env.height)
		}
	}
	step := max(100, (m.H-m.VH)/3+1)
	main := a.run.s.main
	main.ctx = ctx
	x, y := m.VW/2, m.VH/2
	for i := range 3 {
		off := float64(i * 12)
		if err := (proto.InputDispatchMouseEvent{Type: proto.InputDispatchMouseEventTypeMouseMoved, X: x + off, Y: y + off}).Call(main); err != nil {
			return ctxOr(ctx, nil)
		}
		if err := (proto.InputDispatchMouseEvent{Type: proto.InputDispatchMouseEventTypeMouseWheel, X: x + off, Y: y + off, DeltaY: step}).Call(main); err != nil {
			return ctxOr(ctx, nil)
		}
		if err := sleep(ctx, scrollPause); err != nil {
			return err
		}
	}
	if err := a.run.Settle(ctx); err != nil {
		return err
	}
	a.snapshot(ctx)
	return nil
}

// ctxOr is the context's error when it ended, else err: a browser call
// that failed because the run's time ran out reads as that.
func ctxOr(ctx context.Context, err error) error {
	if ctx.Err() != nil {
		return ctx.Err()
	}
	return err
}

// sleep waits d or until ctx ends.
func sleep(ctx context.Context, d time.Duration) error {
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-t.C:
		return nil
	}
}

// pages visits up to --pages same-site links of the current page, in
// DOM order, noting whether the banner showed on each.
func (a *attempt) pages(ctx context.Context, base *url.URL, info consent.CMPInfo) error {
	n := min(a.plan.Pages, a.run.s.rec.limitPages())
	if n <= 0 || base == nil {
		return nil
	}
	var links [][2]string
	if raw, err := a.tab.EvalIsolated(ctx, linksJS); err == nil {
		_ = json.Unmarshal(raw, &links)
	} else if ctx.Err() != nil {
		return ctx.Err()
	}
	for _, link := range pickLinks(base, links, n) {
		if err := a.visit(ctx, link, "page", info); err != nil {
			return err
		}
	}
	return nil
}

// reload loads the start URL again and settles: a choice that wasn't
// stored shows here.
func (a *attempt) reload(ctx context.Context, info consent.CMPInfo) error {
	return a.visit(ctx, a.startURL, "reload", info)
}

// visit loads raw as a new visit of step, settles and records it with
// whether the banner showed. A page that fails to load is recorded
// without a banner; only the context's end stops the run.
func (a *attempt) visit(ctx context.Context, raw, step string, info consent.CMPInfo) error {
	at := a.run.Now()
	a.run.AddVisit(at)
	l, err := a.load(ctx, raw)
	if err != nil {
		return err
	}
	shown := raw
	if l.final != "" {
		shown = l.final
	}
	v := consent.Visit{URL: a.mask(shown), Step: step, AtMS: at}
	if l.err == "" && (info.Found || info.Present) {
		if ok, err := a.c.stages.CMP.BannerShowing(ctx, a.tab, info); err == nil {
			v.Banner = ok
		}
	}
	a.snapshot(ctx)
	a.out.Visits = append(a.out.Visits, v)
	return ctx.Err()
}

// selectorCheckJS lists the strings of its array that the browser's own
// selector parser refuses. Nothing is looked for: the fragment is empty.
const selectorCheckJS = `((list) => list.filter((s) => {
  try { document.createDocumentFragment().querySelector(s); return false; } catch (e) { return true; }
}))(%s)`

// checkSelectors asks the browser whether every CSS part of
// --accept-selector and --reject-selector parses, before the start page
// loads. ParseSelector checks only the shape (balanced quotes and
// brackets, the >> and ::shadow steps); "###" gets this far. A part the
// browser refuses is a usage error (PRD 10.1): consent.FatalSelector.
func (a *attempt) checkSelectors(ctx context.Context) (*consent.FatalError, error) {
	type part struct{ flag, css string }
	var parts []part
	for _, f := range []struct {
		flag string
		locs []consent.Locator
	}{{"--accept-selector", a.plan.AcceptSelector}, {"--reject-selector", a.plan.RejectSelector}} {
		for _, l := range f.locs {
			for _, h := range l.Shadow {
				parts = append(parts, part{f.flag, h})
			}
			parts = append(parts, part{f.flag, l.CSS})
		}
	}
	if len(parts) == 0 {
		return nil, nil
	}
	css := make([]string, len(parts))
	for i, p := range parts {
		css[i] = p.css
	}
	arg, err := json.Marshal(css)
	if err != nil {
		return nil, err
	}
	raw, err := a.run.EvalMain(ctx, fmt.Sprintf(selectorCheckJS, arg))
	if err != nil {
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		return nil, nil // the click step says what it can't find
	}
	var bad []string
	if json.Unmarshal(raw, &bad) != nil || len(bad) == 0 {
		return nil, nil
	}
	for _, p := range parts {
		if p.css == bad[0] {
			return &consent.FatalError{Code: consent.FatalSelector,
				Detail: fmt.Sprintf("%s: %q isn't CSS the browser accepts", p.flag, consent.SafeText(p.css, 120))}, nil
		}
	}
	return nil, nil
}
