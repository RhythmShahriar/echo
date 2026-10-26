// Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
// SPDX-License-Identifier: MIT

package browse

import (
	"bytes"
	"encoding/json"
	"image/gif"
	"net/url"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/go-rod/rod/lib/proto"

	"github.com/RhythmShahriar/echo/consent"
	"github.com/RhythmShahriar/echo/consent/consenttest"
)

// unitRecorder is a recorder with no browser behind it, for the logic
// that doesn't need one.
func unitRecorder(t *testing.T, kind consent.RunKind, mutate func(*consent.Plan)) *recorder {
	t.Helper()
	p := consenttest.NewPlan(t)
	if mutate != nil {
		mutate(p)
	}
	r := newRecorder(t.Context(), nil, p, p.Targets[0].URL, kind, "ctx", nil, nil)
	t.Cleanup(func() { r.finish() })
	return r
}

// The boundary rule (PRD 13.3): an event at the click's millisecond is
// after it; one a millisecond earlier is before. The withdraw run has a
// second boundary; the gpc run is gpc throughout.
func TestPhaseBoundaries(t *testing.T) {
	r := unitRecorder(t, consent.RunReject, nil)
	r.setChoice(1000, consent.PhaseAfterReject)
	for at, want := range map[int64]consent.Phase{0: consent.PhaseBefore, 999: consent.PhaseBefore, 1000: consent.PhaseAfterReject, 5000: consent.PhaseAfterReject} {
		r.mu.Lock()
		got := r.phaseLocked(at)
		r.mu.Unlock()
		if got != want {
			t.Errorf("at %d: %s; want %s", at, got, want)
		}
	}
	w := unitRecorder(t, consent.RunWithdraw, nil)
	w.setChoice(100, consent.PhaseAfterAccept)
	w.setChoice(900, consent.PhaseAfterWithdraw)
	for at, want := range map[int64]consent.Phase{99: consent.PhaseBefore, 100: consent.PhaseAfterAccept, 899: consent.PhaseAfterAccept, 900: consent.PhaseAfterWithdraw} {
		w.mu.Lock()
		got := w.phaseLocked(at)
		w.mu.Unlock()
		if got != want {
			t.Errorf("withdraw at %d: %s; want %s", at, got, want)
		}
	}
	g := unitRecorder(t, consent.RunGPC, nil)
	g.mu.Lock()
	defer g.mu.Unlock()
	if got := g.phaseLocked(123); got != consent.PhaseGPC {
		t.Errorf("gpc: %s", got)
	}
}

// Events sent while a choice is being made wait for its time, so the
// ones from the click's own task are written as after it, in the stream
// as well as in memory. A choice that never happens lets them go as
// before.
func TestChoiceHoldsEvents(t *testing.T) {
	r := unitRecorder(t, consent.RunAccept, nil)
	var mu sync.Mutex
	var streamed []consent.Event
	r.sinks = append(r.sinks, func(e consent.Event) { mu.Lock(); streamed = append(streamed, e); mu.Unlock() })
	count := func() int { mu.Lock(); defer mu.Unlock(); return len(streamed) }
	r.emit(consent.Event{Kind: consent.EvNote, AtMS: 10, Note: "early"})
	time.Sleep(20 * time.Millisecond)
	r.beginChoice()
	r.emit(consent.Event{Kind: consent.EvNote, AtMS: 499, Note: "just before"})
	r.emit(consent.Event{Kind: consent.EvNote, AtMS: 500, Note: "same ms"})
	r.emit(consent.Event{Kind: consent.EvNote, AtMS: 700, Note: "after"})
	time.Sleep(20 * time.Millisecond)
	if count() != 1 {
		t.Fatalf("streamed %d events during the choice; want only the early one", len(streamed))
	}
	r.setChoice(500, consent.PhaseAfterAccept)
	rec := r.finish()
	want := []consent.Phase{consent.PhaseBefore, consent.PhaseBefore, consent.PhaseAfterAccept, consent.PhaseAfterAccept}
	if len(streamed) != 4 || len(rec.Events) != 4 {
		t.Fatalf("streamed %d, kept %d", len(streamed), len(rec.Events))
	}
	for i, e := range streamed {
		if e.Phase != want[i] || rec.Events[i].Phase != want[i] || e.Seq != int64(i+1) {
			t.Errorf("%s: streamed %s kept %s seq %d; want %s", e.Note, e.Phase, rec.Events[i].Phase, e.Seq, want[i])
		}
	}

	n := unitRecorder(t, consent.RunReject, nil)
	n.beginChoice()
	n.emit(consent.Event{Kind: consent.EvNote, AtMS: 50})
	n.setChoice(-1, consent.PhaseAfterReject)
	rec = n.finish()
	if len(rec.Events) != 1 || rec.Events[0].Phase != consent.PhaseBefore {
		t.Errorf("no choice: %+v", rec.Events)
	}
}

