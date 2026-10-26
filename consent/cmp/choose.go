// Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
// SPDX-License-Identifier: MIT

package cmp

import (
	"context"
	"fmt"
	"maps"
	"slices"
	"strings"
	"time"

	"github.com/RhythmShahriar/echo/consent"
)

// maxClicks is how many times a choice is clicked before Echo gives up:
// some banners bind their handlers late (c20), so the first click can
// land before the button does anything.
const maxClicks = 3

// secondGrace is how long the first layer gets to show its Reject
// before the settings are opened instead, so a banner still drawing its
// buttons isn't taken for a two-layer one.
const secondGrace = time.Second

// The widths Choice fields are cut to.
const (
	selectorWidth = 200
	textWidth     = 120
)

// target is the control a choice starts with: the chain clicked on the
// first layer, or the opener of the settings on the second.
type target struct {
	layer  int
	chain  chain
	probe  probeResult
	within []consent.Locator // the banner the first click must be inside; nil: anywhere
	// open: the settings are already open (the withdraw run reopened
	// them with the CMP's API) and chain is their Save.
	open bool
}

// choice is the Choice Find reports for the target, before any click.
func (tg *target) choice() *consent.Choice {
	b := tg.probe.Box
	return &consent.Choice{
		How: "button", Selector: consent.SafeText(tg.chain.Raw, selectorWidth), Text: consent.SafeText(tg.probe.Text, textWidth),
		Layer: tg.layer, Box: &b,
	}
}

// checkKind refuses a run kind that makes no choice on the banner.
func checkKind(k consent.RunKind) error {
	if k != consent.RunAccept && k != consent.RunReject {
		return fmt.Errorf("consent: a %s run makes no choice on the banner; want accept or reject", k)
	}
	return nil
}

// clickMode is the plan's --click, button when unset.
func clickMode(p *consent.Plan) consent.ClickMode {
	if p.Click == "" {
		return consent.ClickButton
	}
	return p.Click
}

// apiCall is the rule's fixed call for k; "" when it has none.
func apiCall(r *rule, k consent.RunKind) string {
	if r == nil {
		return ""
	}
	if k == consent.RunAccept {
		return r.API.Accept
	}
	return r.API.Reject
}

func apiChoice(call string) *consent.Choice {
	return &consent.Choice{How: "api", Selector: consent.SafeText(call, selectorWidth)}
}

// Find locates the Accept or Reject control the way Choose would, up to
// the click: How, Selector, Text, Layer and Box set, nothing clicked.
func (d *driver) Find(ctx context.Context, t consent.Tab, p *consent.Plan, info consent.CMPInfo, k consent.RunKind) (*consent.Choice, consent.Reason, error) {
	if err := checkKind(k); err != nil {
		return nil, consent.ReasonNone, err
	}
	r := d.ruleFor(info)
	mode := clickMode(p)
	call := apiCall(r, k)
	if mode == consent.ClickAPI {
		if call == "" {
			return nil, consent.ReasonNoButton, nil
		}
		return apiChoice(call), consent.ReasonNone, nil
	}
	tg, reason, err := d.locate(ctx, t, p, r, k)
	switch {
	case err != nil:
		return nil, reason, err
	case tg != nil:
		return tg.choice(), consent.ReasonNone, nil
	case mode == consent.ClickButtonAPI && call != "":
		ch := apiChoice(call)
		ch.Fallback = true
		return ch, consent.ReasonNone, nil
	}
	return nil, reason, nil
}

// Choose makes the Accept or Reject choice as a visitor does and checks
// the CMP stored it. --click api calls the CMP's own function instead;
// button,api uses it when no button is found and, for a Reject the
// button didn't make, to tell whether the API would have (APIWorked).
// It never hides or removes a banner and never types into a field.
func (d *driver) Choose(ctx context.Context, t consent.Tab, p *consent.Plan, info consent.CMPInfo, k consent.RunKind) (*consent.Choice, consent.Reason, error) {
	if err := checkKind(k); err != nil {
		return nil, consent.ReasonNone, err
	}
	r := d.ruleFor(info)
	if r != nil && r.Verify.Parser != "banner" && consent.Linked().State == nil {
		return nil, consent.ReasonNone, consent.Need(consent.StageState)
	}
	mode := clickMode(p)
	if mode == consent.ClickAPI {
		return d.viaAPI(ctx, t, p, r, k)
	}
	ch, reason, err := d.viaButton(ctx, t, p, r, k)
	if err != nil || mode != consent.ClickButtonAPI || apiCall(r, k) == "" {
		return ch, reason, err
	}
	switch {
	case reason == consent.ReasonNoButton:
		api, ar, err := d.viaAPI(ctx, t, p, r, k)
		if api != nil {
			api.Fallback = true
		}
		return api, ar, err
	case k == consent.RunReject && ch != nil && (reason == consent.ReasonClickMissed || reason == consent.ReasonStateUnchanged):
		// The run stays inconclusive: a Reject only the API makes is one a
		// visitor can't make (consent/reject-broken).
		api, _, err := d.viaAPI(ctx, t, p, r, k)
		if err != nil {
			return ch, reason, err
		}
		ch.APIWorked = api != nil && api.Verified
	}
	return ch, reason, nil
}

