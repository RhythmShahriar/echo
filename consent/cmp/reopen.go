// Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
// SPDX-License-Identifier: MIT

package cmp

import (
	"context"
	"fmt"
	"strings"

	"github.com/RhythmShahriar/echo/consent"
)

// Reopen opens the banner or preference center again, for the withdraw
// run: it clicks the first of the rule's reopen controls that shows (a
// floating "cookie settings" button) within --banner-wait. When none
// shows, or the rule has none, it calls the CMP's own reopen function,
// but only under --click api or button,api: a withdrawal a visitor
// can't make is not one Echo makes for them unasked. --click api calls
// the function first. Every failure is an error naming the CMP.
func (d *driver) Reopen(ctx context.Context, t consent.Tab, p *consent.Plan, info consent.CMPInfo) error {
	r := d.ruleFor(info)
	name := consent.SafeText(info.Name, 40)
	if r != nil {
		name = r.Name
	}
	if name == "" {
		name = "this banner"
	}
	if r == nil || (len(r.ReopenSel) == 0 && r.ReopenAPI == "") {
		return fmt.Errorf("consent: %s has no known way to reopen the banner", name)
	}
	mode := clickMode(p)
	apiAllowed := r.ReopenAPI != "" && (mode == consent.ClickAPI || mode == consent.ClickButtonAPI)
	if mode == consent.ClickAPI && apiAllowed {
		return reopenAPI(ctx, t, r)
	}
	if len(r.ReopenSel) > 0 {
		c, err := d.waitClickable(ctx, t, p, r.ReopenSel, nil)
		if err != nil {
			return err
		}
		if c != nil {
			reason, err := d.clickSteps(ctx, t, p, c.Steps, nil, &consent.Choice{})
			switch {
			case err != nil:
				return err
			case reason == consent.ReasonNavigation:
				return fmt.Errorf("consent: %s's reopen button %s left the page", name, consent.SafeText(c.Raw, selectorWidth))
			case reason != consent.ReasonNone:
				return fmt.Errorf("consent: %s's reopen button %s couldn't be clicked", name, consent.SafeText(c.Raw, selectorWidth))
			}
			return nil
		}
	}
	switch {
	case apiAllowed:
		return reopenAPI(ctx, t, r)
	case len(r.ReopenSel) == 0:
		return fmt.Errorf("consent: %s has no way to reopen the banner without its API; --click button,api allows it", name)
	case r.ReopenAPI != "":
		return fmt.Errorf("consent: %s showed no button to reopen the banner (%s); --click button,api allows its API", name, reopenTried(r))
	}
	return fmt.Errorf("consent: %s showed no button to reopen the banner (%s)", name, reopenTried(r))
}

// reopenTried lists the reopen selectors in the words of an error.
func reopenTried(r *rule) string {
	raw := make([]string, 0, len(r.ReopenSel))
	for _, c := range r.ReopenSel {
		raw = append(raw, c.Raw)
	}
	return consent.SafeText(strings.Join(raw, ", "), selectorWidth)
}

// reopenAPI calls the rule's fixed reopen function in the main world.
func reopenAPI(ctx context.Context, t consent.Tab, r *rule) error {
	call := r.API.Reopen
	if _, err := t.EvalMain(ctx, call); err != nil {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		return fmt.Errorf("consent: %s's reopen call %s failed: %s", r.Name, consent.SafeText(call, selectorWidth), consent.SafeText(err.Error(), 120))
	}
	return nil
}

// GeoParam is the query parameter that asks cmpID's CMP for the banner
// of country (ISO 3166-1 alpha-2), when its rule has a geo_override:
// ("otgeo", "de") for OneTrust and DE. It is empty for a CMP without
// one or a country that isn't two letters. consent/browse finds it by
// asserting the driver to interface{ GeoParam(string, string) (string,
// string) }, adds it to the start URL and notes it as unverified.
func (d *driver) GeoParam(cmpID, country string) (string, string) {
	r := d.byID[cmpID]
	if r == nil || r.Geo == nil {
		return "", ""
	}
	c := strings.ToLower(strings.TrimSpace(country))
	if len(c) != 2 || c[0] < 'a' || c[0] > 'z' || c[1] < 'a' || c[1] > 'z' {
		return "", ""
	}
	return r.Geo.Param, c
}
