// Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
// SPDX-License-Identifier: MIT

package signal

import (
	"strings"
	"testing"

	"github.com/RhythmShahriar/echo/consent"
)

func TestGCS(t *testing.T) {
	g, d, u, x := consent.Granted, consent.Denied, consent.Unset, consent.Unknown
	tests := []struct {
		raw    string
		known  bool
		ad, an consent.Value
	}{
		{"G100", true, d, d},
		{"G101", true, d, g},
		{"G110", true, g, d},
		{"G111", true, g, g},
		{"G1--", true, u, u},
		{"G1-1", true, u, g},
		// Negatives: never granted, always unknown.
		{"", false, x, x},
		{"g111", false, x, x},
		{"G1110", false, x, x},
		{"G10", false, x, x},
		{"G200", false, x, x},
		{"G1 1", false, x, x},
		{"G1x1", false, x, x},
		{"G111\x00", false, x, x},
	}
	for _, tt := range tests {
		got := Decoder{}.GCS(tt.raw)
		want := consent.GCS{Known: tt.known, AdStorage: tt.ad, AnalyticsStorage: tt.an}
		if got != want {
			t.Errorf("GCS(%q) = %+v, want %+v", tt.raw, got, want)
		}
	}
}

func FuzzGCS(f *testing.F) {
	for _, s := range []string{"G100", "G101", "G110", "G111", "G1--", "G1-1", "", "g111", "G1110", "G10", "G200", "G1 1"} {
		f.Add(s)
	}
	f.Fuzz(func(t *testing.T, raw string) {
		got := Decoder{}.GCS(raw)
		if got != (Decoder{}.GCS(raw)) {
			t.Fatalf("GCS(%q) not deterministic", raw)
		}
		if got.Known && len(raw) != 4 {
			t.Fatalf("GCS(%q) known with length %d", raw, len(raw))
		}
		if !got.Known && (got.AdStorage != consent.Unknown || got.AnalyticsStorage != consent.Unknown) {
			t.Fatalf("GCS(%q) unknown but has values %+v", raw, got)
		}
		for i, v := range []consent.Value{got.AdStorage, got.AnalyticsStorage} {
			if v == consent.Granted && (len(raw) <= 2+i || raw[2+i] != '1') {
				t.Fatalf("GCS(%q) granted without a 1 at position %d", raw, 2+i)
			}
		}
		if !got.Known && strings.HasPrefix(raw, "G1") && len(raw) == 4 && strings.Trim(raw[2:], "01-") == "" {
			t.Fatalf("GCS(%q) should be known", raw)
		}
	})
}
