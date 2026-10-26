// Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
// SPDX-License-Identifier: MIT

package server

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"testing/fstest"
)

// testRoot copies testdata/root to a temporary folder, so tests can
// write state/ without touching the checkout.
func testRoot(t *testing.T) string {
	t.Helper()
	dst := t.TempDir()
	err := os.CopyFS(dst, os.DirFS(filepath.Join("testdata", "root")))
	if err != nil {
		t.Fatal(err)
	}
	return dst
}

var testStatic = fstest.MapFS{
	"tags/gtag.js":     {Data: []byte("/* gtag stand-in */")},
	"tags/fbevents.js": {Data: []byte("/* fbevents stand-in */")},
	"cmps/common.js":   {Data: []byte("/* common */")},
	"cmps/cookiebot/uc.js": {
		Data: []byte("/* cookiebot replica */"),
	},
	"tags/youtube-embed.html": {Data: []byte("<!doctype html><p>video</p>")},
}

func newTestServer(t *testing.T, opts Options) *Server {
	t.Helper()
	if opts.Root == "" {
		opts.Root = testRoot(t)
	}
	if opts.Static == nil {
		opts.Static = testStatic
	}
	s, err := New(opts)
	if err != nil {
		t.Fatal(err)
	}
	return s
}

// get sends a request for host and target to h and returns the response.
func get(t *testing.T, h http.Handler, method, host, target string, body io.Reader, hdr map[string]string) *httptest.ResponseRecorder {
	t.Helper()
	r := httptest.NewRequest(method, target, body)
	r.Host = host
	for k, v := range hdr {
		r.Header.Set(k, v)
	}
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	return w
}

func TestRoutingByHost(t *testing.T) {
	s := newTestServer(t, Options{Admin: "127.0.0.1:9"})
	h := s.Handler()
	cases := []struct {
		host, target string
		code         int
		has          string
	}{
		{"t01.shop.test", "/", 200, "state=good sub= path=/ internal=127.0.0.1:9"},
		{"T01.Shop.Test:443", "/page-2/", 200, "path=/page-2/"},
		{"metrics.t01.shop.test", "/", 200, "sub=metrics"},
		{"t01.shop.test", "/asset.txt", 200, "plain asset"},
		{"t01.shop.test", "/site.html", 404, ""},
		{"t01.shop.test", "/../../etc/passwd", 404, ""},
		{"nope.shop.test", "/", 421, "not a consent testbed host"},
		{"www.googletagmanager.com", "/gtag/js?id=G-TESTBED001", 200, "gtag stand-in"},
		{"www.googletagmanager.com", "/gtm.js?id=GTM-TESTBED1", 200, "gtag stand-in"},
		{"www.googletagmanager.com", "/other.js", 404, ""},
		{"connect.facebook.net", "/en_US/fbevents.js", 200, "fbevents stand-in"},
		{"consent.cookiebot.com", "/uc.js", 200, "/* common */\n/* cookiebot replica */"},
		{"www.example.com", "/", 421, "not a consent testbed host: www.example.com"},
	}
	for _, c := range cases {
		w := get(t, h, "GET", c.host, c.target, nil, nil)
		if w.Code != c.code || !strings.Contains(w.Body.String(), c.has) {
			t.Errorf("%s%s: %d %q, want %d with %q", c.host, c.target, w.Code, w.Body.String(), c.code, c.has)
		}
	}
	if got := s.Count("").Unmapped; got["www.example.com"] != 1 || got["nope.shop.test"] != 1 {
		t.Errorf("unmapped = %v", got)
	}
	get(t, h, "GET", "cdn.example.net", "/x.js", nil, map[string]string{"Referer": "https://t01.shop.test/"})
	if got := s.Count("t01").Unmapped; len(got) != 1 || got["cdn.example.net"] != 1 {
		t.Errorf("t01 unmapped = %v", got)
	}
	w := get(t, h, "GET", "www.youtube.com", "/embed/tbvideo1", nil, nil)
	if c := w.Header().Values("Set-Cookie"); len(c) != 2 || !strings.HasPrefix(c[0], "VISITOR_INFO1_LIVE=") || !strings.Contains(c[0], "SameSite=None") {
		t.Errorf("youtube embed cookies = %q", c)
	}
	if ct := get(t, h, "GET", "connect.facebook.net", "/en_US/fbevents.js", nil, nil).Header().Get("Content-Type"); !strings.HasPrefix(ct, "text/javascript") {
		t.Errorf("Content-Type = %q", ct)
	}
}

