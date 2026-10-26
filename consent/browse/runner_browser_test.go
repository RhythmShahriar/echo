// Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
// SPDX-License-Identifier: MIT

//go:build browser

package browse

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/RhythmShahriar/echo/consent"
	"github.com/RhythmShahriar/echo/consent/consenttest"
	"github.com/RhythmShahriar/echo/internal/testenv"
)

// pageCMP is a CMP driver for the fixture shop below: a #banner with
// #accept and #reject, a #reopen button and the choice in the cookie
// "consent". It clicks with the real Tab, as the CMP stage does.
type pageCMP struct {
	mu    sync.Mutex
	calls []string
	// onChoose, when set, runs before each Choose and may replace its
	// answer (ok false keeps the real one).
	onChoose func(ctx context.Context, k consent.RunKind, n int) (*consent.Choice, consent.Reason, bool)
	chooses  int
}

var _ consent.CMPDriver = (*pageCMP)(nil)

const bannerShowingJS = `() => { const b = document.querySelector("#banner"); return !!b && getComputedStyle(b).display !== "none"; }`

func (c *pageCMP) log(s string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.calls = append(c.calls, s)
}

func (c *pageCMP) showing(ctx context.Context, t consent.Tab) bool {
	raw, err := t.EvalIsolated(ctx, bannerShowingJS)
	return err == nil && string(raw) == "true"
}

func (c *pageCMP) Detect(ctx context.Context, t consent.Tab, _ *consent.Plan, wait time.Duration) (consent.CMPInfo, error) {
	c.log("Detect")
	deadline := time.Now().Add(wait)
	for {
		if c.showing(ctx, t) {
			return consent.CMPInfo{ID: "page", Name: "Page CMP", Found: true, Present: true, DetectedBy: "selector:#banner", BannerAtMS: t.Now(), RejectLayer: 1}, nil
		}
		if time.Now().After(deadline) {
			return consent.CMPInfo{}, nil
		}
		if err := sleep(ctx, 100*time.Millisecond); err != nil {
			return consent.CMPInfo{}, err
		}
	}
}

func (c *pageCMP) Find(_ context.Context, _ consent.Tab, _ *consent.Plan, _ consent.CMPInfo, k consent.RunKind) (*consent.Choice, consent.Reason, error) {
	c.log("Find " + string(k))
	return &consent.Choice{How: "button", Selector: "#" + string(k), Text: k.Label() + " all", Layer: 1}, consent.ReasonNone, nil
}

func (c *pageCMP) Choose(ctx context.Context, t consent.Tab, _ *consent.Plan, _ consent.CMPInfo, k consent.RunKind) (*consent.Choice, consent.Reason, error) {
	c.log("Choose " + string(k))
	c.mu.Lock()
	c.chooses++
	n, hook := c.chooses, c.onChoose
	c.mu.Unlock()
	if hook != nil {
		if ch, r, ok := hook(ctx, k, n); ok {
			return ch, r, ctx.Err()
		}
	}
	res, err := t.Click(ctx, consent.Locator{CSS: "#" + string(k)})
	if err != nil {
		return nil, consent.ReasonNone, err
	}
	if !res.Clicked {
		return nil, consent.ReasonNoButton, nil
	}
	ch := &consent.Choice{How: "button", Selector: "#" + string(k), Text: res.Text, Layer: 1, Clicks: 1, AtMS: res.AtMS, Box: &res.Box}
	if res.NavigatedAway {
		return ch, consent.ReasonNavigation, nil
	}
	if c.showing(ctx, t) {
		return ch, consent.ReasonClickMissed, nil
	}
	ch.Verified, ch.VerifiedBy = true, "banner"
	return ch, consent.ReasonNone, nil
}

func (c *pageCMP) Reopen(ctx context.Context, t consent.Tab, _ *consent.Plan, _ consent.CMPInfo) error {
	c.log("Reopen")
	res, err := t.Click(ctx, consent.Locator{CSS: "#reopen"})
	if err != nil {
		return err
	}
	if !res.Clicked {
		return errors.New("no reopen button")
	}
	return nil
}

func (c *pageCMP) BannerShowing(ctx context.Context, t consent.Tab, _ consent.CMPInfo) (bool, error) {
	c.log("BannerShowing")
	return c.showing(ctx, t), nil
}

func (c *pageCMP) State(ctx context.Context, t consent.Tab, _ consent.CMPInfo) (*consent.CMPState, error) {
	c.log("State")
	v, err := t.CookieValue(ctx, "consent")
	if err != nil {
		return nil, err
	}
	return &consent.CMPState{CMP: "page", Source: "cookie:consent", Decided: v != "", Categories: map[string]bool{"statistics": v == "all"}}, nil
}

func (c *pageCMP) Rules() []consent.CMPRuleInfo {
	return []consent.CMPRuleInfo{{ID: "page", Name: "Page CMP"}}
}

func (c *pageCMP) Info() consent.DataInfo {
	return consent.DataInfo{Version: "test", SHA256: "page", Count: 1}
}