// chainsFor are the chains for k: the plan's selector when set, else the
// rule's, with the rule's second layer for a Reject.
func chainsFor(p *consent.Plan, r *rule, k consent.RunKind) ([]chain, *secondLayer) {
	custom := p.AcceptSelector
	if k == consent.RunReject {
		custom = p.RejectSelector
	}
	if len(custom) > 0 {
		return []chain{{Raw: chainText(custom), Steps: custom}}, nil
	}
	if r == nil {
		return nil, nil
	}
	if k == consent.RunAccept {
		return r.Accept, nil
	}
	return r.FirstLayer, r.Second
}

// within are the banner containers the first-layer controls must be in
// (one of them); nil when the CMP has none (custom selectors on an
// unknown banner).
func within(r *rule) []consent.Locator {
	if r == nil || r.BannerRaw == "" {
		return nil
	}
	return r.Banners
}

// one is a one-step chain for l.
func one(l consent.Locator) chain { return chain{Raw: locKey(l), Steps: []consent.Locator{l}} }

// locate waits up to --banner-wait for the first clickable control: a
// first-layer chain in rule order, else (after secondGrace) the opener
// of the second layer.
func (d *driver) locate(ctx context.Context, t consent.Tab, p *consent.Plan, r *rule, k consent.RunKind) (*target, consent.Reason, error) {
	chains, second := chainsFor(p, r, k)
	if len(chains) == 0 && second == nil {
		if r != nil && r.ID == genericID {
			return d.genericLocate(ctx, t, p, k)
		}
		return nil, consent.ReasonNoButton, nil
	}
	var locs []consent.Locator
	for _, c := range chains {
		locs = append(locs, c.Steps[0])
	}
	if second != nil {
		for _, c := range second.Open {
			locs = append(locs, c.Steps[0])
		}
	}
	start := t.Now()
	deadline := start + p.BannerWait.Milliseconds()
	for {
		res, err := probe(ctx, t, locs, within(r))
		if err != nil && ctx.Err() != nil {
			return nil, consent.ReasonNone, ctx.Err()
		}
		now := t.Now()
		if err == nil {
			for _, c := range chains {
				if pr := res[locKey(c.Steps[0])]; pr.clickable() {
					return &target{layer: 1, chain: c, probe: pr, within: within(r)}, consent.ReasonNone, nil
				}
			}
			if behindClosedRoot(res) {
				// A closed shadow root never opens to the page's scripts:
				// waiting can't help, only the CMP's API can choose.
				return nil, consent.ReasonNoButton, nil
			}
			if second != nil && (now-start >= secondGrace.Milliseconds() || now >= deadline) {
				for _, c := range second.Open {
					if pr := res[locKey(c.Steps[0])]; pr.clickable() {
						return &target{layer: 2, chain: c, probe: pr, within: within(r)}, consent.ReasonNone, nil
					}
				}
				tg, err := openSettings(ctx, t, second)
				if err != nil && ctx.Err() != nil {
					return nil, consent.ReasonNone, ctx.Err()
				}
				if tg != nil {
					return tg, consent.ReasonNone, nil
				}
			}
		}
		if now >= deadline {
			return nil, consent.ReasonNoButton, nil
		}
		if err := d.sleep(ctx, pollEvery); err != nil {
			return nil, consent.ReasonNone, err
		}
	}
}

// openSettings is the Save of settings that are already open, with no
// opener to click: a reopen that went straight to the preference center
// (OneTrust.ToggleInfoDisplay, Didomi.preferences.show). Save sits
// outside the banner, so it is probed anywhere on the page.
func openSettings(ctx context.Context, t consent.Tab, second *secondLayer) (*target, error) {
	var locs []consent.Locator
	for _, c := range second.Save {
		locs = append(locs, c.Steps[0])
	}
	res, err := probe(ctx, t, locs, nil)
	if err != nil {
		return nil, err
	}
	for _, c := range second.Save {
		if pr := res[locKey(c.Steps[0])]; pr.clickable() {
			return &target{layer: 2, chain: c, probe: pr, open: true}, nil
		}
	}
	return nil, nil
}

