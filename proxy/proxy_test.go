// Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
// SPDX-License-Identifier: MIT

package proxy

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"errors"
	"io"
	"net"
	"net/http"
	"net/http/httptrace"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/RhythmShahriar/echo/catcher"
	"github.com/RhythmShahriar/echo/model"
	"github.com/RhythmShahriar/echo/wire"
)

// goClient is a net/http client using the proxy and trusting ca. It can
// only dial the proxy.
func goClient(t *testing.T, h *harness, ca string) *http.Client {
	t.Helper()
	pemBytes, err := os.ReadFile(ca) //nolint:gosec // test file
	if err != nil {
		t.Fatal(err)
	}
	pool := x509.NewCertPool()
	pool.AppendCertsFromPEM(pemBytes)
	pu, _ := url.Parse(h.proxyURL)
	tr := &http.Transport{
		Proxy:           http.ProxyURL(pu),
		TLSClientConfig: &tls.Config{RootCAs: pool, MinVersion: tls.VersionTLS12},
		DialContext: func(ctx context.Context, network, addr string) (net.Conn, error) {
			if addr != pu.Host {
				t.Errorf("client dialed %s directly", addr)
				return nil, errNoNet{addr}
			}
			var d net.Dialer
			return d.DialContext(ctx, network, addr)
		},
	}
	t.Cleanup(tr.CloseIdleConnections)
	return &http.Client{Transport: tr, Timeout: 10 * time.Second}
}

func postTwilio(t *testing.T, c *http.Client, rawURL string) (int, string) {
	t.Helper()
	req, _ := http.NewRequest(http.MethodPost, rawURL, strings.NewReader(twilioForm))
	req.Header.Set("Authorization", twilioAuth)
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	resp, err := c.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = resp.Body.Close() }()
	b, _ := io.ReadAll(resp.Body)
	return resp.StatusCode, string(b)
}

func TestGoClient(t *testing.T) {
	runScenarios(t, func(t *testing.T, h *harness, ca string) (int, string) {
		return postTwilio(t, goClient(t, h, ca), "https://"+twilioHost+twilioPath)
	})
}

func TestCurl(t *testing.T) {
	bin, err := exec.LookPath("curl")
	if err != nil {
		t.Skip("curl not installed")
	}
	runScenarios(t, func(t *testing.T, h *harness, ca string) (int, string) {
		// --resolve pins the name to a closed local port: if curl ignored
		// the proxy it would fail instead of reaching the real host.
		cmd := exec.Command(bin, "-sS", "--max-time", "10", "--cacert", ca, "--resolve", twilioHost+":443:127.0.0.1",
			"-H", "Authorization: "+twilioAuth, "--data", twilioForm, "-w", "\n%{http_code}", "https://"+twilioHost+twilioPath) //nolint:gosec // test
		cmd.Env = []string{"PATH=" + os.Getenv("PATH"), "HOME=" + t.TempDir(), "HTTPS_PROXY=" + h.proxyURL}
		out, err := cmd.CombinedOutput()
		if err != nil {
			t.Fatalf("curl: %v\n%s", err, out)
		}
		// curl prints the body, then the code on the last line.
		s := string(out)
		i := strings.LastIndex(s, "\n")
		return parseOut(t, []byte(s[i+1:]+"\n"+s[:i]))
	})
}

// nodeGuard makes any DNS lookup but loopback fail, so a Node that
// ignored the proxy could not reach the real host.
const nodeGuard = `const dns = require('node:dns');
const orig = dns.lookup;
dns.lookup = (host, opts, cb) => {
  if (typeof opts === 'function') { cb = opts; opts = {}; }
  if (host !== 'localhost' && host !== '127.0.0.1') { const e = new Error('guard: no lookup of ' + host); e.code = 'ENOTFOUND'; return process.nextTick(cb, e); }
  return orig(host, opts, cb);
};
dns.promises.lookup = async (host) => { throw new Error('guard: no lookup of ' + host); };
`

const nodeClient = `const [url, form, auth] = process.argv.slice(1);
fetch(url, {method: 'POST', body: form, headers: {'content-type': 'application/x-www-form-urlencoded', authorization: auth}})
  .then(async r => { console.log(r.status); console.log(await r.text()); })
  .catch(e => { console.error(e, e.cause); process.exit(1); });
`

