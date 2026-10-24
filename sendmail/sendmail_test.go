// Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
// SPDX-License-Identifier: MIT

package sendmail

import (
	"bytes"
	"context"
	"net"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/RhythmShahriar/echo/catcher"
	"github.com/RhythmShahriar/echo/model"
)

// sink starts a real catcher with its SMTP sink on loopback and points
// the shim at it.
func sink(t *testing.T) *catcher.Server {
	t.Helper()
	s := catcher.NewServer(catcher.NewInbox(100), catcher.Config{})
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	srv := catcher.NewSMTP(s, l.Addr().String())
	go func() { _ = srv.Serve(l) }()
	t.Cleanup(func() { _ = srv.Close() })
	clearEnv(t)
	t.Setenv(EnvSMTP, l.Addr().String())
	return s
}

func clearEnv(t *testing.T) {
	for _, k := range []string{EnvSMTP, EnvURL, EnvToken, EnvForce, EnvRun, "WP_ENVIRONMENT_TYPE", "APP_ENV", "VERCEL_ENV", "NODE_ENV"} {
		t.Setenv(k, "")
	}
}

func run(t *testing.T, msg string, args ...string) (int, string) {
	t.Helper()
	var stderr bytes.Buffer
	code := Main(args, strings.NewReader(msg), &stderr)
	return code, stderr.String()
}

func one(t *testing.T, s *catcher.Server) model.Message {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	ms := s.Inbox.Wait(ctx, catcher.Filter{}, 1, 100*time.Millisecond)
	if len(ms) != 1 {
		t.Fatalf("got %d messages", len(ms))
	}
	return ms[0]
}

// The way PHP's mail() calls it: sendmail -t -i, LF line endings.
const phpStyle = "To: Ann <ann@example.test>\nCc: bob@example.test\nBcc: hidden@example.test\nSubject: Order #42\n" +
	"From: shop@example.test\nX-Echo-Run: r1/checkout\n\nThanks for your order.\n.\nSee you soon.\n"

func TestExtractRecipientsAndHideBcc(t *testing.T) {
	s := sink(t)
	if code, errOut := run(t, phpStyle, "-t", "-i"); code != 0 {
		t.Fatalf("exit %d: %s", code, errOut)
	}
	m := one(t, s)
	if m.Subject != "Order #42" || m.From != "shop@example.test" || len(m.To) != 1 || m.To[0] != "ann@example.test" ||
		len(m.CC) != 1 || len(m.BCC) != 1 || m.BCC[0] != "hidden@example.test" || m.Run != "r1" || m.Action != "checkout" {
		t.Errorf("message = %+v", m)
	}
	if strings.Contains(m.Body, "Bcc:") {
		t.Error("Bcc header was delivered")
	}
	if !strings.Contains(m.Text, ".\r\nSee you soon.") && !strings.Contains(m.Text, ".\nSee you soon.") {
		t.Errorf("-i: a lone dot ended the message: %q", m.Text)
	}
	if m.Source.Via != "smtp" {
		t.Errorf("source = %+v", m.Source)
	}
}

func TestArgumentRecipientsAndEnvelopeFrom(t *testing.T) {
	s := sink(t)
	msg := "Subject: Cron report\n\nAll good\n.\nnot part of the message\n"
	if code, errOut := run(t, msg, "-oi", "-odb", "-ffrom@example.test", "-F", "Cron Daemon", "ops@example.test"); code != 0 {
		t.Fatalf("exit %d: %s", code, errOut)
	}
	m := one(t, s)
	if m.From != "from@example.test" || len(m.To) != 1 || m.To[0] != "ops@example.test" {
		t.Errorf("message = %+v", m)
	}
	if !strings.Contains(m.Body, `From: "Cron Daemon" <from@example.test>`) || !strings.Contains(m.Body, "Message-ID: <") || !strings.Contains(m.Body, "Date: ") {
		t.Errorf("added headers missing: %q", m.Body)
	}
}

func TestLoneDotEndsMessageWithoutI(t *testing.T) {
	s := sink(t)
	if code, errOut := run(t, "Subject: x\n\nline one\n.\nafter the dot\n", "-f", "a@example.test", "b@example.test"); code != 0 {
		t.Fatalf("exit %d: %s", code, errOut)
	}
	if m := one(t, s); strings.Contains(m.Text, "after the dot") {
		t.Errorf("text = %q", m.Text)
	}
}

