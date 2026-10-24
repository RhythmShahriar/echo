// Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
// SPDX-License-Identifier: MIT

//go:build docker

// The connector inside a real WordPress: wordpress:php8.2-apache and
// MariaDB in Docker, the mu-plugin installed with Install, and Echo's
// catcher running in this test process on the host. Run with:
//
//	GOWORK=off go test -tags docker -run Docker -v ./connectors/wordpress/
//
// Nothing leaves the machine: the fixture plugin only calls fake hosts,
// and the containers resolve the real provider hosts to 127.0.0.1, so a
// connector bug fails a test instead of reaching Twilio or Slack.
package wordpress

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"math/rand/v2"
	"net"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/RhythmShahriar/echo/catcher"
	"github.com/RhythmShahriar/echo/model"
)

const token = "docker-test-token"

// rig is the running stack.
type rig struct {
	t       *testing.T
	project string
	compose string
	wpURL   string
	echoURL string // as the containers reach it
	content string // the mounted wp-content

	echo  atomic.Pointer[catcher.Server] // nil: Echo is down (503)
	smtp  *catcher.Server                // observed mail lands here
	hooks atomic.Int32                   // requests the webhook fixture got
	proxy struct {
		sync.Mutex
		lines []string
	}
}

func (r *rig) seal() *catcher.Server    { return r.use(catcher.Seal) }
func (r *rig) observe() *catcher.Server { return r.use(catcher.Observe) }

func (r *rig) use(mode string) *catcher.Server {
	s := catcher.NewServer(catcher.NewInbox(500), catcher.Config{Mode: mode, Token: token})
	r.echo.Store(s)
	r.hooks.Store(0)
	return s
}

