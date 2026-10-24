// Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
// SPDX-License-Identifier: MIT

// Package proxy is Echo's HTTP(S) proxy: the bridge for apps and SDKs
// that send without going through a connector's hooks (the Twilio and
// Stripe PHP SDKs call cURL directly, Python and Go read HTTPS_PROXY).
// Every request it sees goes through catcher.Server.Capture, so a proxied
// request is recorded, faked or let through exactly like one a connector
// reported.
//
// HTTPS is intercepted with a local CA (see LoadCA) when MITM is on;
// clients must trust its certificate. Interception speaks HTTP/1.1 only:
// the proxy offers just "http/1.1" in ALPN and clients fall back to it.
// Loopback and private addresses, single-label names that resolve only to
// them (Docker services, the site calling itself) and Allow globs go
// through unchanged, because the site's own host can't be known here.
// Each of those requests is still recorded as sent. Cloud metadata
// addresses never go through while sealed.
//
// This is best effort. The guarantee that nothing leaves a sealed site is
// network egress control with Echo as the only way out (docs/seal).
package proxy

import (
	"bufio"
	"bytes"
	"compress/flate"
	"compress/gzip"
	"context"
	"crypto/subtle"
	"crypto/tls"
	"encoding/base64"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/url"
	"path"
	"strings"
	"sync"
	"time"

	"github.com/RhythmShahriar/echo/catcher"
	"github.com/RhythmShahriar/echo/model"
	"github.com/RhythmShahriar/echo/recognize"
	"github.com/RhythmShahriar/echo/wire"
)

// Defaults for Options.
const (
	DefaultMaxBody     = 32 << 20  // request bodies larger than this are refused
	DefaultCaptureBody = 256 << 10 // bytes of each response kept for the capture
)

// via marks requests the proxy forwards, so a request routed back into
// the proxy is refused instead of looping.
const via = "1.1 rhythm-echo"

// Options configure a Proxy.
type Options struct {
	Server *catcher.Server // where captures go (required)
	CA     *CA             // signs leaves for intercepted hosts (required when MITM)
	MITM   bool            // intercept HTTPS; without it, sealed HTTPS to unknown hosts is refused
	Allow  []string        // host globs passed through untouched, on top of loopback and private ranges
	Token  string          // when set, clients must send it in Proxy-Authorization (Basic password or Bearer)
	Log    *slog.Logger    // nil discards

	Resolver Resolver // checks single-label names; default net.DefaultResolver

	MaxBody     int64                                                             // request body limit; default DefaultMaxBody
	CaptureBody int                                                               // response bytes kept per capture; default DefaultCaptureBody
	Transport   http.RoundTripper                                                 // forwards allowed requests; default: a direct transport that ignores proxy env vars and dials checked addresses
	Dial        func(ctx context.Context, network, addr string) (net.Conn, error) // opens tunnels; default net.Dialer, refusing names that resolve to a metadata endpoint while sealed
}

// Resolver resolves host names. *net.Resolver satisfies it.
type Resolver interface {
	LookupHost(ctx context.Context, host string) ([]string, error)
}

// Proxy is an http.Handler that speaks the forward-proxy protocol.
type Proxy struct {
	opt   Options
	outer *http.Server // serves proxy clients (Serve)
	inner *http.Server // serves requests decrypted from intercepted tunnels
	conns *connListener
	once  sync.Once

	// Tunnels are hijacked, so neither server can close them: they're
	// kept here for Shutdown and Close.
	mu       sync.Mutex
	hijacked map[net.Conn]struct{}
	closed   bool
}

type targetKey struct{}

