// Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
// SPDX-License-Identifier: MIT

package cmp

import (
	"context"
	"encoding/json"
	"slices"
	"strings"
	"time"

	"github.com/RhythmShahriar/echo/consent"
)

// sighting is what one poll saw of one rule.
type sighting struct {
	selectors []string // detect selectors that matched, in rule order
	globals   []string
	scripts   []string
	banner    bool
}

func (s sighting) present() bool {
	return len(s.selectors)+len(s.globals)+len(s.scripts) > 0
}

// Detect polls the page every 250 ms until wait for a rule's banner. It
// returns at the first poll that sees one; on timeout Found is false
// and Present says whether any rule's selector, global or script was
// seen. With --cmp set only that rule is tried.
func (d *driver) Detect(ctx context.Context, t consent.Tab, p *consent.Plan, wait time.Duration) (consent.CMPInfo, error) {
	rules, err := d.candidates(p)
	if err != nil {
		return consent.CMPInfo{}, err
	}
	in := scanFor(rules)
	deadline := t.Now() + wait.Milliseconds()
	var (
		last    consent.CMPInfo
		lastErr error
		ok      bool
	)
	for {
		info, err := d.poll(ctx, t, rules, in)
		switch {
		case err != nil:
			if ctx.Err() != nil {
				return last, ctx.Err()
			}
			lastErr = err
		default:
			ok = true
			// Present is sticky: a CMP seen once is on the page even when a
			// later poll (mid-navigation) misses it.
			if info.Found || !last.Present || info.Present {
				last = info
			}
			if info.Found {
				return info, nil
			}
		}
		if t.Now() >= deadline {
			break
		}
		if err := d.sleep(ctx, pollEvery); err != nil {
			return last, err
		}
	}
	if !ok && lastErr != nil {
		return last, lastErr
	}
	return last, nil
}

// scanFor is the scan script's input for rules.
func scanFor(rules []*rule) scanInput {
	var in scanInput
	seen := map[string]bool{}
	for _, r := range rules {
		sr := scanRule{ID: r.ID}
		for _, l := range r.Selectors {
			sr.Selectors = append(sr.Selectors, keyed{Key: locKey(l), Loc: l})
		}
		sr.Banners = r.Banners
		in.Rules = append(in.Rules, sr)
		for _, s := range r.Scripts {
			if !seen[s] {
				seen[s] = true
				in.Scripts = append(in.Scripts, s)
			}
		}
	}
	return in
}

// poll looks once: the main-world globals and the scan in the top
// document and in every cross-origin frame.
func (d *driver) poll(ctx context.Context, t consent.Tab, rules []*rule, in scanInput) (consent.CMPInfo, error) {
	globals := map[string]bool{}
	raw, err := t.EvalMain(ctx, d.globalsJS)
	if err != nil {
		return consent.CMPInfo{}, err
	}
	if len(raw) > 0 && string(raw) != "null" {
		// A page can't change what the script returns, only whether its
		// own globals exist; an unreadable answer reads as none.
		_ = json.Unmarshal(raw, &globals)
	}
	scans, err := scanFrames(ctx, t, in)
	if err != nil {
		return consent.CMPInfo{}, err
	}
	now := t.Now()
	var present *rule
	var presentSeen sighting
	for _, r := range rules {
		if r.ID == genericID {
			// Last in the order: no named rule shows a banner.
			_, ok, err := genericBanner(ctx, t)
			if err != nil {
				return consent.CMPInfo{}, err
			}
			if ok {
				info := r.info(sighting{}, true)
				info.BannerAtMS = now
				info.TCF = globals[tcfGlobal]
				return info, nil
			}
			continue
		}
		s := sighting{}
		for _, sc := range scans {
			res := sc.Rules[r.ID]
			s.banner = s.banner || res.Banner
			for _, k := range res.Matched {
				if !slices.Contains(s.selectors, k) {
					s.selectors = append(s.selectors, k)
				}
			}
			for _, sub := range sc.Scripts {
				if slices.Contains(r.Scripts, sub) && !slices.Contains(s.scripts, sub) {
					s.scripts = append(s.scripts, sub)
				}
			}
		}
		for _, g := range r.Globals {
			if globals[g] {
				s.globals = append(s.globals, g)
			}
		}
		if s.banner {
			info := r.info(s, true)
			info.BannerAtMS = now
			info.TCF = globals[tcfGlobal]
			return info, nil
		}
		if present == nil && s.present() {
			present, presentSeen = r, s
		}
	}
	if present == nil {
		return consent.CMPInfo{TCF: globals[tcfGlobal]}, nil
	}
	info := present.info(presentSeen, false)
	info.TCF = globals[tcfGlobal]
	return info, nil
}

