// Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
// SPDX-License-Identifier: MIT

//go:build browser

package browse

import (
	"crypto/sha256"
	"encoding/hex"
	"net/url"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/RhythmShahriar/echo/consent"
	"github.com/RhythmShahriar/echo/consent/consenttest"
	"github.com/RhythmShahriar/echo/internal/testenv"
)

// beaconPage sends a GA4-shaped hit to the stand-in tracker host in
// every way a tag does: an image, fetch, XHR, sendBeacon and a
// keepalive fetch on pagehide; plus a cross-site iframe, a dedicated
// worker and a service worker that send their own.
const beaconPage = `<!doctype html><title>shop</title>
<script>
const T = "https://tracker.test/g/collect?v=2&tid=G-TEST1234&en=";
new Image().src = T + "img";
fetch(T + "fetch", {method: "POST", body: "en=page_view", mode: "no-cors"});
const x = new XMLHttpRequest(); x.open("POST", T + "xhr"); x.send("en=xhr");
navigator.sendBeacon(T + "beacon", "en=beacon");
addEventListener("pagehide", () => fetch(T + "keepalive", {method: "POST", body: "x", keepalive: true, mode: "no-cors"}));
new Worker("/worker.js");
navigator.serviceWorker.register("/sw.js").then(r => { window.swReady = true; }, e => { window.swErr = String(e); });
</script>
<iframe src="https://frame.other.test/frame.html"></iframe>`

const frameHTML = `<!doctype html><script>navigator.sendBeacon("https://tracker.test/g/collect?v=2&en=frame", "x");</script>`
const workerJS = `fetch("https://tracker.test/g/collect?v=2&en=worker", {method: "POST", body: "w", mode: "no-cors"});`
const swJS = `self.addEventListener("install", () => { fetch("https://tracker.test/g/collect?v=2&en=sw", {method: "POST", body: "s", mode: "no-cors"}); self.skipWaiting(); });`

func heldRig(t *testing.T) (*rig, *site) {
	t.Helper()
	st := newSite(t)
	st.page("127.0.0.1/", beaconPage)
	st.page("127.0.0.1/worker.js", workerJS)
	st.page("127.0.0.1/sw.js", swJS)
	st.page("frame.other.test/frame.html", frameHTML)
	st.page("tracker.test/g/collect", "sink")
	p := consenttest.NewPlan(t, st.srv.URL+"/")
	p.MapHosts = map[string]string{"*.other.test": st.addr(), "tracker.test": st.addr()}
	return newRig(t, p), st
}

// TestConsentHeldBeacons: every way a page, a cross-site frame and its
// workers send a hit is held in the browser. The stand-in tracker sees
// nothing, yet each hit is in the run with Held set.
func TestConsentHeldBeacons(t *testing.T) {
	g, st := heldRig(t)
	s := g.open(st.srv.URL+"/", consent.RunNone)
	g.load(s, st.srv.URL+"/")
	g.waitFor(s, `window.swReady === true`)
	waitRequest(t, s.rec, "en=sw")
	// pagehide fires on the way to another page.
	g.load(s, st.srv.URL+"/worker.js")
	time.Sleep(testenv.Timeout(500 * time.Millisecond))
	rec := s.close()

	if n := st.hits("tracker.test", ""); n != 0 {
		t.Errorf("the tracker sink was reached %d times", n)
	}
	got := map[string]consent.Event{}
	for _, e := range requestEvents(rec) {
		if e.Request.Host != "tracker.test" {
			continue
		}
		en := e.Request.URL[strings.LastIndex(e.Request.URL, "en=")+3:]
		got[en] = e
		if !e.Request.Held || e.Hit == nil || e.Hit.Tracker != "ga4" {
			t.Errorf("%s: held %v, hit %+v", en, e.Request.Held, e.Hit)
		}
	}
	for _, en := range []string{"img", "fetch", "xhr", "beacon", "keepalive", "frame", "worker", "sw"} {
		if _, ok := got[en]; !ok {
			t.Errorf("no hit for %s; have %v", en, keys(got))
		}
	}
	if e, ok := got["img"]; ok && (e.Request.Type != "image" || e.Request.Status != 200) {
		t.Errorf("image hit: %+v", e.Request)
	}
	if e, ok := got["beacon"]; ok && e.Request.Type != "beacon" {
		t.Errorf("sendBeacon type %q", e.Request.Type)
	}
	if e, ok := got["frame"]; ok && e.Request.Frame != "https://frame.other.test" {
		t.Errorf("frame hit from %q", e.Request.Frame)
	}
	for _, en := range []string{"worker", "sw"} {
		if e, ok := got[en]; ok && !e.Request.Worker {
			t.Errorf("%s hit not marked as a worker's: %+v", en, e.Request)
		}
	}
	if e, ok := got["fetch"]; ok && (e.Request.BodyBytes != len("en=page_view") || e.Request.BodySHA256 == "") {
		t.Errorf("fetch body: %+v", e.Request)
	}
	if rec.Held < 8 {
		t.Errorf("held count %d", rec.Held)
	}
}