// New returns a proxy. Call Shutdown or Close to stop it.
func New(opt Options) (*Proxy, error) {
	if opt.Server == nil {
		return nil, errors.New("proxy: Options.Server is required")
	}
	if opt.MITM && opt.CA == nil {
		return nil, errors.New("proxy: MITM needs a CA (proxy.LoadCA)")
	}
	if opt.Log == nil {
		opt.Log = slog.New(slog.DiscardHandler)
	}
	if opt.MaxBody <= 0 {
		opt.MaxBody = DefaultMaxBody
	}
	if opt.CaptureBody <= 0 {
		opt.CaptureBody = DefaultCaptureBody
	}
	if opt.Resolver == nil {
		opt.Resolver = net.DefaultResolver
	}
	p := &Proxy{opt: opt, conns: newConnListener(), hijacked: map[net.Conn]struct{}{}}
	if p.opt.Dial == nil {
		d := &net.Dialer{Timeout: 15 * time.Second, KeepAlive: 30 * time.Second}
		p.opt.Dial = p.sealedDial(d.DialContext)
	}
	if p.opt.Transport == nil {
		p.opt.Transport = &http.Transport{
			Proxy:                 nil, // never through HTTPS_PROXY: that may be us
			DialContext:           p.dial,
			TLSHandshakeTimeout:   15 * time.Second,
			ResponseHeaderTimeout: 60 * time.Second,
			IdleConnTimeout:       90 * time.Second,
			MaxIdleConnsPerHost:   8,
			ForceAttemptHTTP2:     true,
		}
	}
	p.outer = &http.Server{
		Handler:           p,
		ReadHeaderTimeout: 30 * time.Second,
		IdleTimeout:       90 * time.Second,
		ErrorLog:          slog.NewLogLogger(opt.Log.Handler(), slog.LevelDebug),
	}
	p.inner = &http.Server{
		Handler:           http.HandlerFunc(p.serveIntercepted),
		ReadHeaderTimeout: 30 * time.Second,
		IdleTimeout:       90 * time.Second,
		ErrorLog:          slog.NewLogLogger(opt.Log.Handler(), slog.LevelDebug),
		ConnContext: func(ctx context.Context, c net.Conn) context.Context {
			if mc, ok := c.(*mitmConn); ok {
				return context.WithValue(ctx, targetKey{}, mc.target)
			}
			return ctx
		},
	}
	return p, nil
}

// Shutdown stops the proxy gracefully: it stops accepting clients, waits
// (until ctx is done) for requests in flight, then closes open tunnels.
// When ctx ends first it closes everything at once, like Close.
func (p *Proxy) Shutdown(ctx context.Context) error {
	err := p.outer.Shutdown(ctx)
	_ = p.conns.Close()
	err = errors.Join(err, p.inner.Shutdown(ctx))
	p.closeHijacked()
	if err != nil {
		_ = p.Close()
	}
	return err
}

// Close stops the proxy at once: listeners, requests in flight, tunnels.
func (p *Proxy) Close() error {
	err := p.outer.Close()
	_ = p.conns.Close()
	err = errors.Join(err, p.inner.Close())
	p.closeHijacked()
	return err
}

// track keeps a hijacked connection for Shutdown and Close; it reports
// false (and closes c) once the proxy is stopping.
func (p *Proxy) track(c net.Conn) bool {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.closed {
		_ = c.Close()
		return false
	}
	p.hijacked[c] = struct{}{}
	return true
}

func (p *Proxy) untrack(c net.Conn) {
	p.mu.Lock()
	delete(p.hijacked, c)
	p.mu.Unlock()
}

func (p *Proxy) closeHijacked() {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.closed = true
	for c := range p.hijacked {
		_ = c.Close()
	}
	clear(p.hijacked)
}

// ListenAndServe listens on addr. Beyond loopback it insists on a token,
// so the proxy is never an open relay.
func (p *Proxy) ListenAndServe(addr string) error {
	if !IsLoopbackAddr(addr) && p.opt.Token == "" {
		return fmt.Errorf("proxy: listening on %s (beyond loopback) needs a token", addr)
	}
	l, err := net.Listen("tcp", addr)
	if err != nil {
		return err
	}
	return p.Serve(l)
}

// Serve accepts proxy clients on l until Shutdown or Close. It then
// returns http.ErrServerClosed.
func (p *Proxy) Serve(l net.Listener) error {
	return p.outer.Serve(l)
}

func (p *Proxy) startInner() {
	p.once.Do(func() {
		go func() { _ = p.inner.Serve(p.conns) }()
	})
}

