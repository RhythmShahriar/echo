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
	"regexp"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/RhythmShahriar/echo/catcher"
	"github.com/RhythmShahriar/echo/check"
	"github.com/RhythmShahriar/echo/client"
	"github.com/RhythmShahriar/echo/config"
	"github.com/RhythmShahriar/echo/model"
	"github.com/RhythmShahriar/echo/wire"
)

// wiringPorts returns n loopback addresses in 21300-21349, the range
// the tests of up's flags use, that nothing listens on right now.
func wiringPorts(t *testing.T, n int) []string {
	t.Helper()
	var out []string
	for port := 21300; port < 21350 && len(out) < n; port++ {
		addr := fmt.Sprintf("127.0.0.1:%d", port)
		if l, err := net.Listen("tcp", addr); err == nil {
			_ = l.Close()
			out = append(out, addr)
		}
	}
	if len(out) < n {
		t.Skipf("only %d free ports in 21300-21349", len(out))
	}
	return out
}

// apiCall makes one request to a running up with an optional token.
func apiCall(t *testing.T, method, url, token, body string) (int, string) {
	t.Helper()
	req, err := http.NewRequestWithContext(context.Background(), method, url, strings.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	if body != "" {
		req.Header.Set("Content-Type", "application/json")
	}
	if token != "" {
		req.Header.Set(wire.TokenHeader, token)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = resp.Body.Close() }()
	b, _ := io.ReadAll(resp.Body)
	return resp.StatusCode, string(b)
}

// emailCapture is a capture of one email, as JSON.
func emailCapture(subject string) string {
	b, _ := json.Marshal(wire.Capture{Version: 1, Kind: "email", Source: wire.Source{Via: "test"},
		Email: &wire.Email{From: "shop@shop.test", To: []string{"ann@client.test"}, Subject: subject, Text: "Hello Ann"}})
	return string(b)
}

// TestImageCMDFlagsAreUpFlags: every flag the Docker image's default CMD
// passes is one up has, so docker run with no arguments, the DDEV add-on
// and the Lando recipe start.
func TestImageCMDFlagsAreUpFlags(t *testing.T) {
	b, err := os.ReadFile(filepath.Join("..", "..", "Dockerfile"))
	if err != nil {
		t.Fatal(err)
	}
	cmd := regexp.MustCompile(`(?m)^CMD \[(.*)\]$`).FindSubmatch(b)
	if cmd == nil {
		t.Fatal("no CMD in the Dockerfile")
	}
	flags := regexp.MustCompile(`"--([a-z0-9-]+)`).FindAllSubmatch(cmd[1], -1)
	_, help, _ := runCLI(t, "up", "--help")
	for _, f := range flags {
		if !strings.Contains(help, "--"+string(f[1])+" ") {
			t.Errorf("the image's CMD passes --%s, which up --help doesn't list", f[1])
		}
	}
	up, _ := lookup("up")
	if _, err := parse(up, []string{"--listen=0.0.0.0:8025", "--smtp=0.0.0.0:1025", "--private-network"}); err != nil {
		t.Errorf("the image's CMD doesn't parse: %v", err)
	}
}

// TestPrivateNetworkFlag: --private-network does what the variable does.
func TestPrivateNetworkFlag(t *testing.T) {
	t.Chdir(t.TempDir())
	t.Setenv("RHYTHM_ECHO_TOKEN", "")
	t.Setenv(privateNetworkEnv, "")
	orig := listIfaces
	listIfaces = func() ([]netIface, error) { return publicHost, nil }
	t.Cleanup(func() { listIfaces = orig })
	addr := "0.0.0.0:" + strings.TrimPrefix(wiringPorts(t, 1)[0], "127.0.0.1:")
	code, _, errOut := runCLI(t, "up", "--private-network", "--listen", addr, "--smtp", "off")
	if code != exitUsage || !strings.Contains(errOut, "--listen "+addr+" is reachable from a public address (203.0.113.5 on eth0)") {
		t.Errorf("exit %d: %s", code, errOut)
	}
}

// TestUpLoginFlag: --login works as RHYTHM_ECHO_LOGIN does and its
// errors name the flag.
func TestUpLoginFlag(t *testing.T) {
	upEnv(t)
	t.Setenv(loginEnv, "")
	if code, _, errOut := runCLI(t, "up", "--login", "client:short", "--listen", wiringPorts(t, 1)[0], "--smtp", "off"); code != exitUsage ||
		!strings.Contains(errOut, "rhythm echo: --login: the password must be at least 8 characters") {
		t.Errorf("short password: %d %s", code, errOut)
	}
	api := wiringPorts(t, 1)[0]
	out, _, stop := startUp(t, "--listen", api, "--smtp", "off", "--login", "client:correct horse")
	defer stop()
	if !strings.Contains(out.String(), "Login: viewers sign in as client at http://"+api+"/login") {
		t.Errorf("banner:\n%s", out)
	}
}

// TestUpFreePortsMovesWithoutAsking: with --free-ports up takes the next
// free pair at once when Mailpit holds the ports.
func TestUpFreePortsMovesWithoutAsking(t *testing.T) {
	upEnv(t)
	moveFreeBase(t)
	ports := wiringPorts(t, 2)
	fakeMailpit(t, ports[0], ports[1])
	old := askFreePorts
	askFreePorts = func(*session, string) bool { t.Error("up asked although --free-ports was given"); return false }
	t.Cleanup(func() { askFreePorts = old })
	api, _, ok := nextFreePair("127.0.0.1", "127.0.0.1", true)
	if !ok {
		t.Skip("no free pair")
	}
	up, _ := lookup("up")
	p, err := parse(up, []string{"--plain", "--listen", ports[0], "--smtp", ports[1], "--free-ports"})
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
	cancel()
	if code := <-done; code != exitOK {
		t.Errorf("exit %d", code)
	}
	s.stop()
	if !strings.Contains(out.String(), "API: http://"+api) || !strings.Contains(errOut.String(), "Saved to .echo/config.yaml") {
		t.Errorf("didn't move to %s:\n%s%s", api, out.String(), errOut.String())
	}
}

// TestUpKeepsTheInbox: --store keeps what Echo catches across restarts
// and health says so along with the real SMTP and proxy addresses.
func TestUpKeepsTheInbox(t *testing.T) {
	upEnv(t)
	ports := wiringPorts(t, 2)
	api, smtp := ports[0], ports[1]
	dir := filepath.Join(t.TempDir(), "inbox")
	args := []string{"--listen", api, "--smtp", smtp, "--store", dir, "--keep", "50", "--keep-for", "14d"}
	out, _, stop := startUp(t, args...)
	h, err := client.New("http://"+api, "").Health(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if h.Store != "jsonl" || h.SMTP != smtp || h.Proxy != "off" {
		t.Errorf("health: store %q, smtp %q, proxy %q", h.Store, h.SMTP, h.Proxy)
	}
	if !strings.Contains(out.String(), "Inbox: "+dir+"  (kept across restarts; 0 messages in it now, each kept 14d)") {
		t.Errorf("banner:\n%s", out)
	}
	if code, body := apiCall(t, http.MethodPost, "http://"+api+"/v1/capture", "", emailCapture("Kept")); code != http.StatusOK {
		t.Fatalf("capture: %d %s", code, body)
	}
	if code := stop(); code != exitOK {
		t.Fatalf("exit %d", code)
	}
	out, _, stop = startUp(t, args...)
	defer stop()
	ms, err := client.New("http://"+api, "").Messages(context.Background(), client.Filter{})
	if err != nil || len(ms) != 1 || ms[0].Subject != "Kept" {
		t.Errorf("after a restart: %v %v", ms, err)
	}
	if !strings.Contains(out.String(), "1 message in it now") {
		t.Errorf("banner after a restart:\n%s", out)
	}

	for _, c := range []struct {
		args []string
		want string
	}{
		{[]string{"--keep", "-1"}, "--keep -1: give how many messages to keep"},
		{[]string{"--keep-for", "soon"}, `--keep-for "soon": want a duration like 14d or 12h`},
		{[]string{"--keep-for", "14d"}, "--keep-for and keep_for: need a folder to keep messages in"},
	} {
		if code, _, errOut := runCLI(t, append([]string{"up", "--listen", wiringPorts(t, 1)[0], "--smtp", "off"}, c.args...)...); code != exitUsage || !strings.Contains(errOut, c.want) {
			t.Errorf("%v: %d %s", c.args, code, errOut)
		}
	}
}

// TestUpSiteTokens: with --sites each site's write token may only send
// captures, marked with its site; the read token reads them all.
func TestUpSiteTokens(t *testing.T) {
	upEnv(t)
	t.Setenv("RHYTHM_ECHO_TOKEN", "")
	t.Setenv("ECHO_TEST_READ", "read-token-21300")
	t.Setenv("ECHO_TEST_SITE_A", "write-token-aaaa")
	sites := filepath.Join(t.TempDir(), "sites.yaml")
	if err := os.WriteFile(sites, []byte("read_token_env: ECHO_TEST_READ\nsites:\n  - {id: client-a, token_env: ECHO_TEST_SITE_A}\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	api := wiringPorts(t, 1)[0]
	out, _, stop := startUp(t, "--listen", api, "--smtp", "off", "--sites", sites)
	defer stop()
	if !strings.Contains(out.String(), "Sites: client-a: each sends with its own write token (captures only); the read token (ECHO_TEST_READ) reads them all") {
		t.Errorf("banner:\n%s", out)
	}
	base := "http://" + api
	if code, body := apiCall(t, http.MethodPost, base+"/v1/capture", "write-token-aaaa", emailCapture("From A")); code != http.StatusOK {
		t.Fatalf("a write token's capture: %d %s", code, body)
	}
	if code, _ := apiCall(t, http.MethodGet, base+"/v1/messages", "write-token-aaaa", ""); code != http.StatusForbidden {
		t.Errorf("a write token listing: %d, want 403", code)
	}
	if code, _ := apiCall(t, http.MethodGet, base+"/v1/messages", "", ""); code != http.StatusUnauthorized {
		t.Errorf("no token: %d, want 401", code)
	}
	code, body := apiCall(t, http.MethodGet, base+"/v1/messages", "read-token-21300", "")
	var ms []model.Message
	_ = json.Unmarshal([]byte(body), &ms)
	if code != http.StatusOK || len(ms) != 1 || ms[0].Source.Site != "client-a" {
		t.Errorf("the read token: %d %s", code, body)
	}
	if code, _, errOut := runCLI(t, "up", "--sites", sites, "--token", "other", "--listen", wiringPorts(t, 1)[0], "--smtp", "off"); code != exitUsage ||
		!strings.Contains(errOut, "--token: with --sites the read token is ECHO_TEST_READ's") {
		t.Errorf("--token with --sites: %d %s", code, errOut)
	}
}

// TestUpMountsTheRelay: with relay: in the config, a reader with the
// token can release a caught email to an allowlisted address; nothing
// else can. The relay's server is never reached here: every release is
// refused before it would send.
func TestUpMountsTheRelay(t *testing.T) {
	upEnv(t)
	api := wiringPorts(t, 1)[0]
	t.Setenv(relayEnv, "")
	writeConfig(t, "relay:\n  url: smtp://user:pass@127.0.0.1:21349\n  allow: [\"*@client.test\"]\n")
	t.Setenv("RHYTHM_ECHO_TOKEN", "")
	if code, _, errOut := runCLI(t, "up", "--listen", api, "--smtp", "off"); code != exitUsage ||
		!strings.Contains(errOut, "relay: releasing needs Echo's token or a viewer login") {
		t.Errorf("a relay no one could use: %d %s", code, errOut)
	}
	t.Setenv("RHYTHM_ECHO_TOKEN", "relay-test-token")
	out, _, stop := startUp(t, "--listen", api, "--smtp", "off")
	defer stop()
	if !strings.Contains(out.String(), "Relay: releases go out through 127.0.0.1:21349, only to *@client.test") ||
		strings.Contains(out.String(), "pass") {
		t.Errorf("banner:\n%s", out)
	}
	base := "http://" + api
	_, body := apiCall(t, http.MethodPost, base+"/v1/capture", "relay-test-token", emailCapture("Sign-off"))
	var d wire.Decision
	_ = json.Unmarshal([]byte(body), &d)
	release := base + "/v1/messages/" + d.ID + "/release"
	if code, _ := apiCall(t, http.MethodPost, release, "", `{"to":"ann@client.test"}`); code != http.StatusUnauthorized {
		t.Errorf("without the token: %d", code)
	}
	if code, body := apiCall(t, http.MethodPost, release, "relay-test-token", `{"to":"x@evil.test"}`); code != http.StatusForbidden ||
		!strings.Contains(body, "x@evil.test isn't on the relay's allowlist; nothing was sent") {
		t.Errorf("an address off the allowlist: %d %s", code, body)
	}
}

// fakeSpamd answers every check with the score given, on a loopback
// port of its own.
func fakeSpamd(t *testing.T, score string) string {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = ln.Close() })
	go func() {
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			br := bufio.NewReader(c)
			_, _ = br.ReadString('\n')
			length := 0
			for {
				h, err := br.ReadString('\n')
				if err != nil || strings.TrimSpace(h) == "" {
					break
				}
				if v, ok := strings.CutPrefix(h, "Content-length: "); ok {
					length, _ = strconv.Atoi(strings.TrimSpace(v))
				}
			}
			_, _ = io.CopyN(io.Discard, br, int64(length))
			rules := "HTML_MESSAGE,MISSING_DATE"
			_, _ = fmt.Fprintf(c, "SPAMD/1.1 0 EX_OK\r\nContent-length: %d\r\nSpam: False ; %s / 5.0\r\n\r\n%s", len(rules), score, rules)
			_ = c.Close()
		}
	}()
	return ln.Addr().String()
}

// TestUpScoresEmailWithSpamAssassin: --spamassassin scores each email in
// the background; the score shows as a compat/spam notice on it.
func TestUpScoresEmailWithSpamAssassin(t *testing.T) {
	upEnv(t)
	spamd := fakeSpamd(t, "2.1")
	api := wiringPorts(t, 1)[0]
	out, _, stop := startUp(t, "--listen", api, "--smtp", "off", "--spamassassin", spamd)
	defer stop()
	if !strings.Contains(out.String(), "Spam: each email is scored by SpamAssassin at "+spamd) {
		t.Errorf("banner:\n%s", out)
	}
	_, body := apiCall(t, http.MethodPost, "http://"+api+"/v1/capture", "", emailCapture("Scored"))
	var d wire.Decision
	_ = json.Unmarshal([]byte(body), &d)
	echo := client.New("http://"+api, "")
	waitFor(t, func() bool {
		m, err := echo.Message(context.Background(), d.ID)
		return err == nil && slices.ContainsFunc(m.Issues, func(i model.Issue) bool {
			return i.Check == "compat/spam" && i.Title == "SpamAssassin scores the email 2.1 of 5.0" && i.Detail == "rules: HTML_MESSAGE, MISSING_DATE"
		})
	}, "the spam score on the email")
	if m, _ := echo.Message(context.Background(), d.ID); m.Headers[check.SpamHeader] == "" {
		t.Errorf("no %s header: %v", check.SpamHeader, m.Headers)
	}
	if code, _, errOut := runCLI(t, "up", "--spamassassin", "127.0.0.1:spamd", "--listen", wiringPorts(t, 1)[0], "--smtp", "off"); code != exitUsage ||
		!strings.Contains(errOut, "--spamassassin 127.0.0.1:spamd: want host:port") {
		t.Errorf("a bad address: %d %s", code, errOut)
	}
}

// TestUpChecksFollowTheConfig: captures in the live view and the API
// carry the config's rules: and checks:.
func TestUpChecksFollowTheConfig(t *testing.T) {
	upEnv(t)
	writeConfig(t, "rules:\n  - {id: brand/secret, match: SECRET-PROJECT, level: error}\nchecks:\n  content/placeholder: off\n")
	api := wiringPorts(t, 1)[0]
	_, _, stop := startUp(t, "--listen", api, "--smtp", "off")
	defer stop()
	_, body := apiCall(t, http.MethodPost, "http://"+api+"/v1/capture", "", emailCapture("SECRET-PROJECT for {first_name}"))
	var d wire.Decision
	_ = json.Unmarshal([]byte(body), &d)
	m, err := client.New("http://"+api, "").Message(context.Background(), d.ID)
	if err != nil {
		t.Fatal(err)
	}
	var got []string
	for _, i := range m.Issues {
		got = append(got, i.Check)
	}
	if !slices.Contains(got, "brand/secret") || slices.Contains(got, "content/placeholder") {
		t.Errorf("issues: %v", got)
	}
}

// TestUpHelpListsItsNewFlags: up --help names every flag it takes.
func TestUpHelpListsItsNewFlags(t *testing.T) {
	_, out, _ := runCLI(t, "up", "--help")
	for _, f := range []string{"--private-network", "--free-ports", "--login <user:pass>", "--sites <file>", "--store <dir>",
		"--keep <n>", "--keep-for <d>", "--spamassassin <addr>", "--hostname <name>"} {
		if !strings.Contains(out, f) {
			t.Errorf("up --help lacks %s:\n%s", f, out)
		}
	}
	var b bytes.Buffer
	usage(&b)
	if !strings.Contains(b.String(), "--private-network") {
		t.Error("the main help lacks --private-network")
	}
}

// TestAPIRefusesARebindingHostWithALogin: without a token the API still
// answers only to loopback names when a login is set, as the catcher's
// guard sits in front of it.
func TestAPIRefusesARebindingHostWithALogin(t *testing.T) {
	h := testLoginUp(t, "")
	for _, path := range []string{"/v1/messages", "/api/v1/messages"} {
		r := httptest.NewRequest(http.MethodGet, path, nil)
		r.Host = "evil.example"
		if w := serve(h, r); w.Code != http.StatusForbidden {
			t.Errorf("%s with Host evil.example: %d, want 403", path, w.Code)
		}
	}
}

func TestOpenWarnings(t *testing.T) {
	cfg := config.Default()
	cfg.Proxy = "10.0.0.5:8026"
	if w := openWarnings(nil, cfg); w != nil {
		t.Errorf("nothing open: %q", w)
	}
	open := []string{"10.0.0.5:8025", "10.0.0.5:8026"}
	if w := openWarnings(open, cfg); len(w) != 1 || !strings.HasPrefix(w[0], "no token on 10.0.0.5:8025, 10.0.0.5:8026: anything") {
		t.Errorf("sealed: %q", w)
	}
	cfg.Mode = "observe"
	if w := openWarnings(open, cfg); len(w) != 2 || w[1] != "observing, the proxy on 10.0.0.5:8026 forwards for anything on this private network to any host it reaches; seal Echo or set a token" {
		t.Errorf("observing: %q", w)
	}
}

// hostCall makes one request to a running up with the Host header set to
// host, as a container that names Echo "echo" sends it.
func hostCall(t *testing.T, method, addr, path, host, body string) (int, string) {
	t.Helper()
	req, err := http.NewRequestWithContext(context.Background(), method, "http://"+addr+path, strings.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	req.Host = host
	if body != "" {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = resp.Body.Close() }()
	b, _ := io.ReadAll(resp.Body)
	return resp.StatusCode, string(b)
}

// TestUpPrivateNetworkAnswersToServiceNames: the image's default CMD
// (wildcard listen, --private-network, no token) answers a connector in
// another container that names Echo by its Compose service ("echo:8025")
// or by an IP address, for the API and Echo Inbox alike. A domain name
// stays refused unless --hostname (or RHYTHM_ECHO_HOSTNAMES) lists it.
// A page's Origin is still refused.
func TestUpPrivateNetworkAnswersToServiceNames(t *testing.T) {
	upEnv(t)
	t.Setenv("RHYTHM_ECHO_TOKEN", "")
	t.Setenv(privateNetworkEnv, "")
	t.Setenv(hostnamesEnv, "*.ddev.site")
	orig := listIfaces
	listIfaces = func() ([]netIface, error) { return containerHost, nil }
	t.Cleanup(func() { listIfaces = orig })
	port := strings.TrimPrefix(wiringPorts(t, 1)[0], "127.0.0.1:")
	_, _, stop := startUp(t, "--listen", "0.0.0.0:"+port, "--smtp", "off", "--private-network")
	defer stop()
	addr := "127.0.0.1:" + port
	if code, body := hostCall(t, "POST", addr, "/v1/capture", "echo:8025", emailCapture("From the web container")); code != http.StatusOK {
		t.Fatalf("capture with Host echo:8025: %d %s", code, body)
	}
	for _, c := range []struct{ path, host string }{
		{"/api/v1/messages", "echo:8025"},
		{"/v1/health", "172.17.0.2:" + port},
		{"/", "echo:8025"},
		{"/v1/health", "shop.ddev.site:8026"}, // DDEV's router, from RHYTHM_ECHO_HOSTNAMES
	} {
		if code, body := hostCall(t, "GET", addr, c.path, c.host, ""); code != http.StatusOK {
			t.Errorf("GET %s with Host %s: %d %s", c.path, c.host, code, body)
		}
	}
	if _, body := hostCall(t, "GET", addr, "/api/v1/messages", "echo", ""); !strings.Contains(body, "From the web container") {
		t.Errorf("the capture isn't listed: %s", body)
	}
	for _, host := range []string{"rebind.attacker.example", "0.0.0.0:" + port} {
		if code, _ := hostCall(t, "GET", addr, "/v1/messages", host, ""); code != http.StatusForbidden {
			t.Errorf("Host %s: %d, want 403", host, code)
		}
		if code, _ := hostCall(t, "GET", addr, "/", host, ""); code != http.StatusForbidden {
			t.Errorf("Inbox with Host %s: %d, want 403", host, code)
		}
	}
	req, _ := http.NewRequestWithContext(context.Background(), "POST", "http://"+addr+"/v1/capture", strings.NewReader(emailCapture("x")))
	req.Host = "echo:8025"
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Origin", "http://attacker.example")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	_ = resp.Body.Close()
	if resp.StatusCode != http.StatusForbidden {
		t.Errorf("a page's Origin: %d, want 403", resp.StatusCode)
	}
}

// TestUpHostnameFlag: --hostname takes names and *. patterns and refuses
// anything else; it wins over RHYTHM_ECHO_HOSTNAMES.
func TestUpHostnameFlag(t *testing.T) {
	t.Setenv(hostnamesEnv, "a.test, *.ddev.site")
	if got, err := upHostnames(nil); err != nil || !slices.Equal(got, []string{"a.test", "*.ddev.site"}) {
		t.Errorf("from the variable: %v %v", got, err)
	}
	if got, err := upHostnames([]string{"Echo.MyApp.lndo.site"}); err != nil || !slices.Equal(got, []string{"echo.myapp.lndo.site"}) {
		t.Errorf("the flag: %v %v", got, err)
	}
	for _, bad := range []string{"echo:8025", "*", "a..b", "http://echo", "*.*.x"} {
		_, err := upHostnames([]string{bad})
		if err == nil || !strings.Contains(err.Error(), `--hostname "`+bad+`": want a host name like echo.myapp.lndo.site or *.ddev.site, without a port`) {
			t.Errorf("%s: %v", bad, err)
		}
	}
	t.Setenv(hostnamesEnv, "echo:1")
	if _, err := upHostnames(nil); err == nil || !strings.Contains(err.Error(), `RHYTHM_ECHO_HOSTNAMES "echo:1"`) {
		t.Errorf("variable error: %v", err)
	}
	upEnv(t)
	if code, _, errOut := runCLI(t, "up", "--hostname", "echo:8025", "--listen", wiringPorts(t, 1)[0], "--smtp", "off"); code != exitUsage {
		t.Errorf("exit %d: %s", code, errOut)
	}
}

// TestSpamScorerWhileReading: the scorer's verdicts land while the API
// serves the same messages (go test -race found the scorer writing the
// map a reader held).
func TestSpamScorerWhileReading(t *testing.T) {
	inbox := catcher.NewInbox(100)
	srv := httptest.NewServer(catcher.NewServer(inbox, catcher.Config{}))
	defer srv.Close()
	sc := newSpamScorer(fakeSpamd(t, "2.1"), inbox)
	var ids []string
	for i := range 10 {
		m := inbox.Add(model.Message{Channel: model.Email, Subject: fmt.Sprint("Mail ", i), Headers: map[string]string{"X-Mailer": "test"}})
		ids = append(ids, m.ID)
		sc.score(m)
	}
	done := make(chan struct{})
	go func() { sc.wait(); close(done) }()
	for read := true; read; {
		select {
		case <-done:
			read = false
		default:
		}
		for _, id := range ids {
			if resp, err := http.Get(srv.URL + "/v1/messages/" + id); err == nil { //nolint:noctx // test
				_, _ = io.Copy(io.Discard, resp.Body)
				_ = resp.Body.Close()
			}
		}
	}
	for _, id := range ids {
		if m, _ := inbox.Get(id); m.Headers[check.SpamHeader] == "" {
			t.Errorf("%s has no verdict: %v", id, m.Headers)
		}
	}
}

// TestUpRefusesAStoreInUse: a second up on the same --store folder exits
// 3 naming the first, which keeps running.
func TestUpRefusesAStoreInUse(t *testing.T) {
	upEnv(t)
	dir := filepath.Join(t.TempDir(), "inbox")
	ports := wiringPorts(t, 2)
	_, _, stop := startUp(t, "--listen", ports[0], "--smtp", "off", "--store", dir)
	defer stop()
	code, _, errOut := runCLI(t, "up", "--listen", ports[1], "--smtp", "off", "--store", dir)
	want := fmt.Sprintf("rhythm echo: keeping the inbox in %s (--store): open inbox: another Echo (PID %d) keeps its inbox in %s; stop it first, or give this one a folder of its own",
		dir, os.Getpid(), dir)
	if code != exitError || !strings.Contains(errOut, want) {
		t.Errorf("exit %d: %s\nwant %s", code, errOut, want)
	}
	if code, _ := apiCall(t, http.MethodGet, "http://"+ports[0]+"/v1/health", "", ""); code != http.StatusOK {
		t.Errorf("the first up: %d", code)
	}
}

// TestUpStopCountsThisSession: with --store, the stop line counts what
// this session caught, not what earlier sessions left in the inbox.
func TestUpStopCountsThisSession(t *testing.T) {
	upEnv(t)
	dir := filepath.Join(t.TempDir(), "inbox")
	api := wiringPorts(t, 1)[0]
	_, errOut, stop := startUp(t, "--listen", api, "--smtp", "off", "--store", dir)
	for _, s := range []string{"One", "Two"} {
		if code, body := apiCall(t, http.MethodPost, "http://"+api+"/v1/capture", "", emailCapture(s)); code != http.StatusOK {
			t.Fatalf("capture: %d %s", code, body)
		}
	}
	stop()
	if !strings.Contains(errOut.String(), "Stopped. Captured 2 emails this session.") {
		t.Errorf("first session: %s", errOut)
	}
	_, errOut, stop = startUp(t, "--listen", api, "--smtp", "off", "--store", dir)
	stop()
	if !strings.Contains(errOut.String(), "Stopped. Captured no messages this session.") {
		t.Errorf("second session: %s", errOut)
	}
}
