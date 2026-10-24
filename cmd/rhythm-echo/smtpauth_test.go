package main

import (
	"bytes"
	"context"
	"net"
	"net/smtp"
	"strings"
	"testing"
	"time"

	"github.com/RhythmShahriar/echo/catcher"
	"github.com/RhythmShahriar/echo/sendmail"
)

func TestSMTPSinkBeyondLoopbackAsksForTheToken(t *testing.T) {
	s := catcher.NewServer(catcher.NewInbox(100), catcher.Config{})
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	sink := catcher.NewSMTP(s, ln.Addr().String())
	sink.Backend = tokenBackend{inner: sink.Backend, token: "t0k3n"}
	go func() { _ = sink.Serve(ln) }()
	defer func() { _ = sink.Close() }()
	addr := ln.Addr().String()
	msg := []byte("Subject: hi\r\n\r\nhello\r\n")

	if err := smtp.SendMail(addr, nil, "a@example.test", []string{"b@example.test"}, msg); err == nil || !strings.Contains(err.Error(), "530") {
		t.Errorf("without AUTH: %v, want 530", err)
	}
	if err := smtp.SendMail(addr, sendmail.TokenAuth("wrong"), "a@example.test", []string{"b@example.test"}, msg); err == nil {
		t.Error("a wrong token was accepted")
	}
	if err := smtp.SendMail(addr, sendmail.TokenAuth("t0k3n"), "a@example.test", []string{"b@example.test"}, msg); err != nil {
		t.Fatalf("with the token: %v", err)
	}
	// The sendmail shim logs in with RHYTHM_ECHO_TOKEN.
	t.Setenv(sendmail.EnvSMTP, addr)
	t.Setenv(sendmail.EnvToken, "t0k3n")
	var errb bytes.Buffer
	if code := sendmail.Main([]string{"-i", "c@example.test"}, strings.NewReader("Subject: shim\n\nhi\n"), &errb); code != 0 {
		t.Fatalf("shim: exit %d %s", code, errb.String())
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	if got := s.Inbox.Wait(ctx, catcher.Filter{}, 2, 100*time.Millisecond); len(got) != 2 {
		t.Errorf("got %d messages, want 2", len(got))
	}
}
