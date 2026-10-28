// Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
// SPDX-License-Identifier: MIT

package browse

import (
	"encoding/json"
	"net/url"
	"strings"
	"testing"

	"github.com/go-rod/rod/lib/proto"

	"github.com/RhythmShahriar/echo/consent"
	"github.com/RhythmShahriar/echo/consent/consenttest"
)

// The attribution reads a Set-Cookie line's name, domain, path and value
// length, by hand when net/http refuses the line.
func TestParseSetCookie(t *testing.T) {
	for _, c := range []struct {
		line, host string
		want       setCookie
		ok         bool
	}{
		{"_ga=GA1.1.123.456; Path=/; Domain=.Shop.Test; Max-Age=63072000", "www.shop.test", setCookie{"_ga", "shop.test", "/", 13}, true},
		{"FPID=abc; HttpOnly; Secure", "Shop.Test", setCookie{"FPID", "shop.test", "/", 3}, true},
		{`odd="quoted value"; Path=/x; Expires=not a date`, "a.test", setCookie{"odd", "a.test", "/x", 12}, true},
		{"sp ace=1; Domain=b.test", "a.test", setCookie{"sp ace", "b.test", "/", 1}, true},
		{"=nameless", "a.test", setCookie{}, false},
		{"novalue", "a.test", setCookie{}, false},
	} {
		got, ok := parseSetCookie(c.line, c.host)
		if ok != c.ok || got != c.want {
			t.Errorf("parseSetCookie(%q) = %+v %v; want %+v %v", c.line, got, ok, c.want, c.ok)
		}
	}
}

// FuzzSetCookie: the attribution parser never panics; what it reads has
// a name and a lower-case domain without a leading dot.
func FuzzSetCookie(f *testing.F) {
	for _, s := range []string{"_ga=1; Domain=.x.test", `a="b"; Expires=Wed, 21 Oct 2015 07:28:00 GMT`, "a=b;;;Domain", "=;", "x=\x00\xff; Path"} {
		f.Add(s, "shop.test")
	}
	f.Fuzz(func(t *testing.T, line, host string) {
		c, ok := parseSetCookie(line, host)
		if !ok {
			return
		}
		if c.Name == "" || strings.HasPrefix(c.Domain, ".") || c.Domain != strings.ToLower(c.Domain) || c.Length < 0 || c.Path == "" {
			t.Errorf("parseSetCookie(%q, %q) = %+v", line, host, c)
		}
	})
}

// cookieRecorder is a unit recorder with the fake catalog linked.
func cookieRecorder(t *testing.T) *recorder {
	t.Helper()
	consenttest.Use(t, consent.Stages{Classifier: &storageClassifier{}})
	return unitRecorder(t, consent.RunReject, nil)
}

// storageClassifier is the fake catalog that also knows Clarity's _clck
// storage key.
type storageClassifier struct{ consenttest.FakeClassifier }

func (c *storageClassifier) Storage(k *consent.StorageKey) *consent.Hit {
	if k.Key == "_clck" {
		return &consent.Hit{Tracker: "clarity", Category: consent.CatAnalytics, Need: consent.NeedNeeded}
	}
	return nil
}

func (c *storageClassifier) Hold(r *consent.Request) bool { return c.FakeClassifier.Hold(r) }

// Snapshots record new and changed cookies once, with their length and
// never their value; one a response set since the last snapshot is the
// response's, at the response's time; any other is the script's.
func TestDiffCookies(t *testing.T) {
	r := cookieRecorder(t)
	value := "GA1.1." + strings.Repeat("9", 12) // assembled at runtime
	ga := &proto.NetworkCookie{Name: "_ga", Value: value, Domain: ".shop.test", Path: "/", Expires: 1893456000, Secure: true}
	fp := &proto.NetworkCookie{Name: "_fbp", Value: "fb.1.2", Domain: "shop.test", Path: "/", Session: true, Expires: -1}
	r.noteSetCookies("www.shop.test", []string{"_ga=" + value + "; Domain=.shop.test"}, 40)
	r.diffCookies([]*proto.NetworkCookie{ga, fp}, 100)
	r.mu.Lock()
	r.lastSnapshot = 100
	r.mu.Unlock()
	r.diffCookies([]*proto.NetworkCookie{ga, fp}, 200) // unchanged: nothing
	changed := *fp
	changed.Value = "fb.1.3"
	r.diffCookies([]*proto.NetworkCookie{ga, &changed}, 300)
	rec := r.finish()
	cookies := kindsOf(rec.Events, consent.EvCookie)
	if len(cookies) != 3 {
		t.Fatalf("%d cookie events", len(cookies))
	}
	g, f, f2 := cookies[0].Cookie, cookies[1].Cookie, cookies[2].Cookie
	if g.Name != "_ga" || g.SetBy != "response:www.shop.test" || g.AtMS != 40 || g.Length != len(value) || g.Tracker != "ga4" || g.Expires != 1893456000 || !g.Secure {
		t.Errorf("_ga %+v", g)
	}
	if f.SetBy != "script" || f.AtMS != 100 || f.Expires != 0 || f.Tracker != "meta-pixel" || f.Need != consent.NeedNeeded {
		t.Errorf("_fbp %+v", f)
	}
	if f2.AtMS != 300 || f2.Length != 6 {
		t.Errorf("changed _fbp %+v", f2)
	}
	out, _ := json.Marshal(rec.Events)
	if strings.Contains(string(out), value) {
		t.Error("a cookie value is in the events")
	}
}

