// Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
// SPDX-License-Identifier: MIT

package egress

import (
	"bufio"
	"context"
	"errors"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"net/url"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/RhythmShahriar/rhythm/kit/httpx"
)

// connect sends CONNECT addr to the proxy and returns the status and
// the open connection.
func connect(t *testing.T, p *Proxy, addr string) (int, net.Conn, *bufio.Reader) {
	t.Helper()
	c, err := net.DialTimeout("tcp", p.Addr(), 5*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = c.Close() })
	_ = c.SetDeadline(time.Now().Add(10 * time.Second))
	if _, err := io.WriteString(c, "CONNECT "+addr+" HTTP/1.1\r\nHost: "+addr+"\r\n\r\n"); err != nil {
		t.Fatal(err)
	}
	br := bufio.NewReader(c)
	resp, err := http.ReadResponse(br, &http.Request{Method: http.MethodConnect})
	if err != nil {
		t.Fatal(err)
	}
	if resp.StatusCode != http.StatusOK {
		_, _ = io.Copy(io.Discard, resp.Body)
		_ = resp.Body.Close()
	}
	return resp.StatusCode, c, br
}

// reply is a response read whole.
type reply struct {
	StatusCode int
	Header     http.Header
}

// get sends an absolute-form GET for raw through the proxy.
func get(t *testing.T, p *Proxy, raw string) (reply, string) {
	t.Helper()
	pu, _ := url.Parse(p.URL())
	client := &http.Client{Transport: &http.Transport{Proxy: http.ProxyURL(pu)}, Timeout: 10 * time.Second}
	resp, err := client.Get(raw)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = resp.Body.Close() }()
	b, _ := io.ReadAll(resp.Body)
	return reply{resp.StatusCode, resp.Header}, string(b)
}

// counter is a 127.0.0.1 server that counts what reaches it.
func counter(t *testing.T) (*httptest.Server, *atomic.Int64) {
	t.Helper()
	n := new(atomic.Int64)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		n.Add(1)
		_, _ = io.WriteString(w, "internal")
	}))
	t.Cleanup(srv.Close)
	return srv, n
}

func startGuard(t *testing.T, opts Options) *Proxy {
	t.Helper()
	p, err := Start(opts)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = p.Close() })
	return p
}

// A guard for a public target (an address in TEST-NET-3, so no lookup
// runs) refuses loopback, private and metadata destinations before any
// packet goes out, by CONNECT and by plain HTTP.
func TestPublicTargetRefusesPrivate(t *testing.T) {
	srv, hits := counter(t)
	p := startGuard(t, Options{Target: "203.0.113.10"})
	internal := strings.TrimPrefix(srv.URL, "http://")
	for _, addr := range []string{internal, "169.254.169.254:80", "10.0.0.5:443", "[::1]:443", "[fd00:ec2::254]:80", "localhost:443"} {
		if code, _, _ := connect(t, p, addr); code != http.StatusForbidden {
			t.Errorf("CONNECT %s = %d; want 403", addr, code)
		}
	}
	resp, _ := get(t, p, srv.URL+"/secret")
	if resp.StatusCode != http.StatusForbidden || resp.Header.Get("X-Rhythm-Echo-Refused") != ReasonPrivate {
		t.Errorf("GET %s = %d %q", srv.URL, resp.StatusCode, resp.Header.Get("X-Rhythm-Echo-Refused"))
	}
	if hits.Load() != 0 {
		t.Errorf("the internal server was reached %d times", hits.Load())
	}
	host, port, _ := net.SplitHostPort(internal)
	if got := p.Reason(host, port); got != ReasonPrivate {
		t.Errorf("Reason(%s) = %q", internal, got)
	}
	if got := p.Reason("169.254.169.254", "80"); got != ReasonPrivate {
		t.Errorf("metadata reason = %q", got)
	}
	if got := p.Reason("203.0.113.99", "443"); got != "" {
		t.Errorf("reason for a host never asked for = %q", got)
	}
	if s := p.Stats(); s.Refused != 7 || s.BrowserInternal != 0 {
		t.Errorf("stats = %+v; want 7 refused", s)
	}
}

