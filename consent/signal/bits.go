// Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
// SPDX-License-Identifier: MIT

package signal

import "errors"

// errShort is what a bit reader returns when a field runs past the end.
// TCF and GPP wrap it in their own message.
var errShort = errors.New("too short")

// bits reads big-endian bit fields straight from base64url text, six
// bits a character, the way the IAB TCF and GPP decoders do. Reading the
// characters in place means a string never costs more memory than its
// own length, whatever its length fields claim.
type bits struct {
	s   string // base64url characters, padding already dropped
	pos int    // next bit
	err error  // first error; later reads return 0
}

// newBits checks s is base64url (trailing = padding is dropped) and
// returns a reader over it.
func newBits(s string) (*bits, bool) {
	for len(s) > 0 && s[len(s)-1] == '=' {
		s = s[:len(s)-1]
	}
	for i := 0; i < len(s); i++ {
		if b64(s[i]) < 0 {
			return nil, false
		}
	}
	return &bits{s: s}, true
}

// b64 is the value of one base64url character, -1 for any other byte.
func b64(c byte) int {
	switch {
	case c >= 'A' && c <= 'Z':
		return int(c - 'A')
	case c >= 'a' && c <= 'z':
		return int(c-'a') + 26
	case c >= '0' && c <= '9':
		return int(c-'0') + 52
	case c == '-':
		return 62
	case c == '_':
		return 63
	}
	return -1
}

// left is how many bits are still unread.
func (b *bits) left() int { return len(b.s)*6 - b.pos }

// bit reads one bit.
func (b *bits) bit() int {
	if b.err != nil {
		return 0
	}
	if b.left() < 1 {
		b.err = errShort
		return 0
	}
	v := b64(b.s[b.pos/6]) >> (5 - b.pos%6) & 1
	b.pos++
	return v
}

// int reads an n-bit unsigned field (n at most 62).
func (b *bits) int(n int) int {
	if b.err != nil {
		return 0
	}
	if b.left() < n {
		b.err = errShort
		return 0
	}
	v := 0
	for range n {
		v = v<<1 | b.bit()
	}
	return v
}

// skip moves past n bits without reading them.
func (b *bits) skip(n int) {
	if b.err != nil {
		return
	}
	if n < 0 || b.left() < n {
		b.err = errShort
		return
	}
	b.pos += n
}

// flags reads an n-bit field of flags and returns the 1-based numbers of
// the bits set (purposes, special features), never nil.
func (b *bits) flags(n int) []int {
	out := []int{}
	for i := 1; i <= n; i++ {
		if b.bit() == 1 {
			out = append(out, i)
		}
	}
	if b.err != nil {
		return []int{}
	}
	return out
}

// count reads n bits and returns how many are set.
func (b *bits) count(n int) int {
	if b.err != nil {
		return 0
	}
	if n < 0 || b.left() < n {
		b.err = errShort
		return 0
	}
	c := 0
	for range n {
		c += b.bit()
	}
	return c
}

// maxFib bounds a Fibonacci code: 30 terms already pass any section ID
// the GPP list has, so a longer run of bits is a broken string.
const maxFib = 30

// fib reads one Fibonacci-coded integer (Zeckendorf bits for 1, 2, 3,
// 5, 8 and so on, ended by a second 1 in a row), as the GPP header
// encodes section ID offsets.
func (b *bits) fib() int {
	v, f1, f2, prev := 0, 1, 2, 0
	for range maxFib {
		x := b.bit()
		if b.err != nil {
			return 0
		}
		if x == 1 && prev == 1 {
			return v
		}
		if x == 1 {
			v += f1
		}
		prev = x
		f1, f2 = f2, f1+f2
	}
	b.err = errFibLong
	return 0
}

// errFibLong is a Fibonacci code longer than maxFib terms.
var errFibLong = errors.New("bad Fibonacci number")
