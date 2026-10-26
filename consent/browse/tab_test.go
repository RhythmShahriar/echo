// Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
// SPDX-License-Identifier: MIT

package browse

import (
	"context"
	"errors"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/RhythmShahriar/echo/consent"
	"github.com/go-rod/rod/lib/proto"
)

// Screenshot names keep to letters, digits and dashes, so no name can
// leave the evidence folder.
func TestFileWord(t *testing.T) {
	for in, want := range map[string]string{"banner": "banner", "Page 2": "page-2", "../../etc": "------etc", "": "shot",
		strings.Repeat("a", 60): strings.Repeat("a", 40)} {
		if got := fileWord(in); got != want {
			t.Errorf("fileWord(%q) = %q; want %q", in, got, want)
		}
	}
}

// isolatedJS passes the arguments as JSON and refuses ones that aren't.
func TestIsolatedJS(t *testing.T) {
	js, err := isolatedJS("(a, b) => a + b", []any{1, "x"})
	if err != nil || !strings.Contains(js, ")(...[1,\"x\"])") || !strings.Contains(js, "result too large") {
		t.Errorf("%s %v", js, err)
	}
	if js, _ := isolatedJS("() => 1", nil); !strings.Contains(js, ")(...[])") {
		t.Errorf("no arguments: %s", js)
	}
	if _, err := isolatedJS("() => 1", []any{make(chan int)}); err == nil {
		t.Error("a channel argument was accepted")
	}
}

// cappedJS and mainJS await the expression and measure its JSON in the
// page; a trailing line comment can't swallow the rest of the wrapper.
func TestCappedJS(t *testing.T) {
	js := cappedJS("1 + 1 // note")
	if !strings.Contains(js, "await (\n1 + 1 // note\n);") || !strings.Contains(js, "__s.length > 262144") {
		t.Errorf("%s", js)
	}
	main := mainJS("1 + 1 // note")
	if !strings.Contains(main, "await (\n1 + 1 // note\n);") || !strings.Contains(main, "__n > 262144") || !strings.Contains(main, "return __r;") {
		t.Errorf("%s", main)
	}
}

func TestHostOfURL(t *testing.T) {
	for in, want := range map[string]string{"https://WWW.Shop.test:8443/a": "www.shop.test", "about:blank": "", "%zz": ""} {
		if got := hostOfURL(in); got != want {
			t.Errorf("hostOfURL(%q) = %q; want %q", in, got, want)
		}
	}
}

// Settle ends once nothing started or ended for --settle and stops
// waiting when the context ends.
func TestSettleUnit(t *testing.T) {
	r := unitRecorder(t, consent.RunNone, func(p *consent.Plan) { p.Settle = 150 * time.Millisecond })
	s := &session{rec: r}
	r.activity.Store(time.Now().UnixNano())
	start := time.Now()
	if err := s.settle(t.Context()); err != nil {
		t.Fatal(err)
	}
	if d := time.Since(start); d < 140*time.Millisecond || d > 2*time.Second {
		t.Errorf("settled after %s", d)
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	r.activity.Store(time.Now().UnixNano())
	if err := s.settleAfter(ctx, 0); !errors.Is(err, context.Canceled) {
		t.Errorf("canceled settle: %v", err)
	}
}

// TestCookieFor: a state parser reads the cookie the page is sent, so a
// choice written under another path, host or scheme is no choice (c07).
func TestCookieFor(t *testing.T) {
	ck := func(domain, path, value string, secure bool) *proto.NetworkCookie {
		return &proto.NetworkCookie{Name: "borlabs-cookie", Domain: domain, Path: path, Value: value, Secure: secure}
	}
	page := func(raw string) *url.URL {
		u, err := url.Parse(raw)
		if err != nil {
			t.Fatal(err)
		}
		return u
	}
	for _, tc := range []struct {
		name    string
		cookies []*proto.NetworkCookie
		url     string
		want    string
	}{
		{"host cookie at /", []*proto.NetworkCookie{ck("c07.shop.test", "/", "a", false)}, "https://c07.shop.test/", "a"},
		{"domain cookie", []*proto.NetworkCookie{ck(".shop.test", "/", "a", false)}, "https://c07.shop.test/page-2/", "a"},
		{"domain cookie on its own host", []*proto.NetworkCookie{ck(".shop.test", "/", "a", false)}, "https://shop.test/", "a"},
		{"path prefix at a slash", []*proto.NetworkCookie{ck("c07.shop.test", "/blog", "a", false)}, "https://c07.shop.test/blog/post/", "a"},
		{"longest path wins", []*proto.NetworkCookie{ck("c07.shop.test", "/", "root", false), ck("c07.shop.test", "/blog/", "blog", false)}, "https://c07.shop.test/blog/x", "blog"},
		{"secure over https", []*proto.NetworkCookie{ck("c07.shop.test", "/", "a", true)}, "https://c07.shop.test/", "a"},
		{"other path", []*proto.NetworkCookie{ck("c07.shop.test", "/blog", "a", false)}, "https://c07.shop.test/", ""},
		{"path is no word prefix", []*proto.NetworkCookie{ck("c07.shop.test", "/blog", "a", false)}, "https://c07.shop.test/blogger/", ""},
		{"host cookie of a sub-domain's parent", []*proto.NetworkCookie{ck("shop.test", "/", "a", false)}, "https://c07.shop.test/", ""},
		{"other host", []*proto.NetworkCookie{ck("tracker.test", "/", "a", false)}, "https://c07.shop.test/", ""},
		{"lookalike host", []*proto.NetworkCookie{ck(".hop.test", "/", "a", false)}, "https://c07.shop.test/", ""},
		{"secure over http", []*proto.NetworkCookie{ck("c07.shop.test", "/", "a", true)}, "http://c07.shop.test/", ""},
		{"other name", []*proto.NetworkCookie{{Name: "x", Domain: "c07.shop.test", Path: "/", Value: "a"}}, "https://c07.shop.test/", ""},
		{"nil cookie", []*proto.NetworkCookie{nil}, "https://c07.shop.test/", ""},
	} {
		if got := cookieFor(tc.cookies, page(tc.url), "borlabs-cookie"); got != tc.want {
			t.Errorf("%s: cookieFor = %q, want %q", tc.name, got, tc.want)
		}
	}
	if got := cookieFor([]*proto.NetworkCookie{ck("c07.shop.test", "/", "a", false)}, nil, "borlabs-cookie"); got != "" {
		t.Errorf("no page: %q", got)
	}
}
