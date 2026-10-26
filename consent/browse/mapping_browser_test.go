// Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
// SPDX-License-Identifier: MIT

//go:build browser

package browse

import (
	"net"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/go-rod/rod/lib/proto"

	"github.com/RhythmShahriar/echo/consent"
	"github.com/RhythmShahriar/echo/consent/consenttest"
	"github.com/RhythmShahriar/echo/internal/testenv"
)

// TestMapSetCookie: a Set-Cookie from a mapped address lands as a
// cookie of the original host, as it would from the real server.
func TestMapSetCookie(t *testing.T) {
	st := newSite(t)
	st.page("shop.test/", `<!doctype html><title>c</title>`, "Set-Cookie", "sid="+strings.Repeat("s", 10)+"; Path=/; Max-Age=600")
	p := consenttest.NewPlan(t, "https://shop.test/")
	p.MapHosts = map[string]string{"shop.test": st.addr()}
	g := newRig(t, p)
	s := g.open("https://shop.test/", consent.RunNone)
	g.load(s, "https://shop.test/")
	res, err := proto.StorageGetCookies{BrowserContextID: s.ctx.BrowserContextID}.Call(g.h.root)
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, c := range res.Cookies {
		if c.Name == "sid" {
			found = true
			if c.Domain != "shop.test" || c.Path != "/" {
				t.Errorf("cookie on %s%s; want shop.test/", c.Domain, c.Path)
			}
		}
	}
	if !found {
		t.Errorf("the mapped Set-Cookie isn't a cookie of shop.test: %d cookies", len(res.Cookies))
	}
	var doc *consent.Request
	for _, e := range requestEvents(s.close()) {
		if e.Request.Type == "document" {
			doc = e.Request
		}
	}
	if doc == nil || !doc.Mapped || doc.Status != 200 {
		t.Errorf("document %+v", doc)
	}
	if !g.e.Mapped() {
		t.Error("Engine.Mapped is false with --map-host")
	}
}

// TestMapOnlyRefused: with --map-only an unmapped image is refused in
// the browser, recorded with the reason and counted in Network.Refused.
func TestMapOnlyRefused(t *testing.T) {
	st := newSite(t)
	st.page("shop.test/", `<!doctype html><title>m</title><img src="https://cdn.unmapped.test/x.gif">`)
	p := consenttest.NewPlan(t, "https://shop.test/")
	p.MapHosts = map[string]string{"shop.test": st.addr()}
	p.MapOnly = true
	g := newRig(t, p)
	run := g.openRun("https://shop.test/", consent.RunNone)
	g.load(run.s, "https://shop.test/")
	rec := run.Close()
	var img *consent.Request
	for _, e := range requestEvents(rec) {
		if e.Request.Host == "cdn.unmapped.test" {
			img = e.Request
		}
	}
	if img == nil || img.Refused != "not mapped" || img.Mapped {
		t.Fatalf("unmapped image %+v", img)
	}
	if rec.Refused < 1 || g.e.Network().Refused < 1 {
		t.Errorf("refused: run %d, network %d", rec.Refused, g.e.Network().Refused)
	}
	if st.hits("cdn.unmapped.test", "") != 0 {
		t.Error("the unmapped host was answered")
	}
}

// TestMappedHeldNotFetched: a mapped tracker host in held mode is held,
// never fetched: the stand-in sink sees nothing.
func TestMappedHeldNotFetched(t *testing.T) {
	st := newSite(t)
	st.page("shop.test/", `<!doctype html><title>h</title><img src="https://tracker.test/g/collect?v=2&en=page_view">`)
	st.page("tracker.test/g/collect", "sink")
	p := consenttest.NewPlan(t, "https://shop.test/")
	p.MapHosts = map[string]string{"shop.test": st.addr(), "tracker.test": st.addr()}
	g := newRig(t, p)
	s := g.open("https://shop.test/", consent.RunNone)
	g.load(s, "https://shop.test/")
	waitRequest(t, s.rec, "en=page_view")
	rec := s.close()
	if n := st.hits("tracker.test", ""); n != 0 {
		t.Errorf("the mapped tracker was fetched %d times", n)
	}
	for _, e := range requestEvents(rec) {
		if e.Request.Host == "tracker.test" && (!e.Request.Held || e.Request.Mapped) {
			t.Errorf("tracker hit %+v", e.Request)
		}
	}
}