func TestNode(t *testing.T) {
	bin, err := exec.LookPath("node")
	if err != nil {
		t.Skip("node not installed")
	}
	if out, _ := exec.Command(bin, "-p", "+process.versions.node.split('.')[0] >= 24").Output(); strings.TrimSpace(string(out)) != "true" { //nolint:gosec // test
		t.Skip("NODE_USE_ENV_PROXY needs Node 24")
	}
	dir := t.TempDir()
	guard := filepath.Join(dir, "guard.cjs")
	if err := os.WriteFile(guard, []byte(nodeGuard), 0o600); err != nil {
		t.Fatal(err)
	}
	runScenarios(t, func(t *testing.T, h *harness, ca string) (int, string) {
		cmd := exec.Command(bin, "-r", guard, "-e", nodeClient, "https://"+twilioHost+twilioPath, twilioForm, twilioAuth) //nolint:gosec // test
		cmd.Env = []string{"PATH=" + os.Getenv("PATH"), "HOME=" + dir, "NODE_USE_ENV_PROXY=1", "HTTPS_PROXY=" + h.proxyURL, "NODE_EXTRA_CA_CERTS=" + ca}
		out, err := cmd.CombinedOutput()
		if err != nil {
			t.Fatalf("node: %v\n%s", err, out)
		}
		return parseOut(t, out)
	})
}

// pythonClient uses requests with HTTPS_PROXY and REQUESTS_CA_BUNDLE.
// getaddrinfo is guarded so only loopback resolves.
const pythonClient = `import socket, sys
orig = socket.getaddrinfo
def guard(host, *a, **k):
    if host not in ('127.0.0.1', 'localhost'):
        raise socket.gaierror('guard: no lookup of %s' % host)
    return orig(host, *a, **k)
socket.getaddrinfo = guard
try:
    import requests
except ImportError:
    from pip._vendor import requests
url, form, auth = sys.argv[1:4]
r = requests.post(url, data=form, headers={'Content-Type': 'application/x-www-form-urlencoded', 'Authorization': auth}, timeout=10)
print(r.status_code)
print(r.text)
`

func TestPythonRequests(t *testing.T) {
	bin, err := exec.LookPath("python3")
	if err != nil {
		t.Skip("python3 not installed")
	}
	if exec.Command(bin, "-I", "-c", "import requests").Run() != nil { //nolint:gosec // test
		t.Skip("python requests not installed")
	}
	runScenarios(t, func(t *testing.T, h *harness, ca string) (int, string) {
		cmd := exec.Command(bin, "-I", "-c", pythonClient, "https://"+twilioHost+twilioPath, twilioForm, twilioAuth) //nolint:gosec // test
		cmd.Env = []string{"PATH=" + os.Getenv("PATH"), "HOME=" + t.TempDir(), "HTTPS_PROXY=" + h.proxyURL, "REQUESTS_CA_BUNDLE=" + ca}
		out, err := cmd.CombinedOutput()
		if err != nil {
			t.Fatalf("python: %v\n%s", err, out)
		}
		return parseOut(t, out)
	})
}

func TestKeepAliveServesSeveralRequestsPerTunnel(t *testing.T) {
	h := newHarness(t, setup{mode: catcher.Seal, mitm: true})
	c := goClient(t, h, h.echoCA)
	reused := 0
	for i := range 3 {
		req, _ := http.NewRequest(http.MethodPost, "https://"+twilioHost+twilioPath, strings.NewReader(twilioForm))
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		req = req.WithContext(httptrace.WithClientTrace(req.Context(), &httptrace.ClientTrace{
			GotConn: func(i httptrace.GotConnInfo) {
				if i.Reused {
					reused++
				}
			}}))
		resp, err := c.Do(req)
		if err != nil {
			t.Fatalf("request %d: %v", i, err)
		}
		_, _ = io.Copy(io.Discard, resp.Body)
		_ = resp.Body.Close()
		if resp.StatusCode != 201 {
			t.Fatalf("request %d: %s", i, resp.Status)
		}
	}
	if reused != 2 {
		t.Errorf("reused the tunnel %d times, want 2", reused)
	}
	if ms := h.messages(t, 3); len(ms) != 3 {
		t.Errorf("recorded %d messages, want 3", len(ms))
	}
}

func TestSealedWithoutMITMRefusesHTTPS(t *testing.T) {
	h := newHarness(t, setup{mode: catcher.Seal})
	c := goClient(t, h, h.bundle)
	req, _ := http.NewRequest(http.MethodPost, "https://"+twilioHost+twilioPath, strings.NewReader(twilioForm))
	_, err := c.Do(req) //nolint:bodyclose // fails before a body
	if err == nil || !strings.Contains(err.Error(), "Forbidden") {
		t.Fatalf("err = %v, want the CONNECT refused", err)
	}
	ms := h.messages(t, 1)
	if len(ms) != 1 || ms[0].Status != model.Blocked || !strings.Contains(ms[0].Error, "MITM") || h.hits.Load() != 0 {
		t.Errorf("messages = %+v", ms)
	}
}

func TestObserveWithoutMITMTunnels(t *testing.T) {
	h := newHarness(t, setup{mode: catcher.Observe})
	status, body := postTwilio(t, goClient(t, h, h.bundle), "https://"+twilioHost+twilioPath)
	if status != 201 || !strings.Contains(body, "accepted-by-upstream") {
		t.Fatalf("got %d %s", status, body)
	}
	if ms := h.messages(t, 1); len(ms) != 1 || ms[0].Method != http.MethodConnect || ms[0].Status != model.Sent {
		t.Errorf("messages = %+v", ms)
	}
}

