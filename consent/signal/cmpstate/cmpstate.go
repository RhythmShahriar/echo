// Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
// SPDX-License-Identifier: MIT

// Package cmpstate reads the choice a consent management platform
// stored: one pure parser per CMP over its cookie or storage values
// (Cookiebot, OneTrust, CookieYes, Complianz, Usercentrics, Borlabs,
// Didomi, iubenda and the IAB TCF string). A parser gets the values by
// cookie or storage name and returns the CMP's categories under the
// CMP's own names. Values are read and dropped: nothing a parser returns
// or reports carries one, only category names and whether the visitor
// decided.
package cmpstate

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"strings"

	"github.com/RhythmShahriar/echo/consent"
	"github.com/RhythmShahriar/echo/mask"
)

// Parser reads a CMP's stored choice from its values, keyed by cookie or
// storage name and already URL-decoded once by the caller. It returns
// nil, nil when none of the CMP's values is there (no choice stored yet)
// and an undecided state with ErrUnreadable when a value is there but
// can't be read.
type Parser func(values map[string]string) (*consent.CMPState, error)

// Limits on what a parser reads. The page writes the values.
const (
	// MaxValue is the longest value a parser reads; a longer one is
	// unreadable. Usercentrics keeps a history per service, so its
	// storage value is the largest of the formats.
	MaxValue = 64 << 10
	// maxCategories bounds how many categories a state carries.
	maxCategories = 100
	// maxName bounds a category name, in runes.
	maxName = 64
	// maxDecode bounds how many more times a value is URL-decoded.
	maxDecode = 2
)

// ErrUnreadable is what a parser returns for a value that is there but
// isn't in the CMP's format. Its text never carries the value.
var ErrUnreadable = errors.New("unreadable")

// unreadable returns the undecided state for cmp and the error naming
// the value that couldn't be read (by name, never by content).
func unreadable(cmp, source, name string) (*consent.CMPState, error) {
	return &consent.CMPState{CMP: cmp, Source: source}, fmt.Errorf("consent: %s state in %s %w", cmp, name, ErrUnreadable)
}

// Parsers are the parsers by the name a CMP rule's verify.parser gives.
// TCF needs a decoder, so it is added by consent/signal (see TCF).
var Parsers = map[string]Parser{
	"cookiebot":    Cookiebot,
	"onetrust":     OneTrust,
	"cookieyes":    CookieYes,
	"complianz":    Complianz,
	"usercentrics": Usercentrics,
	"borlabs":      Borlabs,
	"didomi":       Didomi,
	"iubenda":      Iubenda,
}

// value returns values[name] when it is set and not too long; ok is
// false when it is missing or empty, long is true when it is too long.
func value(values map[string]string, name string) (v string, ok, long bool) {
	v = values[name]
	if v == "" {
		return "", false, false
	}
	return v, true, len(v) > MaxValue
}

// decodings returns v and up to maxDecode further URL decodings of it,
// so a parser can try each: browsers hand cookies over as they were set
// and CMPs encode once or twice.
func decodings(v string) []string {
	out := []string{v}
	for range maxDecode {
		if !strings.Contains(v, "%") {
			break
		}
		d, err := url.PathUnescape(v)
		if err != nil || d == v {
			break
		}
		v = d
		out = append(out, v)
	}
	return out
}

// jsonValue unmarshals the first decoding of v that is JSON into dst.
func jsonValue(v string, dst any) bool {
	for _, d := range decodings(v) {
		if json.Unmarshal([]byte(d), dst) == nil {
			return true
		}
	}
	return false
}

// categories collects category names and their state within the caps.
type categories map[string]bool

// set records name as granted or not, once the name is made safe; names
// past the cap or empty are dropped. A name set twice keeps false if
// either was false. The page writes the names, so one the masker would
// change (an API key, a token, a URL with a credential) is dropped
// whole: a masked key could still collide with a real one and a
// category named like a secret says nothing about consent. The check
// runs before SafeText cuts the name, since a cut secret may no longer
// look like one.
func (c categories) set(name string, granted bool) {
	name = strings.TrimSpace(name)
	if mask.Text(name) != name {
		return
	}
	name = consent.SafeText(name, maxName)
	if name == "" {
		return
	}
	if old, ok := c[name]; ok {
		c[name] = old && granted
		return
	}
	if len(c) < maxCategories {
		c[name] = granted
	}
}

// orNil returns nil for no categories, so the JSON leaves them out.
func (c categories) orNil() map[string]bool {
	if len(c) == 0 {
		return nil
	}
	return c
}

// Where is where a parser looks when a CMP rule names no cookies or
// storage keys: each CMP's default names (a trailing * is a prefix).
type Where struct {
	Cookies, Storage []string
}

// Defaults are the default names per parser.
var Defaults = map[string]Where{
	"cookiebot":    {Cookies: []string{cookiebotCookie}},
	"onetrust":     {Cookies: []string{oneTrustCookie, oneTrustClosed}},
	"cookieyes":    {Cookies: []string{cookieYesCookie}},
	"complianz":    {Cookies: []string{complianzPrefix + "*"}},
	"usercentrics": {Storage: []string{ucSettings, ucData, ucInteraction}},
	"borlabs":      {Cookies: []string{borlabsCookie}},
	"didomi":       {Cookies: []string{didomiCookie}},
	"iubenda":      {Cookies: []string{iubendaPrefix + "*"}},
	"tcf":          {Cookies: []string{tcfCookie}},
}