func TestDockerWordPress(t *testing.T) {
	if _, err := exec.LookPath("docker"); err != nil {
		t.Skip("docker not installed")
	}
	r := start(t)

	t.Run("sealed sends are captured and faked", func(t *testing.T) {
		s := r.seal()
		out := r.get(t, "all", map[string]string{"X-Echo-Run": "run1/contact-form"})
		if out["carry_on"] != true || out["mail"] != true {
			t.Fatalf("site didn't carry on: %v", out)
		}
		tw := out["twilio"].(map[string]any)
		if tw["code"] != 201.0 || !strings.Contains(tw["body"].(string), `"status":"queued"`) || tw["object"] != "WP_HTTP_Requests_Response" {
			t.Errorf("twilio fake = %v", tw)
		}
		if sl := out["slack"].(map[string]any); sl["code"] != 200.0 || sl["body"] != "ok" {
			t.Errorf("slack fake = %v", sl)
		}
		if wh := out["webhook"].(map[string]any); wh["code"] != 200.0 {
			t.Errorf("webhook fake = %v", wh)
		}
		if r.hooks.Load() != 0 {
			t.Error("the webhook left the sealed site")
		}
		msgs := s.Inbox.List(catcher.Filter{})
		if len(msgs) != 4 {
			t.Fatalf("captured %d messages, want 4: %+v", len(msgs), msgs)
		}
		byProvider := map[string]model.Message{}
		for _, m := range msgs {
			if m.Run != "run1" || m.Action != "contact-form" {
				t.Errorf("%s: run %q/%q", m.Channel, m.Run, m.Action)
			}
			if m.Source.Plugin != "echo-fixture" || m.Source.Via != "wordpress" {
				t.Errorf("%s: source %+v", m.Channel, m.Source)
			}
			byProvider[string(m.Channel)+"/"+m.Provider] = m
		}
		mail := byProvider["email/"]
		if mail.From != "forms@shop.test" || strings.Join(mail.To, ",") != "owner@example.com" ||
			strings.Join(mail.CC, ",") != "sales@example.com" || strings.Join(mail.BCC, ",") != "audit@example.com" ||
			mail.ReplyTo != "jane@example.org" || mail.Subject != "New submission: Hello" ||
			!strings.Contains(mail.Text, "I'd like a quote") || mail.Headers["x-echo-attachments"] != "cv.pdf" ||
			mail.Source.Hook != "wp_mail" || mail.Status != model.Faked {
			t.Errorf("mail = %+v", mail)
		}
		if strings.Contains(mail.Headers["x-echo-attachments"], "PDF-1.4") {
			t.Error("attachment content was sent")
		}
		sms := byProvider["sms/twilio"]
		if sms.Text != "Your order #42 has shipped" || sms.To[0] != "+15551230000" || sms.Status != model.Faked {
			t.Errorf("sms = %+v", sms)
		}
		if strings.Contains(sms.Headers["authorization"], strings.Repeat("f", 32)) {
			t.Error("the Twilio auth token reached Echo")
		}
		if byProvider["chat/slack"].Text != "New order #42 from Jane" {
			t.Errorf("slack = %+v", byProvider["chat/slack"])
		}
		if hook := byProvider["webhook/woocommerce"]; hook.Event != "order.created" {
			t.Errorf("webhook = %+v", hook)
		}
		if got := r.smtp.Inbox.List(catcher.Filter{}); len(got) != 0 {
			t.Errorf("sealed mail was sent over SMTP: %v", got)
		}
	})

	t.Run("html mail after wp_mail filters", func(t *testing.T) {
		s := r.seal()
		r.get(t, "html", nil)
		msgs := s.Inbox.List(catcher.Filter{})
		if len(msgs) != 1 || msgs[0].HTML != "<p>Thanks, <b>Jane</b></p>" || msgs[0].From != "shop@shop.test" || len(msgs[0].To) != 2 {
			t.Fatalf("html mail = %+v", msgs)
		}
	})

	t.Run("observe sends for real and reports after", func(t *testing.T) {
		s := r.observe()
		out := r.get(t, "webhook", map[string]string{"X-Echo-Run": "run2/order"})
		if out["code"] != 200.0 || out["body"] != `{"received":true}` {
			t.Fatalf("observed webhook = %v", out)
		}
		if r.hooks.Load() != 1 {
			t.Errorf("webhook fixture got %d requests, want 1", r.hooks.Load())
		}
		msgs := s.Inbox.List(catcher.Filter{})
		if len(msgs) != 1 || msgs[0].Status != model.Sent || msgs[0].Response == nil || msgs[0].Response.Status != 200 ||
			msgs[0].Response.Body != `{"received":true}` {
			t.Fatalf("observed message = %+v", msgs)
		}

		mail := r.get(t, "cf7", nil)
		if mail["sent"] != true {
			t.Fatalf("observed mail: %v", mail)
		}
		got := r.smtp.Inbox.List(catcher.Filter{Channel: model.Email})
		if len(got) != 1 || got[0].Subject != "New submission: Hello" || !contains(got[0].To, "owner@example.com") {
			t.Errorf("SMTP sink got %+v", got)
		}
		if m := s.Inbox.List(catcher.Filter{Channel: model.Email}); len(m) != 1 || m[0].Status != model.Sent {
			t.Errorf("observed mail capture = %+v", m)
		}
		r.smtp.Inbox.Clear(catcher.Filter{})
	})

	t.Run("observe reports a failed send on the same message", func(t *testing.T) {
		s := r.observe()
		out := r.get(t, "cf7_fail", nil)
		if out["sent"] != false {
			t.Fatalf("send to a closed SMTP port: %v", out)
		}
		msgs := s.Inbox.List(catcher.Filter{})
		if len(msgs) != 1 || msgs[0].Status != model.Failed || msgs[0].Error == "" {
			t.Fatalf("failed observed mail = %+v", msgs)
		}
	})

	t.Run("run from header beats cookie", func(t *testing.T) {
		s := r.seal()
		r.get(t, "twilio", map[string]string{"X-Echo-Run": "hdr/a", "Cookie": "X-Echo-Run=cookie/b"})
		r.get(t, "twilio", map[string]string{"Cookie": "X-Echo-Run=cookie/b"})
		msgs := s.Inbox.List(catcher.Filter{})
		if len(msgs) != 2 || msgs[0].Run != "hdr" || msgs[1].Run != "cookie" || msgs[1].Action != "b" {
			t.Fatalf("runs = %+v", runs(msgs))
		}
	})

	t.Run("cron carries the run it was scheduled with", func(t *testing.T) {
		s := r.seal()
		r.get(t, "schedule", map[string]string{"X-Echo-Run": "cronrun/schedule"})
		r.wp(t, nil, "cron", "event", "run", "echo_fixture_cron")
		// The stored run beats the environment: a flush for another action
		// must not claim a job this one queued.
		r.get(t, "schedule", map[string]string{"X-Echo-Run": "cronrun/schedule"})
		r.wp(t, []string{"RHYTHM_ECHO_RUN=envrun/flush"}, "cron", "event", "run", "echo_fixture_cron")
		msgs := s.Inbox.List(catcher.Filter{Channel: model.Email})
		if len(msgs) != 2 || msgs[0].Run != "cronrun" || msgs[0].Action != "schedule" || msgs[1].Run != "cronrun" || msgs[1].Action != "schedule" {
			t.Fatalf("cron mail runs = %v", runs(msgs))
		}
	})

	t.Run("action scheduler jobs carry their run", func(t *testing.T) {
		s := r.seal()
		r.get(t, "as_enqueue", map[string]string{"X-Echo-Run": "asrun/checkout"})
		r.wp(t, nil, "eval", "echo json_encode( echo_fixture_as_run() );")
		msgs := s.Inbox.List(catcher.Filter{})
		if len(msgs) != 1 || msgs[0].Run != "asrun" || msgs[0].Action != "checkout" || msgs[0].Event != "order.updated" {
			t.Fatalf("queued webhook = %v", runs(msgs))
		}
	})

	t.Run("sends with no run fall in the open action window", func(t *testing.T) {
		s := r.seal()
		s.Inbox.Begin("win", "manual")
		r.wp(t, nil, "eval", "wp_mail( 'x@example.com', 'From the CLI', 'hi' );")
		s.Inbox.End()
		msgs := s.Inbox.List(catcher.Filter{})
		if len(msgs) != 1 || msgs[0].Run != "win" || msgs[0].Action != "manual" {
			t.Fatalf("windowed = %v", runs(msgs))
		}
	})

	t.Run("fails closed when echo is down", func(t *testing.T) {
		r.seal()
		r.echo.Store(nil)
		out := r.get(t, "all", nil)
		if out["mail"] != false || !strings.Contains(fmt.Sprint(out["twilio"]), "could not be reached") ||
			!strings.Contains(fmt.Sprint(out["webhook"]), "could not be reached") || out["carry_on"] != true {
			t.Errorf("with Echo down = %v", out)
		}
		if r.hooks.Load() != 0 || len(r.smtp.Inbox.List(catcher.Filter{})) != 0 {
			t.Error("something was sent while Echo was down")
		}
	})

	t.Run("production guard", func(t *testing.T) {
		s := r.seal()
		prod := map[string]string{"X-Test-Env": "production"}
		out := r.get(t, "webhook", prod)
		if out["code"] != 200.0 || r.hooks.Load() != 1 {
			t.Errorf("on production the connector should stay out of the way: %v", out)
		}
		if n := len(s.Inbox.List(catcher.Filter{})); n != 0 {
			t.Errorf("captured %d messages on production", n)
		}
		ping := r.rest(t, "ping", prod)
		if ping["active"] != false || ping["reason"] != "production" {
			t.Errorf("ping on production = %v", ping)
		}
		forced := map[string]string{"X-Test-Env": "production", "X-Test-Force": "1"}
		r.get(t, "twilio", forced)
		if n := len(s.Inbox.List(catcher.Filter{})); n != 1 {
			t.Errorf("RHYTHM_ECHO_FORCE: captured %d, want 1", n)
		}
	})

	t.Run("doctor ping and canaries", func(t *testing.T) {
		s := r.seal()
		ping := r.rest(t, "ping", nil)
		if ping["active"] != true || fmt.Sprint(ping["layers"]) != "[wp_mail pre_http_request]" ||
			ping["echo"].(map[string]any)["reachable"] != true || ping["echo"].(map[string]any)["mode"] != "seal" {
			t.Errorf("ping = %v", ping)
		}
		c := r.rest(t, "canary", map[string]string{"X-Echo-Run": "doc/canary"})
		if c["email"].(map[string]any)["sent"] != true || c["http"].(map[string]any)["status"] != 200.0 {
			t.Errorf("canary = %v", c)
		}
		msgs := s.Inbox.List(catcher.Filter{Run: "doc"})
		if len(msgs) != 2 || !contains(msgs[0].To, CanaryEmail) || !strings.HasPrefix(msgs[1].URL, CanaryURL) {
			t.Errorf("canaries caught = %+v", msgs)
		}
		// The routes need the token.
		status, _ := r.do(t, http.MethodGet, r.wpURL+"/?rest_route=/"+PingRoute, map[string]string{"X-Echo-Token": "wrong"})
		if status != http.StatusUnauthorized && status != http.StatusForbidden {
			t.Errorf("ping with a wrong token: HTTP %d", status)
		}
	})

	t.Run("falls back when wp_mail is already defined", func(t *testing.T) {
		s := r.seal()
		early := map[string]string{"X-Test-Early-Mailer": "1"}
		out := r.get(t, "cf7", early)
		if out["sent"] != true {
			t.Errorf("fallback sealed mail = %v", out)
		}
		msgs := s.Inbox.List(catcher.Filter{})
		if len(msgs) != 1 || msgs[0].Source.Hook != "pre_wp_mail" || msgs[0].Subject != "New submission: Hello" {
			t.Errorf("fallback capture = %+v", msgs)
		}
		ping := r.rest(t, "ping", early)
		if fmt.Sprint(ping["layers"]) != "[pre_wp_mail pre_http_request]" || !strings.Contains(fmt.Sprint(ping["warnings"]), "early-mailer.php") {
			t.Errorf("fallback ping = %v", ping)
		}
		if len(r.smtp.Inbox.List(catcher.Filter{})) != 0 {
			t.Error("fallback let sealed mail out")
		}
	})

	// Spike: can the mu-plugin route SDKs that use raw cURL (Twilio, Stripe)
	// through Echo's proxy with putenv('https_proxy=…')?
	t.Run("spike putenv proxy reaches raw curl", func(t *testing.T) {
		r.seal()
		proxy := "http://host.docker.internal:" + os.Getenv("ECHO_TEST_PROXY_PORT")
		for _, target := range []string{"https://api.stripe.com/v1/charges", "http://api.twilio.com/2010-04-01/Accounts"} {
			r.proxy.Lock()
			r.proxy.lines = nil
			r.proxy.Unlock()
			out := r.get(t, "curl&proxy="+proxy+"&url="+target, nil)
			r.proxy.Lock()
			seen := strings.Join(r.proxy.lines, " | ")
			r.proxy.Unlock()
			t.Logf("mod_php putenv proxy, %s: proxy saw %q; curl %v", target, seen, out)
			if seen == "" {
				t.Errorf("%s: raw cURL ignored putenv proxy", target)
			}
			// Without putenv in the next request, PHP has restored the env.
			out = r.get(t, "curl&url="+target, nil)
			if out["env"] != false {
				t.Errorf("putenv leaked into the next request: %v", out["env"])
			}
		}
		r.proxy.Lock()
		r.proxy.lines = nil
		r.proxy.Unlock()
		out := r.wp(t, nil, "eval", fmt.Sprintf("echo json_encode( echo_fixture_curl( 'https://api.stripe.com/v1/charges', %q ) );", proxy))
		r.proxy.Lock()
		t.Logf("php-cli (wp eval) putenv proxy: proxy saw %q; curl %s", strings.Join(r.proxy.lines, " | "), out)
		if len(r.proxy.lines) == 0 {
			t.Error("cli: raw cURL ignored putenv proxy")
		}
		r.proxy.Unlock()
	})

	// With Proxy set, raw cURL (SDKs that skip the HTTP API) goes through
	// Echo's proxy; without it, it goes direct and hits the 127.0.0.1 trap.
	t.Run("proxy setting routes raw curl", func(t *testing.T) {
		r.seal()
		proxy := "http://host.docker.internal:" + os.Getenv("ECHO_TEST_PROXY_PORT")
		ca := filepath.Join(r.content, "mu-plugins", "rhythm-echo", "test-ca.pem")
		r.install(t, Settings{Proxy: proxy, CA: "/var/www/html/wp-content/mu-plugins/rhythm-echo/test-ca.pem"})
		if err := os.WriteFile(ca, []byte("-----BEGIN CERTIFICATE-----\nECHO-TEST-CA\n-----END CERTIFICATE-----\n"), 0o644); err != nil {
			t.Fatal(err)
		}
		defer r.install(t, Settings{})

		seen := func(scenario string) string {
			r.proxy.Lock()
			r.proxy.lines = nil
			r.proxy.Unlock()
			out := r.get(t, scenario, nil)
			r.proxy.Lock()
			defer r.proxy.Unlock()
			t.Logf("%s: proxy saw %q; curl %v", scenario, r.proxy.lines, out)
			return strings.Join(r.proxy.lines, " | ")
		}
		if got := seen("curl&url=https://api.stripe.com/v1/charges"); got != "CONNECT api.stripe.com:443 HTTP/1.1" {
			t.Errorf("with Proxy set, raw cURL to Stripe: proxy saw %q", got)
		}
		// The site itself stays direct.
		if got := seen("curl&url=" + url.QueryEscape("http://localhost/")); got != "" {
			t.Errorf("own host went through the proxy: %q", got)
		}
		// Echo's own traffic stays direct: captures still arrive.
		s := r.seal()
		r.get(t, "twilio", nil)
		if n := len(s.Inbox.List(catcher.Filter{})); n != 1 {
			t.Errorf("with Proxy set, Echo got %d captures, want 1", n)
		}
		ping := r.rest(t, "ping", nil)
		if ping["proxy"] != proxy || ping["ca"] != true || !strings.Contains(fmt.Sprint(ping["layers"]), "proxy") {
			t.Errorf("ping with proxy = %v", ping)
		}
		c := r.get(t, "ca", nil)
		if c["core"] != true || c["echo"] != true || !strings.Contains(fmt.Sprint(c["bundle"]), "rhythm-echo-ca-") {
			t.Errorf("CA bundle = %v", c)
		}

		r.install(t, Settings{})
		if got := seen("curl&url=https://api.stripe.com/v1/charges"); got != "" {
			t.Errorf("without Proxy, raw cURL still used the proxy: %q", got)
		}
	})
}