func TestSpecialSites(t *testing.T) {
	s := newTestServer(t, Options{})
	h := s.Handler()
	w := get(t, h, "GET", "g12.shop.test", "/", nil, nil)
	if c := w.Header().Get("Set-Cookie"); !strings.HasPrefix(c, SessionCookie+"=") || !strings.Contains(c, "HttpOnly") {
		t.Errorf("g12 session cookie = %q", c)
	}
	w = get(t, h, "GET", "g12.shop.test", "/", nil, map[string]string{"Cookie": SessionCookie + "=x"})
	if c := w.Header().Get("Set-Cookie"); c != "" {
		t.Errorf("g12 set the session cookie again: %q", c)
	}
	hops := func() int {
		n, target := 0, "/"
		for n < 20 {
			w := get(t, h, "GET", "redirect-chain.shop.test", target, nil, nil)
			if w.Code != http.StatusFound {
				return n
			}
			target = w.Header().Get("Location")
			n++
		}
		return n
	}
	if n := hops(); n != goodHops {
		t.Errorf("good redirect chain: %d hops, want %d", n, goodHops)
	}
	if err := s.SetBroken("redirect-chain", true); err != nil {
		t.Fatal(err)
	}
	if n := hops(); n != brokenHops {
		t.Errorf("broken redirect chain: %d hops, want %d", n, brokenHops)
	}
	if w := get(t, h, "GET", "redirect-loop.shop.test", "/", nil, nil); w.Code != 200 {
		t.Errorf("good redirect-loop / = %d", w.Code)
	}
	if err := s.SetBroken("redirect-loop", true); err != nil {
		t.Fatal(err)
	}
	a := get(t, h, "GET", "redirect-loop.shop.test", "/", nil, nil)
	b := get(t, h, "GET", "redirect-loop.shop.test", a.Header().Get("Location"), nil, nil)
	if a.Code != 302 || b.Code != 302 || b.Header().Get("Location") != "/" {
		t.Errorf("broken redirect-loop: %d %q, %d %q", a.Code, a.Header().Get("Location"), b.Code, b.Header().Get("Location"))
	}
}

