// Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
// SPDX-License-Identifier: MIT

package main

import (
	"bufio"
	"bytes"
	"context"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/RhythmShahriar/echo/catcher"
	"github.com/RhythmShahriar/echo/check"
	"github.com/RhythmShahriar/echo/model"
	"github.com/RhythmShahriar/echo/report"
	"github.com/RhythmShahriar/echo/wire"
)

const testToken = "t0ken-for-tests"

func testUpHandler(token string) (http.Handler, []upRoute) {
	h, routes, _ := testUp(token)
	return h, routes
}

func testUp(token string) (http.Handler, []upRoute, *catcher.Inbox) {
	inbox := catcher.NewInbox(100)
	srv := catcher.NewServer(inbox, catcher.Config{Mode: catcher.Seal, Token: token, Version: version})
	routes := upRoutes(srv, inbox, catcher.Seal, check.Options{SiteHost: "shop.example.com"})
	return upHandler(routes, token), routes, inbox
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

// freePorts returns n loopback addresses in the range these tests may use
// (20400-20419) that nothing listens on right now.
func freePorts(t *testing.T, n int) []string {
	t.Helper()
	var out []string
	for port := 20400; port < 20420 && len(out) < n; port++ {
		addr := fmt.Sprintf("127.0.0.1:%d", port)
		if l, err := net.Listen("tcp", addr); err == nil {
			_ = l.Close()
			out = append(out, addr)
		}
	}
	if len(out) < n {
		t.Skipf("only %d free ports in 20400-20419", len(out))
	}
	return out
}

func refuses(addr string) bool {
	c, err := net.DialTimeout("tcp", addr, time.Second)
	if err == nil {
		_ = c.Close()
	}
	return err != nil
}

// upEnv keeps up's first-run notice and config out of the user's files.
func upEnv(t *testing.T) {
	t.Helper()
	t.Chdir(t.TempDir())
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("XDG_CONFIG_HOME", home)
}

// TestUpBindsEveryPortFirst: when the SMTP or proxy port is taken, up
// prints only the error (no API line) and leaves no listener behind.
func TestUpBindsEveryPortFirst(t *testing.T) {
	upEnv(t)
	ports := freePorts(t, 3)
	api, smtp, prx := ports[0], ports[1], ports[2]
	for _, tc := range []struct {
		name, taken, want string
		args              []string
	}{
		{"smtp", smtp, "SMTP on " + smtp, []string{"--smtp", smtp, "--proxy", "off"}},
		{"proxy", prx, "proxy on " + prx, []string{"--smtp", smtp, "--proxy", prx}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			busy, err := net.Listen("tcp", tc.taken)
			if err != nil {
				t.Fatal(err)
			}
			defer func() { _ = busy.Close() }()
			code, out, errOut := runCLI(t, append([]string{"up", "--plain", "--listen", api}, tc.args...)...)
			if code != exitError || !strings.Contains(errOut, tc.want) {
				t.Fatalf("exit %d, stderr %q; want 3 and %q", code, errOut, tc.want)
			}
			if strings.Contains(out+errOut, "API") || strings.Contains(out+errOut, "Waiting") {
				t.Errorf("printed the banner before failing:\n%s%s", out, errOut)
			}
			if !refuses(api) {
				t.Error("the API listener is still open")
			}
			if tc.taken != smtp && !refuses(smtp) {
				t.Error("the SMTP listener is still open")
			}
		})
	}
}

// syncBuffer is a bytes.Buffer safe for the server goroutines to write.
type syncBuffer struct {
	mu sync.Mutex
	b  bytes.Buffer
}

func (b *syncBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.b.Write(p)
}

func (b *syncBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.b.String()
}

// startUp runs cmdUp with args until the returned stop is called, and
// waits until the API answers.
func startUp(t *testing.T, args ...string) (out, errOut *syncBuffer, stop func() int) {
	t.Helper()
	p, err := parse(append([]string{"--plain"}, args...))
	if err != nil {
		t.Fatal(err)
	}
	out, errOut = &syncBuffer{}, &syncBuffer{}
	s := p.begin(out, errOut)
	ctx, cancel := context.WithCancel(context.Background())
	s.ctx = ctx
	done := make(chan int, 1)
	go func() { done <- cmdUp(s) }()
	stop = func() int {
		cancel()
		select {
		case code := <-done:
			s.stop()
			return code
		case <-time.After(10 * time.Second):
			t.Fatal("up didn't stop")
			return -1
		}
	}
	for i := 0; ; i++ {
		resp, err := http.Get("http://" + p.cfg.Listen + "/v1/health") //nolint:noctx // test
		if err == nil {
			_ = resp.Body.Close()
			break
		}
		if i > 100 {
			stop()
			t.Fatalf("up never answered: %v\n%s", err, errOut)
		}
		time.Sleep(20 * time.Millisecond)
	}
	return out, errOut, stop
}

