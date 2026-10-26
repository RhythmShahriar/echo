// Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
// SPDX-License-Identifier: MIT

package tracker

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"net/url"
	"slices"
	"strings"
	"testing"

	"github.com/RhythmShahriar/echo/consent"
)

// ga4Post is a GA4 batch: shared parameters in the query, body lines.
func ga4Post(body string) *consent.Hit {
	r := &consent.Request{Method: "POST", URL: "https://region1.google-analytics.com/g/collect?v=2&tid=G-TESTBED001&gcs=G100", Type: "ping"}
	return builtin.Request(r, []byte(body), shop)
}

func TestGA4Batch(t *testing.T) {
	h := ga4Post("en=page_view&_et=1\r\nen=scroll&epn.percent_scrolled=90\r\n\r\nen=user_engagement\r\n")
	if h == nil || h.Tracker != "ga4" {
		t.Fatalf("hit = %+v", h)
	}
	if want := []string{"page_view", "scroll", "user_engagement"}; !slices.Equal(h.Events, want) {
		t.Errorf("events = %v, want %v", h.Events, want)
	}
	if h.Params["events_truncated"] != "" || h.Params["gcs"] != "G100" || h.Google == nil || h.Google.GCS != "G100" {
		t.Errorf("params = %v, google = %+v", h.Params, h.Google)
	}
	// An event in the query comes first, then the body lines.
	r := &consent.Request{Method: "POST", URL: "https://www.google-analytics.com/g/collect?v=2&tid=G-TESTBED001&en=page_view"}
	if h := builtin.Request(r, []byte("en=scroll\n\nen=click"), shop); h == nil || !slices.Equal(h.Events, []string{"page_view", "scroll", "click"}) {
		t.Errorf("query and body events = %+v", h)
	}
	// A line over 64 KiB is no event; the lines around it still count.
	long := "en=" + strings.Repeat("x", maxLine)
	if h := ga4Post("en=a\n" + long + "\nen=b"); h == nil || !slices.Equal(h.Events, []string{"a", "b"}) {
		t.Errorf("long line = %+v", h)
	}
	// Lines without an event name add none.
	if h := ga4Post("_et=1\nen=a\nep.x=1"); h == nil || !slices.Equal(h.Events, []string{"a"}) {
		t.Errorf("lines without en = %+v", h)
	}
}

func TestGA4BatchCap(t *testing.T) {
	limit := consent.DefaultLimits.BatchEvents
	lines := func(n int) string {
		var b strings.Builder
		for i := range n {
			fmt.Fprintf(&b, "en=e%d\n", i)
		}
		return b.String()
	}
	for _, c := range []struct {
		n         int
		events    int
		truncated bool
	}{{300, limit, true}, {limit + 1, limit, true}, {limit, limit, false}, {3, 3, false}} {
		h := ga4Post(lines(c.n))
		if h == nil {
			t.Fatalf("%d lines: no hit", c.n)
		}
		if len(h.Events) != c.events || (h.Params["events_truncated"] == "true") != c.truncated {
			t.Errorf("%d lines: %d events, events_truncated %q; want %d, %v", c.n, len(h.Events), h.Params["events_truncated"], c.events, c.truncated)
		}
		if h.Events[0] != "e0" || h.Events[len(h.Events)-1] != fmt.Sprintf("e%d", c.events-1) {
			t.Errorf("%d lines: events run %s to %s", c.n, h.Events[0], h.Events[len(h.Events)-1])
		}
	}
	if limit != 256 {
		t.Fatalf("BatchEvents = %d, want 256 (PRD 13.8)", limit)
	}
}

func TestMetaFormBody(t *testing.T) {
	r := &consent.Request{Method: "POST", URL: "https://www.facebook.com/tr/", Type: "ping"}
	body := "id=1234567890&ev=Purchase&dl=https%3A%2F%2Fshop.test%2Fthanks&cd%5Bvalue%5D=10.50&cd[currency]=EUR&cd[content_ids]=%5B%22sku-77%22%5D"
	h := builtin.Request(r, []byte(body), shop)
	if h == nil || h.Tracker != "meta-pixel" || h.Kind != consent.HitBeacon {
		t.Fatalf("hit = %+v", h)
	}
	if !slices.Equal(h.Events, []string{"Purchase"}) || h.ID != "••••7890" {
		t.Errorf("events %v, id %q", h.Events, h.ID)
	}
	if got := h.Params["cd"]; got != "cd[content_ids],cd[currency],cd[value]" {
		t.Errorf("cd = %q", got)
	}
	if h.Params["ev"] != "Purchase" || h.Params["id"] != "••••7890" || h.Params["dl"] != "https://shop.test/thanks" {
		t.Errorf("params = %v", h.Params)
	}
	for k, v := range h.Params {
		for _, s := range []string{"10.50", "EUR", "sku-77", "1234567890"} {
			if strings.Contains(v, s) {
				t.Errorf("Params[%s] = %q keeps %q", k, v, s)
			}
		}
	}
	// The same as a GET image; with a query and a body: the query's
	// event, then the body's, as for any beacon.
	if h := builtin.Request(get("https://www.facebook.com/tr/?"+body, "image"), nil, shop); h == nil || h.Params["cd"] != "cd[content_ids],cd[currency],cd[value]" {
		t.Errorf("GET = %+v", h)
	}
	r = &consent.Request{Method: "POST", URL: "https://www.facebook.com/tr/?id=1234567890&ev=PageView"}
	if h := builtin.Request(r, []byte("ev=Lead&cd[x]=1"), shop); h == nil || !slices.Equal(h.Events, []string{"PageView", "Lead"}) || h.Params["ev"] != "PageView" || h.Params["cd"] != "cd[x]" {
		t.Errorf("query and body = %+v", h)
	}
}

