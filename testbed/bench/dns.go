//go:build testbed

package main

import (
	"context"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"net"
	"net/netip"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"

	"golang.org/x/net/dns/dnsmessage"
	"gopkg.in/yaml.v3"
)

// DNSAddr is where the fixture DNS server listens (UDP and TCP).
const DNSAddr = "127.0.0.1:18870"

// Zone is testbed/dns/zone.yaml: name → type → values, and per-scenario
// replacements used while that scenario is broken.
type Zone struct {
	Records map[string]map[string][]string            `yaml:"records"`
	Broken  map[string]map[string]map[string][]string `yaml:"broken"`
}

// LoadZone reads the zone file.
func LoadZone(path string) (*Zone, error) {
	data, err := os.ReadFile(path) //nolint:gosec // the testbed's own zone
	if err != nil {
		return nil, err
	}
	var z Zone
	if err := yaml.Unmarshal(data, &z); err != nil {
		return nil, fmt.Errorf("%s: %w", path, err)
	}
	return &z, nil
}

// DNSServer answers queries from a Zone, as an authoritative server that
// also follows CNAMEs. It never forwards: names outside the zone are
// NXDOMAIN, so a check against it can't reach real DNS.
type DNSServer struct {
	Zone  *Zone
	State string // the testbed's state/ folder: broken.<id> applies while state/<id> exists

	mu    sync.Mutex
	udp   net.PacketConn
	tcp   net.Listener
	conns sync.WaitGroup
}

// Start listens on addr.
func (s *DNSServer) Start(addr string) error {
	pc, err := net.ListenPacket("udp", addr)
	if err != nil {
		return fmt.Errorf("fixture DNS on %s: %w", addr, err)
	}
	// The same port for TCP, which resolvers retry on for long answers.
	ln, err := net.Listen("tcp", pc.LocalAddr().String())
	if err != nil {
		_ = pc.Close()
		return fmt.Errorf("fixture DNS on %s: %w", addr, err)
	}
	s.udp, s.tcp = pc, ln
	go s.serveUDP()
	go s.serveTCP()
	return nil
}

// Close stops the server.
func (s *DNSServer) Close() {
	if s.udp != nil {
		_ = s.udp.Close()
	}
	if s.tcp != nil {
		_ = s.tcp.Close()
	}
	s.conns.Wait()
}

func (s *DNSServer) serveUDP() {
	buf := make([]byte, 4096)
	for {
		n, from, err := s.udp.ReadFrom(buf)
		if err != nil {
			return
		}
		if out := s.answer(buf[:n], 1232); out != nil {
			_, _ = s.udp.WriteTo(out, from)
		}
	}
}

func (s *DNSServer) serveTCP() {
	for {
		c, err := s.tcp.Accept()
		if err != nil {
			return
		}
		s.conns.Add(1)
		go func() {
			defer s.conns.Done()
			defer func() { _ = c.Close() }()
			for {
				_ = c.SetDeadline(time.Now().Add(5 * time.Second))
				var size [2]byte
				if _, err := io.ReadFull(c, size[:]); err != nil {
					return
				}
				msg := make([]byte, binary.BigEndian.Uint16(size[:]))
				if _, err := io.ReadFull(c, msg); err != nil {
					return
				}
				out := s.answer(msg, 65535)
				if out == nil {
					return
				}
				if _, err := c.Write(append(binary.BigEndian.AppendUint16(nil, uint16(len(out))), out...)); err != nil { //nolint:gosec // DNS messages are under 64 KiB
					return
				}
			}
		}()
	}
}

// records returns the records for name, with the broken scenarios'
// replacements applied.
func (s *DNSServer) records(name string) (map[string][]string, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	rr, ok := s.Zone.Records[name]
	ids := make([]string, 0, len(s.Zone.Broken))
	for id := range s.Zone.Broken {
		ids = append(ids, id)
	}
	slices.Sort(ids)
	for _, id := range ids {
		if _, err := os.Stat(filepath.Join(s.State, id)); err != nil {
			continue
		}
		if b, found := s.Zone.Broken[id][name]; found {
			rr, ok = b, true
		}
	}
	return rr, ok
}

