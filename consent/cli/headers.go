// Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
// SPDX-License-Identifier: MIT

package cli

import (
	"errors"
	"fmt"
	"io"
	"io/fs"
	"maps"
	"net"
	"net/url"
	"os"
	"regexp"
	"slices"
	"strings"

	"github.com/RhythmShahriar/echo/config"
)

// maxSecretFile is the most an @file header or cookie value may read:
// a token or a cookie, never a whole file.
const maxSecretFile = 64 << 10

// The warnings a plain secret on the command line prints, once each
// (PRD 10.2 message 22 and its cookie twin).
const (
	msgPlainHeader = "--header value on the command line shows in ps to every user on this machine; use @file or env:NAME"
	msgPlainCookie = "--cookie value on the command line shows in ps to every user on this machine; use @file or env:NAME"
)

// headerToken is what a header or cookie name may be (RFC 9110 token).
var headerToken = regexp.MustCompile("^[!#$%&'*+.^_`|~0-9A-Za-z-]+$")

// configEnvRef is a consent: headers value, ${env:RHYTHM_ECHO_NAME},
// which config validation already holds to that shape.
var configEnvRef = regexp.MustCompile(`^\$\{env:(RHYTHM_ECHO_[A-Z0-9_]+)\}$`)

// secrets are the check's resolved headers and cookies with the
// warnings resolving them printed. Errors never quote a value.
type secrets struct {
	headers map[string]string
	cookies []string
	warn    []string // the warnings to print, each once
}

// resolve reads consent: headers (cfg) first, then -H, which wins for
// the same name, then --cookie. A flag's value is @file (read, at most
// 64 KiB, trimmed), env:NAME (the variable) or plain, which warns once
// since ps shows it.
func (s *secrets) resolve(cfg map[string]string, headers, cookies []string) error {
	if s.headers == nil {
		s.headers = map[string]string{}
	}
	for _, name := range slices.Sorted(maps.Keys(cfg)) {
		m := configEnvRef.FindStringSubmatch(strings.TrimSpace(cfg[name]))
		if m == nil {
			// config validation refuses this; never quote the value.
			return usagef("consent: headers: %s: use ${env:RHYTHM_ECHO_NAME}, not the value itself; secrets never sit in the config", name)
		}
		v, ok := os.LookupEnv(m[1])
		if !ok || v == "" {
			return usagef("consent: headers: %s: %s is not set", name, m[1])
		}
		if err := headerValue(v); err != nil {
			return usagef("consent: headers: %s: %s %v", name, m[1], err)
		}
		s.headers[name] = v
	}
	plainHeader, plainCookie := false, false
	for _, h := range headers {
		name, raw, ok := strings.Cut(h, ":")
		name = strings.TrimSpace(name)
		if !ok || !headerToken.MatchString(name) {
			// Never quoted back: the whole argument may be a secret.
			return usagef("--header: want \"Name: value\", e.g. -H \"Authorization: env:RHYTHM_ECHO_STAGING_AUTH\"")
		}
		v, plain, err := secretValue("--header "+name, strings.TrimSpace(raw))
		if err != nil {
			return err
		}
		if err := headerValue(v); err != nil {
			return usagef("--header %s: %v", name, err)
		}
		plainHeader = plainHeader || plain
		s.headers[name] = v
	}
	for _, c := range cookies {
		whole := strings.HasPrefix(c, "@") || strings.HasPrefix(c, "env:")
		if whole {
			v, plain, err := secretValue("--cookie", c)
			if err != nil {
				return err
			}
			plainCookie = plainCookie || plain
			c = v
		}
		name, raw, ok := strings.Cut(c, "=")
		name = strings.TrimSpace(name)
		if !ok || !headerToken.MatchString(name) {
			return usagef("--cookie: want name=value, e.g. --cookie \"session=env:RHYTHM_ECHO_SESSION\"")
		}
		v := strings.TrimSpace(raw)
		if !whole {
			var plain bool
			var err error
			if v, plain, err = secretValue("--cookie "+name, v); err != nil {
				return err
			}
			plainCookie = plainCookie || plain
		}
		if strings.ContainsAny(v, ";\r\n") {
			return usagef("--cookie %s: the value has a ; or a line break; give one cookie per --cookie", name)
		}
		s.cookies = append(s.cookies, name+"="+v)
	}
	if plainHeader && !slices.Contains(s.warn, msgPlainHeader) {
		s.warn = append(s.warn, msgPlainHeader)
	}
	if plainCookie && !slices.Contains(s.warn, msgPlainCookie) {
		s.warn = append(s.warn, msgPlainCookie)
	}
	return nil
}

