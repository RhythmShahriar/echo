// Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
// SPDX-License-Identifier: MIT

package signal

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"net/url"
	"strings"
	"testing"

	"github.com/RhythmShahriar/echo/consent"
	"github.com/RhythmShahriar/echo/consent/consenttest"
	"github.com/RhythmShahriar/echo/consent/signal/cmpstate"
)

// cookiebotValue is a CookieConsent cookie as Cookiebot sets it: quotes
// and commas URL-encoded.
func cookiebotValue(stamp string, granted bool) string {
	v := fmt.Sprintf("{stamp:'%s',necessary:true,preferences:%v,statistics:%v,marketing:%v,method:'explicit',ver:1}", stamp, granted, granted, granted)
	return strings.NewReplacer("'", "%27", ",", "%2C").Replace(v)
}

var cookiebotSpec = consent.VerifySpec{Parser: "cookiebot", Cookies: []string{"CookieConsent"}}

func TestStateReaderRegistered(t *testing.T) {
	if _, ok := consent.Linked().State.(StateReader); !ok {
		t.Fatalf("state reader %T", consent.Linked().State)
	}
}

func TestStateAcceptThenReject(t *testing.T) {
	ctx := context.Background()
	tab := &consenttest.FakeTab{}
	if st, err := (StateReader{}).Read(ctx, tab, cookiebotSpec); st != nil || err != nil {
		t.Fatalf("before a choice: %+v, %v", st, err)
	}
	tab.SetCookie(consent.Cookie{Name: "CookieConsent"}, cookiebotValue("tbStamp1", true))
	st, err := StateReader{}.Read(ctx, tab, cookiebotSpec)
	want := map[string]bool{"necessary": true, "preferences": true, "statistics": true, "marketing": true}
	if err != nil || !st.Decided || st.CMP != "cookiebot" || st.Source != "cookie:CookieConsent" || !maps.Equal(st.Categories, want) {
		t.Fatalf("accepted: %+v, %v", st, err)
	}
	tab.SetCookie(consent.Cookie{Name: "CookieConsent"}, cookiebotValue("tbStamp1", false))
	st, err = StateReader{}.Read(ctx, tab, cookiebotSpec)
	want = map[string]bool{"necessary": true, "preferences": false, "statistics": false, "marketing": false}
	if err != nil || !st.Decided || !maps.Equal(st.Categories, want) {
		t.Fatalf("rejected: %+v, %v", st, err)
	}
}

func TestStatePrefix(t *testing.T) {
	tab := &consenttest.FakeTab{}
	tab.SetCookie(consent.Cookie{Name: "_iub_cs"}, "not this one")
	tab.SetCookie(consent.Cookie{Name: "_iub_cs-12345"}, url.QueryEscape(`{"purposes":{"1":true,"4":false,"5":false},"id":12345}`))
	st, err := StateReader{}.Read(context.Background(), tab, consent.VerifySpec{Parser: "iubenda", Cookies: []string{"_iub_cs-*"}})
	if err != nil || !st.Decided || st.Source != "cookie:_iub_cs-12345" || !maps.Equal(st.Categories, map[string]bool{"1": true, "4": false, "5": false}) {
		t.Fatalf("%+v, %v", st, err)
	}
	// At most 10 cookies are read for one prefix.
	for i := range 15 {
		tab.SetCookie(consent.Cookie{Name: fmt.Sprintf("cmplz_c%02d", i)}, "deny")
	}
	st, err = StateReader{}.Read(context.Background(), tab, consent.VerifySpec{Parser: "complianz", Cookies: []string{"cmplz_*"}})
	if err != nil || !st.Decided || len(st.Categories) != maxPrefixCookies {
		t.Fatalf("complianz: %+v, %v", st, err)
	}
}

func TestStateDefaultsAndStorage(t *testing.T) {
	tab := &consenttest.FakeTab{Storage: map[string]string{
		"uc_settings":         `{"services":[{"id":"a","name":"Google Analytics","status":false,"history":[{"type":"explicit"}]}]}`,
		"uc_user_interaction": "true",
	}}
	st, err := StateReader{}.Read(context.Background(), tab, consent.VerifySpec{Parser: "usercentrics"})
	if err != nil || !st.Decided || st.Source != "storage:uc_settings" || st.Categories["Google Analytics"] {
		t.Fatalf("usercentrics: %+v, %v", st, err)
	}
	tab.SetCookie(consent.Cookie{Name: "euconsent-v2"}, encodeTC(tcSpec{version: 2, cmp: 7, consent: []int{1, 3}, maxVendor: 2, vendors: []int{2}}))
	st, err = StateReader{}.Read(context.Background(), tab, consent.VerifySpec{Parser: "tcf"})
	if err != nil || !st.Decided || st.Source != "tcf" || st.TCF == nil || st.TCF.CMPID != 7 || st.TCF.VendorsConsent != 1 {
		t.Fatalf("tcf: %+v, %v", st, err)
	}
}