func TestPlainHTTP(t *testing.T) {
	// A plain-HTTP upstream on loopback stands for any cleartext API.
	var got http.Header
	up := http.Server{ReadHeaderTimeout: time.Second, Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		got = r.Header.Clone()
		w.Header().Set("Content-Encoding", "gzip")
		_, _ = w.Write(gzipped(t, `{"plain":"ok"}`))
	})}
	l, _ := net.Listen("tcp", "127.0.0.1:0")
	go func() { _ = up.Serve(l) }()
	t.Cleanup(func() { _ = up.Close() })

	t.Run("sealed is faked", func(t *testing.T) {
		h := newHarness(t, setup{mode: catcher.Seal})
		status, body := postTwilio(t, goClient(t, h, h.echoCA), "http://"+twilioHost+twilioPath)
		if status != 201 || !strings.Contains(body, "queued") || h.hits.Load() != 0 {
			t.Fatalf("got %d %s", status, body)
		}
	})
	t.Run("observed is forwarded and recorded", func(t *testing.T) {
		h := newHarness(t, setup{mode: catcher.Observe})
		// Loopback is passed through, so this is forwarded and recorded
		// in the after phase. A raw request lets us send hop headers.
		pu, _ := url.Parse(h.proxyURL)
		conn, err := net.Dial("tcp", pu.Host)
		if err != nil {
			t.Fatal(err)
		}
		defer func() { _ = conn.Close() }()
		target := "http://" + l.Addr().String() + "/hook?x=1"
		_, _ = io.WriteString(conn, "POST "+target+" HTTP/1.1\r\nHost: "+l.Addr().String()+
			"\r\nConnection: keep-alive, X-Secret-Hop\r\nX-Secret-Hop: drop-me\r\nProxy-Authorization: Basic Zm9vOmJhcg==\r\n"+
			"X-Echo-Run: r9/checkout\r\nAccept-Encoding: gzip\r\nContent-Length: 7\r\n\r\n{\"a\":1}")
		resp, err := http.ReadResponse(bufioReader(conn), nil)
		if err != nil {
			t.Fatal(err)
		}
		_, _ = io.Copy(io.Discard, resp.Body)
		_ = resp.Body.Close()
		if resp.StatusCode != 200 {
			t.Fatalf("status %d", resp.StatusCode)
		}
		for _, k := range []string{"X-Secret-Hop", "Proxy-Authorization", "X-Echo-Run"} {
			if got.Get(k) != "" {
				t.Errorf("%s was forwarded", k)
			}
		}
		if got.Get("Via") != via {
			t.Errorf("Via = %q", got.Get("Via"))
		}
		ms := h.messages(t, 1)
		if len(ms) != 1 || ms[0].Run != "r9" || ms[0].Action != "checkout" || ms[0].Response == nil || ms[0].Response.Body != `{"plain":"ok"}` {
			t.Errorf("messages = %+v", ms)
		}
	})
}

// TestAllowedLivePaymentRefused: an allowlisted payment host is still
// refused, and not forwarded, when the call is in live mode.
func TestAllowedLivePaymentRefused(t *testing.T) {
	for _, mode := range []string{catcher.Seal, catcher.Observe} {
		t.Run(mode, func(t *testing.T) {
			h := newHarness(t, setup{mode: mode, allow: []string{"api.stripe.com"}})
			req, _ := http.NewRequest(http.MethodPost, "http://api.stripe.com/v1/charges", strings.NewReader("amount=1000&currency=usd"))
			req.Header.Set("Authorization", strings.Join([]string{"Bearer sk", "_live_", "abcdefghijklmnop1234"}, ""))
			req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
			resp, err := goClient(t, h, h.echoCA).Do(req)
			if err != nil {
				t.Fatal(err)
			}
			b, _ := io.ReadAll(resp.Body)
			_ = resp.Body.Close()
			if resp.StatusCode != http.StatusForbidden || !strings.Contains(string(b), "rhythm_echo_sealed") {
				t.Fatalf("got %d %s", resp.StatusCode, b)
			}
			if ms := h.messages(t, 1); len(ms) != 1 || ms[0].Status != model.Blocked || ms[0].Provider != "stripe" {
				t.Errorf("messages = %+v", ms)
			}
		})
	}
}

