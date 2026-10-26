// Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
// SPDX-License-Identifier: MIT

//go:build browser

package browse

import (
	"runtime"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/RhythmShahriar/echo/consent"
	"github.com/RhythmShahriar/echo/consent/consenttest"
	"github.com/RhythmShahriar/echo/internal/testenv"
)

// hookRun loads page on shop.test (mapped), runs then in the tab and
// returns the recording.
func hookRun(t *testing.T, page string, mutate func(*consent.Plan), then func(*rig, *session)) *Recording {
	t.Helper()
	st := newSite(t)
	st.page("shop.test/", page)
	st.page("tracker.test/g/collect", "sink")
	p := consenttest.NewPlan(t, "https://shop.test/")
	p.MapHosts = map[string]string{"shop.test": st.addr(), "tracker.test": st.addr()}
	if mutate != nil {
		mutate(p)
	}
	g := newRig(t, p)
	consenttest.Use(t, consent.Stages{Signals: hookSignals{}})
	s := g.open("https://shop.test/", consent.RunNone)
	g.load(s, "https://shop.test/")
	if then != nil {
		then(g, s)
	}
	time.Sleep(testenv.Timeout(400 * time.Millisecond))
	return s.close()
}

// signals are the recording's consent signals as source:command.
func signals(rec *Recording) []string {
	var out []string
	for _, e := range kinds(rec, consent.EvSignal) {
		out = append(out, e.Signal.Source+":"+e.Signal.Command)
	}
	return out
}

// gtagPage is the Consent Mode snippet: a default before the tag, an
// update when the visitor accepts.
const gtagPage = `<!doctype html><title>g</title><script>
window.dataLayer = window.dataLayer || [];
function gtag(){dataLayer.push(arguments);}
gtag("consent", "default", {ad_storage: "denied", analytics_storage: "denied"});
gtag("js", new Date());
gtag("config", "G-TEST1234");
window.accept = () => gtag("consent", "update", {ad_storage: "granted", analytics_storage: "granted"});
</script>`

// TestHooksSeeDefaultAndUpdate: the Consent Mode default and update
// are recorded as signals in order, with their state and times.
func TestHooksSeeDefaultAndUpdate(t *testing.T) {
	rec := hookRun(t, gtagPage, nil, func(g *rig, s *session) {
		time.Sleep(testenv.Timeout(300 * time.Millisecond)) // the poll wraps gtag
		g.eval(s, `window.accept(), true`, nil)
	})
	sig := kinds(rec, consent.EvSignal)
	if len(sig) != 2 {
		t.Fatalf("signals %v", signals(rec))
	}
	def, upd := sig[0].Signal, sig[1].Signal
	if def.Command != "default" || def.State["ad_storage"] != consent.Denied || def.Source != "datalayer" {
		t.Errorf("default %+v", def)
	}
	if upd.Command != "update" || upd.State["analytics_storage"] != consent.Granted || upd.Source != "gtag" {
		t.Errorf("update %+v", upd)
	}
	if sig[0].AtMS > sig[1].AtMS || sig[1].AtMS <= 0 || def.AtMS != sig[0].AtMS {
		t.Errorf("times %d %d", sig[0].AtMS, sig[1].AtMS)
	}
}