// A full channel drops the event, counts it and marks the run
// truncated; the sender never waits.
func TestFullChannelDrops(t *testing.T) {
	r := unitRecorder(t, consent.RunNone, func(p *consent.Plan) { p.Limits.Channel = 2 })
	r.beginChoice() // hold the writer's buffer at the channel size
	start := time.Now()
	for i := range 50 {
		r.emit(consent.Event{Kind: consent.EvNote, AtMS: int64(i)})
	}
	if d := time.Since(start); d > time.Second {
		t.Errorf("emit waited %s", d)
	}
	r.setChoice(-1, "")
	rec := r.finish()
	if !rec.Truncated || rec.Dropped == 0 || len(rec.Events)+rec.Dropped != 50 {
		t.Errorf("kept %d, dropped %d, truncated %v", len(rec.Events), rec.Dropped, rec.Truncated)
	}
}

// Visits split the events by start time; Live hears hits only.
func TestVisitsAndLive(t *testing.T) {
	var live []consent.Event // written by the writer goroutine, read after finish
	r := unitRecorder(t, consent.RunNone, func(p *consent.Plan) { p.Live = func(e consent.Event) { live = append(live, e) } })
	r.addVisit(0)
	r.addVisit(1000)
	hit := &consent.Hit{Tracker: "ga4"}
	r.emit(consent.Event{Kind: consent.EvRequest, AtMS: 10, Request: &consent.Request{URL: "a"}, Hit: hit})
	r.emit(consent.Event{Kind: consent.EvRequest, AtMS: 1500, Request: &consent.Request{URL: "b"}})
	r.emit(consent.Event{Kind: consent.EvCookie, AtMS: 1600, Cookie: &consent.Cookie{Name: "_ga"}})
	rec := r.finish()
	if len(rec.Events) != 3 || rec.Events[0].Visit != 0 || rec.Events[1].Visit != 1 || rec.Events[2].Cookie.Phase != consent.PhaseBefore || rec.Events[2].Cookie.AtMS != 1600 {
		t.Errorf("events %+v", rec.Events)
	}
	if len(live) != 1 || live[0].Request.URL != "a" {
		t.Errorf("live heard %+v", live)
	}
}

// The run clock: wall seconds from Chrome and page milliseconds from
// performance.timeOrigin + performance.now() land on the same scale,
// floored to whole ms from the first navigation.
func TestOneClock(t *testing.T) {
	r := unitRecorder(t, consent.RunNone, nil)
	r.mu.Lock()
	r.base = 1_800_000_000.250
	a := r.atWallLocked(1_800_000_001.2509)
	r.mu.Unlock()
	b := r.atPage(1_800_000_001_250.9)
	if a != 1000 || b != 1000 {
		t.Errorf("wall %d, page %d; want 1000", a, b)
	}
	if got := r.atPage(1_800_000_000_249.5); got != -1 {
		t.Errorf("before the navigation: %d", got)
	}
}