// shopPage is the fixture shop: a banner, a reopen button, a long page
// and links, some of which a check must never follow. Accept writes _ga
// and sends a GA4 hit (held). extra goes at the end of the body.
func shopPage(extra string) string {
	return `<!doctype html><title>Shop</title><style>body{margin:0}</style>
<div id="banner" style="position:fixed;left:0;bottom:0;width:700px;height:90px;background:#eee;z-index:9">
<button id="accept" style="position:absolute;left:20px;top:20px;width:150px;height:40px">Accept all</button>
<button id="reject" style="position:absolute;left:200px;top:20px;width:150px;height:40px">Reject all</button></div>
<button id="reopen" style="position:fixed;right:10px;top:10px;width:120px;height:30px">Cookie settings</button>
<p><a href="/logout">Log out</a> <a href="/cart/">Cart</a> <a href="mailto:shop@shop.test">Mail us</a>
<a href="https://elsewhere.other.test/">Elsewhere</a> <a href="/report.pdf">Report</a> <a href="/about">About us</a> <a href="/about#team">Team</a></p>
<div style="height:3000px"></div>
<script>
function choice() { return (document.cookie.match(/(?:^|; )consent=([^;]*)/) || [])[1] || ""; }
function hit(ev) { new Image().src = "https://region1.google-analytics.com/g/collect?v=2&en=" + ev; }
const banner = document.getElementById("banner");
if (choice()) banner.style.display = "none";
if (choice() === "all") { hit("page_view"); }
document.getElementById("accept").addEventListener("click", () => {
  document.cookie = "consent=all; path=/"; banner.style.display = "none"; document.cookie = "_ga=GA1.1.42; path=/"; hit("accept");
});
document.getElementById("reject").addEventListener("click", () => { document.cookie = "consent=none; path=/"; banner.style.display = "none"; });
document.getElementById("reopen").addEventListener("click", () => { banner.style.display = "block"; });
</script>` + extra
}

// shopSite serves the fixture shop on shop.test with an about page.
func shopSite(t *testing.T, extra string) *site {
	t.Helper()
	st := newSite(t)
	st.page("shop.test/", shopPage(extra))
	st.page("shop.test/about", shopPage(""))
	return st
}

// shopPlan is a plan for https://shop.test/ answered by st, with every
// other host refused, short waits and no retries.
func shopPlan(t *testing.T, st *site, runs ...consent.RunKind) *consent.Plan {
	t.Helper()
	p := consenttest.NewPlan(t, "https://shop.test/")
	p.MapHosts = map[string]string{"shop.test": st.addr(), "*.other.test": st.addr()}
	p.MapOnly = true
	p.Settle = 300 * time.Millisecond
	p.BannerWait = 3 * time.Second
	p.Timeout = testenv.Timeout(60 * time.Second)
	p.Retries = 0
	if len(runs) > 0 {
		p.Runs = runs
	}
	p.Browser.NoDownload = true
	return p
}

// runCheck runs p with the fake catalog and signals and cmp.
func runCheck(t *testing.T, p *consent.Plan, cmp consent.CMPDriver) (*consent.Result, error) {
	t.Helper()
	needChrome(t)
	consenttest.Use(t, consent.Stages{Classifier: &consenttest.FakeClassifier{}, Signals: consenttest.FakeSignals{}, CMP: cmp})
	return Runner{}.Run(context.Background(), p)
}

// runOf is the page's run of kind.
func runOf(t *testing.T, pg consent.Page, kind consent.RunKind) consent.Run {
	t.Helper()
	for _, r := range pg.Runs {
		if r.Kind == kind {
			return r
		}
	}
	t.Fatalf("no %s run in %+v", kind, pg.Runs)
	return consent.Run{}
}

// steps lists a run's visit steps.
func steps(r consent.Run) []string {
	var out []string
	for _, v := range r.Visits {
		out = append(out, v.Step)
	}
	return out
}