// TestLoopbackTargetAllowed: a 127.0.0.1 target reaches its own
// loopback assets through a tunnel and by plain HTTP, never the
// metadata address.
func TestLoopbackTargetAllowed(t *testing.T) {
	srv, hits := counter(t)
	p := startGuard(t, Options{Target: "127.0.0.1"})
	addr := strings.TrimPrefix(srv.URL, "http://")
	code, c, br := connect(t, p, addr)
	if code != http.StatusOK {
		t.Fatalf("CONNECT %s = %d", addr, code)
	}
	_, _ = io.WriteString(c, "GET / HTTP/1.1\r\nHost: "+addr+"\r\nConnection: close\r\n\r\n")
	resp, err := http.ReadResponse(br, nil)
	if err != nil {
		t.Fatal(err)
	}
	b, _ := io.ReadAll(resp.Body)
	_ = resp.Body.Close()
	if string(b) != "internal" {
		t.Errorf("through the tunnel: %q", b)
	}
	if resp, body := get(t, p, srv.URL+"/asset.js"); resp.StatusCode != http.StatusOK || body != "internal" {
		t.Errorf("GET = %d %q", resp.StatusCode, body)
	}
	if hits.Load() != 2 {
		t.Errorf("hits = %d; want 2", hits.Load())
	}
	for _, addr := range []string{"169.254.169.254:80", "[fd00:ec2::254]:80", "metadata.google.internal:80"} {
		if code, _, _ := connect(t, p, addr); code != http.StatusForbidden {
			t.Errorf("CONNECT %s = %d; want 403", addr, code)
		}
	}
	if s := p.Stats(); s.Refused != 3 {
		t.Errorf("stats = %+v", s)
	}
}

// TestBrowserServiceRefused: Chrome's own service hosts are refused on
// a guard and counted as browser traffic, not as the page's.
func TestBrowserServiceRefused(t *testing.T) {
	p := startGuard(t, Options{Target: "127.0.0.1"})
	if code, _, _ := connect(t, p, "update.googleapis.com:443"); code != http.StatusForbidden {
		t.Errorf("CONNECT update.googleapis.com:443 = %d", code)
	}
	if s := p.Stats(); s.BrowserInternal != 1 || s.Refused != 0 {
		t.Errorf("stats = %+v; want BrowserInternal 1", s)
	}
	if got := p.Reason("update.googleapis.com", "443"); got != ReasonBrowser {
		t.Errorf("reason = %q", got)
	}
	for _, h := range BrowserServiceHosts {
		if code, _, _ := connect(t, p, strings.ToUpper(h)+".:443"); code != http.StatusForbidden {
			t.Errorf("CONNECT %s = %d", h, code)
		}
	}
	if s := p.Stats(); s.BrowserInternal != 1+len(BrowserServiceHosts) {
		t.Errorf("stats = %+v", s)
	}
}

// IsBrowserService matches the service hosts in any case, with or
// without a trailing dot; nothing else matches.
func TestIsBrowserService(t *testing.T) {
	for _, h := range []string{"update.googleapis.com", "Clients2.Google.com.", "SAFEBROWSING.GOOGLEAPIS.COM"} {
		if !IsBrowserService(h) {
			t.Errorf("IsBrowserService(%q) = false", h)
		}
	}
	for _, h := range []string{"", "googleapis.com", "www.googletagmanager.com", "update.googleapis.com.evil.test", "x.update.googleapis.com"} {
		if IsBrowserService(h) {
			t.Errorf("IsBrowserService(%q) = true", h)
		}
	}
}

