// Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
// SPDX-License-Identifier: MIT

package signal

import "github.com/RhythmShahriar/echo/consent"

// Hook parses one hooks.js record.
func (Decoder) Hook([]byte) (*consent.Signal, error) { return nil, errNotBuilt }
