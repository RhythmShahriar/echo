// Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
// SPDX-License-Identifier: MIT

package cmp

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/RhythmShahriar/echo/consent"
	"github.com/RhythmShahriar/echo/consent/consenttest"
)

// chooseSetup is the acme rule on a page with its banner showing, a
// state reader the test drives and the CLI's default plan. Tests using
// it swap the registry, so they don't run in parallel.
func chooseSetup(t *testing.T, rules map[string]string) (*driver, *fakePage, *consenttest.FakeState, *consent.Plan) {
	t.Helper()
	if rules == nil {
		rules = map[string]string{"acme.yaml": acmeRule}
	}
	d := testDriver(t, rules)
	pg := newFakePage()
	pg.drive(d)
	pg.banner("acme", true, "#acme-banner")
	fs := &consenttest.FakeState{}
	consenttest.Use(t, consent.Stages{State: fs})
	return d, pg, fs, consenttest.NewPlan(t)
}

// decide stores a choice with statistics as given and closes the banner.
func decide(pg *fakePage, fs *consenttest.FakeState, statistics bool) {
	fs.State = &consent.CMPState{CMP: "acme", Source: "cookie:acme_consent", Decided: true, Categories: map[string]bool{"statistics": statistics}}
	pg.banner("acme", false)
}

func clicked(pg *fakePage) []string {
	var out []string
	for _, l := range pg.tab.Clicked {
		out = append(out, l.CSS)
	}
	return out
}

var acme = consent.CMPInfo{ID: "acme", Name: "Acme CMP", Found: true, Present: true}

func TestChooseFirstLayer(t *testing.T) {
	for _, k := range []consent.RunKind{consent.RunAccept, consent.RunReject} {
		d, pg, fs, p := chooseSetup(t, nil)
		pg.show("#acme-accept", "Accept all")
		pg.show("#acme-reject", "Reject all")
		pg.tab.Clock = 3000
		read := 0
		pg.tab.OnClick = func(tab *consenttest.FakeTab, l consent.Locator) {
			read = len(fs.Specs)
			decide(pg, fs, l.CSS == "#acme-accept")
		}
		ch, reason, err := d.Choose(context.Background(), pg.tab, p, acme, k)
		if err != nil || reason != consent.ReasonNone {
			t.Fatalf("%s: reason %q, err %v", k, reason, err)
		}
		want := "#acme-" + string(k)
		if ch.How != "button" || ch.Selector != want || ch.Layer != 1 || ch.Clicks != 1 || !ch.Verified ||
			ch.VerifiedBy != "cookie:acme_consent" || ch.AtMS != 3000 || ch.Box == nil || ch.State == nil || ch.Fallback {
			t.Errorf("%s: choice = %+v", k, ch)
		}
		if ch.Text != want {
			// FakeTab reports the CSS as the clicked text.
			t.Errorf("%s: text %q", k, ch.Text)
		}
		if !slices.Equal(clicked(pg), []string{want}) {
			t.Errorf("%s: clicked %v", k, clicked(pg))
		}
		if read == 0 || fs.Specs[0].Parser != "cookiebot" || fs.Specs[0].Cookies[0] != "acme_consent" {
			t.Errorf("%s: state not read before the click: %+v", k, fs.Specs)
		}
	}
}

func TestChooseSecondLayer(t *testing.T) {
	d, pg, fs, p := chooseSetup(t, nil)
	pg.show("#acme-accept", "Accept all")
	pg.show("#acme-settings", "Settings")
	pg.tab.OnClick = func(tab *consenttest.FakeTab, l consent.Locator) {
		switch l.CSS {
		case "#acme-settings":
			pg.show("#acme-save", "Save")
			pg.toggles = []toggle{{Path: "#t-stats", On: true}, {Path: "#t-ads"}, {Path: "#t-needed", On: true, Disabled: true}, {On: true}}
			pg.sync()
		case "#t-stats":
			pg.toggles[0].On = false
			pg.sync()
		case "#acme-save":
			decide(pg, fs, pg.toggles[0].On)
		}
	}
	ch, reason, err := d.Choose(context.Background(), pg.tab, p, acme, consent.RunReject)
	if err != nil || reason != consent.ReasonNone {
		t.Fatalf("reason %q, err %v", reason, err)
	}
	if ch.Layer != 2 || ch.Selector != "#acme-settings >> #acme-save" || !ch.Verified || ch.Clicks != 1 {
		t.Fatalf("choice = %+v", ch)
	}
	if got := clicked(pg); !slices.Equal(got, []string{"#acme-settings", "#t-stats", "#acme-save"}) {
		t.Fatalf("clicked %v; want the opener, the one enabled toggle that was on, save", got)
	}
	if pg.tab.Now() < secondGrace.Milliseconds() {
		t.Errorf("opened the settings at %d ms, before the first layer had %s", pg.tab.Now(), secondGrace)
	}
}

