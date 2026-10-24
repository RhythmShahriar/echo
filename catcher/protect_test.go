// Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
// SPDX-License-Identifier: MIT

package catcher

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/RhythmShahriar/echo/wire"
)

const captureJSON = `{"version":1,"kind":"email","email":{"to":["a@example.com"],"subject":"from a page"}}`

func TestProtect(t *testing.T) {
	for _, c := range []struct {
		name   string
		cfg    Config
		method string
		path   string
		host   string
		header map[string]string
		body   string
		want   int
	}{
		{"text/plain POST", Config{}, "POST", "/v1/capture", "127.0.0.1:8025",
			map[string]string{"Content-Type": "text/plain"}, captureJSON, http.StatusUnsupportedMediaType},
		{"text/plain begin", Config{}, "POST", "/v1/actions/begin", "127.0.0.1:8025",
			map[string]string{"Content-Type": "text/plain;charset=UTF-8", "User-Agent": "curl/8.5.0"}, `{"run":"r","action":"a"}`, http.StatusUnsupportedMediaType},
		{"form POST from a browser", Config{}, "POST", "/v1/capture", "127.0.0.1:8025",
			map[string]string{"Content-Type": "application/x-www-form-urlencoded", "User-Agent": "Mozilla/5.0 (X11; Linux x86_64) Firefox/131.0"}, captureJSON, http.StatusUnsupportedMediaType},
		{"form POST with Sec-Fetch", Config{}, "POST", "/v1/capture", "127.0.0.1:8025",
			map[string]string{"Content-Type": "application/x-www-form-urlencoded", "Sec-Fetch-Site": "cross-site"}, captureJSON, http.StatusUnsupportedMediaType},
		{"multipart POST", Config{}, "POST", "/v1/capture", "127.0.0.1:8025",
			map[string]string{"Content-Type": "multipart/form-data; boundary=x"}, captureJSON, http.StatusUnsupportedMediaType},
		{"curl -d", Config{}, "POST", "/v1/capture", "127.0.0.1:8025",
			map[string]string{"Content-Type": "application/x-www-form-urlencoded", "User-Agent": "curl/8.5.0"}, captureJSON, http.StatusOK},
		{"foreign Origin", Config{}, "POST", "/v1/capture", "127.0.0.1:8025",
			map[string]string{"Content-Type": "application/json", "Origin": "https://evil.example"}, captureJSON, http.StatusForbidden},
		{"null Origin", Config{}, "GET", "/v1/messages", "127.0.0.1:8025", map[string]string{"Origin": "null"}, "", http.StatusForbidden},
		{"Origin on another port", Config{}, "GET", "/v1/messages", "127.0.0.1:8025", map[string]string{"Origin": "http://127.0.0.1:3000"}, "", http.StatusForbidden},
		{"rebound Host", Config{}, "GET", "/v1/messages", "rebind.evil.example:8025", nil, "", http.StatusForbidden},
		{"Host 0.0.0.0", Config{Listen: "0.0.0.0:8025"}, "GET", "/v1/messages", "0.0.0.0:8025", nil, "", http.StatusForbidden},
		{"127.0.0.1", Config{}, "GET", "/v1/messages", "127.0.0.1:8025", nil, "", http.StatusOK},
		{"localhost", Config{}, "GET", "/v1/messages", "localhost:8025", nil, "", http.StatusOK},
		{"echo.localhost", Config{}, "GET", "/v1/messages", "echo.localhost", nil, "", http.StatusOK},
		{"[::1]", Config{}, "GET", "/v1/messages", "[::1]:8025", nil, "", http.StatusOK},
		{"the listen host", Config{Listen: "echo:8025"}, "GET", "/v1/messages", "echo:8025", nil, "", http.StatusOK},
		{"Echo's own page", Config{}, "POST", "/v1/capture", "127.0.0.1:8025",
			map[string]string{"Content-Type": "application/json; charset=utf-8", "Origin": "http://127.0.0.1:8025"}, captureJSON, http.StatusOK},
		{"JSON POST from a connector", Config{}, "POST", "/v1/capture", "127.0.0.1:8025",
			map[string]string{"Content-Type": "application/json"}, captureJSON, http.StatusOK},
		{"POST with no body", Config{}, "POST", "/v1/actions/end", "127.0.0.1:8025", nil, "", http.StatusNoContent},
		{"non-loopback Host, valid token", Config{Token: "t0k"}, "GET", "/v1/messages", "echo.internal:8025",
			map[string]string{wire.TokenHeader: "t0k"}, "", http.StatusOK},
		{"non-loopback Host, wrong token", Config{Token: "t0k"}, "GET", "/v1/messages", "echo.internal:8025",
			map[string]string{wire.TokenHeader: "nope"}, "", http.StatusForbidden},
		{"loopback, token set but missing", Config{Token: "t0k"}, "GET", "/v1/messages", "127.0.0.1:8025", nil, "", http.StatusUnauthorized},
		{"Mailpit delete from curl -d", Config{}, "DELETE", "/api/v1/messages", "localhost:8025",
			map[string]string{"Content-Type": "application/x-www-form-urlencoded"}, `{"IDs":[]}`, http.StatusOK},
		{"Mailpit delete from a page", Config{}, "DELETE", "/api/v1/messages", "localhost:8025",
			map[string]string{"Origin": "https://evil.example", "Content-Type": "application/json"}, `{"IDs":[]}`, http.StatusForbidden},
		{"Mailpit list from curl", Config{}, "GET", "/api/v1/messages", "localhost:8025", map[string]string{"User-Agent": "curl/8.5.0"}, "", http.StatusOK},
		{"Mailpit list from a page", Config{}, "GET", "/api/v1/messages", "localhost:8025", map[string]string{"Origin": "https://evil.example"}, "", http.StatusForbidden},
	} {
		t.Run(c.name, func(t *testing.T) {
			s := NewServer(NewInbox(10), c.cfg)
			var body io.Reader
			if c.body != "" {
				body = strings.NewReader(c.body)
			}
			r := httptest.NewRequest(c.method, "http://"+c.host+c.path, body)
			for k, v := range c.header {
				r.Header.Set(k, v)
			}
			w := httptest.NewRecorder()
			s.ServeHTTP(w, r)
			if w.Code != c.want {
				t.Errorf("got %d %s, want %d", w.Code, strings.TrimSpace(w.Body.String()), c.want)
			}
			if w.Code >= 400 && len(s.Inbox.List(Filter{})) != 0 {
				t.Error("a refused request was captured")
			}
		})
	}
}