func TestTagsRunFromEnvironment(t *testing.T) {
	s := sink(t)
	t.Setenv(EnvRun, "cron7/nightly-report")
	if code, errOut := run(t, "Subject: Nightly\n\nAll good\n", "-i", "ops@example.test"); code != 0 {
		t.Fatalf("exit %d: %s", code, errOut)
	}
	if m := one(t, s); m.Run != "cron7" || m.Action != "nightly-report" {
		t.Errorf("run = %q/%q, want cron7/nightly-report", m.Run, m.Action)
	}
	// The message's own header wins over the environment.
	s = sink(t)
	t.Setenv(EnvRun, "cron7/nightly-report")
	if code, errOut := run(t, phpStyle, "-t", "-i"); code != 0 {
		t.Fatalf("exit %d: %s", code, errOut)
	}
	if m := one(t, s); m.Run != "r1" || m.Action != "checkout" {
		t.Errorf("run = %q/%q, want the header's r1/checkout", m.Run, m.Action)
	}
}

func TestTagsRunViaCaptureAPI(t *testing.T) {
	clearEnv(t)
	s := catcher.NewServer(catcher.NewInbox(100), catcher.Config{})
	api := httptest.NewServer(s)
	defer api.Close()
	t.Setenv(EnvURL, api.URL)
	t.Setenv(EnvRun, "job3/welcome")
	if code, errOut := run(t, "Subject: Welcome\n\nHi\n", "-i", "ann@example.test"); code != 0 {
		t.Fatalf("exit %d: %s", code, errOut)
	}
	if m := one(t, s); m.Run != "job3" || m.Action != "welcome" {
		t.Errorf("run = %q/%q, want job3/welcome", m.Run, m.Action)
	}
}

func TestErrors(t *testing.T) {
	clearEnv(t)
	t.Setenv(EnvSMTP, "127.0.0.1:1") // nothing listens
	cases := []struct {
		name string
		args []string
		msg  string
		code int
		want string
	}{
		{"no recipients", nil, "Subject: x\n\nhi\n", exUsage, "no recipients"},
		{"unknown flag", []string{"-Z"}, "", exUsage, "unknown option"},
		{"unsupported mode", []string{"-bs"}, "", exUsage, "not supported"},
		{"missing -f value", []string{"-f"}, "", exUsage, "needs a value"},
		{"echo down", []string{"a@example.test"}, "Subject: x\n\nhi\n", exUnavailable, "rhythm echo up"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			code, errOut := run(t, c.msg, c.args...)
			if code != c.code || !strings.Contains(errOut, c.want) {
				t.Errorf("exit %d %q, want %d containing %q", code, errOut, c.code, c.want)
			}
		})
	}
}

func TestRefusesProduction(t *testing.T) {
	s := sink(t)
	t.Setenv("WP_ENVIRONMENT_TYPE", "production")
	if code, errOut := run(t, phpStyle, "-t", "-i"); code != exConfig || !strings.Contains(errOut, "production") {
		t.Fatalf("exit %d %q", code, errOut)
	}
	t.Setenv(EnvForce, "1")
	if code, errOut := run(t, phpStyle, "-t", "-i"); code != 0 {
		t.Fatalf("forced: exit %d %q", code, errOut)
	}
	one(t, s)
}

func TestViaCaptureAPI(t *testing.T) {
	clearEnv(t)
	s := catcher.NewServer(catcher.NewInbox(100), catcher.Config{Token: "tok-123"})
	api := httptest.NewServer(s)
	defer api.Close()
	t.Setenv(EnvURL, api.URL)
	if code, errOut := run(t, phpStyle, "-t", "-i"); code != exTempFail || !strings.Contains(errOut, "401") {
		t.Fatalf("without token: exit %d %q", code, errOut)
	}
	t.Setenv(EnvToken, "tok-123")
	if code, errOut := run(t, phpStyle, "-t", "-i"); code != 0 {
		t.Fatalf("exit %d: %s", code, errOut)
	}
	m := one(t, s)
	if m.Subject != "Order #42" || len(m.BCC) != 1 || m.Source.Via != "sendmail" || m.Run != "r1" {
		t.Errorf("message = %+v", m)
	}
}

func TestParseArgs(t *testing.T) {
	o, err := parseArgs([]string{"-t", "-i", "-r", "bounce@example.test", "-oem", "-B", "8BITMIME", "--", "-odd@example.test"})
	if err != nil || !o.extract || !o.ignoreDots || o.from != "bounce@example.test" || len(o.rcpts) != 1 || o.rcpts[0] != "-odd@example.test" {
		t.Errorf("%+v %v", o, err)
	}
}