// TestRunSequence: every run loads the page, makes its choice, visits
// the one page a visitor may follow and reloads, with both screenshots;
// Accept's hit and cookie come after the click, Reject's run has none.
func TestRunSequence(t *testing.T) {
	st := shopSite(t, "")
	p := shopPlan(t, st)
	res, err := runCheck(t, p, &pageCMP{})
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Pages) != 1 {
		t.Fatalf("pages = %d", len(res.Pages))
	}
	pg := res.Pages[0]
	if pg.URL != "https://shop.test/" || pg.FinalURL != "https://shop.test/" || len(pg.Redirects) != 0 || !pg.CMP.Found || pg.CMP.ID != "page" {
		t.Errorf("page = %+v", pg)
	}
	for _, kind := range []consent.RunKind{consent.RunNone, consent.RunAccept, consent.RunReject} {
		r := runOf(t, pg, kind)
		if r.Status != consent.StatusOK || r.Reason != consent.ReasonNone || r.Attempts != 1 {
			t.Errorf("%s: status %s reason %q detail %q attempts %d", kind, r.Status, r.Reason, r.Detail, r.Attempts)
		}
		if got := steps(r); !slices.Equal(got, []string{"load", "page", "reload"}) {
			t.Errorf("%s: steps %v", kind, got)
		}
		if len(r.Visits) == 3 && (r.Visits[1].URL != "https://shop.test/about" || !r.Visits[0].Banner) {
			t.Errorf("%s: visits %+v", kind, r.Visits)
		}
		for _, name := range []string{"banner", "after"} {
			rel := r.Screenshots[name]
			if rel != fmt.Sprintf("evidence/page-1/%s-%s.png", kind, name) {
				t.Errorf("%s: screenshot %s = %q", kind, name, rel)
				continue
			}
			if _, err := os.Stat(filepath.Join(p.Dir, rel)); err != nil {
				t.Errorf("%s: %v", kind, err)
			}
		}
		if r.DurationMS <= 0 || len(r.Events) == 0 {
			t.Errorf("%s: duration %d, %d events", kind, r.DurationMS, len(r.Events))
		}
	}
	accept := runOf(t, pg, consent.RunAccept)
	if accept.Choice == nil || !accept.Choice.Verified || accept.Choice.AtMS <= 0 {
		t.Fatalf("accept choice %+v", accept.Choice)
	}
	if len(accept.Hits) == 0 {
		t.Error("accept: no hit")
	}
	for _, h := range accept.Hits {
		if h.Phase != consent.PhaseAfterAccept || !h.Held || h.Tracker != "ga4" || h.AtMS < accept.Choice.AtMS {
			t.Errorf("accept hit %+v", h)
		}
	}
	if accept.FinalState == nil || !accept.FinalState.Decided || !accept.FinalState.Categories["statistics"] {
		t.Errorf("accept final state %+v", accept.FinalState)
	}
	reject := runOf(t, pg, consent.RunReject)
	if len(reject.Hits) != 0 || reject.Visits[2].Banner {
		t.Errorf("reject hits %+v, visits %+v", reject.Hits, reject.Visits)
	}
	if reject.FinalState == nil || !reject.FinalState.Decided || reject.FinalState.Categories["statistics"] {
		t.Errorf("reject final state %+v", reject.FinalState)
	}
	if none := runOf(t, pg, consent.RunNone); none.Choice != nil || none.FinalState != nil || len(none.Hits) != 0 {
		t.Errorf("none run %+v", none)
	}
	if res.Browser == "" || !strings.Contains(res.UserAgent, UASuffix+"1.2.0") || res.OS == "" || !res.Sandbox || !res.TLSVerified ||
		!res.Held || !res.Mapped || res.Viewport != "desktop" || res.Locale != "en-US" || res.Catalog.Count != 2 || res.CMPRules.SHA256 != "page" {
		t.Errorf("result setup %+v", res)
	}
	if res.Network.Requests == 0 || res.Network.Held == 0 {
		t.Errorf("network %+v", res.Network)
	}
	if st.hits("region1.google-analytics.com", "") != 0 {
		t.Error("a held hit reached a server")
	}
}

// TestSettleEndsWhenQuiet: a quiet page settles after --settle.
func TestSettleEndsWhenQuiet(t *testing.T) {
	st := newSite(t)
	st.page("shop.test/", `<!doctype html><title>q</title><p>quiet`)
	p := consenttest.NewPlan(t, "https://shop.test/")
	p.MapHosts, p.MapOnly, p.Settle = map[string]string{"shop.test": st.addr()}, true, 500*time.Millisecond
	g := newRig(t, p)
	run := g.openRun("https://shop.test/", consent.RunNone)
	g.load(run.s, "https://shop.test/")
	start := time.Now()
	if err := run.Settle(t.Context()); err != nil {
		t.Fatal(err)
	}
	if d := time.Since(start); d > testenv.Timeout(2*time.Second) {
		t.Errorf("a quiet page took %s to settle", d)
	}
}

// TestSettleCapped: a page that polls every 500 ms never goes quiet for
// 1s; the wait ends at max(10s, 5 x 1s).
func TestSettleCapped(t *testing.T) {
	st := newSite(t)
	st.page("shop.test/", `<!doctype html><title>p</title><script>setInterval(() => fetch("/poll"), 500);</script>`)
	st.page("shop.test/poll", "ok")
	p := consenttest.NewPlan(t, "https://shop.test/")
	p.MapHosts, p.MapOnly, p.Settle = map[string]string{"shop.test": st.addr()}, true, time.Second
	g := newRig(t, p)
	run := g.openRun("https://shop.test/", consent.RunNone)
	g.load(run.s, "https://shop.test/")
	start := time.Now()
	if err := run.Settle(t.Context()); err != nil {
		t.Fatal(err)
	}
	if d := time.Since(start); d < 9500*time.Millisecond || d > testenv.Timeout(13*time.Second) {
		t.Errorf("settle took %s; want about 10s", d)
	}
}

// TestRetryInconclusive: an Accept that misses once is made again in a
// fresh context and counts two attempts.
func TestRetryInconclusive(t *testing.T) {
	st := shopSite(t, "")
	p := shopPlan(t, st, consent.RunAccept)
	p.Retries = 2
	cmp := &pageCMP{onChoose: func(_ context.Context, _ consent.RunKind, n int) (*consent.Choice, consent.Reason, bool) {
		return nil, consent.ReasonClickMissed, n == 1
	}}
	res, err := runCheck(t, p, cmp)
	if err != nil {
		t.Fatal(err)
	}
	r := res.Pages[0].Runs[0]
	if r.Attempts != 2 || r.Status != consent.StatusOK || r.Reason != consent.ReasonNone || r.Choice == nil {
		t.Errorf("run %+v", r)
	}
}

