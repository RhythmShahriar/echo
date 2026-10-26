// Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
// SPDX-License-Identifier: MIT

//go:build browser

package server

import (
	"net"
	"net/url"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/go-rod/rod"

	"github.com/RhythmShahriar/echo/internal/browser"
	"github.com/RhythmShahriar/echo/internal/testenv"
)

// liveServer is the testbed server on a free port with https plus a
// plain Chrome (not Echo) whose every host name resolves to it.
type liveServer struct {
	t    *testing.T
	srv  *Server
	sess *rod.Browser
}

// startLive serves root (a folder with sites/) and starts Chrome, or
// skips the test when there is no Chrome.
func startLive(t *testing.T, root string) *liveServer {
	t.Helper()
	if _, _, err := browser.Find(browser.Options{NoDownload: true}); err != nil {
		t.Skip("Chrome not found:", err)
	}
	dir := filepath.Join(t.TempDir(), "ca")
	if err := MakeCA(dir); err != nil {
		t.Fatal(err)
	}
	ca, err := LoadCA(dir)
	if err != nil {
		t.Fatal(err)
	}
	srv, err := New(Options{Root: root, CA: ca, Admin: "127.0.0.1:9"})
	if err != nil {
		t.Fatal(err)
	}
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	go func() { _ = srv.Serve(l) }()
	t.Cleanup(func() { _ = l.Close() })
	_, port, _ := net.SplitHostPort(l.Addr().String())
	// Every name and port goes to the testbed, so nothing can reach a
	// real host even when a stand-in names one by mistake.
	b, err := browser.Launch(t.Context(), browser.Options{NoDownload: true, Insecure: true,
		Flags: []string{"--host-resolver-rules=MAP * 127.0.0.1:" + port, "--disable-background-networking"}})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(b.Close)
	sess, err := b.Session()
	if err != nil {
		t.Fatal(err)
	}
	return &liveServer{t: t, srv: srv, sess: sess}
}

// open loads u in a new tab of a fresh incognito context, so no page
// sees another's cookies.
func (l *liveServer) open(u string) *rod.Page {
	l.t.Helper()
	ctx, err := l.sess.Incognito()
	if err != nil {
		l.t.Fatal(err)
	}
	l.t.Cleanup(func() { _ = ctx.Close() })
	p, err := browser.NewTab(ctx)
	if err != nil {
		l.t.Fatal(err)
	}
	p = p.Timeout(testenv.Timeout(30 * time.Second))
	if err := p.Navigate(u); err != nil {
		l.t.Fatal(err)
	}
	if err := p.WaitLoad(); err != nil {
		l.t.Fatal(err)
	}
	return p
}

// eval runs js (a function expression) in p and returns its result as text.
func (l *liveServer) eval(p *rod.Page, js string, args ...any) string {
	l.t.Helper()
	r, err := p.Eval(js, args...)
	if err != nil {
		l.t.Fatalf("eval %s: %v", js, err)
	}
	return r.Value.String()
}

// waitHits waits until scenario has at least n hits matching match,
// then returns the matching hits.
func (l *liveServer) waitHits(scenario string, n int, match func(Hit) bool, within time.Duration) []Hit {
	l.t.Helper()
	deadline := time.Now().Add(testenv.Timeout(within))
	for {
		var got []Hit
		for _, h := range l.srv.Hits(scenario) {
			if match(h) {
				got = append(got, h)
			}
		}
		if len(got) >= n || time.Now().After(deadline) {
			return got
		}
		time.Sleep(50 * time.Millisecond)
	}
}

// waitTrue waits until js returns true in p.
func (l *liveServer) waitTrue(p *rod.Page, js string, within time.Duration) bool {
	l.t.Helper()
	deadline := time.Now().Add(testenv.Timeout(within))
	for time.Now().Before(deadline) {
		if l.eval(p, js) == "true" {
			return true
		}
		time.Sleep(50 * time.Millisecond)
	}
	return false
}

// pathIs matches hits to host+path.
func pathIs(hostPath string) func(Hit) bool {
	return func(h Hit) bool { return h.Host+h.Path == hostPath || strings.HasPrefix(h.Host+h.Path, hostPath) }
}

// param reads a query parameter of a hit.
func param(h Hit, name string) string {
	q, _ := url.ParseQuery(h.Query)
	return q.Get(name)
}
