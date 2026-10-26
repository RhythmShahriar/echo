// Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
// SPDX-License-Identifier: MIT

package cli

import (
	"net/url"
	"regexp"
	"strings"

	"github.com/RhythmShahriar/echo/consent"
)

// refusedSchemes are schemes people paste that a consent check must
// never load: they read files, run script or open the browser's own
// pages (PRD 6, 14.3). Any other scheme but http and https is refused
// too; these are only the ones without "//" that would otherwise read
// as host:port.
var refusedSchemes = regexp.MustCompile(`(?i)^(file|data|javascript|vbscript|chrome|chrome-extension|devtools|view-source|about|ftp|blob|mailto|tel|ws|wss|filesystem|intent):`)

// schemeLike is a scheme followed by something that isn't a port:
// "foo:bar" rather than "shop.test:8443".
var schemeLike = regexp.MustCompile(`^[A-Za-z][A-Za-z0-9+.-]*:[^0-9]`)

// target reads one page to check under PRD 6's URL rules: http and
// https only, https:// assumed without a scheme, a path read against
// site:, never a login in the URL. site is the config's site: ("" when
// there is none). Errors are usage errors with the URL masked.
func target(raw, site string) (*url.URL, error) {
	s := strings.TrimSpace(raw)
	shown := consent.MaskURL(s, false)
	bad := func() error { return usagef("consent %q: want an http or https URL", shown) }
	switch {
	case s == "":
		return nil, usagef("consent: an empty URL; give a page like https://staging.shop.test/")
	case strings.HasPrefix(s, "/") && !strings.HasPrefix(s, "//"):
		if site == "" {
			return nil, usagef("consent %q: a path needs site: in .echo/config.yaml; give the whole URL, e.g. https://staging.shop.test%s", shown, s)
		}
		base, err := url.Parse(strings.TrimSpace(site))
		if err != nil || (base.Scheme != "http" && base.Scheme != "https") || base.Host == "" {
			return nil, usagef("consent %q: site: %q isn't an http or https URL", shown, consent.MaskURL(site, false))
		}
		ref, err := url.Parse(s)
		if err != nil {
			return nil, bad()
		}
		s = base.ResolveReference(ref).String()
	case strings.HasPrefix(s, "//"):
		s = "https:" + s
	case refusedSchemes.MatchString(s):
		return nil, bad()
	case strings.Contains(s, "://"):
	case schemeLike.MatchString(s):
		return nil, bad()
	default:
		s = "https://" + s
	}
	u, err := url.Parse(s)
	if err != nil {
		return nil, bad()
	}
	u.Scheme = strings.ToLower(u.Scheme)
	if u.Scheme != "http" && u.Scheme != "https" || u.Host == "" || u.Opaque != "" {
		return nil, bad()
	}
	if u.User != nil {
		return nil, usagef("consent %q: a URL with a login in it isn't allowed; pass it with -H \"Authorization: env:RHYTHM_ECHO_NAME\"", shown)
	}
	if u.Path == "" {
		u.Path = "/"
	}
	return u, nil
}
