// Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
// SPDX-License-Identifier: MIT

// Package replay sends signed test webhooks (Stripe, Standard Webhooks,
// GitHub, WooCommerce, generic HMAC) to a staging site, so Echo can capture
// what the site sends in response: "payment_intent.succeeded → order email
// + SMS + CRM call".
//
// Safety is enforced here, not left to callers: a target listed under
// production is refused; any other must be a configured staging URL
// or resolve only to loopback or private addresses (checked again at
// dial time, so DNS can't change the answer), live keys
// anywhere in reach refuse the send, secrets must look like test secrets,
// redirects aren't followed and no proxy is used.
package replay

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/RhythmShahriar/rhythm/kit/secrets"

	"github.com/RhythmShahriar/echo/config"
	"github.com/RhythmShahriar/echo/model"
)

// ErrRefused wraps every safety refusal, so callers can tell "Echo said no"
// from "the site failed".
var ErrRefused = errors.New("replay refused")

// ErrNoSecret means no signing secret was given or found in the environment.
var ErrNoSecret = errors.New("no signing secret")

// Resolver resolves target hosts. *net.Resolver and dns.Zone satisfy it.
type Resolver interface {
	LookupHost(ctx context.Context, host string) ([]string, error)
}

// Target is where to send.
type Target struct {
	URL     string            `json:"url"`
	Headers map[string]string `json:"headers,omitempty"` // extra headers; they can't replace signature headers
}

// Options configures Send.
type Options struct {
	// Secret signs the event. When empty it's read from SecretEnv, or the
	// scheme's default variable (SecretEnv(scheme)).
	Secret    string
	SecretEnv string
	// Staging lists URL prefixes from config that may be targeted even
	// when they resolve to public addresses.
	Staging []string
	// Production lists live sites from config (URLs or hosts, see
	// config.ProductionEntry). A target under one is refused even when
	// it's also listed as staging or resolves to a private address.
	Production []string
	// Env is the environment secrets are read from and scanned for live
	// keys; nil means os.Environ().
	Env []string
	// Scan is other configuration in reach (name → contents: .env,
	// wp-config.php, Echo's config), scanned for live keys.
	Scan map[string]string
	HMAC HMAC
	// Run and Action go into X-Echo-Run, so what the site sends in
	// response is tied to this replay. Defaults: a new run ID and
	// "replay-<event>".
	Run, Action string
	Resolver    Resolver // nil: net.DefaultResolver
	Timeout     time.Duration
	Now         func() time.Time
	MaxBody     int // response bytes kept; default 64 KiB

	// RunKey, when set, signs X-Echo-Run, as connectors with a key only
	// trust a signed value. Leave it empty until the Echo the site
	// reports to strips signed values (see runner.Options.RunKey):
	// otherwise the signature ends up in the action ID.
	RunKey string
}

// Result is what happened.
type Result struct {
	Event     string            `json:"event"`
	Scheme    string            `json:"scheme"`
	URL       string            `json:"url"`
	Run       string            `json:"run"`
	Action    string            `json:"action"`
	Status    int               `json:"status"`
	Body      string            `json:"body,omitempty"` // capped, secrets redacted
	Truncated bool              `json:"truncated,omitempty"`
	Location  string            `json:"location,omitempty"` // a redirect, not followed
	Duration  time.Duration     `json:"duration_ns"`
	Headers   map[string]string `json:"headers"` // as sent, signatures and secrets masked
	Secret    string            `json:"secret"`  // where the secret came from, masked
}

// OK reports a 2xx answer.
func (r Result) OK() bool { return r.Status >= 200 && r.Status < 300 }

const defaultMaxBody = 64 << 10

