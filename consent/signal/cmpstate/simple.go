// Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
// SPDX-License-Identifier: MIT

package cmpstate

import (
	"slices"
	"strings"

	"github.com/RhythmShahriar/echo/consent"
)

// cookieYesCookie is where CookieYes keeps the choice.
const cookieYesCookie = "cookieyes-consent"

// cookieYesMeta are the cookieyes-consent fields that aren't categories.
var cookieYesMeta = map[string]bool{"consentid": true, "consent": true, "action": true, "lastrenewaldate": true, "lastreneweddate": true}

// CookieYes reads the cookieyes-consent cookie, comma-separated
// key:value pairs (consentid:..., consent:yes|no, action:yes,
// necessary:yes, functional:no, analytics:no, performance:no,
// advertisement:no, other:no). Every yes or no field but consent is a
// category. It is decided when action is yes. CookieYes documents the
// same categories for its getCkyConsent() API:
// https://www.cookieyes.com/documentation/retrieving-consent-data-using-api-getckyconsent/
// The cookie layout itself is not on a vendor page; it follows the
// cookie as CookieYes writes it.
func CookieYes(values map[string]string) (*consent.CMPState, error) {
	v, ok, long := value(values, cookieYesCookie)
	if !ok {
		return nil, nil
	}
	source := "cookie:" + cookieYesCookie
	if long {
		return unreadable("cookieyes", source, cookieYesCookie)
	}
	for _, d := range decodings(v) {
		fields, ok := pairs(d, ",", ":")
		if !ok || fields["consentid"] == "" && fields["action"] == "" && fields["necessary"] == "" {
			continue
		}
		cats := categories{}
		for k, val := range fields {
			if cookieYesMeta[strings.ToLower(k)] || (val != "yes" && val != "no") {
				continue
			}
			cats.set(k, val == "yes")
		}
		return &consent.CMPState{CMP: "cookieyes", Source: source, Decided: fields["action"] == "yes", Categories: cats.orNil()}, nil
	}
	return unreadable("cookieyes", source, cookieYesCookie)
}

// pairs splits s into key-value pairs; ok is false when a part has no
// separator.
func pairs(s, sep, kv string) (map[string]string, bool) {
	out := map[string]string{}
	for part := range strings.SplitSeq(s, sep) {
		k, v, ok := strings.Cut(strings.TrimSpace(part), kv)
		if !ok || k == "" {
			return nil, false
		}
		if len(out) < maxCategories {
			out[k] = v
		}
	}
	return out, true
}

// complianzPrefix starts every Complianz cookie.
const complianzPrefix = "cmplz_"

// complianzMeta are cmplz_ cookies that aren't categories.
var complianzMeta = []string{"banner-status", "consented_services", "policy_id", "id", "saved_categories", "saved_services"}

// Complianz reads the cmplz_<category> cookies (cmplz_functional,
// cmplz_preferences, cmplz_statistics, cmplz_marketing and others), each
// allow or deny. It is decided when any category cookie exists;
// cmplz_banner-status (dismissed) alone doesn't count. Complianz lists
// its cookies at https://complianz.io/cookies/
func Complianz(values map[string]string) (*consent.CMPState, error) {
	cats := categories{}
	found := false
	names := make([]string, 0, len(values))
	for name := range values {
		names = append(names, name)
	}
	slices.Sort(names) // the cap keeps the same names on every run
	for _, name := range names {
		cat, ok := strings.CutPrefix(name, complianzPrefix)
		if !ok || slices.Contains(complianzMeta, cat) {
			continue
		}
		v, ok, long := value(values, name)
		if !ok {
			continue
		}
		found = true
		if long || (v != "allow" && v != "deny") {
			return unreadable("complianz", "cookie:cmplz_*", name)
		}
		cats.set(cat, v == "allow")
	}
	if !found {
		return nil, nil
	}
	return &consent.CMPState{CMP: "complianz", Source: "cookie:cmplz_*", Decided: true, Categories: cats.orNil()}, nil
}

// borlabsCookie is where Borlabs Cookie keeps the choice.
const borlabsCookie = "borlabs-cookie"

// Borlabs reads the borlabs-cookie cookie, URL-encoded JSON
// {"consents":{"essential":[...],"statistics":[...],"marketing":[...]},
// "uid":"...","version":"..."}. A group listed with at least one service
// is granted, an empty one is not. It is decided when uid is set.
// Borlabs lists what the cookie holds (consents, UID, version, domain
// and path, lifetime) at https://borlabs.io/kb/what-information-does-borlabs-cookie-store/
func Borlabs(values map[string]string) (*consent.CMPState, error) {
	v, ok, long := value(values, borlabsCookie)
	if !ok {
		return nil, nil
	}
	source := "cookie:" + borlabsCookie
	var c struct {
		Consents map[string][]string `json:"consents"`
		UID      string              `json:"uid"`
	}
	if long || !jsonValue(v, &c) || c.Consents == nil {
		return unreadable("borlabs", source, borlabsCookie)
	}
	return &consent.CMPState{CMP: "borlabs", Source: source, Decided: c.UID != "", Categories: groups(c.Consents)}, nil
}

// groups turns Borlabs groups into categories: granted when a group
// lists a service.
func groups(m map[string][]string) map[string]bool {
	cats := categories{}
	names := make([]string, 0, len(m))
	for k := range m {
		names = append(names, k)
	}
	slices.Sort(names)
	for _, k := range names {
		cats.set(k, len(m[k]) > 0)
	}
	return cats.orNil()
}

// iubendaPrefix starts the iubenda cookie; the site's cookie policy ID
// follows it.
const iubendaPrefix = "_iub_cs-"

// Iubenda reads the _iub_cs-<id> cookie, URL-encoded JSON {"timestamp":
// ..., "version": ..., "purposes": {"1": true, "4": false, ...}, "id":
// ..., "cons": {"rand": ...}}. Categories are iubenda's purpose numbers
// (1 strictly necessary, 2 basic interactions, 3 experience, 4
// measurement, 5 marketing). It is decided when purposes is present.
// iubenda names the cookie in its cookie policies
// (https://www.iubenda.com/app/privacy-policy/252372/cookie-policy) and
// the purposes in its Cookie Solution guide; the JSON layout follows the
// cookie as the Cookie Solution writes it.
func Iubenda(values map[string]string) (*consent.CMPState, error) {
	names := make([]string, 0, 1)
	for name := range values {
		if strings.HasPrefix(name, iubendaPrefix) && values[name] != "" {
			names = append(names, name)
		}
	}
	if len(names) == 0 {
		return nil, nil
	}
	slices.Sort(names)
	name := names[0]
	v, _, long := value(values, name)
	source := "cookie:" + name
	var c struct {
		Purposes map[string]bool `json:"purposes"`
	}
	if long || !jsonValue(v, &c) {
		return unreadable("iubenda", source, name)
	}
	cats := categories{}
	keys := make([]string, 0, len(c.Purposes))
	for k := range c.Purposes {
		keys = append(keys, k)
	}
	slices.Sort(keys)
	for _, k := range keys {
		cats.set(k, c.Purposes[k])
	}
	return &consent.CMPState{CMP: "iubenda", Source: source, Decided: c.Purposes != nil, Categories: cats.orNil()}, nil
}
