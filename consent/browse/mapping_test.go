// Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
// SPDX-License-Identifier: MIT

package browse

import (
	"net/url"
	"testing"

	"github.com/RhythmShahriar/echo/consent"
)

// An https page --map-host answers from a plain http address crossed to
// Echo unencrypted: the result must not claim verified TLS.
func TestPlainMapped(t *testing.T) {
	target := func(raw string) consent.Target {
		u, err := url.Parse(raw)
		if err != nil {
			t.Fatal(err)
		}
		return consent.Target{URL: u}
	}
	for _, c := range []struct {
		url  string
		maps map[string]string
		want bool
	}{
		{"https://g02.shop.test/", map[string]string{"*.shop.test": "127.0.0.1:39100"}, true},
		{"https://g02.shop.test/", map[string]string{"g02.shop.test": "http://10.0.0.5:8080"}, true},
		{"https://g02.shop.test/", map[string]string{"*.shop.test": "https://10.0.0.5:8443"}, false},
		{"http://g02.shop.test/", map[string]string{"*.shop.test": "127.0.0.1:39100"}, false},
		{"https://g02.shop.test/", map[string]string{"analytics.tiktok.com": "127.0.0.1:39100"}, false},
		{"https://g02.shop.test/", nil, false},
	} {
		p := &consent.Plan{Targets: []consent.Target{target(c.url)}, MapHosts: c.maps}
		if got := plainMapped(p); got != c.want {
			t.Errorf("%s %v: %v, want %v", c.url, c.maps, got, c.want)
		}
	}
}
