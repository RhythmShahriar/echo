// Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
// SPDX-License-Identifier: MIT

package compat

import (
	"bufio"
	"context"
	"io"
	"net"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"
)

const (
	spamdAddr  = "127.0.0.1:20515"
	closedAddr = "127.0.0.1:20516" // nothing listens here
)

var gtube = []byte("From: shop@shop.test\r\nTo: ann@example.com\r\nSubject: Test\r\n\r\nXJS*C4JDBQADN1.NSBN3*2IDNEN*GTUBE-STANDARD-ANTI-UBE-TEST-EMAIL*C.34X\r\n")

// fakeSpamd answers each connection with answer(request). It returns what
// it was sent: the command line, then the message.
func fakeSpamd(t *testing.T, answer func(cmd string, body []byte) string) <-chan string {
	t.Helper()
	ln, err := net.Listen("tcp", spamdAddr)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = ln.Close() })
	seen := make(chan string, 16)
	go func() {
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			br := bufio.NewReader(c)
			cmd, _ := br.ReadString('\n')
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
			body := make([]byte, length)
			_, _ = io.ReadFull(br, body)
			seen <- strings.TrimSpace(cmd)
			seen <- string(body)
			if a := answer(strings.TrimSpace(cmd), body); a != "" {
				_, _ = io.WriteString(c, a)
				_ = c.Close()
			} else {
				t.Cleanup(func() { _ = c.Close() }) // hang until the test ends
			}
		}
	}()
	return seen
}

func TestSpamScore(t *testing.T) {
	seen := fakeSpamd(t, func(cmd string, body []byte) string {
		rules := "GTUBE,HTML_MESSAGE,MISSING_DATE"
		return "SPAMD/1.1 0 EX_OK\r\nContent-length: " + strconv.Itoa(len(rules)) + "\r\nSpam: True ; 1002.5 / 5.0\r\n\r\n" + rules
	})
	score, rules, err := Spam(context.Background(), spamdAddr, gtube)
	if err != nil {
		t.Fatal(err)
	}
	if score != 1002.5 || !slices.Equal(rules, []string{"GTUBE", "HTML_MESSAGE", "MISSING_DATE"}) {
		t.Errorf("score %v rules %v", score, rules)
	}
	if cmd, body := <-seen, <-seen; cmd != "SYMBOLS SPAMC/1.5" || body != string(gtube) {
		t.Errorf("spamd got %q and %q", cmd, body)
	}
	r, err := SpamCheck(context.Background(), spamdAddr, gtube)
	if err != nil || !r.Spam || r.Threshold != 5 {
		t.Errorf("SpamCheck: %+v %v", r, err)
	}
}

func TestSpamClean(t *testing.T) {
	fakeSpamd(t, func(string, []byte) string {
		// No Content-length: the body runs to the end of the connection.
		return "SPAMD/1.1 0 EX_OK\r\nSpam: False ; -0.1 / 5.0\r\n\r\n"
	})
	r, err := SpamCheck(context.Background(), spamdAddr, []byte("Subject: hi\r\n\r\nhello\r\n"))
	if err != nil {
		t.Fatal(err)
	}
	if r.Spam || r.Score != -0.1 || len(r.Rules) != 0 {
		t.Errorf("%+v", r)
	}
}

func TestSpamDefaultPort(t *testing.T) {
	for in, want := range map[string]string{
		"127.0.0.1":      "127.0.0.1:783",
		"spamd":          "spamd:783",
		"::1":            "[::1]:783",
		"[::1]":          "[::1]:783",
		"127.0.0.1:7830": "127.0.0.1:7830",
	} {
		if got := spamAddr(in); got != want {
			t.Errorf("%s: got %s, want %s", in, got, want)
		}
	}
}

