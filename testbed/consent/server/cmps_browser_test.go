// Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
// SPDX-License-Identifier: MIT

//go:build browser

package server

import (
	"encoding/base64"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/go-rod/rod"
	"github.com/go-rod/rod/lib/proto"
)

const cmpSite = "https://cmptest.shop.test"

// cmpCase is one replica: where its buttons are and what its stored
// choice reads like after each.
type cmpCase struct {
	path   string
	banner string // a JS expression, true while the banner shows
	accept string // a JS expression for the Accept element
	reject string
	// state returns the stored choice as text; accepted and rejected
	// check it.
	state            string
	accepted         func(string) bool
	rejected         func(string) bool
	viaJS            bool // the button is in a shadow root: clicked from JS
	noConsentUpdates bool // the CMP sends no Consent Mode update itself
}

func visibleJS(sel string) string {
	return `(() => { const e = document.querySelector('` + sel + `'); return !!e && e.offsetWidth > 0 && e.offsetHeight > 0; })()`
}

func cookieJS(name string) string {
	return `(() => { const m = document.cookie.split('; ').find(c => c.startsWith('` + name + `=')); return m ? decodeURIComponent(m.slice(` + strconv.Itoa(len(name)+1) + `)) : ''; })()`
}

func contains(subs ...string) func(string) bool {
	return func(s string) bool {
		for _, x := range subs {
			if !strings.Contains(s, x) {
				return false
			}
		}
		return true
	}
}