// TestProtectOverRealHTTP checks the guard sees the Host a client sends
// on the wire, through a real listener.
func TestProtectOverRealHTTP(t *testing.T) {
	srv := httptest.NewServer(NewServer(NewInbox(10), Config{}))
	defer srv.Close()
	req, _ := http.NewRequest("GET", srv.URL+"/v1/messages", nil)
	req.Host = "rebind.evil.example"
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	_ = resp.Body.Close()
	if resp.StatusCode != http.StatusForbidden {
		t.Errorf("rebound Host: %s", resp.Status)
	}
	resp, err = http.Post(srv.URL+"/v1/capture", "text/plain", strings.NewReader(captureJSON))
	if err != nil {
		t.Fatal(err)
	}
	_ = resp.Body.Close()
	if resp.StatusCode != http.StatusUnsupportedMediaType {
		t.Errorf("text/plain capture: %s", resp.Status)
	}
}

// TestHostAllowedPrivateNetwork: under --private-network a private peer
// may name Echo by a service name or an IP address; a domain name only
// when Hosts lists it. A public peer gets nothing more than before.
func TestHostAllowedPrivateNetwork(t *testing.T) {
	hosts := []string{"*.ddev.site", "echo.myapp.lndo.site"}
	for _, c := range []struct {
		host, peer string
		private    bool
		want       bool
	}{
		{"echo:8025", "172.18.0.4:40000", true, true},
		{"mailpit", "10.0.0.7:1", true, true},
		{"172.18.0.2:8025", "172.18.0.4:40000", true, true},
		{"[fd00::2]:8025", "[fd00::9]:1", true, true},
		{"echo:8025", "100.64.1.2:1", true, true},
		{"echo:8025", "127.0.0.1:1", true, true},
		{"echo:8025", "203.0.113.9:1", true, false}, // a public peer
		{"echo:8025", "172.18.0.4:40000", false, false},
		{"0.0.0.0:8025", "172.18.0.4:40000", true, false},
		{"rebind.attacker.example", "172.18.0.1:1", true, false},
		{"shop.ddev.site:8026", "203.0.113.9:1", false, true}, // listed: any peer
		{"ddev.site", "172.18.0.1:1", false, false},
		{"evilddev.site", "172.18.0.1:1", false, false},
		{"ECHO.MyApp.lndo.site", "172.18.0.1:1", false, true},
		{"localhost:8025", "203.0.113.9:1", false, true},
	} {
		r := httptest.NewRequest("GET", "/v1/health", nil)
		r.Host, r.RemoteAddr = c.host, c.peer
		if got := HostAllowed(r, hosts, c.private); got != c.want {
			t.Errorf("Host %s from %s (private %v): %v, want %v", c.host, c.peer, c.private, got, c.want)
		}
	}
}