// TestNoRetryOnIssues: a Reject that finds no button is not made again
// when the run already saw a tracker fire before the choice.
func TestNoRetryOnIssues(t *testing.T) {
	st := shopSite(t, `<script>hit("early");</script>`)
	p := shopPlan(t, st, consent.RunReject)
	p.Retries = 3
	cmp := &pageCMP{onChoose: func(context.Context, consent.RunKind, int) (*consent.Choice, consent.Reason, bool) {
		return nil, consent.ReasonNoButton, true
	}}
	res, err := runCheck(t, p, cmp)
	if err != nil {
		t.Fatal(err)
	}
	r := res.Pages[0].Runs[0]
	if r.Attempts != 1 || r.Status != consent.StatusNoButton || r.Reason != consent.ReasonNoButton {
		t.Errorf("run %s %q attempts %d", r.Status, r.Reason, r.Attempts)
	}
	if len(r.Hits) == 0 || r.Hits[0].Phase != consent.PhaseBefore {
		t.Errorf("hits %+v", r.Hits)
	}
	// Without the early hit the same run is retried.
	st2 := shopSite(t, "")
	p2 := shopPlan(t, st2, consent.RunReject)
	p2.Retries = 1
	res, err = runCheck(t, p2, cmp)
	if err != nil {
		t.Fatal(err)
	}
	if r := res.Pages[0].Runs[0]; r.Attempts != 2 {
		t.Errorf("a clean inconclusive run made %d attempts; want 2", r.Attempts)
	}
}

// shape is a run without its times: what --parallel must not change.
type shape struct {
	Status   consent.RunStatus
	Reason   consent.Reason
	Steps    []string
	Banner   []bool
	Hits     []string
	Cookies  []string
	Choice   bool
	Decided  bool
	Attempts int
}

func shapeOf(r consent.Run) shape {
	s := shape{Status: r.Status, Reason: r.Reason, Steps: steps(r), Choice: r.Choice != nil, Attempts: r.Attempts}
	for _, v := range r.Visits {
		s.Banner = append(s.Banner, v.Banner)
	}
	for _, h := range r.Hits {
		s.Hits = append(s.Hits, h.Tracker+" "+string(h.Phase))
	}
	slices.Sort(s.Hits)
	s.Hits = slices.Compact(s.Hits)
	for _, c := range r.Cookies {
		s.Cookies = append(s.Cookies, c.Name+" "+string(c.Phase))
	}
	slices.Sort(s.Cookies)
	s.Cookies = slices.Compact(s.Cookies)
	if r.FinalState != nil {
		s.Decided = r.FinalState.Decided
	}
	return s
}

// TestParallelIdentical (qa e07): --parallel 1 and 3 record the same
// runs apart from times.
func TestParallelIdentical(t *testing.T) {
	st := shopSite(t, "")
	var got [2][]shape
	for i, n := range []int{1, 3} {
		p := shopPlan(t, st, consent.RunNone, consent.RunAccept, consent.RunReject, consent.RunWithdraw)
		p.Parallel = n
		res, err := runCheck(t, p, &pageCMP{})
		if err != nil {
			t.Fatal(err)
		}
		for _, r := range res.Pages[0].Runs {
			got[i] = append(got[i], shapeOf(r))
		}
	}
	if !reflect.DeepEqual(got[0], got[1]) {
		t.Errorf("--parallel 1: %+v\n--parallel 3: %+v", got[0], got[1])
	}
	withdraw := got[0][3]
	if withdraw.Status != consent.StatusOK || !withdraw.Decided {
		t.Errorf("withdraw %+v", withdraw)
	}
}

// alive reports whether a process with pid still runs.
func alive(pid int) bool {
	p, err := os.FindProcess(pid)
	if err != nil {
		return false
	}
	return processAlive(p)
}

// gone waits a moment for Chrome's process and profile to be gone.
func gone(t *testing.T, pid int, profile string) {
	t.Helper()
	deadline := time.Now().Add(testenv.Timeout(10 * time.Second))
	for {
		_, statErr := os.Stat(profile)
		if !alive(pid) && os.IsNotExist(statErr) {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("after the check: Chrome %d alive %v, profile %s stat %v", pid, alive(pid), profile, statErr)
		}
		time.Sleep(100 * time.Millisecond)
	}
}

// watchEngine records the Engine a check starts.
func watchEngine(t *testing.T) func() (int, string) {
	t.Helper()
	var mu sync.Mutex
	var pid int
	var profile string
	engineStarted = func(e *Engine) {
		mu.Lock()
		defer mu.Unlock()
		pid, profile = e.b.PID(), e.b.ProfileDir()
	}
	t.Cleanup(func() { engineStarted = nil })
	return func() (int, string) {
		mu.Lock()
		defer mu.Unlock()
		return pid, profile
	}
}

