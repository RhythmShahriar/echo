// Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
// SPDX-License-Identifier: MIT

package main

import (
	"bytes"
	"context"
	"net"
	"net/netip"
	"strings"
	"testing"

	"golang.org/x/net/dns/dnsmessage"
)

// resolver asks the server at addr over network (udp or tcp).
func resolver(network, addr string) *net.Resolver {
	return &net.Resolver{PreferGo: true, Dial: func(ctx context.Context, _, _ string) (net.Conn, error) {
		var d net.Dialer
		return d.DialContext(ctx, network, addr)
	}}
}

func TestEveryNameIsTheGate(t *testing.T) {
	ip := netip.MustParseAddr("10.254.250.10")
	pc, err := net.ListenPacket("udp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = pc.Close() })
	// Each protocol gets its own free port: the UDP port's number may be
	// taken on TCP.
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = l.Close() })
	go serveUDP(pc, ip)
	go serveTCP(l, ip)
	addrs := map[string]string{"udp": pc.LocalAddr().String(), "tcp": l.Addr().String()}
	for _, network := range []string{"udp", "tcp"} {
		r := resolver(network, addrs[network])
		for _, name := range []string{"example.com", "www.google-analytics.com", "c01.shop.test", "metadata.google.internal"} {
			addrs, err := r.LookupHost(t.Context(), name)
			if err != nil || len(addrs) != 1 || addrs[0] != "10.254.250.10" {
				t.Errorf("%s %s = %v, %v", network, name, addrs, err)
			}
		}
		// IPv6 gets no address: the gate has none.
		if ips, err := r.LookupIP(t.Context(), "ip6", "example.com"); err == nil && len(ips) > 0 {
			t.Errorf("%s AAAA = %v", network, ips)
		}
	}
}

func TestRespond(t *testing.T) {
	ip := netip.MustParseAddr("10.0.0.1")
	q := dnsmessage.NewBuilder(nil, dnsmessage.Header{ID: 7, RecursionDesired: true})
	_ = q.StartQuestions()
	_ = q.Question(dnsmessage.Question{Name: dnsmessage.MustNewName("x.test."), Type: dnsmessage.TypeMX, Class: dnsmessage.ClassINET})
	msg, _ := q.Finish()
	resp, ok := respond(msg, ip)
	if !ok {
		t.Fatal("no answer")
	}
	var p dnsmessage.Parser
	h, err := p.Start(resp)
	if err != nil || h.ID != 7 || !h.Response || h.RCode != dnsmessage.RCodeSuccess {
		t.Fatalf("header %+v %v", h, err)
	}
	_ = p.SkipAllQuestions()
	if ans, _ := p.AllAnswers(); len(ans) != 0 {
		t.Errorf("an MX question got %v", ans)
	}
	// Garbage and responses aren't answered.
	if _, ok := respond([]byte{1, 2, 3}, ip); ok {
		t.Error("garbage answered")
	}
	r := dnsmessage.NewBuilder(nil, dnsmessage.Header{ID: 1, Response: true})
	rm, _ := r.Finish()
	if _, ok := respond(rm, ip); ok {
		t.Error("a response answered")
	}
}

func TestFlags(t *testing.T) {
	var errOut bytes.Buffer
	for _, args := range [][]string{{"-answer", "nope"}, {"-answer", "::1"}, {"-bad"}} {
		errOut.Reset()
		if code := run(args, &errOut); code != 2 {
			t.Errorf("%v: exit %d", args, code)
		}
	}
	if code := run([]string{"-answer", "10.0.0.1", "-listen", "256.0.0.1:53"}, &errOut); code != 1 || !strings.Contains(errOut.String(), "dns:") {
		t.Errorf("a bad -listen: exit %d, %s", code, errOut.String())
	}
}

func FuzzRespond(f *testing.F) {
	q := dnsmessage.NewBuilder(nil, dnsmessage.Header{ID: 1})
	_ = q.StartQuestions()
	_ = q.Question(dnsmessage.Question{Name: dnsmessage.MustNewName("example.com."), Type: dnsmessage.TypeA, Class: dnsmessage.ClassINET})
	msg, _ := q.Finish()
	f.Add(msg)
	f.Add([]byte{})
	ip := netip.MustParseAddr("10.0.0.1")
	f.Fuzz(func(t *testing.T, b []byte) {
		resp, ok := respond(b, ip)
		if !ok {
			return
		}
		var p dnsmessage.Parser
		if _, err := p.Start(resp); err != nil {
			t.Fatalf("an answer that doesn't parse: %v", err)
		}
	})
}