func TestUpStopsEveryListener(t *testing.T) {
	upEnv(t)
	ports := freePorts(t, 3)
	api, smtp, prx := ports[0], ports[1], ports[2]
	out, _, stop := startUp(t, "--listen", api, "--smtp", smtp, "--proxy", prx)
	for _, want := range []string{"API: http://" + api, "SMTP: " + smtp, "Proxy: " + prx} {
		if !strings.Contains(out.String(), want) {
			t.Errorf("banner lacks %q:\n%s", want, out)
		}
	}
	for _, addr := range ports {
		if refuses(addr) {
			t.Errorf("%s isn't listening", addr)
		}
	}
	if code := stop(); code != exitOK {
		t.Errorf("exit %d", code)
	}
	for _, addr := range ports {
		if !refuses(addr) {
			t.Errorf("%s still accepts connections after up stopped", addr)
		}
	}
}

// pipeListener hands out the server ends of net.Pipe connections, so the
// proxy can be served without a port.
type pipeListener struct {
	ch     chan net.Conn
	done   chan struct{}
	closed sync.Once
}

func newPipeListener() *pipeListener {
	return &pipeListener{ch: make(chan net.Conn), done: make(chan struct{})}
}

func (l *pipeListener) dial() (net.Conn, error) {
	client, server := net.Pipe()
	select {
	case l.ch <- server:
		return client, nil
	case <-l.done:
		return nil, net.ErrClosed
	}
}

func (l *pipeListener) Accept() (net.Conn, error) {
	select {
	case c := <-l.ch:
		return c, nil
	case <-l.done:
		return nil, net.ErrClosed
	}
}

func (l *pipeListener) Close() error {
	l.closed.Do(func() { close(l.done) })
	return nil
}

func (l *pipeListener) Addr() net.Addr { return &net.TCPAddr{IP: net.IPv4(127, 0, 0, 1)} }

func TestStartProxyServesOnTheListener(t *testing.T) {
	upEnv(t)
	p, err := parse([]string{"--plain", "--proxy", "127.0.0.1:20419"})
	if err != nil {
		t.Fatal(err)
	}
	var out, errOut syncBuffer
	s := p.begin(&out, &errOut)
	defer s.stop()
	s.view = report.NewPretty(&out, &errOut, report.PrettyOptions{Plain: true})
	inbox := catcher.NewInbox(10)
	srv := catcher.NewServer(inbox, catcher.Config{Mode: catcher.Seal})
	ln := newPipeListener()
	line, stop, err := startProxy(s, srv, ln)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(line, "127.0.0.1:20419  (HTTPS_PROXY") {
		t.Errorf("line = %q", line)
	}

	// A sealed plain-HTTP webhook is faked and recorded; nothing is dialed.
	c, err := ln.dial()
	if err != nil {
		t.Fatal(err)
	}
	_ = c.SetDeadline(time.Now().Add(5 * time.Second))
	body := `{"event":"order.created"}`
	_, _ = io.WriteString(c, "POST http://hooks.example.com/order HTTP/1.1\r\nHost: hooks.example.com\r\n"+
		"Content-Type: application/json\r\nContent-Length: "+fmt.Sprint(len(body))+"\r\nConnection: close\r\n\r\n"+body)
	resp, err := http.ReadResponse(bufio.NewReader(c), nil)
	if err != nil {
		t.Fatal(err)
	}
	_ = resp.Body.Close()
	_ = c.Close()
	if resp.StatusCode != http.StatusOK || resp.Header.Get("X-Echo-Id") == "" {
		t.Errorf("proxied webhook: %s %v", resp.Status, resp.Header)
	}
	if ms := inbox.List(catcher.Filter{}); len(ms) != 1 || ms[0].Source.Via != "proxy" {
		t.Errorf("inbox = %+v", ms)
	}

	stop(context.Background())
	if _, err := ln.dial(); err == nil {
		t.Error("the listener still accepts after stop")
	}
}