// Send signs e and POSTs it to t.
func Send(ctx context.Context, e Event, t Target, o Options) (Result, error) {
	now := time.Now
	if o.Now != nil {
		now = o.Now
	}
	res := Result{Event: e.Name, Scheme: e.Scheme}
	if e.Scheme == PayPal {
		return res, ErrPayPal
	}
	if !known(e.Scheme) {
		return res, fmt.Errorf("unknown signing scheme %q", e.Scheme)
	}
	env := o.Env
	if env == nil {
		env = os.Environ()
	}
	if err := liveKeys(env, o.Scan); err != nil {
		return res, err
	}
	if liveMode.Match(e.Body) {
		return res, fmt.Errorf(`%w: the payload has "livemode": true; replays are for test-mode events only`, ErrRefused)
	}
	secret, from, err := resolveSecret(e.Scheme, o, env)
	if err != nil {
		return res, err
	}
	res.Secret = from + "=" + maskSecret(secret)

	u, err := url.Parse(t.URL)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Hostname() == "" {
		return res, fmt.Errorf("target %q: want an http:// or https:// URL", t.URL)
	}
	res.URL = secrets.Redact(u.String())
	if e := config.ProductionEntry(o.Production, t.URL); e != "" {
		return res, fmt.Errorf("%w: %s matches %q under production; replays never go to a live site", ErrRefused, res.URL, e)
	}
	r := o.Resolver
	if r == nil {
		r = net.DefaultResolver
	}
	staged := inStaging(u, o.Staging)
	if !staged {
		if _, err := privateAddrs(ctx, r, u.Hostname()); err != nil {
			return res, err
		}
	}

	h, err := Sign(e, secret, o.HMAC, now())
	if err != nil {
		return res, err
	}
	signed := map[string]bool{}
	for k := range h {
		signed[k] = true
	}
	for k, v := range t.Headers {
		if ck := http.CanonicalHeaderKey(k); !signed[ck] {
			h.Set(ck, v)
		}
	}
	res.Run = orDefault(o.Run, "replay-"+model.NewID(e.Name+now().String()+randomID(8)))
	res.Action = orDefault(o.Action, "replay-"+strings.NewReplacer("/", "-", " ", "-").Replace(e.Name))
	h.Set("X-Echo-Run", signRun(o.RunKey, res.Run, res.Action))
	res.Headers = maskHeaders(h, o.HMAC)

	timeout := o.Timeout
	if timeout == 0 {
		timeout = 30 * time.Second
	}
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, u.String(), bytes.NewReader(e.Body))
	if err != nil {
		return res, err
	}
	req.Header = h
	client := &http.Client{
		Transport: &http.Transport{Proxy: nil, DialContext: guardedDial(r, staged), ForceAttemptHTTP2: true, TLSHandshakeTimeout: 10 * time.Second},
		// A redirect could point anywhere, including production.
		CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
	}
	start := time.Now()
	resp, err := client.Do(req)
	res.Duration = time.Since(start)
	if err != nil {
		var ue *url.Error
		if errors.As(err, &ue) {
			err = ue.Err
		}
		return res, fmt.Errorf("POST %s: %w", res.URL, err)
	}
	defer func() { _ = resp.Body.Close() }()
	limit := o.MaxBody
	if limit <= 0 {
		limit = defaultMaxBody
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, int64(limit)+1))
	res.Duration = time.Since(start)
	res.Status = resp.StatusCode
	res.Location = secrets.Redact(resp.Header.Get("Location"))
	if len(body) > limit {
		body, res.Truncated = body[:limit], true
	}
	res.Body = secrets.Redact(string(body))
	if err != nil {
		return res, fmt.Errorf("reading the response: %w", err)
	}
	return res, nil
}

var (
	liveKey  = regexp.MustCompile(`\b(?:sk|rk)_live_[0-9A-Za-z]{8,}`)
	liveMode = regexp.MustCompile(`"livemode"\s*:\s*true`)
	apiKey   = regexp.MustCompile(`^(sk|rk|pk)_(test|live)_`)
)

// liveKeys refuses when a live Stripe secret or restricted key is in the
// environment or any scanned config: a live key in reach means this may be
// production, or one copy-paste away from it.
func liveKeys(env []string, scan map[string]string) error {
	var found []string
	for _, kv := range env {
		name, v, _ := strings.Cut(kv, "=")
		if k := liveKey.FindString(v); k != "" {
			found = append(found, name+"="+secrets.Mask(k, 8))
		}
	}
	names := make([]string, 0, len(scan))
	for n := range scan {
		names = append(names, n)
	}
	sort.Strings(names)
	for _, n := range names {
		if k := liveKey.FindString(scan[n]); k != "" {
			found = append(found, n+": "+secrets.Mask(k, 8))
		}
	}
	if len(found) > 0 {
		return fmt.Errorf("%w: a live Stripe key is in reach (%s); run replays where only test keys are configured",
			ErrRefused, strings.Join(found, ", "))
	}
	return nil
}

func lookupEnv(env []string, name string) (string, bool) {
	for i := len(env) - 1; i >= 0; i-- {
		if k, v, ok := strings.Cut(env[i], "="); ok && k == name {
			return v, true
		}
	}
	return "", false
}

// SecretReady reports, before anything is sent, whether a signing secret
// for e can be found: ErrNoSecret when it can't.
func SecretReady(e Event, o Options) error {
	if e.Scheme == PayPal || !known(e.Scheme) {
		return nil // Send explains these
	}
	env := o.Env
	if env == nil {
		env = os.Environ()
	}
	if _, _, err := resolveSecret(e.Scheme, o, env); errors.Is(err, ErrNoSecret) {
		return err
	}
	return nil
}