var cmpCases = []cmpCase{
	{path: "/cookiebot/", banner: visibleJS("#CybotCookiebotDialog"),
		accept: `document.querySelector('#CybotCookiebotDialogBodyLevelButtonLevelOptinAllowAll')`,
		reject: `document.querySelector('#CybotCookiebotDialogBodyButtonDecline')`,
		state:  cookieJS("CookieConsent"), accepted: contains("statistics:true", "marketing:true", "method:'explicit'"),
		rejected: contains("statistics:false", "marketing:false", "stamp:'")},
	{path: "/onetrust/", banner: visibleJS("#onetrust-banner-sdk"),
		accept:   `document.querySelector('#onetrust-accept-btn-handler')`,
		reject:   `document.querySelector('#onetrust-reject-all-handler')`,
		state:    `(` + cookieJS("OptanonConsent") + `) + '|' + (` + cookieJS("OptanonAlertBoxClosed") + `)`,
		accepted: contains("groups=C0001:1,C0002:1,C0003:1,C0004:1", "interactionCount=1", "|20"),
		rejected: contains("groups=C0001:1,C0002:0,C0003:0,C0004:0", "|20")},
	{path: "/usercentrics/", viaJS: true,
		banner:   `(() => { const r = document.getElementById('usercentrics-root'); const b = r && r.shadowRoot && r.shadowRoot.querySelector('[data-testid=uc-default-banner]'); return !!b && b.offsetWidth > 0; })()`,
		accept:   `document.getElementById('usercentrics-root').shadowRoot.querySelector('[data-testid=uc-accept-all-button]')`,
		reject:   `document.getElementById('usercentrics-root').shadowRoot.querySelector('[data-testid=uc-deny-all-button]')`,
		state:    `localStorage.getItem('uc_user_interaction') + '|' + localStorage.getItem('uc_settings')`,
		accepted: contains(`true|`, `"name":"Google Analytics","status":true`, `"type":"explicit"`),
		rejected: contains(`true|`, `"name":"Google Analytics","status":false`)},
	{path: "/didomi/", banner: visibleJS("#didomi-notice"),
		accept:   `document.querySelector('#didomi-notice-agree-button')`,
		reject:   `document.querySelector('#didomi-notice-disagree-button')`,
		state:    `(` + cookieJS("didomi_token") + `) + '|' + (` + cookieJS("euconsent-v2") + `)`,
		accepted: func(s string) bool { return didomiPurposes(s, true) && tcPurposes(s) == "1,2,3,4,5,6,7,8,9,10" },
		rejected: func(s string) bool { return didomiPurposes(s, false) && tcPurposes(s) == "" }},
	{path: "/didomi-c19/", banner: visibleJS("#didomi-notice"),
		accept:   `document.querySelector('#didomi-notice-agree-button')`,
		reject:   `document.querySelector('#didomi-notice-disagree-button')`,
		state:    `(` + cookieJS("didomi_token") + `) + '|' + (` + cookieJS("euconsent-v2") + `)`,
		accepted: func(s string) bool { return tcPurposes(s) == "1,2,3,4,5,6,7,8,9,10" },
		// The c19 bug: Disagree still grants purposes 1 and 3 in the TC string.
		rejected: func(s string) bool { return didomiPurposes(s, false) && tcPurposes(s) == "1,3" }},
	{path: "/iubenda/", banner: visibleJS("#iubenda-cs-banner"),
		accept: `document.querySelector('.iubenda-cs-accept-btn')`,
		reject: `document.querySelector('.iubenda-cs-reject-btn')`,
		state:  cookieJS("_iub_cs-12345678"), accepted: contains(`"4":true`, `"5":true`, `"id":12345678`), rejected: contains(`"1":true`, `"4":false`, `"5":false`)},
	{path: "/borlabs/", banner: visibleJS("#BorlabsCookieBox ._brlbs-box-wrap"),
		accept: `document.querySelector('[data-cookie-accept-all]')`,
		reject: `document.querySelector('[data-cookie-refuse]')`,
		state:  cookieJS("borlabs-cookie"), accepted: contains(`"statistics":["google-analytics"]`, `"uid":"tb`), rejected: func(s string) bool {
			return strings.Contains(s, `"essential":["borlabs-cookie"]`) && !strings.Contains(s, "statistics")
		}},
	{path: "/borlabs3/", banner: visibleJS("#BorlabsCookieBox .brlbs-cmpnt-dialog"),
		accept: `document.querySelector('[data-borlabs-cookie-actions=accept-all]')`,
		reject: `document.querySelector('[data-borlabs-cookie-actions=reject]')`,
		state:  cookieJS("borlabs-cookie"), accepted: contains(`"marketing":["facebook-pixel","google-ads"]`), rejected: func(s string) bool {
			return strings.Contains(s, `"uid":"tb`) && !strings.Contains(s, "marketing")
		}},
	{path: "/complianz/", banner: visibleJS(".cmplz-cookiebanner"),
		accept:   `document.querySelector('.cmplz-accept')`,
		reject:   `document.querySelector('.cmplz-deny')`,
		state:    `document.cookie.split('; ').filter(c => c.startsWith('cmplz_')).sort().join(';')`,
		accepted: contains("cmplz_banner-status=dismissed", "cmplz_marketing=allow", "cmplz_statistics=allow"),
		rejected: contains("cmplz_banner-status=dismissed", "cmplz_marketing=deny", "cmplz_statistics=deny", "cmplz_functional=allow")},
	{path: "/cookieyes/", banner: visibleJS(".cky-consent-container"),
		accept: `document.querySelector('[data-cky-tag=accept-button]')`,
		reject: `document.querySelector('[data-cky-tag=reject-button]')`,
		state:  cookieJS("cookieyes-consent"), accepted: contains("consent:yes", "action:yes", "analytics:yes", "advertisement:yes"),
		rejected: contains("consent:no", "action:yes", "necessary:yes", "analytics:no", "advertisement:no")},
	{path: "/generic-en/", banner: visibleJS("#site-cookie-banner"),
		accept: `[...document.querySelectorAll('#site-cookie-banner button')].find(b => b.textContent === 'Accept all')`,
		reject: `[...document.querySelectorAll('#site-cookie-banner button')].find(b => b.textContent === 'Reject all')`,
		state:  cookieJS("site_consent"), accepted: contains("accepted"), rejected: contains("rejected")},
	{path: "/generic-de/", banner: visibleJS("#site-cookie-banner"),
		accept: `[...document.querySelectorAll('#site-cookie-banner button')].find(b => b.textContent === 'Alle akzeptieren')`,
		reject: `[...document.querySelectorAll('#site-cookie-banner button')].find(b => b.textContent === 'Alle ablehnen')`,
		state:  cookieJS("site_consent"), accepted: contains("accepted"), rejected: contains("rejected")},
	{path: "/generic-ar/", banner: visibleJS("#site-cookie-banner[dir=rtl]"),
		accept: `[...document.querySelectorAll('#site-cookie-banner button')].find(b => b.textContent === 'قبول الكل')`,
		reject: `[...document.querySelectorAll('#site-cookie-banner button')].find(b => b.textContent === 'رفض الكل')`,
		state:  cookieJS("site_consent"), accepted: contains("accepted"), rejected: contains("rejected")},
	{path: "/klaro/", banner: visibleJS(".klaro .cookie-notice"), noConsentUpdates: true,
		accept: `document.querySelector('.klaro .cookie-notice .cm-btn-success')`,
		reject: `document.querySelector('.klaro .cookie-notice .cn-decline')`,
		state:  cookieJS("klaro"), accepted: contains(`"google-analytics":true`), rejected: contains(`"google-analytics":false`)},
	{path: "/cookieconsent/", banner: `(document.documentElement.classList.contains('show--consent') && !!document.querySelector('#cc-main .cm'))`, noConsentUpdates: true,
		accept: `document.querySelector('#cc-main .cm [data-role=all]')`,
		reject: `document.querySelector('#cc-main .cm [data-role=necessary]')`,
		state:  cookieJS("cc_cookie"), accepted: contains(`"categories":["necessary","analytics"]`), rejected: contains(`"categories":["necessary"]`)},
}