// install rewrites the connector's settings in the mounted mu-plugins.
func (r *rig) install(t *testing.T, s Settings) {
	t.Helper()
	s.URL, s.Token = r.echoURL, token
	if err := Install(r.content, s); err != nil {
		t.Fatal(err)
	}
	// The image's opcache rechecks files every 2 seconds.
	time.Sleep(2500 * time.Millisecond)
}

func runs(ms []model.Message) []string {
	out := []string{}
	for _, m := range ms {
		out = append(out, string(m.Channel)+" "+m.Run+"/"+m.Action)
	}
	return out
}

func contains(list []string, v string) bool {
	for _, s := range list {
		if s == v {
			return true
		}
	}
	return false
}

// get runs a fixture scenario and decodes its JSON.
func (r *rig) get(t *testing.T, scenario string, headers map[string]string) map[string]any {
	t.Helper()
	status, b := r.do(t, http.MethodGet, r.wpURL+"/?echo_fixture="+scenario, headers)
	return decode(t, status, b)
}

func (r *rig) rest(t *testing.T, route string, headers map[string]string) map[string]any {
	t.Helper()
	h := map[string]string{"X-Echo-Token": token}
	for k, v := range headers {
		h[k] = v
	}
	// ?rest_route= works whatever the permalink settings (/wp-json/ needs
	// pretty permalinks).
	method := http.MethodGet
	if route == "canary" {
		method = http.MethodPost
	}
	status, b := r.do(t, method, r.wpURL+"/?rest_route=/rhythm-echo/v1/"+route, h)
	return decode(t, status, b)
}

