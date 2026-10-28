// Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
// SPDX-License-Identifier: MIT

// Package egress is the consent check's egress guard (PRD 13.7): small
// in-process HTTP proxies on 127.0.0.1 that every request Chrome makes
// goes through. They handle CONNECT and absolute-form HTTP.
//
// Two kinds of listener exist. The browser-wide one (StartRefuseAll) is
// Chrome's --proxy-server: only traffic outside any run's own context
// reaches it, which is Chrome's own, so it refuses everything and counts
// it as browser traffic. A guard (Start) serves one target: each run's
// incognito context names it as its proxy. A guard dials with the kit's
// address guard for the target host, so loopback, link-local and
// private destinations are allowed only when the target itself is one;
// the cloud metadata addresses and Chrome's own service hosts are
// refused whatever the target. A host --map-host covers is Echo's to
// answer in the tab, so a request for one that reaches a guard is
// refused rather than sent to the real host. With --map-only every host
// --map-host doesn't cover is refused too. With --proxy the connections
// a guard allows go on through that upstream proxy.
//
// The proxies never read, log or keep a body. Classification never
// relies on them: they see only the host and port of a TLS connection.
package egress

import (
	"bufio"
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"net/http/httputil"
	"net/netip"
	"net/url"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"
	"time"

	"github.com/RhythmShahriar/rhythm/kit/httpx"

	"github.com/RhythmShahriar/echo/consent"
	"github.com/RhythmShahriar/echo/mask"
)

// The reasons a proxy refuses a request, as consent.Request.Refused
// spells them.
const (
	ReasonPrivate   = "private address"             // loopback, link-local, private or metadata, for a target that isn't one
	ReasonBrowser   = "browser traffic"             // Chrome's own: outside any run, or a browser service host
	ReasonNotMapped = "not mapped"                  // --map-only and no --map-host covers the host
	ReasonMapped    = "mapped host outside the tab" // a --map-host host never goes to its real address
	ReasonLimit     = "limit"                       // too many open tunnels
)

// The defaults Start uses for a zero Options field.
const (
	DefaultMaxTunnels  = 256
	DefaultIdleTimeout = 30 * time.Second
)

// maxReasons bounds how many refused host:port pairs a proxy remembers
// for Reason, so a page can't grow the map without end.
const maxReasons = 4096

// metadataHosts are the cloud metadata services, refused for every
// target: a loopback or private target may reach its own network, never
// the machine's cloud credentials.
var metadataHosts = map[string]bool{
	"169.254.169.254":          true,
	"fd00:ec2::254":            true,
	"metadata.google.internal": true,
}

// IsMetadata reports whether host is a cloud metadata address or name,
// which a guard refuses for every target, ignoring case, brackets, a
// trailing dot and an IPv4 address written as IPv6.
func IsMetadata(host string) bool {
	host = strings.TrimSuffix(strings.ToLower(strings.Trim(host, "[]")), ".")
	if ip, err := netip.ParseAddr(host); err == nil {
		host = ip.Unmap().String()
	}
	return metadataHosts[host]
}

// BrowserServiceHosts are the hosts Chrome's own services call
// (updates, Safe Browsing, optimization hints, autofill). A page never
// needs them, so a guard refuses them and counts them as browser
// traffic.
var BrowserServiceHosts = []string{
	"clients2.google.com",
	"update.googleapis.com",
	"redirector.gvt1.com",
	"android.clients.google.com",
	"safebrowsing.googleapis.com",
	"optimizationguide-pa.googleapis.com",
	"content-autofill.googleapis.com",
}

// Options configure a guard.
type Options struct {
	// Target is the checked page's host. Loopback, link-local and
	// private destinations are allowed only when it resolves to the
	// same kind; "" allows public destinations only.
	Target string
	// MapHosts are the --map-host patterns (exact or *.suffix); with
	// MapOnly every other host is refused.
	MapHosts []string
	MapOnly  bool
	// Upstream is the --proxy URL (http://user:pass@host:port) allowed
	// connections go on through; "" dials directly.
	Upstream string
	// Log receives one line per refusal and the upstream in use, with
	// credentials masked; nil logs nothing.
	Log func(string)
	// MaxTunnels and IdleTimeout bound the open CONNECT tunnels; zero
	// means DefaultMaxTunnels and DefaultIdleTimeout.
	MaxTunnels  int
	IdleTimeout time.Duration
}

