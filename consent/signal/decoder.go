// Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
// SPDX-License-Identifier: MIT

package signal

import "github.com/RhythmShahriar/echo/consent"

// Decoder is the consent.SignalDecoder: Google's gcs and gcd parameters,
// hook records and TCF and GPP strings. It has no state, so the zero
// value is ready and safe for concurrent use.
type Decoder struct{}

var _ consent.SignalDecoder = Decoder{}

func init() { consent.SetSignals(Decoder{}) }
