// Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
// SPDX-License-Identifier: MIT

package recognize_test

import (
	"net/url"
	"testing"

	"github.com/RhythmShahriar/echo/catcher"
	"github.com/RhythmShahriar/echo/model"
	"github.com/RhythmShahriar/echo/recognize"
	"github.com/RhythmShahriar/echo/wire"
)

// TestLivePaymentsAreRefusedInEveryMode runs each gateway's calls through
// the catcher: a live call is refused (faked with the gateway's error and
// recorded as blocked) when sealed, when observing and when its host is
// allowlisted. A sandbox call is faked when sealed and sent otherwise.
func TestLivePaymentsAreRefusedInEveryMode(t *testing.T) {
	for _, g := range recognize.GatewayRequests() {
		u, _ := url.Parse(g.URL)
		for _, cfg := range []catcher.Config{
			{Mode: catcher.Seal},
			{Mode: catcher.Observe},
			{Mode: catcher.Seal, Allow: []string{u.Hostname()}},
		} {
			srv := catcher.NewServer(catcher.NewInbox(10), cfg)
			d, err := srv.Capture(wire.Capture{Version: wire.Version, Kind: "http", Phase: wire.Before,
				HTTP: &wire.HTTP{Method: g.Method, URL: g.URL, Headers: g.Headers, Body: g.Body}})
			if err != nil {
				t.Fatal(err)
			}
			m, _ := srv.Inbox.Get(d.ID)
			name := g.Provider + " " + u.Host + " " + cfg.Mode
			if len(cfg.Allow) > 0 {
				name += " allowlisted"
			}
			sealed := cfg.Mode == catcher.Seal && len(cfg.Allow) == 0
			switch {
			case m.Provider != g.Provider:
				t.Errorf("%s: recorded as %q", name, m.Provider)
			case g.Live && (d.Action != wire.Fake || d.Response == nil || d.Response.Status < 400 || m.Status != model.Blocked):
				t.Errorf("%s: live call not refused: %s %+v, status %s", name, d.Action, d.Response, m.Status)
			case !g.Live && sealed && (d.Action != wire.Fake || d.Response.Status >= 300 || m.Status != model.Faked):
				t.Errorf("%s: sandbox call not faked: %s %+v", name, d.Action, d.Response)
			case !g.Live && !sealed && (d.Action != wire.Allow || m.Status != model.Sent):
				t.Errorf("%s: sandbox call not sent: %s, status %s", name, d.Action, m.Status)
			}
		}
	}
}