// The browser-wide listener refuses everything (public or not) and
// counts each as browser traffic.
func TestRefuseAll(t *testing.T) {
	srv, hits := counter(t)
	var logs []string
	var mu sync.Mutex
	p, err := StartRefuseAll(func(s string) { mu.Lock(); logs = append(logs, s); mu.Unlock() })
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = p.Close() }()
	if code, _, _ := connect(t, p, "203.0.113.10:443"); code != http.StatusForbidden {
		t.Errorf("CONNECT = %d", code)
	}
	if resp, _ := get(t, p, srv.URL); resp.StatusCode != http.StatusForbidden {
		t.Errorf("GET = %d", resp.StatusCode)
	}
	if s := p.Stats(); s.BrowserInternal != 2 || s.Refused != 0 || hits.Load() != 0 {
		t.Errorf("stats = %+v, hits %d", s, hits.Load())
	}
	mu.Lock()
	defer mu.Unlock()
	if len(logs) != 2 || !strings.Contains(logs[0], "refused CONNECT 203.0.113.10:443 (browser traffic)") {
		t.Errorf("logs = %q", logs)
	}
}

// TestMapOnlyRefuses: with --map-only a host no --map-host covers is
// refused as not mapped.
func TestMapOnlyRefuses(t *testing.T) {
	srv, hits := counter(t)
	p := startGuard(t, Options{Target: "127.0.0.1", MapHosts: []string{"*.shop.test", "cdn.test"}, MapOnly: true})
	if code, _, _ := connect(t, p, "other.test:443"); code != http.StatusForbidden {
		t.Errorf("CONNECT other.test = %d", code)
	}
	// Loopback is allowed for this target but not mapped either.
	if resp, _ := get(t, p, srv.URL); resp.StatusCode != http.StatusForbidden || resp.Header.Get("X-Rhythm-Echo-Refused") != ReasonNotMapped {
		t.Errorf("GET %s = %d", srv.URL, resp.StatusCode)
	}
	if p.Reason("other.test", "443") != ReasonNotMapped || hits.Load() != 0 {
		t.Errorf("reason %q, hits %d", p.Reason("other.test", "443"), hits.Load())
	}
	if s := p.Stats(); s.Refused != 2 {
		t.Errorf("stats = %+v", s)
	}

	// Without --map-only the same loopback request goes through.
	p = startGuard(t, Options{Target: "127.0.0.1", MapHosts: []string{"*.shop.test"}})
	if resp, _ := get(t, p, srv.URL); resp.StatusCode != http.StatusOK {
		t.Errorf("without --map-only: %d", resp.StatusCode)
	}
}

// TestMappedHostNeverDialed: a host --map-host covers is answered in
// the tab; a request for it that reaches the guard (one Fetch never
// paused) is refused by CONNECT and by plain HTTP, with or without
// --map-only, and never reaches the real listener. The same loopback
// listener by an unmapped name still goes through.
func TestMappedHostNeverDialed(t *testing.T) {
	srv, hits := counter(t)
	_, port, _ := net.SplitHostPort(strings.TrimPrefix(srv.URL, "http://"))
	for _, mapOnly := range []bool{true, false} {
		p := startGuard(t, Options{Target: "127.0.0.1", MapHosts: []string{"localhost", "*.shop.test"}, MapOnly: mapOnly})
		for _, addr := range []string{"localhost:" + port, "LOCALHOST.:" + port, "api.shop.test:443"} {
			if code, _, _ := connect(t, p, addr); code != http.StatusForbidden {
				t.Errorf("map-only %v: CONNECT %s = %d; want 403", mapOnly, addr, code)
			}
		}
		resp, _ := get(t, p, "http://localhost:"+port+"/")
		if resp.StatusCode != http.StatusForbidden || resp.Header.Get("X-Rhythm-Echo-Refused") != ReasonMapped {
			t.Errorf("map-only %v: GET localhost = %d %q", mapOnly, resp.StatusCode, resp.Header.Get("X-Rhythm-Echo-Refused"))
		}
		if got := p.Reason("localhost", port); got != ReasonMapped {
			t.Errorf("map-only %v: Reason = %q", mapOnly, got)
		}
		if s := p.Stats(); s.Refused != 4 || s.BrowserInternal != 0 {
			t.Errorf("map-only %v: stats = %+v; want 4 refused", mapOnly, s)
		}
		if hits.Load() != 0 {
			t.Fatalf("map-only %v: the real listener was reached %d times", mapOnly, hits.Load())
		}
	}
	// Negative: the same listener by its unmapped address goes through.
	p := startGuard(t, Options{Target: "127.0.0.1", MapHosts: []string{"localhost"}})
	if resp, _ := get(t, p, srv.URL+"/"); resp.StatusCode != http.StatusOK || hits.Load() != 1 {
		t.Errorf("unmapped 127.0.0.1 = %d, hits %d", resp.StatusCode, hits.Load())
	}
}

