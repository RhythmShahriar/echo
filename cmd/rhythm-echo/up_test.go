// Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
// SPDX-License-Identifier: MIT

package main

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"

	"github.com/RhythmShahriar/echo/catcher"
	"github.com/RhythmShahriar/echo/check"
	"github.com/RhythmShahriar/echo/config"
	"github.com/RhythmShahriar/echo/model"
	"github.com/RhythmShahriar/echo/report"
	"github.com/RhythmShahriar/echo/web"
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
	checks := check.Options{SiteHost: "shop.example.com"}
	ui := web.New(web.Options{Inbox: inbox, Mode: catcher.Seal, Checks: checks, AnyHost: token != ""})
	routes := upRoutes(srv, inbox, ui, catcher.Seal, checks)
	return upHandler(routes, token, nil), routes, inbox
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
		r := httptest.NewRequest(http.MethodGet, rt.probe, nil)
		r.Host = "127.0.0.1:8025"
		if w := serve(h, r); w.Code != http.StatusOK {
			t.Errorf("%s: %d", rt.pattern, w.Code)
		}
	}
	// Without a token, Echo Inbox answers only to loopback names, so a
	// DNS-rebinding page can't read it.
	for _, path := range []string{"/", web.PathMessages} {
		r := httptest.NewRequest(http.MethodGet, path, nil)
		r.Host = "rebind.evil.example:8025"
		if w := serve(h, r); w.Code != http.StatusForbidden {
			t.Errorf("%s for Host rebind.evil.example: %d, want 403", path, w.Code)
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
			// The API serves before the banner is printed: wait for that too.
			if strings.Contains(out.String(), "API:") {
				break
			}
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
	line, notes, stop, err := startProxy(s, srv, ln)
	if err != nil {
		t.Fatal(err)
	}
	if len(notes) != 0 {
		t.Errorf("notes without --mitm = %v", notes)
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

// TestLiveShowsContentChecks: Echo Inbox, the session report and the
// live log show what the content checks find; a clean email shows nothing.
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
		page := serve(h, httptest.NewRequest(http.MethodGet, "/report", nil)).Body.String()
		for _, want := range []string{"Unfilled placeholder {first_name} in email", "Email links to a development address (localhost)"} {
			if !strings.Contains(page, want) {
				t.Errorf("live view lacks %q", want)
			}
		}
		if strings.Contains(strings.ToLower(page), "all clear") {
			t.Error("live view says all clear")
		}
		r := httptest.NewRequest(http.MethodGet, web.PathMessages, nil)
		r.Host = "127.0.0.1:8025"
		var list web.Page
		if err := json.Unmarshal(serve(h, r).Body.Bytes(), &list); err != nil || len(list.Messages) != 1 || list.Messages[0].Warnings+list.Messages[0].Errors < 2 {
			t.Errorf("inbox list: %+v %v", list.Messages, err)
		}
		if got := log.String(); !strings.Contains(got, "Issues: unfilled {first_name}, dev link localhost\n") {
			t.Errorf("live log:\n%s", got)
		}
	})
	t.Run("clean", func(t *testing.T) {
		h, log := setup()
		post(t, h, "Welcome, Ann", "Confirm here: https://shop.example.com/confirm?k=1")
		page := serve(h, httptest.NewRequest(http.MethodGet, "/report", nil)).Body.String()
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

// fakeMailpit serves Mailpit's /api/v1/info and page on addr and its
// SMTP greeting on smtp when that isn't "".
func fakeMailpit(t *testing.T, addr, smtp string) {
	t.Helper()
	ln, err := net.Listen("tcp", addr)
	if err != nil {
		t.Fatal(err)
	}
	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/v1/info", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"Version":"v1.21.0","LatestVersion":"","Database":"/data/mailpit.db","DatabaseSize":4096,"Messages":0,"Unread":0,"Tags":{},"RuntimeStats":{"Uptime":12}}`)
	})
	srv := &http.Server{Handler: mux, ReadHeaderTimeout: time.Second}
	go func() { _ = srv.Serve(ln) }()
	t.Cleanup(func() { _ = srv.Close() })
	if smtp != "" {
		fakeBanner(t, smtp, "220 4f2a Mailpit ESMTP Service ready\r\n")
	}
}

// fakeBanner greets every connection on addr with banner.
func fakeBanner(t *testing.T, addr, banner string) {
	t.Helper()
	l, err := net.Listen("tcp", addr)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = l.Close() })
	go func() {
		for {
			c, err := l.Accept()
			if err != nil {
				return
			}
			_, _ = io.WriteString(c, banner)
			_ = c.Close()
		}
	}()
}

