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
	// Hosts are host names accepted besides loopback ones: the host part
	// of the address Echo listens on ("echo" in Docker Compose).
	Hosts []string
}

// Protect guards a loopback API against web pages open in the developer's
// browser. Without a token Echo trusts anything that reaches it, and
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
		if o.Token != "" && subtle.ConstantTimeCompare([]byte(r.Header.Get(wire.TokenHeader)), []byte(o.Token)) == 1 {
			next.ServeHTTP(w, r)
			return
		}
		if !o.hostAllowed(r.Host) {
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

func (o ProtectOptions) hostAllowed(hostport string) bool {
	if hostport == "" {
		return true // HTTP/1.0 without Host: not a browser
	}
	host := strings.ToLower(hostOnly(hostport))
	host = strings.TrimSuffix(strings.TrimPrefix(host, "["), "]")
	if loopbackName(host) {
		return true
	}
	for _, h := range o.Hosts {
		h = strings.ToLower(strings.TrimSuffix(strings.TrimPrefix(h, "["), "]"))
		if h != "" && h == host {
			return true
		}
	}
	return false
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
