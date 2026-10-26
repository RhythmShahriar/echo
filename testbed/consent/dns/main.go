// Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
// SPDX-License-Identifier: MIT

// Command dns is the sealed Docker testbed's resolver: it answers every
// A query, whatever the name, with one address (the gate's) and never
// forwards anything. A browser in the runner that asks for any host,
// real or not, reaches the gate and nothing else.
//
//	dns -listen 0.0.0.0:53 -answer 10.254.250.10
package main

import (
	"encoding/binary"
	"errors"
	"flag"
	"fmt"
	"io"
	"net"
	"net/netip"
	"os"
	"time"

	"golang.org/x/net/dns/dnsmessage"
)

func main() { os.Exit(run(os.Args[1:], os.Stderr)) }

func run(args []string, stderr io.Writer) int {
	fs := flag.NewFlagSet("dns", flag.ContinueOnError)
	fs.SetOutput(stderr)
	listen := fs.String("listen", "0.0.0.0:53", "UDP and TCP address to answer on")
	answer := fs.String("answer", "", "the IPv4 address every name resolves to (the gate's)")
	if err := fs.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return 0
		}
		return 2
	}
	ip, err := netip.ParseAddr(*answer)
	if err != nil || !ip.Is4() {
		fmt.Fprintf(stderr, "dns: -answer %q: want an IPv4 address\n", *answer)
		return 2
	}
	pc, err := net.ListenPacket("udp", *listen)
	if err != nil {
		fmt.Fprintln(stderr, "dns:", err)
		return 1
	}
	l, err := net.Listen("tcp", *listen)
	if err != nil {
		fmt.Fprintln(stderr, "dns:", err)
		return 1
	}
	fmt.Fprintf(stderr, "dns: every name is %s, on %s\n", ip, *listen)
	go serveTCP(l, ip)
	serveUDP(pc, ip)
	return 1
}

// serveUDP answers queries on pc until it is closed.
func serveUDP(pc net.PacketConn, ip netip.Addr) {
	buf := make([]byte, 1232)
	for {
		n, from, err := pc.ReadFrom(buf)
		if err != nil {
			return
		}
		if resp, ok := respond(buf[:n], ip); ok {
			_, _ = pc.WriteTo(resp, from)
		}
	}
}

// serveTCP answers queries on l (two-byte length prefix each) until it
// is closed.
func serveTCP(l net.Listener, ip netip.Addr) {
	for {
		c, err := l.Accept()
		if err != nil {
			return
		}
		go func() {
			defer func() { _ = c.Close() }()
			_ = c.SetDeadline(time.Now().Add(10 * time.Second))
			var size [2]byte
			for {
				if _, err := io.ReadFull(c, size[:]); err != nil {
					return
				}
				msg := make([]byte, int(size[0])<<8|int(size[1]))
				if _, err := io.ReadFull(c, msg); err != nil {
					return
				}
				resp, ok := respond(msg, ip)
				if !ok {
					return
				}
				if len(resp) > 0xffff {
					return
				}
				prefix := binary.BigEndian.AppendUint16(nil, uint16(len(resp))) //nolint:gosec // checked against 0xffff above
				if _, err := c.Write(append(prefix, resp...)); err != nil {
					return
				}
			}
		}()
	}
}

// respond builds the answer to query: every A question gets ip, any
// other type an empty NOERROR answer (so a resolver doesn't go looking
// elsewhere). A message that isn't a query isn't answered.
func respond(query []byte, ip netip.Addr) ([]byte, bool) {
	var p dnsmessage.Parser
	h, err := p.Start(query)
	if err != nil || h.Response {
		return nil, false
	}
	qs, err := p.AllQuestions()
	if err != nil {
		return nil, false
	}
	b := dnsmessage.NewBuilder(nil, dnsmessage.Header{ID: h.ID, Response: true, Authoritative: true, RecursionDesired: h.RecursionDesired, RecursionAvailable: true})
	b.EnableCompression()
	if b.StartQuestions() != nil {
		return nil, false
	}
	for _, q := range qs {
		if b.Question(q) != nil {
			return nil, false
		}
	}
	if b.StartAnswers() != nil {
		return nil, false
	}
	for _, q := range qs {
		if q.Type != dnsmessage.TypeA || q.Class != dnsmessage.ClassINET {
			continue
		}
		if b.AResource(dnsmessage.ResourceHeader{Name: q.Name, Class: dnsmessage.ClassINET, TTL: 60}, dnsmessage.AResource{A: ip.As4()}) != nil {
			return nil, false
		}
	}
	out, err := b.Finish()
	return out, err == nil
}