func TestChooseSecondLayerNoSave(t *testing.T) {
	d, pg, _, p := chooseSetup(t, nil)
	pg.show("#acme-settings", "Settings")
	p.BannerWait = 2 * time.Second
	ch, reason, err := d.Choose(context.Background(), pg.tab, p, acme, consent.RunReject)
	if err != nil || reason != consent.ReasonNoButton || ch == nil || ch.Layer != 2 || ch.Verified {
		t.Fatalf("choice %+v, reason %q, err %v", ch, reason, err)
	}
}

func TestChooseStateUnchanged(t *testing.T) {
	for name, stored := range map[string]func(fs *consenttest.FakeState){
		"undecided": func(fs *consenttest.FakeState) {},
		"not rejected": func(fs *consenttest.FakeState) {
			fs.State = &consent.CMPState{Decided: true, Categories: map[string]bool{"statistics": true}}
		},
	} {
		d, pg, fs, p := chooseSetup(t, nil)
		pg.show("#acme-reject", "Reject all")
		pg.tab.OnClick = func(*consenttest.FakeTab, consent.Locator) {
			stored(fs)
			pg.banner("acme", false) // the banner hid: that is never a Reject
		}
		ch, reason, err := d.Choose(context.Background(), pg.tab, p, acme, consent.RunReject)
		if err != nil || reason != consent.ReasonStateUnchanged || ch.Verified || ch.Clicks != 1 {
			t.Errorf("%s: choice %+v, reason %q, err %v", name, ch, reason, err)
		}
	}
}

func TestChooseLateBinding(t *testing.T) {
	d, pg, fs, p := chooseSetup(t, nil)
	pg.show("#acme-accept", "Accept all")
	n := 0
	pg.tab.OnClick = func(*consenttest.FakeTab, consent.Locator) {
		if n++; n == 2 {
			decide(pg, fs, true)
		}
	}
	ch, reason, err := d.Choose(context.Background(), pg.tab, p, acme, consent.RunAccept)
	if err != nil || reason != consent.ReasonNone || !ch.Verified || ch.Clicks != 2 {
		t.Fatalf("choice %+v, reason %q, err %v", ch, reason, err)
	}
	if pg.tab.Now() < p.Settle.Milliseconds() {
		t.Errorf("retried at %d ms, before --settle %s", pg.tab.Now(), p.Settle)
	}
}

func TestChooseClickMissed(t *testing.T) {
	d, pg, _, p := chooseSetup(t, nil)
	pg.show("#acme-reject", "Reject all")
	pg.tab.Clicks = map[string]consent.ClickResult{"#acme-reject": {Covered: true, Detail: "covered by .overlay"}}
	ch, reason, err := d.Choose(context.Background(), pg.tab, p, acme, consent.RunReject)
	if err != nil || reason != consent.ReasonClickMissed || ch.Clicks != maxClicks || ch.Verified {
		t.Fatalf("overlay: choice %+v, reason %q, err %v", ch, reason, err)
	}

	// Clicked three times and nothing at all happened.
	d, pg, _, p = chooseSetup(t, nil)
	pg.show("#acme-reject", "Reject all")
	ch, reason, _ = d.Choose(context.Background(), pg.tab, p, acme, consent.RunReject)
	if reason != consent.ReasonClickMissed || ch.Clicks != maxClicks || len(pg.tab.Clicked) != maxClicks {
		t.Fatalf("no effect: choice %+v, reason %q", ch, reason)
	}

	// The control went away after a click that did nothing.
	d, pg, _, p = chooseSetup(t, nil)
	pg.show("#acme-reject", "Reject all")
	pg.tab.OnClick = func(*consenttest.FakeTab, consent.Locator) { pg.hide("#acme-reject") }
	ch, reason, _ = d.Choose(context.Background(), pg.tab, p, acme, consent.RunReject)
	if reason != consent.ReasonClickMissed || ch.Clicks != 1 {
		t.Fatalf("gone: choice %+v, reason %q", ch, reason)
	}
}