// behindClosedRoot reports whether a probe found no control at all and
// at least one is behind a closed shadow root.
func behindClosedRoot(res map[string]probeResult) bool {
	closed := false
	for _, pr := range res {
		if pr.Found {
			return false
		}
		closed = closed || pr.Closed
	}
	return closed
}

// waitClickable waits up to --banner-wait for the first of chains whose
// first step is clickable inside in (nil: anywhere on the page).
func (d *driver) waitClickable(ctx context.Context, t consent.Tab, p *consent.Plan, chains []chain, in []consent.Locator) (*chain, error) {
	var locs []consent.Locator
	for _, c := range chains {
		locs = append(locs, c.Steps[0])
	}
	deadline := t.Now() + p.BannerWait.Milliseconds()
	for {
		res, err := probe(ctx, t, locs, in)
		if err != nil && ctx.Err() != nil {
			return nil, ctx.Err()
		}
		if err == nil {
			for i := range chains {
				if res[locKey(chains[i].Steps[0])].clickable() {
					return &chains[i], nil
				}
			}
		}
		if t.Now() >= deadline {
			return nil, nil
		}
		if err := d.sleep(ctx, pollEvery); err != nil {
			return nil, err
		}
	}
}

// viaButton clicks the target as a visitor does and verifies the result.
func (d *driver) viaButton(ctx context.Context, t consent.Tab, p *consent.Plan, r *rule, k consent.RunKind) (*consent.Choice, consent.Reason, error) {
	tg, reason, err := d.locate(ctx, t, p, r, k)
	if tg == nil {
		return nil, reason, err
	}
	before, err := stateNow(ctx, t, r)
	if err != nil {
		return nil, consent.ReasonNone, err
	}
	ch := tg.choice()
	if tg.layer == 1 {
		return d.clickAndVerify(ctx, t, p, r, k, tg.chain, tg.within, ch, before)
	}

	// Second layer: open the settings (unless they are open already),
	// wait for Save, switch every enabled toggle off, then save.
	save := &tg.chain
	if !tg.open {
		if reason, err := d.clickSteps(ctx, t, p, tg.chain.Steps, tg.within, ch); reason != consent.ReasonNone || err != nil {
			return ch, reason, err
		}
		if r.ID == genericID {
			return d.genericSettings(ctx, t, p, r, k, tg, ch, before)
		}
		save, err = d.waitClickable(ctx, t, p, r.Second.Save, nil)
		if err != nil {
			return ch, consent.ReasonNone, err
		}
		if save == nil {
			return ch, consent.ReasonNoButton, nil
		}
		ch.Selector = consent.SafeText(tg.chain.Raw+" >> "+save.Raw, selectorWidth)
	}
	if err := switchOff(ctx, t, r.Second.Toggles, false); err != nil {
		return ch, consent.ReasonNone, err
	}
	if err := switchOff(ctx, t, r.Second.Press, true); err != nil {
		return ch, consent.ReasonNone, err
	}
	return d.clickAndVerify(ctx, t, p, r, k, *save, nil, ch, before)
}

// clickSteps clicks each of steps once, in order, waiting for each to be
// clickable; the first inside in, the rest anywhere (a step can open a
// dialog outside the banner).
func (d *driver) clickSteps(ctx context.Context, t consent.Tab, p *consent.Plan, steps []consent.Locator, in []consent.Locator, ch *consent.Choice) (consent.Reason, error) {
	for i, l := range steps {
		where := in
		if i > 0 {
			where = nil
		}
		c, err := d.waitClickable(ctx, t, p, []chain{one(l)}, where)
		if err != nil {
			return consent.ReasonNone, err
		}
		if c == nil {
			return consent.ReasonNoButton, nil
		}
		res, err := t.Click(ctx, l)
		if err != nil {
			return consent.ReasonNone, err
		}
		if res.NavigatedAway {
			return consent.ReasonNavigation, nil
		}
		if !res.Clicked {
			return consent.ReasonClickMissed, nil
		}
		ch.AtMS = res.AtMS
	}
	return consent.ReasonNone, nil
}

