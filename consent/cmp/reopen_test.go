// Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
// SPDX-License-Identifier: MIT

package cmp

import (
	"context"
	"errors"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/RhythmShahriar/echo/consent"
	"github.com/RhythmShahriar/echo/consent/consenttest"
)

// acmeRevisit is acme with a floating "cookie settings" button.
var acmeRevisit = strings.Replace(acmeRule, "reopen:\n  api: reopen\n", "reopen:\n  selectors: [\"#acme-revisit\"]\n  api: reopen\n", 1)

// mainCalls lists the main-world calls the tab made, the globals check
// left out.
func mainCalls(pg *fakePage) []string {
	var out []string
	for _, e := range pg.tab.Evals {
		if !strings.HasPrefix(e, "cmp-") {
			out = append(out, e)
		}
	}
	return out
}

func TestReopenByButton(t *testing.T) {
	for _, mode := range []consent.ClickMode{consent.ClickButton, consent.ClickButtonAPI} {
		d, pg, _, p := chooseSetup(t, map[string]string{"acme.yaml": acmeRevisit})
		p.Click = mode
		pg.at(2000, func(pg *fakePage) { pg.show("#acme-revisit", "Cookie settings") })
		if err := d.Reopen(context.Background(), pg.tab, p, acme); err != nil {
			t.Fatalf("%s: %v", mode, err)
		}
		if got := clicked(pg); !slices.Equal(got, []string{"#acme-revisit"}) {
			t.Errorf("%s: clicked %v", mode, got)
		}
		if calls := mainCalls(pg); len(calls) != 0 {
			t.Errorf("%s: called %v with a button there", mode, calls)
		}
		if pg.tab.Now() < 2000 {
			t.Errorf("%s: gave up waiting at %d ms", mode, pg.tab.Now())
		}
	}
}

func TestReopenByAPI(t *testing.T) {
	// No button: the API only with button,api.
	d, pg, _, p := chooseSetup(t, map[string]string{"acme.yaml": acmeRevisit})
	p.Click, p.BannerWait = consent.ClickButtonAPI, time.Second
	if err := d.Reopen(context.Background(), pg.tab, p, acme); err != nil {
		t.Fatal(err)
	}
	if calls := mainCalls(pg); !slices.Equal(calls, []string{"Acme.show()"}) || len(pg.tab.Clicked) != 0 {
		t.Fatalf("calls %v, clicks %v", calls, clicked(pg))
	}

	// --click api calls it first, whatever shows.
	d, pg, _, p = chooseSetup(t, map[string]string{"acme.yaml": acmeRevisit})
	p.Click = consent.ClickAPI
	pg.show("#acme-revisit", "Cookie settings")
	if err := d.Reopen(context.Background(), pg.tab, p, acme); err != nil {
		t.Fatal(err)
	}
	if calls := mainCalls(pg); !slices.Equal(calls, []string{"Acme.show()"}) || len(pg.tab.Clicked) != 0 {
		t.Fatalf("api: calls %v, clicks %v", calls, clicked(pg))
	}

	// A call that throws is an error naming the CMP and the call.
	d, pg, _, p = chooseSetup(t, nil)
	p.Click = consent.ClickAPI
	pg.tab.Main["Acme.show()"] = consenttest.Answer{Err: errors.New("Acme is not defined")}
	err := d.Reopen(context.Background(), pg.tab, p, acme)
	if err == nil || err.Error() != "consent: Acme CMP's reopen call Acme.show() failed: Acme is not defined" {
		t.Fatalf("err = %v", err)
	}
}

