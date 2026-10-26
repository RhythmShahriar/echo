// Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
// SPDX-License-Identifier: MIT

//go:build browser

package browse

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/RhythmShahriar/echo/consent"
	"github.com/RhythmShahriar/echo/consent/consenttest"
	"github.com/RhythmShahriar/echo/internal/testenv"
)

// tabRig opens a run of kind on https://shop.test/ answered by st and
// returns its Tab.
func tabRig(t *testing.T, st *site, kind consent.RunKind, mutate func(*consent.Plan)) (*rig, *Run, *Tab) {
	t.Helper()
	p := consenttest.NewPlan(t, "https://shop.test/")
	p.MapHosts = map[string]string{"shop.test": st.addr(), "*.other.test": st.addr()}
	if mutate != nil {
		mutate(p)
	}
	g := newRig(t, p)
	run := g.openRun("https://shop.test/", kind)
	g.load(run.s, "https://shop.test/")
	return g, run, run.Tab(1)
}

const buttonCSS = `position:fixed;left:40px;top:40px;width:160px;height:40px`

// TestClickShadowDOM: a button inside an open shadow root is clicked
// with real mouse events; the click time is the page's pointerdown. A
// closed root is reported, not clicked.
func TestClickShadowDOM(t *testing.T) {
	st := newSite(t)
	st.page("shop.test/", `<!doctype html><title>s</title><div id="host"></div><div id="closed"></div><script>
const root = document.getElementById("host").attachShadow({mode: "open"});
root.innerHTML = '<button id="accept" style="`+buttonCSS+`">Accept all</button>';
root.getElementById("accept").addEventListener("click", (e) => { window.clicked = e.isTrusted; window.at = performance.timeOrigin + performance.now(); });
document.getElementById("closed").attachShadow({mode: "closed"}).innerHTML = "<button id=x>x</button>";
</script>`)
	g, run, tab := tabRig(t, st, consent.RunAccept, nil)
	before := tab.Now()
	res, err := tab.Click(t.Context(), consent.Locator{CSS: "#accept", Shadow: []string{"#host"}})
	if err != nil {
		t.Fatal(err)
	}
	var clicked bool
	g.eval(run.s, `window.clicked === true`, &clicked)
	if !res.Clicked || !clicked || res.Text != "Accept all" || res.Covered || res.NavigatedAway {
		t.Errorf("click %+v, page saw a trusted click: %v", res, clicked)
	}
	if res.Box.W != 160 || res.Box.H != 40 || res.Box.X != 40 {
		t.Errorf("box %+v", res.Box)
	}
	if res.AtMS < before || res.AtMS > tab.Now() {
		t.Errorf("click at %d, outside %d..%d", res.AtMS, before, tab.Now())
	}
	closed, err := tab.Click(t.Context(), consent.Locator{CSS: "#x", Shadow: []string{"#closed"}})
	if err != nil || closed.Clicked || closed.Detail != "closed shadow root" {
		t.Errorf("closed root: %+v %v", closed, err)
	}
	missing, err := tab.Click(t.Context(), consent.Locator{CSS: "#nope"})
	if err != nil || missing.Clicked || missing.Detail != "no element matches" {
		t.Errorf("missing: %+v %v", missing, err)
	}
}

