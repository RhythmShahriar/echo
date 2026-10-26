// Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
// SPDX-License-Identifier: MIT

package tracker

import (
	"crypto/sha256"
	"encoding/hex"
	"slices"
	"strings"
	"testing"

	"github.com/RhythmShahriar/echo/consent"
)

func TestMetaPersonalFieldsMasked(t *testing.T) {
	sum := sha256.Sum256([]byte("ann" + "@" + "example.test"))
	email := hex.EncodeToString(sum[:]) // 64 hex, built at run time
	phone := "49151" + "12345678"
	secrets := []string{email, phone, "Ann", "ext-" + "991"}
	query := "id=1234567890&ev=PageView&ud%5Bem%5D=" + email + "&ph=" + phone + "&fn=Ann&external_id=ext-991"
	for _, c := range []struct {
		name string
		r    *consent.Request
		body string
	}{
		{"GET", get("https://www.facebook.com/tr/?"+query, "image"), ""},
		{"POST form", &consent.Request{Method: "POST", URL: "https://www.facebook.com/tr/", Type: "ping"}, query},
		{"first party", get("https://capi.shop.test/tr?"+query, "image"), ""},
		{"masked URL", get(consent.MaskURL("https://www.facebook.com/tr/?"+query, false), "image"), ""},
	} {
		h := builtin.Request(c.r, []byte(c.body), shop)
		if h == nil || h.Tracker != "meta-pixel" || h.Kind != consent.HitBeacon {
			t.Fatalf("%s: hit = %+v", c.name, h)
		}
		if !slices.Equal(h.Events, []string{"PageView"}) || h.ID != "••••7890" {
			t.Errorf("%s: events %v, id %q", c.name, h.Events, h.ID)
		}
		if got := h.Params["personal"]; got != "ud[em],ph,fn,external_id" && c.name != "masked URL" {
			t.Errorf("%s: personal = %q", c.name, got)
		}
		for k, v := range h.Params {
			for _, s := range secrets {
				if strings.Contains(v, s) || strings.Contains(k, s) {
					t.Errorf("%s: Params[%s] = %q shows a personal value", c.name, k, v)
				}
			}
		}
		for _, s := range secrets {
			if strings.Contains(h.ID, s) || slices.ContainsFunc(h.Events, func(e string) bool { return strings.Contains(e, s) }) {
				t.Errorf("%s: a personal value reached the ID or events", c.name)
			}
		}
	}
}