// Stats count what a proxy refused.
type Stats struct {
	Refused         int // refused page traffic: private addresses, not mapped, the tunnel limit
	BrowserInternal int // Chrome's own traffic
}

// Add returns the sum of s and o.
func (s Stats) Add(o Stats) Stats {
	return Stats{Refused: s.Refused + o.Refused, BrowserInternal: s.BrowserInternal + o.BrowserInternal}
}

// Proxy is one running listener.
type Proxy struct {
	opts      Options
	refuseAll bool
	guard     *httpx.Guard
	upstream  *url.URL
	ln        net.Listener
	srv       *http.Server
	fwd       *httputil.ReverseProxy
	sem       chan struct{}

	refused, internal atomic.Int64

	mu      sync.Mutex
	reasons map[string]string // host:port -> reason
	dialErr map[string]string // host:port -> why the last dial failed, masked
	tunnels map[net.Conn]struct{}
	closed  bool
}

// StartRefuseAll starts the browser-wide listener: it refuses every
// request with 403 and counts it as browser traffic.
func StartRefuseAll(logf func(string)) (*Proxy, error) {
	return start(Options{Log: logf}, true)
}

// Start starts a guard for opts.Target.
func Start(opts Options) (*Proxy, error) {
	return start(opts, false)
}

func start(opts Options, refuseAll bool) (*Proxy, error) {
	if opts.MaxTunnels <= 0 {
		opts.MaxTunnels = DefaultMaxTunnels
	}
	if opts.IdleTimeout <= 0 {
		opts.IdleTimeout = DefaultIdleTimeout
	}
	p := &Proxy{opts: opts, refuseAll: refuseAll, reasons: map[string]string{}, dialErr: map[string]string{}, tunnels: map[net.Conn]struct{}{},
		sem: make(chan struct{}, opts.MaxTunnels)}
	if !refuseAll {
		p.guard = httpx.NewGuard(opts.Target)
	}
	if opts.Upstream != "" {
		u, err := url.Parse(opts.Upstream)
		if err != nil || u.Scheme != "http" || u.Host == "" {
			return nil, fmt.Errorf("--proxy %s: want http://host:port", maskProxy(opts.Upstream))
		}
		p.upstream = u
		p.logf("egress: allowed connections go on through %s", maskProxy(u.String()))
	}
	p.fwd = &httputil.ReverseProxy{
		Rewrite:      func(r *httputil.ProxyRequest) { r.Out.Host = r.In.Host },
		Transport:    p.transport(),
		ErrorLog:     log.New(io.Discard, "", 0), // its lines carry URLs
		ErrorHandler: p.forwardError,
	}
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return nil, err
	}
	p.ln = ln
	p.srv = &http.Server{
		Handler:           p,
		ReadHeaderTimeout: 10 * time.Second,
		IdleTimeout:       opts.IdleTimeout,
		ErrorLog:          log.New(io.Discard, "", 0),
	}
	go func() { _ = p.srv.Serve(ln) }()
	return p, nil
}

// Addr is the listener's address, 127.0.0.1:port.
func (p *Proxy) Addr() string { return p.ln.Addr().String() }

// URL is the proxy as Chrome takes it: http://127.0.0.1:port.
func (p *Proxy) URL() string { return "http://" + p.Addr() }

// Stats returns the counts so far.
func (p *Proxy) Stats() Stats {
	return Stats{Refused: int(p.refused.Load()), BrowserInternal: int(p.internal.Load())}
}

// Reason says why the proxy refused host:port, or "" when it didn't,
// so the recorder can mark a request that failed at the proxy.
func (p *Proxy) Reason(host, port string) string {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.reasons[net.JoinHostPort(strings.ToLower(host), port)]
}

// DialError says why the proxy last failed to connect to host:port
// ("dial tcp 127.0.0.1:443: connect: connection refused", masked), or ""
// when it never did. Chrome only sees a failed tunnel or a 502; this is
// the reason a check names when the site didn't answer.
func (p *Proxy) DialError(host, port string) string {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.dialErr[net.JoinHostPort(strings.ToLower(host), port)]
}

// noteDialError remembers why a connection to host:port failed, for at
// most maxReasons pairs.
func (p *Proxy) noteDialError(host, port string, err error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	key := net.JoinHostPort(strings.ToLower(host), port)
	if _, ok := p.dialErr[key]; ok || len(p.dialErr) < maxReasons {
		p.dialErr[key] = consent.MaskURL(err.Error(), false)
	}
}