// TestCanceledPartial (qa e05): Ctrl-C during Reject keeps the finished
// Accept, marks Reject canceled and leaves no Chrome or profile behind.
func TestCanceledPartial(t *testing.T) {
	st := shopSite(t, "")
	p := shopPlan(t, st, consent.RunAccept, consent.RunReject)
	p.Parallel = 1
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	cmp := &pageCMP{onChoose: func(ctx context.Context, k consent.RunKind, _ int) (*consent.Choice, consent.Reason, bool) {
		if k != consent.RunReject {
			return nil, "", false
		}
		cancel()
		<-ctx.Done()
		return nil, consent.ReasonCanceled, true
	}}
	engine := watchEngine(t)
	needChrome(t)
	consenttest.Use(t, consent.Stages{Classifier: &consenttest.FakeClassifier{}, Signals: consenttest.FakeSignals{}, CMP: cmp})
	res, err := Runner{}.Run(ctx, p)
	if !errors.Is(err, context.Canceled) || res == nil {
		t.Fatalf("Run = %v, %v", res, err)
	}
	accept, reject := runOf(t, res.Pages[0], consent.RunAccept), runOf(t, res.Pages[0], consent.RunReject)
	if accept.Status != consent.StatusOK || reject.Status != consent.StatusCanceled || reject.Reason != consent.ReasonCanceled {
		t.Errorf("accept %s, reject %s %q", accept.Status, reject.Status, reject.Reason)
	}
	pid, profile := engine()
	if pid == 0 || profile == "" {
		t.Fatal("the engine wasn't seen")
	}
	gone(t, pid, profile)
}

// TestConsentProfileRemoved: after a normal run Chrome is gone and so is
// its temporary profile.
func TestConsentProfileRemoved(t *testing.T) {
	st := shopSite(t, "")
	p := shopPlan(t, st, consent.RunNone)
	engine := watchEngine(t)
	if _, err := runCheck(t, p, &pageCMP{}); err != nil {
		t.Fatal(err)
	}
	pid, profile := engine()
	gone(t, pid, profile)
}

// redirectSite answers shop.test with handlers by path, for redirects
// and statuses the plain site can't give.
func redirectSite(t *testing.T, routes map[string]http.HandlerFunc) string {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if h, ok := routes[r.URL.Path]; ok {
			h(w, r)
			return
		}
		if strings.HasPrefix(r.URL.Path, "/r/") {
			if h, ok := routes["/r/"]; ok {
				h(w, r)
				return
			}
		}
		w.Header().Set("Content-Type", "text/html")
		_, _ = fmt.Fprint(w, shopPage(""))
	}))
	t.Cleanup(srv.Close)
	return strings.TrimPrefix(srv.URL, "http://")
}

// fatalOf runs p and returns the fatal error it must end with.
func fatalOf(t *testing.T, p *consent.Plan) *consent.FatalError {
	t.Helper()
	res, err := runCheck(t, p, &pageCMP{})
	var f *consent.FatalError
	if !errors.As(err, &f) {
		t.Fatalf("Run = %v; want a fatal error", err)
	}
	if res == nil || res.Pages[0].Runs[0].Status != consent.StatusLoadFailed {
		t.Errorf("result %+v", res)
	}
	return f
}

// TestConsentRedirectLoop: a loop and a chain of more than 10 redirects
// end the check with redirect-loop well within --timeout.
func TestConsentRedirectLoop(t *testing.T) {
	addr := redirectSite(t, map[string]http.HandlerFunc{
		"/":     func(w http.ResponseWriter, r *http.Request) { http.Redirect(w, r, "/a", http.StatusFound) },
		"/a":    func(w http.ResponseWriter, r *http.Request) { http.Redirect(w, r, "/", http.StatusFound) },
		"/long": func(w http.ResponseWriter, r *http.Request) { http.Redirect(w, r, "/r/1", http.StatusFound) },
		"/r/": func(w http.ResponseWriter, r *http.Request) {
			var n int
			_, _ = fmt.Sscanf(r.URL.Path, "/r/%d", &n)
			if n >= 15 {
				_, _ = fmt.Fprint(w, "end")
				return
			}
			http.Redirect(w, r, fmt.Sprintf("/r/%d", n+1), http.StatusFound)
		},
	})
	for _, start := range []string{"https://shop.test/", "https://shop.test/long"} {
		p := consenttest.NewPlan(t, start)
		p.MapHosts, p.MapOnly, p.Runs, p.Retries = map[string]string{"shop.test": addr}, true, []consent.RunKind{consent.RunNone}, 0
		p.Settle, p.Timeout, p.Browser.NoDownload = 200*time.Millisecond, 30*time.Second, true
		began := time.Now()
		f := fatalOf(t, p)
		if f.Code != consent.FatalRedirectLoop || f.URL != start {
			t.Errorf("%s: %+v", start, f)
		}
		if d := time.Since(began); d > 25*time.Second {
			t.Errorf("%s: took %s", start, d)
		}
	}
}