func TestTokenRequiredWhenSet(t *testing.T) {
	h := newHarness(t, setup{mode: catcher.Seal, mitm: true, token: "s3cret-echo"})
	c := goClient(t, h, h.echoCA)
	req, _ := http.NewRequest(http.MethodGet, "http://"+twilioHost+"/", nil)
	resp, err := c.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	_ = resp.Body.Close()
	if resp.StatusCode != http.StatusProxyAuthRequired {
		t.Fatalf("no token: %s", resp.Status)
	}
	pu, _ := url.Parse(h.proxyURL)
	pu.User = url.UserPassword("echo", "s3cret-echo")
	c.Transport.(*http.Transport).Proxy = http.ProxyURL(pu)
	c.Transport.(*http.Transport).DialContext = nil
	status, body := postTwilio(t, c, "https://"+twilioHost+twilioPath)
	if status != 201 || !strings.Contains(body, "queued") {
		t.Fatalf("with token: %d %s", status, body)
	}
}

func TestListenBeyondLoopbackNeedsToken(t *testing.T) {
	p, err := New(Options{Server: catcher.NewServer(catcher.NewInbox(1), catcher.Config{})})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = p.Close() }()
	if err := p.ListenAndServe("0.0.0.0:0"); err == nil || !strings.Contains(err.Error(), "token") {
		t.Fatalf("err = %v", err)
	}
}

func TestServesCAAndRefusesLoops(t *testing.T) {
	h := newHarness(t, setup{mode: catcher.Seal, mitm: true})
	resp, err := http.Get(h.proxyURL + "/ca.pem")
	if err != nil {
		t.Fatal(err)
	}
	b, _ := io.ReadAll(resp.Body)
	_ = resp.Body.Close()
	if string(b) != string(h.ca.PEM) {
		t.Errorf("GET /ca.pem = %q", b)
	}
	c := goClient(t, h, h.echoCA)
	req, _ := http.NewRequest(http.MethodGet, "http://127.0.0.1:1/", nil)
	req.Header.Set("Via", via)
	resp, err = c.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	_ = resp.Body.Close()
	if resp.StatusCode != http.StatusLoopDetected {
		t.Errorf("loop: %s", resp.Status)
	}
}

// fakeResolver answers from a map; any other name doesn't resolve.
type fakeResolver map[string][]string

func (f fakeResolver) LookupHost(_ context.Context, host string) ([]string, error) {
	if a, ok := f[host]; ok {
		return a, nil
	}
	return nil, &net.DNSError{Err: "no such host", Name: host, IsNotFound: true}
}

var testNames = fakeResolver{
	"wordpress": {"172.18.0.5"},            // a Docker service
	"db":        {"172.18.0.5", "fd00::5"}, // private v4 and ULA
	"api":       {"93.184.216.34"},         // a search domain made it public
	"mixed":     {"10.0.0.1", "8.8.8.8"},   // one public address is enough to refuse
	"meta":      {"169.254.169.254"},       // a name for the metadata endpoint
	"linklocal": {"169.254.10.10"},         // link-local is neither loopback nor private
}

func TestAllowedHosts(t *testing.T) {
	for _, mode := range []string{catcher.Seal, catcher.Observe} {
		p, err := New(Options{Server: catcher.NewServer(catcher.NewInbox(1), catcher.Config{Mode: mode}),
			Allow: []string{"*.stripe.com", "169.254.*"}, Resolver: testNames})
		if err != nil {
			t.Fatal(err)
		}
		for host, want := range map[string]bool{
			"127.0.0.1": true, "::1": true, "10.1.2.3": true, "192.168.1.5": true, "172.20.0.4": true, "fd12::1": true,
			"localhost": true, "shop.localhost": true, "wordpress": true, "db": true, "api.stripe.com": true,
			"169.254.169.254": mode == catcher.Observe, "fd00:ec2::254": mode == catcher.Observe, "169.254.1.1": true, // by the glob
			"api": false, "mixed": false, "meta": false, "linklocal": false, "unknown": false, "fe80::1": false,
			"api.twilio.com": false, "8.8.8.8": false, "stripe.com.evil.test": false, "hooks.slack.com": false,
		} {
			if got, _ := p.allowed(context.Background(), host); got != want {
				t.Errorf("%s: allowed(%q) = %v, want %v", mode, host, got, want)
			}
		}
		_ = p.Close()
	}
}

