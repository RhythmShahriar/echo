// Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
// SPDX-License-Identifier: MIT

package browse

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/go-rod/rod/lib/proto"

	"github.com/RhythmShahriar/echo/consent"
)

// clickWait is how long a click waits for the pointerdown hooks.js saw,
// whose page time is the click's time on the run clock.
const clickWait = 100 * time.Millisecond

// navWait is how long after a click a top-frame navigation to another
// host counts as the click's.
const navWait = time.Second

// maxTopNavs bounds the top-frame navigations a run remembers.
const maxTopNavs = 256

// minSettleCap is the shortest a settle may take at most: a page that
// never goes quiet still ends within max(minSettleCap, 5 x --settle).
const minSettleCap = 10 * time.Second

// topNav is a document request of the tab's top frame.
type topNav struct {
	host string
	at   time.Time
}

// hostOfURL is the lower-case host of a URL, "" when it has none.
func hostOfURL(raw string) string {
	u, err := url.Parse(raw)
	if err != nil {
		return ""
	}
	return strings.ToLower(u.Hostname())
}

// checkMainScript, when set (tests only), vets every script EvalMain is
// given: it must be a constant from an embedded file or a rule, never
// text built from the page.
var checkMainScript func(js string) error

// locateJS finds a locator's element in a frame's isolated world,
// entering open shadow roots by their host selectors, scrolls it into
// view and says whether a visitor could click it at its center.
const locateJS = `async (input) => {
  let root = document;
  for (const host of input.shadow || []) {
    let h = null;
    try { h = root.querySelector(host); } catch (e) { return { found: false, detail: "bad selector" }; }
    if (!h) return { found: false, detail: "no element matches the shadow host" };
    if (!h.shadowRoot) return { found: false, detail: "closed shadow root" };
    root = h.shadowRoot;
  }
  let el = null;
  try { el = root.querySelector(input.css); } catch (e) { return { found: false, detail: "bad selector" }; }
  if (!el) return { found: false, detail: "no element matches" };
  try { el.scrollIntoView({ block: "center", inline: "center", behavior: "instant" }); } catch (e) { /* not scrollable */ }
  await new Promise((r) => { requestAnimationFrame(() => r()); setTimeout(r, 100); });
  const r = el.getBoundingClientRect();
  const out = { found: true, left: r.left, top: r.top, w: r.width, h: r.height,
    text: String(el.innerText || el.value || el.getAttribute("aria-label") || el.getAttribute("title") || "").trim().slice(0, 400) };
  let visible = true;
  try {
    visible = el.checkVisibility ? el.checkVisibility({ opacityProperty: true, visibilityProperty: true }) : true;
  } catch (e) { /* older engine */ }
  out.visible = visible;
  if (r.width < 1 || r.height < 1) return out;
  const x = r.left + r.width / 2, y = r.top + r.height / 2;
  out.inView = x >= 0 && y >= 0 && x <= innerWidth && y <= innerHeight;
  const top = (root.elementFromPoint ? root.elementFromPoint(x, y) : document.elementFromPoint(x, y));
  out.topmost = !!top && (top === el || el.contains(top));
  return out;
}`

// ownerBoxJS is the top-left of a frame owner's content box in its own
// document's viewport.
const ownerBoxJS = `function () {
  const r = this.getBoundingClientRect();
  const cs = getComputedStyle(this);
  return [r.left + this.clientLeft + (parseFloat(cs.paddingLeft) || 0), r.top + this.clientTop + (parseFloat(cs.paddingTop) || 0)];
}`

// getItemJS reads one localStorage value for a state parser.
const getItemJS = `(key) => { try { return localStorage.getItem(key); } catch (e) { return null; } }`

// located is locateJS's answer.
type located struct {
	Found   bool    `json:"found"`
	Detail  string  `json:"detail"`
	Left    float64 `json:"left"`
	Top     float64 `json:"top"`
	W       float64 `json:"w"`
	H       float64 `json:"h"`
	Text    string  `json:"text"`
	Visible bool    `json:"visible"`
	InView  bool    `json:"inView"`
	Topmost bool    `json:"topmost"`
}

// Tab is consent.Tab over one run's tab: what the CMP driver and the
// state reader may do with the page.
type Tab struct {
	s    *session
	page int
	kind consent.RunKind

	mu    sync.Mutex
	shots int
}