// TestConsentRedirectChain: the redirects of the start page are kept,
// masked; one to another host stops the check unless --follow-host.
func TestConsentRedirectChain(t *testing.T) {
	addr := redirectSite(t, map[string]http.HandlerFunc{
		"/": func(w http.ResponseWriter, r *http.Request) {
			if strings.HasPrefix(r.Host, "elsewhere.") {
				w.Header().Set("Content-Type", "text/html")
				_, _ = fmt.Fprint(w, shopPage(""))
				return
			}
			http.Redirect(w, r, "/step?email=jane@example.com", http.StatusFound)
		},
		"/step": func(w http.ResponseWriter, r *http.Request) {
			http.Redirect(w, r, "/home", http.StatusMovedPermanently)
		},
		"/away": func(w http.ResponseWriter, r *http.Request) {
			http.Redirect(w, r, "https://elsewhere.other.test/", http.StatusFound)
		},
	})
	p := consenttest.NewPlan(t, "https://shop.test/")
	p.MapHosts = map[string]string{"shop.test": addr, "*.other.test": addr}
	p.MapOnly, p.Runs, p.Settle, p.Browser.NoDownload = true, []consent.RunKind{consent.RunNone}, 200*time.Millisecond, true
	res, err := runCheck(t, p, &pageCMP{})
	if err != nil {
		t.Fatal(err)
	}
	pg := res.Pages[0]
	if len(pg.Redirects) != 2 || pg.FinalURL != "https://shop.test/home" || !strings.HasPrefix(pg.Redirects[0], "https://shop.test/step?email=") {
		t.Fatalf("redirects %q, final %q", pg.Redirects, pg.FinalURL)
	}
	if strings.Contains(strings.Join(pg.Redirects, " "), "jane") {
		t.Errorf("an e-mail address in the redirects: %q", pg.Redirects)
	}

	away := consenttest.NewPlan(t, "https://shop.test/away")
	away.MapHosts, away.MapOnly, away.Runs, away.Retries = p.MapHosts, true, []consent.RunKind{consent.RunNone}, 0
	away.Settle, away.Browser.NoDownload = 200*time.Millisecond, true
	f := fatalOf(t, away)
	if f.Code != consent.FatalRedirectHost || f.URL != "https://shop.test/away" || f.To != "https://elsewhere.other.test/" {
		t.Errorf("fatal %+v", f)
	}
	away.FollowHost = true
	res, err = runCheck(t, away, &pageCMP{})
	if err != nil || res.Pages[0].FinalURL != "https://elsewhere.other.test/" {
		t.Errorf("with --follow-host: %v, %+v", err, res)
	}
}

// TestBotWall: a Cloudflare challenge stops the check as a bot wall,
// and a login wall as an HTTP status.
func TestBotWall(t *testing.T) {
	addr := redirectSite(t, map[string]http.HandlerFunc{
		"/": func(w http.ResponseWriter, _ *http.Request) {
			w.Header().Set("cf-mitigated", "challenge")
			w.Header().Set("Content-Type", "text/html")
			w.WriteHeader(http.StatusForbidden)
			_, _ = fmt.Fprint(w, "<title>Just a moment...</title>")
		},
		"/login": func(w http.ResponseWriter, _ *http.Request) {
			w.WriteHeader(http.StatusUnauthorized)
			_, _ = fmt.Fprint(w, "who are you")
		},
	})
	p := consenttest.NewPlan(t, "https://shop.test/", "https://shop.test/login")
	p.MapHosts, p.MapOnly, p.Runs, p.Retries = map[string]string{"shop.test": addr}, true, []consent.RunKind{consent.RunNone}, 0
	p.Settle, p.Browser.NoDownload = 200*time.Millisecond, true
	res, err := runCheck(t, p, &pageCMP{})
	var f *consent.FatalError
	if !errors.As(err, &f) || f.Code != consent.FatalBotWall || f.Detail != "Cloudflare" {
		t.Fatalf("Run = %v", err)
	}
	login := res.Pages[1].Runs[0]
	if login.Status != consent.StatusLoadFailed || login.Detail != "https://shop.test/login answered 401 Unauthorized" {
		t.Errorf("login run %s %q", login.Status, login.Detail)
	}
}

// tlsPlan is a plan for a 127.0.0.1 TLS server with a certificate no one
// trusts.
func tlsPlan(t *testing.T) *consent.Plan {
	t.Helper()
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		_, _ = fmt.Fprint(w, "<title>local</title><p>local")
	}))
	t.Cleanup(srv.Close)
	p := consenttest.NewPlan(t, srv.URL+"/")
	p.Runs, p.Retries, p.Settle, p.Browser.NoDownload = []consent.RunKind{consent.RunNone}, 0, 200*time.Millisecond, true
	p.BannerWait = 500 * time.Millisecond
	return p
}

// TestConsentSelfSignedRefused: a certificate this machine doesn't
// trust stops the check without -k.
func TestConsentSelfSignedRefused(t *testing.T) {
	f := fatalOf(t, tlsPlan(t))
	if f.Code != consent.FatalTLS || !strings.Contains(f.Detail, "CERT") {
		t.Errorf("fatal %+v", f)
	}
}

// TestInsecureLoopbackAllowed: with -k a loopback site with its own
// certificate is checked; the result says TLS was not verified.
func TestInsecureLoopbackAllowed(t *testing.T) {
	p := tlsPlan(t)
	p.Insecure = true
	res, err := runCheck(t, p, &pageCMP{})
	if err != nil {
		t.Fatal(err)
	}
	if res.TLSVerified || res.Pages[0].Runs[0].Status != consent.StatusOK {
		t.Errorf("tls_verified %v, run %+v", res.TLSVerified, res.Pages[0].Runs[0])
	}
}

// TestConsentUnreachable: a site nothing answers for stops the check
// with the guard's reason.
func TestConsentUnreachable(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	addr := ln.Addr().String()
	_ = ln.Close()
	p := consenttest.NewPlan(t, "http://"+addr+"/")
	p.Runs, p.Retries, p.Settle, p.Browser.NoDownload = []consent.RunKind{consent.RunNone}, 0, 200*time.Millisecond, true
	f := fatalOf(t, p)
	if f.Code != consent.FatalUnreachable || !strings.Contains(f.Detail, "refused") {
		t.Errorf("fatal %+v", f)
	}
}