func TestChooseNoButton(t *testing.T) {
	d, pg, _, p := chooseSetup(t, nil)
	p.BannerWait = time.Second
	ch, reason, err := d.Choose(context.Background(), pg.tab, p, acme, consent.RunAccept)
	if err != nil || reason != consent.ReasonNoButton || ch != nil {
		t.Fatalf("choice %+v, reason %q, err %v", ch, reason, err)
	}
	if pg.tab.Now() < 1000 {
		t.Errorf("gave up at %d ms, before --banner-wait", pg.tab.Now())
	}

	// A control that is there but not one a visitor clicks: outside the
	// banner, a link off the site, disabled, covered or not a button.
	for name, change := range map[string]func(*probeResult){
		"outside":  func(p *probeResult) { p.Inside = false },
		"off-site": func(p *probeResult) { p.Offsite = true },
		"disabled": func(p *probeResult) { p.Disabled = true },
		"covered":  func(p *probeResult) { p.Top = false },
		"a div":    func(p *probeResult) { p.Kind = false },
		"hidden":   func(p *probeResult) { p.Visible = false },
	} {
		d, pg, _, p := chooseSetup(t, nil)
		p.BannerWait = 0
		pg.show("#acme-accept", "Accept")
		pr := pg.controls["#acme-accept"]
		change(&pr)
		pg.controls["#acme-accept"] = pr
		pg.sync()
		if _, reason, _ := d.Choose(context.Background(), pg.tab, p, acme, consent.RunAccept); reason != consent.ReasonNoButton || len(pg.tab.Clicked) != 0 {
			t.Errorf("%s: reason %q, clicked %v", name, reason, clicked(pg))
		}
	}
}

func TestChooseNavigatedAway(t *testing.T) {
	d, pg, _, p := chooseSetup(t, nil)
	pg.show("#acme-reject", "Reject all")
	pg.tab.Clicks = map[string]consent.ClickResult{"#acme-reject": {Clicked: true, NavigatedAway: true}}
	ch, reason, err := d.Choose(context.Background(), pg.tab, p, acme, consent.RunReject)
	if err != nil || reason != consent.ReasonNavigation || ch.Verified {
		t.Fatalf("choice %+v, reason %q, err %v", ch, reason, err)
	}
}

// apiState is a state reader that reports state once the page ran call.
type apiState struct {
	mu    sync.Mutex
	tab   *consenttest.FakeTab
	call  string
	state *consent.CMPState
}

func (s *apiState) Read(context.Context, consent.Tab, consent.VerifySpec) (*consent.CMPState, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, e := range s.tab.Evals {
		if e == s.call {
			return s.state, nil
		}
	}
	return nil, nil
}

func rejected() *consent.CMPState {
	return &consent.CMPState{CMP: "acme", Source: "cookie:acme_consent", Decided: true, Categories: map[string]bool{"statistics": false}}
}