func TestReopenRefused(t *testing.T) {
	d, err := load(rulesFS, "rules")
	if err != nil {
		t.Fatal(err)
	}
	pg := newFakePage()
	pg.drive(d)
	p := consenttest.NewPlan(t)
	cookiebot := consent.CMPInfo{ID: "cookiebot", Name: "Cookiebot", Found: true, Present: true}
	err = d.Reopen(context.Background(), pg.tab, p, cookiebot)
	if err == nil || err.Error() != "consent: Cookiebot has no way to reopen the banner without its API; --click button,api allows it" {
		t.Fatalf("err = %v", err)
	}
	if len(pg.tab.Evals) != 0 || len(pg.tab.Clicked) != 0 {
		t.Fatalf("a refused reopen touched the page: %v %v", pg.tab.Evals, clicked(pg))
	}

	for _, tc := range []struct {
		name, rules string
		info        consent.CMPInfo
		want        string
	}{
		{"button not there", acmeRevisit, acme,
			"consent: Acme CMP showed no button to reopen the banner (#acme-revisit); --click button,api allows its API"},
		{"button only", strings.NewReplacer("  api: reopen\n", "", "  reopen: Acme.show()\n", "").Replace(acmeRevisit), acme,
			"consent: Acme CMP showed no button to reopen the banner (#acme-revisit)"},
		{"no reopen", strings.NewReplacer("reopen:\n  api: reopen\n", "", "  reopen: Acme.show()\n", "").Replace(acmeRule), acme,
			"consent: Acme CMP has no known way to reopen the banner"},
		{"no rule", acmeRule, consent.CMPInfo{Name: "custom"},
			"consent: custom has no known way to reopen the banner"},
		{"nothing known", acmeRule, consent.CMPInfo{},
			"consent: this banner has no known way to reopen the banner"},
	} {
		d, pg, _, p := chooseSetup(t, map[string]string{"acme.yaml": tc.rules})
		p.BannerWait = time.Second
		err := d.Reopen(context.Background(), pg.tab, p, tc.info)
		if err == nil || err.Error() != tc.want {
			t.Errorf("%s: err = %v\nwant %s", tc.name, err, tc.want)
		}
		if len(mainCalls(pg)) != 0 || len(pg.tab.Clicked) != 0 {
			t.Errorf("%s: touched the page", tc.name)
		}
	}
}

func TestReopenClickFails(t *testing.T) {
	for res, want := range map[consent.ClickResult]string{
		{NavigatedAway: true}: "consent: Acme CMP's reopen button #acme-revisit left the page",
		{Covered: true}:       "consent: Acme CMP's reopen button #acme-revisit couldn't be clicked",
	} {
		d, pg, _, p := chooseSetup(t, map[string]string{"acme.yaml": acmeRevisit})
		pg.show("#acme-revisit", "Cookie settings")
		pg.tab.Clicks = map[string]consent.ClickResult{"#acme-revisit": res}
		if err := d.Reopen(context.Background(), pg.tab, p, acme); err == nil || err.Error() != want {
			t.Errorf("err = %v; want %s", err, want)
		}
	}
	d, pg, _, p := chooseSetup(t, map[string]string{"acme.yaml": acmeRevisit})
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := d.Reopen(ctx, pg.tab, p, acme); !errors.Is(err, context.Canceled) {
		t.Fatalf("canceled: %v", err)
	}
}

