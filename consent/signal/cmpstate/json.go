// Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
// SPDX-License-Identifier: MIT

package cmpstate

import (
	"encoding/base64"
	"encoding/json"
	"slices"
	"strings"

	"github.com/RhythmShahriar/echo/consent"
)

// The Usercentrics storage keys.
const (
	ucSettings    = "uc_settings"
	ucData        = "ucData"
	ucInteraction = "uc_user_interaction"
)

// ucService is one service in uc_settings.
type ucService struct {
	ID      string      `json:"id"`
	Name    string      `json:"name"`
	Status  bool        `json:"status"`
	History []ucHistory `json:"history"`
}

// ucHistory is one entry of a service's consent history.
type ucHistory struct {
	Type string `json:"type"` // explicit, implicit
}

// Usercentrics reads localStorage uc_settings, JSON with
// {"services": [{"id", "name", "status", "history": [{"type", ...}]}]},
// or, when that is missing, ucData ({"consent": {"services": {"<id>":
// {"name", "consent"}}}}, the newer browser UI's key). Categories are
// services_granted (some service has consent) and services_denied (some
// service has none), plus each service by name when names are present.
// It is decided when uc_user_interaction is true or every service has an
// explicit history entry. Usercentrics documents the consent model per
// service (https://usercentrics.com/docs/web/features/events/uc-consent/);
// the storage layout follows what its browser SDK writes.
func Usercentrics(values map[string]string) (*consent.CMPState, error) {
	interacted := strings.TrimSpace(values[ucInteraction]) == "true"
	if v, ok, long := value(values, ucSettings); ok {
		source := "storage:" + ucSettings
		var s struct {
			Services []ucService `json:"services"`
		}
		if long || !jsonValue(v, &s) || s.Services == nil {
			return unreadable("usercentrics", source, ucSettings)
		}
		explicit := len(s.Services) > 0
		cats := categories{}
		for _, svc := range s.Services {
			ucCount(cats, svc.Name, svc.Status)
			if !slices.ContainsFunc(svc.History, func(h ucHistory) bool { return h.Type == "explicit" }) {
				explicit = false
			}
		}
		return &consent.CMPState{CMP: "usercentrics", Source: source, Decided: interacted || explicit, Categories: cats.orNil()}, nil
	}
	if v, ok, long := value(values, ucData); ok {
		source := "storage:" + ucData
		var d struct {
			Consent struct {
				Services map[string]struct {
					Name    string `json:"name"`
					Consent bool   `json:"consent"`
				} `json:"services"`
			} `json:"consent"`
		}
		if long || !jsonValue(v, &d) || d.Consent.Services == nil {
			return unreadable("usercentrics", source, ucData)
		}
		ids := make([]string, 0, len(d.Consent.Services))
		for id := range d.Consent.Services {
			ids = append(ids, id)
		}
		slices.Sort(ids)
		cats := categories{}
		for _, id := range ids {
			svc := d.Consent.Services[id]
			ucCount(cats, svc.Name, svc.Consent)
		}
		return &consent.CMPState{CMP: "usercentrics", Source: source, Decided: interacted, Categories: cats.orNil()}, nil
	}
	if interacted {
		return &consent.CMPState{CMP: "usercentrics", Source: "storage:" + ucInteraction, Decided: true}, nil
	}
	return nil, nil
}

// ucCount records one service: its name when it has one and the
// services_granted or services_denied summary.
func ucCount(cats categories, name string, granted bool) {
	if granted {
		cats["services_granted"] = true
	} else {
		cats["services_denied"] = true
	}
	if name != "" {
		cats.set(name, granted)
	}
}

// didomiCookie is where Didomi keeps the choice for custom purposes and
// vendors (IAB purposes are in euconsent-v2).
const didomiCookie = "didomi_token"

// didomiLists is one enabled and disabled pair in didomi_token.
type didomiLists struct {
	Enabled  []string `json:"enabled"`
	Disabled []string `json:"disabled"`
}

// Didomi reads the didomi_token cookie, base64 JSON {"user_id",
// "created", "updated", "purposes": {"enabled": [...], "disabled":
// [...]}, "vendors": {...}, "purposes_li", "vendors_li"}. Categories are
// the purpose IDs (enabled true, disabled false). It is decided when any
// purpose or vendor list is non-empty. Format:
// https://docs.didomi.io/get-started/general/cookies-and-local-storage
// (Didomi now also writes didomi_dcs, which this parser doesn't read).
func Didomi(values map[string]string) (*consent.CMPState, error) {
	v, ok, long := value(values, didomiCookie)
	if !ok {
		return nil, nil
	}
	source := "cookie:" + didomiCookie
	var t struct {
		Purposes didomiLists `json:"purposes"`
		Vendors  didomiLists `json:"vendors"`
	}
	if long || !didomiJSON(v, &t) {
		return unreadable("didomi", source, didomiCookie)
	}
	cats := categories{}
	for _, p := range t.Purposes.Enabled {
		cats.set(p, true)
	}
	for _, p := range t.Purposes.Disabled {
		cats.set(p, false)
	}
	decided := len(t.Purposes.Enabled)+len(t.Purposes.Disabled)+len(t.Vendors.Enabled)+len(t.Vendors.Disabled) > 0
	return &consent.CMPState{CMP: "didomi", Source: source, Decided: decided, Categories: cats.orNil()}, nil
}

// didomiJSON decodes v (URL-encoded or not, base64 in any of its four
// alphabets) and unmarshals the JSON object into dst.
func didomiJSON(v string, dst any) bool {
	for _, d := range decodings(v) {
		d = strings.TrimSpace(d)
		for _, enc := range []*base64.Encoding{base64.StdEncoding, base64.RawStdEncoding, base64.URLEncoding, base64.RawURLEncoding} {
			b, err := enc.DecodeString(d)
			if err != nil || len(b) == 0 || b[0] != '{' {
				continue
			}
			if json.Unmarshal(b, dst) == nil {
				return true
			}
		}
	}
	return false
}
