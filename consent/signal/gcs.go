// Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
// SPDX-License-Identifier: MIT

package signal

import "github.com/RhythmShahriar/echo/consent"

// GCS decodes a gcs parameter: G1 and one character each for ad_storage
// and analytics_storage, 0 denied, 1 granted and - unset (G100, G111,
// G1--). Anything else is unknown, never granted.
func (Decoder) GCS(raw string) consent.GCS {
	unknown := consent.GCS{AdStorage: consent.Unknown, AnalyticsStorage: consent.Unknown}
	if len(raw) != 4 || raw[0] != 'G' || raw[1] != '1' {
		return unknown
	}
	ad, ok1 := gcsValue(raw[2])
	an, ok2 := gcsValue(raw[3])
	if !ok1 || !ok2 {
		return unknown
	}
	return consent.GCS{Known: true, AdStorage: ad, AnalyticsStorage: an}
}

// gcsValue reads one gcs position.
func gcsValue(b byte) (consent.Value, bool) {
	switch b {
	case '0':
		return consent.Denied, true
	case '1':
		return consent.Granted, true
	case '-':
		return consent.Unset, true
	}
	return consent.Unknown, false
}