var _ consent.Tab = (*Tab)(nil)

// Tab returns the run's tab for the CMP driver and the state reader;
// page is the target's place in the plan, from 1, for the evidence
// file names.
func (r *Run) Tab(page int) *Tab { return &Tab{s: r.s, page: page, kind: r.s.rec.kind} }

// Settle waits until no request has started or ended for --settle, at
// most max(10s, 5 x --settle).
func (r *Run) Settle(ctx context.Context) error { return r.s.settle(ctx) }

// EvalIsolated runs the function expression js with args in Echo's
// isolated world of the top frame or, when the last argument is a
// consent.Frame, of that frame. The result is its JSON, at most
// 256 KiB.
func (t *Tab) EvalIsolated(ctx context.Context, js string, args ...any) (json.RawMessage, error) {
	f, args, err := t.frameArg(ctx, args)
	if err != nil {
		return nil, err
	}
	return t.s.evalIsolated(ctx, f, js, args...)
}

// frameArg picks the frame a script runs in: the consent.Frame passed
// last, else the top frame.
func (t *Tab) frameArg(ctx context.Context, args []any) (frame, []any, error) {
	var want *consent.Frame
	if n := len(args); n > 0 {
		switch v := args[n-1].(type) {
		case consent.Frame:
			want, args = &v, args[:n-1]
		case *consent.Frame:
			want, args = v, args[:n-1]
		}
	}
	fs, err := t.s.frames(ctx)
	if err != nil {
		return frame{}, nil, err
	}
	if len(fs) == 0 {
		return frame{}, nil, errors.New("the tab has no frame")
	}
	if want == nil {
		return fs[0], args, nil
	}
	for _, f := range fs {
		if string(f.id) == want.ID {
			return f, args, nil
		}
	}
	return frame{}, nil, errors.New("the frame is gone")
}

// EvalMain evaluates a constant expression (a CMP rule's API call or
// state script, or an embedded script) in the top frame's main world
// and returns its value as JSON, at most 256 KiB. The page can change
// anything in that world, so the answer is only as good as the page.
func (t *Tab) EvalMain(ctx context.Context, js string) (json.RawMessage, error) {
	if checkMainScript != nil {
		if err := checkMainScript(js); err != nil {
			return nil, err
		}
	}
	out, err := t.s.evalMain(ctx, js)
	if err != nil {
		return nil, err
	}
	if len(out) > maxEvalResult {
		return nil, errResultTooLarge
	}
	return out, nil
}

// Click clicks the element l names as a visitor would: it must be
// there, visible, sized, in view and the top-most element at its center,
// else nothing is clicked and the result says why. The click is a real
// mouse press and release at the center. AtMS is the time of the
// pointerdown the page saw, else the time of the press.
func (t *Tab) Click(ctx context.Context, l consent.Locator) (consent.ClickResult, error) {
	var res consent.ClickResult
	fs, err := t.s.frames(ctx)
	if err != nil {
		return res, err
	}
	if len(fs) == 0 {
		return res, errors.New("the tab has no frame")
	}
	f := fs[0]
	if l.Frame != "" {
		found := false
		for _, cand := range fs[1:] {
			if strings.Contains(cand.url, l.Frame) {
				f, found = cand, true
				break
			}
		}
		if !found {
			res.Detail = "no frame matches " + consent.SafeText(l.Frame, 80)
			return res, nil
		}
	}
	raw, err := t.s.evalIsolated(ctx, f, locateJS, map[string]any{"css": l.CSS, "shadow": l.Shadow})
	if err != nil {
		return res, err
	}
	var loc located
	if err := json.Unmarshal(raw, &loc); err != nil {
		return res, fmt.Errorf("reading the element: %w", err)
	}
	if !loc.Found {
		res.Detail = consent.SafeText(loc.Detail, 120)
		return res, nil
	}
	ox, oy, err := t.offset(ctx, f, fs)
	if err != nil {
		return res, err
	}
	res.Text = consent.SafeText(loc.Text, 120)
	res.Box = consent.Box{X: loc.Left + ox, Y: loc.Top + oy, W: loc.W, H: loc.H}
	switch {
	case !loc.Visible:
		res.Detail = "not visible"
		return res, nil
	case loc.W < 1 || loc.H < 1:
		res.Detail = "has no size"
		return res, nil
	case !loc.InView:
		res.Detail = "outside the viewport"
		return res, nil
	case !loc.Topmost:
		res.Covered, res.Detail = true, "covered by another element"
		return res, nil
	}
	x, y := res.Box.X+res.Box.W/2, res.Box.Y+res.Box.H/2
	return t.press(ctx, res, x, y)
}