// answer builds the response to one query, or nil for garbage.
func (s *DNSServer) answer(query []byte, limit int) []byte {
	var p dnsmessage.Parser
	h, err := p.Start(query)
	if err != nil {
		return nil
	}
	q, err := p.Question()
	if err != nil {
		return nil
	}
	resp := dnsmessage.Header{ID: h.ID, Response: true, Authoritative: true, RecursionDesired: h.RecursionDesired, RecursionAvailable: true}
	var answers []dnsmessage.Resource
	name := strings.ToLower(strings.TrimSuffix(q.Name.String(), "."))
	found := false
	for hops := 0; hops < 8; hops++ {
		rr, ok := s.records(name)
		if !ok {
			break
		}
		found = true
		if c := rr["CNAME"]; len(c) > 0 && q.Type != dnsmessage.TypeCNAME {
			answers = append(answers, resource(name, dnsmessage.TypeCNAME, c[0]))
			name = strings.ToLower(strings.TrimSuffix(c[0], "."))
			continue
		}
		for _, v := range rr[typeName(q.Type)] {
			answers = append(answers, resource(name, q.Type, v))
		}
		break
	}
	if !found {
		resp.RCode = dnsmessage.RCodeNameError
	}
	out, err := build(resp, q, answers)
	if err != nil {
		resp.RCode = dnsmessage.RCodeServerFailure
		out, _ = build(resp, q, nil)
	}
	if len(out) > limit {
		resp.Truncated = true
		out, _ = build(resp, q, nil)
	}
	return out
}

func build(h dnsmessage.Header, q dnsmessage.Question, answers []dnsmessage.Resource) ([]byte, error) {
	b := dnsmessage.NewBuilder(nil, h)
	b.EnableCompression()
	if err := b.StartQuestions(); err != nil {
		return nil, err
	}
	if err := b.Question(q); err != nil {
		return nil, err
	}
	if err := b.StartAnswers(); err != nil {
		return nil, err
	}
	for _, a := range answers {
		if a.Body == nil {
			continue
		}
		var err error
		switch body := a.Body.(type) {
		case *dnsmessage.AResource:
			err = b.AResource(a.Header, *body)
		case *dnsmessage.TXTResource:
			err = b.TXTResource(a.Header, *body)
		case *dnsmessage.MXResource:
			err = b.MXResource(a.Header, *body)
		case *dnsmessage.CNAMEResource:
			err = b.CNAMEResource(a.Header, *body)
		}
		if err != nil {
			return nil, err
		}
	}
	return b.Finish()
}

func typeName(t dnsmessage.Type) string {
	switch t {
	case dnsmessage.TypeA:
		return "A"
	case dnsmessage.TypeTXT:
		return "TXT"
	case dnsmessage.TypeMX:
		return "MX"
	case dnsmessage.TypeCNAME:
		return "CNAME"
	}
	return t.String()
}

// resource makes one answer record; a value that doesn't parse gives a
// record with no body, which build skips.
func resource(name string, t dnsmessage.Type, v string) dnsmessage.Resource {
	h := dnsmessage.ResourceHeader{Name: dnsmessage.MustNewName(name + "."), Type: t, Class: dnsmessage.ClassINET, TTL: 60}
	r := dnsmessage.Resource{Header: h}
	switch t {
	case dnsmessage.TypeA:
		if ip, err := netip.ParseAddr(v); err == nil && ip.Is4() {
			r.Body = &dnsmessage.AResource{A: ip.As4()}
		}
	case dnsmessage.TypeTXT:
		var parts []string
		for len(v) > 255 {
			parts, v = append(parts, v[:255]), v[255:]
		}
		r.Body = &dnsmessage.TXTResource{TXT: append(parts, v)}
	case dnsmessage.TypeMX:
		pref, host, ok := strings.Cut(v, " ")
		n, err := strconv.Atoi(pref)
		if ok && err == nil {
			r.Body = &dnsmessage.MXResource{Pref: uint16(n), MX: dnsmessage.MustNewName(strings.TrimSuffix(host, ".") + ".")} //nolint:gosec // MX preferences are small
		}
	case dnsmessage.TypeCNAME:
		r.Body = &dnsmessage.CNAMEResource{CNAME: dnsmessage.MustNewName(strings.TrimSuffix(v, ".") + ".")}
	}
	return r
}

// lookupTXT queries the fixture server, for tests.
func lookupTXT(ctx context.Context, addr, name string) ([]string, error) {
	r := &net.Resolver{PreferGo: true, Dial: func(ctx context.Context, network, _ string) (net.Conn, error) {
		var d net.Dialer
		return d.DialContext(ctx, network, addr)
	}}
	txt, err := r.LookupTXT(ctx, name)
	var de *net.DNSError
	if errors.As(err, &de) && de.IsNotFound {
		return nil, nil
	}
	return txt, err
}