// ServeHTTP handles CONNECT, absolute-form requests and GET /ca.pem so
// containers can fetch the CA from the proxy itself.
func (p *Proxy) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.Method == http.MethodGet && !r.URL.IsAbs() && r.URL.Path == "/ca.pem" && p.opt.CA != nil {
		// Public by design: it's a certificate and containers fetch it
		// before they have anything else.
		w.Header().Set("Content-Type", "application/x-pem-file")
		_, _ = w.Write(p.opt.CA.PEM)
		return
	}
	if !p.authorized(r) {
		w.Header().Set("Proxy-Authenticate", `Basic realm="rhythm-echo"`)
		http.Error(w, "rhythm-echo proxy: send the Echo token in Proxy-Authorization", http.StatusProxyAuthRequired)
		return
	}
	switch {
	case r.Method == http.MethodConnect:
		p.connect(w, r)
	case r.URL.IsAbs():
		if strings.Contains(r.Header.Get("Via"), "rhythm-echo") {
			http.Error(w, "rhythm-echo proxy: request loops back into the proxy", http.StatusLoopDetected)
			return
		}
		scheme := r.URL.Scheme
		if scheme != "http" && scheme != "https" {
			http.Error(w, "rhythm-echo proxy: only http and https", http.StatusBadRequest)
			return
		}
		p.handle(w, r, scheme, r.URL.Host, "http")
	default:
		http.Error(w, "rhythm-echo proxy: set HTTPS_PROXY/HTTP_PROXY to this address; GET /ca.pem for the CA", http.StatusBadRequest)
	}
}

func (p *Proxy) authorized(r *http.Request) bool {
	if p.opt.Token == "" {
		return true
	}
	h := r.Header.Get("Proxy-Authorization")
	got := ""
	switch scheme, cred, _ := strings.Cut(h, " "); strings.ToLower(scheme) {
	case "bearer":
		got = cred
	case "basic":
		if b, err := base64.StdEncoding.DecodeString(cred); err == nil {
			_, got, _ = strings.Cut(string(b), ":")
		}
	}
	return subtle.ConstantTimeCompare([]byte(got), []byte(p.opt.Token)) == 1
}

// allowed reports whether host goes through unchanged: loopback and
// private (RFC 1918, ULA) addresses, localhost, the Allow globs and
// single-label names (Docker services, the site calling itself) whose
// every address is loopback or private. Such a name is resolved here, once:
// a resolv.conf search domain can turn "api" into a public host. The
// returned context pins the name to the checked addresses, so the dial
// can't resolve it differently. While sealed, cloud metadata addresses
// are never allowed, even by a glob.
func (p *Proxy) allowed(ctx context.Context, host string) (bool, context.Context) {
	host = strings.ToLower(strings.TrimSuffix(host, "."))
	if p.sealed() && metadata(host) {
		return false, ctx
	}
	for _, a := range p.opt.Allow {
		if ok, _ := path.Match(strings.ToLower(a), host); ok {
			return true, ctx
		}
	}
	if ip := net.ParseIP(host); ip != nil {
		return ip.IsLoopback() || ip.IsPrivate(), ctx
	}
	if host == "localhost" || strings.HasSuffix(host, ".localhost") {
		return true, ctx
	}
	if host == "" || strings.Contains(host, ".") {
		return false, ctx
	}
	addrs, err := p.opt.Resolver.LookupHost(ctx, host)
	if err != nil || len(addrs) == 0 {
		return false, ctx
	}
	for _, a := range addrs {
		ip := net.ParseIP(a)
		if ip == nil || (!ip.IsLoopback() && !ip.IsPrivate()) || (p.sealed() && metadata(ip.String())) {
			p.opt.Log.Debug("single-label name resolves beyond private ranges", "host", host, "addr", a)
			return false, ctx
		}
	}
	return true, context.WithValue(ctx, pinKey{}, pin{host: host, addrs: addrs})
}

func (p *Proxy) sealed() bool { return p.opt.Server.Mode() == catcher.Seal }

// metadata reports whether host is a cloud metadata or credentials
// endpoint (catcher.Metadata): all of 169.254.0.0/16, Alibaba's
// 100.100.100.200, AWS's fd00:ec2::254 and the clouds' names for them. A
// sealed site must never read cloud credentials from it. Any other name
// can resolve there too: sealedDial catches those.
func metadata(host string) bool { return catcher.Metadata(host) }

// errMetadata refuses a dial that would reach a metadata endpoint.
var errMetadata = errors.New("refused: the name resolves to a cloud metadata endpoint, which a sealed site never reaches")