// TestGuardDialRefusesMetadata: the guard's dial checks every resolved
// address, so a metadata address is refused even for a link-local
// target the kit guard alone would allow it for. The check runs before
// connect, so no packet leaves. A loopback target still dials its own
// listener.
func TestGuardDialRefusesMetadata(t *testing.T) {
	p := startGuard(t, Options{Target: "169.254.10.1"})
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if !p.guard.Allow(ctx, netip.MustParseAddr("169.254.169.254")) {
		t.Fatal("the kit guard no longer allows link-local for a link-local target; the test needs another fixture")
	}
	for _, addr := range []string{"169.254.169.254:80", "[::ffff:169.254.169.254]:80"} {
		c, err := p.guardDial(ctx, "tcp", addr)
		if err == nil {
			_ = c.Close()
			t.Fatalf("guardDial %s connected", addr)
		}
		if !errors.Is(err, httpx.ErrPrivate) {
			t.Errorf("guardDial %s: %v; want ErrPrivate", addr, err)
		}
	}
	srv, hits := counter(t)
	lp := startGuard(t, Options{Target: "127.0.0.1"})
	c, err := lp.guardDial(ctx, "tcp", strings.TrimPrefix(srv.URL, "http://"))
	if err != nil {
		t.Fatalf("loopback dial: %v", err)
	}
	_ = c.Close()
	if hits.Load() != 0 {
		t.Errorf("hits = %d", hits.Load())
	}
}

func TestMatchHost(t *testing.T) {
	pats := []string{"*.Example.test", "cdn.test", " api.shop.test. ", ""}
	for host, want := range map[string]bool{
		"www.example.test": true, "a.b.example.test": true, "WWW.EXAMPLE.TEST.": true,
		"example.test": false, "badexample.test": false, "cdn.test": true, "x.cdn.test": false,
		"api.shop.test": true, "shop.test": false, "": false,
	} {
		if got := MatchHost(pats, host); got != want {
			t.Errorf("MatchHost(%q) = %v; want %v", host, got, want)
		}
	}
	if MatchHost(nil, "cdn.test") {
		t.Error("no patterns matched")
	}
}

// fakeUpstream is a --proxy on 127.0.0.1 that records each request
// line and Proxy-Authorization, answers CONNECT with 200 and then plays
// the destination itself.
type fakeUpstream struct {
	ln    net.Listener
	mu    sync.Mutex
	lines []string
	auth  []string
}

func newFakeUpstream(t *testing.T) *fakeUpstream {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	f := &fakeUpstream{ln: ln}
	t.Cleanup(func() { _ = ln.Close() })
	go func() {
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			go f.serve(c)
		}
	}()
	return f
}

func (f *fakeUpstream) serve(c net.Conn) {
	defer func() { _ = c.Close() }()
	br := bufio.NewReader(c)
	req, err := http.ReadRequest(br)
	if err != nil {
		return
	}
	f.mu.Lock()
	f.lines = append(f.lines, req.Method+" "+req.RequestURI)
	f.auth = append(f.auth, req.Header.Get("Proxy-Authorization"))
	f.mu.Unlock()
	if req.Method == http.MethodConnect {
		_, _ = io.WriteString(c, "HTTP/1.1 200 OK\r\n\r\n")
		if _, err := http.ReadRequest(br); err != nil {
			return
		}
	}
	_, _ = io.WriteString(c, "HTTP/1.1 200 OK\r\nContent-Length: 8\r\nConnection: close\r\n\r\nupstream")
}