// scanFrames runs the scan in the top document and in each cross-origin
// frame (same-origin frames are reached from the top document). The Tab
// runs a script in a frame when its last argument is that consent.Frame;
// a frame that is gone by then is skipped.
func scanFrames(ctx context.Context, t consent.Tab, in scanInput) ([]scanResult, error) {
	var top scanResult
	raw, err := t.EvalIsolated(ctx, scanJS, in)
	if err != nil {
		return nil, err
	}
	if len(raw) > 0 && string(raw) != "null" {
		if err := json.Unmarshal(raw, &top); err != nil {
			return nil, err
		}
	}
	out := []scanResult{top}
	frames, err := t.Frames(ctx)
	if err != nil {
		return out, nil
	}
	for i, f := range frames {
		if i >= 32 {
			break
		}
		if !f.CrossOrigin {
			continue
		}
		var res scanResult
		raw, err := t.EvalIsolated(ctx, scanJS, in, f)
		if err != nil || len(raw) == 0 || json.Unmarshal(raw, &res) != nil {
			continue
		}
		out = append(out, res)
	}
	return out, nil
}

// info is the CMPInfo of r as seen in s.
func (r *rule) info(s sighting, found bool) consent.CMPInfo {
	// The first piece of each kind, so a second selector of the same
	// banner never crowds out the global that names the CMP.
	var ev []string
	if len(s.selectors) > 0 {
		ev = append(ev, "selector:"+s.selectors[0])
	}
	if len(s.globals) > 0 {
		ev = append(ev, "global:"+s.globals[0])
	}
	if len(s.scripts) > 0 {
		ev = append(ev, "script:"+s.scripts[0])
	}
	switch {
	case r.ID == genericID:
		ev = []string{"generic"}
	case len(ev) == 0 && found:
		ev = []string{"selector:" + locKey(r.Banners[0])}
	}
	if len(ev) > 2 {
		ev = ev[:2]
	}
	info := consent.CMPInfo{ID: r.ID, Name: r.Name, Found: found, Present: true, DetectedBy: strings.Join(ev, " and ")}
	if len(r.FirstLayer) == 0 && r.Second != nil {
		// Only a second layer: the Reject is in the settings whatever the
		// page shows. Otherwise the layer is known once Find looks.
		info.RejectLayer = 2
	}
	return info
}

// BannerShowing reports whether the CMP's banner container is visible
// now: the scan of Detect, once, for that rule alone. A banner found
// only through custom selectors has no container and never shows.
func (d *driver) BannerShowing(ctx context.Context, t consent.Tab, info consent.CMPInfo) (bool, error) {
	r := d.ruleFor(info)
	switch {
	case r != nil && r.ID == genericID:
		return genericShowing(ctx, t)
	case r == nil || r.BannerRaw == "":
		return false, nil
	}
	return bannerShowing(ctx, t, r)
}

func bannerShowing(ctx context.Context, t consent.Tab, r *rule) (bool, error) {
	scans, err := scanFrames(ctx, t, scanInput{Rules: []scanRule{{ID: r.ID, Banners: r.Banners}}})
	if err != nil {
		return false, err
	}
	for _, s := range scans {
		if s.Rules[r.ID].Banner {
			return true, nil
		}
	}
	return false, nil
}