func TestConsentMasksUserData(t *testing.T) {
	sum := sha256.Sum256([]byte("bea" + "@" + "example.test"))
	email := hex.EncodeToString(sum[:])
	plain := "bea" + "@" + "example.test"
	phone := "49170" + "7654321"
	ext := "cust-" + "4471"
	city := "ham" + "burg"
	secrets := []string{email, plain, phone, ext, city}
	meta := url.Values{}
	meta.Set("id", "1234567890")
	meta.Set("ev", "Lead")
	meta.Set("ud[em]", email)
	meta.Set("ud[ph]", phone)
	meta.Set("ud[ct]", city)
	meta.Set("external_id", ext)
	meta.Set("dl", "https://shop.test/signup?email="+plain)
	meta.Set("cd[email]", plain)
	ga := url.Values{}
	ga.Set("v", "2")
	ga.Set("tid", "G-TESTBED001")
	ga.Set("en", "sign_up")
	ga.Set("uid", ext)
	ga.Set("dl", "https://shop.test/thanks?em="+plain+"&ph="+phone)
	ga.Set("dr", "https://mail.example.test/?to="+plain)
	ga.Set("ep.email", plain)
	for _, c := range []struct {
		name, url, body string
		personal        string
	}{
		{"meta GET", "https://www.facebook.com/tr/?" + meta.Encode(), "", "ud[em],ud[ph],ud[ct],external_id"},
		{"meta POST", "https://www.facebook.com/tr/", meta.Encode(), "ud[em],ud[ph],ud[ct],external_id"},
		{"ga4 GET", "https://www.google-analytics.com/g/collect?" + ga.Encode(), "", ""},
		{"ga4 batch", "https://www.google-analytics.com/g/collect?v=2&tid=G-TESTBED001", ga.Encode() + "\n" + ga.Encode(), ""},
	} {
		method := "GET"
		if c.body != "" {
			method = "POST"
		}
		h := builtin.Request(&consent.Request{Method: method, URL: c.url}, []byte(c.body), shop)
		if h == nil || h.Kind != consent.HitBeacon {
			t.Fatalf("%s: hit = %+v", c.name, h)
		}
		if h.Params["personal"] != c.personal {
			t.Errorf("%s: personal = %q, want %q", c.name, h.Params["personal"], c.personal)
		}
		out := []string{h.ID}
		out = append(out, h.Events...)
		for k, v := range h.Params {
			out = append(out, k, v)
		}
		for _, o := range out {
			for _, s := range secrets {
				if strings.Contains(o, s) {
					t.Errorf("%s: %q shows %q", c.name, o, s)
				}
			}
		}
	}
}

func TestPlausibleFailsClosed(t *testing.T) {
	for _, c := range []struct {
		body string
		want bool
	}{
		{`{"n":"pageview","u":"https://shop.test/","d":"shop.test","r":null}`, true},
		{`{"n":"pageview","u":"https://shop.test/"`, false}, // cut by the parse window
		{`{"n":1,"u":"https://shop.test/"}`, false},
		{`{"n":"pageview","u":null}`, false},
		{`{"n":["pageview"],"u":"x"}`, false},
		{`{"name":"pageview","url":"x"}`, false},
		{`"n u"`, false},
		{`n=pageview&u=x`, false},
	} {
		r := &consent.Request{Method: "POST", URL: "https://shop.test/api/event", Type: "fetch"}
		h := builtin.Request(r, []byte(c.body), shop)
		if got := h != nil && h.Tracker == "plausible"; got != c.want {
			t.Errorf("%s: plausible %v, want %v", c.body, got, c.want)
		}
	}
}