// TestUpstreamProxy: with --proxy the allowed connections go on through
// it with its credentials; refused ones never reach it and the
// credentials never reach the log.
func TestUpstreamProxy(t *testing.T) {
	up := newFakeUpstream(t)
	pass := "s3" + "cret-" + "pw"
	var logs []string
	var mu sync.Mutex
	p := startGuard(t, Options{Target: "127.0.0.1", Upstream: "http://echo:" + pass + "@" + up.ln.Addr().String(),
		Log: func(s string) { mu.Lock(); logs = append(logs, s); mu.Unlock() }})
	code, c, br := connect(t, p, "127.0.0.1:35070")
	if code != http.StatusOK {
		t.Fatalf("CONNECT = %d", code)
	}
	_, _ = io.WriteString(c, "GET / HTTP/1.1\r\nHost: 127.0.0.1:35070\r\n\r\n")
	resp, err := http.ReadResponse(br, nil)
	if err != nil {
		t.Fatal(err)
	}
	b, _ := io.ReadAll(resp.Body)
	_ = resp.Body.Close()
	if string(b) != "upstream" {
		t.Errorf("tunnel body %q", b)
	}
	if resp, body := get(t, p, "http://127.0.0.1:35071/page"); resp.StatusCode != http.StatusOK || body != "upstream" {
		t.Errorf("GET = %d %q", resp.StatusCode, body)
	}
	if code, _, _ := connect(t, p, "169.254.169.254:80"); code != http.StatusForbidden {
		t.Errorf("metadata through the upstream = %d", code)
	}
	up.mu.Lock()
	lines, auth := append([]string(nil), up.lines...), append([]string(nil), up.auth...)
	up.mu.Unlock()
	if len(lines) != 2 || lines[0] != "CONNECT 127.0.0.1:35070" || lines[1] != "GET http://127.0.0.1:35071/page" {
		t.Errorf("upstream saw %q", lines)
	}
	for _, a := range auth {
		if !strings.HasPrefix(a, "Basic ") {
			t.Errorf("upstream auth %q", a)
		}
	}
	mu.Lock()
	defer mu.Unlock()
	joined := strings.Join(logs, "\n")
	if strings.Contains(joined, pass) || !strings.Contains(joined, "go on through http://") {
		t.Errorf("logs = %q", logs)
	}
	if _, err := Start(Options{Upstream: "socks5://127.0.0.1:1080"}); err == nil {
		t.Error("a socks5 --proxy was taken")
	}
	if _, err := Start(Options{Upstream: "http://u:" + pass + "@"}); err == nil || strings.Contains(err.Error(), pass) {
		t.Errorf("bad --proxy: %v", err)
	}
	for _, raw := range []string{"http://u:" + pass + "@h:1", "http://" + pass + "@", "ftp://a:" + pass + "@x@y", "http://h:1"} {
		if got := maskProxy(raw); strings.Contains(got, pass) || !strings.HasPrefix(got, strings.SplitN(raw, ":", 2)[0]+"://") {
			t.Errorf("maskProxy(%q) = %q", raw, got)
		}
	}
}

// The listener keeps at most MaxTunnels open; one more is refused as a
// limit.
func TestTunnelLimit(t *testing.T) {
	srv, _ := counter(t)
	addr := strings.TrimPrefix(srv.URL, "http://")
	p := startGuard(t, Options{Target: "127.0.0.1", MaxTunnels: 2})
	for range 2 {
		if code, _, _ := connect(t, p, addr); code != http.StatusOK {
			t.Fatalf("CONNECT = %d", code)
		}
	}
	if code, _, _ := connect(t, p, addr); code != http.StatusServiceUnavailable {
		t.Errorf("third CONNECT = %d; want 503", code)
	}
	if p.Stats().Refused != 1 {
		t.Errorf("stats = %+v", p.Stats())
	}
}