// switchOff clicks every enabled switch of locs that is on, once (with
// press, every off button not yet pressed). A switch still on afterwards
// shows in the stored state the verify step reads.
func switchOff(ctx context.Context, t consent.Tab, locs []consent.Locator, press bool) error {
	for _, l := range locs {
		list, err := toggles(ctx, t, l, press)
		if err != nil {
			return err
		}
		for _, tg := range list {
			if !tg.On || tg.Disabled || tg.Path == "" {
				continue
			}
			if _, err := t.Click(ctx, consent.Locator{CSS: tg.Path, Shadow: l.Shadow, Frame: l.Frame}); err != nil {
				return err
			}
		}
	}
	return nil
}

// clickAndVerify clicks c (the steps before the last once, the last up
// to maxClicks times) and waits up to --settle after each click of the
// last step for the CMP to store the choice and close its banner.
func (d *driver) clickAndVerify(ctx context.Context, t consent.Tab, p *consent.Plan, r *rule, k consent.RunKind, c chain, in []consent.Locator, ch *consent.Choice, before *consent.CMPState) (*consent.Choice, consent.Reason, error) {
	last := c.Steps[len(c.Steps)-1]
	if len(c.Steps) > 1 {
		if reason, err := d.clickSteps(ctx, t, p, c.Steps[:len(c.Steps)-1], in, ch); reason != consent.ReasonNone || err != nil {
			return ch, reason, err
		}
		in = nil
	}
	for attempt := 1; attempt <= maxClicks; attempt++ {
		if attempt > 1 {
			// Wait for the control again: a retry needs it there and
			// clickable, never a click at where it used to be.
			got, err := d.waitClickable(ctx, t, p, []chain{one(last)}, in)
			if err != nil {
				return ch, consent.ReasonNone, err
			}
			if got == nil {
				return ch, consent.ReasonClickMissed, nil
			}
		}
		res, err := t.Click(ctx, last)
		if err != nil {
			return ch, consent.ReasonNone, err
		}
		ch.Clicks++
		if res.NavigatedAway {
			return ch, consent.ReasonNavigation, nil
		}
		if !res.Clicked {
			if err := d.sleep(ctx, pollEvery); err != nil {
				return ch, consent.ReasonNone, err
			}
			continue
		}
		ch.AtMS = res.AtMS
		if res.Text != "" {
			ch.Text = consent.SafeText(res.Text, textWidth)
		}
		if res.Box != (consent.Box{}) {
			b := res.Box
			ch.Box = &b
		}
		verified, changed, st, err := d.settle(ctx, t, p, r, k, last, before)
		if err != nil {
			return ch, consent.ReasonNone, err
		}
		ch.State = st
		if verified && r != nil && r.ID == genericID {
			// The generic rule has no stored state to read: the banner
			// must stay gone when the page loads again.
			gone, err := d.staysGone(ctx, t, p)
			if err != nil {
				return ch, consent.ReasonNone, err
			}
			if !gone {
				return ch, consent.ReasonStateUnchanged, nil
			}
		}
		if verified {
			ch.Verified = true
			ch.VerifiedBy = verifiedBy(r, st)
			return ch, consent.ReasonNone, nil
		}
		if changed {
			// The banner closed or the state moved, yet not to the choice:
			// hiding a banner is never a Reject.
			return ch, consent.ReasonStateUnchanged, nil
		}
	}
	return ch, consent.ReasonClickMissed, nil
}

// settle polls up to --settle for the effect of a click: the stored
// state matching the choice and the banner gone (for a rule judged by
// its banner, or a custom selector on an unknown banner, the banner or
// the clicked control gone). changed reports whether anything moved.
func (d *driver) settle(ctx context.Context, t consent.Tab, p *consent.Plan, r *rule, k consent.RunKind, clicked consent.Locator, before *consent.CMPState) (verified, changed bool, st *consent.CMPState, err error) {
	deadline := t.Now() + p.Settle.Milliseconds()
	for {
		st, err = stateNow(ctx, t, r)
		if err != nil {
			return false, false, nil, err
		}
		showing, err := d.stillShowing(ctx, t, r, clicked)
		if err != nil {
			return false, false, st, err
		}
		changed = !showing || !sameState(before, st)
		if !showing && (r == nil || r.Verify.Parser == "banner" || satisfied(r, k, st)) {
			if st == nil {
				id := ""
				if r != nil {
					id = r.ID
				}
				st = &consent.CMPState{CMP: id, Source: "banner", Decided: true}
			}
			return true, true, st, nil
		}
		if t.Now() >= deadline {
			return false, changed, st, nil
		}
		if err := d.sleep(ctx, pollEvery); err != nil {
			return false, changed, st, err
		}
	}
}