func TestResourceTypes(t *testing.T) {
	for in, want := range map[proto.NetworkResourceType]string{
		proto.NetworkResourceTypePing: "beacon", proto.NetworkResourceTypeFetch: "fetch", proto.NetworkResourceTypeXHR: "xhr",
		proto.NetworkResourceTypeImage: "image", proto.NetworkResourceTypeScript: "script", proto.NetworkResourceTypeDocument: "document",
		proto.NetworkResourceTypeStylesheet: "stylesheet", proto.NetworkResourceTypeFont: "font", proto.NetworkResourceTypeMedia: "media",
		proto.NetworkResourceTypeWebSocket: "other", proto.NetworkResourceTypeOther: "other", "": "other",
	} {
		if got := resourceType(in); got != want {
			t.Errorf("%q: %q; want %q", in, got, want)
		}
	}
}

func TestInitiatorAndOrigin(t *testing.T) {
	stack := &proto.RuntimeStackTrace{CallFrames: []*proto.RuntimeCallFrame{{URL: ""}},
		Parent: &proto.RuntimeStackTrace{CallFrames: []*proto.RuntimeCallFrame{{URL: "https://www.googletagmanager.com/gtag/js?id=G-1"}}}}
	for _, c := range []struct {
		in   *proto.NetworkInitiator
		want string
	}{
		{nil, ""},
		{&proto.NetworkInitiator{URL: "https://shop.test/"}, "https://shop.test/"},
		{&proto.NetworkInitiator{Type: "script", Stack: stack}, "https://www.googletagmanager.com/gtag/js?id=G-1"},
		{&proto.NetworkInitiator{Type: "other"}, ""},
	} {
		if got := initiatorURL(c.in); got != c.want {
			t.Errorf("initiatorURL = %q; want %q", got, c.want)
		}
	}
	for in, want := range map[string]string{"https://Shop.Test:8443/a?b": "https://shop.test:8443", "about:blank": "", "data:text/html,x": "", "": "", "http://[::1]:80/": "http://[::1]:80"} {
		if got := origin(in); got != want {
			t.Errorf("origin(%q) = %q; want %q", in, got, want)
		}
	}
}

// Stored URLs are cut at Limits.URLBytes without splitting a rune.
func TestURLCut(t *testing.T) {
	r := unitRecorder(t, consent.RunNone, func(p *consent.Plan) { p.Limits.URLBytes = 10 })
	if got := r.cut("https://a.test/"); got != "https://a." {
		t.Errorf("cut = %q", got)
	}
	if got := r.cut("https://ä€"); got != "https://ä" {
		t.Errorf("cut on a rune = %q", got)
	}
	if got := r.cut("short"); got != "short" {
		t.Errorf("cut short = %q", got)
	}
}

// The held image is a valid 1x1 GIF of 43 bytes.
func TestHeldGIF(t *testing.T) {
	if len(heldGIF) != 43 {
		t.Fatalf("%d bytes", len(heldGIF))
	}
	img, err := gif.Decode(bytes.NewReader(heldGIF))
	if err != nil {
		t.Fatal(err)
	}
	if b := img.Bounds(); b.Dx() != 1 || b.Dy() != 1 {
		t.Errorf("bounds %v", b)
	}
	if _, _, _, a := img.At(0, 0).RGBA(); a != 0 {
		t.Errorf("not transparent: alpha %d", a)
	}
}