// TestConsentRunsIsolated: Accept and Reject run at once; Reject starts
// with no cookie and sees the banner Accept closed in its own context.
func TestConsentRunsIsolated(t *testing.T) {
	st := shopSite(t, "")
	p := shopPlan(t, st, consent.RunAccept, consent.RunReject)
	p.Parallel = 2
	res, err := runCheck(t, p, &pageCMP{})
	if err != nil {
		t.Fatal(err)
	}
	reject := runOf(t, res.Pages[0], consent.RunReject)
	if !reject.Visits[0].Banner || reject.Choice == nil {
		t.Errorf("reject didn't see the banner: %+v", reject.Visits)
	}
	for _, c := range reject.Cookies {
		if c.Phase == consent.PhaseBefore || c.Name == "_ga" {
			t.Errorf("reject run has cookie %s in %s", c.Name, c.Phase)
		}
	}
	if accept := runOf(t, res.Pages[0], consent.RunAccept); !slices.ContainsFunc(accept.Cookies, func(c consent.Cookie) bool { return c.Name == "_ga" }) {
		t.Errorf("accept cookies %+v", accept.Cookies)
	}
}

// TestConsentClickNavigatesAway: an Accept that sends the tab to another
// host ends the run navigated-away.
func TestConsentClickNavigatesAway(t *testing.T) {
	st := shopSite(t, `<script>document.getElementById("accept").addEventListener("click", () => { location.href = "https://elsewhere.other.test/"; });</script>`)
	st.page("elsewhere.other.test/", "<title>elsewhere</title>")
	p := shopPlan(t, st, consent.RunAccept)
	res, err := runCheck(t, p, &pageCMP{})
	if err != nil {
		t.Fatal(err)
	}
	r := res.Pages[0].Runs[0]
	if r.Status != consent.StatusNavigatedAway || r.Reason != consent.ReasonNavigation || slices.Contains(steps(r), "reload") {
		t.Errorf("run %s %q steps %v", r.Status, r.Reason, steps(r))
	}
}

// TestRunTimeout: a page holding a request open past --timeout ends
// with timeout and names that request.
func TestRunTimeout(t *testing.T) {
	release := make(chan struct{})
	t.Cleanup(func() { close(release) })
	addr := redirectSite(t, map[string]http.HandlerFunc{
		"/": func(w http.ResponseWriter, _ *http.Request) {
			w.Header().Set("Content-Type", "text/html")
			_, _ = fmt.Fprint(w, `<title>slow</title><img src="/slow.gif?session=abc">`)
		},
		"/slow.gif": func(w http.ResponseWriter, r *http.Request) {
			select {
			case <-release:
			case <-r.Context().Done():
			}
		},
	})
	p := consenttest.NewPlan(t, "https://shop.test/")
	p.MapHosts, p.MapOnly, p.Runs, p.Retries = map[string]string{"shop.test": addr}, true, []consent.RunKind{consent.RunNone}, 0
	p.Settle, p.Timeout, p.Browser.NoDownload = 300*time.Millisecond, 5*time.Second, true
	began := time.Now()
	res, err := runCheck(t, p, &pageCMP{})
	if err != nil {
		t.Fatal(err)
	}
	r := res.Pages[0].Runs[0]
	if r.Status != consent.StatusTimeout || r.Reason != consent.ReasonTimeout ||
		!strings.HasPrefix(r.Detail, "the last request still open was https://shop.test/slow.gif") {
		t.Errorf("run %s %q %q", r.Status, r.Reason, r.Detail)
	}
	if d := time.Since(began); d > testenv.Timeout(20*time.Second) {
		t.Errorf("took %s", d)
	}
}

// TestNoBannerRuns: with no banner Accept and Reject say so and still
// visit and reload; the no-choice run isn't inconclusive. Naming the
// CMP makes it cmp_not_found.
func TestNoBannerRuns(t *testing.T) {
	st := shopSite(t, "")
	for _, cmp := range []string{"auto", "onetrust"} {
		p := shopPlan(t, st)
		p.CMP = cmp
		fake := &consenttest.FakeCMP{}
		res, err := runCheck(t, p, fake)
		if err != nil {
			t.Fatal(err)
		}
		want := consent.ReasonNoBanner
		if cmp != "auto" {
			want = consent.ReasonCMPNotFound
		}
		for _, r := range res.Pages[0].Runs {
			if !slices.Equal(steps(r), []string{"load", "page", "reload"}) {
				t.Errorf("%s %s: steps %v", cmp, r.Kind, steps(r))
			}
			switch r.Kind {
			case consent.RunNone:
				if r.Status != consent.StatusOK || r.Reason != consent.ReasonNone {
					t.Errorf("%s none: %s %q", cmp, r.Status, r.Reason)
				}
			default:
				if r.Status != consent.StatusNoBanner || r.Reason != want || r.Choice != nil {
					t.Errorf("%s %s: %s %q", cmp, r.Kind, r.Status, r.Reason)
				}
			}
		}
		if slices.ContainsFunc(fake.Calls, func(c string) bool { return strings.HasPrefix(c, "Choose") }) {
			t.Errorf("%s: Choose called without a banner: %v", cmp, fake.Calls)
		}
	}
}

