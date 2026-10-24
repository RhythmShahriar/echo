// Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
// SPDX-License-Identifier: MIT

package web

import (
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"
)

const testLogin = "client:correct horse"

// viewerApp stands in for Echo Inbox behind a Login: it says whether the
// request came in read only.
func viewerApp(t *testing.T) (*Login, http.Handler) {
	t.Helper()
	l, err := ParseLogin(testLogin)
	if err != nil {
		t.Fatal(err)
	}
	app := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if ReadOnly(r.Context()) {
			_, _ = w.Write([]byte("viewer " + r.URL.Path))
			return
		}
		_, _ = w.Write([]byte("open " + r.URL.Path))
	})
	return l, l.Guard(app)
}

func do(h http.Handler, method, target string, body url.Values, cookies ...*http.Cookie) *httptest.ResponseRecorder {
	var r *http.Request
	if body != nil {
		r = httptest.NewRequest(method, target, strings.NewReader(body.Encode()))
		r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	} else {
		r = httptest.NewRequest(method, target, nil)
	}
	for _, c := range cookies {
		r.AddCookie(c)
	}
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	return w
}

// signIn posts the form and returns the session cookie.
func signIn(t *testing.T, h http.Handler, user, pass, next string) (*httptest.ResponseRecorder, *http.Cookie) {
	t.Helper()
	w := do(h, http.MethodPost, PathLogin, url.Values{"user": {user}, "password": {pass}, "next": {next}})
	for _, c := range w.Result().Cookies() {
		if c.Name == SessionCookie {
			return w, c
		}
	}
	return w, nil
}

func TestParseLogin(t *testing.T) {
	for spec, want := range map[string]string{
		"client:correct horse": "", "client:pa:ss:word": "", "client": "want user:password", ":password1": "want user:password",
		"client:": "want user:password", "client:short": "at least 8 characters",
	} {
		l, err := ParseLogin(spec)
		if want == "" && (err != nil || l.User() != "client") || want != "" && (err == nil || !strings.Contains(err.Error(), want)) {
			t.Errorf("ParseLogin(%q) = %v, want %q", spec, err, want)
		}
	}
}

func TestNoSessionRedirectsToLogin(t *testing.T) {
	_, h := viewerApp(t)
	w := do(h, http.MethodGet, "/?x=1", nil)
	if w.Code != http.StatusFound || w.Header().Get("Location") != "/login?next=%2F%3Fx%3D1" {
		t.Errorf("GET / = %d %q", w.Code, w.Header().Get("Location"))
	}
	if w := do(h, http.MethodDelete, "/ui/messages/abc", nil); w.Code != http.StatusUnauthorized {
		t.Errorf("DELETE without a session = %d", w.Code)
	}
	if w := do(h, http.MethodGet, PathLogin+"?next=/report", nil); w.Code != 200 || !strings.Contains(w.Body.String(), `name="next" value="/report"`) ||
		!strings.Contains(w.Header().Get("Content-Security-Policy"), "frame-ancestors 'none'") {
		t.Errorf("login page = %d %s", w.Code, w.Body)
	}
	if w := do(h, http.MethodGet, "/assets/app.css", nil); w.Code != 200 {
		t.Errorf("assets need no session, got %d", w.Code)
	}
}

func TestWrongPasswordIs401(t *testing.T) {
	_, h := viewerApp(t)
	for _, c := range [][2]string{{"client", "wrong horse"}, {"other", "correct horse"}, {"", ""}, {"client", "correct horse!"}} {
		w, cookie := signIn(t, h, c[0], c[1], "/")
		if w.Code != http.StatusUnauthorized || cookie != nil || !strings.Contains(w.Body.String(), "Wrong user name or password.") {
			t.Errorf("%q: %d, cookie %v", c, w.Code, cookie)
		}
	}
}