// press sends the mouse events at x, y (top-frame CSS pixels) and works
// out the click's time and whether it took the tab off the site.
func (t *Tab) press(ctx context.Context, res consent.ClickResult, x, y float64) (consent.ClickResult, error) {
	rec := t.s.rec
	for len(rec.clicks) > 0 {
		<-rec.clicks // an earlier click's record
	}
	rec.mu.Lock()
	navsBefore := len(rec.topNavs)
	rec.mu.Unlock()
	before := t.topHost()
	main := t.s.main
	main.ctx = ctx
	started := time.Now()
	dispatchAt := rec.now()
	for _, typ := range []proto.InputDispatchMouseEventType{proto.InputDispatchMouseEventTypeMouseMoved,
		proto.InputDispatchMouseEventTypeMousePressed, proto.InputDispatchMouseEventTypeMouseReleased} {
		ev := proto.InputDispatchMouseEvent{Type: typ, X: x, Y: y}
		if typ != proto.InputDispatchMouseEventTypeMouseMoved {
			ev.Button, ev.ClickCount = proto.InputMouseButtonLeft, 1
		}
		if typ == proto.InputDispatchMouseEventTypeMousePressed {
			dispatchAt = rec.now()
		}
		if err := ev.Call(main); err != nil {
			return res, fmt.Errorf("clicking: %w", err)
		}
	}
	res.Clicked = true
	res.AtMS = dispatchAt
	timer := time.NewTimer(clickWait)
	defer timer.Stop()
	select {
	case at := <-rec.clicks:
		// The page's own pointerdown; a time far from the press is the
		// page's invention and is ignored.
		if at >= dispatchAt-50 && at <= rec.now() {
			res.AtMS = at
		}
	case <-timer.C:
	case <-ctx.Done():
		return res, ctx.Err()
	}
	for {
		if t.navigatedAway(before, navsBefore) {
			res.NavigatedAway = true
			break
		}
		if time.Since(started) >= navWait {
			break
		}
		select {
		case <-ctx.Done():
			return res, ctx.Err()
		case <-time.After(25 * time.Millisecond):
		}
	}
	return res, nil
}

// topHost is the host of the tab's top frame now.
func (t *Tab) topHost() string {
	rec := t.s.rec
	rec.mu.Lock()
	defer rec.mu.Unlock()
	if rec.main == nil {
		return ""
	}
	return hostOfURL(rec.frames[proto.PageFrameID(rec.main.id)])
}

// navigatedAway reports whether the top frame went to, or started to
// load, another host since the click.
func (t *Tab) navigatedAway(before string, navsBefore int) bool {
	if before == "" {
		return false
	}
	if h := t.topHost(); h != "" && h != before {
		return true
	}
	rec := t.s.rec
	rec.mu.Lock()
	defer rec.mu.Unlock()
	for _, n := range rec.topNavs[min(navsBefore, len(rec.topNavs)):] {
		if n.host != "" && n.host != before {
			return true
		}
	}
	return false
}

// offset is where frame f's viewport starts in the top frame's: the
// content box of each frame owner on the way up, read in the owner's
// own document.
func (t *Tab) offset(ctx context.Context, f frame, fs []frame) (float64, float64, error) {
	var x, y float64
	for depth := 0; f.parent != "" && depth < 16; depth++ {
		var parent *frame
		for i := range fs {
			if fs[i].id == f.parent {
				parent = &fs[i]
				break
			}
		}
		if parent == nil {
			return 0, 0, errors.New("the frame's parent is gone")
		}
		c := parent.s
		c.ctx = ctx
		owner, err := proto.DOMGetFrameOwner{FrameID: f.id}.Call(c)
		if err != nil {
			return 0, 0, fmt.Errorf("finding the frame's element: %w", err)
		}
		world, err := t.s.world(*parent)
		if err != nil {
			return 0, 0, err
		}
		obj, err := proto.DOMResolveNode{BackendNodeID: owner.BackendNodeID, ExecutionContextID: world}.Call(c)
		if err != nil || obj.Object == nil {
			return 0, 0, fmt.Errorf("reading the frame's element: %w", err)
		}
		box, err := proto.RuntimeCallFunctionOn{FunctionDeclaration: ownerBoxJS, ObjectID: obj.Object.ObjectID, ReturnByValue: true}.Call(c)
		_ = proto.RuntimeReleaseObject{ObjectID: obj.Object.ObjectID}.Call(c)
		if err != nil || box.Result == nil {
			return 0, 0, fmt.Errorf("measuring the frame: %w", err)
		}
		var xy [2]float64
		if err := json.Unmarshal([]byte(box.Result.Value.JSON("", "")), &xy); err != nil {
			return 0, 0, fmt.Errorf("measuring the frame: %w", err)
		}
		x, y = x+xy[0], y+xy[1]
		f = *parent
	}
	return x, y, nil
}