func TestChooseAPI(t *testing.T) {
	d, pg, _, p := chooseSetup(t, nil)
	consenttest.Use(t, consent.Stages{State: &apiState{tab: pg.tab, call: "Acme.rejectAll()", state: rejected()}})
	pg.show("#acme-reject", "Reject all")
	p.Click = consent.ClickAPI
	ch, reason, err := d.Choose(context.Background(), pg.tab, p, acme, consent.RunReject)
	if err != nil || reason != consent.ReasonNone {
		t.Fatalf("reason %q, err %v", reason, err)
	}
	if ch.How != "api" || ch.Selector != "Acme.rejectAll()" || ch.Clicks != 0 || !ch.Verified || ch.VerifiedBy != "cookie:acme_consent" || len(pg.tab.Clicked) != 0 {
		t.Fatalf("choice = %+v, clicked %v", ch, clicked(pg))
	}

	// The CMP's function threw (or is missing): the call did nothing.
	pg.tab.Main["Acme.acceptAll()"] = consenttest.Answer{Err: errors.New("ReferenceError: Acme is not defined")}
	if _, reason, _ := d.Choose(context.Background(), pg.tab, p, acme, consent.RunAccept); reason != consent.ReasonClickMissed {
		t.Errorf("a failing call: reason %q", reason)
	}

	// A rule without an API under --click api.
	plain := strings.NewReplacer("  accept: Acme.acceptAll()\n", "", "  reject: Acme.rejectAll()\n", "").Replace(acmeRule)
	d2, pg2, _, p2 := chooseSetup(t, map[string]string{"acme.yaml": plain})
	p2.Click = consent.ClickAPI
	if ch, reason, _ := d2.Choose(context.Background(), pg2.tab, p2, acme, consent.RunReject); reason != consent.ReasonNoButton || ch != nil {
		t.Errorf("no API: choice %+v, reason %q", ch, reason)
	}
}

func TestChooseAPIStateUnchanged(t *testing.T) {
	d, pg, _, p := chooseSetup(t, nil)
	accepted := &consent.CMPState{Decided: true, Categories: map[string]bool{"statistics": true}}
	consenttest.Use(t, consent.Stages{State: &apiState{tab: pg.tab, call: "Acme.rejectAll()", state: accepted}})
	p.Click = consent.ClickAPI
	ch, reason, _ := d.Choose(context.Background(), pg.tab, p, acme, consent.RunReject)
	if reason != consent.ReasonStateUnchanged || ch.Verified {
		t.Fatalf("choice %+v, reason %q", ch, reason)
	}
	consenttest.Use(t, consent.Stages{State: &apiState{tab: pg.tab, call: "never"}})
	if _, reason, _ := d.Choose(context.Background(), pg.tab, p, acme, consent.RunReject); reason != consent.ReasonClickMissed {
		t.Fatalf("nothing stored: reason %q", reason)
	}
}

func TestChooseButtonAPIFallback(t *testing.T) {
	d, pg, _, p := chooseSetup(t, nil)
	consenttest.Use(t, consent.Stages{State: &apiState{tab: pg.tab, call: "Acme.rejectAll()", state: rejected()}})
	p.Click, p.BannerWait = consent.ClickButtonAPI, 2*time.Second
	ch, reason, err := d.Choose(context.Background(), pg.tab, p, acme, consent.RunReject)
	if err != nil || reason != consent.ReasonNone || ch.How != "api" || !ch.Fallback || !ch.Verified {
		t.Fatalf("choice %+v, reason %q, err %v", ch, reason, err)
	}
}

func TestChooseRejectBrokenAPIWorks(t *testing.T) {
	for _, works := range []bool{true, false} {
		d, pg, _, p := chooseSetup(t, nil)
		st := &apiState{tab: pg.tab, call: "Acme.rejectAll()"}
		if works {
			st.state = rejected()
		}
		consenttest.Use(t, consent.Stages{State: st})
		p.Click = consent.ClickButtonAPI
		pg.show("#acme-reject", "Reject all")
		pg.tab.OnClick = func(*consenttest.FakeTab, consent.Locator) { pg.banner("acme", false) }
		ch, reason, err := d.Choose(context.Background(), pg.tab, p, acme, consent.RunReject)
		if err != nil || reason != consent.ReasonStateUnchanged || ch.How != "button" || ch.Verified || ch.APIWorked != works || ch.Fallback {
			t.Errorf("api works %v: choice %+v, reason %q, err %v", works, ch, reason, err)
		}
	}
}