// sealedDial wraps the default dialer. While sealed it resolves a name
// itself and refuses it when any address is a metadata endpoint, then
// dials the checked addresses so a second lookup can't answer otherwise.
// A glob such as "*" lets any name through allowed. Any name (an
// attacker's own among them) can point at 169.254.169.254.
func (p *Proxy) sealedDial(dial func(ctx context.Context, network, addr string) (net.Conn, error)) func(ctx context.Context, network, addr string) (net.Conn, error) {
	return func(ctx context.Context, network, addr string) (net.Conn, error) {
		host, port, err := net.SplitHostPort(addr)
		if err != nil || !p.sealed() {
			return dial(ctx, network, addr)
		}
		if metadata(host) {
			return nil, &net.OpError{Op: "dial", Net: network, Err: errMetadata}
		}
		if net.ParseIP(host) != nil {
			return dial(ctx, network, addr)
		}
		addrs, err := p.opt.Resolver.LookupHost(ctx, host)
		if err != nil {
			return nil, err
		}
		for _, a := range addrs {
			if metadata(a) {
				p.opt.Log.Warn("refused a name that resolves to the metadata endpoint", "host", host, "addr", a)
				return nil, &net.OpError{Op: "dial", Net: network, Err: errMetadata}
			}
		}
		var errs []error
		for _, a := range addrs {
			c, err := dial(ctx, network, net.JoinHostPort(a, port))
			if err == nil {
				return c, nil
			}
			errs = append(errs, err)
		}
		return nil, errors.Join(errs...)
	}
}

type pinKey struct{}

// pin is a name allowed for the addresses it was checked against.
type pin struct {
	host  string
	addrs []string
}

// dial opens a connection through Options.Dial, to the checked addresses
// when allowed pinned the host.
func (p *Proxy) dial(ctx context.Context, network, addr string) (net.Conn, error) {
	pn, ok := ctx.Value(pinKey{}).(pin)
	host, port, err := net.SplitHostPort(addr)
	if !ok || err != nil || !strings.EqualFold(strings.TrimSuffix(host, "."), pn.host) {
		return p.opt.Dial(ctx, network, addr)
	}
	var errs []error
	for _, a := range pn.addrs {
		c, err := p.opt.Dial(ctx, network, net.JoinHostPort(a, port))
		if err == nil {
			return c, nil
		}
		errs = append(errs, err)
	}
	return nil, errors.Join(errs...)
}

// connect handles CONNECT host:port: a blind tunnel for allowed hosts (and
// for everything when observing without MITM), interception when MITM is
// on and a refusal when sealed without MITM. Payment APIs are never
// tunneled blind, in any mode: they're intercepted with MITM, refused
// without it.
func (p *Proxy) connect(w http.ResponseWriter, r *http.Request) {
	target := r.Host
	if _, _, err := net.SplitHostPort(target); err != nil {
		target = net.JoinHostPort(target, "443")
	}
	host, _, _ := net.SplitHostPort(target)
	run := runOf(r)
	pass, ctx := p.allowed(r.Context(), host)
	r = r.WithContext(ctx)
	switch {
	case blindPayment(host) && p.opt.MITM:
		// Even when allowlisted: only inside can test and live keys be told apart.
		p.intercept(w, r, target, host)
	case blindPayment(host):
		d, _ := p.opt.Server.Capture(wire.Capture{Version: wire.Version, Kind: "http", Phase: wire.Before, Run: run,
			Source: source("connect"), HTTP: &wire.HTTP{Method: http.MethodConnect, URL: tunnelURL(target)}})
		why := tunnelRefusal(host)
		p.markBlocked(d.ID, why)
		p.opt.Log.Warn("refused a tunnel", "host", host, "reason", why)
		http.Error(w, "rhythm-echo: "+host+": "+why, http.StatusForbidden)
	case pass || (!p.opt.MITM && !p.sealed()):
		p.tunnel(w, r, target, "", run)
	case p.opt.MITM:
		p.intercept(w, r, target, host)
	default:
		// Sealed and can't see inside: ask the catcher (its allowlist may
		// let the host through), else refuse and record it as blocked.
		d, err := p.opt.Server.Capture(wire.Capture{Version: wire.Version, Kind: "http", Phase: wire.Before, Run: run,
			Source: source("connect"), HTTP: &wire.HTTP{Method: http.MethodConnect, URL: tunnelURL(target)}})
		if err != nil {
			http.Error(w, "rhythm-echo proxy: "+err.Error(), http.StatusBadGateway)
			return
		}
		if d.Action == wire.Allow && !metadata(host) {
			p.tunnel(w, r, target, d.ID, run)
			return
		}
		p.markBlocked(d.ID, "sealed: HTTPS to "+host+" refused (run the proxy with MITM to fake it)")
		http.Error(w, "rhythm-echo: "+host+" is sealed; HTTPS can't be faked without MITM", http.StatusForbidden)
	}
}