// secretValue reads one value: @file, env:NAME or the value itself
// (plain is then true). what names it in errors, e.g. "--header
// Authorization".
func secretValue(what, raw string) (v string, plain bool, err error) {
	switch {
	case strings.HasPrefix(raw, "env:"):
		name := strings.TrimPrefix(raw, "env:")
		v, ok := os.LookupEnv(name)
		if !ok || v == "" {
			return "", false, usagef("%s: env:%s is not set", what, name)
		}
		return v, false, nil
	case strings.HasPrefix(raw, "@"):
		path := strings.TrimPrefix(raw, "@")
		f, err := os.Open(path) //nolint:gosec // a file the user names for this value
		if errors.Is(err, fs.ErrNotExist) {
			return "", false, usagef("%s: @%s doesn't exist", what, path)
		}
		if err != nil {
			return "", false, usagef("%s: @%s: %v", what, path, err)
		}
		defer func() { _ = f.Close() }()
		data, err := io.ReadAll(io.LimitReader(f, maxSecretFile+1))
		if err != nil {
			return "", false, usagef("%s: @%s: %v", what, path, err)
		}
		if len(data) > maxSecretFile {
			return "", false, usagef("%s: @%s is more than 64 KiB", what, path)
		}
		return strings.TrimSpace(string(data)), false, nil
	}
	return raw, true, nil
}

// headerValue refuses a value that would split the request.
func headerValue(v string) error {
	if strings.ContainsAny(v, "\r\n\x00") {
		return errors.New("has a line break")
	}
	return nil
}

// parseMapHosts reads consent: map_hosts and then --map-host values
// (host=addr), the flags winning for the same host.
func parseMapHosts(cfg map[string]string, flags []string) (map[string]string, error) {
	out := map[string]string{}
	for host, addr := range cfg {
		if err := config.ParseMapHost(host, addr); err != nil {
			return nil, usagef("consent: map_hosts: %v", err)
		}
		out[strings.ToLower(strings.TrimSpace(host))] = strings.TrimSpace(addr)
	}
	for _, m := range flags {
		host, addr, ok := strings.Cut(m, "=")
		if !ok || config.ParseMapHost(host, addr) != nil {
			return nil, usagef("--map-host %q: want host=address, e.g. *.shop.test=127.0.0.1:18950", m)
		}
		out[strings.ToLower(strings.TrimSpace(host))] = strings.TrimSpace(addr)
	}
	if len(out) == 0 {
		return nil, nil
	}
	return out, nil
}

// mappedTo is the address host is answered from by maps, or "".
func mappedTo(host string, maps map[string]string) string {
	h := strings.ToLower(host)
	if a, ok := maps[h]; ok {
		return a
	}
	for pattern, a := range maps {
		if base, ok := strings.CutPrefix(pattern, "*."); ok && (h == base || strings.HasSuffix(h, "."+base)) {
			return a
		}
	}
	return ""
}

// privateNames are suffixes of names that never resolve on the public
// internet (RFC 6761, RFC 6762, RFC 8375 and the usual intranet ones).
var privateNames = []string{".localhost", ".test", ".local", ".internal", ".lan", ".home.arpa", ".localdomain", ".intranet", ".corp", ".private"}

// privateTarget reports whether u is a loopback or private site, which
// -k may check without --insecure-public: a loopback or private
// address, a name that can't be public, or a host --map-host answers
// from such an address.
func privateTarget(u *url.URL, maps map[string]string) bool {
	if privateHost(u.Hostname()) {
		return true
	}
	a := mappedTo(u.Hostname(), maps)
	if a == "" {
		return false
	}
	if p, err := url.Parse(a); err == nil && p.Host != "" {
		return privateHost(p.Hostname())
	}
	if h, _, err := net.SplitHostPort(a); err == nil {
		return privateHost(h)
	}
	return false
}

// privateHost reports whether host is loopback, private or link-local,
// or a name that can't be on the public internet.
func privateHost(host string) bool {
	h := strings.ToLower(strings.TrimSuffix(strings.Trim(host, "[]"), "."))
	if ip := net.ParseIP(h); ip != nil {
		return ip.IsLoopback() || ip.IsPrivate() || ip.IsLinkLocalUnicast() || ip.IsUnspecified()
	}
	if h == "localhost" || !strings.Contains(h, ".") {
		return true
	}
	return slices.ContainsFunc(privateNames, func(s string) bool { return strings.HasSuffix(h, s) })
}

// msgSendHitsProduction is PRD 10.2 message 6.
func msgSendHitsProduction(host, entry string) string {
	return fmt.Sprintf("--send-hits on %s, which matches %q under production:. Echo holds tracker hits on production so your analytics stay clean; leave --send-hits off", host, entry)
}
