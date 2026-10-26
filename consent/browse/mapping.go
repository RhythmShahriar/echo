// Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
// SPDX-License-Identifier: MIT

package browse

import (
	"bytes"
	"context"
	"errors"
	"io"
	"net/http"
	"net/url"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/go-rod/rod/lib/proto"

	"github.com/RhythmShahriar/echo/consent/browse/egress"
)

// maxMapped caps a mapped response's body: more than any fixture or
// staging page needs, little enough that a broken server can't fill
// the memory.
const maxMapped = 32 << 20

// mapTimeout bounds one mapped request.
const mapTimeout = 15 * time.Second

// mapper answers --map-host hosts from their local address instead of
// the network.
type mapper struct {
	patterns []string            // most specific first: exact hosts, then the longest *.suffix
	bases    map[string]*url.URL // pattern (lower case) -> base URL
	client   *http.Client
}

// newMapper reads --map-host host=addr pairs: addr is host:port (plain
// http) or a full http:// URL used as the base.
func newMapper(m map[string]string) *mapper {
	mp := &mapper{bases: map[string]*url.URL{}, client: &http.Client{
		Timeout:       mapTimeout,
		CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
		Transport: &http.Transport{
			Proxy:               nil, // a mapped address is local; never send it through a proxy
			DisableCompression:  true,
			MaxIdleConnsPerHost: 16,
			IdleConnTimeout:     30 * time.Second,
		},
	}}
	for pat, addr := range m {
		base := mapBase(addr)
		if base == nil {
			continue
		}
		key := strings.TrimSuffix(strings.ToLower(strings.TrimSpace(pat)), ".")
		mp.bases[key] = base
		mp.patterns = append(mp.patterns, key)
	}
	slices.SortFunc(mp.patterns, func(a, b string) int {
		aw, bw := strings.HasPrefix(a, "*."), strings.HasPrefix(b, "*.")
		if aw != bw {
			if aw {
				return 1
			}
			return -1
		}
		if len(a) != len(b) {
			return len(b) - len(a)
		}
		return strings.Compare(a, b)
	})
	return mp
}

// mapBase reads a --map-host address.
func mapBase(addr string) *url.URL {
	addr = strings.TrimSpace(addr)
	if !strings.Contains(addr, "://") {
		addr = "http://" + addr
	}
	u, err := url.Parse(addr)
	if err != nil || u.Host == "" || (u.Scheme != "http" && u.Scheme != "https") {
		return nil
	}
	u.Path = strings.TrimSuffix(u.Path, "/")
	return u
}

// lookup returns the base URL host is mapped to.
func (m *mapper) lookup(host string) (bool, *url.URL) {
	if m == nil {
		return false, nil
	}
	for _, p := range m.patterns {
		if egress.MatchHost([]string{p}, host) {
			return true, m.bases[p]
		}
	}
	return false, nil
}

// any reports whether any host is mapped.
func (m *mapper) any() bool { return m != nil && len(m.patterns) > 0 }

// hopHeaders are the headers a hop owns, never passed on.
var hopHeaders = []string{"Connection", "Keep-Alive", "Proxy-Connection", "Transfer-Encoding", "Te", "Trailer", "Upgrade"}

// fetchMapped sends the request to the mapped address with the same
// method, path, query, headers and body (Host stays the original) and
// answers Chrome with what came back, Set-Cookie included, so cookies
// land as they would from the real host. It returns the status, 0 when
// the mapped server couldn't be reached, with the Set-Cookie lines.
func (r *recorder) fetchMapped(s cdp, e *proto.FetchRequestPaused, u *url.URL, body []byte, base *url.URL) (int, []string) {
	target := strings.TrimSuffix(base.String(), "/") + u.EscapedPath()
	if u.RawQuery != "" {
		target += "?" + u.RawQuery
	}
	ctx, cancel := context.WithTimeout(r.halt, mapTimeout)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, e.Request.Method, target, bytes.NewReader(body))
	if err != nil {
		_ = proto.FetchFailRequest{RequestID: e.RequestID, ErrorReason: proto.NetworkErrorReasonFailed}.Call(s)
		return 0, nil
	}
	for name, v := range e.Request.Headers {
		if strings.EqualFold(name, "Host") || slices.ContainsFunc(hopHeaders, func(h string) bool { return strings.EqualFold(h, name) }) {
			continue
		}
		req.Header.Set(name, v.Str())
	}
	for _, h := range r.sameSiteHeaders(u, nil) {
		req.Header.Set(h.Name, h.Value)
	}
	req.Host = u.Host
	resp, err := r.mapper.client.Do(req)
	if err != nil {
		_ = proto.FetchFailRequest{RequestID: e.RequestID, ErrorReason: proto.NetworkErrorReasonConnectionRefused}.Call(s)
		return 0, nil
	}
	defer func() { _ = resp.Body.Close() }()
	data, err := io.ReadAll(io.LimitReader(resp.Body, maxMapped+1))
	if err != nil && !errors.Is(err, io.EOF) {
		_ = proto.FetchFailRequest{RequestID: e.RequestID, ErrorReason: proto.NetworkErrorReasonFailed}.Call(s)
		return 0, nil
	}
	if len(data) > maxMapped {
		data = data[:maxMapped]
	}
	lines := resp.Header.Values("Set-Cookie")
	if len(lines) > 0 {
		// Chrome reports no raw headers for a response Echo answered, so
		// the attribution notes them here.
		r.noteSetCookies(u.Hostname(), lines, r.now())
	}
	var headers []*proto.FetchHeaderEntry
	for name, vs := range resp.Header {
		if strings.EqualFold(name, "Content-Length") || slices.ContainsFunc(hopHeaders, func(h string) bool { return strings.EqualFold(h, name) }) {
			continue
		}
		for _, v := range vs {
			headers = append(headers, &proto.FetchHeaderEntry{Name: name, Value: v})
		}
	}
	_ = proto.FetchFulfillRequest{RequestID: e.RequestID, ResponseCode: resp.StatusCode, ResponseHeaders: headers, Body: data,
		ResponsePhrase: strings.TrimSpace(strings.TrimPrefix(resp.Status, strconv.Itoa(resp.StatusCode)))}.Call(s)
	return resp.StatusCode, lines
}