// TestConsentScriptsNeverHeld: tracker scripts load and run as a
// visitor's browser runs them; only their hits are held.
func TestConsentScriptsNeverHeld(t *testing.T) {
	st := newSite(t)
	st.page("shop.test/", `<!doctype html><title>s</title>
<script src="https://connect.facebook.test/en_US/fbevents.js"></script>
<script src="https://www.googletagmanager.test/gtag/js?id=G-TEST1234"></script>`)
	st.page("connect.facebook.test/en_US/fbevents.js", `window.fbqRan = true; new Image().src = "https://www.facebook.test/tr?id=1234567890&ev=PageView";`)
	st.page("www.googletagmanager.test/gtag/js", `window.gtagRan = true; navigator.sendBeacon("https://www.google-analytics.test/g/collect?v=2&tid=G-TEST1234&en=page_view");`)
	p := consenttest.NewPlan(t, "https://shop.test/")
	p.MapHosts = map[string]string{"shop.test": st.addr(), "*.facebook.test": st.addr(), "*.googletagmanager.test": st.addr(), "*.google-analytics.test": st.addr()}
	g := newRig(t, p)
	s := g.open("https://shop.test/", consent.RunNone)
	g.load(s, "https://shop.test/")
	g.waitFor(s, `window.fbqRan === true && window.gtagRan === true`)
	time.Sleep(testenv.Timeout(300 * time.Millisecond))
	rec := s.close()
	if st.hits("connect.facebook.test", "/en_US/fbevents.js") != 1 || st.hits("www.googletagmanager.test", "/gtag/js") != 1 {
		t.Errorf("scripts didn't load from their (mapped) hosts")
	}
	if st.hits("www.facebook.test", "") != 0 || st.hits("www.google-analytics.test", "") != 0 {
		t.Errorf("a hit reached its tracker")
	}
	held, scripts := 0, 0
	for _, e := range requestEvents(rec) {
		switch {
		case e.Request.Type == "script" && e.Request.Host != "shop.test":
			scripts++
			if e.Request.Held || !e.Request.Mapped || e.Request.Status != 200 {
				t.Errorf("script %s: %+v", e.Request.URL, e.Request)
			}
		case e.Request.Held:
			held++
			if e.Request.Initiator == "" {
				t.Errorf("held hit %s has no initiator", e.Request.URL)
			}
		}
	}
	if scripts != 2 || held != 2 {
		t.Errorf("%d scripts, %d held hits; want 2 and 2", scripts, held)
	}
}

// boundaryPage sends one hit, waits 3 ms, notes the click time and
// sends another in the same task as the click.
const boundaryPage = `<!doctype html><title>b</title>
<button id="reject" style="position:fixed;left:10px;top:10px;width:200px;height:50px">Reject</button>
<script>
const T = "https://tracker.test/g/collect?v=2&en=";
document.getElementById("reject").addEventListener("pointerdown", () => {
  navigator.sendBeacon(T + "before");
  const until = performance.now() + 3; while (performance.now() < until) {}
  window.t0 = performance.timeOrigin + performance.now();
  navigator.sendBeacon(T + "same");
});
</script>`