// moveFreeBase points the free-pair search at ports these tests may use.
func moveFreeBase(t *testing.T) {
	t.Helper()
	old := freeBase
	freeBase = [2]int{20410, 20415}
	t.Cleanup(func() { freeBase = old })
}

func TestUpNamesMailpitOnItsPorts(t *testing.T) {
	upEnv(t)
	moveFreeBase(t)
	ports := freePorts(t, 2)
	fakeMailpit(t, ports[0], ports[1])
	api, smtp, ok := nextFreePair("127.0.0.1", true)
	if !ok {
		t.Skip("no free pair")
	}
	code, out, errOut := runCLI(t, "up", "--plain", "--listen", ports[0], "--smtp", ports[1])
	want := "Mailpit is already on " + ports[0] + "; run Echo next to it: rhythm echo up --listen " + api + " --smtp " + smtp
	if code != exitError || !strings.Contains(errOut, want) {
		t.Fatalf("exit %d, stderr %q; want 3 and %q", code, errOut, want)
	}
	if strings.Contains(errOut, "doctor") || strings.Contains(out, "API") {
		t.Errorf("blamed Echo or printed the banner:\n%s%s", out, errOut)
	}
	if _, err := os.Stat(".echo/config.yaml"); err == nil {
		t.Error("wrote the config without being asked")
	}
}

func TestUpNamesAnUnknownProgram(t *testing.T) {
	upEnv(t)
	moveFreeBase(t)
	ports := freePorts(t, 2)
	busy, err := net.Listen("tcp", ports[0])
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = busy.Close() }()
	code, _, errOut := runCLI(t, "up", "--plain", "--listen", ports[0], "--smtp", ports[1])
	if code != exitError || !strings.Contains(errOut, "listening on "+ports[0]) || !strings.Contains(errOut, "(another program has this port); run Echo next to it: rhythm echo up --listen 127.0.0.1:20410") {
		t.Fatalf("exit %d, stderr %q", code, errOut)
	}
}

func TestIdentifyBusyPorts(t *testing.T) {
	ports := freePorts(t, 8)
	page := func(addr, body string) {
		ln, err := net.Listen("tcp", addr)
		if err != nil {
			t.Fatal(err)
		}
		srv := &http.Server{ReadHeaderTimeout: time.Second, Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.URL.Path != "/" {
				http.NotFound(w, r)
				return
			}
			_, _ = io.WriteString(w, body)
		})}
		go func() { _ = srv.Serve(ln) }()
		t.Cleanup(func() { _ = srv.Close() })
	}
	fakeMailpit(t, ports[0], "")
	page(ports[1], "<!doctype html><title>MailHog</title>")
	page(ports[2], "<html><head><title>MailDev</title></head></html>")
	page(ports[3], "<html><title>Shop admin</title></html>")
	fakeBanner(t, ports[4], "220 mailhog.example ESMTP MailHog\r\n")
	fakeBanner(t, ports[5], "220 echo.local ESMTP Service Ready\r\n")
	fakeBanner(t, ports[6], "220 mx.shop.test ESMTP Postfix\r\n")
	h, _ := testUpHandler(testToken)
	ln, err := net.Listen("tcp", ports[7])
	if err != nil {
		t.Fatal(err)
	}
	echo := &http.Server{Handler: h, ReadHeaderTimeout: time.Second}
	go func() { _ = echo.Serve(ln) }()
	defer func() { _ = echo.Close() }()
	for _, c := range []struct {
		addr string
		smtp bool
		want string
	}{
		{ports[0], false, "Mailpit"},
		{ports[1], false, "MailHog"},
		{ports[2], false, "MailDev"},
		{ports[3], false, ""},
		{ports[4], true, "MailHog"},
		{ports[5], true, "Echo"},
		{ports[6], true, ""},
		{ports[7], false, "Echo"}, // with a token: the 401 names X-Echo-Token
		{strings.Replace(ports[0], "127.0.0.1", "0.0.0.0", 1), false, "Mailpit"},
	} {
		if got := identify(c.addr, c.smtp); got != c.want {
			t.Errorf("identify(%s, smtp=%v) = %q, want %q", c.addr, c.smtp, got, c.want)
		}
	}
}

