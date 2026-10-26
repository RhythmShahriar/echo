// Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
// SPDX-License-Identifier: MIT

package judge

import (
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"regexp"
	"slices"
	"strings"

	"github.com/RhythmShahriar/echo/consent"
	"github.com/RhythmShahriar/echo/testbed/consent/server"
)

// StandIn is one tracker stand-in script: its name and the host and
// path prefix it is served at.
type StandIn struct{ Name, Prefix string }

// StandIns are the tracker stand-in scripts a page can load, by name:
// a host and path prefix each. A page that names one but whose run lists
// no script there didn't load it; then nothing the run saw (or
// didn't see) says anything about Echo (qa.md 5.2: an http tag URL on an
// HSTS host silently fails and the scenario passes for the wrong
// reason). CMP scripts aren't listed: Echo doesn't report them as
// tracker scripts; a CMP that didn't load shows as no banner.
var StandIns = []StandIn{
	{"gtag", "www.googletagmanager.com/gtag/js"},
	{"gtm", "www.googletagmanager.com/gtm.js"},
	{"fbevents", "connect.facebook.net/"},
	{"tiktok", "analytics.tiktok.com/i18n/pixel/events.js"},
	{"linkedin", "snap.licdn.com/li.lms-analytics/insight.min.js"},
	{"uet", "bat.bing.com/bat.js"},
	{"clarity", "www.clarity.ms/tag/"},
	{"hotjar", "static.hotjar.com/c/"},
	{"plausible", "plausible.io/js/"},
}

// urlPattern finds absolute https URLs in a page, also the start of one
// a loader builds by concatenation ('https://static.hotjar.com/c/hotjar-').
var urlPattern = regexp.MustCompile(`https://[a-z0-9.-]+/[^\s"'<>)\\]*`)

// standInOf names the stand-in at u (host and path, no scheme), or "".
func standInOf(hostPath string) string {
	for _, s := range StandIns {
		if strings.HasPrefix(hostPath, s.Prefix) {
			return s.Name
		}
	}
	return ""
}

// PageStandIns renders the start page of scenario id with h (the
// testbed server's handler, in the state it is in now) and lists the
// stand-ins it names, sorted.
func PageStandIns(h http.Handler, id string) ([]string, error) {
	req := httptest.NewRequest(http.MethodGet, "https://"+id+server.SiteSuffix+"/", nil)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		return nil, fmt.Errorf("%s: the testbed answered %d", id, rec.Code)
	}
	body, _ := io.ReadAll(rec.Body)
	return StandInsIn(string(body)), nil
}

// StandInsIn lists the stand-ins page names, sorted and once each.
func StandInsIn(page string) []string {
	var names []string
	for _, u := range urlPattern.FindAllString(page, -1) {
		if n := standInOf(strings.TrimPrefix(u, "https://")); n != "" {
			names = append(names, n)
		}
	}
	slices.Sort(names)
	return slices.Compact(names)
}

// LoadedStandIns lists the stand-ins e's runs loaded: a script entry, or
// a script hit, at a stand-in's address.
func LoadedStandIns(e *Envelope) []string {
	var names []string
	add := func(raw string) {
		u, err := url.Parse(raw)
		if err != nil || u.Host == "" {
			return
		}
		if n := standInOf(u.Hostname() + u.EscapedPath()); n != "" {
			names = append(names, n)
		}
	}
	e.Runs(func(_ consent.Page, r consent.Run) {
		for _, s := range r.Scripts {
			add(s.URL)
		}
		for _, h := range r.Hits {
			if h.Kind == consent.HitScript {
				add(h.URL)
			}
		}
	})
	slices.Sort(names)
	return slices.Compact(names)
}

// MissingStandIns is want less what e loaded.
func MissingStandIns(want []string, e *Envelope) []string {
	if e == nil {
		return nil
	}
	got := LoadedStandIns(e)
	var out []string
	for _, w := range want {
		if !slices.Contains(got, w) {
			out = append(out, w)
		}
	}
	return out
}