// tunnel copies bytes both ways and records the host (not the content).
func (p *Proxy) tunnel(w http.ResponseWriter, r *http.Request, target, id, run string) {
	up, err := p.dial(r.Context(), "tcp", target)
	host, _, _ := net.SplitHostPort(target)
	rec := func(status int, errMsg string) {
		_, _ = p.opt.Server.Capture(wire.Capture{Version: wire.Version, Kind: "http", Phase: wire.After, ID: id, Run: run,
			Source: source("tunnel"), Error: errMsg,
			HTTP: &wire.HTTP{Method: http.MethodConnect, URL: tunnelURL(target), Status: status}})
	}
	if err != nil {
		rec(http.StatusBadGateway, err.Error())
		http.Error(w, "rhythm-echo proxy: "+err.Error(), http.StatusBadGateway)
		return
	}
	if !p.track(up) {
		http.Error(w, "rhythm-echo proxy: shutting down", http.StatusServiceUnavailable)
		return
	}
	defer p.untrack(up)
	client, buf, err := p.hijack(w)
	if err != nil {
		_ = up.Close()
		return
	}
	defer p.untrack(client)
	rec(http.StatusOK, "")
	p.opt.Log.Debug("tunnel", "host", host)
	_, _ = io.WriteString(client, "HTTP/1.1 200 Connection established\r\n\r\n")
	done := make(chan struct{}, 2)
	go func() { _, _ = io.Copy(up, buf); closeWrite(up); done <- struct{}{} }()
	go func() { _, _ = io.Copy(client, up); closeWrite(client); done <- struct{}{} }()
	<-done
	<-done
	_ = up.Close()
	_ = client.Close()
}

// intercept terminates TLS with a leaf for host and hands the connection
// to the inner HTTP/1.1 server, which supports keep-alive.
func (p *Proxy) intercept(w http.ResponseWriter, r *http.Request, target, host string) {
	client, buf, err := p.hijack(w)
	if err != nil {
		return
	}
	// Tracked through the handshake only: the inner server owns it after.
	defer p.untrack(client)
	if _, err := io.WriteString(client, "HTTP/1.1 200 Connection established\r\n\r\n"); err != nil {
		_ = client.Close()
		return
	}
	cfg := &tls.Config{
		MinVersion: tls.VersionTLS12,
		NextProtos: []string{"http/1.1"},
		GetCertificate: func(hello *tls.ClientHelloInfo) (*tls.Certificate, error) {
			name := hello.ServerName
			if name == "" {
				name = host
			}
			return p.opt.CA.Leaf(name)
		},
	}
	tc := tls.Server(&bufConn{Conn: client, r: buf}, cfg)
	_ = client.SetDeadline(time.Now().Add(15 * time.Second))
	if err := tc.HandshakeContext(r.Context()); err != nil {
		// Usually the client doesn't trust the CA: say so where it can be seen.
		p.opt.Log.Warn("TLS handshake failed; does the client trust the Echo CA?", "host", host, "err", err)
		_ = tc.Close()
		return
	}
	_ = client.SetDeadline(time.Time{})
	p.startInner()
	if !p.conns.push(&mitmConn{Conn: tc, target: target}) {
		_ = tc.Close()
	}
}

func (p *Proxy) serveIntercepted(w http.ResponseWriter, r *http.Request) {
	target, _ := r.Context().Value(targetKey{}).(string)
	if target == "" {
		http.Error(w, "rhythm-echo proxy: lost the tunnel target", http.StatusInternalServerError)
		return
	}
	if h, port, err := net.SplitHostPort(target); err == nil && port == "443" {
		target = h
	}
	p.handle(w, r, "https", target, "mitm")
}