func TestSessionCookieWorksAndExpires(t *testing.T) {
	l, h := viewerApp(t)
	clock := time.Date(2026, 10, 24, 9, 0, 0, 0, time.UTC)
	l.now = func() time.Time { return clock }
	w, c := signIn(t, h, "client", "correct horse", "/report")
	if w.Code != http.StatusSeeOther || w.Header().Get("Location") != "/report" || c == nil {
		t.Fatalf("sign in = %d %q %v", w.Code, w.Header().Get("Location"), c)
	}
	if !c.HttpOnly || c.SameSite != http.SameSiteStrictMode || c.MaxAge != 8*3600 || c.Path != "/" || len(c.Value) < 26 {
		t.Errorf("cookie = %+v", c)
	}
	if w := do(h, http.MethodGet, "/ui/messages", nil, c); w.Code != 200 || w.Body.String() != "viewer /ui/messages" {
		t.Errorf("with the session: %d %s", w.Code, w.Body)
	}
	if w := do(h, http.MethodDelete, "/ui/messages/abc", nil, c); w.Code != http.StatusForbidden || !strings.Contains(w.Body.String(), "view only") {
		t.Errorf("a viewer deleting: %d %s", w.Code, w.Body)
	}
	if w := do(h, http.MethodGet, "/", nil, &http.Cookie{Name: SessionCookie, Value: c.Value + "x"}); w.Code != http.StatusFound {
		t.Errorf("a made-up session: %d", w.Code)
	}
	clock = clock.Add(SessionTTL - time.Second)
	if w := do(h, http.MethodGet, "/", nil, c); w.Code != 200 {
		t.Errorf("just before expiry: %d", w.Code)
	}
	clock = clock.Add(time.Second)
	if w := do(h, http.MethodGet, "/", nil, c); w.Code != http.StatusFound {
		t.Errorf("after 8 hours: %d", w.Code)
	}
	if len(l.sessions) != 0 {
		t.Errorf("expired session kept: %v", l.sessions)
	}
}

func TestSignOutEndsTheSession(t *testing.T) {
	_, h := viewerApp(t)
	_, c := signIn(t, h, "client", "correct horse", "/")
	w := do(h, http.MethodPost, PathLogout, url.Values{}, c)
	if w.Code != http.StatusSeeOther || w.Header().Get("Location") != PathLogin {
		t.Errorf("sign out = %d", w.Code)
	}
	if w := do(h, http.MethodGet, "/", nil, c); w.Code != http.StatusFound {
		t.Errorf("after sign out: %d", w.Code)
	}
}

func TestLoginRefusesOtherSitesAndOpenRedirects(t *testing.T) {
	_, h := viewerApp(t)
	r := httptest.NewRequest(http.MethodPost, PathLogin, strings.NewReader("user=client&password=correct+horse"))
	r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	r.Header.Set("Origin", "https://evil.test")
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	if w.Code != http.StatusForbidden {
		t.Errorf("cross-site sign in = %d", w.Code)
	}
	for _, next := range []string{"//evil.test/", "https://evil.test/", `/\evil.test`, PathLogin,
		"/\t/evil.test", "/\n/evil.test", "/\r/evil.test", "/\x7f/evil.test", "/x\\..\\/evil.test",
		"/%2F/evil.test", "javascript:alert(1)", "mailto:a@evil.test", "evil.test", ""} {
		if w, _ := signIn(t, h, "client", "correct horse", next); w.Header().Get("Location") != "/" {
			t.Errorf("next=%q redirected to %q", next, w.Header().Get("Location"))
		}
		if w := do(h, http.MethodGet, PathLogin+"?next="+url.QueryEscape(next), nil); !strings.Contains(w.Body.String(), `name="next" value="/"`) {
			t.Errorf("next=%q kept on the sign-in page", next)
		}
	}
	// A path on this site comes back escaped and with its query.
	for next, want := range map[string]string{"/report?id=7": "/report?id=7", "/a b": "/a%20b"} {
		if w, _ := signIn(t, h, "client", "correct horse", next); w.Header().Get("Location") != want {
			t.Errorf("next=%q redirected to %q, want %q", next, w.Header().Get("Location"), want)
		}
	}
}

func TestLoginThrottlesGuessing(t *testing.T) {
	l, h := viewerApp(t)
	now := time.Now()
	l.now = func() time.Time { return now }
	for i := range maxFailures {
		if w, _ := signIn(t, h, "client", "guess-guess", "/"); w.Code != http.StatusUnauthorized {
			t.Fatalf("wrong password %d: %d", i+1, w.Code)
		}
	}
	if w, _ := signIn(t, h, "client", "guess-guess", "/"); w.Code != http.StatusTooManyRequests {
		t.Errorf("after %d wrong passwords: %d", maxFailures, w.Code)
	}
	// The right password is refused too while throttled: otherwise the
	// throttle would slow nothing.
	if w, c := signIn(t, h, "client", "correct horse", "/"); w.Code != http.StatusTooManyRequests || c != nil {
		t.Errorf("right password while throttled: %d, cookie %v", w.Code, c)
	}
	now = now.Add(time.Minute + time.Second)
	if w, c := signIn(t, h, "client", "correct horse", "/"); w.Code != http.StatusSeeOther || c == nil {
		t.Errorf("right password a minute later: %d", w.Code)
	}
}