func TestChooseCustomSelectorWithoutRule(t *testing.T) {
	d, pg, _, p := chooseSetup(t, nil)
	steps, err := consent.ParseSelector("#cookie-bar .more >> #cookie-bar .reject")
	if err != nil {
		t.Fatal(err)
	}
	p.RejectSelector = steps
	pg.show("#cookie-bar .more", "More")
	pg.tab.OnClick = func(_ *consenttest.FakeTab, l consent.Locator) {
		switch l.CSS {
		case "#cookie-bar .more":
			pg.show("#cookie-bar .reject", "Reject")
		case "#cookie-bar .reject":
			pg.hide("#cookie-bar .reject")
		}
	}
	ch, reason, err := d.Choose(context.Background(), pg.tab, p, consent.CMPInfo{}, consent.RunReject)
	if err != nil || reason != consent.ReasonNone || !ch.Verified || ch.VerifiedBy != "banner" || ch.Selector != "#cookie-bar .more >> #cookie-bar .reject" || ch.Clicks != 1 {
		t.Fatalf("choice %+v, reason %q, err %v", ch, reason, err)
	}
	if got := clicked(pg); !slices.Equal(got, []string{"#cookie-bar .more", "#cookie-bar .reject"}) {
		t.Fatalf("clicked %v", got)
	}
	if _, reason, _ := d.Choose(context.Background(), pg.tab, &consent.Plan{}, consent.CMPInfo{}, consent.RunAccept); reason != consent.ReasonNoButton {
		t.Errorf("no rule, no selector: reason %q", reason)
	}
}

func TestChooseNeedsStateReader(t *testing.T) {
	d := testDriver(t, map[string]string{"acme.yaml": acmeRule})
	consenttest.Use(t, consent.Stages{})
	if consent.Linked().State != nil {
		t.Skip("a state reader is linked into this test binary")
	}
	_, _, err := d.Choose(context.Background(), newFakePage().tab, consenttest.NewPlan(t), acme, consent.RunAccept)
	if err == nil || err.Error() != "consent: state reader not linked into this build" {
		t.Fatalf("err = %v", err)
	}
}

func TestChooseRunKinds(t *testing.T) {
	d, pg, _, p := chooseSetup(t, nil)
	for _, k := range []consent.RunKind{consent.RunNone, consent.RunGPC, consent.RunWithdraw} {
		if _, _, err := d.Choose(context.Background(), pg.tab, p, acme, k); err == nil {
			t.Errorf("Choose %s: no error", k)
		}
		if _, _, err := d.Find(context.Background(), pg.tab, p, acme, k); err == nil {
			t.Errorf("Find %s: no error", k)
		}
	}
}

func TestChooseCanceled(t *testing.T) {
	d, pg, _, p := chooseSetup(t, nil)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, _, err := d.Choose(ctx, pg.tab, p, acme, consent.RunAccept); !errors.Is(err, context.Canceled) {
		t.Fatalf("err = %v", err)
	}
}

func TestFind(t *testing.T) {
	d, pg, _, p := chooseSetup(t, nil)
	pg.show("#acme-accept", "Accept all")
	pg.show("#acme-settings", "Settings")
	ch, reason, err := d.Find(context.Background(), pg.tab, p, acme, consent.RunAccept)
	if err != nil || reason != consent.ReasonNone {
		t.Fatal(reason, err)
	}
	if ch.How != "button" || ch.Selector != "#acme-accept" || ch.Text != "Accept all" || ch.Layer != 1 || ch.Box == nil || ch.Verified || ch.Clicks != 0 {
		t.Fatalf("accept = %+v", ch)
	}
	ch, _, _ = d.Find(context.Background(), pg.tab, p, acme, consent.RunReject)
	if ch == nil || ch.Layer != 2 || ch.Selector != "#acme-settings" || ch.Text != "Settings" {
		t.Fatalf("reject = %+v", ch)
	}
	if len(pg.tab.Clicked) != 0 {
		t.Fatalf("Find clicked %v", clicked(pg))
	}

	// Find picks what Choose then clicks.
	pg.tab.OnClick = func(*consenttest.FakeTab, consent.Locator) {}
	_, _, _ = d.Choose(context.Background(), pg.tab, p, acme, consent.RunAccept)
	if len(pg.tab.Clicked) == 0 || pg.tab.Clicked[0].CSS != "#acme-accept" {
		t.Fatalf("Choose clicked %v", clicked(pg))
	}

	p.Click = consent.ClickAPI
	if ch, _, _ := d.Find(context.Background(), pg.tab, p, acme, consent.RunReject); ch.How != "api" || ch.Selector != "Acme.rejectAll()" {
		t.Fatalf("api = %+v", ch)
	}
	p.Click, p.BannerWait = consent.ClickButtonAPI, 0
	pg.hide("#acme-accept")
	if ch, _, _ := d.Find(context.Background(), pg.tab, p, acme, consent.RunAccept); ch.How != "api" || !ch.Fallback {
		t.Fatalf("fallback = %+v", ch)
	}
	p.Click = consent.ClickButton
	if ch, reason, _ := d.Find(context.Background(), pg.tab, p, acme, consent.RunAccept); ch != nil || reason != consent.ReasonNoButton {
		t.Fatalf("none = %+v, %q", ch, reason)
	}
}