// handle records one request a client sent in the clear (plain HTTP, or
// decrypted from a tunnel) and fakes, blocks or forwards it as the
// catcher decides.
func (p *Proxy) handle(w http.ResponseWriter, r *http.Request, scheme, host, hook string) {
	u := &url.URL{Scheme: scheme, Host: host, Path: r.URL.Path, RawPath: r.URL.RawPath, RawQuery: r.URL.RawQuery}
	body, err := io.ReadAll(io.LimitReader(r.Body, p.opt.MaxBody+1))
	if err != nil {
		http.Error(w, "rhythm-echo proxy: reading request: "+err.Error(), http.StatusBadRequest)
		return
	}
	if int64(len(body)) > p.opt.MaxBody {
		http.Error(w, "rhythm-echo proxy: request body too large", http.StatusRequestEntityTooLarge)
		return
	}
	run := runOf(r)
	capt := &wire.HTTP{Method: r.Method, URL: u.String(), Headers: flatten(r.Header), Body: string(body)}
	// Allowed hosts go straight through, except a live payment call: the
	// catcher refuses those whatever the allowlist says.
	if pass, ctx := p.allowed(r.Context(), u.Hostname()); pass && !livePayment(r.Method, u, capt.Headers, body) {
		p.forward(w, r.WithContext(ctx), u, body, "", run, hook, capt)
		return
	}
	d, err := p.opt.Server.Capture(wire.Capture{Version: wire.Version, Kind: "http", Phase: wire.Before, Run: run,
		Source: source(hook), HTTP: capt})
	if err != nil {
		http.Error(w, "rhythm-echo proxy: "+err.Error(), http.StatusBadGateway)
		return
	}
	switch {
	case d.Action == wire.Allow && p.sealed() && metadata(u.Hostname()):
		// Allowlisted by a glob such as "*": still never reached.
		if p.opt.Server.Inbox != nil {
			p.opt.Server.Inbox.Update(d.ID, func(m *model.Message) {
				m.Status, m.Response = model.Faked, &model.Response{Status: http.StatusNotFound}
			})
		}
		writeFake(w, wire.Decision{ID: d.ID, Response: &wire.Response{Status: http.StatusNotFound}})
	case d.Action == wire.Allow:
		p.forward(w, r, u, body, d.ID, run, hook, capt)
	case d.Action == wire.Fake:
		writeFake(w, d)
	default:
		w.Header().Set("X-Echo-Id", d.ID)
		http.Error(w, "rhythm-echo: blocked "+u.Hostname()+" (sealed)", http.StatusForbidden)
	}
}

func writeFake(w http.ResponseWriter, d wire.Decision) {
	resp := d.Response
	if resp == nil {
		resp = &wire.Response{Status: http.StatusOK}
	}
	for k, v := range resp.Headers {
		w.Header().Set(k, v)
	}
	if w.Header().Get("Content-Type") == "" && resp.Body != "" {
		w.Header().Set("Content-Type", "application/json")
	}
	w.Header().Set("X-Echo-Id", d.ID)
	status := resp.Status
	if status == 0 {
		status = http.StatusOK
	}
	w.WriteHeader(status)
	_, _ = io.WriteString(w, resp.Body)
}

// forward sends the request for real, streams the answer back and
// records it in the after phase (id "" records a new message).
func (p *Proxy) forward(w http.ResponseWriter, r *http.Request, u *url.URL, body []byte, id, run, hook string, capt *wire.HTTP) {
	out, err := http.NewRequestWithContext(r.Context(), r.Method, u.String(), bytes.NewReader(body))
	if err != nil {
		http.Error(w, "rhythm-echo proxy: "+err.Error(), http.StatusBadRequest)
		return
	}
	out.Header = r.Header.Clone()
	removeHopHeaders(out.Header)
	out.Header.Del(wire.RunHeader)
	out.Header.Add("Via", via)
	out.Host = r.Host
	if hook == "mitm" || out.Host == "" {
		out.Host = u.Host
	}
	if len(body) == 0 {
		out.Body = http.NoBody
	}
	after := func(status int, response, errMsg string) {
		c := *capt
		c.Status, c.Response = status, response
		_, _ = p.opt.Server.Capture(wire.Capture{Version: wire.Version, Kind: "http", Phase: wire.After, ID: id, Run: run,
			Source: source(hook), HTTP: &c, Error: errMsg})
	}
	resp, err := p.opt.Transport.RoundTrip(out)
	if err != nil {
		after(0, "", err.Error())
		http.Error(w, "rhythm-echo proxy: "+err.Error(), http.StatusBadGateway)
		return
	}
	defer func() { _ = resp.Body.Close() }()
	removeHopHeaders(resp.Header)
	for k, vs := range resp.Header {
		w.Header()[k] = vs
	}
	w.WriteHeader(resp.StatusCode)
	kept := &capBuffer{max: p.opt.CaptureBody}
	_, copyErr := io.Copy(w, io.TeeReader(resp.Body, kept))
	msg := ""
	if copyErr != nil {
		msg = "copying response: " + copyErr.Error()
	}
	after(resp.StatusCode, decode(resp.Header.Get("Content-Encoding"), kept.Bytes(), kept.cut), msg)
}

