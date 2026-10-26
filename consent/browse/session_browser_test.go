// Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
// SPDX-License-Identifier: MIT

//go:build browser

package browse

import (
	"io/fs"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/go-rod/rod/lib/proto"

	"github.com/RhythmShahriar/echo/consent"
	"github.com/RhythmShahriar/echo/consent/consenttest"
	"github.com/RhythmShahriar/echo/internal/testenv"
)

// TestConsentUserAgentSuffix: the run's user agent is the browser's own
// with HeadlessChrome made Chrome and the product token at the end, in
// the page, in the request header and in the client hints.
func TestConsentUserAgentSuffix(t *testing.T) {
	st := newSite(t)
	st.page("127.0.0.1/", `<!doctype html><title>ua</title>`)
	p := consenttest.NewPlan(t, st.srv.URL+"/")
	p.Version = "1.2.0"
	g := newRig(t, p)
	s := g.open(st.srv.URL+"/", consent.RunNone)
	g.load(s, st.srv.URL+"/")
	var ua string
	g.eval(s, `navigator.userAgent`, &ua)
	var brands string
	g.eval(s, `navigator.userAgentData ? navigator.userAgentData.brands.map(b => b.brand).join(",") : ""`, &brands)
	s.close()
	if strings.Contains(ua, "HeadlessChrome") || !strings.HasSuffix(ua, " rhythm-echo-consent/1.2.0") || !strings.Contains(ua, " Chrome/") {
		t.Errorf("navigator.userAgent = %q", ua)
	}
	if strings.Contains(brands, "Headless") || !strings.Contains(brands, "Chromium") {
		t.Errorf("client hint brands = %q", brands)
	}
	reqs := st.requests("127.0.0.1")
	if len(reqs) == 0 || reqs[0].UserAgent() != ua {
		t.Errorf("request User-Agent differs from navigator.userAgent")
	}
	for _, r := range reqs {
		if strings.Contains(r.Header.Get("Sec-CH-UA"), "Headless") {
			t.Errorf("Sec-CH-UA = %q", r.Header.Get("Sec-CH-UA"))
		}
	}

	// -A replaces the whole user agent.
	p2 := consenttest.NewPlan(t, st.srv.URL+"/")
	p2.UserAgent = "Mozilla/5.0 custom"
	if got := userAgent(p2, ua, false); got != "Mozilla/5.0 custom" {
		t.Errorf("-A: %q", got)
	}
}

// TestGeoDE: --geo de alone gives the page German and Berlin time.
func TestGeoDE(t *testing.T) {
	st := newSite(t)
	st.page("127.0.0.1/", `<!doctype html><title>geo</title>`)
	p := consenttest.NewPlan(t, st.srv.URL+"/")
	p.Locale, p.Geo = "", "de"
	g := newRig(t, p)
	s := g.open(st.srv.URL+"/", consent.RunNone)
	g.load(s, st.srv.URL+"/")
	var lang, tz string
	g.eval(s, `navigator.language`, &lang)
	g.eval(s, `Intl.DateTimeFormat().resolvedOptions().timeZone`, &tz)
	s.close()
	if lang != "de-DE" || tz != "Europe/Berlin" {
		t.Errorf("language %q, time zone %q", lang, tz)
	}
	if reqs := st.requests("127.0.0.1"); len(reqs) == 0 || reqs[0].Header.Get("Accept-Language") != "de-DE,de;q=0.9" {
		t.Errorf("Accept-Language not German")
	}
}

// The mobile viewport is a small touch screen at 3x with a mobile user
// agent.
func TestMobileViewport(t *testing.T) {
	st := newSite(t)
	st.page("127.0.0.1/", `<!doctype html><meta name="viewport" content="width=device-width"><title>m</title>`)
	p := consenttest.NewPlan(t, st.srv.URL+"/")
	p.Viewport = ViewportMobile
	g := newRig(t, p)
	s := g.open(st.srv.URL+"/", consent.RunNone)
	g.load(s, st.srv.URL+"/")
	var got struct {
		W, DPR, Touch float64
		UA            string
	}
	g.eval(s, `({W: innerWidth, DPR: devicePixelRatio, Touch: navigator.maxTouchPoints, UA: navigator.userAgent})`, &got)
	s.close()
	if got.W != 390 || got.DPR != 3 || got.Touch < 1 || !strings.Contains(got.UA, "Android") || !strings.Contains(got.UA, "Mobile Safari/") {
		t.Errorf("mobile: %+v", got)
	}
}

