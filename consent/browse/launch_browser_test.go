// Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
// SPDX-License-Identifier: MIT

//go:build browser

package browse

import (
	"net"
	"net/url"
	"testing"

	"github.com/RhythmShahriar/echo/consent/consenttest"
	"github.com/RhythmShahriar/echo/internal/browser"
)

// needChrome skips a browser test where no Chrome is installed.
func needChrome(t *testing.T) {
	t.Helper()
	if _, _, err := browser.Find(browser.Options{NoDownload: true}); err != nil {
		t.Skip("no Chrome installed:", err)
	}
}

// DevTools listens on loopback only, on a port Chrome picked, never the
// well-known 9222 another process on the machine could guess.
func TestConsentDevToolsLoopbackOnly(t *testing.T) {
	needChrome(t)
	p := consenttest.NewPlan(t)
	p.Browser.NoDownload = true
	b, err := Launch(t.Context(), p, "127.0.0.1:35064")
	if err != nil {
		t.Fatal(err)
	}
	defer b.Close()
	u, err := url.Parse(b.ControlURL)
	if err != nil {
		t.Fatal(err)
	}
	host, port, err := net.SplitHostPort(u.Host)
	if err != nil {
		t.Fatal(err)
	}
	if u.Scheme != "ws" || host != "127.0.0.1" || port == "9222" || port == "0" {
		t.Errorf("DevTools at %s", b.ControlURL)
	}
}
