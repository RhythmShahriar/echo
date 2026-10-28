// Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
// SPDX-License-Identifier: MIT

package browse

import (
	"errors"
	"io"
	"log"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync/atomic"
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

// An https --map-host address is checked against the original host, not
// the address: without -k an untrusted certificate is a TLS error, with
// -k the request goes through with the original host as its SNI.
func TestMapClientTLS(t *testing.T) {
	var sni atomic.Value
	srv := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		sni.Store(r.TLS.ServerName)
		_, _ = w.Write([]byte("ok"))
	}))
	srv.Config.ErrorLog = log.New(io.Discard, "", 0) // the refused handshake is the point
	srv.StartTLS()
	t.Cleanup(srv.Close)
	base := mapBase(srv.URL)
	if base == nil || base.Scheme != "https" {
		t.Fatalf("mapBase(%s) = %v", srv.URL, base)
	}
	get := func(m *mapper) error {
		req, err := http.NewRequestWithContext(t.Context(), http.MethodGet, srv.URL+"/", nil)
		if err != nil {
			t.Fatal(err)
		}
		req.Host = "g01.shop.test"
		resp, err := m.clientFor(base, "g01.shop.test").Do(req)
		if err == nil {
			_ = resp.Body.Close()
		}
		return err
	}
	err := get(newMapper(nil, false))
	if err == nil || !tlsError(err) {
		t.Fatalf("without -k: %v; want a TLS error", err)
	}
	if got := mapErrorText(mapError{addr: "https://" + base.Host, err: err}); !strings.HasPrefix(got, "https://"+base.Host+" (--map-host): ") ||
		!strings.Contains(got, "certificate") {
		t.Errorf("mapErrorText: %q", got)
	}
	m := newMapper(nil, true)
	if err := get(m); err != nil {
		t.Fatalf("with -k: %v", err)
	}
	if got, _ := sni.Load().(string); got != "g01.shop.test" {
		t.Errorf("SNI %q; want the original host", got)
	}
	if m.clientFor(base, "G01.shop.test") != m.clientFor(base, "g01.shop.test") || m.clientFor(base, "a.shop.test") == m.clientFor(base, "g01.shop.test") {
		t.Error("an https base doesn't get one client per host")
	}
	if plain := mapBase("127.0.0.1:1"); m.clientFor(plain, "x.test") != m.client {
		t.Error("a plain http base doesn't use the shared client")
	}
	if tlsError(errors.New("dial tcp: connection refused")) {
		t.Error("a refused connection reads as a TLS error")
	}
}