// Cookies lists the context's cookies, HttpOnly included, without their
// values.
func (t *Tab) Cookies(ctx context.Context) ([]consent.Cookie, error) {
	cookies, err := t.s.cookies(ctx)
	if err != nil {
		return nil, err
	}
	out := make([]consent.Cookie, 0, len(cookies))
	for _, c := range cookies {
		if c != nil {
			out = append(out, toCookie(c))
		}
	}
	return out, nil
}

// CookieValue reads one cookie's value for a state parser; it is never
// recorded. It is the cookie the top frame's page is sent, as the CMP's
// own script reads it: the domain, path and secure flag must match that
// page; of several the longest path wins. A choice stored under
// another path (Path=/blog, read on /) is no choice. "" when there is
// none.
func (t *Tab) CookieValue(ctx context.Context, name string) (string, error) {
	cookies, err := t.s.cookies(ctx)
	if err != nil {
		return "", err
	}
	return cookieFor(cookies, t.topURL(), name), nil
}

// topURL is the URL of the tab's top frame now, else the target's.
func (t *Tab) topURL() *url.URL {
	rec := t.s.rec
	rec.mu.Lock()
	raw := ""
	if rec.main != nil {
		raw = rec.frames[proto.PageFrameID(rec.main.id)]
	}
	rec.mu.Unlock()
	if u, err := url.Parse(raw); err == nil && u.Host != "" {
		return u
	}
	return rec.site
}

// cookieFor is the value of the cookie called name that a request to u
// carries (RFC 6265 5.4: domain and path match, secure only over https),
// the longest path first. "" when none does.
func cookieFor(cookies []*proto.NetworkCookie, u *url.URL, name string) string {
	if u == nil {
		return ""
	}
	host := strings.ToLower(u.Hostname())
	reqPath := u.EscapedPath()
	if reqPath == "" || reqPath[0] != '/' {
		reqPath = "/"
	}
	value, best := "", -1
	for _, c := range cookies {
		if c == nil || c.Name != name || (c.Secure && u.Scheme != "https") {
			continue
		}
		d := strings.ToLower(c.Domain)
		hostOnly := !strings.HasPrefix(d, ".")
		d = strings.TrimPrefix(d, ".")
		if host != d && (hostOnly || !strings.HasSuffix(host, "."+d)) {
			continue
		}
		if !pathMatch(reqPath, c.Path) || len(c.Path) <= best {
			continue
		}
		value, best = c.Value, len(c.Path)
	}
	return value
}

// pathMatch is RFC 6265 5.1.4: the cookie path is the request path, or
// a prefix of it that ends at a "/".
func pathMatch(reqPath, cookiePath string) bool {
	if cookiePath == "" {
		cookiePath = "/"
	}
	if reqPath == cookiePath {
		return true
	}
	if !strings.HasPrefix(reqPath, cookiePath) {
		return false
	}
	return strings.HasSuffix(cookiePath, "/") || reqPath[len(cookiePath)] == '/'
}

// LocalStorage reads one localStorage value of the top frame for a
// state parser; it is never recorded. "" when there is none.
func (t *Tab) LocalStorage(ctx context.Context, key string) (string, error) {
	raw, err := t.EvalIsolated(ctx, getItemJS, key)
	if err != nil {
		return "", err
	}
	var v *string
	if err := json.Unmarshal(raw, &v); err != nil || v == nil {
		return "", nil
	}
	return *v, nil
}