// A cookie Echo added with --cookie is Echo's, not the page's scripts';
// once the page changes its value it is the page's.
func TestEchoCookieSetByEcho(t *testing.T) {
	r := cookieRecorder(t)
	sid := "s" + strings.Repeat("7", 10) // assembled at runtime
	target, _ := url.Parse("https://shop.test/")
	r.noteEchoCookies(cookieParams([]string{"sid=" + sid}, target))
	ck := &proto.NetworkCookie{Name: "sid", Value: sid, Domain: "shop.test", Path: "/"}
	r.diffCookies([]*proto.NetworkCookie{ck}, 100)
	r.mu.Lock()
	r.lastSnapshot = 100
	r.mu.Unlock()
	changed := *ck
	changed.Value = "other"
	r.diffCookies([]*proto.NetworkCookie{&changed}, 200)
	c := kindsOf(r.finish().Events, consent.EvCookie)
	if len(c) != 2 || c[0].Cookie.SetBy != consent.SetByEcho || c[1].Cookie.SetBy != "script" {
		t.Errorf("cookies %+v", c)
	}
}

// A GA4 cookie is named after the measurement ID: it is classified by
// its real name and recorded with the ID cut to its last 4 characters,
// unless --show-ids.
func TestGA4CookieNameMasked(t *testing.T) {
	id := "TESTBED" + "001" // assembled at runtime
	for _, show := range []bool{false, true} {
		r := cookieRecorder(t)
		r.plan.ShowIDs = show
		r.diffCookies([]*proto.NetworkCookie{{Name: "_ga_" + id, Value: "GS1.1", Domain: ".shop.test", Path: "/"}}, 100)
		c := kindsOf(r.finish().Events, consent.EvCookie)
		want := "_ga_••••D001"
		if show {
			want = "_ga_" + id
		}
		if len(c) != 1 || c[0].Cookie.Name != want || c[0].Cookie.Tracker != "ga4" {
			t.Errorf("show-ids %v: %+v", show, c)
		}
	}
}

// A blocked cookie is recorded at once with Chrome's reasons, once.
func TestBlockedCookie(t *testing.T) {
	r := cookieRecorder(t)
	r.mu.Lock()
	r.hosts["7"] = "tracker.test"
	r.mu.Unlock()
	e := &proto.NetworkResponseReceivedExtraInfo{RequestID: "7",
		BlockedCookies: []*proto.NetworkBlockedSetCookieWithReason{{CookieLine: "_fbp=fb.1; SameSite=None",
			BlockedReasons: []proto.NetworkSetCookieBlockedReason{"ThirdPartyPhaseout", "SameSiteNoneInsecure"}}}}
	r.extraInfo(e)
	r.extraInfo(e)
	rec := r.finish()
	c := kindsOf(rec.Events, consent.EvCookie)
	if len(c) != 1 || c[0].Cookie.Blocked != "SameSiteNoneInsecure,ThirdPartyPhaseout" || c[0].Cookie.SetBy != "response:tracker.test" ||
		c[0].Cookie.Domain != "tracker.test" || c[0].Cookie.Tracker != "meta-pixel" {
		t.Errorf("blocked cookies %+v", c)
	}
}

// Storage keys are recorded when new or when their value's length
// changes; unreadable rows and opaque origins are skipped.
func TestDiffStorage(t *testing.T) {
	r := cookieRecorder(t)
	row := func(s string) json.RawMessage { return json.RawMessage(s) }
	r.diffStorage(storageAnswer{Origin: "https://Shop.Test", Keys: []json.RawMessage{
		row(`["local","_clck",12]`), row(`["session","cart",3]`), row(`["indexeddb","firebase",0]`),
		row(`["cookie","x",1]`), row(`"broken"`), row(`["local",5,1]`)}}, 50)
	r.diffStorage(storageAnswer{Origin: "https://shop.test", Keys: []json.RawMessage{row(`["local","_clck",12]`), row(`["session","cart",4]`)}}, 90)
	r.diffStorage(storageAnswer{Origin: "null", Keys: []json.RawMessage{row(`["local","a",1]`)}}, 95)
	rec := r.finish()
	var got []string
	for _, e := range kindsOf(rec.Events, consent.EvStorage) {
		k := e.Storage
		got = append(got, k.Area+":"+k.Key+":"+k.Origin+":"+k.Tracker)
	}
	want := "local:_clck:https://shop.test:clarity session:cart:https://shop.test: indexeddb:firebase:https://shop.test: session:cart:https://shop.test:"
	if strings.Join(got, " ") != want {
		t.Errorf("storage events\n%v\nwant\n%s", got, want)
	}
}

// setCookieLines splits Chrome's joined Set-Cookie header.
func TestSetCookieLines(t *testing.T) {
	var h proto.NetworkHeaders
	_ = json.Unmarshal([]byte(`{"set-cookie":"a=1\nb=2; Path=/\n","Content-Type":"text/html"}`), &h)
	if got := setCookieLines(h); strings.Join(got, "|") != "a=1|b=2; Path=/" {
		t.Errorf("lines %q", got)
	}
}