func TestStateByFileAndAdmin(t *testing.T) {
	root := testRoot(t)
	s := newTestServer(t, Options{Root: root})
	h, adm := s.Handler(), s.AdminHandler()
	page := func() string { return get(t, h, "GET", "t01.shop.test", "/", nil, nil).Body.String() }
	if !strings.Contains(page(), "state=good") {
		t.Fatal("t01 isn't good at the start")
	}
	// A file written by hand (or toggle.sh) breaks it at once.
	if err := os.MkdirAll(filepath.Join(root, "state"), 0o750); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "state", "t01"), nil, 0o600); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(page(), "state=broken") {
		t.Error("state/t01 didn't break t01")
	}
	w := get(t, adm, "GET", "127.0.0.1", "/_admin/state?id=t01&state=good", nil, nil)
	if w.Code != 200 || !strings.Contains(w.Body.String(), `"state": "good"`) || !strings.Contains(page(), "state=good") {
		t.Errorf("admin good: %d %s", w.Code, w.Body.String())
	}
	if _, err := os.Stat(filepath.Join(root, "state", "t01")); !os.IsNotExist(err) {
		t.Errorf("state/t01 still there: %v", err)
	}
	w = get(t, adm, "GET", "127.0.0.1", "/_admin/state?id=t01&state=broken", nil, nil)
	if w.Code != 200 || !strings.Contains(page(), "state=broken") {
		t.Errorf("admin broken: %d %s", w.Code, w.Body.String())
	}
	if w := get(t, adm, "GET", "127.0.0.1", "/_admin/state?id=t01", nil, nil); !strings.Contains(w.Body.String(), `"state": "broken"`) {
		t.Errorf("admin read: %s", w.Body.String())
	}
	for _, bad := range []string{"id=nope", "id=../etc", "id=t01&state=maybe"} {
		if w := get(t, adm, "GET", "127.0.0.1", "/_admin/state?"+bad, nil, nil); w.Code == 200 {
			t.Errorf("admin %s answered 200", bad)
		}
	}
	// The admin endpoints are not on the site listener.
	if w := get(t, h, "GET", "t01.shop.test", "/_admin/state?id=t01&state=good", nil, nil); strings.Contains(w.Body.String(), `"state"`) {
		t.Error("the site listener answered an admin request")
	}
	w = get(t, adm, "GET", "127.0.0.1", "/_admin/sites", nil, nil)
	var sites map[string]string
	if err := json.Unmarshal(w.Body.Bytes(), &sites); err != nil || sites["t01"] != "broken" || sites["g12"] != "good" {
		t.Errorf("sites = %v (%v)", sites, err)
	}
}

func TestSinkCountsAndResets(t *testing.T) {
	s := newTestServer(t, Options{})
	h, adm := s.Handler(), s.AdminHandler()
	get(t, h, "GET", "www.google-analytics.com", "/g/collect?v=2&tid=G-TESTBED001&en=page_view&_tbrun=t01", nil, nil)
	get(t, h, "POST", "region1.google-analytics.com", "/g/collect?v=2&tid=G-TESTBED001", strings.NewReader("en=page_view&_tbrun=t01\nen=scroll"), nil)
	get(t, h, "GET", "www.facebook.com", "/tr?id=100000000000001&ev=PageView", nil, map[string]string{"Referer": "https://t01.shop.test/page-2/"})
	get(t, h, "POST", "metrics.t01.shop.test", "/g/collect?v=2&tid=G-TESTBED001", nil, map[string]string{RunHeader: "t01"})
	get(t, h, "GET", "www.facebook.com", "/tr?id=1&ev=PageView", nil, nil) // no scenario
	// Lookalikes are not hits.
	get(t, h, "GET", "t01.shop.test", "/api/collect", nil, nil)
	get(t, h, "GET", "www.facebook.com", "/tracking", nil, nil)
	get(t, h, "GET", "t01.shop.test", "/cart/", nil, nil)
	get(t, h, "GET", "t01.shop.test", "/logout/", nil, nil)

	w := get(t, adm, "GET", "127.0.0.1", "/_sink/count?scenario=t01", nil, nil)
	var c Count
	if err := json.Unmarshal(w.Body.Bytes(), &c); err != nil {
		t.Fatal(err)
	}
	if c.Hits != 4 || c.By["www.google-analytics.com/g/collect"] != 1 || c.By["region1.google-analytics.com/g/collect"] != 1 ||
		c.By["www.facebook.com/tr"] != 1 || c.By["metrics.t01.shop.test/g/collect"] != 1 || c.Forbidden != 2 {
		t.Errorf("t01 count = %+v", c)
	}
	if got := s.Count("unknown").Hits; got != 1 {
		t.Errorf("unknown hits = %d", got)
	}
	if got := s.Count("").Hits; got != 5 {
		t.Errorf("all hits = %d", got)
	}
	hits := s.Hits("t01")
	if len(hits) != 4 || hits[1].Body != "en=page_view&_tbrun=t01\nen=scroll" || hits[1].Method != "POST" {
		t.Errorf("recorded = %+v", hits)
	}
	w = get(t, adm, "GET", "127.0.0.1", "/_sink/hits?scenario=nobody", nil, nil)
	if strings.TrimSpace(w.Body.String()) != "[]" {
		t.Errorf("hits of nobody = %s", w.Body.String())
	}
	// One scenario's reset leaves the others' counts alone.
	if w := get(t, adm, "POST", "127.0.0.1", "/_sink/reset?scenario=unknown", nil, nil); w.Code != http.StatusNoContent {
		t.Errorf("reset unknown = %d", w.Code)
	}
	if s.Count("unknown").Hits != 0 || s.Count("t01").Hits != 4 {
		t.Errorf("after resetting unknown: unknown %+v, t01 %+v", s.Count("unknown"), s.Count("t01"))
	}
	if w := get(t, adm, "POST", "127.0.0.1", "/_sink/reset", nil, nil); w.Code != http.StatusNoContent {
		t.Errorf("reset = %d", w.Code)
	}
	if c := s.Count(""); c.Hits != 0 || c.Forbidden != 0 || len(s.Hits("t01")) != 0 {
		t.Errorf("after reset: %+v", c)
	}
	// The ssrf-probe target counts on the admin listener only.
	get(t, adm, "GET", "127.0.0.1", "/_internal/probe?s=ssrf-probe", nil, nil)
	if got := s.Count("ssrf-probe").Internal; got != 1 {
		t.Errorf("internal = %d", got)
	}
	// A hit answers like the endpoint: an image for GET, 204 for POST.
	if w := get(t, h, "GET", "www.facebook.com", "/tr?id=1&ev=PageView", nil, nil); w.Header().Get("Content-Type") != "image/gif" {
		t.Errorf("GET hit Content-Type = %q", w.Header().Get("Content-Type"))
	}
	if w := get(t, h, "POST", "www.google-analytics.com", "/g/collect", nil, nil); w.Code != http.StatusNoContent {
		t.Errorf("POST hit = %d", w.Code)
	}
}