// TestSealedSingleLabelAndPrivateHosts: a single-label name goes through
// only to the private address it was checked against and is recorded as
// sent. One that resolves to a public address is faked, as is the
// metadata endpoint even when a glob allows it.
func TestSealedSingleLabelAndPrivateHosts(t *testing.T) {
	up := http.Server{ReadHeaderTimeout: time.Second, Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.WriteString(w, `{"from":"`+r.Host+`"}`)
	})}
	ul, _ := net.Listen("tcp", "127.0.0.1:0")
	go func() { _ = up.Serve(ul) }()
	t.Cleanup(func() { _ = up.Close() })

	var dialed []string
	dial := func(ctx context.Context, network, addr string) (net.Conn, error) {
		dialed = append(dialed, addr)
		if addr != "172.18.0.5:80" { // the only "private" address that exists here
			return nil, &net.OpError{Op: "dial", Net: network, Err: errNoNet{addr}}
		}
		var d net.Dialer
		return d.DialContext(ctx, network, ul.Addr().String())
	}
	inbox := catcher.NewInbox(20)
	p, err := New(Options{Server: catcher.NewServer(inbox, catcher.Config{Mode: catcher.Seal, Allow: []string{"169.254.*"}}),
		Allow: []string{"*.internal.test"}, Resolver: testNames, Dial: dial})
	if err != nil {
		t.Fatal(err)
	}
	l, _ := net.Listen("tcp", "127.0.0.1:0")
	go func() { _ = p.Serve(l) }()
	t.Cleanup(func() { _ = p.Close() })
	pu, _ := url.Parse("http://" + l.Addr().String())
	tr := &http.Transport{Proxy: http.ProxyURL(pu)}
	t.Cleanup(tr.CloseIdleConnections)
	c := &http.Client{Transport: tr, Timeout: 5 * time.Second}
	get := func(rawURL string) (int, string, string) {
		t.Helper()
		resp, err := c.Get(rawURL) //nolint:noctx // test
		if err != nil {
			t.Fatal(err)
		}
		b, _ := io.ReadAll(resp.Body)
		_ = resp.Body.Close()
		return resp.StatusCode, string(b), resp.Header.Get("X-Echo-Id")
	}

	// db resolves to 172.18.0.5: forwarded to that address and recorded.
	status, body, _ := get("http://db/orders")
	if status != 200 || body != `{"from":"db"}` {
		t.Fatalf("db: %d %s", status, body)
	}
	if len(dialed) != 1 || dialed[0] != "172.18.0.5:80" {
		t.Errorf("dialed %v, want only the checked address", dialed)
	}
	ms := inbox.List(catcher.Filter{})
	if len(ms) != 1 || ms[0].URL != "http://db/orders" || ms[0].Status != model.Sent || ms[0].Source.Via != "proxy" {
		t.Fatalf("db capture = %+v", ms)
	}

	// A tunnel to db is pinned the same way and recorded as sent.
	dialed = nil
	conn, err := net.Dial("tcp", l.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	_ = conn.SetDeadline(time.Now().Add(5 * time.Second))
	_, _ = io.WriteString(conn, "CONNECT db:80 HTTP/1.1\r\nHost: db:80\r\n\r\nGET / HTTP/1.1\r\nHost: db\r\nConnection: close\r\n\r\n")
	br := bufioReader(conn)
	if resp, err := http.ReadResponse(br, &http.Request{Method: http.MethodConnect}); err != nil || resp.StatusCode != 200 { //nolint:bodyclose // a CONNECT answer has no body
		t.Fatalf("CONNECT db: %v %v", resp, err)
	}
	resp, err := http.ReadResponse(br, nil)
	if err != nil {
		t.Fatal(err)
	}
	b, _ := io.ReadAll(resp.Body)
	_ = resp.Body.Close()
	_ = conn.Close()
	if string(b) != `{"from":"db"}` || len(dialed) != 1 || dialed[0] != "172.18.0.5:80" {
		t.Errorf("tunnel: %s, dialed %v", b, dialed)
	}
	if ms := inbox.List(catcher.Filter{}); len(ms) != 2 || ms[1].Method != http.MethodConnect || ms[1].Status != model.Sent {
		t.Errorf("tunnel capture = %+v", ms)
	}

	// api resolves to a public address: faked, recorded, never dialed.
	dialed = nil
	status, _, id := get("http://api/v1/send")
	if id == "" || len(dialed) != 0 {
		t.Errorf("api: %d id %q, dialed %v", status, id, dialed)
	}
	if m, ok := inbox.Get(id); !ok || m.Status != model.Faked || m.URL != "http://api/v1/send" {
		t.Errorf("api capture = %+v", m)
	}

	// The metadata endpoint is faked though the catcher's glob allows it.
	status, _, id = get("http://169.254.169.254/latest/meta-data/iam/")
	if status != http.StatusNotFound || id == "" || len(dialed) != 0 {
		t.Errorf("metadata: %d id %q, dialed %v", status, id, dialed)
	}
	if m, ok := inbox.Get(id); !ok || m.Status != model.Faked {
		t.Errorf("metadata capture = %+v", m)
	}
	// And a tunnel to it is refused, not opened.
	req, _ := http.NewRequest(http.MethodGet, "https://169.254.169.254/", nil)
	if _, err := c.Do(req); err == nil || !strings.Contains(err.Error(), "Forbidden") { //nolint:bodyclose // fails before a body
		t.Errorf("metadata tunnel: %v", err)
	}
	if len(dialed) != 0 {
		t.Errorf("dialed %v", dialed)
	}
}

