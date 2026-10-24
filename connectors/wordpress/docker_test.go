// Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
// SPDX-License-Identifier: MIT

//go:build docker

// The connector inside a real WordPress: wordpress:php8.2-apache and
// MariaDB in Docker, the mu-plugin installed with Install, and Echo's
// catcher running in this test process on the host. Run with:
//
//	GOWORK=off go test -tags docker -run Docker -v ./connectors/wordpress/
//
// ECHO_TEST_PORTS=LOW-HIGH moves the host ports it uses (18900-18999).
//
// Nothing leaves the machine: the fixture plugin only calls fake hosts,
// and the containers resolve the real provider hosts to 127.0.0.1, so a
// connector bug fails a test instead of reaching Twilio or Slack.
package wordpress

import (
	"bufio"
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math/rand/v2"
	"net"
	"net/http"
	"net/http/cookiejar"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/RhythmShahriar/echo/catcher"
	"github.com/RhythmShahriar/echo/conformance"
	"github.com/RhythmShahriar/echo/model"
	"github.com/RhythmShahriar/echo/wire"
)

const token = "docker-test-token"

// signed is "<run>/<action>" as Echo sends it with the token set: the
// connector ignores an unsigned X-Echo-Run then.
func signed(v string) string {
	run, action, _ := strings.Cut(v, "/")
	return wire.SignRun(token, run, action)
}

// rig is the running stack.
type rig struct {
	t       *testing.T
	project string
	compose string
	wpURL   string
	echoURL string // as the containers reach it
	content string // the mounted wp-content

	echo atomic.Pointer[catcher.Server] // nil: Echo is down (503)
	// standin, when set, answers instead of echo: Echo's conformance
	// stand-in, to act like an older Echo.
	standin atomic.Pointer[conformance.Server]
	// invalid collects captures that broke Echo's wire schema; the test
	// fails on any.
	invalid struct {
		sync.Mutex
		got []string
	}
	smtp  *catcher.Server // observed mail lands here
	hooks atomic.Int32    // requests the webhook fixture got
	proxy struct {
		sync.Mutex
		lines []string
	}
	// The email captures as the connector sent them, before Echo parsed
	// them: email.attachments is checked here.
	mails struct {
		sync.Mutex
		got []wire.Email
	}
}

