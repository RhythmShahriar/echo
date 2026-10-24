// Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
// SPDX-License-Identifier: MIT

package dns

import (
	"context"
	"errors"
	"net"
	"sync"
	"time"
)

// A dead resolver used to cost Go's defaults (5 s, 2 attempts) on every
// lookup, about a minute for one check. These bound a query and the whole
// check, so a dead server is reported in seconds and the records that do
// answer are still checked.
var (
	queryTimeout = 3 * time.Second
	checkBudget  = 15 * time.Second
)

// Server is a Resolver that sends every query to one DNS server, as
// --resolver does. Its errors name that server: Go's own text names the
// first nameserver in resolv.conf, whatever the dialer connected to.
type Server struct {
	Addr string // host:port
	r    *net.Resolver
}

// NewServer returns a Resolver that asks the DNS server at addr
// (host:port; port 53 when missing) over UDP, or TCP for long answers.
func NewServer(addr string) *Server {
	if _, _, err := net.SplitHostPort(addr); err != nil {
		addr = net.JoinHostPort(addr, "53")
	}
	s := &Server{Addr: addr}
	s.r = &net.Resolver{PreferGo: true, Dial: func(ctx context.Context, network, _ string) (net.Conn, error) {
		var d net.Dialer
		return d.DialContext(ctx, network, s.Addr)
	}}
	return s
}

// LookupTXT implements Resolver.
func (s *Server) LookupTXT(ctx context.Context, name string) ([]string, error) {
	v, err := s.r.LookupTXT(ctx, name)
	return v, s.named(err)
}

// LookupMX implements Resolver.
func (s *Server) LookupMX(ctx context.Context, name string) ([]*net.MX, error) {
	v, err := s.r.LookupMX(ctx, name)
	return v, s.named(err)
}

// LookupHost implements Resolver.
func (s *Server) LookupHost(ctx context.Context, host string) ([]string, error) {
	v, err := s.r.LookupHost(ctx, host)
	return v, s.named(err)
}

// LookupCNAME implements Resolver.
func (s *Server) LookupCNAME(ctx context.Context, host string) (string, error) {
	v, err := s.r.LookupCNAME(ctx, host)
	return v, s.named(err)
}

// named rewrites the server in a DNS error ("lookup x on <server>") to
// the one actually asked. The error is copied, never changed in place.
func (s *Server) named(err error) error {
	var de *net.DNSError
	if err == nil || !errors.As(err, &de) {
		return err
	}
	cp := *de
	cp.Server = s.Addr
	return &cp
}

// tracked wraps the check's Resolver: it gives each query its own
// timeout and counts which lookups got an answer, so Check can tell a
// resolver it never reached from records that are missing or broken.
type tracked struct {
	r Resolver

	mu       sync.Mutex
	answered int
	failed   int
	first    error // the first lookup that got no answer
}

func (t *tracked) LookupTXT(ctx context.Context, name string) ([]string, error) {
	ctx, cancel := context.WithTimeout(ctx, queryTimeout)
	defer cancel()
	v, err := t.r.LookupTXT(ctx, name)
	return v, t.note(err)
}

func (t *tracked) LookupMX(ctx context.Context, name string) ([]*net.MX, error) {
	ctx, cancel := context.WithTimeout(ctx, queryTimeout)
	defer cancel()
	v, err := t.r.LookupMX(ctx, name)
	return v, t.note(err)
}

func (t *tracked) LookupHost(ctx context.Context, host string) ([]string, error) {
	ctx, cancel := context.WithTimeout(ctx, queryTimeout)
	defer cancel()
	v, err := t.r.LookupHost(ctx, host)
	return v, t.note(err)
}

func (t *tracked) LookupCNAME(ctx context.Context, host string) (string, error) {
	ctx, cancel := context.WithTimeout(ctx, queryTimeout)
	defer cancel()
	v, err := t.r.LookupCNAME(ctx, host)
	return v, t.note(err)
}

func (t *tracked) note(err error) error {
	t.mu.Lock()
	defer t.mu.Unlock()
	if answered(err) {
		t.answered++
	} else {
		t.failed++
		if t.first == nil {
			t.first = err
		}
	}
	return err
}

// unreachable reports whether lookups were made and none got an answer.
func (t *tracked) unreachable() bool {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.failed > 0 && t.answered == 0
}

// answered reports whether a lookup's result came from a server that
// replied: records, "no such name or data", or an error code such as
// SERVFAIL (which Go reports as "server misbehaving"). A timeout or a
// refused connection means no reply.
func answered(err error) bool {
	if err == nil || NotFound(err) {
		return true
	}
	var de *net.DNSError
	return errors.As(err, &de) && de.Err == "server misbehaving"
}

// serverOf names the resolver a check asked: the --resolver address, the
// server in the first failed lookup's error (the system's first
// nameserver), or "" when neither is known.
func serverOf(r Resolver, first error) string {
	if s, ok := r.(*Server); ok {
		return s.Addr
	}
	var de *net.DNSError
	if errors.As(first, &de) && de.Server != "" {
		return de.Server
	}
	return ""
}