// TestTLSWithGeneratedCA serves https and http on one port and checks
// the certificate chains to the generated CA, per host name, with the
// self-signed fixture as the exception.
func TestTLSWithGeneratedCA(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "ca")
	if err := MakeCA(dir); err != nil {
		t.Fatal(err)
	}
	if err := MakeCA(dir); err == nil {
		t.Error("MakeCA overwrote an existing CA")
	}
	ca, err := LoadCA(dir)
	if err != nil {
		t.Fatal(err)
	}
	s := newTestServer(t, Options{CA: ca})
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	go func() { _ = s.Serve(l) }()
	t.Cleanup(func() { _ = l.Close() })
	addr := l.Addr().String()
	client := func(pool bool) *http.Client {
		cfg := &tls.Config{MinVersion: tls.VersionTLS12}
		if pool {
			cfg.RootCAs = ca.Pool()
		}
		return &http.Client{Transport: &http.Transport{
			TLSClientConfig: cfg,
			DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
				var d net.Dialer
				return d.DialContext(ctx, "tcp", addr)
			},
		}}
	}
	for _, u := range []string{"https://t01.shop.test/", "https://www.google-analytics.com/g/collect?v=2", "http://t01.shop.test/"} {
		resp, err := client(true).Get(u)
		if err != nil {
			t.Errorf("%s: %v", u, err)
			continue
		}
		_ = resp.Body.Close()
		if resp.StatusCode != 200 {
			t.Errorf("%s: %d", u, resp.StatusCode)
		}
		if strings.HasPrefix(u, "https:") && resp.TLS.PeerCertificates[0].DNSNames[0] != strings.Split(strings.TrimPrefix(u, "https://"), "/")[0] {
			t.Errorf("%s: certificate for %v", u, resp.TLS.PeerCertificates[0].DNSNames)
		}
	}
	if resp, err := client(false).Get("https://t01.shop.test/"); err == nil {
		_ = resp.Body.Close()
		t.Error("the system roots trusted the testbed CA")
	}
	resp, err := client(true).Get("https://self-signed.shop.test/")
	if err == nil {
		_ = resp.Body.Close()
	}
	if err == nil || !strings.Contains(err.Error(), "certificate") {
		t.Errorf("self-signed host: %v, want a certificate error", err)
	}
	// The CA can't vouch for a real name.
	if _, err := ca.leaf("bank.example"); err != nil {
		t.Fatal(err)
	}
	cert, _ := ca.leaf("bank.example")
	leaf, err := parseLeaf(cert)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := leaf.Verify(x509VerifyOptions(ca, "bank.example")); err == nil {
		t.Error("a certificate for bank.example verified against the testbed CA")
	}
}

