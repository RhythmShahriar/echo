// Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
// SPDX-License-Identifier: MIT

package cmp

import (
	"context"
	"errors"

	"github.com/RhythmShahriar/echo/consent"
)

// errNotBuilt is what the parts of the driver still to come return.
var errNotBuilt = errors.New("consent: clicking a CMP's banner isn't built yet")

// Find isn't built yet.
func (d *driver) Find(context.Context, consent.Tab, *consent.Plan, consent.CMPInfo, consent.RunKind) (*consent.Choice, consent.Reason, error) {
	return nil, consent.ReasonNone, errNotBuilt
}

// Choose isn't built yet.
func (d *driver) Choose(context.Context, consent.Tab, *consent.Plan, consent.CMPInfo, consent.RunKind) (*consent.Choice, consent.Reason, error) {
	return nil, consent.ReasonNone, errNotBuilt
}
