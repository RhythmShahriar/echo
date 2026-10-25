// Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
// SPDX-License-Identifier: MIT

package dns

import (
	"context"
	"net"
	"sync"
)

// Zone is a static Resolver: fixture records keyed by name. It never
// touches the network, so tests and dry runs can check made-up domains.
// LookupTXT and LookupHost follow CNAMEs, as a recursive resolver would.
// Fail lists names whose lookups fail with a temporary error (SERVFAIL).
type Zone struct {
	TXT   map[string][]string  // name to its TXT strings
	MX    map[string][]*net.MX // name to its mail servers
	Host  map[string][]string  // name to its A and AAAA addresses
	CNAME map[string]string    // alias to target
	Fail  map[string]bool      // names that answer SERVFAIL

	mu      sync.Mutex
	queries int
}

// Queries returns how many lookups the zone has answered.
func (z *Zone) Queries() int {
	z.mu.Lock()
	defer z.mu.Unlock()
	return z.queries
}

func (z *Zone) count() {
	z.mu.Lock()
	z.queries++
	z.mu.Unlock()
}

func notFound(name string) error {
	return &net.DNSError{Err: "no such host", Name: name, IsNotFound: true}
}

func (z *Zone) fail(name string) error {
	if z.Fail[name] {
		return &net.DNSError{Err: "server misbehaving", Name: name, IsTemporary: true}
	}
	return nil
}

// canonical follows CNAMEs from name (at most 8 hops).
func (z *Zone) canonical(name string) string {
	for i := 0; i < 8; i++ {
		t, ok := z.CNAME[name]
		if !ok {
			break
		}
		name = normalize(t)
	}
	return name
}

// LookupTXT answers from TXT after following CNAMEs.
func (z *Zone) LookupTXT(_ context.Context, name string) ([]string, error) {
	z.count()
	name = z.canonical(normalize(name))
	if err := z.fail(name); err != nil {
		return nil, err
	}
	if v, ok := z.TXT[name]; ok && len(v) > 0 {
		return v, nil
	}
	return nil, notFound(name)
}

// LookupMX answers from MX after following CNAMEs.
func (z *Zone) LookupMX(_ context.Context, name string) ([]*net.MX, error) {
	z.count()
	name = z.canonical(normalize(name))
	if err := z.fail(name); err != nil {
		return nil, err
	}
	if v, ok := z.MX[name]; ok && len(v) > 0 {
		return v, nil
	}
	return nil, notFound(name)
}

// LookupHost answers from Host after following CNAMEs. An IP address
// answers itself, as net.LookupHost does.
func (z *Zone) LookupHost(_ context.Context, host string) ([]string, error) {
	z.count()
	host = z.canonical(normalize(host))
	if ip := net.ParseIP(host); ip != nil {
		return []string{host}, nil
	}
	if err := z.fail(host); err != nil {
		return nil, err
	}
	if v, ok := z.Host[host]; ok && len(v) > 0 {
		return v, nil
	}
	return nil, notFound(host)
}

// LookupCNAME returns the canonical name, with its trailing dot, for a
// name that is an alias or has records; any other name is not found.
func (z *Zone) LookupCNAME(_ context.Context, host string) (string, error) {
	z.count()
	host = normalize(host)
	if err := z.fail(host); err != nil {
		return "", err
	}
	c := z.canonical(host)
	if c != host || len(z.TXT[c]) > 0 || len(z.Host[c]) > 0 || len(z.MX[c]) > 0 {
		return c + ".", nil
	}
	return "", notFound(host)
}
