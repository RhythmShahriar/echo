// Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
// SPDX-License-Identifier: MIT

package config

import (
	"strings"
	"testing"
	"time"
)

// fixedToday pins the clock ignore: until dates are held to.
func fixedToday(t *testing.T) {
	t.Helper()
	old := consentNow
	consentNow = func() time.Time { return time.Date(2026, 10, 26, 9, 0, 0, 0, time.UTC) }
	t.Cleanup(func() { consentNow = old })
}

// consentConfig is a config whose consent: block holds body (indented
// by the caller), with site: set so paths are read against it.
func consentConfig(body string) string {
	return "site: https://staging.shop.test\nconsent:\n" + body
}

// Each rule of the consent: block, refused with its line and the next
// step; the same block passes once the mistake is gone.
func TestConsentRules(t *testing.T) {
	fixedToday(t)
	for _, tc := range []struct{ bad, want, good string }{
		{"  runz: [none]\n", "line 3: consent: runz isn't a key; did you mean runs?", "  runs: [none]\n"},
		{"  bannerwait: 5s\n", "line 3: consent: bannerwait isn't a key; did you mean banner_wait?", "  banner_wait: 5s\n"},
		{"  zzzzzzzzzz: 1\n", "line 3: consent: zzzzzzzzzz isn't a key; use urls, runs, pages,", "  pages: 0\n"},
		{"  ignore:\n    - check: consent/before-choice\n      reason: r\n      until: 2026-11-30\n      tracer: x\n",
			"line 7: consent: ignore: tracer isn't a key; did you mean tracker?",
			"  ignore:\n    - check: consent/before-choice\n      reason: r\n      until: 2026-11-30\n      tracker: x\n"},
		{"  urls: [ftp://shop.test]\n", `line 3: consent: urls: "ftp://shop.test": want an http or https URL, or a path like /shop/`, "  urls: [/, https://shop.test/menu]\n"},
		{"  urls: [\"https://u:p@shop.test/\"]\n", "consent: urls: \"https://•••••@shop.test/\": a URL with a login in it isn't allowed", "  urls: [/]\n"},
		{"  runs: [none, decline]\n", `line 3: consent: runs "decline": want none, accept, reject, gpc or withdraw (+gpc adds one); did you mean reject?`, "  runs: [none, reject, +gpc]\n"},
		{"  pages: 11\n", "line 3: consent: pages 11: want 0 to 10", "  pages: 10\n"},
		{"  retries: 4\n", "line 3: consent: retries 4: want 0 to 3", "  retries: 0\n"},
		{"  parallel: 9\n", "line 3: consent: parallel 9: want 1 to 8", "  parallel: 8\n"},
		{"  settle: 31s\n", "line 3: consent: settle 31s: want at most 30s", "  settle: 30s\n"},
		{"  banner_wait: 2m\n", "line 3: consent: banner_wait 2m: want at most 60s", "  banner_wait: 1m\n"},
		{"  timeout: 11m\n", "line 3: consent: timeout 11m: want at most 10m", "  timeout: 10m\n"},
		{"  timeout: 0s\n", "line 3: consent: timeout 0s: want more than 0 and at most 10m", "  timeout: 1s\n"},
		{"  settle: soon\n", `line 3: "soon" isn't a duration like 2s`, "  settle: 1s\n"},
		{"  cmp: quantcast\n", `line 3: consent: cmp "quantcast": want auto or one of cookiebot, onetrust,`, "  cmp: OneTrust\n"},
		{"  click: mouse\n", `line 3: consent: click "mouse": want button, api or button,api`, "  click: button,api\n"},
		{"  consent_mode: loose\n", `line 3: consent: consent_mode "loose": want advanced, basic or strict`, "  consent_mode: strict\n"},
		{"  reject_selector: \"#a >> \"\n", "line 3: consent: reject_selector: selector \"#a >> \": step 2 is empty", "  reject_selector: \"#a >> #b\"\n"},
		{"  geo: deu\n", `line 3: consent: geo "deu": want a two-letter country code (ISO 3166-1 alpha-2), e.g. de`, "  geo: DE\n"},
		{"  locale: german!\n", `line 3: consent: locale "german!": want a language tag like de-DE or en-US`, "  locale: de_DE\n"},
		{"  timezone: Berlin time\n", `line 3: consent: timezone "Berlin time": want an IANA time zone like Europe/Berlin or UTC`, "  timezone: America/Argentina/Buenos_Aires\n"},
		{"  viewport: tablet\n", `line 3: consent: viewport "tablet": want desktop or mobile`, "  viewport: mobile\n"},
		{"  exempt: [\"\"]\n", "line 3: consent: exempt: an empty tracker id", "  exempt: [plausible]\n"},
		{"  headers:\n    \"Bad Name\": ${env:RHYTHM_ECHO_X}\n", `line 4: consent: headers: "Bad Name" isn't a header name`, "  headers:\n    X-Staging: ${env:RHYTHM_ECHO_X}\n"},
		{"  headers:\n    Authorization: ${env:HOME}\n", "line 4: consent: headers: Authorization: use ${env:RHYTHM_ECHO_NAME}, not the value itself", "  headers:\n    Authorization: ${env:RHYTHM_ECHO_STAGING_AUTH}\n"},
		{"  map_hosts:\n    \"*.shop.test\": nowhere\n", `line 4: consent: map_hosts: *.shop.test: "nowhere": want host:port or an http:// or https:// URL, e.g. 127.0.0.1:18950`, "  map_hosts:\n    \"*.shop.test\": 127.0.0.1:18950\n    cdn.shop.test: http://10.0.0.5:8080\n"},
		{"  map_hosts:\n    \"https://x/\": 127.0.0.1:1\n", `line 4: consent: map_hosts: "https://x/": want a host name or *.name, e.g. *.shop.test`, "  map_hosts:\n    x.test: 127.0.0.1:1\n"},
		{"  ignore:\n    - reason: r\n      until: 2026-11-30\n", "line 4: consent: ignore: needs check, a consent check ID like consent/before-choice",
			"  ignore:\n    - check: consent/no-banner\n      reason: r\n      until: 2026-11-30\n"},
		{"  ignore:\n    - check: consent/befor-choice\n      reason: r\n      until: 2026-11-30\n", "line 4: consent: ignore: check consent/befor-choice isn't a consent check; did you mean consent/before-choice?",
			"  ignore:\n    - check: consent/before-choice\n      reason: r\n      until: 2026-11-30\n"},
		{"  ignore:\n    - check: consent/before-choice\n      until: 2026-11-30\n", "line 4: consent: ignore: consent/before-choice: needs a reason",
			"  ignore:\n    - check: consent/before-choice\n      reason: \"client signs off\"\n      until: 2026-11-30\n"},
		{"  ignore:\n    - check: consent/before-choice\n      reason: r\n", "line 4: consent: ignore: consent/before-choice: needs until",
			"  ignore:\n    - check: consent/before-choice\n      reason: r\n      until: 2025-01-01\n"},
		{"  ignore:\n    - check: consent/before-choice\n      reason: r\n      until: next week\n", `line 4: consent: ignore: consent/before-choice: until "next week": want a date like 2026-11-30`,
			"  ignore:\n    - check: consent/before-choice\n      reason: r\n      until: 2027-10-26\n"},
		{"  ignore:\n    - check: consent/before-choice\n      run: decline\n      reason: r\n      until: 2026-11-30\n", `line 4: consent: ignore: consent/before-choice: run "decline": want none, accept, reject, gpc or withdraw`,
			"  ignore:\n    - check: consent/before-choice\n      run: none\n      reason: r\n      until: 2026-11-30\n"},
		{"  trackers: {id: x}\n", "line 3: consent: trackers: want a list of trackers", "  trackers: []\n"},
		{"  [urls]\n", "consent: want keys like urls: and runs:", "  urls: [/]\n"},
	} {
		_, err := parse([]byte(consentConfig(tc.bad)))
		if err == nil || !strings.Contains(err.Error(), tc.want) {
			t.Errorf("%q:\n got %v\nwant %s", tc.bad, err, tc.want)
		}
		if _, err := parse([]byte(consentConfig(tc.good))); err != nil {
			t.Errorf("%q: %v", tc.good, err)
		}
	}
	// A path needs site:.
	if _, err := parse([]byte("consent:\n  urls: [/shop/]\n")); err == nil || err.Error() != `line 2: consent: urls: "/shop/" is a path; set site: or give the whole URL` {
		t.Errorf("a path without site: %v", err)
	}
	// An empty block is a config without one.
	if c, err := parse([]byte("consent:\n")); err != nil || c.Consent != nil {
		t.Errorf("consent: alone: %+v %v", c.Consent, err)
	}
}

