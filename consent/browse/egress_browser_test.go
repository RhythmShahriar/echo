// Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
// SPDX-License-Identifier: MIT

//go:build browser

package browse

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/go-rod/rod/lib/proto"

	"github.com/RhythmShahriar/echo/consent/browse/egress"
	"github.com/RhythmShahriar/echo/consent/consenttest"
	"github.com/RhythmShahriar/echo/internal/browser"
)

// A loopback target's context reaches its own server through its guard;
// a page outside every run's context reaches only the refuse-all
// listener, which counts it as browser traffic and lets nothing out.
func TestContextProxyAndRefuseAll(t *testing.T) {
	needChrome(t)
	var hits atomic.Int64
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/favicon.ico" {
			http.NotFound(w, r)
			return
		}
		hits.Add(1)
		w.Header().Set("Content-Type", "text/html")
		_, _ = io.WriteString(w, `<title>ok</title><img src="http://169.254.169.254/latest/meta-data/">`)
	}))
	defer srv.Close()
	internal, err := egress.StartRefuseAll(nil)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = internal.Close() }()
	guard, err := egress.Start(egress.Options{Target: "127.0.0.1"})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = guard.Close() }()
	p := consenttest.NewPlan(t)
	p.Browser.NoDownload = true
	b, err := Launch(t.Context(), p, internal.Addr())
	if err != nil {
		t.Fatal(err)
	}
	defer b.Close()

	sess, err := b.SessionWithProxy(guard.URL(), bypassLoopback)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = sess.Close() }()
	tab, err := browser.NewTab(sess)
	if err != nil {
		t.Fatal(err)
	}
	if err := tab.Timeout(20 * time.Second).Navigate(srv.URL + "/"); err != nil {
		t.Fatal(err)
	}
	_ = tab.Timeout(20 * time.Second).WaitLoad()
	if hits.Load() != 1 {
		t.Errorf("the target was reached %d times; want 1", hits.Load())
	}
	deadline := time.Now().Add(10 * time.Second)
	for guard.Reason("169.254.169.254", "80") == "" && time.Now().Before(deadline) {
		time.Sleep(50 * time.Millisecond)
	}
	if got := guard.Reason("169.254.169.254", "80"); got != egress.ReasonPrivate {
		t.Errorf("metadata image: reason %q, stats %+v", got, guard.Stats())
	}

	// The default context has no proxy of its own: the browser-wide
	// listener answers and nothing reaches the server.
	before := internal.Stats().BrowserInternal
	page, err := b.Page(proto.TargetCreateTarget{URL: "about:blank"})
	if err != nil {
		t.Fatal(err)
	}
	_ = page.Timeout(20 * time.Second).Navigate(srv.URL + "/outside")
	if hits.Load() != 1 {
		t.Errorf("a page outside every context reached the server: %d hits", hits.Load())
	}
	if internal.Stats().BrowserInternal <= before {
		t.Errorf("refuse-all counted nothing: %+v", internal.Stats())
	}
	if !strings.HasPrefix(guard.URL(), "http://127.0.0.1:") {
		t.Errorf("guard at %s", guard.URL())
	}
}