// boundaryRun clicks the page's button as a run's choice and returns
// the recording with the hits by their en parameter.
func boundaryRun(t *testing.T, page string, kind consent.RunKind, ph consent.Phase, before func(*rig, *session)) (*Recording, map[string][]consent.Event) {
	t.Helper()
	st := newSite(t)
	st.page("shop.test/", page)
	st.page("tracker.test/g/collect", "sink")
	p := consenttest.NewPlan(t, "https://shop.test/")
	p.MapHosts = map[string]string{"shop.test": st.addr(), "tracker.test": st.addr()}
	g := newRig(t, p)
	s := g.open("https://shop.test/", kind)
	g.load(s, "https://shop.test/")
	if before != nil {
		before(g, s)
	}
	s.rec.beginChoice()
	click(t, s, 110, 35)
	var t0 float64
	g.waitFor(s, `typeof window.t0 === "number"`)
	g.eval(s, `window.t0`, &t0)
	s.rec.setChoice(s.rec.atPage(t0), ph)
	time.Sleep(testenv.Timeout(500 * time.Millisecond))
	rec := s.close()
	if st.hits("tracker.test", "") != 0 {
		t.Errorf("hits reached the tracker")
	}
	byEn := map[string][]consent.Event{}
	for _, e := range requestEvents(rec) {
		if e.Request.Host == "tracker.test" {
			en := e.Request.URL[strings.LastIndex(e.Request.URL, "en=")+3:]
			en, _, _ = strings.Cut(en, "&")
			byEn[en] = append(byEn[en], e)
		}
	}
	return rec, byEn
}

// TestClickBoundary: a hit sent in the click's own task is after the
// choice; one sent 3 ms before it is before.
func TestClickBoundary(t *testing.T) {
	_, hits := boundaryRun(t, boundaryPage, consent.RunReject, consent.PhaseAfterReject, nil)
	if e := hits["same"]; len(e) != 1 || e[0].Phase != consent.PhaseAfterReject {
		t.Errorf("same-task hit: %+v", e)
	}
	if e := hits["before"]; len(e) != 1 || e[0].Phase != consent.PhaseBefore {
		t.Errorf("hit before the click: %+v", e)
	}
}

// queuePage is a gtag stand-in that queues hits while consent is denied
// and sends them all when the visitor accepts.
const queuePage = `<!doctype html><title>q</title>
<button id="accept" style="position:fixed;left:10px;top:10px;width:200px;height:50px">Accept</button>
<script>
const T = "https://tracker.test/g/collect?v=2&en=";
let granted = false; const queue = [];
function hit(name) { if (granted) navigator.sendBeacon(T + name); else queue.push(name); }
hit("page_view"); setTimeout(() => hit("scroll"), 50); setTimeout(() => { window.queued = queue.length; }, 200);
document.getElementById("accept").addEventListener("pointerdown", () => {
  window.t0 = performance.timeOrigin + performance.now();
  granted = true; while (queue.length) hit(queue.shift());
});
</script>`

// TestQueuedFlushAfter: hits a tag held back while consent was denied
// and sent on the update are after the Accept.
func TestQueuedFlushAfter(t *testing.T) {
	_, hits := boundaryRun(t, queuePage, consent.RunAccept, consent.PhaseAfterAccept, func(g *rig, s *session) {
		g.waitFor(s, `window.queued === 2`)
	})
	for _, en := range []string{"page_view", "scroll"} {
		if e := hits[en]; len(e) != 1 || e[0].Phase != consent.PhaseAfterAccept || !e[0].Request.Held {
			t.Errorf("%s: %+v", en, e)
		}
	}
}

// TestRequestStorm: 50,000 beacons stop the recording at the request
// limit: the run is marked as limited and truncated, nothing past the
// limit leaves the browser and the memory stays bounded. The limit is
// lowered to 2,000: sendBeacon gives up while Chrome's keepalive quota
// is full, so how many of the 50,000 are sent depends on how fast the
// recorder answers (slow under -race), not on the page.
func TestRequestStorm(t *testing.T) {
	st := newSite(t)
	st.page("shop.test/", `<!doctype html><title>storm</title><script>
const T = "https://tracker.test/g/collect?v=2&en=storm&i=";
let i = 0;
function burst() { for (let n = 0; n < 1000 && i < 50000; n++, i++) navigator.sendBeacon(T + i); if (i < 50000) setTimeout(burst, 0); else window.sent = true; }
burst();
</script>`)
	p := consenttest.NewPlan(t, "https://shop.test/")
	p.MapHosts = map[string]string{"shop.test": st.addr(), "tracker.test": st.addr()}
	const limit = 2000
	p.Limits.Requests = limit
	g := newRig(t, p)
	s := g.open("https://shop.test/", consent.RunNone)
	if err := s.navigate("https://shop.test/"); err != nil {
		t.Fatal(err)
	}
	select {
	case <-s.rec.limit:
	case <-time.After(testenv.Timeout(3 * time.Minute)):
		t.Fatal("the request limit was never reached")
	}
	// Let the page finish its burst so the requests past the limit are
	// the ones checked below.
	deadline := time.Now().Add(testenv.Timeout(3 * time.Minute))
	for time.Now().Before(deadline) {
		raw, err := s.evalMain(t.Context(), `window.sent === true`)
		if err == nil && string(raw) == "true" {
			break
		}
		time.Sleep(200 * time.Millisecond)
	}
	time.Sleep(testenv.Timeout(2 * time.Second))
	rec := s.close()
	var ms runtime.MemStats
	runtime.GC()
	runtime.ReadMemStats(&ms)
	if !rec.Limit || !rec.Truncated || rec.Dropped == 0 {
		t.Errorf("limit %v, truncated %v, dropped %d", rec.Limit, rec.Truncated, rec.Dropped)
	}
	if n := len(requestEvents(rec)); n > limit {
		t.Errorf("%d requests recorded; the limit is %d", n, limit)
	}
	if st.hits("tracker.test", "") != 0 {
		t.Errorf("%d beacons reached the tracker", st.hits("tracker.test", ""))
	}
	if ms.HeapAlloc > 300<<20 {
		t.Errorf("heap %d MB after the storm", ms.HeapAlloc>>20)
	}
}