// hostileBanner's Accept starts a download, an alert, a popup, a
// geolocation prompt and a navigation to another site.
const hostileBanner = `<!doctype html><title>hostile</title>
<button id="accept" style="position:fixed;left:10px;top:10px;width:200px;height:50px">Accept</button>
<script>
document.getElementById("accept").addEventListener("click", () => {
  alert("Thanks \u202efor accepting!");
  window.open("https://popup.test/win", "_blank");
  navigator.geolocation.getCurrentPosition(() => fetch("/geo?ok=1"), e => fetch("/geo?code=" + e.code));
  const a = document.createElement("a"); a.href = "/file.bin"; a.download = "file.bin"; document.body.appendChild(a); a.click();
  setTimeout(() => { location = "https://other.test/"; }, 1000);
});
</script>`

// click sends a real left click at x, y to the tab.
func click(t *testing.T, s *session, x, y float64) {
	t.Helper()
	for _, typ := range []proto.InputDispatchMouseEventType{proto.InputDispatchMouseEventTypeMouseMoved, proto.InputDispatchMouseEventTypeMousePressed, proto.InputDispatchMouseEventTypeMouseReleased} {
		ev := proto.InputDispatchMouseEvent{Type: typ, X: x, Y: y}
		if typ != proto.InputDispatchMouseEventTypeMouseMoved {
			ev.Button, ev.ClickCount = proto.InputMouseButtonLeft, 1
		}
		if err := ev.Call(s.main); err != nil {
			t.Fatal(err)
		}
	}
}

// TestHostileBanner: every trick behind the Accept button is recorded
// as an event and none of them lands: no file, no popup left open, no
// permission; the run still finishes.
func TestHostileBanner(t *testing.T) {
	st := newSite(t)
	st.page("shop.test/", hostileBanner)
	st.page("shop.test/geo", "ok")
	st.page("shop.test/file.bin", "binary", "Content-Disposition", `attachment; filename="file.bin"`, "Content-Type", "application/octet-stream")
	p := consenttest.NewPlan(t, "https://shop.test/")
	p.MapHosts = map[string]string{"shop.test": st.addr()}
	p.MapOnly = true // popup.test and other.test are refused, never looked up
	g := newRig(t, p)
	s := g.open("https://shop.test/", consent.RunAccept)
	g.load(s, "https://shop.test/")
	click(t, s, 110, 35)
	deadline := time.Now().Add(testenv.Timeout(10 * time.Second))
	for st.hits("shop.test", "/geo") == 0 && time.Now().Before(deadline) {
		time.Sleep(50 * time.Millisecond)
	}
	time.Sleep(testenv.Timeout(1500 * time.Millisecond)) // the navigation away
	rec := s.close()

	dialogs, popups := kinds(rec, consent.EvDialog), kinds(rec, consent.EvPopup)
	if len(dialogs) != 1 || dialogs[0].Note != "Thanks \\u202efor accepting!" {
		t.Errorf("dialogs %+v", dialogs)
	}
	if len(popups) != 1 {
		t.Errorf("popups %+v", popups)
	}
	if d := kinds(rec, consent.EvDownload); len(d) != 1 || d[0].Note != "https://shop.test/file.bin" {
		t.Errorf("downloads %+v", d)
	}
	var geo string
	for _, r := range st.requests("shop.test") {
		if r.URL.Path == "/geo" {
			geo = r.URL.RawQuery
		}
	}
	if geo != "code=1" {
		t.Errorf("geolocation answered %q; want denied (code=1)", geo)
	}
	_ = filepath.WalkDir(g.b.ProfileDir(), func(path string, d fs.DirEntry, err error) error {
		if err == nil && !d.IsDir() && strings.Contains(d.Name(), "file.bin") {
			t.Errorf("downloaded %s", path)
		}
		return nil
	})
	if pages, _ := g.b.Pages(); len(pages) > 2 {
		t.Errorf("%d pages left open", len(pages))
	}
	var away bool
	for _, e := range requestEvents(rec) {
		if e.Request.Host == "other.test" && e.Request.Type == "document" && e.Request.Refused == "not mapped" {
			away = true
		}
	}
	if !away {
		t.Error("the navigation away isn't in the run")
	}
}