const errPaymentTunnel = "payment APIs need --mitm so Echo can tell test from live keys"

// tunnelRefusal says why a tunnel to a payment API was refused: a live
// host (Braintree's, Square's) is refused with MITM too, so --mitm is no
// fix there; a host that serves test and live keys (Stripe, Mollie) needs
// --mitm for Echo to read the key.
func tunnelRefusal(host string) string {
	for _, g := range recognize.PaymentGateways() {
		if g.IsLive(host) {
			return "a live " + g.Name + " API is never reached from staging; use the " + g.Name + " sandbox"
		}
	}
	return errPaymentTunnel
}

// blindPayment reports whether host is a payment API that a blind tunnel
// could reach with live keys: one whose recognizer refuses a call it can't
// see inside (Stripe, Mollie and Razorpay by key; the live hosts of
// PayPal, Braintree, Square, Adyen and Authorize.net; not their sandbox
// hosts).
func blindPayment(host string) bool {
	req := recognize.Request{Method: http.MethodPost, URL: &url.URL{Scheme: "https", Host: strings.ToLower(host), Path: "/"},
		Headers: map[string]string{}}
	var m model.Message
	rec := recognize.Recognize(req, &m)
	return rec.Channel == model.Payment && recognize.Refuses(rec, req)
}

// livePayment reports whether a request is a payment call the catcher
// refuses in every mode (recognize.Refuses).
func livePayment(method string, u *url.URL, headers map[string]string, body []byte) bool {
	h := make(map[string]string, len(headers))
	for k, v := range headers {
		h[strings.ToLower(k)] = v
	}
	req := recognize.Request{Method: strings.ToUpper(method), URL: u, Headers: h, Body: body}
	var m model.Message
	return recognize.Refuses(recognize.Recognize(req, &m), req)
}

func (p *Proxy) markBlocked(id, reason string) {
	if id == "" || p.opt.Server.Inbox == nil {
		return
	}
	p.opt.Server.Inbox.Update(id, func(m *model.Message) {
		m.Status, m.Error, m.Response = model.Blocked, reason, nil
	})
}

// tunnelURL names a tunnel's target for the record: https://host/.
func tunnelURL(target string) string {
	if h, port, err := net.SplitHostPort(target); err == nil && port == "443" {
		return "https://" + h + "/"
	}
	return "https://" + target + "/"
}

func source(hook string) wire.Source { return wire.Source{Via: "proxy", Hook: hook} }

// runOf reads X-Echo-Run from a header or cookie, like the connectors.
func runOf(r *http.Request) string {
	if v := r.Header.Get(wire.RunHeader); v != "" {
		return v
	}
	if c, err := r.Cookie(wire.RunHeader); err == nil {
		return c.Value
	}
	return ""
}

// hopHeaders apply to one connection and are never forwarded (RFC 9110
// section 7.6.1).
var hopHeaders = []string{"Connection", "Proxy-Connection", "Keep-Alive", "Proxy-Authenticate",
	"Proxy-Authorization", "Te", "Trailer", "Transfer-Encoding", "Upgrade"}

func removeHopHeaders(h http.Header) {
	for _, f := range h.Values("Connection") {
		for _, name := range strings.Split(f, ",") {
			if name = strings.TrimSpace(name); name != "" {
				h.Del(name)
			}
		}
	}
	for _, k := range hopHeaders {
		h.Del(k)
	}
}