// didomiPurposes checks the didomi_token half of state: every purpose
// enabled (agreed) or disabled.
func didomiPurposes(state string, agreed bool) bool {
	tok, _, _ := strings.Cut(state, "|")
	raw, err := base64.StdEncoding.DecodeString(tok)
	if err != nil {
		return false
	}
	s := string(raw)
	if agreed {
		return strings.Contains(s, `"enabled":["cookies"`) && strings.Contains(s, `"disabled":[]`)
	}
	return strings.Contains(s, `"enabled":[]`) && strings.Contains(s, `"disabled":["cookies"`)
}

// tcPurposes decodes the purposes with consent from the euconsent-v2
// half of state: the 24 bits after the first 152 of the core segment
// (TCF v2.2), here rather than through Echo's decoder so the replica is
// checked against the specification.
func tcPurposes(state string) string {
	_, tc, _ := strings.Cut(state, "|")
	core, _, _ := strings.Cut(tc, ".")
	raw, err := base64.RawURLEncoding.DecodeString(core)
	if err != nil || len(raw)*8 < 176 {
		return "bad"
	}
	bit := func(i int) bool { return raw[i/8]&(0x80>>(i%8)) != 0 }
	if v := int(raw[0] >> 2); v != 2 {
		return "bad version"
	}
	var out []string
	for p := 1; p <= 24; p++ {
		if bit(152 + p - 1) {
			out = append(out, strconv.Itoa(p))
		}
	}
	return strings.Join(out, ",")
}

// click clicks el (a JS expression for an element) with real mouse
// events, or from JS when it sits in a shadow root.
func (l *liveServer) click(p *rod.Page, el string, viaJS bool) {
	l.t.Helper()
	if viaJS {
		l.eval(p, `() => (`+el+`).click()`)
		return
	}
	e, err := p.ElementByJS(rod.Eval(`() => ` + el))
	if err != nil {
		l.t.Fatalf("find %s: %v", el, err)
	}
	if err := e.Click(proto.InputMouseButtonLeft, 1); err != nil {
		l.t.Fatalf("click %s: %v", el, err)
	}
}

