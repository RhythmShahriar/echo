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

	"github.com/RhythmShahriar/echo/consent"
	"github.com/RhythmShahriar/echo/consent/browse/egress"
	"github.com/RhythmShahriar/echo/consent/consenttest"
	"github.com/RhythmShahriar/echo/internal/testenv"
)

// internalServer is a 127.0.0.1 server a public page must never reach;
// it counts every request.
func internalServer(t *testing.T) (*httptest.Server, *atomic.Int64) {
	t.Helper()
	n := new(atomic.Int64)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		n.Add(1)
		_, _ = io.WriteString(w, "internal")
	}))
	t.Cleanup(srv.Close)
	return srv, n
}

// TestConsentEgressGuard: a public-looking page (answered through
// --map-host) asks for a loopback server, the metadata address and a
// private address by image, fetch and iframe. Every one is refused at
// the guard, the loopback server is never reached and the run records
// the refusals as private addresses.
func TestConsentEgressGuard(t *testing.T) {
	internal, hits := internalServer(t)
	in := strings.TrimPrefix(internal.URL, "http://")
	st := newSite(t)
	st.page("shop.test/", `<!doctype html><title>shop</title>
<img src="http://`+in+`/img">
<img src="http://169.254.169.254/latest/meta-data/">
<img src="http://10.0.0.5/img">
<script>
fetch("http://`+in+`/fetch", {mode: "no-cors"}).catch(() => {});
fetch("http://169.254.169.254/fetch", {mode: "no-cors"}).catch(() => {});
fetch("https://10.0.0.5/fetch", {mode: "no-cors"}).catch(() => {});
</script>
<iframe src="http://`+in+`/frame"></iframe>
<iframe src="http://169.254.169.254/frame"></iframe>`)
	p := consenttest.NewPlan(t, "http://shop.test/")
	p.MapHosts = map[string]string{"shop.test": st.addr()}
	g := newRig(t, p)
	s := g.open("http://shop.test/", consent.RunNone)
	g.load(s, "http://shop.test/")
	time.Sleep(testenv.Timeout(time.Second))
	rec := s.close()
	if hits.Load() != 0 {
		t.Errorf("the loopback server was reached %d times", hits.Load())
	}
	refused := map[string]int{}
	for _, e := range requestEvents(rec) {
		if e.Request.Host == "shop.test" {
			continue
		}
		if e.Request.Refused != egress.ReasonPrivate {
			t.Errorf("%s %s: refused %q", e.Request.Type, e.Request.URL, e.Request.Refused)
		}
		refused[e.Request.Host]++
	}
	for _, h := range []string{"127.0.0.1", "169.254.169.254", "10.0.0.5"} {
		if refused[h] < 2 {
			t.Errorf("%s: %d refused requests recorded; want the image, fetch and frame ones (%v)", h, refused[h], refused)
		}
	}
	if st := g.guards[0].Stats(); st.Refused < 6 {
		t.Errorf("guard stats %+v", st)
	}
}

// TestLoopbackTargetAllowed: a 127.0.0.1 target loads its own loopback
// assets through its guard, never the metadata address.
func TestLoopbackTargetAllowedInBrowser(t *testing.T) {
	st := newSite(t)
	st.page("127.0.0.1/", `<!doctype html><title>local</title><script src="/app.js"></script>
<img src="http://169.254.169.254/latest/meta-data/">`)
	st.page("127.0.0.1/app.js", `window.appRan = true;`)
	g := newRig(t, consenttest.NewPlan(t, st.srv.URL+"/"))
	s := g.open(st.srv.URL+"/", consent.RunNone)
	g.load(s, st.srv.URL+"/")
	g.waitFor(s, `window.appRan === true`)
	rec := s.close()
	if st.hits("127.0.0.1", "/app.js") != 1 {
		t.Errorf("app.js reached the server %d times", st.hits("127.0.0.1", "/app.js"))
	}
	var meta *consent.Request
	for _, e := range requestEvents(rec) {
		if e.Request.Host == "169.254.169.254" {
			meta = e.Request
		}
	}
	if meta == nil || meta.Refused != egress.ReasonPrivate {
		t.Errorf("metadata request: %+v", meta)
	}
}

// TestConsentBrowserTrafficExcluded: traffic outside every run's context
// (Chrome's own) reaches only the refuse-all listener, is counted as
// browser traffic and never shows up in a run; the frameless requests a
// run saw in its own context are added to Network.BrowserInternal.
func TestConsentBrowserTrafficExcluded(t *testing.T) {
	internal, hits := internalServer(t)
	st := newSite(t)
	st.page("127.0.0.1/", `<!doctype html><title>run</title>`)
	g := newRig(t, consenttest.NewPlan(t, st.srv.URL+"/"))
	run := g.openRun(st.srv.URL+"/", consent.RunNone)
	s := run.s
	g.load(s, st.srv.URL+"/")

	before := g.internal.Stats().BrowserInternal
	// A page in the default context stands in for Chrome's own requests:
	// no run owns it, so only the browser-wide proxy setting applies.
	page, err := g.b.Page(proto.TargetCreateTarget{URL: "about:blank"})
	if err != nil {
		t.Fatal(err)
	}
	_ = page.Timeout(testenv.Timeout(20 * time.Second)).Navigate(internal.URL + "/chrome-service")
	_ = page.Timeout(testenv.Timeout(20 * time.Second)).Navigate("http://update.googleapis.com/service/update2")
	// A frameless request inside the run's context can't be made on
	// demand, so the run's counter stands in for the ones it saw: the
	// engine must add them to the listeners' count.
	run.s.rec.counts.internal.Add(3)
	rec := run.Close()
	if rec.Internal < 3 {
		t.Errorf("run counted %d frameless requests; want at least 3", rec.Internal)
	}
	if hits.Load() != 0 {
		t.Errorf("traffic outside the runs reached a server %d times", hits.Load())
	}
	if got := g.internal.Stats().BrowserInternal - before; got < 2 {
		t.Errorf("refuse-all counted %d requests; want 2", got)
	}
	for _, e := range requestEvents(rec) {
		if strings.Contains(e.Request.URL, "chrome-service") || e.Request.Host == "update.googleapis.com" {
			t.Errorf("browser traffic in the run: %+v", e.Request)
		}
	}
	for _, gd := range g.guards {
		if gd.Stats().BrowserInternal != 0 {
			t.Errorf("a run's guard saw browser traffic: %+v", gd.Stats())
		}
	}
	if got, want := g.e.Network().BrowserInternal, g.internal.Stats().BrowserInternal+rec.Internal; got != want {
		t.Errorf("network browser_internal = %d; want %d (listener plus the run's frameless requests)", got, want)
	}
}
