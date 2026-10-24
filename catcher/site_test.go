// Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
// SPDX-License-Identifier: MIT

package catcher

import (
	"fmt"
	"slices"
	"sync"
	"testing"

	"github.com/RhythmShahriar/echo/wire"
)

func TestSiteHostNeverUnsealsAProvider(t *testing.T) {
	stripeKey := "sk" + "_test_51Abc"
	cases := []struct {
		name, site, method, url, body string
		headers                       map[string]string
		want                          string
	}{
		// Negative: a forged site host naming the provider.
		{"twilio", "api.twilio.com", "POST", "https://api.twilio.com/2010-04-01/Accounts/AC1/Messages.json",
			"To=%2B15551230000&Body=hi", nil, wire.Fake},
		{"sendgrid", "api.sendgrid.com", "POST", "https://api.sendgrid.com/v3/mail/send",
			`{"personalizations":[{"to":[{"email":"a@example.com"}]}],"subject":"Hi"}`, nil, wire.Fake},
		{"stripe", "api.stripe.com:443", "POST", "https://api.stripe.com/v1/payment_intents",
			"amount=500&currency=usd", map[string]string{"Authorization": "Bearer " + stripeKey}, wire.Fake},
		{"provider path no recognizer claims", "https://api.twilio.com", "GET", "https://api.twilio.com/unknown", "", nil, wire.Fake},
		{"recognized call to the site itself", "shop.test", "POST", "https://shop.test/hook", "{}",
			map[string]string{"X-WC-Webhook-Topic": "order.created"}, wire.Fake},
		{"public suffix", "github.io", "GET", "https://github.io/", "", nil, wire.Fake},
		// Positive: the site calling itself.
		{"own site", "shop.test", "GET", "https://shop.test/wp-cron.php", "", nil, wire.Allow},
		{"own site with scheme", "https://shop.test", "POST", "https://shop.test/wp-json/x", "{}", nil, wire.Allow},
		{"compose service name", "wordpress", "GET", "http://wordpress/wp-cron.php", "", nil, wire.Allow},
		{"site under a public suffix", "shop.github.io", "GET", "https://shop.github.io/", "", nil, wire.Allow},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			s := NewServer(NewInbox(10), Config{Warn: func(string) {}})
			d, err := s.Capture(wire.Capture{Version: 1, Kind: "http", Phase: wire.Before, Source: wire.Source{Via: "laravel", Host: c.site},
				HTTP: &wire.HTTP{Method: c.method, URL: c.url, Body: c.body, Headers: c.headers}})
			if err != nil || d.Action != c.want {
				t.Errorf("site %s, %s %s: %+v %v, want %s", c.site, c.method, c.url, d, err, c.want)
			}
		})
	}
}

func TestSiteHostWarnsOncePerHost(t *testing.T) {
	var mu sync.Mutex
	var got []string
	s := NewServer(NewInbox(10), Config{Warn: func(msg string) { mu.Lock(); got = append(got, msg); mu.Unlock() }})
	for range 3 {
		for _, site := range []string{"api.twilio.com", "co.uk", "shop.test"} {
			_, _ = s.Capture(wire.Capture{Version: 1, Kind: "http", Source: wire.Source{Host: site},
				HTTP: &wire.HTTP{Method: "GET", URL: "https://api.twilio.com/x"}})
		}
	}
	want := []string{
		"connector reported site host api.twilio.com, which is a provider; ignoring",
		"connector reported site host co.uk, which is a public suffix; ignoring",
	}
	if !slices.Equal(got, want) {
		t.Errorf("warnings = %q", got)
	}
}

// TestSiteHostWarningsAreBounded: a caller that reports a new host with
// every capture gets warned about the first maxWarned, and the set of
// hosts warned about stops growing.
func TestSiteHostWarningsAreBounded(t *testing.T) {
	n := 0
	s := NewServer(NewInbox(10), Config{Warn: func(string) { n++ }})
	for i := range maxWarned + 50 {
		if got := s.siteHost(fmt.Sprintf("x%d.twilio.com", i), "x.twilio.com", false); got != "" {
			t.Fatalf("a provider host was trusted: %q", got)
		}
	}
	if n != maxWarned || s.warnedN.Load() != maxWarned {
		t.Errorf("warned %d times, %d hosts kept; want %d", n, s.warnedN.Load(), maxWarned)
	}
}

// TestSelfCallToARecognizedHost says what the host is: where a request
// Echo recognizes as a provider's went, not a provider itself.
func TestSelfCallToARecognizedHost(t *testing.T) {
	var got string
	s := NewServer(NewInbox(10), Config{Warn: func(msg string) { got = msg }})
	if h := s.siteHost("shop.test", "shop.test", true); h != "" {
		t.Fatalf("trusted %q", h)
	}
	if got != "connector reported site host shop.test, which is where a request Echo recognizes as a provider's went; ignoring" {
		t.Errorf("warning %q", got)
	}
}