// TestMetadataByName: a glob such as "*" lets any name through, but
// never the clouds' own names for the metadata endpoint while sealed.
func TestMetadataByName(t *testing.T) {
	for _, mode := range []string{catcher.Seal, catcher.Observe} {
		p, err := New(Options{Server: catcher.NewServer(catcher.NewInbox(1), catcher.Config{Mode: mode}),
			Allow: []string{"*"}, Resolver: testNames})
		if err != nil {
			t.Fatal(err)
		}
		for _, host := range []string{"metadata.google.internal", "METADATA.Google.Internal.", "metadata.goog", "instance-data.ec2.internal"} {
			if got, _ := p.allowed(context.Background(), host); got != (mode == catcher.Observe) {
				t.Errorf("%s: allowed(%q) = %v", mode, host, got)
			}
		}
		if got, _ := p.allowed(context.Background(), "shop.example"); !got {
			t.Errorf("%s: the glob no longer allows other names", mode)
		}
		_ = p.Close()
	}
}

// TestSealedDialRefusesNamesForMetadata: any name can resolve to
// 169.254.169.254, so while sealed the default dialer resolves names
// itself, refuses those and dials only the addresses it checked.
func TestSealedDialRefusesNamesForMetadata(t *testing.T) {
	names := fakeResolver{
		"evil.test":  {"169.254.169.254"},
		"evil6.test": {"10.0.0.1", "fd00:ec2::254"},
		"ok.test":    {"127.0.0.1"},
	}
	for _, mode := range []string{catcher.Seal, catcher.Observe} {
		p, err := New(Options{Server: catcher.NewServer(catcher.NewInbox(1), catcher.Config{Mode: mode}), Resolver: names})
		if err != nil {
			t.Fatal(err)
		}
		var dialed []string
		dial := p.sealedDial(func(_ context.Context, network, addr string) (net.Conn, error) {
			dialed = append(dialed, addr)
			return nil, &net.OpError{Op: "dial", Net: network, Err: errNoNet{addr}}
		})
		for host, want := range map[string]string{
			"evil.test:80":                "",
			"evil6.test:80":               "",
			"169.254.169.254:80":          "",
			"metadata.google.internal:80": "",
			"ok.test:8080":                "127.0.0.1:8080",
			"10.0.0.9:80":                 "10.0.0.9:80",
		} {
			dialed = nil
			_, err := dial(context.Background(), "tcp", host)
			switch {
			case mode == catcher.Observe:
				if len(dialed) != 1 || dialed[0] != host {
					t.Errorf("observe %s: dialed %v", host, dialed)
				}
			case want == "":
				if !errors.Is(err, errMetadata) || len(dialed) != 0 {
					t.Errorf("sealed %s: err %v, dialed %v", host, err, dialed)
				}
			default:
				if len(dialed) != 1 || dialed[0] != want {
					t.Errorf("sealed %s: dialed %v, want %s", host, dialed, want)
				}
			}
		}
		_ = p.Close()
	}
}

// TestSealedGlobNeverForwardsToMetadata: with the catcher allowing "*",
// a sealed request to a name for 169.254.169.254 never leaves, while a
// name for a reachable address is forwarded to the address checked.
func TestSealedGlobNeverForwardsToMetadata(t *testing.T) {
	up := http.Server{ReadHeaderTimeout: time.Second, Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.WriteString(w, "reached "+r.Host)
	})}
	ul, _ := net.Listen("tcp", "127.0.0.1:0")
	go func() { _ = up.Serve(ul) }()
	t.Cleanup(func() { _ = up.Close() })
	_, port, _ := net.SplitHostPort(ul.Addr().String())

	p, err := New(Options{Server: catcher.NewServer(catcher.NewInbox(10), catcher.Config{Mode: catcher.Seal, Allow: []string{"*"}}),
		Resolver: fakeResolver{"evil.test": {"169.254.169.254"}, "ok.test": {"127.0.0.1"}}})
	if err != nil {
		t.Fatal(err)
	}
	l, _ := net.Listen("tcp", "127.0.0.1:0")
	go func() { _ = p.Serve(l) }()
	t.Cleanup(func() { _ = p.Close() })
	pu, _ := url.Parse("http://" + l.Addr().String())
	tr := &http.Transport{Proxy: http.ProxyURL(pu)}
	t.Cleanup(tr.CloseIdleConnections)
	c := &http.Client{Transport: tr, Timeout: 5 * time.Second}
	get := func(rawURL string) (int, string) {
		t.Helper()
		resp, err := c.Get(rawURL) //nolint:noctx // test
		if err != nil {
			t.Fatal(err)
		}
		b, _ := io.ReadAll(resp.Body)
		_ = resp.Body.Close()
		return resp.StatusCode, string(b)
	}
	if status, body := get("http://ok.test:" + port + "/"); status != 200 || body != "reached ok.test:"+port {
		t.Errorf("ok.test: %d %s", status, body)
	}
	if status, body := get("http://evil.test:" + port + "/latest/meta-data/"); status == 200 || strings.Contains(body, "reached") {
		t.Errorf("evil.test: %d %s", status, body)
	}
}