// TestHeadersSameSiteOnly: -H headers and --cookie reach the target's
// own requests and never a third party's.
func TestHeadersSameSiteOnly(t *testing.T) {
	token := "Bearer " + strings.Repeat("k7", 16) // assembled at runtime
	st := newSite(t)
	st.page("127.0.0.1/", `<!doctype html><title>h</title><script src="/asset.js"></script><img src="https://third.test/pixel.gif">`)
	st.page("127.0.0.1/asset.js", `window.assetRan = true;`)
	st.page("third.test/pixel.gif", "GIF89a")
	p := consenttest.NewPlan(t, st.srv.URL+"/")
	p.Headers = map[string]string{"Authorization": token}
	p.Cookies = []string{"session=" + strings.Repeat("c", 12)}
	p.MapHosts = map[string]string{"third.test": st.addr()}
	g := newRig(t, p)
	s := g.open(st.srv.URL+"/", consent.RunNone)
	g.load(s, st.srv.URL+"/")
	g.waitFor(s, `window.assetRan === true`)
	s.close()
	own := 0
	for _, r := range st.requests("127.0.0.1") {
		if r.URL.Path == "/favicon.ico" {
			continue
		}
		own++
		if r.Header.Get("Authorization") != token || !strings.Contains(r.Header.Get("Cookie"), "session=") {
			t.Errorf("%s: Authorization %v, Cookie %q", r.URL.Path, r.Header.Get("Authorization") != "", r.Header.Get("Cookie"))
		}
	}
	if own < 2 {
		t.Errorf("%d same-site requests", own)
	}
	third := st.requests("third.test")
	if len(third) == 0 {
		t.Fatal("the third-party pixel never loaded")
	}
	for _, r := range third {
		if r.Header.Get("Authorization") != "" || r.Header.Get("Cookie") != "" {
			t.Errorf("third party got Authorization %v, Cookie %q", r.Header.Get("Authorization") != "", r.Header.Get("Cookie"))
		}
	}
}

// TestGPC: the gpc run sends Sec-GPC: 1 and defines
// navigator.globalPrivacyControl before page scripts; other runs do
// neither.
func TestGPC(t *testing.T) {
	st := newSite(t)
	st.page("127.0.0.1/", `<!doctype html><title>gpc</title><script>window.early = navigator.globalPrivacyControl === true; fetch("/check");</script>`)
	st.page("127.0.0.1/check", "ok")
	g := newRig(t, consenttest.NewPlan(t, st.srv.URL+"/"))
	for _, c := range []struct {
		kind consent.RunKind
		on   bool
	}{{consent.RunGPC, true}, {consent.RunNone, false}} {
		before := len(st.requests("127.0.0.1"))
		s := g.open(st.srv.URL+"/", c.kind)
		g.load(s, st.srv.URL+"/")
		var early bool
		g.eval(s, `window.early`, &early)
		rec := s.close()
		if early != c.on {
			t.Errorf("%s: navigator.globalPrivacyControl before page scripts = %v", c.kind, early)
		}
		reqs := st.requests("127.0.0.1")[before:]
		if len(reqs) < 2 {
			t.Fatalf("%s: %d requests", c.kind, len(reqs))
		}
		for _, r := range reqs {
			if got := r.Header.Get("Sec-GPC") == "1"; got != c.on {
				t.Errorf("%s: %s Sec-GPC %q", c.kind, r.URL.Path, r.Header.Get("Sec-GPC"))
			}
		}
		for _, e := range rec.Events {
			want := consent.PhaseBefore
			if c.on {
				want = consent.PhaseGPC
			}
			if e.Phase != want {
				t.Errorf("%s: event phase %s", c.kind, e.Phase)
			}
		}
	}
}