// Close stops the listener and closes every open tunnel.
func (p *Proxy) Close() error {
	p.mu.Lock()
	p.closed = true
	for c := range p.tunnels {
		_ = c.Close()
	}
	p.mu.Unlock()
	return p.srv.Close()
}

// MatchHost reports whether host is covered by one of patterns: an
// exact host or *.suffix (any subdomain of suffix, not suffix itself),
// ignoring case and a trailing dot.
func MatchHost(patterns []string, host string) bool {
	host = strings.TrimSuffix(strings.ToLower(host), ".")
	for _, pat := range patterns {
		pat = strings.TrimSuffix(strings.ToLower(strings.TrimSpace(pat)), ".")
		if suffix, ok := strings.CutPrefix(pat, "*."); ok {
			if strings.HasSuffix(host, "."+suffix) {
				return true
			}
		} else if pat != "" && host == pat {
			return true
		}
	}
	return false
}

// ServeHTTP answers one proxy request: CONNECT or absolute-form HTTP.
func (p *Proxy) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	var host, port string
	switch {
	case r.Method == http.MethodConnect:
		h, pt, err := net.SplitHostPort(r.Host)
		if err != nil {
			http.Error(w, "CONNECT wants host:port", http.StatusBadRequest)
			return
		}
		host, port = h, pt
	case r.URL.IsAbs() && r.URL.Scheme == "http" && r.URL.Host != "":
		host, port = r.URL.Hostname(), r.URL.Port()
		if port == "" {
			port = "80"
		}
	default:
		http.Error(w, "this is a proxy: send CONNECT or an absolute http URL", http.StatusBadRequest)
		return
	}
	host = strings.ToLower(strings.TrimSuffix(host, "."))
	if reason := p.check(r.Context(), host); reason != "" {
		p.refuse(w, r.Method, host, port, reason)
		return
	}
	if r.Method == http.MethodConnect {
		p.tunnel(w, r, host, port)
		return
	}
	p.fwd.ServeHTTP(w, r)
}

// check returns why host may not be reached, or "" when it may (the
// dial checks a name's resolved addresses again).
func (p *Proxy) check(ctx context.Context, host string) string {
	switch {
	case p.refuseAll:
		return ReasonBrowser
	case IsBrowserService(host):
		return ReasonBrowser
	case metadataHosts[host]:
		return ReasonPrivate
	case MatchHost(p.opts.MapHosts, host):
		// The tab answers a mapped host from its mapped address; one
		// that got past it (a request Fetch never paused) must not
		// reach the real host the mapping stands in for.
		return ReasonMapped
	case p.opts.MapOnly && !MatchHost(p.opts.MapHosts, host):
		return ReasonNotMapped
	}
	if ip, err := netip.ParseAddr(host); err == nil {
		if metadataHosts[ip.Unmap().String()] || !p.guard.Allow(ctx, ip) {
			return ReasonPrivate
		}
		return ""
	}
	if p.upstream != nil {
		// The upstream resolves the name itself; refuse a name that
		// resolves here to an address the guard doesn't allow, so a
		// rebinding name can't reach this machine's network through it.
		for _, ip := range httpx.Resolve(ctx, host) {
			if metadataHosts[ip.Unmap().String()] || !p.guard.Allow(ctx, ip) {
				return ReasonPrivate
			}
		}
	}
	return ""
}

// IsBrowserService reports whether host is one of BrowserServiceHosts,
// ignoring case and a trailing dot.
func IsBrowserService(host string) bool {
	host = strings.TrimSuffix(strings.ToLower(host), ".")
	for _, h := range BrowserServiceHosts {
		if host == h {
			return true
		}
	}
	return false
}

// refuse answers 403, counts the refusal and remembers its reason.
func (p *Proxy) refuse(w http.ResponseWriter, method, host, port, reason string) {
	p.count(host, port, reason)
	p.logf("egress: refused %s %s (%s)", method, net.JoinHostPort(host, port), reason)
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	w.Header().Set("X-Rhythm-Echo-Refused", reason)
	w.WriteHeader(http.StatusForbidden)
	_, _ = io.WriteString(w, "refused by rhythm-echo consent: "+reason+"\n")
}

