// Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
// SPDX-License-Identifier: MIT

package config

import (
	"strings"
	"testing"

	"github.com/RhythmShahriar/echo/consent"
)

// prdConsent is the consent: example of PRD 8.1.
const prdConsent = `site: https://staging.shop.test
production: [shop.example.com]

consent:
  urls: [/, /shop/, /contact/]
  runs: [none, accept, reject]
  pages: 1
  settle: 3s
  banner_wait: 10s
  timeout: 90s
  retries: 1
  cmp: auto
  click: button
  accept_selector: "#accept-all"
  reject_selector: "#manage >> #reject-all"
  consent_mode: advanced
  geo: de
  locale: de-DE
  timezone: Europe/Berlin
  viewport: desktop
  parallel: 3
  exempt: [plausible]
  headers:
    Authorization: ${env:RHYTHM_ECHO_STAGING_AUTH}
  map_hosts:
    "*.shop.test": 127.0.0.1:18950
  ignore:
    - check: consent/before-choice
      tracker: hotjar
      page: /
      run: none
      reason: "client signs off until the Nov launch"
      until: 2026-11-30
  trackers:
    - id: shop-pixel
      name: Shop pixel
      category: advertising
      hits:
        - host: px.shop.example
          path: /collect
      cookies: [_shpx]
      fixtures:
        match: ["https://px.shop.example/collect?e=view"]
        no_match: ["https://px.shop.example/pixel.js"]

checks:
  consent/cookieless-ping: warning
  consent/no-first-layer-reject: error
`

// The PRD's whole consent: block loads; consent check IDs are
// accepted under checks:.
func TestConsentBlockLoads(t *testing.T) {
	c, err := parse([]byte(prdConsent))
	if err != nil {
		t.Fatal(err)
	}
	k := c.Consent
	if k == nil || len(k.URLs) != 3 || *k.Pages != 1 || k.Settle.String() != "3s" || k.BannerWait.String() != "10s" || *k.Retries != 1 ||
		k.RejectSelector != "#manage >> #reject-all" || k.MapHosts["*.shop.test"] != "127.0.0.1:18950" || k.Parallel != 3 {
		t.Fatalf("consent = %+v", k)
	}
	if len(k.Ignore) != 1 || k.Ignore[0].Run != consent.RunNone || k.Ignore[0].Until != "2026-11-30" || k.Ignore[0].Tracker != "hotjar" {
		t.Fatalf("ignore = %+v", k.Ignore)
	}
	if len(k.Trackers.Content) != 1 {
		t.Fatalf("trackers = %+v", k.Trackers)
	}
	if c.Checks["consent/cookieless-ping"] != Warning {
		t.Fatalf("checks = %v", c.Checks)
	}
	if _, err := parse([]byte("checks:\n  consent/before-choice: warning\n")); err != nil {
		t.Fatalf("checks: consent/before-choice: %v", err)
	}
}

// A config without consent: still loads; an unknown key in it is
// refused like any other.
func TestConsentBlockStrict(t *testing.T) {
	if c, err := parse([]byte("site: https://shop.test\n")); err != nil || c.Consent != nil {
		t.Fatalf("no consent block: %+v, %v", c.Consent, err)
	}
	_, err := parse([]byte("consent:\n  runz: [none]\n"))
	if err == nil || !strings.Contains(err.Error(), "runz") {
		t.Fatalf("unknown key: %v", err)
	}
}
