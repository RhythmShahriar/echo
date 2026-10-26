// Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
// SPDX-License-Identifier: MIT

package signal

import "github.com/RhythmShahriar/echo/consent"

// GCDUnofficial is the line every consumer of a decoded gcd prints next
// to it: Google doesn't document the parameter.
const GCDUnofficial = "decoded with an unofficial, community-documented mapping"

// NoteLetterE is GCD.Note when a signal uses the letter e, which only
// Didomi lists.
const NoteLetterE = "letter e is documented by one source"

// gcdLetter is what one gcd letter says about a signal's default and
// update.
type gcdLetter struct{ def, upd consent.Value }

// gcdLetters is the community table (Stape, Didomi, Complianz). l means
// Consent Mode isn't set for the signal at all; e is Didomi's alone.
var gcdLetters = map[byte]gcdLetter{
	'l': {consent.Unset, consent.Unset},
	'p': {consent.Denied, consent.Unset},
	'q': {consent.Denied, consent.Denied},
	't': {consent.Granted, consent.Unset},
	'r': {consent.Denied, consent.Granted},
	'm': {consent.Unset, consent.Denied},
	'n': {consent.Unset, consent.Granted},
	'u': {consent.Granted, consent.Denied},
	'v': {consent.Granted, consent.Granted},
	'e': {consent.Unknown, consent.Granted},
}

// gcdLen is the prefix and the four letter and digit pairs.
const gcdLen = 2 + 4*2

// maxExtra caps GCD.Extra in runes.
const maxExtra = 64

// GCD decodes a gcd parameter such as 13p3p3p3p5: a prefix 11 or 13, then
// a letter and a digit for each of ad_storage, analytics_storage,
// ad_user_data and ad_personalization (the first three digits 1 or 3, the
// last 5 or 7). What follows the fourth pair is kept in Extra. A letter
// outside the table leaves that signal unknown; a wrong shape leaves all
// four unknown. GCD.Known is true only when all four are known.
func (Decoder) GCD(raw string) consent.GCD {
	u := consent.GCDSignal{Default: consent.Unknown, Update: consent.Unknown}
	g := consent.GCD{AdStorage: u, AnalyticsStorage: u, AdUserData: u, AdPersonalization: u}
	if !gcdShape(raw) {
		return g
	}
	sigs := [4]*consent.GCDSignal{&g.AdStorage, &g.AnalyticsStorage, &g.AdUserData, &g.AdPersonalization}
	g.Known = true
	for i, s := range sigs {
		c := raw[2+2*i]
		s.Letter = string(c)
		l, ok := gcdLetters[c]
		if !ok {
			g.Known = false
			continue
		}
		s.Default, s.Update, s.Known = l.def, l.upd, true
		if c == 'e' {
			g.Note = NoteLetterE
		}
	}
	// The tail comes from a page's request, so it is made safe to print
	// and capped: nothing reads it.
	g.Extra = consent.SafeText(raw[gcdLen:], maxExtra)
	return g
}

// gcdShape reports whether raw has the prefix, four letters and the
// separators gcd always carries.
func gcdShape(raw string) bool {
	if len(raw) < gcdLen || raw[0] != '1' || (raw[1] != '1' && raw[1] != '3') {
		return false
	}
	for i := range 4 {
		c, d := raw[2+2*i], raw[3+2*i]
		if !(c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z') {
			return false
		}
		if i < 3 && d != '1' && d != '3' || i == 3 && d != '5' && d != '7' {
			return false
		}
	}
	return true
}