// sentMails returns the email captures with this subject Echo got so far.
func (r *rig) sentMails(subject string) []wire.Email {
	r.mails.Lock()
	defer r.mails.Unlock()
	var got []wire.Email
	for _, m := range r.mails.got {
		if m.Subject == subject {
			got = append(got, m)
		}
	}
	return got
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
		out := r.get(t, "all", map[string]string{"X-Echo-Run": signed("run1/contact-form")})
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
		sum := sha256.Sum256([]byte("%PDF-1.4 fake"))
		want := []wire.Attachment{{Name: "cv.pdf", Type: "application/pdf", Size: 13, SHA256: hex.EncodeToString(sum[:])}}
		if sent := r.sentMails("New submission: Hello"); len(sent) == 0 || fmt.Sprint(sent[0].Attachments) != fmt.Sprint(want) {
			t.Errorf("sent attachments = %+v, want %+v", sent, want)
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
		out := r.get(t, "webhook", map[string]string{"X-Echo-Run": signed("run2/order")})
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
		r.get(t, "twilio", map[string]string{"X-Echo-Run": signed("hdr/a"), "Cookie": "X-Echo-Run=" + signed("cookie/b")})
		r.get(t, "twilio", map[string]string{"Cookie": "X-Echo-Run=" + signed("cookie/b")})
		msgs := s.Inbox.List(catcher.Filter{})
		if len(msgs) != 2 || msgs[0].Run != "hdr" || msgs[1].Run != "cookie" || msgs[1].Action != "b" {
			t.Fatalf("runs = %+v", runs(msgs))
		}
	})

	t.Run("an unsigned or tampered run is ignored", func(t *testing.T) {
		s := r.seal()
		r.get(t, "twilio", map[string]string{"X-Echo-Run": "visitor/a"})
		r.get(t, "twilio", map[string]string{"X-Echo-Run": strings.Replace(signed("run9/a"), "run9", "run8", 1)})
		msgs := s.Inbox.List(catcher.Filter{})
		if len(msgs) != 2 || msgs[0].Run != "" || msgs[1].Run != "" {
			t.Fatalf("runs = %+v", runs(msgs))
		}
	})

	t.Run("cron carries the run it was scheduled with", func(t *testing.T) {
		s := r.seal()
		r.get(t, "schedule", map[string]string{"X-Echo-Run": signed("cronrun/schedule")})
		r.wp(t, nil, "cron", "event", "run", "echo_fixture_cron")
		// The stored run beats the environment: a flush for another action
		// must not claim a job this one queued.
		r.get(t, "schedule", map[string]string{"X-Echo-Run": signed("cronrun/schedule")})
		r.wp(t, []string{"RHYTHM_ECHO_RUN=envrun/flush"}, "cron", "event", "run", "echo_fixture_cron")
		msgs := s.Inbox.List(catcher.Filter{Channel: model.Email})
		if len(msgs) != 2 || msgs[0].Run != "cronrun" || msgs[0].Action != "schedule" || msgs[1].Run != "cronrun" || msgs[1].Action != "schedule" {
			t.Fatalf("cron mail runs = %v", runs(msgs))
		}
		// A mail without files has no attachments field.
		cron := r.sentMails("Cron reminder")
		if len(cron) == 0 {
			t.Error("no cron mail reached Echo")
		}
		for _, m := range cron {
			if m.Attachments != nil {
				t.Errorf("cron mail attachments = %+v, want none", m.Attachments)
			}
		}
	})

	t.Run("action scheduler jobs carry their run", func(t *testing.T) {
		s := r.seal()
		r.get(t, "as_enqueue", map[string]string{"X-Echo-Run": signed("asrun/checkout")})
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
		// On production the routes don't exist at all.
		prod["X-Echo-Token"] = token
		if status, b := r.do(t, http.MethodGet, r.wpURL+"/?rest_route=/"+PingRoute, prod); status != http.StatusNotFound {
			t.Errorf("ping on production: HTTP %d, want 404: %.300s", status, b)
		}
		delete(prod, "X-Echo-Token")
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
		c := r.rest(t, "canary", map[string]string{"X-Echo-Run": signed("doc/canary")})
		if c["email"].(map[string]any)["sent"] != true || c["http"].(map[string]any)["status"] != 200.0 {
			t.Errorf("canary = %v", c)
		}
		msgs := s.Inbox.List(catcher.Filter{Run: "doc"})
		if len(msgs) != 2 || !contains(msgs[0].To, CanaryEmail) || !strings.HasPrefix(msgs[1].URL, CanaryURL) {
			t.Errorf("canaries caught = %+v", msgs)
		}
		// The routes need the token. This test reaches WordPress through
		// Docker's port mapping, so REMOTE_ADDR is the bridge gateway
		// (172.17.0.1 or similar), a private address that used to pass.
		for name, h := range map[string]map[string]string{"no token": nil, "a wrong token": {"X-Echo-Token": "wrong"}} {
			if status, b := r.do(t, http.MethodGet, r.wpURL+"/?rest_route=/"+PingRoute, h); status != http.StatusForbidden {
				t.Errorf("ping with %s: HTTP %d, want 403: %.300s", name, status, b)
			}
		}
	})

	t.Run("without a token only direct loopback calls get in", func(t *testing.T) {
		r.seal()
		r.write(t, Settings{URL: r.echoURL})
		defer r.install(t, Settings{})
		if status, b := r.do(t, http.MethodGet, r.wpURL+"/?rest_route=/"+PingRoute, nil); status != http.StatusForbidden {
			t.Errorf("from the Docker gateway: HTTP %d, want 403: %.300s", status, b)
		}
		if status, b := r.inside(t, PingRoute, ""); status != http.StatusOK || !strings.Contains(b, `"connector":"wordpress"`) {
			t.Errorf("from loopback: HTTP %d, want 200: %.300s", status, b)
		}
		for _, h := range []string{"X-Forwarded-For: 198.51.100.7", "Forwarded: for=198.51.100.7"} {
			if status, b := r.inside(t, PingRoute, h); status != http.StatusForbidden {
				t.Errorf("from loopback with %s: HTTP %d, want 403: %.300s", h, status, b)
			}
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

	// security #17: a fake body must never become a file, such as a plugin
	// update zip someone on the path to Echo answered with.
	t.Run("a streamed download is refused, not faked", func(t *testing.T) {
		s := r.seal()
		out := r.get(t, "download", nil)
		for _, k := range []string{"error", "download_url_error"} {
			if !strings.Contains(fmt.Sprint(out[k]), "sealed by Echo, so downloads.example.test was not downloaded") {
				t.Errorf("%s = %v", k, out[k])
			}
		}
		if out["file"] != false || out["download_url_file"] != false {
			t.Errorf("a file was written: %v", out)
		}
		if n := len(s.Inbox.List(catcher.Filter{})); n != 2 {
			t.Errorf("captured %d downloads, want 2 (each is still recorded)", n)
		}
	})

	t.Run("a hub site names itself", func(t *testing.T) {
		st, err := conformance.New(conformance.Options{Token: token, Expect: []string{"site", "http-before"}})
		if err != nil {
			t.Fatal(err)
		}
		r.install(t, Settings{SiteID: "client-a"})
		r.standin.Store(st)
		defer func() {
			r.standin.Store(nil)
			r.install(t, Settings{})
		}()
		r.get(t, "twilio", nil)
		if rep := st.Report(); !rep.OK {
			var b strings.Builder
			rep.Table(&b)
			t.Errorf("site ID:\n%s", b.String())
		}
		msgs := st.Catcher().Inbox.List(catcher.Filter{})
		if len(msgs) != 1 {
			t.Fatalf("captured %d", len(msgs))
		}
	})

	t.Run("an Echo that speaks wire 1 gets wire 1", func(t *testing.T) {
		st, err := conformance.New(conformance.Options{Token: token, Wire: 1, Expect: []string{"http-before", "wire-1"}})
		if err != nil {
			t.Fatal(err)
		}
		r.standin.Store(st)
		defer func() {
			r.standin.Store(nil)
			r.wp(t, nil, "eval", "delete_transient( 'rhythm_echo_wire_' . md5( Rhythm_Echo::config()['url'] ) );")
		}()
		r.get(t, "twilio", nil)
		r.get(t, "twilio", nil) // the next request remembers: no refusal
		rep := st.Report()
		var fallback string
		for _, row := range rep.Rows {
			if row.Check == "fallback" {
				fallback = row.Detail
			}
		}
		if !rep.OK || fallback != "refused 1 newer capture, then got wire 1" {
			var b strings.Builder
			rep.Table(&b)
			t.Errorf("wire 1 Echo:\n%s", b.String())
		}
	})

	t.Run("echo staging seal plugin", func(t *testing.T) { testPlugin(t, r) })

	r.invalid.Lock()
	defer r.invalid.Unlock()
	for _, bad := range r.invalid.got {
		t.Errorf("a capture broke Echo's wire schema: %s", bad)
	}
}

// Echo Staging Seal, the regular plugin, in place of the mu-plugin: the
// seal, the admin bar banner, Tools > Echo with its capability and nonce
// checks, the buttons and wp echo with a stub rhythm-echo. It runs last:
// removing the plugin removes the folder the container mounts.
func testPlugin(t *testing.T, r *rig) {
	s := r.seal()
	if err := Uninstall(r.content); err != nil {
		t.Fatal(err)
	}
	if err := InstallPlugin(r.content, Settings{URL: r.echoURL, Token: token, Bin: "/opt/echo-bin/rhythm-echo", Project: "/tmp"}); err != nil {
		t.Fatal(err)
	}
	time.Sleep(2500 * time.Millisecond) // opcache
	r.wp(t, nil, "plugin", "activate", PluginSlug)
	defer func() {
		r.wp(t, nil, "plugin", "deactivate", PluginSlug)
		if err := UninstallPlugin(r.content); err != nil {
			t.Error(err)
		}
		r.install(t, Settings{})
	}()

	r.get(t, "twilio", map[string]string{"X-Echo-Run": signed("plug/sms")})
	msgs := s.Inbox.List(catcher.Filter{})
	if len(msgs) != 1 || msgs[0].Status != model.Faked || msgs[0].Source.Plugin != "echo-fixture" || msgs[0].Run != "plug" {
		t.Fatalf("sealed through the plugin = %+v", msgs)
	}

	admin := r.login(t, "admin", "admin-test-pass")
	tools := r.wpURL + "/wp-admin/tools.php?page=" + PluginSlug
	status, page := r.browse(t, admin, http.MethodGet, tools, nil)
	for _, want := range []string{"This is a sealed copy", `id="echo-staging-seal-log"`, "<td>echo-fixture</td><td>sms</td><td>1</td><td>0</td>", "Snapshot before updating", "Compare since last update"} {
		if status != http.StatusOK || !strings.Contains(page, want) {
			t.Errorf("Tools > Echo as admin: HTTP %d, no %q", status, want)
		}
	}
	m := regexp.MustCompile(`value="snapshot">\s*<input type="hidden" id="_wpnonce" name="_wpnonce" value="([0-9a-f]+)"`).FindStringSubmatch(page)
	if m == nil {
		t.Fatalf("no nonce on the snapshot button:\n%.3000s", page)
	}
	runNonce := m[1]

	r.wp(t, nil, "user", "create", "sub", "sub@shop.test", "--role=subscriber", "--user_pass=sub-test-pass")
	sub := r.login(t, "sub", "sub-test-pass")
	if status, page := r.browse(t, sub, http.MethodGet, tools, nil); status != http.StatusForbidden || strings.Contains(page, "echo-staging-seal-log") {
		t.Errorf("Tools > Echo as a subscriber: HTTP %d", status)
	}
	if _, page := r.browse(t, sub, http.MethodGet, r.wpURL+"/wp-admin/profile.php", nil); !strings.Contains(page, "This is a sealed copy") {
		t.Error("a subscriber sees no banner")
	}

	post := r.wpURL + "/wp-admin/admin-post.php"
	run := url.Values{"action": {"echo_staging_seal_run"}, "command": {"snapshot"}}
	if status, _ := r.browse(t, admin, http.MethodPost, post, run); status != http.StatusForbidden {
		t.Errorf("snapshot without a nonce: HTTP %d, want 403", status)
	}
	run.Set("_wpnonce", runNonce)
	if status, _ := r.browse(t, sub, http.MethodPost, post, run); status != http.StatusForbidden {
		t.Errorf("snapshot as a subscriber: HTTP %d, want 403", status)
	}
	status, page = r.browse(t, admin, http.MethodPost, post, run)
	if status != http.StatusOK || !strings.Contains(page, "stub rhythm-echo snapshot --name before-updates") ||
		!strings.Contains(page, "stub on stderr") || !strings.Contains(page, "rhythm echo snapshot --name before-updates (exit code 0)") {
		t.Errorf("snapshot button: HTTP %d\n%.3000s", status, page)
	}

	// Settings need the nonce too. A blank token keeps the saved one.
	m = regexp.MustCompile(`name="action" value="echo_staging_seal_settings">\s*<input type="hidden" id="_wpnonce" name="_wpnonce" value="([0-9a-f]+)"`).FindStringSubmatch(page)
	if m == nil {
		t.Fatal("no nonce on the settings form")
	}
	form := url.Values{"action": {"echo_staging_seal_settings"}, "echo_url": {"http://echo.test:8025/"}, "echo_mode": {"observe"}, "echo_token": {"saved-token"}, "echo_site_id": {"client-b"}}
	if status, _ := r.browse(t, admin, http.MethodPost, post, form); status != http.StatusForbidden {
		t.Errorf("settings without a nonce: HTTP %d, want 403", status)
	}
	form.Set("_wpnonce", m[1])
	r.browse(t, admin, http.MethodPost, post, form)
	form.Set("echo_token", "")
	if status, page := r.browse(t, admin, http.MethodPost, post, form); status != http.StatusOK || !strings.Contains(page, "Settings saved.") {
		t.Errorf("settings: HTTP %d", status)
	}
	var saved map[string]string
	if err := json.Unmarshal([]byte(r.wp(t, nil, "option", "get", "echo_staging_seal_settings", "--format=json")), &saved); err != nil ||
		saved["url"] != "http://echo.test:8025" || saved["mode"] != "observe" || saved["token"] != "saved-token" || saved["site_id"] != "client-b" {
		t.Errorf("saved settings = %v (%v)", saved, err)
	}
	// The installer's settings file wins over the screen's; the screen
	// fills what the file leaves out, such as the site ID.
	if ping := r.rest(t, "ping", nil); ping["echo"].(map[string]any)["url"] != r.echoURL || ping["site_id"] != "client-b" {
		t.Errorf("ping = %v", ping)
	}

	// The banner follows the mode Echo reports.
	r.observe()
	r.wp(t, nil, "transient", "delete", "echo_staging_seal_mode")
	if _, page := r.browse(t, admin, http.MethodGet, tools, nil); !strings.Contains(page, "Echo is observing: sends go out") {
		t.Error("no observe banner")
	}
	r.seal()
	r.wp(t, nil, "transient", "delete", "echo_staging_seal_mode")

	// wp echo runs rhythm-echo with its output streamed and its exit code kept.
	if out := r.wp(t, nil, "echo", "snapshot", "before-x"); !strings.Contains(out, "stub rhythm-echo snapshot --name before-x") {
		t.Errorf("wp echo snapshot: %s", out)
	}
	out, err := r.dc("exec", "-T", "cli", "wp", "echo", "compare", "--snapshot=before-x")
	if code := exitCode(err); code != 1 || !strings.Contains(out, "stub rhythm-echo compare --snapshot before-x") || !strings.Contains(out, "stub on stderr") {
		t.Errorf("wp echo compare: exit %d\n%s", code, out)
	}
	if out := r.wp(t, nil, "echo", "status"); !strings.Contains(out, "Seal:        on: nothing leaves this site") ||
		!strings.Contains(out, "(reachable, seal)") || !strings.Contains(out, "stub rhythm-echo snapshots") {
		t.Errorf("wp echo status: %s", out)
	}
	if out, err := r.dc("exec", "-T", "cli", "wp", "echo", "snapshot", "a/b"); err == nil || !strings.Contains(out, "is not a snapshot name") {
		t.Errorf("wp echo snapshot a/b: %v\n%s", err, out)
	}
}

func exitCode(err error) int {
	var e *exec.ExitError
	if errors.As(err, &e) {
		return e.ExitCode()
	}
	if err != nil {
		return -1
	}
	return 0
}

// login signs a user in through wp-login.php and returns their browser.
func (r *rig) login(t *testing.T, user, pass string) *http.Client {
	t.Helper()
	jar, _ := cookiejar.New(nil)
	c := &http.Client{Jar: jar}
	r.browse(t, c, http.MethodGet, r.wpURL+"/wp-login.php", nil) // sets the test cookie
	status, page := r.browse(t, c, http.MethodPost, r.wpURL+"/wp-login.php", url.Values{"log": {user}, "pwd": {pass}, "testcookie": {"1"}, "redirect_to": {r.wpURL + "/wp-admin/"}})
	if status != http.StatusOK || strings.Contains(page, "login_error") {
		t.Fatalf("login as %s: HTTP %d\n%.1500s", user, status, page)
	}
	return c
}

// browse makes a request as a logged-in user, following redirects.
func (r *rig) browse(t *testing.T, c *http.Client, method, u string, form url.Values) (int, string) {
	t.Helper()
	var body io.Reader
	if form != nil {
		body = strings.NewReader(form.Encode())
	}
	req, _ := http.NewRequest(method, u, body)
	if form != nil {
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	}
	resp, err := c.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	b, _ := io.ReadAll(resp.Body)
	return resp.StatusCode, string(b)
}

// install rewrites the connector's settings in the mounted mu-plugins,
// pointed at the test's Echo with its token and waits until the site
// runs with them.
func (r *rig) install(t *testing.T, s Settings) {
	t.Helper()
	s.URL, s.Token = r.echoURL, token
	r.write(t, s)
	// opcache keeps the old settings file for a while (revalidate_freq),
	// longer than the sleep in write on a slow disk: ask the site.
	deadline := time.Now().Add(15 * time.Second)
	for {
		status, b := r.do(t, http.MethodGet, r.wpURL+"/?rest_route=/"+PingRoute, map[string]string{"X-Echo-Token": token})
		var ping map[string]any
		if status == http.StatusOK && json.Unmarshal(b, &ping) == nil && ping["proxy"] == s.Proxy && ping["site_id"] == s.SiteID {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("the site still runs with other settings than %+v: HTTP %d %.300s", s, status, b)
		}
		time.Sleep(250 * time.Millisecond)
	}
}

// write installs exactly s.
func (r *rig) write(t *testing.T, s Settings) {
	t.Helper()
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

// inside calls a REST route from inside the WordPress container, so the
// request comes from loopback, with one extra header line if given.
func (r *rig) inside(t *testing.T, route, header string) (int, string) {
	t.Helper()
	code := `$ctx = stream_context_create(array("http" => array("ignore_errors" => true, "header" => $argv[2])));
$body = file_get_contents("http://127.0.0.1/?rest_route=/" . $argv[1], false, $ctx);
echo explode(" ", $http_response_header[0])[1], "\n", $body;`
	out, err := r.dc("exec", "-T", "wp", "php", "-r", code, route, header)
	if err != nil {
		t.Fatalf("php in the wp container: %v\n%s", err, out)
	}
	status, body, _ := strings.Cut(out, "\n")
	n, _ := strconv.Atoi(strings.TrimSpace(status))
	return n, body
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

// ports picks n free ports in 18900–18999, or in the range ECHO_TEST_PORTS
// names ("20480-20499") on a machine where those are taken.
func ports(t *testing.T, n int) []int {
	t.Helper()
	lo, hi := 18900, 18999
	if v := os.Getenv("ECHO_TEST_PORTS"); v != "" {
		a, b, _ := strings.Cut(v, "-")
		l, err1 := strconv.Atoi(a)
		h, err2 := strconv.Atoi(b)
		if err1 != nil || err2 != nil || h < l {
			t.Fatalf("ECHO_TEST_PORTS=%q: want LOW-HIGH", v)
		}
		lo, hi = l, h
	}
	var out []int
	for _, p := range rand.Perm(hi - lo + 1) {
		port := lo + p
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
	t.Fatalf("no %d free ports in %d-%d", n, lo, hi)
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

	// Echo, switchable between seal, observe and down. Every capture is
	// checked against the wire schema first, as Echo's conformance stand-in
	// does.
	r.seal()
	schema, err := conformance.WireSchema()
	if err != nil {
		t.Fatal(err)
	}
	listen(t, echoPort, http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		if st := r.standin.Load(); st != nil {
			st.ServeHTTP(w, req)
			return
		}
		if req.URL.Path == "/v1/capture" {
			b, _ := io.ReadAll(req.Body)
			req.Body = io.NopCloser(bytes.NewReader(b))
			if problems := schema.Capture(b); len(problems) > 0 {
				r.invalid.Lock()
				r.invalid.got = append(r.invalid.got, strings.Join(problems, "; ")+"\n  "+string(b))
				r.invalid.Unlock()
				http.Error(w, "conformance: "+strings.Join(problems, "; "), http.StatusBadRequest)
				return
			}
			var c wire.Capture
			if json.Unmarshal(b, &c) == nil && c.Kind == "email" && c.Email != nil {
				r.mails.Lock()
				r.mails.got = append(r.mails.got, *c.Email)
				r.mails.Unlock()
			}
		}
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
	for _, d := range []string{"mu-plugins", "plugins/echo-fixture", PluginDir, "echo-bin"} {
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
	// A stand-in rhythm-echo for Echo Staging Seal's buttons and wp echo:
	// it prints its arguments and, like compare finding a change, exits 1
	// for compare.
	stub := "#!/bin/sh\necho \"stub rhythm-echo $*\"\necho \"stub on stderr\" >&2\n[ \"$1\" = compare ] && exit 1\nexit 0\n"
	if err := os.WriteFile(filepath.Join(content, "echo-bin", "rhythm-echo"), []byte(stub), 0o755); err != nil {
		t.Fatal(err)
	}
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
  - downloads.example.test:127.0.0.1
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
      - %[1]s/plugins/echo-staging-seal:/var/www/html/wp-content/plugins/echo-staging-seal:ro
      - %[1]s/echo-bin:/opt/echo-bin:ro
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
      - %[1]s/plugins/echo-staging-seal:/var/www/html/wp-content/plugins/echo-staging-seal:ro
      - %[1]s/echo-bin:/opt/echo-bin:ro
volumes:
  site: {}
`, content, wpPort, smtpPort, hookPort, indent(extra, "    "))
}

func indent(s, pad string) string {
	return pad + strings.ReplaceAll(s, "\n", "\n"+pad)
}
