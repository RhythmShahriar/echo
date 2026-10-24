package proxy

import (
	"context"
	"crypto/tls"
	"crypto/x509"
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

func TestAllowedHosts(t *testing.T) {
	p := &Proxy{opt: Options{Allow: []string{"*.stripe.com"}}}
	for host, want := range map[string]bool{
		"127.0.0.1": true, "::1": true, "10.1.2.3": true, "192.168.1.5": true, "172.20.0.4": true, "169.254.1.1": true,
		"localhost": true, "shop.localhost": true, "wordpress": true, "api.stripe.com": true,
		"api.twilio.com": false, "8.8.8.8": false, "stripe.com.evil.test": false, "hooks.slack.com": false,
	} {
		if got := p.allowed(host); got != want {
			t.Errorf("allowed(%q) = %v, want %v", host, got, want)
		}
	}
}

func TestWriteFake(t *testing.T) {
	rec := &recorder{header: http.Header{}}
	writeFake(rec, wire.Decision{ID: "m1", Response: &wire.Response{Status: 202, Headers: map[string]string{"X-A": "b"}, Body: "{}"}})
	if rec.status != 202 || rec.header.Get("X-Echo-Id") != "m1" || rec.header.Get("X-A") != "b" {
		t.Errorf("fake: %d %v", rec.status, rec.header)
	}
}
