// Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
// SPDX-License-Identifier: MIT

package cmp

import (
	"context"
	"errors"
	"time"

	"github.com/RhythmShahriar/echo/consent"
)

// errNotBuilt is what the parts of the driver still to come return.
var errNotBuilt = errors.New("consent: CMP detection isn't built yet")

// pollEvery is how often Detect and Choose look at the page again.
const pollEvery = 250 * time.Millisecond

var _ = pollEvery

// Detect isn't built yet.
func (d *driver) Detect(context.Context, consent.Tab, *consent.Plan, time.Duration) (consent.CMPInfo, error) {
	return consent.CMPInfo{}, errNotBuilt
}

// Find isn't built yet.
func (d *driver) Find(context.Context, consent.Tab, *consent.Plan, consent.CMPInfo, consent.RunKind) (*consent.Choice, consent.Reason, error) {
	return nil, consent.ReasonNone, errNotBuilt
}

// Choose isn't built yet.
func (d *driver) Choose(context.Context, consent.Tab, *consent.Plan, consent.CMPInfo, consent.RunKind) (*consent.Choice, consent.Reason, error) {
	return nil, consent.ReasonNone, errNotBuilt
}

// BannerShowing isn't built yet.
func (d *driver) BannerShowing(context.Context, consent.Tab, consent.CMPInfo) (bool, error) {
	return false, errNotBuilt
}