func TestReplicaButtonsWriteState(t *testing.T) {
	l := startLive(t, testRoot(t))
	for _, c := range cmpCases {
		for _, choice := range []string{"accept", "reject"} {
			t.Run(strings.Trim(c.path, "/")+"/"+choice, func(t *testing.T) {
				p := l.open(cmpSite + c.path)
				if !l.waitTrue(p, `() => `+c.banner, 15*time.Second) {
					t.Fatal("the banner never showed")
				}
				el, check := c.accept, c.accepted
				if choice == "reject" {
					el, check = c.reject, c.rejected
				}
				l.click(p, el, c.viaJS)
				deadline := time.Now().Add(5 * time.Second)
				var st string
				for {
					st = l.eval(p, `() => `+c.state)
					if check(st) || time.Now().After(deadline) {
						break
					}
					time.Sleep(100 * time.Millisecond)
				}
				if !check(st) {
					t.Fatalf("stored state after %s: %q", choice, st)
				}
				if l.eval(p, `() => `+c.banner) == "true" {
					t.Error("the banner still shows after the choice")
				}
				if !c.noConsentUpdates {
					want := map[string]string{"accept": "granted", "reject": "denied"}[choice]
					if got := l.eval(p, `() => String(Array.from(dataLayer).some(e => e && e[0] === 'consent' && e[1] === 'update' && e[2].analytics_storage === '`+want+`'))`); got != "true" {
						t.Errorf("no Consent Mode update with analytics_storage %s", want)
					}
				}
				// The choice is kept: after a reload the banner stays away.
				if err := p.Reload(); err != nil {
					t.Fatal(err)
				}
				_ = p.WaitLoad()
				time.Sleep(700 * time.Millisecond)
				if l.eval(p, `() => `+c.banner) == "true" {
					t.Error("the banner came back after a reload")
				}
			})
		}
	}
}

// TestUsercentricsClosedRoot: buttons in a closed shadow root are out of
// reach of page scripts, so only the API makes the choice.
func TestUsercentricsClosedRoot(t *testing.T) {
	l := startLive(t, testRoot(t))
	p := l.open(cmpSite + "/usercentrics-closed/")
	if !l.waitTrue(p, `() => !!document.getElementById('usercentrics-root')`, 10*time.Second) {
		t.Fatal("no #usercentrics-root")
	}
	if got := l.eval(p, `() => String(document.getElementById('usercentrics-root').shadowRoot)`); got != "null" {
		t.Errorf("shadowRoot = %s, want null for a closed root", got)
	}
	l.eval(p, `() => UC_UI.denyAllConsents()`)
	if st := l.eval(p, `() => localStorage.getItem('uc_settings')`); !strings.Contains(st, `"name":"Google Analytics","status":false`) {
		t.Errorf("after denyAllConsents: %s", st)
	}
}

// TestDidomiTCFAPI: __tcfapi answers getTCData and tells listeners
// about the choice with useractioncomplete.
func TestDidomiTCFAPI(t *testing.T) {
	l := startLive(t, testRoot(t))
	p := l.open(cmpSite + "/didomi/")
	l.waitTrue(p, `() => typeof __tcfapi === 'function'`, 10*time.Second)
	l.eval(p, `() => { window.__events = []; __tcfapi('addEventListener', 2, (d) => __events.push(d.eventStatus)); }`)
	l.eval(p, `() => Didomi.setUserDisagreeToAll()`)
	if got := l.eval(p, `() => __events.join(',')`); got != "cmpuishown,useractioncomplete" {
		t.Errorf("events = %s", got)
	}
	got := l.eval(p, `() => new Promise(r => __tcfapi('getTCData', 2, (d, ok) => r(ok + '|' + d.cmpId + '|' + d.tcString.length + '|' + d.purpose.consents[1])))`)
	if !strings.HasPrefix(got, "true|7|") || !strings.HasSuffix(got, "|false") {
		t.Errorf("getTCData = %s", got)
	}
}
