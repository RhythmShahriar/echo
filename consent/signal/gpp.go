// Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
// SPDX-License-Identifier: MIT

package signal

import (
	"errors"
	"fmt"
	"strconv"
	"strings"

	"github.com/RhythmShahriar/echo/consent"
)

// Limits on a GPP string, which the page writes (__gpp, the
// __gpp_string cookie).
const (
	// maxGPPString is the longest GPP string read.
	maxGPPString = 16 << 10
	// maxSections bounds the section IDs a header may list. The IAB list
	// has about 25 sections, so more is a broken or hostile string.
	maxSections = 64
)

// The GPP section IDs this decoder names (IAB "Section Information").
const (
	gppTCFEU  = 2
	gppUSPV1  = 6
	gppUSNat  = 7
	gppHeader = 3 // the header's Type field
)

// gppNames names the section IDs Echo knows; others are listed as id:<n>.
var gppNames = map[int]string{
	gppTCFEU: "tcfeuv2", 5: "tcfcav1", gppUSPV1: "uspv1", gppUSNat: "usnat",
	8: "usca", 9: "usva", 10: "usco", 11: "usut", 12: "usct",
}

// The GPP errors, by their exact text.
var (
	errGPPLong      = errors.New("GPP string too large")
	errGPPShort     = errors.New("GPP string too short")
	errGPPNotBase64 = errors.New("GPP string is not base64url")
	errGPPSections  = errors.New("GPP header lists too many sections")
	errUSPBad       = errors.New("US Privacy string unreadable")
)

// GPP decodes an IAB GPP string (header~section~section...) or a bare
// US Privacy string (1YNN, from the usprivacy cookie or __uspapi). The
// header follows the IAB "Consent String Specification" of the Global
// Privacy Platform:
// https://github.com/InteractiveAdvertisingBureau/Global-Privacy-Platform/blob/main/Core/Consent%20String%20Specification.md
//
// Sections lists the header's section names in order. SaleOptOut and
// ShareOptOut say whether selling and sharing are allowed: Denied when
// the visitor opted out, Granted when not, Unset when not applicable and
// Unknown when unreadable. They come from usnat when the string has it,
// else from uspv1 (which has no sharing field, so ShareOptOut is Unset);
// other sections are listed only. Valid is false when the header lists a
// different number of sections than the string carries.
//
// Errors: GPP string too large (over 16 KiB), GPP string is not
// base64url, GPP string too short, GPP string type <n> is not 3, GPP
// header lists too many sections (over 64) and, for a bare string, US
// Privacy string unreadable. It never panics.
func (Decoder) GPP(s string) (*consent.GPP, error) {
	if len(s) > maxGPPString {
		return nil, errGPPLong
	}
	if len(s) == 4 && s[0] == '1' {
		sale, ok := uspSale(s)
		if !ok {
			return nil, errUSPBad
		}
		return &consent.GPP{Sections: []string{"uspv1"}, SaleOptOut: sale, ShareOptOut: consent.Unset, Valid: true}, nil
	}
	parts := strings.Split(s, "~")
	ids, err := gppHeaderIDs(parts[0])
	if err != nil {
		return nil, err
	}
	out := &consent.GPP{Sections: make([]string, 0, len(ids)), Valid: len(ids) == len(parts)-1}
	var usp, usnat *[2]consent.Value
	for i, id := range ids {
		name, ok := gppNames[id]
		if !ok {
			name = "id:" + strconv.Itoa(id)
		}
		out.Sections = append(out.Sections, name)
		data := ""
		if i+1 < len(parts) {
			data = parts[i+1]
		}
		switch id {
		case gppUSPV1:
			sale, ok := uspSale(data)
			if !ok {
				sale = consent.Unknown
			}
			usp = &[2]consent.Value{sale, consent.Unset}
		case gppUSNat:
			v := usNat(data)
			usnat = &v
		}
	}
	if pick := firstNonNil(usnat, usp); pick != nil {
		out.SaleOptOut, out.ShareOptOut = pick[0], pick[1]
	}
	return out, nil
}

// firstNonNil returns the first non-nil pair.
func firstNonNil(ps ...*[2]consent.Value) *[2]consent.Value {
	for _, p := range ps {
		if p != nil {
			return p
		}
	}
	return nil
}

// gppHeaderIDs reads the header: Type 6 bits (3), Version 6 bits and the
// section IDs as a Fibonacci range (NumEntries 12 bits; each entry a
// group bit, a Fibonacci offset from the last ID and, for a group, a
// Fibonacci length).
func gppHeaderIDs(h string) ([]int, error) {
	b, ok := newBits(h)
	if !ok {
		return nil, errGPPNotBase64
	}
	typ := b.int(6)
	b.skip(6) // Version
	n := b.int(12)
	if b.err != nil {
		return nil, errGPPShort
	}
	if typ != gppHeader {
		return nil, fmt.Errorf("GPP string type %d is not 3", typ)
	}
	if n > maxSections {
		return nil, errGPPSections
	}
	ids := make([]int, 0, n)
	last := 0
	for range n {
		group := b.bit()
		start := last + b.fib()
		end := start
		if group == 1 {
			end = start + b.fib()
		}
		if b.err != nil {
			return nil, errGPPShort
		}
		if len(ids)+end-start+1 > maxSections {
			return nil, errGPPSections
		}
		for id := start; id <= end; id++ {
			ids = append(ids, id)
		}
		last = end
	}
	return ids, nil
}

// uspSale reads the opt-out-of-sale character of a US Privacy string
// (version 1, notice, opt-out of sale, LSPA covered): Y opted out
// (Denied), N did not (Granted), - not applicable (Unset). ok is false
// when the string isn't one.
func uspSale(s string) (consent.Value, bool) {
	if len(s) != 4 || s[0] != '1' {
		return consent.Unknown, false
	}
	for i := 1; i < 4; i++ {
		if c := s[i] &^ 0x20; c != 'Y' && c != 'N' && s[i] != '-' {
			return consent.Unknown, false
		}
	}
	switch s[2] {
	case 'Y', 'y':
		return consent.Denied, true
	case 'N', 'n':
		return consent.Granted, true
	}
	return consent.Unset, true
}

// usNat reads SaleOptOut and SharingOptOut from a usnat section's core
// segment: Version 6 bits, six notice fields of 2 bits, then SaleOptOut
// and SharingOptOut, 2 bits each (0 not applicable, 1 opted out, 2 did
// not opt out). The same offsets hold in versions 1 and 2. An unreadable
// section gives Unknown for both.
func usNat(data string) [2]consent.Value {
	core, _, _ := strings.Cut(data, ".")
	b, ok := newBits(core)
	if !ok {
		return [2]consent.Value{consent.Unknown, consent.Unknown}
	}
	b.skip(6 + 6*2)
	sale, share := b.int(2), b.int(2)
	if b.err != nil {
		return [2]consent.Value{consent.Unknown, consent.Unknown}
	}
	return [2]consent.Value{optOut(sale), optOut(share)}
}

// optOut maps a usnat opt-out field to a Value.
func optOut(v int) consent.Value {
	switch v {
	case 0:
		return consent.Unset
	case 1:
		return consent.Denied
	case 2:
		return consent.Granted
	}
	return consent.Unknown
}