func TestWriteFake(t *testing.T) {
	rec := &recorder{header: http.Header{}}
	writeFake(rec, wire.Decision{ID: "m1", Response: &wire.Response{Status: 202, Headers: map[string]string{"X-A": "b"}, Body: "{}"}})
	if rec.status != 202 || rec.header.Get("X-Echo-Id") != "m1" || rec.header.Get("X-A") != "b" {
		t.Errorf("fake: %d %v", rec.status, rec.header)
	}
}

// TestPaymentTunnelsNeedMITM: a blind tunnel to a payment API could carry
// live keys Echo can't see, so it's refused in every mode, allowlisted or
// not. With MITM the host is intercepted instead, even when allowlisted.
// A gateway's live host is refused as live; one host for test and live
// keys asks for --mitm.
func TestPaymentTunnelsNeedMITM(t *testing.T) {
	why := map[string]string{
		"api.stripe.com": "payment APIs need --mitm", "api.mollie.com": "payment APIs need --mitm", "api.razorpay.com": "payment APIs need --mitm",
		"api-m.paypal.com": "a live PayPal API is never reached from staging; use the PayPal sandbox", "api.paypal.com": "a live PayPal API",
		"api.braintreegateway.com": "a live Braintree API", "connect.squareup.com": "a live Square API",
		"shop-Demo-checkout-live.adyenpayments.com": "a live Adyen API", "api.authorize.net": "a live Authorize.net API",
	}
	for _, s := range []setup{
		{mode: catcher.Seal},
		{mode: catcher.Observe},
		{mode: catcher.Observe, allow: []string{"api.stripe.com", "api-m.paypal.com", "api.braintreegateway.com"}},
		{mode: catcher.Seal, allow: []string{"*.stripe.com", "*.squareup.com"}},
	} {
		h := newHarness(t, s)
		for host := range why {
			req, _ := http.NewRequest(http.MethodPost, "https://"+host+"/v1/charges", strings.NewReader("amount=1000"))
			_, err := goClient(t, h, h.bundle).Do(req) //nolint:bodyclose // fails before a body
			if err == nil || !strings.Contains(err.Error(), "Forbidden") {
				t.Errorf("%+v %s: err = %v, want the CONNECT refused", s, host, err)
			}
		}
		ms := h.messages(t, len(why))
		for _, m := range ms {
			u, _ := url.Parse(m.URL)
			if m.Status != model.Blocked || m.Channel != model.Payment || !strings.Contains(m.Error, why[u.Hostname()]) {
				t.Errorf("%+v: message = %+v", s, m)
			}
		}
		if len(ms) != len(why) {
			t.Errorf("%+v: %d messages, want %d", s, len(ms), len(why))
		}
	}
	if !blindPayment("API.Stripe.com") || blindPayment("api-m.sandbox.paypal.com") || blindPayment(twilioHost) {
		t.Error("blindPayment: Stripe and live PayPal only")
	}
	for host, blind := range map[string]bool{
		"api.braintreegateway.com": true, "api.sandbox.braintreegateway.com": false, "connect.squareupsandbox.com": false,
		"checkout-test.adyen.com": false, "apitest.authorize.net": false, "api.mollie.com": true,
	} {
		if blindPayment(host) != blind {
			t.Errorf("blindPayment(%s) = %v", host, !blind)
		}
	}
}

// TestBraintreeTunnelRefusedWhenObserving is the case a test could
// otherwise charge a card through: observing, no MITM, a live Braintree
// host. The CONNECT is answered 403 and recorded as blocked.
func TestBraintreeTunnelRefusedWhenObserving(t *testing.T) {
	h := newHarness(t, setup{mode: catcher.Observe})
	req, _ := http.NewRequest(http.MethodPost, "https://api.braintreegateway.com/merchants/abc/transactions", strings.NewReader("<transaction/>"))
	_, err := goClient(t, h, h.bundle).Do(req) //nolint:bodyclose // fails before a body
	if err == nil || !strings.Contains(err.Error(), "Forbidden") {
		t.Fatalf("err = %v, want the CONNECT refused with 403", err)
	}
	ms := h.messages(t, 1)
	if len(ms) != 1 || ms[0].Provider != "braintree" || ms[0].Status != model.Blocked || ms[0].Method != http.MethodConnect {
		t.Errorf("messages = %+v", ms)
	}
}

// TestSandboxGatewayTunnelsPassWhenObserving: a sandbox host is a test,
// so it's tunneled like any other host when observing.
func TestSandboxGatewayTunnelsPassWhenObserving(t *testing.T) {
	h := newHarness(t, setup{mode: catcher.Observe})
	req, _ := http.NewRequest(http.MethodPost, "https://api.sandbox.braintreegateway.com/merchants/abc/transactions", strings.NewReader("<transaction/>"))
	_, err := goClient(t, h, h.bundle).Do(req) //nolint:bodyclose // fails before a body
	if err != nil && strings.Contains(err.Error(), "Forbidden") {
		t.Fatalf("sandbox tunnel refused: %v", err)
	}
	ms := h.messages(t, 1)
	if len(ms) != 1 || ms[0].Provider != "braintree" || ms[0].Status == model.Blocked {
		t.Errorf("messages = %+v", ms)
	}
}

