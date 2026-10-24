// Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
// SPDX-License-Identifier: MIT

package dns

import (
	"context"
	"fmt"
	"net"
	"strings"
	"testing"
	"time"

	"golang.org/x/net/dns/dnsmessage"

	"github.com/RhythmShahriar/echo/model"
)

// listenDNS opens UDP and TCP on the same free port in 20440-20459 (the
// range these tests may use) and returns both with the address.
func listenDNS(t *testing.T) (net.PacketConn, net.Listener, string) {
	t.Helper()
	for port := 20440; port <= 20459; port++ {
		addr := fmt.Sprintf("127.0.0.1:%d", port)
		pc, err := net.ListenPacket("udp", addr)
		if err != nil {
			continue
		}
		ln, err := net.Listen("tcp", addr)
		if err != nil {
			_ = pc.Close()
			continue
		}
		t.Cleanup(func() { _ = pc.Close(); _ = ln.Close() })
		return pc, ln, addr
	}
	t.Fatal("no free port in 20440-20459")
	return nil, nil, ""
}

// holdTCP accepts connections and never answers on them.
func holdTCP(ln net.Listener) {
	for {
		c, err := ln.Accept()
		if err != nil {
			return
		}
		go func() {
			b := make([]byte, 512)
			for {
				if _, err := c.Read(b); err != nil {
					_ = c.Close()
					return
				}
			}
		}()
	}
}

// blackHole is a DNS server that reads every query and answers none.
func blackHole(t *testing.T) string {
	pc, ln, addr := listenDNS(t)
	go func() {
		b := make([]byte, 1500)
		for {
			if _, _, err := pc.ReadFrom(b); err != nil {
				return
			}
		}
	}()
	go holdTCP(ln)
	return addr
}

// halfWorking answers MX for shop.test and A for its MX host. It drops
// every TXT and CNAME query, like a resolver that times out on large or
// filtered record types.
func halfWorking(t *testing.T) string {
	pc, ln, addr := listenDNS(t)
	go holdTCP(ln)
	go func() {
		b := make([]byte, 1500)
		for {
			n, from, err := pc.ReadFrom(b)
			if err != nil {
				return
			}
			if out := answerQuery(b[:n]); out != nil {
				_, _ = pc.WriteTo(out, from)
			}
		}
	}()
	return addr
}

func answerQuery(q []byte) []byte {
	var p dnsmessage.Parser
	h, err := p.Start(q)
	if err != nil {
		return nil
	}
	qs, err := p.AllQuestions()
	if err != nil || len(qs) != 1 {
		return nil
	}
	question := qs[0]
	name := strings.ToLower(question.Name.String())
	if question.Type == dnsmessage.TypeTXT || question.Type == dnsmessage.TypeCNAME {
		return nil
	}
	resp := dnsmessage.Message{
		Header:    dnsmessage.Header{ID: h.ID, Response: true, RecursionDesired: h.RecursionDesired, RecursionAvailable: true},
		Questions: qs,
	}
	rh := dnsmessage.ResourceHeader{Name: question.Name, Class: dnsmessage.ClassINET, TTL: 60}
	switch {
	case question.Type == dnsmessage.TypeMX && name == "shop.test.":
		resp.Answers = append(resp.Answers, dnsmessage.Resource{Header: rh,
			Body: &dnsmessage.MXResource{Pref: 10, MX: dnsmessage.MustNewName("mx.shop.test.")}})
	case question.Type == dnsmessage.TypeA && name == "mx.shop.test.":
		resp.Answers = append(resp.Answers, dnsmessage.Resource{Header: rh,
			Body: &dnsmessage.AResource{A: [4]byte{127, 0, 0, 1}}})
	case name == "shop.test." || name == "mx.shop.test.":
		// No data of this type: an empty NOERROR answer.
	default:
		resp.RCode = dnsmessage.RCodeNameError
	}
	out, err := resp.Pack()
	if err != nil {
		return nil
	}
	return out
}

// shortWaits makes a query wait 300ms and a check 2s for one test, so a
// dead resolver costs seconds, not the real 15s budget. The test must not
// call t.Parallel: parallel tests run after it, when the waits are back.
func shortWaits(t *testing.T) {
	t.Helper()
	q, b := queryTimeout, checkBudget
	queryTimeout, checkBudget = 300*time.Millisecond, 2*time.Second
	t.Cleanup(func() { queryTimeout, checkBudget = q, b })
}