// TestHooksSurviveRedefinedDataLayer: a page-level const dataLayer is
// found by name and watched from then on; a GTM-style push replacement
// is reported once per call and still runs.
func TestHooksSurviveRedefinedDataLayer(t *testing.T) {
	t.Run("const", func(t *testing.T) {
		rec := hookRun(t, `<!doctype html><title>c</title><script>
const dataLayer = [];
dataLayer.push(["consent", "default", {ad_storage: "denied"}]);
window.later = () => dataLayer.push(["consent", "update", {ad_storage: "granted"}]);
</script>`, nil, func(g *rig, s *session) {
			time.Sleep(testenv.Timeout(300 * time.Millisecond))
			g.eval(s, `window.later()`, nil)
		})
		if got := strings.Join(signals(rec), " "); got != "datalayer:default datalayer:update" {
			t.Errorf("signals %s", got)
		}
	})
	t.Run("gtm", func(t *testing.T) {
		var state struct{ Saw, Len int }
		rec := hookRun(t, `<!doctype html><title>g</title><script>
window.dataLayer = window.dataLayer || [];
dataLayer.push(["consent", "default", {ad_storage: "denied"}]);
(function () {
  const b = dataLayer.push; let n = 0;
  dataLayer.push = function () { n++; window.gtmSaw = n; return b.apply(dataLayer, arguments); };
})();
dataLayer.push(["consent", "update", {ad_storage: "granted"}]);
</script>`, nil, func(g *rig, s *session) {
			g.eval(s, `({saw: window.gtmSaw, len: window.dataLayer.length})`, &state)
		})
		if got := strings.Join(signals(rec), " "); got != "datalayer:default datalayer:update" {
			t.Errorf("signals %s", got)
		}
		if state.Saw != 1 || state.Len != 2 {
			t.Errorf("GTM's push ran %d times, dataLayer has %d entries", state.Saw, state.Len)
		}
	})
}

// TestHooksDoNotChangeBehaviour: the page's own push wrapper, the Meta
// Pixel stub's properties and gtag's return value are what the page
// built; nothing the page does sees the hooks.
func TestHooksDoNotChangeBehaviour(t *testing.T) {
	var got struct {
		N1       int    `json:"n1"`
		R2       string `json:"r2"`
		PageSaw  int    `json:"pageSaw"`
		Len      int    `json:"len"`
		Queue    int    `json:"queue"`
		Loaded   bool   `json:"loaded"`
		Self     bool   `json:"self"`
		GtagRet  int    `json:"gtagRet"`
		GtagThis bool   `json:"gtagThis"`
	}
	hookRun(t, `<!doctype html><title>b</title><script>
window.dataLayer = window.dataLayer || [];
const n1 = dataLayer.push({a: 1});
const orig = dataLayer.push; let pageSaw = 0;
dataLayer.push = function () { pageSaw++; return "page:" + orig.apply(this, arguments); };
const r2 = dataLayer.push({b: 2});
!function (f, n) { if (f.fbq) return; n = f.fbq = function () { n.callMethod ? n.callMethod.apply(n, arguments) : n.queue.push(arguments); };
  if (!f._fbq) f._fbq = n; n.push = n; n.loaded = !0; n.version = "2.0"; n.queue = []; }(window);
fbq("consent", "revoke"); fbq("init", "1234567890");
const holder = {};
window.gtag = function () { dataLayer.push(arguments); return this === holder ? 42 : 0; };
holder.g = window.gtag;
const gtagRet = holder.g("event", "x");
window.result = {n1, r2, pageSaw, len: dataLayer.length, queue: fbq.queue.length, loaded: fbq.loaded === true,
  self: fbq.push === fbq._fbq || fbq.push.queue === fbq.queue, gtagRet, gtagThis: gtagRet === 42};
</script>`, nil, func(g *rig, s *session) {
		g.eval(s, `window.result`, &got)
	})
	if got.N1 != 1 || got.R2 != "page:2" || got.PageSaw != 2 || got.Len != 3 || got.Queue != 2 || !got.Loaded || !got.Self || !got.GtagThis {
		t.Errorf("the page saw %+v", got)
	}
}