// flatten turns headers into the capture's one-value-per-name map,
// leaving out the proxy's own credentials and hop-by-hop headers.
func flatten(h http.Header) map[string]string {
	c := h.Clone()
	removeHopHeaders(c)
	c.Del(wire.RunHeader)
	out := make(map[string]string, len(c))
	for k, vs := range c {
		out[k] = strings.Join(vs, ", ")
	}
	return out
}

// decode undoes gzip or deflate on the kept copy, so the capture holds
// readable JSON. A truncated copy decodes as far as it goes.
func decode(enc string, b []byte, cut bool) string {
	var r io.Reader
	switch strings.ToLower(strings.TrimSpace(enc)) {
	case "", "identity":
		return suffix(string(b), cut)
	case "gzip", "x-gzip":
		zr, err := gzip.NewReader(bytes.NewReader(b))
		if err != nil {
			return fmt.Sprintf("(%d bytes, gzip, not decoded)", len(b))
		}
		r = zr
	case "deflate":
		r = flate.NewReader(bytes.NewReader(b))
	default:
		return fmt.Sprintf("(%d bytes, %s, not decoded)", len(b), enc)
	}
	out, _ := io.ReadAll(io.LimitReader(r, 4<<20))
	return suffix(string(out), cut)
}

func suffix(s string, cut bool) string {
	if cut {
		return s + "\n…(truncated)"
	}
	return s
}

// capBuffer keeps the first max bytes written to it and drops the rest.
type capBuffer struct {
	bytes.Buffer
	max int
	cut bool
}

func (c *capBuffer) Write(b []byte) (int, error) {
	if room := c.max - c.Len(); room > 0 {
		if len(b) > room {
			c.Buffer.Write(b[:room])
			c.cut = true
		} else {
			c.Buffer.Write(b)
		}
	} else if len(b) > 0 {
		c.cut = true
	}
	return len(b), nil
}

// hijack takes over the client connection and tracks it; the caller
// untracks it when done.
func (p *Proxy) hijack(w http.ResponseWriter) (net.Conn, *bufio.Reader, error) {
	c, rw, err := http.NewResponseController(w).Hijack()
	if err != nil {
		http.Error(w, "rhythm-echo proxy: can't take over the connection", http.StatusInternalServerError)
		return nil, nil, err
	}
	if !p.track(c) {
		return nil, nil, net.ErrClosed
	}
	_ = c.SetDeadline(time.Time{}) // the server's header timeout no longer applies
	return c, rw.Reader, nil
}

func closeWrite(c net.Conn) {
	if cw, ok := c.(interface{ CloseWrite() error }); ok {
		_ = cw.CloseWrite()
		return
	}
	_ = c.Close()
}

// IsLoopbackAddr reports whether a host:port listen address is reachable
// from this machine only: localhost or a loopback IP.
func IsLoopbackAddr(addr string) bool {
	host, _, err := net.SplitHostPort(addr)
	if err != nil {
		return false
	}
	if host == "localhost" {
		return true
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}

// bufConn reads first what the HTTP server already buffered (a client
// may send its ClientHello right behind CONNECT).
type bufConn struct {
	net.Conn
	r *bufio.Reader
}

func (c *bufConn) Read(b []byte) (int, error) { return c.r.Read(b) }

// mitmConn carries the CONNECT target into the inner server, which trusts
// it over the Host header.
type mitmConn struct {
	net.Conn
	target string
}

// connListener feeds intercepted connections to the inner server.
type connListener struct {
	ch     chan net.Conn
	done   chan struct{}
	closed sync.Once
}

func newConnListener() *connListener {
	return &connListener{ch: make(chan net.Conn), done: make(chan struct{})}
}

func (l *connListener) push(c net.Conn) bool {
	select {
	case l.ch <- c:
		return true
	case <-l.done:
		return false
	}
}

func (l *connListener) Accept() (net.Conn, error) {
	select {
	case c := <-l.ch:
		return c, nil
	case <-l.done:
		return nil, net.ErrClosed
	}
}

func (l *connListener) Close() error {
	l.closed.Do(func() { close(l.done) })
	return nil
}

func (l *connListener) Addr() net.Addr { return &net.TCPAddr{IP: net.IPv4(127, 0, 0, 1)} }