// TestClickCrossOriginFrame: a button in a cross-site frame (its own
// process) is clicked at the right place on the page.
func TestClickCrossOriginFrame(t *testing.T) {
	st := newSite(t)
	st.page("shop.test/", `<!doctype html><title>f</title><style>body{margin:0}</style>
<iframe src="https://cmp.other.test/banner.html" style="position:absolute;left:100px;top:150px;width:400px;height:200px;border:5px solid #000;padding:3px"></iframe>`)
	st.page("cmp.other.test/banner.html", `<!doctype html><style>body{margin:0}</style>
<button id="reject" style="position:absolute;left:20px;top:30px;width:120px;height:30px">Reject all</button>
<script>document.getElementById("reject").addEventListener("click", (e) => { if (e.isTrusted) fetch("/clicked"); });</script>`)
	st.page("cmp.other.test/clicked", "ok")
	g, run, tab := tabRig(t, st, consent.RunReject, nil)
	g.waitFor(run.s, `document.querySelector("iframe").contentWindow !== null`)
	time.Sleep(testenv.Timeout(500 * time.Millisecond)) // the frame's document
	res, err := tab.Click(t.Context(), consent.Locator{CSS: "#reject", Frame: "cmp.other.test"})
	if err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(testenv.Timeout(5 * time.Second))
	for st.hits("cmp.other.test", "/clicked") == 0 && time.Now().Before(deadline) {
		time.Sleep(50 * time.Millisecond)
	}
	if !res.Clicked || st.hits("cmp.other.test", "/clicked") != 1 {
		t.Errorf("click %+v; the frame saw %d clicks", res, st.hits("cmp.other.test", "/clicked"))
	}
	if res.Box.X != 128 || res.Box.Y != 188 {
		t.Errorf("box %+v; want the frame's content box (108, 158) plus (20, 30)", res.Box)
	}
	frames, err := tab.Frames(t.Context())
	if err != nil || len(frames) != 2 || frames[0].CrossOrigin || !frames[1].CrossOrigin || frames[1].Origin != "https://cmp.other.test" {
		t.Fatalf("frames %+v %v", frames, err)
	}
	raw, err := tab.EvalIsolated(t.Context(), `(want) => location.host + "/" + want`, "x", frames[1])
	if err != nil || string(raw) != `"cmp.other.test/x"` {
		t.Errorf("script in the frame: %s %v", raw, err)
	}
	if _, err := tab.EvalIsolated(t.Context(), `() => 1`, consent.Frame{ID: "gone"}); err == nil {
		t.Error("a script ran in a frame that isn't there")
	}
	if res, _ := tab.Click(t.Context(), consent.Locator{CSS: "#reject", Frame: "nowhere.test"}); res.Clicked || res.Detail != "no frame matches nowhere.test" {
		t.Errorf("missing frame: %+v", res)
	}
}

// TestClickCovered: a transparent overlay over the button means no
// click: Covered is set and neither element gets one.
func TestClickCovered(t *testing.T) {
	st := newSite(t)
	st.page("shop.test/", `<!doctype html><title>c</title>
<button id="accept" style="`+buttonCSS+`">Accept</button>
<div id="veil" style="position:fixed;inset:0;opacity:0;z-index:9"></div>
<script>window.clicks = 0; for (const id of ["accept", "veil"]) document.getElementById(id).addEventListener("pointerdown", () => window.clicks++);</script>`)
	g, run, tab := tabRig(t, st, consent.RunAccept, nil)
	res, err := tab.Click(t.Context(), consent.Locator{CSS: "#accept"})
	if err != nil {
		t.Fatal(err)
	}
	var clicks int
	g.eval(run.s, `window.clicks`, &clicks)
	if !res.Covered || res.Clicked || clicks != 0 {
		t.Errorf("click %+v; %d clicks delivered", res, clicks)
	}
}

// TestEvalIsolatedIgnoresPagePatches: a page that breaks
// querySelector in its own world doesn't break Echo's scripts.
func TestEvalIsolatedIgnoresPagePatches(t *testing.T) {
	st := newSite(t)
	st.page("shop.test/", `<!doctype html><title>p</title><button id="accept">Accept</button><script>
Element.prototype.querySelector = Document.prototype.querySelector = function () { return null; };
JSON.stringify = function () { return '"forged"'; };
</script>`)
	_, _, tab := tabRig(t, st, consent.RunAccept, nil)
	raw, err := tab.EvalIsolated(t.Context(), `(sel) => ({found: !!document.querySelector(sel), text: document.querySelector(sel).textContent})`, "#accept")
	if err != nil || string(raw) != `{"found":true,"text":"Accept"}` {
		t.Errorf("isolated: %s %v", raw, err)
	}
	main, err := tab.EvalMain(t.Context(), "// patched\ndocument.querySelector('#accept') === null")
	if err != nil || string(main) != "true" {
		t.Errorf("main world: %s %v", main, err)
	}
}

