// Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
// SPDX-License-Identifier: MIT

package main

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/RhythmShahriar/echo/catcher"
	"github.com/RhythmShahriar/echo/wire"
)

const testToken = "t0ken-for-tests"

func testUpHandler(token string) (http.Handler, []upRoute) {
	inbox := catcher.NewInbox(100)
	srv := catcher.NewServer(inbox, catcher.Config{Mode: catcher.Seal, Token: token, Version: version})
	routes := upRoutes(srv, inbox, catcher.Seal)
	return upHandler(routes, token), routes
}

func serve(h http.Handler, r *http.Request) *httptest.ResponseRecorder {
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	return w
}

// TestEveryUpRouteNeedsTheToken probes every route of the API listener:
// none may answer without the token.
func TestEveryUpRouteNeedsTheToken(t *testing.T) {
	h, routes := testUpHandler(testToken)
	if len(routes) < 3 {
		t.Fatalf("routes = %d", len(routes))
	}
	for _, rt := range routes {
		if w := serve(h, httptest.NewRequest(http.MethodGet, rt.probe, nil)); w.Code != http.StatusUnauthorized {
			t.Errorf("%s (GET %s) without the token: %d, want 401", rt.pattern, rt.probe, w.Code)
		}
		r := httptest.NewRequest(http.MethodGet, rt.probe, nil)
		r.Header.Set(wire.TokenHeader, testToken)
		if w := serve(h, r); w.Code != http.StatusOK {
			t.Errorf("%s (GET %s) with the token: %d, want 200", rt.pattern, rt.probe, w.Code)
		}
		r = httptest.NewRequest(http.MethodGet, rt.probe, nil)
		r.AddCookie(&http.Cookie{Name: tokenCookie, Value: "wrong"})
		if w := serve(h, r); w.Code != http.StatusUnauthorized {
			t.Errorf("%s with a wrong cookie: %d, want 401", rt.pattern, w.Code)
		}
	}
	// Unrouted paths are refused before the mux says 404.
	if w := serve(h, httptest.NewRequest(http.MethodGet, "/nothing-here", nil)); w.Code != http.StatusUnauthorized {
		t.Errorf("unrouted path: %d", w.Code)
	}
}

func TestTokenQuerySetsCookieAndRedirects(t *testing.T) {
	h, _ := testUpHandler(testToken)
	w := serve(h, httptest.NewRequest(http.MethodGet, "/?token="+testToken+"&x=1", nil))
	if w.Code != http.StatusSeeOther || w.Header().Get("Location") != "/?x=1" {
		t.Fatalf("got %d to %q, want 303 to /?x=1", w.Code, w.Header().Get("Location"))
	}
	cookies := w.Result().Cookies() //nolint:bodyclose // a recorder
	if len(cookies) != 1 {
		t.Fatalf("cookies = %v", cookies)
	}
	c := cookies[0]
	if c.Name != tokenCookie || c.Value != testToken || !c.HttpOnly || c.SameSite != http.SameSiteStrictMode || c.Path != "/" || c.Secure {
		t.Errorf("cookie = %+v", c)
	}
	for _, path := range []string{"/", "/v1/messages", "/api/v1/messages"} {
		r := httptest.NewRequest(http.MethodGet, path, nil)
		r.AddCookie(c)
		if w := serve(h, r); w.Code != http.StatusOK {
			t.Errorf("%s with the cookie: %d", path, w.Code)
		}
	}

	if w := serve(h, httptest.NewRequest(http.MethodGet, "/?token=wrong", nil)); w.Code != http.StatusUnauthorized || len(w.Result().Cookies()) != 0 { //nolint:bodyclose // a recorder
		t.Errorf("wrong ?token=: %d %v", w.Code, w.Result().Cookies()) //nolint:bodyclose // a recorder
	}
	// "//host" must not become a redirect to another site.
	w = serve(h, httptest.NewRequest(http.MethodGet, "//evil.example/?token="+testToken, nil))
	if loc := w.Header().Get("Location"); w.Code != http.StatusSeeOther || loc != "/" {
		t.Errorf("open redirect: %d to %q", w.Code, loc)
	}
	// Under TLS the cookie is Secure.
	r := httptest.NewRequest(http.MethodGet, "https://127.0.0.1:8025/?token="+testToken, nil)
	if c := serve(h, r).Result().Cookies(); len(c) != 1 || !c[0].Secure { //nolint:bodyclose // a recorder
		t.Errorf("TLS cookie = %v", c)
	}
	// A POST with ?token= isn't a browser opening the view.
	r = httptest.NewRequest(http.MethodPost, "/v1/capture?token="+testToken, strings.NewReader("{}"))
	if w := serve(h, r); w.Code != http.StatusUnauthorized {
		t.Errorf("POST with ?token=: %d", w.Code)
	}
}

func TestNoTokenLeavesLoopbackOpen(t *testing.T) {
	h, routes := testUpHandler("")
	for _, rt := range routes {
		if w := serve(h, httptest.NewRequest(http.MethodGet, rt.probe, nil)); w.Code != http.StatusOK {
			t.Errorf("%s: %d", rt.pattern, w.Code)
		}
	}
}