func TestPrintMapListsEveryTagHost(t *testing.T) {
	line := MapLine("127.0.0.1:23400")
	if !strings.HasPrefix(line, "--map-host '*.shop.test=127.0.0.1:23400' --map-host ") || !strings.HasSuffix(line, " --map-only") {
		t.Errorf("map line = %q", line)
	}
	for h := range tagHosts {
		if !strings.Contains(line, "--map-host "+h+"=127.0.0.1:23400") {
			t.Errorf("map line lacks %s", h)
		}
	}
	args := MapArgs("127.0.0.1:1")
	if n := strings.Count(strings.Join(args, " "), "--map-host"); n != len(tagHosts)+1 {
		t.Errorf("%d --map-host flags for %d hosts", n, len(tagHosts)+1)
	}
	if args[1] != "*.shop.test=127.0.0.1:1" {
		t.Errorf("args[1] = %q (unquoted for exec)", args[1])
	}
}

func TestCAPIOnlyThroughALoopbackProxy(t *testing.T) {
	for _, bad := range []string{"https://127.0.0.1:8026", "http://proxy.example.com:8026", "http://10.0.0.5:8026", "ftp://127.0.0.1"} {
		if _, err := newCAPISender(bad, ""); err == nil {
			t.Errorf("%s accepted", bad)
		}
	}
	got := make(chan *http.Request, 4)
	proxy := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		got <- r
		w.WriteHeader(http.StatusBadGateway) // the test only needs the CONNECT
	}))
	defer proxy.Close()
	s := newTestServer(t, Options{EchoProxy: proxy.URL})
	h := s.Handler()
	// Good state: nothing without marketing consent, an event with it.
	get(t, h, "GET", "c18.shop.test", "/page-2/", nil, map[string]string{"Cookie": "cmplz_marketing=deny"})
	get(t, h, "GET", "c18.shop.test", "/page-2/", nil, map[string]string{"Cookie": "cmplz_marketing=allow", "X-Echo-Run": "r1/accept"})
	r := <-got
	if r.Method != http.MethodConnect || r.Host != "graph.facebook.com:443" {
		t.Errorf("proxy got %s %s", r.Method, r.Host)
	}
	select {
	case r := <-got:
		t.Errorf("a second event in the good state: %s %s", r.Method, r.Host)
	default:
	}
	if err := s.SetBroken("c18", true); err != nil {
		t.Fatal(err)
	}
	get(t, h, "GET", "c18.shop.test", "/page-2/", nil, map[string]string{"Cookie": "cmplz_marketing=deny"})
	if r := <-got; r.Method != http.MethodConnect {
		t.Errorf("broken: proxy got %s", r.Method)
	}
}

func parseLeaf(c *tls.Certificate) (*x509.Certificate, error) {
	return x509.ParseCertificate(c.Certificate[0])
}

func x509VerifyOptions(ca *CA, name string) x509.VerifyOptions {
	return x509.VerifyOptions{Roots: ca.Pool(), DNSName: name}
}
