// Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
// SPDX-License-Identifier: MIT

package cmpstate

import (
	"bytes"
	"encoding/json"
	"strings"

	"github.com/RhythmShahriar/echo/consent"
)

// apiSource is CMPState.Source for a state read from the CMP's API.
const apiSource = "api"

// API reads what a CMP rule's state_js returned (JSON, or a JSON string
// holding JSON, as JSON.stringify gives it) for the named parser. It
// returns nil, nil for null, empty or a shape it doesn't know, so the
// stored cookie decides. Any parser accepts the generic shape
// {"decided": bool, "categories": {name: bool}}; the others are:
//
//   - cookiebot: Cookiebot.consent, read like the cookie.
//   - onetrust: {"groups": ",C0001,C0003,"} (OnetrustActiveGroups); never
//     decided, since the active groups exist before a choice.
//   - cookieyes: getCkyConsent(), {"categories": {...},
//     "isUserActionCompleted": bool}.
//   - usercentrics: [{"id", "status"}] per service; never decided.
//   - borlabs: BorlabsCookie.Consents.getAll(), groups to services;
//     decided when it isn't null.
func API(parser string, raw []byte) (*consent.CMPState, error) {
	raw = bytes.TrimSpace(raw)
	if len(raw) > MaxValue {
		return unreadable(parser, apiSource, "the API state")
	}
	var s string
	if json.Unmarshal(raw, &s) == nil {
		raw = []byte(strings.TrimSpace(s))
	}
	if len(raw) == 0 || string(raw) == "null" {
		return nil, nil
	}
	var generic struct {
		Decided    *bool           `json:"decided"`
		Categories map[string]bool `json:"categories"`
	}
	if json.Unmarshal(raw, &generic) == nil && generic.Decided != nil {
		cats := categories{}
		for k, v := range generic.Categories {
			cats.set(k, v)
		}
		return &consent.CMPState{CMP: parser, Source: apiSource, Decided: *generic.Decided, Categories: cats.orNil()}, nil
	}
	switch parser {
	case "cookiebot":
		if obj, ok := jsObject(string(raw)); ok {
			st := cookiebotState(obj, apiSource)
			return st, nil
		}
	case "onetrust":
		var g struct {
			Groups *string `json:"groups"`
		}
		if json.Unmarshal(raw, &g) == nil && g.Groups != nil {
			cats := categories{}
			for id := range strings.SplitSeq(*g.Groups, ",") {
				cats.set(id, true)
			}
			return &consent.CMPState{CMP: parser, Source: apiSource, Categories: cats.orNil()}, nil
		}
	case "cookieyes":
		var c struct {
			Categories map[string]bool `json:"categories"`
			Done       bool            `json:"isUserActionCompleted"`
		}
		if json.Unmarshal(raw, &c) == nil && c.Categories != nil {
			cats := categories{}
			for k, v := range c.Categories {
				cats.set(k, v)
			}
			return &consent.CMPState{CMP: parser, Source: apiSource, Decided: c.Done, Categories: cats.orNil()}, nil
		}
	case "usercentrics":
		var svcs []struct {
			Status bool `json:"status"`
		}
		if json.Unmarshal(raw, &svcs) == nil && svcs != nil {
			cats := categories{}
			for _, s := range svcs {
				ucCount(cats, "", s.Status)
			}
			return &consent.CMPState{CMP: parser, Source: apiSource, Categories: cats.orNil()}, nil
		}
	case "borlabs":
		var g map[string][]string
		if json.Unmarshal(raw, &g) == nil && g != nil {
			return &consent.CMPState{CMP: parser, Source: apiSource, Decided: true, Categories: groups(g)}, nil
		}
	}
	return nil, nil
}
