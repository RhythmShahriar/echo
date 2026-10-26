// Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
// SPDX-License-Identifier: MIT

package cmp

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/RhythmShahriar/echo/consent"
)

// The scripts Find and Choose use to look at controls before clicking.
var (
	probeJS   = buildScript("probe")
	togglesJS = buildScript("toggles")
)

// chainText writes a chain the way --accept-selector reads it.
func chainText(steps []consent.Locator) string {
	out := make([]string, 0, len(steps))
	for _, l := range steps {
		out = append(out, locKey(l))
	}
	return strings.Join(out, " >> ")
}

// probeResult is what the probe script says of one control.
type probeResult struct {
	Found    bool        `json:"found"`
	Visible  bool        `json:"visible"`
	Kind     bool        `json:"kind"`
	Offsite  bool        `json:"offsite"`
	Disabled bool        `json:"disabled"`
	Inside   bool        `json:"inside"`
	Top      bool        `json:"top"`
	Closed   bool        `json:"closed"` // not found: behind a closed shadow root
	Text     string      `json:"text"`
	Box      consent.Box `json:"box"`
}

// clickable is the PRD 13.5 test before a click: the control is there,
// visible, a kind a visitor clicks, enabled, top-most, not a link off
// the site and inside the banner.
func (p probeResult) clickable() bool {
	return p.Found && p.Visible && p.Kind && !p.Offsite && !p.Disabled && p.Inside && p.Top
}

type probeInput struct {
	Targets []keyed           `json:"targets"`
	Within  []consent.Locator `json:"within,omitempty"` // nil: anywhere on the page
}

// toggle is one switch of a second layer as the toggles script sees it.
type toggle struct {
	Path     string `json:"path"`
	On       bool   `json:"on"`
	Disabled bool   `json:"disabled"`
	Text     string `json:"text"`
}

// evalJSON runs an isolated-world script with in and decodes its answer
// into out. A null answer leaves out as it is.
func evalJSON(ctx context.Context, t consent.Tab, js string, in, out any) error {
	raw, err := t.EvalIsolated(ctx, js, in)
	if err != nil {
		return err
	}
	if len(raw) == 0 || string(raw) == "null" {
		return nil
	}
	if err := json.Unmarshal(raw, out); err != nil {
		return fmt.Errorf("consent: the page answered a CMP script with something unreadable: %s", consent.SafeText(err.Error(), 120))
	}
	return nil
}

// probe asks about every locator in locs at once, so one call answers a
// whole poll.
func probe(ctx context.Context, t consent.Tab, locs []consent.Locator, within []consent.Locator) (map[string]probeResult, error) {
	in := probeInput{Within: within}
	for _, l := range locs {
		in.Targets = append(in.Targets, keyed{Key: locKey(l), Loc: l})
	}
	out := map[string]probeResult{}
	err := evalJSON(ctx, t, probeJS, in, &out)
	return out, err
}

// toggles lists the switches l matches.
func toggles(ctx context.Context, t consent.Tab, l consent.Locator) ([]toggle, error) {
	var out []toggle
	err := evalJSON(ctx, t, togglesJS, struct {
		Loc consent.Locator `json:"loc"`
	}{l}, &out)
	if len(out) > 200 {
		out = out[:200]
	}
	return out, err
}