// TestLiveShowsContentChecks: the live view and the live log show what
// the content checks find, and a clean email shows nothing.
func TestLiveShowsContentChecks(t *testing.T) {
	post := func(t *testing.T, h http.Handler, subject, text string) {
		t.Helper()
		body := fmt.Sprintf(`{"v":1,"kind":"email","source":{"via":"wordpress"},"email":{"from":"shop@shop.example.com","to":["ann@example.org"],"subject":%q,"text":%q}}`,
			subject, text)
		r := httptest.NewRequest(http.MethodPost, "/v1/capture", strings.NewReader(body))
		r.Header.Set("Content-Type", "application/json")
		if w := serve(h, r); w.Code != http.StatusOK {
			t.Fatalf("capture: %d %s", w.Code, w.Body)
		}
	}
	setup := func() (http.Handler, *bytes.Buffer) {
		h, _, inbox := testUp("")
		var log bytes.Buffer
		l := newLiveLog(report.NewPretty(&log, &log, report.PrettyOptions{Plain: true}))
		l.checks = check.Options{SiteHost: "shop.example.com"}
		inbox.OnAdd(l.add)
		return h, &log
	}

	t.Run("problems", func(t *testing.T) {
		h, log := setup()
		post(t, h, "Welcome, {first_name}", "Confirm here: http://localhost/confirm?k=1")
		page := serve(h, httptest.NewRequest(http.MethodGet, "/", nil)).Body.String()
		for _, want := range []string{"Unfilled placeholder {first_name} in email", "Email links to a development address (localhost)"} {
			if !strings.Contains(page, want) {
				t.Errorf("live view lacks %q", want)
			}
		}
		if strings.Contains(strings.ToLower(page), "all clear") {
			t.Error("live view says all clear")
		}
		if got := log.String(); !strings.Contains(got, "Issues: unfilled {first_name}, dev link localhost\n") {
			t.Errorf("live log:\n%s", got)
		}
	})
	t.Run("clean", func(t *testing.T) {
		h, log := setup()
		post(t, h, "Welcome, Ann", "Confirm here: https://shop.example.com/confirm?k=1")
		page := serve(h, httptest.NewRequest(http.MethodGet, "/", nil)).Body.String()
		if strings.Contains(page, "Unfilled placeholder") || strings.Contains(page, "development address") ||
			!strings.Contains(strings.ToLower(page), "all clear") {
			t.Error("a clean email shows problems")
		}
		if got := log.String(); strings.Contains(got, "Issues:") || !strings.Contains(got, "Welcome, Ann") {
			t.Errorf("live log:\n%s", got)
		}
	})
}

func TestMarkers(t *testing.T) {
	m := model.Message{ID: "m1", Channel: model.Email, Subject: "", Text: "Hi {first_name} {first_name}, see http://shop.test/ \nWarning: Undefined variable $x in /var/www/a.php on line 3",
		HTML: "<p>x</p>", Status: model.Failed}
	got := markers(check.Message(m, check.Options{}))
	if got != "unfilled {first_name}, PHP error, dev link shop.test, no subject" {
		t.Errorf("markers = %q", got)
	}
	if got := markers(nil); got != "" {
		t.Errorf("no issues: %q", got)
	}
}

func TestLiveLogReprintsChangedIssues(t *testing.T) {
	var out bytes.Buffer
	l := newLiveLog(report.NewPretty(&out, &out, report.PrettyOptions{Plain: true}))
	m := model.Message{ID: "m1", Channel: model.Email, To: []string{"ann@example.org"}, Subject: "Hi {name}", Text: "x", Status: model.Faked}
	l.add(m)
	l.add(m)
	if n := strings.Count(out.String(), "Issues: unfilled {name}"); n != 1 {
		t.Errorf("issues printed %d times:\n%s", n, out.String())
	}
	m.Text = "<?php error: Fatal error: boom in /x.php on line 1"
	l.add(m) // same status, new problem
	if !strings.Contains(out.String(), "Issues: unfilled {name}, PHP error") {
		t.Errorf("changed issues not printed:\n%s", out.String())
	}
}