func (r *rig) do(t *testing.T, method, url string, headers map[string]string) (int, []byte) {
	t.Helper()
	req, _ := http.NewRequest(method, url, nil)
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	b, _ := io.ReadAll(resp.Body)
	return resp.StatusCode, b
}

func decode(t *testing.T, status int, b []byte) map[string]any {
	t.Helper()
	var out map[string]any
	if err := json.Unmarshal(b, &out); err != nil {
		t.Fatalf("HTTP %d: not JSON: %.600s", status, b)
	}
	return out
}

// wp runs WP-CLI in the cli container.
func (r *rig) wp(t *testing.T, env []string, args ...string) string {
	t.Helper()
	cmd := []string{"exec", "-T"}
	for _, e := range env {
		cmd = append(cmd, "-e", e)
	}
	cmd = append(cmd, "cli", "wp")
	out, err := r.dc(append(cmd, args...)...)
	if err != nil {
		t.Fatalf("wp %v: %v\n%s", args, err, out)
	}
	return out
}

func (r *rig) dc(args ...string) (string, error) {
	cmd := exec.Command("docker", append([]string{"compose", "-p", r.project, "-f", r.compose}, args...)...)
	out, err := cmd.CombinedOutput()
	return string(out), err
}

// ports picks n free ports in 18900–18999.
func ports(t *testing.T, n int) []int {
	t.Helper()
	var out []int
	for _, p := range rand.Perm(100) {
		port := 18900 + p
		l, err := net.Listen("tcp", fmt.Sprintf("0.0.0.0:%d", port))
		if err != nil {
			continue
		}
		_ = l.Close()
		out = append(out, port)
		if len(out) == n {
			return out
		}
	}
	t.Fatal("no free ports in 18900-18999")
	return nil
}

