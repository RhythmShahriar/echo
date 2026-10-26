// Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
// SPDX-License-Identifier: MIT

package signal

import (
	"runtime"
	"slices"
	"strings"
	"testing"
)

// bitWriter builds base64url bit strings for the TCF and GPP tests.
type bitWriter struct{ b strings.Builder }

// put writes v as an n-bit field.
func (w *bitWriter) put(v uint64, n int) *bitWriter {
	for i := n - 1; i >= 0; i-- {
		w.b.WriteByte('0' + byte(v>>uint(i)&1))
	}
	return w
}

// fib writes v (at least 1) Fibonacci-coded, ended by an extra 1.
func (w *bitWriter) fib(v int) *bitWriter {
	f := []int{1, 2}
	for f[len(f)-1] <= v {
		f = append(f, f[len(f)-1]+f[len(f)-2])
	}
	code := make([]byte, len(f))
	for i := range code {
		code[i] = '0'
	}
	last := 0
	for i := len(f) - 1; i >= 0; i-- {
		if f[i] <= v {
			v -= f[i]
			code[i] = '1'
			last = max(last, i)
		}
	}
	w.b.Write(code[:last+1])
	w.b.WriteByte('1')
	return w
}

// String returns the bits as base64url, padded with 0 bits to a whole
// character.
func (w *bitWriter) String() string {
	s := w.b.String()
	for len(s)%6 != 0 {
		s += "0"
	}
	const alpha = "ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz0123456789-_"
	var out strings.Builder
	for i := 0; i < len(s); i += 6 {
		v := 0
		for _, c := range s[i : i+6] {
			v = v<<1 | int(c-'0')
		}
		out.WriteByte(alpha[v])
	}
	return out.String()
}

// tcSpec is what encodeTC writes.
type tcSpec struct {
	version, cmp int
	consent, li  []int // purposes, 1 to 24
	maxVendor    int
	vendors      []int    // bitfield: the vendors with consent
	ranges       [][2]int // range encoding when set
}

// mask24 turns purpose numbers into the 24-bit field.
func mask24(ps []int) uint64 {
	var m uint64
	for _, p := range ps {
		m |= 1 << uint(24-p)
	}
	return m
}

// encodeTC writes a TC string core segment.
func encodeTC(s tcSpec) string {
	w := &bitWriter{}
	w.put(uint64(s.version), 6).put(16504920000, 36).put(16504920000, 36).put(uint64(s.cmp), 12)
	w.put(1, 12).put(1, 6).put(269, 12).put(126, 12).put(4, 6).put(0, 1).put(0, 1).put(0, 12)
	w.put(mask24(s.consent), 24).put(mask24(s.li), 24).put(0, 1).put(196, 12)
	w.put(uint64(s.maxVendor), 16)
	if s.ranges == nil {
		w.put(0, 1)
		for id := 1; id <= s.maxVendor; id++ {
			v := uint64(0)
			if slices.Contains(s.vendors, id) {
				v = 1
			}
			w.put(v, 1)
		}
	} else {
		w.put(1, 1).put(uint64(len(s.ranges)), 12)
		for _, r := range s.ranges {
			if r[0] == r[1] {
				w.put(0, 1).put(uint64(r[0]), 16)
			} else {
				w.put(1, 1).put(uint64(r[0]), 16).put(uint64(r[1]), 16)
			}
		}
	}
	// The vendor legitimate interest section, empty.
	w.put(0, 16).put(0, 1)
	return w.String()
}

func TestTCFSpecExamples(t *testing.T) {
	tests := []struct {
		name, tc         string
		cmp, vendors     int
		consent, li      []int
	}{
		// IAB TCF v2.2 GPP spec example (the TCF section of DBABM~...).
		{"iab gpp example", "CPXxRfAPXxRfAAfKABENB-CgAAAAAAAAAAYgAAAAAAAA", 31, 0, []int{}, nil},
		// IAB TCF v2 specification example, with its disclosed vendors segment.
		{"iab tcf example", "COvFyGBOvFyGBAbAAAENAPCAAOAAAAAAAAAAAEEUACCKAAA.IFoEUQQgAIQwgIwQABAEAAAAOIAACAIAAAAQAIAgEAACEAAAAAgAQBAAAAAAAGBAAgAAAAAAAAFAAECAAAgAAQARAEQAAAAAJAAIAAgAAAYQEAAAQmAgBC3ZAYzUw", 27, 3, []int{1, 2, 3}, nil},
	}
	for _, tt := range tests {
		got, err := Decoder{}.TCF(tt.tc)
		if err != nil {
			t.Fatalf("%s: %v", tt.name, err)
		}
		if !got.Valid || got.Version != 2 || got.CMPID != tt.cmp || got.VendorsConsent != tt.vendors ||
			!slices.Equal(got.PurposesConsent, tt.consent) || !slices.Equal(got.PurposesLI, tt.li) {
			t.Errorf("%s: %+v", tt.name, got)
		}
	}
}