// TestAddrInUseOnWindows: Windows fails a bind with WSAEADDRINUSE, so
// the busy-port help must match it there and only there.
func TestAddrInUseOnWindows(t *testing.T) {
	bind := func(errno syscall.Errno) error {
		return &net.OpError{Op: "listen", Net: "tcp", Err: os.NewSyscallError("bind", errno)}
	}
	for _, c := range []struct {
		err  error
		goos string
		want bool
	}{
		{bind(syscall.EADDRINUSE), "linux", true},
		{bind(syscall.EADDRINUSE), "darwin", true},
		{bind(wsaeaddrinuse), "windows", true},
		{bind(wsaeaddrinuse), "linux", false},
		{bind(syscall.EACCES), "windows", false},
		{bind(syscall.EACCES), "linux", false},
		{fmt.Errorf("no such host"), "windows", false},
	} {
		if got := addrInUse(c.err, c.goos); got != c.want {
			t.Errorf("addrInUse(%v, %s) = %v, want %v", c.err, c.goos, got, c.want)
		}
	}
}

func TestNextFreePairSkipsBusyPorts(t *testing.T) {
	moveFreeBase(t)
	for _, p := range []string{"127.0.0.1:20410", "127.0.0.1:20416"} {
		l, err := net.Listen("tcp", p)
		if err != nil {
			t.Skipf("%s: %v", p, err)
		}
		defer func() { _ = l.Close() }()
	}
	api, smtp, ok := nextFreePair("127.0.0.1", true)
	if !ok || api != "127.0.0.1:20412" || smtp != "127.0.0.1:20417" {
		t.Errorf("pair = %s %s %v, want 20412 and 20417 (20410 and 20416 are taken)", api, smtp, ok)
	}
	if api, _, _ := nextFreePair("127.0.0.1", false); api != "127.0.0.1:20411" {
		t.Errorf("without SMTP: %s, want 20411", api)
	}
}

func TestSetAddressesKeepsTheRestOfTheFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, ".echo", "config.yaml")
	if err := os.MkdirAll(filepath.Dir(path), 0o750); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(config.Template), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := setAddresses(path, "127.0.0.1:18025", "127.0.0.1:11025"); err != nil {
		t.Fatal(err)
	}
	cfg, err := config.Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Listen != "127.0.0.1:18025" || cfg.SMTP != "127.0.0.1:11025" {
		t.Errorf("listen %s, smtp %s", cfg.Listen, cfg.SMTP)
	}
	got, _ := os.ReadFile(path)
	want := strings.Replace(strings.Replace(config.Template,
		"listen: 127.0.0.1:8025  # API", "listen: 127.0.0.1:18025 # API", 1),
		"smtp: 127.0.0.1:1025    # SMTP", "smtp: 127.0.0.1:11025   # SMTP", 1)
	if string(got) != want {
		t.Errorf("file changed beyond the two lines:\n%s", got)
	}

	// No file yet: one is made with just the addresses.
	fresh := filepath.Join(dir, "new", "config.yaml")
	if err := setAddresses(fresh, "127.0.0.1:18026", ""); err != nil {
		t.Fatal(err)
	}
	if got, _ := os.ReadFile(fresh); string(got) != "listen: 127.0.0.1:18026\n" {
		t.Errorf("new file = %q", got)
	}

	// IPv6 addresses are quoted: unquoted, [::1] starts a YAML sequence.
	if err := setAddresses(path, "[::1]:18025", "[::1]:11025"); err != nil {
		t.Fatal(err)
	}
	if cfg, err := config.Load(path); err != nil || cfg.Listen != "[::1]:18025" || cfg.SMTP != "[::1]:11025" {
		t.Errorf("IPv6: %+v %v", cfg, err)
	}
	if got, _ := os.ReadFile(path); !strings.Contains(string(got), `listen: "[::1]:18025"   # API`) {
		t.Errorf("IPv6 line:\n%s", got)
	}
	if left, _ := filepath.Glob(filepath.Join(dir, ".echo", ".config.yaml.*")); len(left) > 0 {
		t.Errorf("temporary files left: %v", left)
	}
}