func TestSpamErrors(t *testing.T) {
	for _, tc := range []struct {
		name, answer, want string
	}{
		{"not spamd", "HTTP/1.1 400 Bad Request\r\n\r\n", `spamassassin at 127.0.0.1:20515: not a spamd answer: "HTTP/1.1 400 Bad Request"`},
		{"refused", "SPAMD/1.0 76 Bad header line: (EOF)\r\n", "spamassassin at 127.0.0.1:20515: spamd refused the message: 76 Bad header line: (EOF)"},
		{"no score", "SPAMD/1.1 0 EX_OK\r\nContent-length: 0\r\n\r\n", "spamassassin at 127.0.0.1:20515: not a spamd answer: no Spam header"},
		{"bad score", "SPAMD/1.1 0 EX_OK\r\nSpam: True ; lots\r\n\r\n", `spamassassin at 127.0.0.1:20515: not a spamd answer: Spam: "True ; lots"`},
		{"cut short", "SPAMD/1.1 0 EX_OK\r\nSpam: True ; 9.0 / 5.0\r\n", "spamassassin at 127.0.0.1:20515: closed the connection without a score"},
		{"huge body", "SPAMD/1.1 0 EX_OK\r\nContent-length: 99999999999\r\nSpam: True ; 9.0 / 5.0\r\n\r\nGTUBE", `spamassassin at 127.0.0.1:20515: not a spamd answer: Content-length "99999999999"`},
		{"body over the limit", "SPAMD/1.1 0 EX_OK\r\nContent-length: 1048577\r\nSpam: True ; 9.0 / 5.0\r\n\r\nGTUBE", `spamassassin at 127.0.0.1:20515: not a spamd answer: Content-length "1048577"`},
		{"short body", "SPAMD/1.1 0 EX_OK\r\nContent-length: 40\r\nSpam: True ; 9.0 / 5.0\r\n\r\nGTUBE", "spamassassin at 127.0.0.1:20515: closed the connection without a score"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			fakeSpamd(t, func(string, []byte) string { return tc.answer })
			_, _, err := Spam(context.Background(), spamdAddr, gtube)
			if err == nil || err.Error() != tc.want {
				t.Errorf("\n got %v\nwant %s", err, tc.want)
			}
		})
	}
}

func TestSpamConnectionRefused(t *testing.T) {
	_, _, err := Spam(context.Background(), closedAddr, gtube)
	if err == nil || !strings.HasPrefix(err.Error(), "spamassassin at 127.0.0.1:20516: couldn't connect: ") ||
		!strings.Contains(err.Error(), "refused") {
		t.Errorf("err %v", err)
	}
}

func TestSpamTimeout(t *testing.T) {
	fakeSpamd(t, func(string, []byte) string { return "" }) // never answers
	ctx, cancel := context.WithTimeout(context.Background(), 300*time.Millisecond)
	defer cancel()
	start := time.Now()
	_, _, err := Spam(ctx, spamdAddr, gtube)
	if err == nil || !strings.HasPrefix(err.Error(), "spamassassin at 127.0.0.1:20515: no answer within ") {
		t.Errorf("err %v", err)
	}
	if d := time.Since(start); d > 2*time.Second {
		t.Errorf("took %s", d)
	}
}

func TestSpamCanceled(t *testing.T) {
	fakeSpamd(t, func(string, []byte) string { return "" })
	ctx, cancel := context.WithCancel(context.Background())
	time.AfterFunc(100*time.Millisecond, cancel)
	_, _, err := Spam(ctx, spamdAddr, gtube)
	if err == nil || err.Error() != "spamassassin at 127.0.0.1:20515: canceled" {
		t.Errorf("err %v", err)
	}
}

func TestSpamTooLarge(t *testing.T) {
	_, _, err := Spam(context.Background(), closedAddr, make([]byte, MaxSpamSize+1))
	if err == nil || err.Error() != "spamassassin at 127.0.0.1:20516: the message is 512001 bytes; spamd only scores messages up to 500 KB" {
		t.Errorf("err %v", err)
	}
}