func TestTCFEncoded(t *testing.T) {
	tests := []struct {
		name    string
		spec    tcSpec
		vendors int
	}{
		{"purposes 1 and 3", tcSpec{version: 2, cmp: 7, consent: []int{1, 3}, li: []int{2, 7}, maxVendor: 10, vendors: []int{1, 5, 10}}, 3},
		{"all denied", tcSpec{version: 2, cmp: 300, consent: nil, maxVendor: 0}, 0},
		{"all granted", tcSpec{version: 2, cmp: 4095, consent: []int{1, 2, 3, 4, 5, 6, 7, 8, 9, 10, 11, 24}, maxVendor: 4, vendors: []int{1, 2, 3, 4}}, 4},
		{"ranges with overlap", tcSpec{version: 2, cmp: 1, consent: []int{1}, maxVendor: 900,
			ranges: [][2]int{{10, 20}, {15, 30}, {100, 100}, {800, 900}, {5, 5}}}, 21 + 1 + 101 + 1},
		{"range past max", tcSpec{version: 2, cmp: 1, maxVendor: 50, ranges: [][2]int{{40, 60}, {70, 80}}}, 11},
	}
	for _, tt := range tests {
		got, err := Decoder{}.TCF(encodeTC(tt.spec))
		if err != nil {
			t.Fatalf("%s: %v", tt.name, err)
		}
		want := tt.spec.consent
		if want == nil {
			want = []int{}
		}
		var li []int
		if len(tt.spec.li) > 0 {
			li = tt.spec.li
		}
		if !got.Valid || got.CMPID != tt.spec.cmp || got.VendorsConsent != tt.vendors ||
			!slices.Equal(got.PurposesConsent, want) || !slices.Equal(got.PurposesLI, li) {
			t.Errorf("%s: %+v, want vendors %d purposes %v li %v", tt.name, got, tt.vendors, want, li)
		}
	}
}

func TestTCFErrors(t *testing.T) {
	tests := []struct{ name, tc, err string }{
		{"empty", "", "TC string too short"},
		{"bad base64", "CPXx*RfA", "TC string is not base64url"},
		{"standard base64", "CPXx+RfA/", "TC string is not base64url"},
		{"version 1", encodeTC(tcSpec{version: 1}), "TC string version 1 is not 2"},
		{"bitfield too large", encodeTC(tcSpec{version: 2, maxVendor: 10001}), "TC string vendor section too large"},
		{"too long", strings.Repeat("A", maxTCString+1), "TC string too large"},
	}
	for _, tt := range tests {
		got, err := Decoder{}.TCF(tt.tc)
		if err == nil || err.Error() != tt.err || got != nil {
			t.Errorf("%s: %v, %v; want %q", tt.name, got, err, tt.err)
		}
	}
	// NumEntries over 10,000 can't be written in 12 bits; a NumEntries
	// the string can't hold is too short.
	w := &bitWriter{}
	w.put(2, 6).put(0, 207).put(100, 16).put(1, 1).put(4095, 12)
	if _, err := (Decoder{}).TCF(w.String()); err == nil || err.Error() != "TC string too short" {
		t.Errorf("NumEntries past the end: %v", err)
	}
	// Padding is accepted.
	if _, err := (Decoder{}).TCF("CPXxRfAPXxRfAAfKABENB-CgAAAAAAAAAAYgAAAAAAAA=="); err != nil {
		t.Errorf("padded: %v", err)
	}
}