func (p *Proxy) count(host, port, reason string) {
	if reason == ReasonBrowser {
		p.internal.Add(1)
	} else {
		p.refused.Add(1)
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	key := net.JoinHostPort(host, port)
	if _, ok := p.reasons[key]; ok || len(p.reasons) < maxReasons {
		p.reasons[key] = reason
	}
}

func (p *Proxy) logf(format string, args ...any) {
	if p.opts.Log != nil {
		p.opts.Log(fmt.Sprintf(format, args...))
	}
}

// dial connects to host:port directly through the address guard, or
// through the upstream proxy.
func (p *Proxy) dial(ctx context.Context, host, port string) (net.Conn, error) {
	addr := net.JoinHostPort(host, port)
	if p.upstream == nil {
		return p.guardDial(ctx, "tcp", addr)
	}
	return dialUpstream(ctx, p.upstream, addr)
}

// guardDial connects like the kit guard's DialContext and also refuses
// the metadata addresses: its check runs after DNS on every address the
// dialer tries, so a name that resolves to 169.254.169.254 is refused
// even for a link-local target the kit guard would let through.
func (p *Proxy) guardDial(ctx context.Context, network, addr string) (net.Conn, error) {
	d := net.Dialer{Timeout: 30 * time.Second, KeepAlive: 30 * time.Second}
	d.ControlContext = func(cctx context.Context, _, address string, _ syscall.RawConn) error {
		host, _, err := net.SplitHostPort(address)
		if err != nil {
			return err
		}
		ip, err := netip.ParseAddr(host)
		if err != nil {
			return err
		}
		if metadataHosts[ip.Unmap().String()] || !p.guard.Allow(cctx, ip) {
			return fmt.Errorf("refusing to connect to %s: %w", ip, httpx.ErrPrivate)
		}
		return nil
	}
	return d.DialContext(ctx, network, addr)
}

// tunnel connects a CONNECT request and copies bytes both ways until
// either side closes or the tunnel idles for IdleTimeout.
func (p *Proxy) tunnel(w http.ResponseWriter, r *http.Request, host, port string) {
	select {
	case p.sem <- struct{}{}:
	default:
		p.refuseLimit(w, host, port)
		return
	}
	defer func() { <-p.sem }()
	ctx, cancel := context.WithTimeout(r.Context(), 30*time.Second)
	upstream, err := p.dial(ctx, host, port)
	cancel()
	if err != nil {
		if errors.Is(err, httpx.ErrPrivate) {
			p.refuse(w, r.Method, host, port, ReasonPrivate)
			return
		}
		p.noteDialError(host, port, err)
		http.Error(w, "can't connect", http.StatusBadGateway)
		return
	}
	hj, ok := w.(http.Hijacker)
	if !ok {
		_ = upstream.Close()
		http.Error(w, "can't tunnel", http.StatusInternalServerError)
		return
	}
	client, buf, err := hj.Hijack()
	if err != nil {
		_ = upstream.Close()
		return
	}
	if !p.track(client, upstream) {
		_ = client.Close()
		_ = upstream.Close()
		return
	}
	defer p.untrack(client, upstream)
	if _, err := io.WriteString(client, "HTTP/1.1 200 Connection established\r\n\r\n"); err != nil {
		return
	}
	idle := p.opts.IdleTimeout
	c, u := &idleConn{Conn: client, idle: idle}, &idleConn{Conn: upstream, idle: idle}
	done := make(chan struct{}, 2)
	go func() {
		// Bytes the client sent after its CONNECT line sit in buf.
		if n := buf.Reader.Buffered(); n > 0 {
			b, _ := buf.Peek(n)
			if _, err := u.Write(b); err != nil {
				done <- struct{}{}
				return
			}
		}
		_, _ = io.Copy(u, c)
		done <- struct{}{}
	}()
	go func() { _, _ = io.Copy(c, u); done <- struct{}{} }()
	<-done
}

// refuseLimit answers a CONNECT past MaxTunnels.
func (p *Proxy) refuseLimit(w http.ResponseWriter, host, port string) {
	p.count(host, port, ReasonLimit)
	p.logf("egress: refused CONNECT %s (%s)", net.JoinHostPort(host, port), ReasonLimit)
	http.Error(w, "too many open tunnels", http.StatusServiceUnavailable)
}

func (p *Proxy) track(conns ...net.Conn) bool {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.closed {
		return false
	}
	for _, c := range conns {
		p.tunnels[c] = struct{}{}
	}
	return true
}

func (p *Proxy) untrack(conns ...net.Conn) {
	p.mu.Lock()
	defer p.mu.Unlock()
	for _, c := range conns {
		_ = c.Close()
		delete(p.tunnels, c)
	}
}

// transport forwards absolute-form HTTP requests: through the guard's
// dialer, or as absolute-form requests to the upstream proxy. It never
// follows redirects (the browser does) and leaves encodings alone.
func (p *Proxy) transport() *http.Transport {
	t := &http.Transport{
		DisableCompression:    true,
		IdleConnTimeout:       p.opts.IdleTimeout,
		TLSHandshakeTimeout:   30 * time.Second,
		ResponseHeaderTimeout: 0, // long polls are a page's business
		MaxIdleConnsPerHost:   8,
	}
	if p.upstream != nil {
		t.Proxy = http.ProxyURL(p.upstream)
		t.DialContext = (&net.Dialer{Timeout: 30 * time.Second, KeepAlive: 30 * time.Second}).DialContext
	} else if p.guard != nil {
		t.DialContext = p.guardDial
	}
	return t
}

// forwardError turns a failed forward into 403 for a refused address
// and 502 for anything else.
func (p *Proxy) forwardError(w http.ResponseWriter, r *http.Request, err error) {
	if errors.Is(err, httpx.ErrPrivate) {
		port := r.URL.Port()
		if port == "" {
			port = "80"
		}
		p.refuse(w, r.Method, strings.ToLower(r.URL.Hostname()), port, ReasonPrivate)
		return
	}
	port := r.URL.Port()
	if port == "" {
		port = "80"
	}
	p.noteDialError(r.URL.Hostname(), port, err)
	http.Error(w, "can't connect", http.StatusBadGateway)
}

// maskProxy hides the login in a --proxy URL, even one too broken to
// parse; the rest is masked as any URL.
func maskProxy(raw string) string {
	if scheme, rest, ok := strings.Cut(raw, "://"); ok {
		if i := strings.LastIndex(rest, "@"); i >= 0 {
			raw = scheme + "://" + rest[i+1:]
			return scheme + "://" + mask.Hidden + "@" + strings.TrimPrefix(consent.MaskURL(raw, false), scheme+"://")
		}
	}
	return consent.MaskURL(raw, false)
}

// dialUpstream opens a tunnel to addr through an HTTP proxy, with Basic
// credentials from its URL.
func dialUpstream(ctx context.Context, proxy *url.URL, addr string) (net.Conn, error) {
	d := net.Dialer{Timeout: 30 * time.Second, KeepAlive: 30 * time.Second}
	hostport := proxy.Host
	if proxy.Port() == "" {
		hostport = net.JoinHostPort(proxy.Hostname(), "80")
	}
	conn, err := d.DialContext(ctx, "tcp", hostport)
	if err != nil {
		return nil, err
	}
	if dl, ok := ctx.Deadline(); ok {
		_ = conn.SetDeadline(dl)
	}
	req := "CONNECT " + addr + " HTTP/1.1\r\nHost: " + addr + "\r\n"
	if u := proxy.User; u != nil {
		pass, _ := u.Password()
		req += "Proxy-Authorization: Basic " + base64.StdEncoding.EncodeToString([]byte(u.Username()+":"+pass)) + "\r\n"
	}
	req += "\r\n"
	if _, err := io.WriteString(conn, req); err != nil {
		_ = conn.Close()
		return nil, err
	}
	br := bufio.NewReader(conn)
	resp, err := http.ReadResponse(br, &http.Request{Method: http.MethodConnect})
	if err != nil {
		_ = conn.Close()
		return nil, err
	}
	_ = resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		_ = conn.Close()
		return nil, fmt.Errorf("the --proxy answered %s to CONNECT %s", resp.Status, addr)
	}
	_ = conn.SetDeadline(time.Time{})
	if br.Buffered() > 0 {
		return &bufferedConn{Conn: conn, r: br}, nil
	}
	return conn, nil
}

// bufferedConn reads what the upstream sent along with its CONNECT
// answer before reading the connection.
type bufferedConn struct {
	net.Conn
	r *bufio.Reader
}

func (c *bufferedConn) Read(b []byte) (int, error) { return c.r.Read(b) }

// idleConn closes when no byte moves for idle: each read and write
// pushes the deadline on.
type idleConn struct {
	net.Conn
	idle time.Duration
}

func (c *idleConn) Read(b []byte) (int, error) {
	_ = c.SetDeadline(time.Now().Add(c.idle))
	return c.Conn.Read(b)
}

func (c *idleConn) Write(b []byte) (int, error) {
	_ = c.SetDeadline(time.Now().Add(c.idle))
	return c.Conn.Write(b)
}
