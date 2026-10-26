// Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
// SPDX-License-Identifier: MIT

//go:build browser

package server

import (
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/go-rod/rod/lib/proto"

	"github.com/RhythmShahriar/echo/internal/browser"
	"github.com/RhythmShahriar/echo/internal/testenv"
)

// realRoot copies the checkout's testbed/consent/sites to a temporary
// root, so the test can switch states without touching state/.
func realRoot(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	if err := os.CopyFS(filepath.Join(dir, "sites"), os.DirFS(filepath.Join("..", "sites"))); err != nil {
		t.Fatal(err)
	}
	return dir
}

// pingOrExempt reports whether h may reach the sink before any choice
// on a good site: a cookieless Consent Mode ping (advanced mode) or
// Plausible, which needs no consent.
func pingOrExempt(h Hit) bool {
	if h.Host == "plausible.io" {
		return true
	}
	q, _ := url.ParseQuery(h.Query)
	return strings.HasSuffix(h.Path, "/g/collect") && q.Get("gcs") == "G100" && q.Get("cid") == ""
}

// TestScenarioSitesLoad loads every scenario site in plain Chrome (no
// Echo, no clicks): no script error in either state, nothing but
// cookieless pings or exempt analytics before the choice in the good
// state plus hits before the choice in the broken state of the
// scenarios whose bug is exactly that.
func TestScenarioSitesLoad(t *testing.T) {
	root := realRoot(t)
	l := startLive(t, root)
	ids, err := l.srv.Sites()
	if err != nil {
		t.Fatal(err)
	}
	firesBroken := map[string]bool{"c01": true, "c02": true, "c03": true, "c05": true, "c08": true, "c09": true, "c12": true, "c14": true,
		"c17": true, "c23": true, "c25": true, "sgtm-first-party": true, "pii-beacons": true, "secret-url": true}
	skip := map[string]bool{"redirect-chain": true, "redirect-loop": true, "self-signed": true, "request-storm": true, "huge-datalayer": true, "ssrf-probe": true}
	// Clarity and Hotjar wait 3 s before their first hit.
	slow := map[string]bool{"c09": true, "g03": true, "g08": true}
	// Their own scripts are broken on purpose and point at evil.test.
	hostile := map[string]bool{"hostile-text": true}
	for _, id := range ids {
		if skip[id] {
			continue
		}
		t.Run(id, func(t *testing.T) {
			t.Parallel()
			for _, broken := range []bool{false, true} {
				if err := l.srv.SetBroken(id, broken); err != nil {
					t.Fatal(err)
				}
				l.srv.ResetScenario(id)
				errs := l.openWatching(t, "https://"+id+SiteSuffix+"/")
				wait := 1500 * time.Millisecond
				if slow[id] {
					wait = 3500 * time.Millisecond
				}
				time.Sleep(testenv.Timeout(wait))
				state := map[bool]string{false: "good", true: "broken"}[broken]
				expected := broken && hostile[id]
				if e := errs(); len(e) > 0 && !expected {
					t.Errorf("%s: script errors: %v", state, e)
				}
				var early []string
				for _, h := range l.srv.Hits(id) {
					if !pingOrExempt(h) {
						early = append(early, h.Host+h.Path)
					}
				}
				switch {
				case !broken && len(early) > 0:
					t.Errorf("hits before any choice in the good state: %v", early)
				case broken && firesBroken[id] && len(l.srv.Hits(id)) == 0:
					t.Error("no hit before the choice in the broken state")
				}
				if u := l.srv.Count(id).Unmapped; len(u) > 0 && !expected {
					t.Errorf("%s: asked for hosts the testbed doesn't serve: %v", state, u)
				}
			}
		})
	}
}

// openWatching opens u in a fresh context and returns a function that
// lists the uncaught script errors seen so far.
func (l *liveServer) openWatching(t *testing.T, u string) func() []string {
	t.Helper()
	ctx, err := l.sess.Incognito()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = ctx.Close() })
	p, err := browser.NewTab(ctx)
	if err != nil {
		t.Fatal(err)
	}
	p = p.Timeout(testenv.Timeout(30 * time.Second))
	var mu sync.Mutex
	var errs []string
	if err := (proto.RuntimeEnable{}).Call(p); err != nil {
		t.Fatal(err)
	}
	go p.EachEvent(func(e *proto.RuntimeExceptionThrown) {
		mu.Lock()
		defer mu.Unlock()
		msg := e.ExceptionDetails.Text
		if e.ExceptionDetails.Exception != nil {
			msg += " " + e.ExceptionDetails.Exception.Description
		}
		errs = append(errs, msg)
	})()
	if err := p.Navigate(u); err != nil {
		t.Fatal(err)
	}
	_ = p.WaitLoad()
	return func() []string {
		mu.Lock()
		defer mu.Unlock()
		return append([]string(nil), errs...)
	}
}
