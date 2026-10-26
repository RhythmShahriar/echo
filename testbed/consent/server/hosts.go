// Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
// SPDX-License-Identifier: MIT

package server

import (
	"slices"
	"strings"
)

// SiteSuffix is the domain every scenario site lives under: c01.shop.test,
// g12.shop.test, metrics.c12.shop.test. .test is reserved (RFC 2606), so
// a name that escapes the mapping can never reach a real site.
const SiteSuffix = ".shop.test"

// A tagRoute serves one stand-in file for paths with a prefix on a host.
type tagRoute struct {
	prefix string // path prefix; "" matches every path
	file   string // under static/; "" when the path is a hit endpoint only
}

// tagHosts are the tracker and CMP hosts the server stands in for, with
// the files it serves on each. Every name here is printed by -print-map,
// so Echo maps it to the testbed; TestManifest checks that no
// fixture names a host outside this list, *.test or loopback.
var tagHosts = map[string][]tagRoute{
	// Google
	"www.googletagmanager.com": {
		{"/gtag/js", "tags/gtag.js"},
		{"/gtm.js", "tags/gtag.js"},
	},
	"www.google-analytics.com":     {},
	"region1.google-analytics.com": {},
	"googleads.g.doubleclick.net":  {},
	"fonts.googleapis.com":         {{"/css", "tags/fonts.css"}},
	"www.google.com":               {{"/recaptcha/api.js", "tags/recaptcha.js"}},
	"www.youtube.com":              {{"/embed/", "tags/youtube-embed.html"}},
	"www.youtube-nocookie.com":     {{"/embed/", "tags/youtube-nocookie-embed.html"}},
	// Meta, TikTok, LinkedIn, Microsoft
	"connect.facebook.net": {{"/", "tags/fbevents.js"}},
	"www.facebook.com":     {},
	"analytics.tiktok.com": {{"/i18n/pixel/events.js", "tags/tiktok.js"}},
	"snap.licdn.com":       {{"/li.lms-analytics/insight.min.js", "tags/linkedin.js"}},
	"px.ads.linkedin.com":  {},
	"bat.bing.com":         {{"/bat.js", "tags/uet.js"}},
	"www.clarity.ms":       {{"/tag/", "tags/clarity.js"}},
	// Hotjar and Plausible
	"static.hotjar.com": {{"/c/", "tags/hotjar.js"}},
	"in.hotjar.com":     {},
	"plausible.io":      {{"/js/", "tags/plausible.js"}},
	// CMP CDNs (the replicas)
	"consent.cookiebot.com": {{"/uc.js", "cmps/cookiebot/uc.js"}},
	"cdn.cookielaw.org":     {{"/scripttemplates/otSDKStub.js", "cmps/onetrust/otSDKStub.js"}},
	"app.usercentrics.eu":   {{"/browser-ui/latest/loader.js", "cmps/usercentrics/loader.js"}},
	"sdk.privacy-center.org": {
		{"/", "cmps/didomi/loader.js"},
	},
	"cdn.iubenda.com": {{"/cs/iubenda_cs.js", "cmps/iubenda/iubenda_cs.js"}},
	"cdn-cookieyes.com": {
		{"/client_data/", "cmps/cookieyes/script.js"},
	},
}

// sitePaths are files every scenario site serves from static/ at the
// paths a WordPress plugin or a self-hosted library would use, so the
// replicas of the plugins load from the site's own host as the real
// ones do.
var sitePaths = []tagRoute{
	{"/wp-content/plugins/complianz-gdpr/", "cmps/complianz/complianz.js"},
	{"/wp-content/plugins/borlabs-cookie/", "cmps/borlabs/borlabs-cookie.js"},
	{"/_tb/banner.js", "cmps/generic/banner.js"},
	{"/_tb/banner.css", "cmps/generic/banner.css"},
	{"/vendor/klaro/klaro.js", "cmps/vendor/klaro/klaro.js"},
	{"/vendor/cookieconsent/cookieconsent.umd.js", "cmps/vendor/cookieconsent/cookieconsent.umd.js"},
	{"/vendor/cookieconsent/cookieconsent.css", "cmps/vendor/cookieconsent/cookieconsent.css"},
	{"/~partytown/gtm-worker.js", "tags/worker.js"},
	{"/~partytown/loader.js", "tags/worker-loader.js"},
}

// TagHosts lists every host the server stands in for, sorted: the
// names a run must map to the testbed.
func TagHosts() []string {
	hosts := make([]string, 0, len(tagHosts))
	for h := range tagHosts {
		hosts = append(hosts, h)
	}
	slices.Sort(hosts)
	return hosts
}

// MapArgs is the rhythm-echo flags that send every scenario site and
// stand-in host to addr and nothing anywhere else: one --map-host per
// name, then --map-only.
func MapArgs(addr string) []string {
	args := []string{"--map-host", "*" + SiteSuffix + "=" + addr}
	for _, h := range TagHosts() {
		args = append(args, "--map-host", h+"="+addr)
	}
	return append(args, "--map-only")
}

// MapLine is MapArgs as one line for a shell, as -print-map prints it.
// The site pattern is quoted so the shell doesn't expand the star.
func MapLine(addr string) string {
	args := MapArgs(addr)
	out := make([]string, len(args))
	for i, a := range args {
		if strings.ContainsAny(a, "*?[") {
			a = "'" + a + "'"
		}
		out[i] = a
	}
	return strings.Join(out, " ")
}

// A hitRoute is a tracker hit endpoint the sink counts: a path prefix,
// on one host or (host "") on any host, as a first-party server-side
// tag would receive it.
type hitRoute struct {
	host   string
	prefix string
}

// hitRoutes are the endpoints whose requests are tracking hits: what
// Echo holds in its default mode, so the sink must never count one.
var hitRoutes = []hitRoute{
	{"", "/g/collect"},
	{"", "/g/s/collect"},
	{"", "/tr"},
	{"googleads.g.doubleclick.net", "/pagead/"},
	{"analytics.tiktok.com", "/api/v2/pixel"},
	{"px.ads.linkedin.com", "/collect"},
	{"px.ads.linkedin.com", "/wa/"},
	{"bat.bing.com", "/action/"},
	{"www.clarity.ms", "/collect"},
	{"in.hotjar.com", "/api/"},
	{"plausible.io", "/api/event"},
	{"www.youtube.com", "/api/stats/"},
}

// isHit reports whether a request to host and path is a tracker hit.
// /tr counts only on www.facebook.com or as a first-party Meta
// endpoint, never as a prefix of /tracking or /translate.
func isHit(host, path string) bool {
	for _, r := range hitRoutes {
		if r.host != "" && r.host != host {
			continue
		}
		if r.prefix == "/tr" {
			if path == "/tr" || path == "/tr/" {
				return true
			}
			continue
		}
		if strings.HasPrefix(path, r.prefix) {
			return true
		}
	}
	return false
}