// TestTCFTruncated cuts valid strings at every length: each is an error
// or a valid reading, never a panic.
func TestTCFTruncated(t *testing.T) {
	for _, full := range []string{
		"COvFyGBOvFyGBAbAAAENAPCAAOAAAAAAAAAAAEEUACCKAAA",
		encodeTC(tcSpec{version: 2, cmp: 7, consent: []int{1, 3}, maxVendor: 10, vendors: []int{1, 5}}),
		encodeTC(tcSpec{version: 2, maxVendor: 900, ranges: [][2]int{{1, 9}, {20, 900}}}),
	} {
		for n := range len(full) {
			got, err := Decoder{}.TCF(full[:n])
			if err == nil && !got.Valid {
				t.Fatalf("TCF(%q) no error and not valid", full[:n])
			}
			// A cut through the core header must fail: it is 230 bits
			// at least (39 characters).
			if n < 39 && (err == nil || err.Error() != "TC string too short") {
				t.Fatalf("TCF(%q) = %v, want too short", full[:n], err)
			}
		}
	}
}

// TestTCFAllocs feeds the worst cases: the largest bitfield allowed, a
// MaxVendorId of 65535 and 64 KiB of input. None may allocate 1 MiB.
func TestTCFAllocs(t *testing.T) {
	big := encodeTC(tcSpec{version: 2, maxVendor: maxVendorID, vendors: []int{1, 2, 3}})
	w := &bitWriter{}
	w.put(2, 6).put(0, 207).put(65535, 16).put(0, 1)
	huge := w.String() + strings.Repeat("_", maxTCString-len(w.String()))
	ranged := &bitWriter{}
	ranged.put(2, 6).put(0, 207).put(10000, 16).put(1, 1).put(4095, 12)
	for range 4095 {
		ranged.put(1, 1).put(1, 16).put(10000, 16)
	}
	for _, tc := range []string{big, huge, ranged.String(), strings.Repeat("A", maxTCString)} {
		var before, after runtime.MemStats
		runtime.GC()
		runtime.ReadMemStats(&before)
		_, _ = Decoder{}.TCF(tc)
		runtime.ReadMemStats(&after)
		if d := after.TotalAlloc - before.TotalAlloc; d > 1<<20 {
			t.Errorf("TCF of %d bytes allocated %d bytes", len(tc), d)
		}
		allocs := testing.AllocsPerRun(5, func() { _, _ = Decoder{}.TCF(tc) })
		if allocs > 40 {
			t.Errorf("TCF of %d bytes: %v allocations", len(tc), allocs)
		}
	}
	if got, _ := (Decoder{}).TCF(ranged.String()); got == nil || got.VendorsConsent != 10000 {
		t.Errorf("4095 overlapping ranges: %+v", got)
	}
}

func FuzzTCString(f *testing.F) {
	f.Add("CPXxRfAPXxRfAAfKABENB-CgAAAAAAAAAAYgAAAAAAAA")
	f.Add("COvFyGBOvFyGBAbAAAENAPCAAOAAAAAAAAAAAEEUACCKAAA.IFoEUQQgAIQwgIwQ")
	f.Add(encodeTC(tcSpec{version: 2, cmp: 7, consent: []int{1, 3}, maxVendor: 10, vendors: []int{1, 5}}))
	f.Add(encodeTC(tcSpec{version: 2, maxVendor: 900, ranges: [][2]int{{1, 9}, {20, 900}}}))
	f.Add("")
	f.Add("____________________________________________________")
	f.Fuzz(func(t *testing.T, tc string) {
		got, err := Decoder{}.TCF(tc)
		again, err2 := Decoder{}.TCF(tc)
		if (err == nil) != (err2 == nil) || (got == nil) != (again == nil) {
			t.Fatalf("TCF(%q) not deterministic", tc)
		}
		if err != nil {
			if got != nil {
				t.Fatalf("TCF(%q) returned a value with %v", tc, err)
			}
			return
		}
		if !got.Valid || got.Version != 2 || got.VendorsConsent < 0 || got.VendorsConsent > maxVendorID {
			t.Fatalf("TCF(%q) = %+v", tc, got)
		}
		if !slices.Equal(got.PurposesConsent, again.PurposesConsent) || got.VendorsConsent != again.VendorsConsent {
			t.Fatalf("TCF(%q) not deterministic", tc)
		}
		for _, p := range append(slices.Clone(got.PurposesConsent), got.PurposesLI...) {
			if p < 1 || p > 24 {
				t.Fatalf("TCF(%q) purpose %d", tc, p)
			}
		}
	})
}