// TestHeaderLiteralRefused: a literal header value is PRD 10.2 message
// 7 with its line; the value is never quoted back.
func TestHeaderLiteralRefused(t *testing.T) {
	secret := "Basic " + "c3RhZ2luZzpodW50ZXIy"
	_, err := parse([]byte(consentConfig("  headers:\n    Authorization: " + secret + "\n")))
	want := "line 4: consent: headers: Authorization: use ${env:RHYTHM_ECHO_NAME}, not the value itself; secrets never sit in the config"
	if err == nil || err.Error() != want {
		t.Fatalf("got %v\nwant %s", err, want)
	}
	if strings.Contains(err.Error(), "c3RhZ2luZz") {
		t.Error("the error quotes the header's value")
	}
}

func TestIgnoreUntilTooFar(t *testing.T) {
	fixedToday(t)
	block := "  ignore:\n    - check: consent/before-choice\n      reason: r\n      until: %s\n"
	_, err := parse([]byte(consentConfig(strings.Replace(block, "%s", "2028-01-01", 1))))
	if want := "line 4: consent: ignore: until 2028-01-01 is more than a year away; review it sooner"; err == nil || err.Error() != want {
		t.Errorf("got %v\nwant %s", err, want)
	}
	// 365 days from today is the latest it may be.
	if _, err := parse([]byte(consentConfig(strings.Replace(block, "%s", "2027-10-26", 1)))); err != nil {
		t.Errorf("a year away: %v", err)
	}
	if _, err := parse([]byte(consentConfig(strings.Replace(block, "%s", "2027-10-27", 1)))); err == nil {
		t.Error("366 days away was taken")
	}
}