func TestVerifiedBy(t *testing.T) {
	r := &rule{Verify: consent.VerifySpec{Parser: "cookiebot", Cookies: []string{"CookieConsent"}}}
	for _, tc := range []struct {
		st   *consent.CMPState
		want string
	}{
		{nil, "banner"},
		{&consent.CMPState{Source: "banner"}, "banner"},
		{&consent.CMPState{Source: "api:Cookiebot.consent"}, "api"},
		{&consent.CMPState{Source: "tcf"}, "tcf"},
		{&consent.CMPState{Source: "cookie:CookieConsent"}, "cookie:CookieConsent"},
		{&consent.CMPState{}, "cookie:CookieConsent"},
	} {
		if got := verifiedBy(r, tc.st); got != tc.want {
			t.Errorf("%+v: %q; want %q", tc.st, got, tc.want)
		}
	}
	if !matches(&consent.CMPState{Decided: true}, map[string]bool{"marketing": false}) || matches(&consent.CMPState{Decided: true}, map[string]bool{"marketing": true}) {
		t.Error("a category the CMP doesn't list must read as not granted")
	}
}

// pageWrites are JavaScript that changes the page: removing or hiding a
// node, writing a style, an attribute or markup. Reading computed
// display and visibility is how a banner is judged; writing them is
// never how a choice is made.
var pageWrites = regexp.MustCompile(`\.remove\(|removeChild|\.style\b|style\s*=|setAttribute\(|innerHTML|outerHTML|\b(display|visibility|hidden|checked|value)\s*=[^=]|insertAdjacent|appendChild|\.click\(`)

func TestNeverHides(t *testing.T) {
	files, _ := filepath.Glob("js/*.js")
	if len(files) == 0 {
		t.Fatal("no scripts")
	}
	for _, f := range files {
		b, _ := os.ReadFile(f)
		for i, line := range strings.Split(string(b), "\n") {
			if m := pageWrites.FindString(line); m != "" && !strings.HasPrefix(strings.TrimSpace(line), "//") {
				t.Errorf("%s:%d writes to the page (%q): %s", f, i+1, m, strings.TrimSpace(line))
			}
		}
	}
	d, err := load(rulesFS, "rules")
	if err != nil {
		t.Fatal(err)
	}
	for _, r := range d.rules {
		for _, call := range []string{r.API.Accept, r.API.Reject, r.API.Reopen, r.API.State, r.Verify.StateJS} {
			if m := pageWrites.FindString(call); m != "" {
				t.Errorf("%s: %q writes to the page (%q)", r.ID, call, m)
			}
		}
	}
	for _, f := range []string{"choose.go", "detect.go", "probe.go", "scripts.go", "driver.go"} {
		b, _ := os.ReadFile(f)
		if regexp.MustCompile(`"[^"\n]*(\.remove\(|\.style|display\s*=|visibility\s*=)[^"\n]*"`).Match(b) {
			t.Errorf("%s holds JavaScript that hides or removes nodes", f)
		}
	}
	if !pageWrites.MatchString(`el.style.display = "none"`) || !pageWrites.MatchString(`banner.remove()`) || pageWrites.MatchString(`cs.display === "none"`) {
		t.Fatal("pageWrites doesn't tell writes from reads")
	}
}