// checkValues fails when a hit holds a value over 1024 bytes or more
// events than a batch may give.
func checkValues(t *testing.T, h *consent.Hit) {
	t.Helper()
	if h == nil {
		return
	}
	if len(h.Events) > consent.DefaultLimits.BatchEvents {
		t.Fatalf("%d events", len(h.Events))
	}
	vals := append([]string{h.ID}, h.Events...)
	for k, v := range h.Params {
		vals = append(vals, k, v)
	}
	if g := h.Google; g != nil {
		vals = append(vals, g.GCS, g.GCD, g.NPA, g.DMA)
	}
	for _, v := range vals {
		if len(v) > 1024 {
			t.Fatalf("a value of %d bytes", len(v))
		}
	}
}

func FuzzGA4Body(f *testing.F) {
	f.Add([]byte("en=page_view\nen=scroll"))
	f.Add([]byte("en=page_view\r\nen=scroll\r\n\r\nen=user_engagement\r\n"))
	f.Add([]byte("\r\r\n\n\ren=a\x00\n%zz=1&en=%E2%80%AE\n"))
	f.Add([]byte("en=" + strings.Repeat("x", 1<<20)))
	f.Add([]byte(strings.Repeat("en=a\n", 400)))
	f.Add([]byte(`{"en":"x"}`))
	f.Fuzz(func(t *testing.T, body []byte) {
		for _, u := range []string{
			"https://region1.google-analytics.com/g/collect?v=2&tid=G-TESTBED001",
			"https://metrics.shop.test/g/collect?v=2&tid=G-TESTBED001",
		} {
			r := &consent.Request{Method: "POST", URL: u}
			h := builtin.Request(r, body, shop)
			if h == nil || h.Tracker != "ga4" {
				t.Fatalf("%s: hit = %+v", u, h)
			}
			checkValues(t, h)
		}
	})
}

func FuzzMetaParams(f *testing.F) {
	f.Add("ud[em]", "0f1e2d3c4b5a69788796a5b4c3d2e1f00f1e2d3c4b5a69788796a5b4c3d2e1f0", "", false)
	f.Add("ud[ph]", "4915112345678", "ev=Lead", true)
	f.Add("ud%5Bfn%5D", "annabelle", "cd[x]=1", true)
	f.Add("ud[ct]", "berlin&ud[zp]=10115", "\n", false)
	f.Fuzz(func(t *testing.T, key, value, extra string, post bool) {
		k := strings.ToLower(key)
		if u, err := url.QueryUnescape(k); err == nil {
			k = u
		}
		// One ud[..] pair: a key that carries its own = or & would be
		// another parameter, which the test can't follow.
		if !strings.HasPrefix(k, "ud[") || strings.ContainsAny(key, "=&#?") || strings.Contains(value, "#") || len(value) < 6 {
			return
		}
		pairs := "id=1234567890&ev=PageView&" + key + "=" + value + "&" + extra
		r := &consent.Request{Method: "GET", URL: "https://www.facebook.com/tr/?" + pairs}
		var body []byte
		if post {
			r = &consent.Request{Method: "POST", URL: "https://www.facebook.com/tr/"}
			body = []byte(pairs)
		}
		h := builtin.Request(r, body, shop)
		if h == nil {
			return // a value that broke the URL apart
		}
		checkValues(t, h)
		// The value as the matcher reads it: up to the next pair.
		v, _, _ := strings.Cut(value, "&")
		if d, err := url.QueryUnescape(v); err == nil {
			v = d
		}
		known := "id=1234567890&ev=PageView " + strings.Join(metaPersonal(t), ",")
		if len(v) < 6 || strings.Contains(extra, v) || strings.Contains(known, v) {
			return
		}
		out := append([]string{h.ID}, h.Events...)
		for name, val := range h.Params {
			out = append(out, name, val)
		}
		for _, o := range out {
			if strings.Contains(o, v) {
				t.Fatalf("%s=%q shows in %q", key, v, o)
			}
		}
	})
}

// metaPersonal is the meta-pixel entry's personal field names.
func metaPersonal(t *testing.T) []string {
	for _, e := range builtin.entries {
		if e.info.ID == "meta-pixel" {
			return e.personal
		}
	}
	t.Fatal("no meta-pixel entry")
	return nil
}

func TestLongPageURLDropped(t *testing.T) {
	long := "https://shop.test/?q=" + strings.Repeat("a", maxPageURL)
	h := builtin.Request(get("https://www.google-analytics.com/g/collect?v=2&tid=G-TESTBED001&dl="+url.QueryEscape(long)+"&dr=https%3A%2F%2Fshop.test%2F", "ping"), nil, shop)
	if h == nil || h.Params["dl"] != "" || h.Params["dr"] != "https://shop.test/" {
		t.Fatalf("hit = %+v", h)
	}
}