// spyClassifier is the fake catalog that also keeps the body window it
// was given for each GA4 hit.
type spyClassifier struct {
	consenttest.FakeClassifier
	mu      sync.Mutex
	windows []int
}

func (c *spyClassifier) Request(r *consent.Request, body []byte, site *url.URL) *consent.Hit {
	h := c.FakeClassifier.Request(r, body, site)
	if h != nil {
		c.mu.Lock()
		c.windows = append(c.windows, len(body))
		c.mu.Unlock()
	}
	return h
}

func (c *spyClassifier) Hold(r *consent.Request) bool { return c.FakeClassifier.Hold(r) }

// TestBodyFallback: a 100 KiB GA4 POST is hashed whole and parsed from
// its first 64 KiB, marked partial.
func TestBodyFallback(t *testing.T) {
	st := newSite(t)
	st.page("shop.test/", `<!doctype html><title>body</title><script>
const line = "en=page_view&_et=12\n"; let body = ""; while (body.length < 102400) body += line;
body = body.slice(0, 102400);
fetch("https://tracker.test/g/collect?v=2&tid=G-TEST1234", {method: "POST", body, mode: "no-cors"}).then(() => { window.done = true; });
</script>`)
	p := consenttest.NewPlan(t, "https://shop.test/")
	p.MapHosts = map[string]string{"shop.test": st.addr(), "tracker.test": st.addr()}
	g := newRig(t, p)
	spy := &spyClassifier{}
	consenttest.Use(t, consent.Stages{Classifier: spy})
	s := g.open("https://shop.test/", consent.RunNone)
	g.load(s, "https://shop.test/")
	g.waitFor(s, `window.done === true`)
	rec := s.close()
	line := "en=page_view&_et=12\n"
	full := strings.Repeat(line, 102400/len(line)+1)[:102400]
	sum := sha256.Sum256([]byte(full))
	var got *consent.Request
	for _, e := range requestEvents(rec) {
		if e.Request.Host == "tracker.test" {
			got = e.Request
		}
	}
	if got == nil || !got.BodyPartial || got.BodyBytes != 102400 || got.BodySHA256 != hex.EncodeToString(sum[:]) || !got.Held {
		t.Fatalf("request %+v", got)
	}
	spy.mu.Lock()
	defer spy.mu.Unlock()
	if len(spy.windows) != 1 || spy.windows[0] != consent.DefaultLimits.BodyBytes {
		t.Errorf("parse windows %v; want one of %d", spy.windows, consent.DefaultLimits.BodyBytes)
	}
}

// waitRequest waits until the recorder has written a request whose URL
// contains part.
func waitRequest(t *testing.T, r *recorder, part string) {
	t.Helper()
	deadline := time.Now().Add(testenv.Timeout(20 * time.Second))
	for time.Now().Before(deadline) {
		r.mu.Lock()
		for _, e := range r.out {
			if e.Request != nil && strings.Contains(e.Request.URL, part) {
				r.mu.Unlock()
				return
			}
		}
		r.mu.Unlock()
		time.Sleep(50 * time.Millisecond)
	}
	t.Errorf("no request with %s was recorded", part)
}

func keys[V any](m map[string]V) []string {
	var out []string
	for k := range m {
		out = append(out, k)
	}
	return out
}