func TestAllowedPaymentHostIsInterceptedWithMITM(t *testing.T) {
	h := newHarness(t, setup{mode: catcher.Seal, mitm: true, allow: []string{"api.stripe.com"}})
	req, _ := http.NewRequest(http.MethodPost, "https://api.stripe.com/v1/charges", strings.NewReader("amount=1000&currency=usd"))
	req.Header.Set("Authorization", strings.Join([]string{"Bearer sk", "_live_", "abcdefghijklmnop1234"}, ""))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	resp, err := goClient(t, h, h.echoCA).Do(req)
	if err != nil {
		t.Fatal(err)
	}
	b, _ := io.ReadAll(resp.Body)
	_ = resp.Body.Close()
	if resp.StatusCode != http.StatusForbidden || !strings.Contains(string(b), "rhythm_echo_sealed") {
		t.Fatalf("got %d %s", resp.StatusCode, b)
	}
	if ms := h.messages(t, 1); len(ms) != 1 || ms[0].Provider != "stripe" || ms[0].Status != model.Blocked {
		t.Errorf("messages = %+v", ms)
	}
}

// TestShutdownStopsListenerAndTunnels: after Shutdown the port refuses
// connections and an open tunnel is closed.
func TestShutdownStopsListenerAndTunnels(t *testing.T) {
	up, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = up.Close() }()
	go func() {
		for {
			c, err := up.Accept()
			if err != nil {
				return
			}
			go func() { _, _ = io.Copy(c, c); _ = c.Close() }() // echo server
		}
	}()

	p, err := New(Options{Server: catcher.NewServer(catcher.NewInbox(10), catcher.Config{Mode: catcher.Observe})})
	if err != nil {
		t.Fatal(err)
	}
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	served := make(chan error, 1)
	go func() { served <- p.Serve(l) }()

	conn, err := net.Dial("tcp", l.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = conn.Close() }()
	_ = conn.SetDeadline(time.Now().Add(5 * time.Second))
	_, _ = io.WriteString(conn, "CONNECT "+up.Addr().String()+" HTTP/1.1\r\nHost: "+up.Addr().String()+"\r\n\r\n")
	br := bufioReader(conn)
	resp, err := http.ReadResponse(br, &http.Request{Method: http.MethodConnect}) //nolint:bodyclose // a CONNECT answer has no body; the conn is the tunnel
	if err != nil || resp.StatusCode != http.StatusOK {
		t.Fatalf("CONNECT: %v %v", resp, err)
	}
	_, _ = io.WriteString(conn, "ping")
	b := make([]byte, 4)
	if _, err := io.ReadFull(br, b); err != nil || string(b) != "ping" {
		t.Fatalf("tunnel answered %q, %v", b, err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	if err := p.Shutdown(ctx); err != nil {
		t.Fatalf("Shutdown: %v", err)
	}
	if err := <-served; !errors.Is(err, http.ErrServerClosed) {
		t.Errorf("Serve returned %v", err)
	}
	if c, err := net.DialTimeout("tcp", l.Addr().String(), time.Second); err == nil {
		_ = c.Close()
		t.Error("the proxy port still accepts connections")
	}
	if _, err := br.ReadByte(); err == nil {
		t.Error("the tunnel is still open")
	}
}

// TestLiveGatewayInterceptedWithMITM: with MITM a live gateway host is
// intercepted and answered with the gateway's own error, observing and
// allowlisted.
func TestLiveGatewayInterceptedWithMITM(t *testing.T) {
	h := newHarness(t, setup{mode: catcher.Observe, mitm: true, allow: []string{"connect.squareup.com"}})
	req, _ := http.NewRequest(http.MethodPost, "https://connect.squareup.com/v2/payments", strings.NewReader(`{"amount_money":{"amount":100,"currency":"USD"}}`))
	req.Header.Set("Content-Type", "application/json")
	resp, err := goClient(t, h, h.echoCA).Do(req)
	if err != nil {
		t.Fatal(err)
	}
	b, _ := io.ReadAll(resp.Body)
	_ = resp.Body.Close()
	if resp.StatusCode != http.StatusForbidden || !strings.Contains(string(b), "AUTHENTICATION_ERROR") {
		t.Fatalf("got %d %s", resp.StatusCode, b)
	}
	if ms := h.messages(t, 1); len(ms) != 1 || ms[0].Provider != "square" || ms[0].Status != model.Blocked || h.hits.Load() != 0 {
		t.Errorf("messages = %+v, upstream hits %d", ms, h.hits.Load())
	}
}
