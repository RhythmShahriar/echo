// Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
// SPDX-License-Identifier: MIT

package signal

import (
	"errors"
	"fmt"
	"slices"
	"strings"

	"github.com/RhythmShahriar/echo/consent"
)

// Limits on a TC string. The page writes the string (euconsent-v2,
// __tcfapi), so its length fields are untrusted.
const (
	// maxTCString is the longest TC string read. Real ones with every
	// vendor in ranges stay far below it.
	maxTCString = 64 << 10
	// maxVendorID bounds MaxVendorId and NumEntries. The Global Vendor
	// List has about 1,500 vendors; 10,000 leaves room for growth.
	maxVendorID = 10000
)

// The TC string errors, by their exact text.
var (
	errTCLong        = errors.New("TC string too large")
	errTCShort       = errors.New("TC string too short")
	errTCNotBase64   = errors.New("TC string is not base64url")
	errTCVendorLarge = errors.New("TC string vendor section too large")
)

// TCF decodes the core segment of an IAB TCF v2.2 TC string: the version,
// CMP ID, the purposes with consent and with legitimate interest and how
// many vendors have consent. Only the first segment is read (the others,
// split by '.', hold disclosed vendors and publisher purposes). The
// layout follows the IAB "Consent string and vendor list formats v2"
// specification:
// https://github.com/InteractiveAdvertisingBureau/GDPR-Transparency-and-Consent-Framework/blob/master/TCFv2/IAB%20Tech%20Lab%20-%20Consent%20string%20and%20vendor%20list%20formats%20v2.md
//
// Errors: TC string too large (over 64 KiB), TC string is not base64url,
// TC string too short (a field runs past the end), TC string version
// <n> is not 2 and TC string vendor section too large (MaxVendorId or
// NumEntries over 10,000). It never panics and allocates only the
// purpose lists and, for range encoding, the ranges themselves.
func (Decoder) TCF(tc string) (*consent.TCF, error) {
	if len(tc) > maxTCString {
		return nil, errTCLong
	}
	core, _, _ := strings.Cut(tc, ".")
	b, ok := newBits(core)
	if !ok {
		return nil, errTCNotBase64
	}
	out := &consent.TCF{Version: b.int(6)}
	if b.err == nil && out.Version != 2 {
		return nil, fmt.Errorf("TC string version %d is not 2", out.Version)
	}
	b.skip(36 + 36) // Created, LastUpdated
	out.CMPID = b.int(12)
	b.skip(12 + 6 + 12 + 12 + 6 + 1 + 1 + 12) // CmpVersion to SpecialFeatureOptIns
	out.PurposesConsent = b.flags(24)
	if li := b.flags(24); len(li) > 0 {
		out.PurposesLI = li
	}
	b.skip(1 + 12) // PurposeOneTreatment, PublisherCC
	n, err := vendors(b)
	if err != nil {
		return nil, err
	}
	if b.err != nil {
		return nil, errTCShort
	}
	out.VendorsConsent = n
	out.Valid = true
	return out, nil
}

// vendors reads the vendor consent section and counts the vendors with
// consent: a bitfield of MaxVendorId bits, or ranges whose overlaps
// count once.
func vendors(b *bits) (int, error) {
	maxID := b.int(16)
	ranged := b.bit()
	if b.err != nil {
		return 0, errTCShort
	}
	if maxID > maxVendorID {
		return 0, errTCVendorLarge
	}
	if ranged == 0 {
		return b.count(maxID), nil
	}
	n := b.int(12)
	if b.err != nil {
		return 0, errTCShort
	}
	if n > maxVendorID {
		return 0, errTCVendorLarge
	}
	// Each range needs at least 17 bits, so a NumEntries the string
	// can't hold is refused before the slice is made.
	if n*17 > b.left() {
		return 0, errTCShort
	}
	spans := make([][2]int, 0, n)
	for range n {
		isRange := b.bit()
		start := b.int(16)
		end := start
		if isRange == 1 {
			end = b.int(16)
		}
		if b.err != nil {
			return 0, errTCShort
		}
		// Only IDs from 1 to MaxVendorId count; a reversed range has none.
		start, end = max(start, 1), min(end, maxID)
		if start <= end {
			spans = append(spans, [2]int{start, end})
		}
	}
	return union(spans), nil
}

// union counts the integers the closed spans cover, overlaps once.
func union(spans [][2]int) int {
	slices.SortFunc(spans, func(a, b [2]int) int { return a[0] - b[0] })
	total, reach := 0, 0 // reach: highest ID counted so far
	for _, s := range spans {
		lo := max(s[0], reach+1)
		if lo <= s[1] {
			total += s[1] - lo + 1
			reach = s[1]
		}
	}
	return total
}