// resolveSecret finds the signing secret and checks it's test-shaped
// where that can be told.
func resolveSecret(scheme string, o Options, env []string) (secret, from string, err error) {
	secret, from = o.Secret, "option"
	if secret == "" {
		from = orDefault(o.SecretEnv, secretEnv[scheme])
		secret, _ = lookupEnv(env, from)
		if secret == "" {
			return "", from, fmt.Errorf("%w: set %s to the endpoint's test signing secret", ErrNoSecret, from)
		}
	}
	secret = strings.TrimSpace(secret)
	if strings.Contains(secret, "_live_") {
		return "", from, fmt.Errorf("%w: %s holds a live key", ErrRefused, from)
	}
	if apiKey.MatchString(secret) {
		return "", from, fmt.Errorf("%w: %s holds a Stripe API key, not a webhook signing secret (whsec_…)", ErrRefused, from)
	}
	switch scheme {
	case Stripe:
		if !strings.HasPrefix(secret, "whsec_") {
			return "", from, fmt.Errorf("%w: %s isn't a Stripe webhook signing secret (whsec_…); use the test-mode endpoint's secret or `stripe listen`'s", ErrRefused, from)
		}
	case Standard:
		if _, err := standardKey(secret); err != nil {
			return "", from, fmt.Errorf("%w: %s: %w", ErrRefused, from, err)
		}
	}
	return secret, from, nil
}

// inStaging reports whether u starts with one of the configured staging
// URL prefixes: same scheme and host, path at a segment boundary.
func inStaging(u *url.URL, prefixes []string) bool {
	for _, p := range prefixes {
		pu, err := url.Parse(strings.TrimSpace(p))
		if err != nil || pu.Host == "" {
			continue
		}
		if !strings.EqualFold(pu.Scheme, u.Scheme) || !strings.EqualFold(hostPort(pu), hostPort(u)) {
			continue
		}
		pp := strings.TrimSuffix(pu.Path, "/")
		if pp == "" || u.Path == pp || strings.HasPrefix(u.Path, pp+"/") {
			return true
		}
	}
	return false
}

func hostPort(u *url.URL) string {
	port := u.Port()
	if port == "" {
		port = map[string]string{"http": "80", "https": "443"}[strings.ToLower(u.Scheme)]
	}
	return strings.ToLower(u.Hostname()) + ":" + port
}

// privateAddrs resolves host and requires every address to be loopback or
// private (RFC 1918, fc00::/7). Link-local is excluded on purpose: it
// holds cloud metadata endpoints.
func privateAddrs(ctx context.Context, r Resolver, host string) ([]string, error) {
	var addrs []string
	if ip := net.ParseIP(host); ip != nil {
		addrs = []string{ip.String()}
	} else {
		var err error
		addrs, err = r.LookupHost(ctx, host)
		if err != nil {
			return nil, fmt.Errorf("resolving %s: %w", host, err)
		}
	}
	if len(addrs) == 0 {
		return nil, fmt.Errorf("resolving %s: no addresses", host)
	}
	for _, a := range addrs {
		ip := net.ParseIP(a)
		if ip == nil || (!ip.IsLoopback() && !ip.IsPrivate()) {
			return nil, fmt.Errorf("%w: %s resolves to %s, which is neither loopback nor private; add the staging URL under staging: in the Echo config to allow it",
				ErrRefused, host, a)
		}
	}
	return addrs, nil
}

// guardedDial resolves once and dials the checked address, so a DNS answer
// that changes between the check and the connection can't redirect the
// request.
func guardedDial(r Resolver, staged bool) func(ctx context.Context, network, addr string) (net.Conn, error) {
	return func(ctx context.Context, network, addr string) (net.Conn, error) {
		host, port, err := net.SplitHostPort(addr)
		if err != nil {
			return nil, err
		}
		var addrs []string
		if staged {
			if ip := net.ParseIP(host); ip != nil {
				addrs = []string{host}
			} else if addrs, err = r.LookupHost(ctx, host); err != nil {
				return nil, err
			}
		} else if addrs, err = privateAddrs(ctx, r, host); err != nil {
			return nil, err
		}
		var d net.Dialer
		var last error
		for _, a := range addrs {
			c, err := d.DialContext(ctx, network, net.JoinHostPort(a, port))
			if err == nil {
				return c, nil
			}
			last = err
		}
		if last == nil {
			last = fmt.Errorf("%s: no addresses", host)
		}
		return nil, last
	}
}

// maskHeaders returns the sent headers with signature values cut short and
// anything secret-looking redacted.
func maskHeaders(h http.Header, cfg HMAC) map[string]string {
	sig := map[string]bool{}
	for _, k := range signatureHeaders(cfg) {
		sig[k] = true
	}
	out := map[string]string{}
	for k, vs := range h {
		v := strings.Join(vs, ", ")
		if sig[k] {
			v = maskSignature(v)
		} else {
			v = secrets.Redact(v)
		}
		out[k] = v
	}
	return out
}

// maskSignature keeps the structure (t=…, v1=, sha256=) and the first
// characters of each digest.
var digest = regexp.MustCompile(`[A-Za-z0-9+/=]{16,}`)

func maskSignature(v string) string {
	return digest.ReplaceAllStringFunc(v, func(d string) string { return d[:6] + "•••••" })
}

func maskSecret(s string) string {
	if p, ok := strings.CutPrefix(s, "whsec_"); ok {
		return "whsec_" + secrets.Mask(p, 0)
	}
	return secrets.Mask(s, 0)
}
