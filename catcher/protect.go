// Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
// SPDX-License-Identifier: MIT

package catcher

import (
	"crypto/subtle"
	"mime"
	"net"
	"net/http"
	"net/url"
	"strings"

	"github.com/RhythmShahriar/echo/wire"
)

// ProtectOptions configures Protect.
type ProtectOptions struct {
	// Token, when set, lets a request that carries it in X-Echo-Token skip
	// every check: a web page can't know it, so it can't be a forged
	// cross-site request.
	Token string
	// Tokens are more tokens that pass the same way (read and write
	// tokens); empty ones are ignored.
	Tokens []string
	// Hosts are host names accepted besides loopback ones: the host part
	// of the address Echo listens on and the names up --hostname gives.
	// "*.ddev.site" accepts every name under ddev.site.
	Hosts []string
	// PrivateNetwork (up --private-network without a token) also accepts
	// a Host that is an IP address or a name of one label ("echo" in
	// Docker Compose, DDEV and Lando) from a peer on loopback, a private
	// network or the shared address space. A DNS-rebinding page always
	// sends its own domain name, so it can be neither. Origin and
	// Content-Type are still checked.
	PrivateNetwork bool
}

// Protect guards a loopback API against web pages open in the developer's
// browser. Without a token Echo trusts anything that reaches it and
// browsers can reach 127.0.0.1 too:
//
//   - a page can send a "simple" text/plain, form or multipart POST
//     without a CORS preflight, so a body sent with POST, PUT or PATCH must
//     be JSON (415 otherwise). One exception keeps shell scripts working:
//     curl -d labels its JSON as a form, so a form type (or none) is
//     accepted from a client that shows no sign of being a browser. DELETE
//     is exempt: browsers always preflight it and send Origin;
//   - a page can't stop its browser sending Origin, so any Origin other
//     than Echo's own is refused (403);
//   - with DNS rebinding a page points its own name at 127.0.0.1 and reads
//     the inbox but its Host header still names the attacker's domain, so
//     a Host that isn't loopback or a configured name is refused (403).
//
// Tools that aren't browsers (curl, connectors, Mailpit clients) send no
// Origin and a plain Host and pass unchanged.
func Protect(next http.Handler, o ProtectOptions) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if o.hasToken(r.Header.Get(wire.TokenHeader)) {
			next.ServeHTTP(w, r)
			return
		}
		if !HostAllowed(r, o.Hosts, o.PrivateNetwork) {
			http.Error(w, "Echo answers only to localhost: Host "+r.Host+" is not allowed", http.StatusForbidden)
			return
		}
		if origin := r.Header.Get("Origin"); origin != "" && !sameOrigin(origin, r.Host) {
			http.Error(w, "Echo refuses requests from web pages: Origin "+origin+" is not allowed", http.StatusForbidden)
			return
		}
		if hasBody(r) && !jsonBody(r) {
			http.Error(w, "send JSON: Content-Type must be application/json", http.StatusUnsupportedMediaType)
			return
		}
		next.ServeHTTP(w, r)
	})
}

// hasToken reports whether tok is one of the configured tokens.
func (o ProtectOptions) hasToken(tok string) bool {
	if tok == "" {
		return false
	}
	ok := false
	for _, t := range append([]string{o.Token}, o.Tokens...) {
		if t != "" && subtle.ConstantTimeCompare([]byte(tok), []byte(t)) == 1 {
			ok = true
		}
	}
	return ok
}

// HostAllowed reports whether r's Host header may be served without a
// token: a loopback name, one of hosts (exact, or "*.example.test" for the
// names under example.test) or, with privateNetwork, an IP address or a
// name of one label sent from a private peer (see
// ProtectOptions.PrivateNetwork). Echo Inbox applies the same rule.
func HostAllowed(r *http.Request, hosts []string, privateNetwork bool) bool {
	if r.Host == "" {
		return true // HTTP/1.0 without Host: not a browser
	}
	host := strings.ToLower(hostOnly(r.Host))
	host = strings.TrimSuffix(strings.TrimPrefix(host, "["), "]")
	if loopbackName(host) {
		return true
	}
	for _, h := range hosts {
		if hostMatch(strings.ToLower(strings.TrimSuffix(strings.TrimPrefix(h, "["), "]")), host) {
			return true
		}
	}
	if !privateNetwork || !PrivatePeer(r.RemoteAddr) {
		return false
	}
	if ip := net.ParseIP(host); ip != nil {
		return !ip.IsUnspecified() // "0.0.0.0" reaches loopback from a browser
	}
	return host != "" && !strings.Contains(host, ".")
}

// hostMatch reports whether host is pattern, or under it when pattern
// starts with "*.".
func hostMatch(pattern, host string) bool {
	if suffix, ok := strings.CutPrefix(pattern, "*."); ok {
		return suffix != "" && strings.HasSuffix(host, "."+suffix)
	}
	return pattern != "" && pattern == host
}

// sharedSpace is the carrier-grade NAT range (RFC 6598), which some
// container runtimes give their bridges. It is never routed on the
// internet.
var sharedSpace = &net.IPNet{IP: net.IPv4(100, 64, 0, 0).To4(), Mask: net.CIDRMask(10, 32)}

// PrivatePeer reports whether remote (a request's RemoteAddr) is on
// loopback, a private network (RFC 1918, fc00::/7) or the shared address
// space (100.64.0.0/10): somewhere --private-network lets Echo be reached
// from.
func PrivatePeer(remote string) bool {
	host, _, err := net.SplitHostPort(remote)
	if err != nil {
		host = remote
	}
	ip := net.ParseIP(host)
	if ip == nil {
		return false
	}
	return ip.IsLoopback() || ip.IsPrivate() || sharedSpace.Contains(ip)
}

// loopbackName reports whether host is a loopback address or a localhost
// name. Browsers resolve *.localhost to loopback themselves (RFC 6761), so
// a rebinding page can't own one.
func loopbackName(host string) bool {
	if ip := net.ParseIP(host); ip != nil {
		return ip.IsLoopback()
	}
	return host == "localhost" || strings.HasSuffix(host, ".localhost")
}

// sameOrigin reports whether origin is Echo's own page: the live view
// served from the same host and port the request was sent to.
func sameOrigin(origin, host string) bool {
	u, err := url.Parse(origin)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") {
		return false // includes "null" from sandboxed frames and files
	}
	return strings.EqualFold(u.Host, host)
}

// jsonBody reports whether r's body may be read as JSON: it says it is,
// or it comes from curl -d (a form type) and not from a browser.
func jsonBody(r *http.Request) bool {
	ct, _, _ := mime.ParseMediaType(r.Header.Get("Content-Type"))
	switch ct {
	case "application/json":
		return true
	case "", "application/x-www-form-urlencoded":
		return !browser(r)
	}
	return false
}

// browser reports whether r shows any sign of coming from a web browser.
// Every browser sends a User-Agent starting "Mozilla/"; current ones also
// send Sec-Fetch headers and pages may add a Referer.
func browser(r *http.Request) bool {
	return strings.HasPrefix(r.UserAgent(), "Mozilla/") || r.Header.Get("Sec-Fetch-Mode") != "" ||
		r.Header.Get("Sec-Fetch-Site") != "" || r.Referer() != ""
}

// hasBody reports whether r is a write that carries a body.
func hasBody(r *http.Request) bool {
	switch r.Method {
	case http.MethodPost, http.MethodPut, http.MethodPatch:
		return r.ContentLength != 0 // -1 is a chunked body of unknown length
	}
	return false
}
