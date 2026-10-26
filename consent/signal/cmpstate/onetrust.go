// Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
// SPDX-License-Identifier: MIT

package cmpstate

import (
	"net/url"
	"strconv"
	"strings"

	"github.com/RhythmShahriar/echo/consent"
)

// The OneTrust cookies: the choice and the time the banner was closed.
const (
	oneTrustCookie = "OptanonConsent"
	oneTrustClosed = "OptanonAlertBoxClosed"
)

// OneTrust reads the OptanonConsent cookie, a query string whose groups
// field lists each category group as ID:1 or ID:0
// (groups=C0001:1,C0002:0,C0003:0,C0004:0). Categories are the group IDs
// (C0001 strictly necessary, C0002 performance, C0003 functional, C0004
// targeting, C0005 social media; sites add their own). It is decided
// when the OptanonAlertBoxClosed cookie exists or interactionCount is
// above 0. OneTrust publishes no format page outside its customer portal;
// this follows the cookie as observed and described by ObservePoint:
// https://www.observepoint.com/cookies/optanonconsent/
func OneTrust(values map[string]string) (*consent.CMPState, error) {
	v, ok, long := value(values, oneTrustCookie)
	if !ok {
		return nil, nil
	}
	source := "cookie:" + oneTrustCookie
	if long {
		return unreadable("onetrust", source, oneTrustCookie)
	}
	fields := map[string]string{}
	for _, part := range strings.Split(v, "&") {
		k, val, ok := strings.Cut(part, "=")
		if !ok || k == "" {
			continue
		}
		if d, err := url.QueryUnescape(val); err == nil {
			val = d
		}
		fields[k] = val
	}
	groups, ok := fields["groups"]
	if !ok {
		return unreadable("onetrust", source, oneTrustCookie)
	}
	cats := categories{}
	for _, d := range decodings(groups) {
		if strings.Contains(d, ":") {
			groups = d
			break
		}
	}
	for _, g := range strings.Split(groups, ",") {
		id, on, ok := strings.Cut(strings.TrimSpace(g), ":")
		if !ok || (on != "0" && on != "1") {
			continue
		}
		cats.set(id, on == "1")
	}
	n, _ := strconv.Atoi(fields["interactionCount"])
	_, closed, _ := value(values, oneTrustClosed)
	return &consent.CMPState{CMP: "onetrust", Source: source, Decided: closed || n > 0, Categories: cats.orNil()}, nil
}