// TestSetAddressesKeepsAFileItCantFix: when the new text wouldn't load,
// the user's file is left exactly as it was.
func TestSetAddressesKeepsAFileItCantFix(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.yaml")
	orig := "mode: seal\nunknown_key: 1\n" // already broken: KnownFields refuses it
	if err := os.WriteFile(path, []byte(orig), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := setAddresses(path, "127.0.0.1:18025", ""); err == nil || !strings.Contains(err.Error(), "left as it was") {
		t.Errorf("err = %v", err)
	}
	if got, _ := os.ReadFile(path); string(got) != orig {
		t.Errorf("file changed: %q", got)
	}
	if left, _ := filepath.Glob(filepath.Join(filepath.Dir(path), ".config.yaml.*")); len(left) > 0 {
		t.Errorf("temporary files left: %v", left)
	}
}

func TestYAMLScalar(t *testing.T) {
	for in, want := range map[string]string{
		"127.0.0.1:8025": "127.0.0.1:8025", "localhost:8025": "localhost:8025", "off": "off",
		"[::1]:8025": `"[::1]:8025"`, "0.0.0.0: x": `"0.0.0.0: x"`, "a #b": `"a #b"`,
	} {
		if got := yamlScalar(in); got != want {
			t.Errorf("yamlScalar(%q) = %s, want %s", in, got, want)
		}
	}
}

func TestUpMovesAsideWhenAsked(t *testing.T) {
	upEnv(t)
	moveFreeBase(t)
	ports := freePorts(t, 2)
	fakeMailpit(t, ports[0], ports[1])
	if err := os.WriteFile("artisan", []byte("#!/usr/bin/env php\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	var asked string
	old := askFreePorts
	askFreePorts = func(_ *session, q string) bool { asked = q; return true }
	t.Cleanup(func() { askFreePorts = old })
	api, smtp, ok := nextFreePair("127.0.0.1", true)
	if !ok {
		t.Skip("no free pair")
	}

	p, err := parse([]string{"--plain", "--listen", ports[0], "--smtp", ports[1]})
	if err != nil {
		t.Fatal(err)
	}
	var out, errOut syncBuffer
	s := p.begin(&out, &errOut)
	ctx, cancel := context.WithCancel(context.Background())
	s.ctx = ctx
	done := make(chan int, 1)
	go func() { done <- cmdUp(s) }()
	for i := 0; !strings.Contains(out.String(), "API:"); i++ {
		if i > 200 {
			cancel()
			t.Fatalf("up never started:\n%s%s", out.String(), errOut.String())
		}
		time.Sleep(20 * time.Millisecond)
	}
	resp, err := http.Get("http://" + api + "/v1/health") //nolint:noctx // test
	if err != nil {
		t.Fatalf("Echo isn't on %s: %v", api, err)
	}
	_ = resp.Body.Close()
	cancel()
	if code := <-done; code != exitOK {
		t.Errorf("exit %d", code)
	}
	s.stop()

	if !strings.Contains(asked, "Mailpit is already on "+ports[0]+". Run Echo on "+api+" and "+smtp+" instead and save them to .echo/config.yaml?") {
		t.Errorf("asked %q", asked)
	}
	for _, want := range []string{
		"Mailpit is already on " + ports[0] + ". Echo runs on " + api + " and " + smtp + " instead.",
		"Saved to .echo/config.yaml",
		"  RHYTHM_ECHO_URL=http://" + api,
		"  RHYTHM_ECHO_SMTP=" + smtp,
		"  MAIL_PORT=" + strings.TrimPrefix(smtp, "127.0.0.1:"),
	} {
		if !strings.Contains(errOut.String(), want) {
			t.Errorf("stderr lacks %q:\n%s", want, errOut.String())
		}
	}
	if !strings.Contains(out.String(), "API: http://"+api) || !strings.Contains(out.String(), "SMTP: "+smtp) {
		t.Errorf("banner:\n%s", out.String())
	}
	cfg, err := config.Load(".echo/config.yaml")
	if err != nil || cfg.Listen != api || cfg.SMTP != smtp {
		t.Errorf("saved config: %+v, %v", cfg, err)
	}
}

func TestEnvLinesPerStack(t *testing.T) {
	eqLines := func(got []string, want ...string) {
		t.Helper()
		if strings.Join(got, "\n") != strings.Join(want, "\n") {
			t.Errorf("got\n%s\nwant\n%s", strings.Join(got, "\n"), strings.Join(want, "\n"))
		}
	}
	base := []string{"RHYTHM_ECHO_URL=http://127.0.0.1:18025", "RHYTHM_ECHO_SMTP=127.0.0.1:11025"}
	eqLines(envLines("laravel", "127.0.0.1:18025", "127.0.0.1:11025", "/bin/rhythm-echo"),
		append(base, "MAIL_MAILER=smtp", "MAIL_HOST=127.0.0.1", "MAIL_PORT=11025   (Laravel .env)")...)
	eqLines(envLines("wordpress", "127.0.0.1:18025", "127.0.0.1:11025", "/bin/rhythm-echo"),
		append(base, `sendmail_path = "RHYTHM_ECHO_SMTP=127.0.0.1:11025 /bin/rhythm-echo sendmail -t -i"   (php.ini)`)...)
	eqLines(envLines("next", "127.0.0.1:18025", "127.0.0.1:11025", ""), base...)
	eqLines(envLines("laravel", "127.0.0.1:18025", "", ""), base[0])
}

func TestGuessStack(t *testing.T) {
	for _, c := range []struct{ file, want string }{
		{"wp-config.php", "wordpress"}, {"artisan", "laravel"}, {"next.config.mjs", "next"},
		{"package.json", "node"}, {"index.php", "php"}, {"README.md", ""},
	} {
		dir := t.TempDir()
		if err := os.WriteFile(filepath.Join(dir, c.file), nil, 0o600); err != nil {
			t.Fatal(err)
		}
		if got := guessStack(dir); got != c.want {
			t.Errorf("%s: %q, want %q", c.file, got, c.want)
		}
	}
}

// containerHost is what a Docker container sees: loopback, its bridge
// address and a link-local IPv6 address.
var containerHost = []netIface{
	{"lo", []net.IP{net.ParseIP("127.0.0.1"), net.ParseIP("::1")}},
	{"eth0", []net.IP{net.ParseIP("172.17.0.2"), net.ParseIP("fe80::42:acff:fe11:2")}},
}

// publicHost is a server with a public address next to Docker's bridge.
var publicHost = []netIface{
	{"lo", []net.IP{net.ParseIP("127.0.0.1")}},
	{"eth0", []net.IP{net.ParseIP("203.0.113.5")}},
	{"docker0", []net.IP{net.ParseIP("100.64.0.1")}},
}

// cgnatHost has a carrier-grade NAT address on an ordinary interface,
// which other customers of the same carrier can reach.
var cgnatHost = []netIface{
	{"lo", []net.IP{net.ParseIP("127.0.0.1")}},
	{"eth0", []net.IP{net.ParseIP("100.64.0.9")}},
}

// publicBridge is a Docker network given a public range: the bridge's
// name does not make it private.
var publicBridge = []netIface{
	{"lo", []net.IP{net.ParseIP("127.0.0.1")}},
	{"eth0", []net.IP{net.ParseIP("10.0.0.4")}},
	{"br-3f2a", []net.IP{net.ParseIP("198.51.100.1")}},
}

func TestPrivateOnly(t *testing.T) {
	lookup := func(host string) ([]net.IP, error) {
		switch host {
		case "echo":
			return []net.IP{net.ParseIP("10.0.0.7")}, nil
		case "echo.shop.test":
			return []net.IP{net.ParseIP("10.0.0.7"), net.ParseIP("198.51.100.9")}, nil
		}
		return nil, fmt.Errorf("no such host %s", host)
	}
	for _, c := range []struct {
		addr   string
		ifaces []netIface
		want   string // "" for allowed
	}{
		{"0.0.0.0:8025", containerHost, ""},
		{":8025", containerHost, ""},
		{"[::]:8025", containerHost, ""},
		{"172.17.0.2:8025", containerHost, ""},
		{"192.168.1.20:8025", publicHost, ""},
		{"[fd00::5]:8025", publicHost, ""},
		{"100.64.0.1:8025", publicHost, ""},                    // not RFC 1918; allowed as it is on docker0
		{"100.64.0.9:8025", cgnatHost, "(100.64.0.9 on eth0)"}, // shared space off a container bridge
		{"0.0.0.0:8025", publicBridge, "(198.51.100.1 on br-3f2a)"},
		{"198.51.100.1:8025", publicBridge, "(198.51.100.1 on br-3f2a)"},
		{"echo:8025", publicHost, ""},
		{"0.0.0.0:8025", publicHost, "--listen 0.0.0.0:8025 is reachable from a public address (203.0.113.5 on eth0)"},
		{"203.0.113.5:8025", publicHost, "(203.0.113.5 on eth0); --private-network allows only private (RFC 1918) and container networks"},
		{"echo.shop.test:8025", publicHost, "(198.51.100.9)"},
		{"nowhere:8025", publicHost, "no such host nowhere"},
	} {
		err := privateOnly("--listen", c.addr, c.ifaces, lookup)
		if c.want == "" && err != nil || c.want != "" && (err == nil || !strings.Contains(err.Error(), c.want)) {
			t.Errorf("%s: err = %v, want %q", c.addr, err, c.want)
		}
	}
}

// TestPrivateNetworkRefusesAPublicInterface: with --private-network on, a
// wildcard listen on a machine with a public address is still refused,
// before anything is bound.
func TestPrivateNetworkRefusesAPublicInterface(t *testing.T) {
	t.Chdir(t.TempDir())
	t.Setenv("RHYTHM_ECHO_TOKEN", "")
	t.Setenv(privateNetworkEnv, "1")
	orig := listIfaces
	listIfaces = func() ([]netIface, error) { return publicHost, nil }
	t.Cleanup(func() { listIfaces = orig })
	code, _, errOut := runCLI(t, "up", "--listen", "0.0.0.0:20400", "--smtp", "off")
	if code != exitUsage || !strings.Contains(errOut, "--listen 0.0.0.0:20400 is reachable from a public address (203.0.113.5 on eth0)") {
		t.Errorf("exit %d: %s", code, errOut)
	}
	// Without --private-network the old refusal stands.
	t.Setenv(privateNetworkEnv, "")
	code, _, errOut = runCLI(t, "up", "--listen", "0.0.0.0:20400", "--smtp", "off")
	if code != exitUsage || !strings.Contains(errOut, "--listen 0.0.0.0:20400 is reachable from other machines; set --token") {
		t.Errorf("exit %d: %s", code, errOut)
	}
}

// testLoginUp is up's handler with a viewer login, with or without a token.
func testLoginUp(t *testing.T, token string) http.Handler {
	t.Helper()
	inbox := catcher.NewInbox(100)
	inbox.Add(model.Message{ID: "m1", Channel: model.Email, Subject: "Order 1042", Status: model.Faked})
	srv := catcher.NewServer(inbox, catcher.Config{Mode: catcher.Seal, Token: token, Version: version})
	ui := web.New(web.Options{Inbox: inbox, Mode: catcher.Seal, AnyHost: true})
	login, err := web.ParseLogin("client:correct horse")
	if err != nil {
		t.Fatal(err)
	}
	return upHandler(upRoutes(srv, inbox, ui, catcher.Seal, check.Options{}), token, login)
}

func viewerSession(t *testing.T, h http.Handler) *http.Cookie {
	t.Helper()
	r := httptest.NewRequest(http.MethodPost, web.PathLogin, strings.NewReader("user=client&password=correct+horse&next=%2F"))
	r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	w := serve(h, r)
	for _, c := range w.Result().Cookies() {
		if c.Name == web.SessionCookie {
			return c
		}
	}
	t.Fatalf("no session: %d %s", w.Code, w.Body)
	return nil
}

// TestLoginGivesViewersTheInboxOnly: with a token and a login, a viewer
// reads Echo Inbox and nothing else. Deleting is refused.
func TestLoginGivesViewersTheInboxOnly(t *testing.T) {
	h := testLoginUp(t, testToken)
	get := func(path string, c *http.Cookie, header bool) *httptest.ResponseRecorder {
		r := httptest.NewRequest(http.MethodGet, path, nil)
		if c != nil {
			r.AddCookie(c)
		}
		if header {
			r.Header.Set(wire.TokenHeader, testToken)
		}
		return serve(h, r)
	}
	if w := get("/", nil, false); w.Code != http.StatusFound || !strings.HasPrefix(w.Header().Get("Location"), "/login?next=") {
		t.Errorf("GET / without anything = %d %q", w.Code, w.Header().Get("Location"))
	}
	if w := get("/v1/messages", nil, false); w.Code != http.StatusUnauthorized {
		t.Errorf("API without the token = %d", w.Code)
	}
	c := viewerSession(t, h)
	if w := get("/", c, false); w.Code != 200 || !strings.Contains(w.Body.String(), "Echo Inbox") {
		t.Errorf("GET / signed in = %d", w.Code)
	}
	w := get(web.PathMessages, c, false)
	var page web.Page
	if err := json.Unmarshal(w.Body.Bytes(), &page); err != nil || !page.ReadOnly || len(page.Messages) != 1 {
		t.Errorf("list signed in = %d %s", w.Code, w.Body)
	}
	if w := get("/report", c, false); w.Code != 200 {
		t.Errorf("report signed in = %d", w.Code)
	}
	if w := get("/v1/messages", c, false); w.Code != http.StatusUnauthorized {
		t.Errorf("a viewer reading the API = %d", w.Code)
	}
	r := httptest.NewRequest(http.MethodDelete, web.PathMessages+"/m1", nil)
	r.AddCookie(c)
	if w := serve(h, r); w.Code != http.StatusForbidden {
		t.Errorf("a viewer deleting = %d", w.Code)
	}
	// The token still opens everything, read-write.
	if w := get(web.PathMessages, nil, true); w.Code != 200 || strings.Contains(w.Body.String(), "read_only") {
		t.Errorf("list with the token = %d %s", w.Code, w.Body)
	}
}

// TestLoginWithoutAToken: the Inbox needs the login, the API stays open
// to connectors on this machine as before.
func TestLoginWithoutAToken(t *testing.T) {
	h := testLoginUp(t, "")
	if w := serve(h, httptest.NewRequest(http.MethodGet, "/", nil)); w.Code != http.StatusFound {
		t.Errorf("GET / = %d", w.Code)
	}
	if w := serve(h, httptest.NewRequest(http.MethodGet, "/v1/messages", nil)); w.Code != 200 {
		t.Errorf("API = %d", w.Code)
	}
	c := viewerSession(t, h)
	r := httptest.NewRequest(http.MethodGet, web.PathMessages, nil)
	r.AddCookie(c)
	if w := serve(h, r); w.Code != 200 || !strings.Contains(w.Body.String(), `"read_only":true`) {
		t.Errorf("list signed in = %d %s", w.Code, w.Body)
	}
}

func TestLoginFromEnv(t *testing.T) {
	t.Setenv(loginEnv, "")
	if l, err := loginFromEnv(); l != nil || err != nil {
		t.Errorf("unset: %v %v", l, err)
	}
	t.Setenv(loginEnv, "client:short")
	if _, err := loginFromEnv(); err == nil || err.Error() != "RHYTHM_ECHO_LOGIN: the password must be at least 8 characters" {
		t.Errorf("short password: %v", err)
	}
	t.Setenv(loginEnv, "client:correct horse")
	if l, err := loginFromEnv(); err != nil || l.User() != "client" {
		t.Errorf("good: %v %v", l, err)
	}
}

// TestUpWithLogin: RHYTHM_ECHO_LOGIN puts the Inbox behind the sign-in
// page and says so in the banner.
func TestUpWithLogin(t *testing.T) {
	upEnv(t)
	t.Setenv(loginEnv, "client:correct horse")
	api := freePorts(t, 1)[0]
	out, _, stop := startUp(t, "--listen", api, "--smtp", "off")
	defer stop()
	want := "Login: viewers sign in as client at http://" + api + "/login (read only, for 8 hours)"
	if !strings.Contains(out.String(), want) {
		t.Errorf("banner lacks %q:\n%s", want, out)
	}
	c := &http.Client{CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	resp, err := c.Get("http://" + api + "/") //nolint:noctx // test
	if err != nil {
		t.Fatal(err)
	}
	_ = resp.Body.Close()
	if resp.StatusCode != http.StatusFound || resp.Header.Get("Location") != "/login?next=%2F" {
		t.Errorf("GET / = %d %q", resp.StatusCode, resp.Header.Get("Location"))
	}
}