// TestMapHTTPS: an https URL of a mapped host loads from a plain-http
// local server: Echo answers it, so no TLS is involved.
func TestMapHTTPS(t *testing.T) {
	st := newSite(t)
	st.page("shop.test/", `<!doctype html><title>s</title><script src="https://www.googletagmanager.com/gtag/js?id=G-TEST1234"></script>`)
	st.page("www.googletagmanager.com/gtag/js", `window.gtagLoaded = true;`)
	p := consenttest.NewPlan(t, "https://shop.test/")
	p.MapHosts = map[string]string{"shop.test": st.addr(), "www.googletagmanager.com": st.addr()}
	g := newRig(t, p)
	s := g.open("https://shop.test/", consent.RunNone)
	g.load(s, "https://shop.test/")
	g.waitFor(s, `window.gtagLoaded === true`)
	rec := s.close()
	ok := false
	for _, e := range requestEvents(rec) {
		if e.Request.Host == "www.googletagmanager.com" {
			ok = e.Request.Mapped && e.Request.Status == 200 && strings.HasPrefix(e.Request.URL, "https://")
		}
	}
	if !ok || st.hits("www.googletagmanager.com", "/gtag/js") != 1 {
		t.Error("the https script wasn't answered from the mapped address")
	}
}

// TestMappedHostNeverReal: a mapped host's requests are answered from
// its mapped address or refused, never sent to its real address, even
// those outside the tab's frames: a service worker's script and its own
// fetch. "localhost" stands in for a production host mapped to staging
// over https; a listener on its real port counts every connection and
// must see none. The target is 127.0.0.1, so its guard allows loopback
// for anything unmapped.
func TestMappedHostNeverReal(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = ln.Close() })
	var realConns atomic.Int64
	go func() {
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			realConns.Add(1)
			_ = c.Close()
		}
	}()
	_, realPort, _ := net.SplitHostPort(ln.Addr().String())
	st := newSite(t)
	st.page("127.0.0.1/", `<!doctype html><title>t</title>`)
	st.page("localhost/", `<!doctype html><title>m</title><script>
navigator.serviceWorker.register("/sw.js").then(() => { window.swDone = true; }, e => { window.swDone = true; window.swErr = String(e); });
</script>`)
	st.page("localhost/sw.js", `self.addEventListener("install", e => { e.waitUntil(fetch("/from-sw").catch(() => {})); self.skipWaiting(); });`)
	st.page("localhost/from-sw", "ok")
	p := consenttest.NewPlan(t, st.srv.URL+"/")
	p.MapHosts = map[string]string{"localhost": st.addr()}
	g := newRig(t, p)
	run := g.openRun(st.srv.URL+"/", consent.RunNone)
	g.load(run.s, st.srv.URL+"/")
	g.load(run.s, "https://localhost:"+realPort+"/")
	g.waitFor(run.s, `window.swDone === true`)
	time.Sleep(testenv.Timeout(time.Second))
	run.Close()
	if n := realConns.Load(); n != 0 {
		t.Errorf("the mapped host's real address was reached %d times", n)
	}
	if st.hits("localhost", "/") != 1 {
		t.Errorf("the mapped page wasn't answered from the mapped address: %d", st.hits("localhost", "/"))
	}
	// Negative: the target's own loopback address, unmapped, is reached.
	if st.hits("127.0.0.1", "/") < 1 {
		t.Error("the unmapped loopback target wasn't loaded")
	}
}