// TestScreenshotCap: viewport screenshots land in the evidence folder
// (0600), at most Limits.Screenshots per run; none when they are off.
func TestScreenshotCap(t *testing.T) {
	st := newSite(t)
	st.page("shop.test/", `<!doctype html><title>s</title><h1>shop</h1>`)
	_, run, tab := tabRig(t, st, consent.RunReject, nil)
	p := run.s.rec.plan
	var paths []string
	for _, name := range []string{"banner", "after", "Page 1", "reload", "extra"} {
		path, err := tab.Screenshot(t.Context(), name)
		if err != nil {
			t.Fatal(err)
		}
		paths = append(paths, path)
	}
	want := []string{"evidence/page-1/reject-banner.png", "evidence/page-1/reject-after.png", "evidence/page-1/reject-page-1.png", "evidence/page-1/reject-reload.png", ""}
	if strings.Join(paths, " ") != strings.Join(want, " ") {
		t.Errorf("paths %q", paths)
	}
	info, err := os.Stat(filepath.Join(p.Dir, "evidence", "page-1", "reject-banner.png"))
	if err != nil || info.Mode().Perm() != 0o600 || info.Size() < 100 {
		t.Errorf("banner screenshot: %v %v", info, err)
	}
	b, _ := os.ReadFile(filepath.Join(p.Dir, filepath.FromSlash(paths[0])))
	if len(b) < 8 || string(b[1:4]) != "PNG" {
		t.Error("not a PNG")
	}

	_, _, off := tabRig(t, st, consent.RunAccept, func(p *consent.Plan) { p.Screenshots = false })
	if path, err := off.Screenshot(t.Context(), "banner"); path != "" || err != nil {
		t.Errorf("screenshots off: %q %v", path, err)
	}
}

// TestEvalResultCap: a script answering more than 256 KiB is an error,
// in either world.
func TestEvalResultCap(t *testing.T) {
	st := newSite(t)
	st.page("shop.test/", `<!doctype html><title>r</title>`)
	_, _, tab := tabRig(t, st, consent.RunNone, nil)
	if _, err := tab.EvalIsolated(t.Context(), `() => "x".repeat(300 * 1024)`); !errors.Is(err, errResultTooLarge) {
		t.Errorf("isolated: %v", err)
	}
	if _, err := tab.EvalMain(t.Context(), `"x".repeat(300 * 1024)`); !errors.Is(err, errResultTooLarge) {
		t.Errorf("main: %v", err)
	}
	if raw, err := tab.EvalIsolated(t.Context(), `() => "x".repeat(200 * 1024)`); err != nil || len(raw) != 200*1024+2 {
		t.Errorf("200 KiB: %d %v", len(raw), err)
	}
	// The main world measures its answer in the page too: an object is
	// refused before it is sent, a smaller one comes back whole.
	if _, err := tab.EvalMain(t.Context(), `({a: "x".repeat(300 * 1024)})`); !errors.Is(err, errResultTooLarge) {
		t.Errorf("main object: %v", err)
	}
	if raw, err := tab.EvalMain(t.Context(), `({a: "x".repeat(200 * 1024)})`); err != nil || len(raw) != 200*1024+8 {
		t.Errorf("main 200 KiB: %d %v", len(raw), err)
	}
	if raw, err := tab.EvalMain(t.Context(), "1 + 1 // a trailing comment"); err != nil || string(raw) != "2" {
		t.Errorf("trailing comment: %s %v", raw, err)
	}
	// A page that replaces JSON.stringify can skip the measuring but
	// can't change the answer or slip a large one past Echo.
	if raw, err := tab.EvalMain(t.Context(), `(JSON.stringify = () => '"forged"', 1)`); err != nil || string(raw) != "1" {
		t.Errorf("patched JSON.stringify: %s %v", raw, err)
	}
	if _, err := tab.EvalMain(t.Context(), `"x".repeat(300 * 1024)`); !errors.Is(err, errResultTooLarge) {
		t.Errorf("patched JSON.stringify, 300 KiB: %v", err)
	}
}

