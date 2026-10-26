// Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
// SPDX-License-Identifier: MIT

package signal

import (
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/RhythmShahriar/echo/consent"
)

// usNatSection writes a usnat core segment with the given SaleOptOut and
// SharingOptOut fields (version 1, the rest zero).
func usNatSection(sale, share uint64) string {
	w := &bitWriter{}
	w.put(1, 6).put(0, 12).put(sale, 2).put(share, 2).put(0, 2).put(0, 24).put(0, 4).put(0, 8)
	return w.String()
}

// encodeGPPHeader writes a GPP header listing single section IDs (sorted).
func encodeGPPHeader(ids ...int) string {
	w := &bitWriter{}
	w.put(3, 6).put(1, 6).put(uint64(len(ids)), 12)
	last := 0
	for _, id := range ids {
		w.put(0, 1).fib(id - last)
		last = id
	}
	return w.String()
}

const tcSection = "CPXxRfAPXxRfAAfKABENB-CgAAAAAAAAAAYgAAAAAAAA"

func TestGPP(t *testing.T) {
	g, d, u, x := consent.Granted, consent.Denied, consent.Unset, consent.Unknown
	tests := []struct {
		name, s     string
		sections    []string
		sale, share consent.Value
		valid       bool
	}{
		// IAB GPP Consent String Specification examples.
		{"iab tcfeuv2", "DBABM~" + tcSection, []string{"tcfeuv2"}, "", "", true},
		{"iab tcfeuv2 and uspv1", "DBACNY~" + tcSection + "~1YNN", []string{"tcfeuv2", "uspv1"}, g, u, true},
		{"iab tcfcav1 and uspv1 (group)", "DBABjw~" + tcSection + "~1YYN", []string{"tcfcav1", "uspv1"}, d, u, true},
		// Bare US Privacy strings.
		// The third character is the opt-out of sale.
		{"1YNN", "1YNN", []string{"uspv1"}, g, u, true},
		{"1YYN", "1YYN", []string{"uspv1"}, d, u, true},
		{"1NNN", "1NNN", []string{"uspv1"}, g, u, true},
		{"1---", "1---", []string{"uspv1"}, u, u, true},
		{"lowercase", "1yyn", []string{"uspv1"}, d, u, true},
		// usnat.
		{"usnat sale opted out", encodeGPPHeader(7) + "~" + usNatSection(1, 2), []string{"usnat"}, d, g, true},
		{"usnat not opted out", encodeGPPHeader(7) + "~" + usNatSection(2, 2), []string{"usnat"}, g, g, true},
		{"usnat not applicable", encodeGPPHeader(7) + "~" + usNatSection(0, 0), []string{"usnat"}, u, u, true},
		{"usnat wins over uspv1", encodeGPPHeader(6, 7) + "~1NNN~" + usNatSection(1, 1), []string{"uspv1", "usnat"}, d, d, true},
		{"usnat with gpc segment", encodeGPPHeader(7) + "~" + usNatSection(1, 0) + ".YA", []string{"usnat"}, d, u, true},
		{"usnat unreadable", encodeGPPHeader(7) + "~*", []string{"usnat"}, x, x, true},
		{"usnat short", encodeGPPHeader(7) + "~BA", []string{"usnat"}, x, x, true},
		{"uspv1 unreadable", encodeGPPHeader(6) + "~1XYZ", []string{"uspv1"}, x, u, true},
		// Listed only, unknown IDs by number.
		{"states and unknown", encodeGPPHeader(8, 9, 10, 11, 12, 40) + "~A~A~A~A~A~A", []string{"usca", "usva", "usco", "usut", "usct", "id:40"}, "", "", true},
		{"missing section", encodeGPPHeader(2, 6) + "~" + tcSection, []string{"tcfeuv2", "uspv1"}, x, u, false},
		{"no sections", encodeGPPHeader(), []string{}, "", "", true},
	}
	for _, tt := range tests {
		got, err := Decoder{}.GPP(tt.s)
		if err != nil {
			t.Fatalf("%s: GPP(%q): %v", tt.name, tt.s, err)
		}
		if !slices.Equal(got.Sections, tt.sections) || got.SaleOptOut != tt.sale || got.ShareOptOut != tt.share || got.Valid != tt.valid {
			t.Errorf("%s: GPP(%q) = %+v", tt.name, tt.s, got)
		}
	}
}

func TestGPPErrors(t *testing.T) {
	w := &bitWriter{}
	w.put(3, 6).put(1, 6).put(65, 12)
	tooMany := w.String()
	group := &bitWriter{}
	group.put(3, 6).put(1, 6).put(1, 12).put(1, 1).fib(1).fib(100)
	longFib := &bitWriter{}
	longFib.put(3, 6).put(1, 6).put(1, 12).put(0, 1).put(0, 40)
	tests := []struct{ name, s, err string }{
		{"empty", "", "GPP string too short"},
		{"malformed base64", "DB*BM~x", "GPP string is not base64url"},
		{"wrong type", "BBABM~x", "GPP string type 1 is not 3"},
		{"cut header", "DBACN", "GPP string too short"},
		{"too many entries", tooMany, "GPP header lists too many sections"},
		{"group too large", group.String(), "GPP header lists too many sections"},
		{"endless fibonacci", longFib.String(), "GPP string too short"},
		{"bad usp", "1ABC", "US Privacy string unreadable"},
		{"usp version 2", "2YNN", "GPP string type 54 is not 3"},
		{"too long", strings.Repeat("D", maxGPPString+1), "GPP string too large"},
	}
	for _, tt := range tests {
		got, err := Decoder{}.GPP(tt.s)
		if err == nil || err.Error() != tt.err || got != nil {
			t.Errorf("%s: GPP(%q) = %v, %v; want %q", tt.name, tt.s, got, err, tt.err)
		}
	}
}

func FuzzGPP(f *testing.F) {
	for _, s := range []string{"DBABM~" + tcSection, "DBACNY~" + tcSection + "~1YNN", "DBABjw~x~1YNN", "1YNN", "1---", "",
		encodeGPPHeader(7) + "~" + usNatSection(1, 2), "DBABM", "D", "~~~~"} {
		f.Add(s)
	}
	f.Fuzz(func(t *testing.T, s string) {
		start := time.Now()
		got, err := Decoder{}.GPP(s)
		if time.Since(start) > time.Second {
			t.Fatalf("GPP(%d bytes) took %v", len(s), time.Since(start))
		}
		again, err2 := Decoder{}.GPP(s)
		if (err == nil) != (err2 == nil) {
			t.Fatalf("GPP(%q) not deterministic", s)
		}
		if err != nil {
			return
		}
		if !slices.Equal(got.Sections, again.Sections) || got.SaleOptOut != again.SaleOptOut || len(got.Sections) > maxSections {
			t.Fatalf("GPP(%q) = %+v, then %+v", s, got, again)
		}
		if got.SaleOptOut == consent.Granted && !strings.ContainsAny(s, "Nn") && !strings.Contains(s, "~") {
			t.Fatalf("GPP(%q) granted without a section", s)
		}
	})
}