// -H headers go to same-site requests only, replacing a header of the
// same name whatever its case.
func TestSameSiteHeaders(t *testing.T) {
	token := "Bearer " + strings.Repeat("x9", 12) // assembled at runtime
	r := unitRecorder(t, consent.RunNone, func(p *consent.Plan) { p.Headers = map[string]string{"Authorization": token} })
	var have proto.NetworkHeaders
	if err := json.Unmarshal([]byte(`{"authorization":"old","Accept":"*/*"}`), &have); err != nil {
		t.Fatal(err)
	}
	same, _ := url.Parse("https://shop.test/a.js")
	www, _ := url.Parse("https://www.shop.test/")
	third, _ := url.Parse("https://tracker.test/g/collect")
	other, _ := url.Parse("https://shop.test:8443/")
	for _, u := range []*url.URL{same, www} {
		got := r.sameSiteHeaders(u, have)
		var auth, accept int
		for _, h := range got {
			switch strings.ToLower(h.Name) {
			case "authorization":
				auth++
				if h.Value != token {
					t.Errorf("%s: Authorization %q", u, h.Value)
				}
			case "accept":
				accept++
			}
		}
		if auth != 1 || accept != 1 {
			t.Errorf("%s: headers %+v", u, got)
		}
	}
	for _, u := range []*url.URL{third, other} {
		if got := r.sameSiteHeaders(u, have); got != nil {
			t.Errorf("%s got headers: %+v", u, got)
		}
	}
	plain := unitRecorder(t, consent.RunNone, nil)
	if got := plain.sameSiteHeaders(same, have); got != nil {
		t.Errorf("no -H: %+v", got)
	}
}

// urlSpy is a catalog that keeps the URLs it was asked about and knows
// one hit only its body proves.
type urlSpy struct {
	consenttest.FakeClassifier
	mu   sync.Mutex
	urls []string
}

func (c *urlSpy) Request(r *consent.Request, body []byte, site *url.URL) *consent.Hit {
	c.mu.Lock()
	c.urls = append(c.urls, r.URL)
	c.mu.Unlock()
	if strings.HasSuffix(r.URL, "/api/event") && strings.Contains(string(body), `"n":"pageview"`) {
		return &consent.Hit{Tracker: "plausible", Kind: consent.HitBeacon, Need: consent.NeedNeeded}
	}
	if strings.HasSuffix(r.URL, "/js/script.js") {
		return &consent.Hit{Tracker: "plausible", Kind: consent.HitScript, Need: consent.NeedNeeded}
	}
	return c.FakeClassifier.Request(r, body, site)
}

func (c *urlSpy) Hold(r *consent.Request) bool { return c.FakeClassifier.Hold(r) }

// The catalog sees the raw URL while the record keeps the masked one; a
// hit proven by its body is held, a script never, nothing with
// --send-hits.
func TestClassifyRawURLAndBodyHold(t *testing.T) {
	spy := &urlSpy{}
	consenttest.Use(t, consent.Stages{Classifier: spy})
	r := unitRecorder(t, consent.RunNone, nil)
	raw := "https://www.google-analytics.com/g/collect?v=2&tid=G-ABCD1234&cid=555.666"
	req := &consent.Request{Method: "GET", URL: consent.MaskURL(raw, false), Host: "www.google-analytics.com"}
	hit, hold := r.classify(req, raw, nil)
	if hit == nil || !hold || len(spy.urls) != 1 || spy.urls[0] != raw || strings.Contains(req.URL, "ABCD1234") {
		t.Errorf("hit %+v hold %v; the catalog saw %q; stored %q", hit, hold, spy.urls, req.URL)
	}
	ev := &consent.Request{Method: "POST", URL: "https://shop.test/api/event", Host: "shop.test"}
	if hit, hold := r.classify(ev, ev.URL, []byte(`{"n":"pageview"}`)); hit == nil || !hold {
		t.Errorf("body-proven hit: %+v %v", hit, hold)
	}
	sc := &consent.Request{Method: "GET", URL: "https://plausible.io/js/script.js", Host: "plausible.io"}
	if hit, hold := r.classify(sc, sc.URL, nil); hit == nil || hold {
		t.Errorf("script: %+v held %v", hit, hold)
	}
	pre := &consent.Request{Method: "OPTIONS", URL: raw, Host: "www.google-analytics.com"}
	if hit, hold := r.classify(pre, raw, nil); hit != nil || !hold {
		t.Errorf("preflight: %+v held %v", hit, hold)
	}
	send := unitRecorder(t, consent.RunNone, func(p *consent.Plan) { p.Hold = false })
	if _, hold := send.classify(req, raw, nil); hold {
		t.Error("held with --send-hits")
	}
}