func listen(t *testing.T, port int, h http.Handler) {
	t.Helper()
	l, err := net.Listen("tcp", fmt.Sprintf("0.0.0.0:%d", port))
	if err != nil {
		t.Fatal(err)
	}
	srv := &http.Server{Handler: h, ReadHeaderTimeout: 5 * time.Second}
	go func() { _ = srv.Serve(l) }()
	t.Cleanup(func() { _ = srv.Close() })
}

func start(t *testing.T) *rig {
	p := ports(t, 5)
	wpPort, echoPort, smtpPort, hookPort, proxyPort := p[0], p[1], p[2], p[3], p[4]
	r := &rig{t: t, project: fmt.Sprintf("echo-wp-test-%d", time.Now().UnixNano()%1e8), wpURL: fmt.Sprintf("http://localhost:%d", wpPort)}
	t.Setenv("ECHO_TEST_PROXY_PORT", fmt.Sprint(proxyPort))

	// Echo, switchable between seal, observe and down.
	r.seal()
	listen(t, echoPort, http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		if s := r.echo.Load(); s != nil {
			s.ServeHTTP(w, req)
			return
		}
		http.Error(w, "echo is down", http.StatusServiceUnavailable)
	}))
	// An SMTP sink for observed mail.
	r.smtp = catcher.NewServer(catcher.NewInbox(100), catcher.Config{Mode: catcher.Observe})
	sink := catcher.NewSMTP(r.smtp, fmt.Sprintf("0.0.0.0:%d", smtpPort))
	go func() { _ = sink.ListenAndServe() }()
	t.Cleanup(func() { _ = sink.Close() })
	// A webhook receiver, for observe mode and the production guard.
	listen(t, hookPort, http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		r.hooks.Add(1)
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"received":true}`)
	}))
	// The spike's proxy: records the request line and refuses everything.
	pl, err := net.Listen("tcp", fmt.Sprintf("0.0.0.0:%d", proxyPort))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = pl.Close() })
	go func() {
		for {
			c, err := pl.Accept()
			if err != nil {
				return
			}
			line, _ := bufio.NewReader(c).ReadString('\n')
			r.proxy.Lock()
			r.proxy.lines = append(r.proxy.lines, strings.TrimSpace(line))
			r.proxy.Unlock()
			_, _ = io.WriteString(c, "HTTP/1.1 403 Forbidden\r\nContent-Length: 0\r\nConnection: close\r\n\r\n")
			_ = c.Close()
		}
	}()

	// The WordPress tree the containers mount: Echo's mu-plugin, the early
	// mailer and the fixture plugin.
	dir := t.TempDir()
	content := filepath.Join(dir, "wp-content")
	for _, d := range []string{"mu-plugins", "plugins/echo-fixture"} {
		if err := os.MkdirAll(filepath.Join(content, d), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	r.content, r.echoURL = content, fmt.Sprintf("http://host.docker.internal:%d", echoPort)
	if err := Install(content, Settings{URL: r.echoURL, Token: token}); err != nil {
		t.Fatal(err)
	}
	copyFile(t, "testdata/early-mailer.php", filepath.Join(content, "mu-plugins/early-mailer.php"))
	copyFile(t, "testdata/echo-fixture/echo-fixture.php", filepath.Join(content, "plugins/echo-fixture/echo-fixture.php"))
	_ = os.Chmod(dir, 0o755)

	r.compose = filepath.Join(dir, "compose.yaml")
	if err := os.WriteFile(r.compose, []byte(composeFile(content, wpPort, smtpPort, hookPort)), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if out, err := r.dc("down", "-v", "--remove-orphans", "-t", "2"); err != nil {
			t.Logf("compose down: %v\n%s", err, out)
		}
	})
	if out, err := r.dc("up", "-d", "--quiet-pull"); err != nil {
		t.Fatalf("compose up: %v\n%s", err, out)
	}

	// Install WordPress once the database answers.
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()
	for {
		out, err := r.dc("exec", "-T", "cli", "wp", "core", "install", "--url="+r.wpURL, "--title=Echo test",
			"--admin_user=admin", "--admin_password=admin-test-pass", "--admin_email=admin@shop.test", "--skip-email")
		if err == nil {
			break
		}
		if ctx.Err() != nil {
			logs, _ := r.dc("logs", "--tail", "40")
			t.Fatalf("wp core install: %v\n%s\n%s", err, out, logs)
		}
		time.Sleep(2 * time.Second)
	}
	r.wp(t, nil, "plugin", "activate", "echo-fixture")
	for ctx.Err() == nil {
		if resp, err := http.Get(r.wpURL + "/?echo_fixture=none"); err == nil {
			resp.Body.Close()
			break
		}
		time.Sleep(time.Second)
	}
	return r
}

func copyFile(t *testing.T, from, to string) {
	t.Helper()
	b, err := os.ReadFile(from)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(to, b, 0o644); err != nil {
		t.Fatal(err)
	}
}

// composeFile describes the stack. The provider hosts the fixture calls
// resolve to 127.0.0.1 inside the containers: a leak fails, it never
// reaches the real service.
func composeFile(content string, wpPort, smtpPort, hookPort int) string {
	// Compose interpolates $; PHP needs it, so it is doubled.
	extra := strings.ReplaceAll(`define( 'WP_ENVIRONMENT_TYPE', isset( $_SERVER['HTTP_X_TEST_ENV'] ) ? $_SERVER['HTTP_X_TEST_ENV'] : 'staging' );