// TestFinalState: after the reload Accept and Reject keep what the CMP
// says it stored; the no-choice run has none.
func TestFinalState(t *testing.T) {
	st := shopSite(t, "")
	p := shopPlan(t, st)
	stored := &consent.CMPState{CMP: "fake", Source: "cookie:fake", Decided: true, Categories: map[string]bool{"marketing": false}}
	fake := &consenttest.FakeCMP{Found: consent.CMPInfo{ID: "fake", Name: "Fake", Found: true, Present: true}, Stored: stored}
	res, err := runCheck(t, p, fake)
	if err != nil {
		t.Fatal(err)
	}
	for _, r := range res.Pages[0].Runs {
		want := stored
		if r.Kind == consent.RunNone {
			want = nil
		}
		if !reflect.DeepEqual(r.FinalState, want) {
			t.Errorf("%s: final state %+v", r.Kind, r.FinalState)
		}
	}
}

// TestDetectScreenshot: detect finds both controls and writes one
// screenshot named after the stamp.
func TestDetectScreenshot(t *testing.T) {
	st := shopSite(t, "")
	p := shopPlan(t, st)
	p.ID = "2026-10-26T09-14-03-a1b2c3"
	cmp := &pageCMP{}
	needChrome(t)
	consenttest.Use(t, consent.Stages{Classifier: &consenttest.FakeClassifier{}, Signals: consenttest.FakeSignals{}, CMP: cmp})
	res, err := Runner{}.Detect(context.Background(), p)
	if err != nil {
		t.Fatal(err)
	}
	if !res.CMP.Found || res.Accept == nil || res.Reject == nil || res.Accept.Clicks != 0 || res.Screenshot != p.ID+".png" || res.Browser == "" {
		t.Fatalf("detect %+v", res)
	}
	fi, err := os.Stat(filepath.Join(p.Dir, res.Screenshot))
	if err != nil || fi.Mode().Perm() != 0o600 {
		t.Errorf("screenshot %v %v", fi, err)
	}
	if slices.ContainsFunc(cmp.calls, func(c string) bool { return strings.HasPrefix(c, "Choose") }) {
		t.Errorf("detect clicked: %v", cmp.calls)
	}
	raw, _ := json.Marshal(res)
	if !json.Valid(raw) {
		t.Error("detect result isn't JSON")
	}
}

// TestRunFilesNDJSONAndHAR: each run streams its events to its NDJSON
// file, one line per event in memory; with --har it writes a HAR of
// every request with no login, cookie value, token or body in it.
func TestRunFilesNDJSONAndHAR(t *testing.T) {
	token := fakeToken()
	st := shopSite(t, `<script>document.cookie = "sid=`+token+`; path=/";
fetch("/api?debug=1", {method: "POST", headers: {"X-Api-Key": "`+token+`"}, body: "secret-body-`+token+`"});</script>`)
	st.page("shop.test/api", `{"ok":true}`, "Set-Cookie", "srv="+token+"; Path=/")
	p := shopPlan(t, st, consent.RunNone, consent.RunReject)
	p.HAR = true
	p.Headers = map[string]string{"Authorization": "Bearer " + token}
	res, err := runCheck(t, p, &pageCMP{})
	if err != nil {
		t.Fatal(err)
	}
	for _, r := range res.Pages[0].Runs {
		if r.EventsFile != "events/page-1-"+string(r.Kind)+".ndjson" || r.HARFile != "har/page-1-"+string(r.Kind)+".har" {
			t.Fatalf("%s: files %q %q", r.Kind, r.EventsFile, r.HARFile)
		}
		lines := readNDJSON(t, filepath.Join(p.Dir, r.EventsFile))
		if len(lines) != len(r.Events) || len(lines) == 0 {
			t.Errorf("%s: %d lines for %d events", r.Kind, len(lines), len(r.Events))
		}
		for i := range lines {
			if lines[i].Seq != r.Events[i].Seq || lines[i].Phase != r.Events[i].Phase || lines[i].Kind != r.Events[i].Kind {
				t.Errorf("%s: line %d is %+v; the run has %+v", r.Kind, i, lines[i], r.Events[i])
				break
			}
		}
		b, err := os.ReadFile(filepath.Join(p.Dir, r.HARFile))
		if err != nil {
			t.Fatal(err)
		}
		var har harFile
		if err := json.Unmarshal(b, &har); err != nil {
			t.Fatal(err)
		}
		requests := 0
		for _, e := range r.Events {
			if e.Kind == consent.EvRequest {
				requests++
			}
		}
		if len(har.Log.Entries) != requests || har.Log.Creator.Name != "rhythm-echo" {
			t.Errorf("%s: %d HAR entries for %d requests", r.Kind, len(har.Log.Entries), requests)
		}
		if strings.Contains(string(b), token) || strings.Contains(string(b), "secret-body") {
			t.Errorf("%s: the HAR holds the token or a body", r.Kind)
		}
		var api *harEntry
		for i := range har.Log.Entries {
			if strings.Contains(har.Log.Entries[i].Request.URL, "/api") {
				api = &har.Log.Entries[i]
			}
		}
		if api == nil || api.Request.Method != "POST" || api.Response.Status != 200 || len(api.Request.Headers) == 0 ||
			!slices.ContainsFunc(api.Response.Headers, func(h harPair) bool { return strings.EqualFold(h.Name, "set-cookie") && h.Value == "srv" }) {
			t.Errorf("%s: api entry %+v", r.Kind, api)
		}
	}
}
