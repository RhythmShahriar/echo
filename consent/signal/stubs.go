// Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
// SPDX-License-Identifier: MIT

package signal

import "github.com/RhythmShahriar/echo/consent"

// GCD decodes a gcd parameter.
func (Decoder) GCD(string) consent.GCD {
	u := consent.GCDSignal{Default: consent.Unknown, Update: consent.Unknown}
	return consent.GCD{AdStorage: u, AnalyticsStorage: u, AdUserData: u, AdPersonalization: u}
}

// Hook parses one hooks.js record.
func (Decoder) Hook([]byte) (*consent.Signal, error) { return nil, errNotBuilt }