// A tunnel nobody uses closes after IdleTimeout; Close ends the rest.
func TestIdleTunnelCloses(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = ln.Close() }()
	go func() {
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			go func() { _, _ = io.Copy(c, c) }() // echo, never closes by itself
		}
	}()
	p := startGuard(t, Options{Target: "127.0.0.1", IdleTimeout: 200 * time.Millisecond})
	code, c, br := connect(t, p, ln.Addr().String())
	if code != http.StatusOK {
		t.Fatalf("CONNECT = %d", code)
	}
	_, _ = io.WriteString(c, "ping")
	buf := make([]byte, 4)
	if _, err := io.ReadFull(br, buf); err != nil || string(buf) != "ping" {
		t.Fatalf("echo: %q %v", buf, err)
	}
	start := time.Now()
	if _, err := br.ReadByte(); err == nil {
		t.Error("read a byte from an idle tunnel")
	}
	if d := time.Since(start); d > 5*time.Second {
		t.Errorf("the idle tunnel closed after %s", d)
	}

	code, c2, br2 := connect(t, p, ln.Addr().String())
	if code != http.StatusOK {
		t.Fatalf("CONNECT = %d", code)
	}
	_ = c2
	_ = p.Close()
	if _, err := br2.ReadByte(); err == nil {
		t.Error("a tunnel outlived Close")
	}
}

// Requests that aren't proxy requests are refused as bad requests.
func TestNotAProxyRequest(t *testing.T) {
	p := startGuard(t, Options{Target: "127.0.0.1"})
	resp, err := http.Get(p.URL() + "/")
	if err != nil {
		t.Fatal(err)
	}
	_ = resp.Body.Close()
	if resp.StatusCode != http.StatusBadRequest {
		t.Errorf("origin-form GET = %d", resp.StatusCode)
	}
	if code, _, _ := connect(t, p, "no-port"); code != http.StatusBadRequest {
		t.Errorf("CONNECT without a port = %d", code)
	}
	if s := (Stats{1, 2}).Add(Stats{3, 4}); s != (Stats{4, 6}) {
		t.Errorf("Add = %+v", s)
	}
}

// TestDialErrorRemembered: a loopback port nothing listens on fails by
// CONNECT and by plain HTTP; the guard keeps why, so the check can say
// the site didn't answer. A port that answered has no error.
func TestDialErrorRemembered(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	closed := ln.Addr().String()
	_ = ln.Close()
	srv, _ := counter(t)
	p := startGuard(t, Options{Target: "127.0.0.1"})
	if code, _, _ := connect(t, p, closed); code != http.StatusBadGateway {
		t.Errorf("CONNECT %s = %d; want 502", closed, code)
	}
	host, port, _ := net.SplitHostPort(closed)
	if got := p.DialError(host, port); !strings.Contains(got, "refused") {
		t.Errorf("DialError after CONNECT = %q", got)
	}
	p2 := startGuard(t, Options{Target: "127.0.0.1"})
	if resp, _ := get(t, p2, "http://"+closed+"/"); resp.StatusCode != http.StatusBadGateway {
		t.Errorf("GET = %d; want 502", resp.StatusCode)
	}
	if got := p2.DialError(host, port); !strings.Contains(got, "refused") {
		t.Errorf("DialError after GET = %q", got)
	}
	_, _ = get(t, p2, srv.URL+"/")
	h2, p2port, _ := net.SplitHostPort(strings.TrimPrefix(srv.URL, "http://"))
	if got := p2.DialError(h2, p2port); got != "" {
		t.Errorf("DialError for a port that answered = %q", got)
	}
}
