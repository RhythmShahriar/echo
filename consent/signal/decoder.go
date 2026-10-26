// Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
// SPDX-License-Identifier: MIT

package signal

import (
	"errors"

	"github.com/RhythmShahriar/echo/consent"
)

// Decoder is the consent.SignalDecoder: Google's gcs and gcd parameters,
// hook records and TCF and GPP strings. It has no state, so the zero
// value is ready and safe for concurrent use.
type Decoder struct{}

var _ consent.SignalDecoder = Decoder{}

func init() { consent.SetSignals(Decoder{}) }

// errNotBuilt is what the decoders this build doesn't have yet return.
var errNotBuilt = errors.New("consent: decoder not built yet")

// TCF decodes a TCF consent string.
func (Decoder) TCF(string) (*consent.TCF, error) { return nil, errNotBuilt }

// GPP decodes a GPP string.
func (Decoder) GPP(string) (*consent.GPP, error) { return nil, errNotBuilt }
