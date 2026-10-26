// Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
// SPDX-License-Identifier: MIT

// Package signal decodes consent signals: Google's gcs and gcd
// parameters, Consent Mode and other consent calls recorded by the page
// hooks, IAB TCF and GPP strings and each CMP's stored state. Every
// parser is pure Go over bytes with a fuzz target. It registers the
// consent SignalDecoder and StateReader.
package signal