// Screenshot saves the viewport as evidence/page-N/<run>-<name>.png in
// the plan's folder (0600) and returns that path. With screenshots off,
// or past Limits.Screenshots for the run, it saves nothing and returns
// "".
func (t *Tab) Screenshot(ctx context.Context, name string) (string, error) {
	p := t.s.rec.plan
	if !p.Screenshots || p.Dir == "" {
		return "", nil
	}
	t.mu.Lock()
	if t.shots >= t.s.rec.limits().Screenshots {
		t.mu.Unlock()
		return "", nil
	}
	t.shots++
	t.mu.Unlock()
	main := t.s.main
	main.ctx = ctx
	shot, err := proto.PageCaptureScreenshot{Format: proto.PageCaptureScreenshotFormatPng}.Call(main)
	if err != nil {
		return "", fmt.Errorf("taking a screenshot: %w", err)
	}
	rel := fmt.Sprintf("evidence/page-%d/%s-%s.png", max(t.page, 1), t.kind, fileWord(name))
	path := filepath.Join(p.Dir, filepath.FromSlash(rel))
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return "", err
	}
	if err := os.WriteFile(path, shot.Data, 0o600); err != nil {
		return "", err
	}
	return rel, nil
}

// fileWord keeps a screenshot name to lower-case letters, digits and
// dashes, so a name can't leave the evidence folder.
func fileWord(s string) string {
	var b strings.Builder
	for _, r := range strings.ToLower(s) {
		if b.Len() >= 40 {
			break
		}
		if r >= 'a' && r <= 'z' || r >= '0' && r <= '9' || r == '-' {
			b.WriteRune(r)
		} else {
			b.WriteByte('-')
		}
	}
	if b.Len() == 0 {
		return "shot"
	}
	return b.String()
}

// Frames lists the tab's frames, top first. CrossOrigin is set for a
// frame whose origin differs from the top frame's.
func (t *Tab) Frames(ctx context.Context) ([]consent.Frame, error) {
	fs, err := t.s.frames(ctx)
	if err != nil {
		return nil, err
	}
	out := make([]consent.Frame, 0, len(fs))
	top := ""
	for i, f := range fs {
		o := origin(f.url)
		if i == 0 {
			top = o
		}
		out = append(out, consent.Frame{ID: string(f.id), URL: f.url, Origin: o, CrossOrigin: i > 0 && o != top})
	}
	return out, nil
}

// Now is the current time on the run clock.
func (t *Tab) Now() int64 { return t.s.rec.now() }

// Reload loads the page again and waits for it to settle.
func (t *Tab) Reload(ctx context.Context) error {
	rec := t.s.rec
	loads := rec.loads.Load()
	rec.activity.Store(time.Now().UnixNano())
	main := t.s.main
	main.ctx = ctx
	if err := (proto.PageReload{}).Call(main); err != nil {
		return fmt.Errorf("reloading: %w", err)
	}
	return t.s.settleAfter(ctx, loads)
}

// cookies reads the context's cookies.
func (s *session) cookies(ctx context.Context) ([]*proto.NetworkCookie, error) {
	root := s.h.root
	root.ctx = ctx
	res, err := proto.StorageGetCookies{BrowserContextID: s.ctx.BrowserContextID}.Call(root)
	if err != nil {
		return nil, err
	}
	return res.Cookies, nil
}

// settle waits until no request has started or ended for --settle,
// ending after max(10s, 5 x --settle) on a page that never goes quiet.
func (s *session) settle(ctx context.Context) error { return s.settleAfter(ctx, -1) }

// settleAfter is settle that first waits for the top frame's load event
// number loads+1 (loads < 0: no wait), within the same bound.
func (s *session) settleAfter(ctx context.Context, loads int64) error {
	quiet := s.rec.plan.Settle
	limit := max(minSettleCap, 5*quiet)
	start := time.Now()
	tick := time.NewTicker(25 * time.Millisecond)
	defer tick.Stop()
	for {
		loaded := loads < 0 || s.rec.loads.Load() > loads
		last := time.Unix(0, s.rec.activity.Load())
		if loaded && time.Since(last) >= quiet {
			return nil
		}
		if time.Since(start) >= limit {
			return nil
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-tick.C:
		}
	}
}