// The real waits keep a dead resolver to seconds too.
func TestWaitsAreBounded(t *testing.T) {
	if queryTimeout > 5*time.Second || checkBudget > 15*time.Second || queryTimeout >= checkBudget {
		t.Errorf("query %s, check %s; want at most 5s and 15s", queryTimeout, checkBudget)
	}
}

func TestUnreachableResolverIsOneError(t *testing.T) {
	shortWaits(t)
	addr := blackHole(t)
	start := time.Now()
	r := Check(context.Background(), "shop.test", Options{Resolver: NewServer(addr)})
	if took := time.Since(start); took > checkBudget+time.Second {
		t.Errorf("took %s, want under the %s budget", took, checkBudget)
	}
	if !r.Unreachable || r.Resolver != addr {
		t.Errorf("Unreachable = %v, Resolver = %q; want true, %q", r.Unreachable, r.Resolver, addr)
	}
	if len(r.Issues) != 1 {
		t.Fatalf("want one issue, got %q", titles(r))
	}
	i := r.Issues[0]
	if i.Check != "dns/resolver" || i.Level != model.Error || i.Title != "Could not reach the resolver "+addr {
		t.Errorf("issue = %s %s %q", i.Check, i.Level, i.Title)
	}
	if !strings.Contains(i.Fix, addr) || strings.Contains(i.Fix, "shop.test") {
		t.Errorf("fix should point at the resolver, not the domain: %q", i.Fix)
	}
	if strings.Contains(i.Detail, "127.0.0.53") || !strings.Contains(i.Detail, "on "+addr) {
		t.Errorf("detail should name %s: %q", addr, i.Detail)
	}
}

func TestHalfWorkingResolverKeepsRecordWarnings(t *testing.T) {
	shortWaits(t)
	addr := halfWorking(t)
	start := time.Now()
	r := Check(context.Background(), "shop.test", Options{Resolver: NewServer(addr)})
	if took := time.Since(start); took > checkBudget+time.Second {
		t.Errorf("took %s, want under the %s budget", took, checkBudget)
	}
	if r.Unreachable {
		t.Fatalf("MX answered, so the resolver is reachable: %q", titles(r))
	}
	if len(r.MX) != 1 || r.MX[0] != "mx.shop.test" {
		t.Errorf("MX = %q", r.MX)
	}
	for _, want := range []string{"SPF lookup failed", "DKIM lookup failed", "DMARC lookup failed"} {
		i := find(r, want)
		if i == nil {
			t.Errorf("want %q in %q", want, titles(r))
			continue
		}
		if !strings.Contains(i.Detail, "on "+addr) {
			t.Errorf("%s should name %s: %q", want, addr, i.Detail)
		}
	}
	if find(r, "MX lookup failed") != nil {
		t.Errorf("MX answered: %q", titles(r))
	}
}

func TestServerFailIsNotUnreachable(t *testing.T) {
	// SERVFAIL for every name is a reply: the domain's nameservers are
	// broken, not the resolver, so each record keeps its warning.
	z := clean()
	z.Fail = map[string]bool{}
	for _, n := range []string{"shop.example", "_dmarc.shop.example", "_mta-sts.shop.example", "_smtp._tls.shop.example", "default._bimi.shop.example"} {
		z.Fail[n] = true
	}
	for _, s := range commonSelectors() {
		z.Fail[s+"._domainkey.shop.example"] = true
	}
	r := run(z, nil)
	if r.Unreachable || find(r, "SPF lookup failed") == nil {
		t.Errorf("Unreachable = %v, issues %q", r.Unreachable, titles(r))
	}
}

func TestNewServerAddsPortAndNamesItself(t *testing.T) {
	if s := NewServer("127.0.0.1"); s.Addr != "127.0.0.1:53" {
		t.Errorf("Addr = %q", s.Addr)
	}
	if s := NewServer("::1"); s.Addr != "[::1]:53" {
		t.Errorf("Addr = %q", s.Addr)
	}
	s := NewServer("127.0.0.1:20440")
	err := s.named(&net.DNSError{Err: "i/o timeout", Name: "shop.test", Server: "127.0.0.53:53", IsTimeout: true})
	if got := err.Error(); got != "lookup shop.test on 127.0.0.1:20440: i/o timeout" {
		t.Errorf("error = %q", got)
	}
	if answered(err) {
		t.Error("a timeout is not an answer")
	}
}

func TestCanceledRunIsNotUnreachable(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	r := Check(ctx, "shop.test", Options{Resolver: NewServer("127.0.0.1:20459")})
	if r.Unreachable {
		t.Errorf("a canceled run should not blame the resolver: %q", titles(r))
	}
}