// stillShowing is the rule's banner visible test, or for a banner
// without a rule whether the clicked control is still there.
func (d *driver) stillShowing(ctx context.Context, t consent.Tab, r *rule, clicked consent.Locator) (bool, error) {
	if r != nil && r.ID == genericID {
		return genericShowing(ctx, t)
	}
	if r != nil && r.BannerRaw != "" {
		return bannerShowing(ctx, t, r)
	}
	res, err := probe(ctx, t, []consent.Locator{clicked}, nil)
	if err != nil {
		return false, err
	}
	pr := res[locKey(clicked)]
	return pr.Found && pr.Visible, nil
}

// stateNow reads the stored choice; nil for a rule judged by its banner
// or no rule at all.
func stateNow(ctx context.Context, t consent.Tab, r *rule) (*consent.CMPState, error) {
	if r == nil || r.Verify.Parser == "banner" {
		return nil, nil
	}
	return readState(ctx, t, r)
}

func wanted(r *rule, k consent.RunKind) map[string]bool {
	if k == consent.RunAccept {
		return r.Accepted
	}
	return r.Rejected
}

// satisfied reports whether st is the choice k: a TC string read for a
// TCF rule needs purpose 1 (store and access) consented for an Accept
// and no purpose consented for a Reject; any other state the rule's
// categories.
func satisfied(r *rule, k consent.RunKind, st *consent.CMPState) bool {
	if st != nil && st.Source == tcfSource && st.TCF != nil {
		if !st.Decided {
			return false
		}
		if k == consent.RunAccept {
			return slices.Contains(st.TCF.PurposesConsent, 1)
		}
		return len(st.TCF.PurposesConsent) == 0
	}
	return matches(st, wanted(r, k))
}

// matches reports whether st is a decided choice with every category of
// want as want says. A category the CMP doesn't list counts as not
// granted: a missing group was never consented to. A state with no
// categories at all matches no categories: a stored choice whose
// categories couldn't be read is not a verified Reject.
func matches(st *consent.CMPState, want map[string]bool) bool {
	if st == nil || !st.Decided {
		return false
	}
	if len(want) > 0 && len(st.Categories) == 0 {
		return false
	}
	for k, v := range want {
		if st.Categories[k] != v {
			return false
		}
	}
	return true
}

func sameState(a, b *consent.CMPState) bool {
	if a == nil || b == nil {
		return a == b
	}
	return a.Decided == b.Decided && maps.Equal(a.Categories, b.Categories)
}

// verifiedBy names what confirmed the choice: cookie:<name>, api, tcf or
// banner.
func verifiedBy(r *rule, st *consent.CMPState) string {
	switch {
	case st == nil || st.Source == "banner":
		return "banner"
	case strings.HasPrefix(st.Source, "api"):
		return "api"
	case st.Source != "":
		return st.Source
	case r != nil && len(r.Verify.Cookies) > 0:
		return "cookie:" + r.Verify.Cookies[0]
	}
	return "api"
}

// viaAPI makes the choice with the CMP's own fixed function in the
// page's main world and verifies the stored state.
func (d *driver) viaAPI(ctx context.Context, t consent.Tab, p *consent.Plan, r *rule, k consent.RunKind) (*consent.Choice, consent.Reason, error) {
	call := apiCall(r, k)
	if call == "" {
		return nil, consent.ReasonNoButton, nil
	}
	before, err := stateNow(ctx, t, r)
	if err != nil {
		return nil, consent.ReasonNone, err
	}
	ch := apiChoice(call)
	ch.AtMS = t.Now()
	if _, err := t.EvalMain(ctx, call); err != nil {
		if ctx.Err() != nil {
			return ch, consent.ReasonNone, ctx.Err()
		}
		// The CMP's function is missing or threw: the call did nothing.
		return ch, consent.ReasonClickMissed, nil
	}
	deadline := t.Now() + p.Settle.Milliseconds()
	for {
		st, err := stateNow(ctx, t, r)
		if err != nil {
			return ch, consent.ReasonNone, err
		}
		ch.State = st
		ok := satisfied(r, k, st)
		if r.Verify.Parser == "banner" {
			showing, err := bannerShowing(ctx, t, r)
			if err != nil {
				return ch, consent.ReasonNone, err
			}
			ok = !showing
		}
		if ok {
			ch.Verified, ch.VerifiedBy = true, verifiedBy(r, st)
			return ch, consent.ReasonNone, nil
		}
		if t.Now() >= deadline {
			if sameState(before, st) {
				return ch, consent.ReasonClickMissed, nil
			}
			return ch, consent.ReasonStateUnchanged, nil
		}
		if err := d.sleep(ctx, pollEvery); err != nil {
			return ch, consent.ReasonNone, err
		}
	}
}
