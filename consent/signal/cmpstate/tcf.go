// Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
// SPDX-License-Identifier: MIT

package cmpstate

import "github.com/RhythmShahriar/echo/consent"

// tcfCookie is where TCF CMPs keep the TC string.
const tcfCookie = "euconsent-v2"

// TCF returns the parser for the euconsent-v2 cookie: the TC string
// decoded by decode (consent/signal's TCF decoder, passed in so this
// package stays free of it). It is decided when the string is valid; the
// purposes are in CMPState.TCF, not in Categories. euconsent-v2 is the
// first-party cookie name TCF CMPs (Didomi, Quantcast, Sourcepoint) use
// by convention; the string format is the IAB's (see consent/signal).
func TCF(decode func(string) (*consent.TCF, error)) Parser {
	return func(values map[string]string) (*consent.CMPState, error) {
		v, ok, long := value(values, tcfCookie)
		if !ok {
			return nil, nil
		}
		const source = "tcf"
		if long {
			return unreadable("tcf", source, tcfCookie)
		}
		tc, err := decode(v)
		if err != nil || tc == nil {
			return unreadable("tcf", source, tcfCookie)
		}
		return &consent.CMPState{CMP: "tcf", Source: source, Decided: tc.Valid, TCF: tc}, nil
	}
}