func TestStateAPI(t *testing.T) {
	ctx := context.Background()
	quoted := func(s string) consenttest.Answer { b, _ := json.Marshal(s); return consenttest.Answer{Result: b} }
	spec := cookiebotSpec
	spec.StateJS = "JSON.stringify(Cookiebot.consent)"

	// The API overrides an undecided cookie.
	tab := &consenttest.FakeTab{Main: map[string]consenttest.Answer{spec.StateJS: quoted(
		`{"stamp":"tbStamp1","necessary":true,"preferences":false,"statistics":false,"marketing":false,"method":"explicit"}`)}}
	tab.SetCookie(consent.Cookie{Name: "CookieConsent"}, "{stamp:'',necessary:true,method:''}")
	st, err := StateReader{}.Read(ctx, tab, spec)
	if err != nil || !st.Decided || st.Source != "api" || st.Categories["marketing"] {
		t.Fatalf("api over undecided cookie: %+v, %v", st, err)
	}

	// An undecided API state leaves a decided cookie in place.
	tab.Main[spec.StateJS] = quoted(`{"stamp":"0","necessary":true,"method":null}`)
	tab.SetCookie(consent.Cookie{Name: "CookieConsent"}, cookiebotValue("tbStamp1", true))
	st, err = StateReader{}.Read(ctx, tab, spec)
	if err != nil || !st.Decided || st.Source != "cookie:CookieConsent" {
		t.Fatalf("decided cookie: %+v, %v", st, err)
	}

	// No cookie: the undecided API state is what there is.
	tab = &consenttest.FakeTab{Main: map[string]consenttest.Answer{spec.StateJS: quoted(`{"stamp":"0","necessary":true,"method":null}`)}}
	st, err = StateReader{}.Read(ctx, tab, spec)
	if err != nil || st == nil || st.Decided || st.Source != "api" {
		t.Fatalf("api only: %+v, %v", st, err)
	}

	// A failing script or an oversized result is ignored.
	tab.Main[spec.StateJS] = consenttest.Answer{Err: errors.New("ReferenceError: Cookiebot is not defined")}
	if st, err := (StateReader{}).Read(ctx, tab, spec); st != nil || err != nil {
		t.Fatalf("failing script: %+v, %v", st, err)
	}
	tab.Main[spec.StateJS] = quoted(strings.Repeat("x", maxStateJS))
	if st, err := (StateReader{}).Read(ctx, tab, spec); st != nil || err != nil {
		t.Fatalf("oversized: %+v, %v", st, err)
	}

	// A canceled context is an error.
	cctx, cancel := context.WithCancel(ctx)
	cancel()
	if _, err := (StateReader{}).Read(cctx, tab, spec); !errors.Is(err, context.Canceled) {
		t.Fatalf("canceled: %v", err)
	}
}

func TestStateBannerAndUnknown(t *testing.T) {
	st, err := StateReader{}.Read(context.Background(), &consenttest.FakeTab{}, consent.VerifySpec{Parser: "banner"})
	if err != nil || st == nil || st.Decided || st.Source != "banner" {
		t.Fatalf("banner: %+v, %v", st, err)
	}
	_, err = StateReader{}.Read(context.Background(), &consenttest.FakeTab{}, consent.VerifySpec{Parser: "x"})
	if err == nil || err.Error() != `consent: no state parser "x"` {
		t.Fatalf("unknown: %v", err)
	}
	// Every parser a rule may name is known.
	for _, name := range []string{"cookiebot", "onetrust", "cookieyes", "complianz", "usercentrics", "borlabs", "didomi", "iubenda", "tcf"} {
		if _, ok := parser(name); !ok {
			t.Errorf("no parser %q", name)
		}
		if _, ok := cmpstate.Defaults[name]; !ok {
			t.Errorf("no default names for %q", name)
		}
	}
}

// TestNoValueLeaks stores a secret-looking value (assembled at run time)
// in every CMP's cookie (readable or not), both as a value and as a
// category name. It must appear in no state and no error.
func TestNoValueLeaks(t *testing.T) {
	secret := "sk_" + "live_" + strings.Repeat("Q7xZ", 8)
	cases := []struct {
		spec  consent.VerifySpec
		value string
	}{
		{cookiebotSpec, cookiebotValue(secret, true)},
		{cookiebotSpec, secret},
		{consent.VerifySpec{Parser: "onetrust"}, "consentId=" + secret + "&interactionCount=1&groups=C0001:1"},
		{consent.VerifySpec{Parser: "onetrust"}, secret},
		{consent.VerifySpec{Parser: "cookieyes"}, "consentid:" + secret + ",action:yes,necessary:yes"},
		{consent.VerifySpec{Parser: "cookieyes"}, "consentid:x," + secret + ":yes,action:yes"},
		{consent.VerifySpec{Parser: "onetrust"}, "interactionCount=1&groups=" + secret + ":1"},
		{consent.VerifySpec{Parser: "borlabs"}, `{"consents":{"` + secret + `":["x"]}}`},
		{consent.VerifySpec{Parser: "borlabs"}, `{"consents":{"essential":["x"]},"uid":"` + secret + `"}`},
		{consent.VerifySpec{Parser: "borlabs"}, secret},
		{consent.VerifySpec{Parser: "didomi"}, secret},
		{consent.VerifySpec{Parser: "tcf"}, secret},
	}
	for _, c := range cases {
		tab := &consenttest.FakeTab{}
		for _, n := range cmpstate.Defaults[c.spec.Parser].Cookies {
			tab.SetCookie(consent.Cookie{Name: n}, c.value)
		}
		st, err := StateReader{}.Read(context.Background(), tab, c.spec)
		out, _ := json.Marshal(st)
		if strings.Contains(string(out), secret) || err != nil && strings.Contains(err.Error(), secret) {
			t.Errorf("%s: the value leaked: %s, %v", c.spec.Parser, out, err)
		}
		if st == nil {
			t.Errorf("%s: no state", c.spec.Parser)
		}
		if c.value == secret && (err == nil || !errors.Is(err, cmpstate.ErrUnreadable) || st.Decided) {
			t.Errorf("%s: garbage gave %+v, %v", c.spec.Parser, st, err)
		}
	}
}