// TestChecksTypo: a consent/ ID under checks: that isn't a check names
// the one meant, with or without a consent: block.
func TestChecksTypo(t *testing.T) {
	for _, cfg := range []string{"checks:\n  consent/befor-choice: warning\n", consentConfig("  pages: 1\n") + "checks:\n  consent/befor-choice: warning\n"} {
		_, err := parse([]byte(cfg))
		if err == nil || !strings.HasSuffix(err.Error(), "checks: consent/befor-choice isn't a check; did you mean consent/before-choice?") || !strings.HasPrefix(err.Error(), "line ") {
			t.Errorf("%q: %v", cfg, err)
		}
	}
	if _, err := parse([]byte("checks:\n  consent/nothing-like-it: warning\n")); err == nil || err.Error() != "line 2: checks: consent/nothing-like-it isn't a check; rhythm echo checks lists them" {
		t.Errorf("no close ID: %v", err)
	}
	if _, err := parse([]byte("checks:\n  consent/after-reject: broke\n")); err == nil || err.Error() != `line 2: checks: consent/after-reject: "broke" isn't off, notice, warning or error` {
		t.Errorf("broke on a consent check: %v", err)
	}
	if _, err := parse([]byte("checks:\n  consent/after-reject: off\n  consent/cookieless-ping: error\n")); err != nil {
		t.Errorf("good levels: %v", err)
	}
}

// FuzzConsentConfig: the loader never panics on any consent: block
// and a consent error always names its line.
func FuzzConsentConfig(f *testing.F) {
	f.Add(prdConsent)
	f.Add(consentConfig("  runs: [+gpc]\n  pages: 3\n"))
	f.Add(consentConfig("  headers:\n    A: ${env:RHYTHM_ECHO_A}\n  map_hosts: {\"*.x.test\": 127.0.0.1:1}\n"))
	f.Add(consentConfig("  ignore:\n    - {check: consent/no-banner, reason: r, until: 2026-11-30}\n"))
	f.Add(consentConfig("  trackers:\n    - id: x\n      hits: [{host: a.test}]\n"))
	f.Add("consent: 3\n")
	f.Add("consent: [a, b]\n")
	f.Add("consent:\n  ignore: {check: x}\n")
	f.Fuzz(func(t *testing.T, data string) {
		_, err := parse([]byte(data))
		if err != nil && strings.HasPrefix(err.Error(), "consent:") {
			t.Errorf("a consent error without its line: %v", err)
		}
	})
}