if ( isset( $_SERVER['HTTP_X_TEST_FORCE'] ) ) { define( 'RHYTHM_ECHO_FORCE', true ); }
define( 'DISABLE_WP_CRON', true );`, "$", "$$")
	return fmt.Sprintf(`x-wp-env: &wpenv
  WORDPRESS_DB_HOST: db
  WORDPRESS_DB_USER: wp
  WORDPRESS_DB_PASSWORD: wp
  WORDPRESS_DB_NAME: wp
  FIXTURE_SMTP: host.docker.internal:%[3]d
  FIXTURE_HOOK: http://host.docker.internal:%[4]d/hook
  WORDPRESS_CONFIG_EXTRA: |
%[5]s
x-hosts: &hosts
  - host.docker.internal:host-gateway
  - api.twilio.com:127.0.0.1
  - hooks.slack.com:127.0.0.1
  - api.stripe.com:127.0.0.1
  - canary.echo.invalid:127.0.0.1
services:
  db:
    image: mariadb:10.11
    environment:
      MARIADB_DATABASE: wp
      MARIADB_USER: wp
      MARIADB_PASSWORD: wp
      MARIADB_RANDOM_ROOT_PASSWORD: "1"
  wp:
    image: wordpress:php8.2-apache
    depends_on: [db]
    ports: ["127.0.0.1:%[2]d:80"]
    environment: *wpenv
    extra_hosts: *hosts
    volumes:
      - site:/var/www/html
      - %[1]s/mu-plugins:/var/www/html/wp-content/mu-plugins:ro
      - %[1]s/plugins/echo-fixture:/var/www/html/wp-content/plugins/echo-fixture:ro
  cli:
    image: wordpress:cli-2.12.0-php8.3
    depends_on: [wp]
    user: "33:33"
    command: ["sleep", "infinity"]
    environment: *wpenv
    extra_hosts: *hosts
    volumes:
      - site:/var/www/html
      - %[1]s/mu-plugins:/var/www/html/wp-content/mu-plugins:ro
      - %[1]s/plugins/echo-fixture:/var/www/html/wp-content/plugins/echo-fixture:ro
volumes:
  site: {}
`, content, wpPort, smtpPort, hookPort, indent(extra, "    "))
}

func indent(s, pad string) string {
	return pad + strings.ReplaceAll(s, "\n", "\n"+pad)
}
