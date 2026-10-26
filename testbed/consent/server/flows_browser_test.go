// Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
// SPDX-License-Identifier: MIT

//go:build browser

package server

import (
	"testing"
	"time"

	"github.com/go-rod/rod"
)

// flow drives one scenario site in plain Chrome with page scripts, the
// way a visitor would, so the tricky broken states are known to behave
// before Echo is pointed at them.
type flow struct {
	l  *liveServer
	t  *testing.T
	id string
	p  *rod.Page
}

func (l *liveServer) flow(t *testing.T, id string, broken bool) *flow {
	t.Helper()
	if err := l.srv.SetBroken(id, broken); err != nil {
		t.Fatal(err)
	}
	l.srv.ResetScenario(id)
	return &flow{l: l, t: t, id: id, p: l.open("https://" + id + SiteSuffix + "/")}
}

func (f *flow) do(js string) { f.t.Helper(); f.l.eval(f.p, `() => { `+js+` }`) }

func (f *flow) visit(path string) {
	f.t.Helper()
	if err := f.p.Navigate("https://" + f.id + SiteSuffix + path); err != nil {
		f.t.Fatal(err)
	}
	_ = f.p.WaitLoad()
}

func (f *flow) count(hostPath string, within time.Duration) int {
	f.t.Helper()
	time.Sleep(within)
	n := 0
	for _, h := range f.l.srv.Hits(f.id) {
		if pathIs(hostPath)(h) {
			n++
		}
	}
	return n
}

func TestScenarioFlows(t *testing.T) {
	l := startLive(t, realRoot(t))
	wait := 1500 * time.Millisecond
	meta := "www.facebook.com/tr"

	t.Run("g01 accept and reject", func(t *testing.T) {
		f := l.flow(t, "g01", false)
		l.waitTrue(f.p, `() => !!document.querySelector('.cmplz-accept')`, 10*time.Second)
		f.do(`document.querySelector('.cmplz-accept').click()`)
		hits := l.waitHits("g01", 1, ga4Hit, 10*time.Second)
		if len(hits) == 0 || param(hits[0], "gcs") != "G111" {
			t.Errorf("GA4 after Accept: %+v", hits)
		}
		if n := f.count(meta, wait); n == 0 {
			t.Error("no Meta hit after Accept")
		}
		f = l.flow(t, "g01", false)
		l.waitTrue(f.p, `() => !!document.querySelector('.cmplz-deny')`, 10*time.Second)
		f.do(`document.querySelector('.cmplz-deny').click()`)
		f.visit("/page-2/")
		if n := len(l.srv.Hits("g01")); n != 0 {
			t.Errorf("%d hits after Reject", n)
		}
	})

	t.Run("c07 broken shows the banner again", func(t *testing.T) {
		f := l.flow(t, "c07", true)
		l.waitTrue(f.p, `() => !!document.querySelector('[data-cookie-refuse]')`, 10*time.Second)
		f.do(`document.querySelector('[data-cookie-refuse]').click()`)
		f.visit("/page-2/")
		if !l.waitTrue(f.p, `() => { const b = document.querySelector('#BorlabsCookieBox ._brlbs-box-wrap'); return !!b && b.offsetWidth > 0; }`, 5*time.Second) {
			t.Error("the banner didn't come back on the next page")
		}
		g := l.flow(t, "c07", false)
		l.waitTrue(g.p, `() => !!document.querySelector('[data-cookie-refuse]')`, 10*time.Second)
		g.do(`document.querySelector('[data-cookie-refuse]').click()`)
		g.visit("/page-2/")
		time.Sleep(time.Second)
		if l.eval(g.p, `() => { const b = document.querySelector('#BorlabsCookieBox'); return !!b && b.style.display !== 'none'; }`) == "true" {
			t.Error("good c07: the banner came back")
		}
	})

	t.Run("c24 withdraw", func(t *testing.T) {
		for _, broken := range []bool{false, true} {
			f := l.flow(t, "c24", broken)
			l.waitTrue(f.p, `() => typeof OneTrust === 'object'`, 10*time.Second)
			f.do(`OneTrust.AllowAll()`)
			f.visit("/page-2/")
			before := f.count(meta, wait)
			if before == 0 {
				t.Errorf("broken %v: no Meta hit after Accept", broken)
			}
			f.do(`document.querySelector('#ot-sdk-btn').click(); document.querySelector('#onetrust-reject-all-handler').click();`)
			f.visit("/")
			after := f.count(meta, wait)
			if broken && after == before {
				t.Error("broken: Meta stopped after the withdrawal")
			}
			if !broken && after != before {
				t.Errorf("good: Meta kept firing after the withdrawal (%d then %d)", before, after)
			}
		}
	})

	t.Run("c13 beacon on pagehide", func(t *testing.T) {
		for _, broken := range []bool{false, true} {
			f := l.flow(t, "c13", broken)
			l.waitTrue(f.p, `() => !!document.querySelector('.cookie-reject')`, 10*time.Second)
			f.do(`document.querySelector('.cookie-reject').click()`)
			f.visit("/page-2/")
			n := f.count(meta, wait)
			if broken != (n > 0) {
				t.Errorf("broken %v: %d Meta beacons after Reject", broken, n)
			}
		}
	})

	t.Run("c04 reject granted", func(t *testing.T) {
		f := l.flow(t, "c04", true)
		l.waitTrue(f.p, `() => typeof OneTrust === 'object'`, 10*time.Second)
		f.do(`OneTrust.RejectAll()`)
		hits := l.waitHits("c04", 1, ga4Hit, 10*time.Second)
		if len(hits) == 0 || param(hits[0], "gcs") != "G101" {
			t.Errorf("GA4 after a Reject read as granted: %+v", hits)
		}
	})
}