// TestWithdrawIntoOpenSettings is the withdraw run on a CMP whose reopen
// opens the preference center itself: Reject goes straight to its
// toggles and Save, with no opener to click.
func TestWithdrawIntoOpenSettings(t *testing.T) {
	d, pg, fs, p := chooseSetup(t, nil)
	p.Click = consent.ClickButtonAPI
	pg.banner("acme", false)
	fs.State = &consent.CMPState{CMP: "acme", Decided: true, Categories: map[string]bool{"statistics": true}}
	pg.tab.Main["Acme.show()"] = consenttest.Answer{Result: []byte("null")}
	if err := d.Reopen(context.Background(), pg.tab, p, acme); err != nil {
		t.Fatal(err)
	}
	// What Acme.show() drew: the settings, no banner.
	pg.show("#acme-save", "Save")
	pg.toggles = []toggle{{Path: "#t-stats", On: true}}
	pg.sync()
	pg.tab.OnClick = func(tab *consenttest.FakeTab, l consent.Locator) {
		switch l.CSS {
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
	if ch.Layer != 2 || ch.Selector != "#acme-save" || !ch.Verified || ch.How != "button" || ch.Fallback {
		t.Fatalf("choice = %+v", ch)
	}
	if got := clicked(pg); !slices.Equal(got, []string{"#t-stats", "#acme-save"}) {
		t.Fatalf("clicked %v", got)
	}
}

func TestStateAfterReject(t *testing.T) {
	d, pg, fs, _ := chooseSetup(t, nil)
	decide(pg, fs, false)
	st, err := d.State(context.Background(), pg.tab, acme)
	if err != nil || st == nil || !st.Decided || st.Categories["statistics"] || st.Source != "cookie:acme_consent" {
		t.Fatalf("state = %+v, %v", st, err)
	}
	if len(fs.Specs) != 1 || fs.Specs[0].Parser != "cookiebot" || fs.Specs[0].Cookies[0] != "acme_consent" || fs.Specs[0].StateJS != "JSON.stringify(Acme.state)" {
		t.Fatalf("read with %+v", fs.Specs)
	}
	// No rule: nothing to read.
	if st, err := d.State(context.Background(), pg.tab, consent.CMPInfo{}); st != nil || err != nil {
		t.Fatalf("no rule: %+v, %v", st, err)
	}
	// A read that fails is an error, not a decided state.
	fs.State, fs.Err = nil, errors.New("cookie unreadable")
	if st, err := d.State(context.Background(), pg.tab, acme); st != nil || err == nil {
		t.Fatalf("failed read: %+v, %v", st, err)
	}
}

func TestStateGenericFromBanner(t *testing.T) {
	d, err := load(rulesFS, "rules")
	if err != nil {
		t.Fatal(err)
	}
	pg := newFakePage()
	info := consent.CMPInfo{ID: genericID, Name: "Generic banner"}
	for _, showing := range []bool{true, false} {
		pg.generic = nil
		if showing {
			pg.generic = []genContainer{cookieBar("Accept all", "Reject all")}
		}
		pg.sync()
		got, err := d.BannerShowing(context.Background(), pg.tab, info)
		if err != nil || got != showing {
			t.Errorf("BannerShowing = %v, %v; want %v", got, err, showing)
		}
		st, err := d.State(context.Background(), pg.tab, info)
		if err != nil || st == nil || st.Source != "banner" || st.Decided == showing || st.CMP != genericID {
			t.Errorf("showing %v: state = %+v, %v", showing, st, err)
		}
	}
}

func TestGeoParam(t *testing.T) {
	d, err := load(rulesFS, "rules")
	if err != nil {
		t.Fatal(err)
	}
	// What consent/browse asserts the driver to.
	g, ok := consent.Linked().CMP.(interface {
		GeoParam(string, string) (string, string)
	})
	if !ok {
		t.Fatal("the linked driver has no GeoParam")
	}
	for _, tc := range []struct{ cmp, country, param, value string }{
		{"onetrust", "de", "otgeo", "de"},
		{"onetrust", "DE", "otgeo", "de"},
		{"onetrust", " fr ", "otgeo", "fr"},
		{"onetrust", "", "", ""},
		{"onetrust", "deu", "", ""},
		{"onetrust", "d1", "", ""},
		{"onetrust", "de&x=1", "", ""},
		{"cookiebot", "de", "", ""},
		{"generic", "de", "", ""},
		{"nope", "de", "", ""},
	} {
		for _, gp := range []interface {
			GeoParam(string, string) (string, string)
		}{d, g} {
			param, value := gp.GeoParam(tc.cmp, tc.country)
			if param != tc.param || value != tc.value {
				t.Errorf("GeoParam(%q, %q) = %q, %q; want %q, %q", tc.cmp, tc.country, param, value, tc.param, tc.value)
			}
		}
	}
}

func TestReopenData(t *testing.T) {
	// Every rule except generic has a way back to the banner: the
	// withdraw run relies on it.
	d, err := load(rulesFS, "rules")
	if err != nil {
		t.Fatal(err)
	}
	for _, r := range d.rules {
		if r.ID == genericID {
			continue
		}
		if len(r.ReopenSel) == 0 && r.ReopenAPI == "" {
			t.Errorf("%s: no reopen selectors or api", r.ID)
		}
		if r.ReopenAPI != "" && r.API.Reopen == "" {
			t.Errorf("%s: reopen.api set without api.reopen", r.ID)
		}
	}
	if ot := d.byID["onetrust"]; len(ot.ReopenSel) == 0 || ot.ReopenSel[0].Raw != "#ot-sdk-btn" {
		t.Errorf("onetrust reopen = %+v", ot.ReopenSel)
	}
}