// TestHugeDataLayer: 100 MB pushed, through a watched push and through
// a push that can only be polled. The run finishes, the hooks stop at
// their limits and say so; this process stays small.
func TestHugeDataLayer(t *testing.T) {
	for name, setup := range map[string]string{
		"watched": `window.dataLayer = window.dataLayer || []; const dl = window.dataLayer;`,
		"polled": `const dl = []; Object.defineProperty(dl, "push", {value: Array.prototype.push, writable: false, configurable: false});
Object.defineProperty(window, "dataLayer", {value: dl, configurable: false});`,
	} {
		t.Run(name, func(t *testing.T) {
			rec := hookRun(t, `<!doctype html><title>big</title><script>`+setup+`
const blob = "x".repeat(1024);
for (let i = 0; i < 100000; i++) dl.push({event: "e" + i, blob: blob});
window.pushed = dl.length;
</script>`, nil, func(g *rig, s *session) {
				g.waitFor(s, `window.pushed === 100000`)
				// The poll, the records and the dropped report reach Go in
				// their own time.
				deadline := time.Now().Add(testenv.Timeout(30 * time.Second))
				for s.rec.dropped.Load() < 90000 && time.Now().Before(deadline) {
					time.Sleep(50 * time.Millisecond)
				}
			})
			var ms runtime.MemStats
			runtime.ReadMemStats(&ms)
			if ms.Sys > 500<<20 {
				t.Errorf("the test process holds %d MB", ms.Sys>>20)
			}
			if !rec.Truncated || rec.Dropped < 90000 {
				t.Errorf("truncated %v, dropped %d", rec.Truncated, rec.Dropped)
			}
		})
	}
}

// TestHookFlood: the page calls the binding itself 10,000 times; 2,000
// records are kept and the rest counted.
func TestHookFlood(t *testing.T) {
	rec := hookRun(t, `<!doctype html><title>f</title><script>
const rec = JSON.stringify({t: performance.timeOrigin + performance.now(), src: "gtag", args: ["consent", "default", {ad_storage: "denied"}]});
for (let i = 0; i < 10000; i++) window.__rhythmEchoConsent(rec);
window.flooded = true;
</script>`, nil, func(g *rig, s *session) {
		g.waitFor(s, `window.flooded === true`)
		deadline := time.Now().Add(testenv.Timeout(20 * time.Second))
		for time.Now().Before(deadline) {
			s.rec.mu.Lock()
			n := s.rec.hookN
			s.rec.mu.Unlock()
			if n >= 10000 {
				break
			}
			time.Sleep(50 * time.Millisecond)
		}
	})
	if n := len(kinds(rec, consent.EvSignal)); n != consent.DefaultLimits.HookRecords || rec.Dropped != 8000 || !rec.Truncated {
		t.Errorf("%d signals, dropped %d, truncated %v", n, rec.Dropped, rec.Truncated)
	}
}

// TestPatchedDOM: a page that overrides querySelectorAll and wraps
// dataLayer.push to drop events gives the same network evidence as one
// that doesn't; its consent calls are still seen.
func TestPatchedDOM(t *testing.T) {
	body := `
dataLayer.push(["consent", "default", {ad_storage: "denied"}]);
new Image().src = "https://tracker.test/g/collect?v=2&en=page_view";
navigator.sendBeacon("https://tracker.test/g/collect?v=2&en=scroll");
window.sent = true;
</script>`
	plain := `<!doctype html><title>p</title><script>window.dataLayer = [];` + body
	patched := `<!doctype html><title>p</title><script>
Element.prototype.querySelectorAll = Document.prototype.querySelectorAll = function () { return []; };
window.dataLayer = []; dataLayer.push = function () { return 0; };` + body
	hits := func(rec *Recording) []string {
		var out []string
		for _, e := range requestEvents(rec) {
			if e.Hit != nil {
				out = append(out, e.Request.URL+" "+e.Hit.Tracker+" "+map[bool]string{true: "held"}[e.Request.Held])
			}
		}
		slices.Sort(out) // an image and a beacon race each other
		return out
	}
	wait := func(g *rig, s *session) {
		g.waitFor(s, `window.sent === true`)
		waitRequest(t, s.rec, "en=scroll")
	}
	a, b := hookRun(t, plain, nil, wait), hookRun(t, patched, nil, wait)
	ha, hb := hits(a), hits(b)
	if len(ha) != 2 || strings.Join(ha, "|") != strings.Join(hb, "|") {
		t.Errorf("hits\n%v\npatched\n%v", ha, hb)
	}
	if sa, sb := strings.Join(signals(a), " "), strings.Join(signals(b), " "); sa != "datalayer:default" || sb != sa {
		t.Errorf("signals %q, patched %q", sa, sb)
	}
}