// TestClickNavigatesAway: an Accept that sends the tab to another host
// is reported as navigating away.
func TestClickNavigatesAway(t *testing.T) {
	st := newSite(t)
	st.page("shop.test/", `<!doctype html><title>n</title><button id="accept" style="`+buttonCSS+`">Accept</button>
<script>document.getElementById("accept").addEventListener("click", () => { location = "https://elsewhere.test/"; });</script>`)
	_, _, tab := tabRig(t, st, consent.RunAccept, func(p *consent.Plan) { p.MapOnly = true })
	res, err := tab.Click(t.Context(), consent.Locator{CSS: "#accept"})
	if err != nil || !res.Clicked || !res.NavigatedAway {
		t.Errorf("click %+v %v", res, err)
	}

	stay := newSite(t)
	stay.page("shop.test/", `<!doctype html><title>n</title><button id="accept" style="`+buttonCSS+`">Accept</button>
<script>document.getElementById("accept").addEventListener("click", () => { location = "/thanks"; });</script>`)
	stay.page("shop.test/thanks", "thanks")
	_, _, tab = tabRig(t, stay, consent.RunAccept, nil)
	if res, err := tab.Click(t.Context(), consent.Locator{CSS: "#accept"}); err != nil || !res.Clicked || res.NavigatedAway {
		t.Errorf("same host: %+v %v", res, err)
	}
}

// The state reader's reads: one cookie value (the target host's when
// several share the name), one localStorage value; Cookies lists without
// values. Reload waits for the new page.
func TestTabStateReadsAndReload(t *testing.T) {
	st := newSite(t)
	st.page("shop.test/", `<!doctype html><title>r</title><script>
document.cookie = "CookieConsent={stamp:%27x%27%2Cnecessary:true}; path=/";
localStorage.setItem("uc_settings", '{"consent":false}');
window.loads = (Number(sessionStorage.getItem("n")) || 0) + 1; sessionStorage.setItem("n", window.loads);
</script>`)
	g, run, tab := tabRig(t, st, consent.RunReject, nil)
	ctx := t.Context()
	if v, err := tab.CookieValue(ctx, "CookieConsent"); err != nil || v != "{stamp:%27x%27%2Cnecessary:true}" {
		t.Errorf("cookie %q %v", v, err)
	}
	if v, err := tab.CookieValue(ctx, "missing"); err != nil || v != "" {
		t.Errorf("missing cookie %q %v", v, err)
	}
	if v, err := tab.LocalStorage(ctx, "uc_settings"); err != nil || v != `{"consent":false}` {
		t.Errorf("localStorage %q %v", v, err)
	}
	if v, err := tab.LocalStorage(ctx, "missing"); err != nil || v != "" {
		t.Errorf("missing key %q %v", v, err)
	}
	cookies, err := tab.Cookies(ctx)
	if err != nil || len(cookies) != 1 || cookies[0].Length != len("{stamp:%27x%27%2Cnecessary:true}") {
		t.Errorf("cookies %+v %v", cookies, err)
	}
	out, _ := json.Marshal(cookies)
	if strings.Contains(string(out), "necessary") {
		t.Error("Cookies returned a value")
	}
	run.s.rec.plan.Settle = 200 * time.Millisecond
	if err := tab.Reload(ctx); err != nil {
		t.Fatal(err)
	}
	var loads int
	g.eval(run.s, `window.loads`, &loads)
	if loads != 2 {
		t.Errorf("loads %d after Reload", loads)
	}
}

// EvalMain takes constant scripts only; in tests every script is
// checked against the known ones.
func TestEvalMainKnownScripts(t *testing.T) {
	st := newSite(t)
	st.page("shop.test/", `<!doctype html><title>k</title>`)
	_, _, tab := tabRig(t, st, consent.RunNone, nil)
	known := map[string]bool{"1 + 1": true}
	checkMainScript = func(js string) error {
		if !known[js] {
			return errors.New("not a known script")
		}
		return nil
	}
	t.Cleanup(func() { checkMainScript = nil })
	if raw, err := tab.EvalMain(t.Context(), "1 + 1"); err != nil || string(raw) != "2" {
		t.Errorf("known: %s %v", raw, err)
	}
	if _, err := tab.EvalMain(t.Context(), "document.title"); err == nil {
		t.Error("an unknown script ran")
	}
}
