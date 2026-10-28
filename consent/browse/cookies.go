// Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
// SPDX-License-Identifier: MIT

package browse

import (
	"context"
	"encoding/json"
	"hash/fnv"
	"net/http"
	"slices"
	"strings"

	"github.com/go-rod/rod/lib/proto"

	"github.com/RhythmShahriar/echo/consent"
)

// maxStorageKeys bounds the storage keys read per origin (local,
// session and IndexedDB together) in one snapshot.
const maxStorageKeys = 500

// maxSetCookieNotes bounds the Set-Cookie headers remembered for the
// SetBy attribution; past it the oldest half is forgotten.
const maxSetCookieNotes = 4096

// storageJS lists a frame's localStorage and sessionStorage keys with
// the lengths of their values and its IndexedDB database names. It runs
// in Echo's isolated world and never returns a value.
const storageJS = `async () => {
  const MAX = 500; // maxStorageKeys
  const out = { origin: "", keys: [], cut: false };
  try { out.origin = String(location.origin); } catch (e) { return out; }
  for (const [area, name] of [["local", "localStorage"], ["session", "sessionStorage"]]) {
    let st = null;
    try { st = window[name]; } catch (e) { continue; }
    if (!st) continue;
    let n = 0;
    try { n = st.length; } catch (e) { continue; }
    for (let i = 0; i < n; i++) {
      if (out.keys.length >= MAX) { out.cut = true; break; }
      try {
        const k = st.key(i);
        const v = st.getItem(k);
        out.keys.push([area, String(k).slice(0, 512), v == null ? 0 : v.length]);
      } catch (e) { /* gone meanwhile */ }
    }
  }
  try {
    if (typeof indexedDB !== "undefined" && indexedDB.databases) {
      const dbs = await Promise.race([indexedDB.databases(), new Promise((r) => setTimeout(() => r([]), 1000))]);
      for (const d of dbs || []) {
        if (out.keys.length >= MAX) { out.cut = true; break; }
        if (d && d.name) out.keys.push(["indexeddb", String(d.name).slice(0, 512), 0]);
      }
    }
  } catch (e) { /* no IndexedDB here */ }
  return out;
}`

// setCookieNote is a Set-Cookie header a response carried.
type setCookieNote struct {
	host string
	at   int64
}

// cookieKey names a cookie as the browser keys it.
func cookieKey(name, domain, path string) string {
	return name + "\x00" + strings.ToLower(strings.TrimPrefix(domain, ".")) + "\x00" + path
}

// noteKey names a cookie for the attribution, without its path: a
// Set-Cookie line and the stored cookie agree on name and domain.
func noteKey(name, domain string) string {
	return name + "\x00" + strings.ToLower(strings.TrimPrefix(domain, "."))
}

// setCookie is what the attribution reads from one Set-Cookie line.
type setCookie struct {
	Name, Domain, Path string
	Length             int
}

// parseSetCookie reads a Set-Cookie line sent by host: its name, the
// domain the cookie lands on (host for a host-only cookie), its path and
// the length of its value. net/http's parser is strict; a line it
// refuses is read by hand so a tracker's odd cookie is still seen. ok is
// false only for a line without a name.
func parseSetCookie(line, host string) (setCookie, bool) {
	var c setCookie
	if hc, err := http.ParseSetCookie(line); err == nil && hc.Name != "" {
		c = setCookie{Name: hc.Name, Domain: hc.Domain, Path: hc.Path, Length: len(hc.Value)}
	} else {
		pair, rest, _ := strings.Cut(line, ";")
		name, value, ok := strings.Cut(pair, "=")
		name = strings.TrimSpace(name)
		if !ok || name == "" {
			return setCookie{}, false
		}
		c = setCookie{Name: name, Length: len(strings.TrimSpace(value))}
		for _, attr := range strings.Split(rest, ";") {
			k, v, _ := strings.Cut(attr, "=")
			switch strings.ToLower(strings.TrimSpace(k)) {
			case "domain":
				c.Domain = strings.TrimSpace(v)
			case "path":
				c.Path = strings.TrimSpace(v)
			}
		}
	}
	c.Domain = strings.ToLower(strings.TrimLeft(strings.TrimSpace(c.Domain), "."))
	if c.Domain == "" {
		c.Domain = strings.ToLower(strings.TrimLeft(host, "."))
	}
	if c.Path == "" {
		c.Path = "/"
	}
	c.Name = consent.SafeText(c.Name, 256)
	return c, true
}

// noteSetCookies remembers the Set-Cookie lines a response from host
// carried at run time at, for the SetBy attribution of the next
// snapshot.
func (r *recorder) noteSetCookies(host string, lines []string, at int64) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if len(r.setCookies) >= maxSetCookieNotes {
		// Snapshots run after every step; notes older than the last one
		// have done their work.
		for k, n := range r.setCookies {
			if n.at < r.lastSnapshot {
				delete(r.setCookies, k)
			}
		}
		if len(r.setCookies) >= maxSetCookieNotes {
			clear(r.setCookies)
		}
	}
	for _, line := range lines {
		if c, ok := parseSetCookie(line, host); ok {
			r.setCookies[noteKey(c.Name, c.Domain)] = setCookieNote{host: strings.ToLower(host), at: at}
		}
	}
}

// setCookieLines splits a Set-Cookie header as Chrome reports it (one
// line per cookie, joined by newlines).
func setCookieLines(h proto.NetworkHeaders) []string {
	var out []string
	for k, v := range h {
		if !strings.EqualFold(k, "Set-Cookie") {
			continue
		}
		for _, line := range strings.Split(v.Str(), "\n") {
			if line = strings.TrimSpace(line); line != "" {
				out = append(out, line)
			}
		}
	}
	return out
}

// extraInfo reads a response's raw headers: its Set-Cookie lines for the
// attribution and the cookies Chrome refused to store. Each refused one
// is recorded at once with Chrome's reason (the tracker tried).
func (r *recorder) extraInfo(e *proto.NetworkResponseReceivedExtraInfo) {
	host := r.hostOf(e.RequestID)
	at := r.now()
	blocked := map[string]bool{}
	for _, b := range e.BlockedCookies {
		if b == nil {
			continue
		}
		blocked[b.CookieLine] = true
		r.blockedCookie(b, host, at)
	}
	var lines []string
	all := setCookieLines(e.Headers)
	r.harSetCookies(e.RequestID, all)
	for _, line := range all {
		if !blocked[line] {
			lines = append(lines, line)
		}
	}
	if len(lines) > 0 {
		r.noteSetCookies(host, lines, at)
	}
}

// blockedCookie records a cookie Chrome refused, once per name, domain
// and reason.
func (r *recorder) blockedCookie(b *proto.NetworkBlockedSetCookieWithReason, host string, at int64) {
	c, ok := parseSetCookie(b.CookieLine, host)
	if !ok && b.Cookie == nil {
		return
	}
	if b.Cookie != nil {
		c.Name, c.Domain, c.Path = consent.SafeText(b.Cookie.Name, 256), strings.TrimPrefix(b.Cookie.Domain, "."), b.Cookie.Path
		c.Length = len(b.Cookie.Value)
	}
	reasons := make([]string, 0, len(b.BlockedReasons))
	for _, why := range b.BlockedReasons {
		reasons = append(reasons, string(why))
	}
	slices.Sort(reasons)
	why := consent.SafeText(strings.Join(reasons, ","), 200)
	key := cookieKey(c.Name, c.Domain, c.Path) + "\x00" + why
	r.mu.Lock()
	seen := r.blocked[key]
	r.blocked[key] = true
	r.mu.Unlock()
	if seen {
		return
	}
	ck := &consent.Cookie{Name: c.Name, Domain: c.Domain, Path: c.Path, Length: c.Length, Blocked: why}
	if host != "" {
		ck.SetBy = "response:" + host
	}
	r.classifyCookie(ck)
	ck.Name = consent.MaskCookieNames(ck.Name, r.plan.ShowIDs)
	r.emit(consent.Event{Kind: consent.EvCookie, AtMS: at, Cookie: ck})
}

// hostOf is the host a request went to, from the requests seen.
func (r *recorder) hostOf(id proto.NetworkRequestID) string {
	r.mu.Lock()
	defer r.mu.Unlock()
	if p := r.reqs[id]; p != nil && p.req != nil {
		return p.req.Host
	}
	return r.hosts[id]
}

// noteHostLocked remembers the host of a request for the attribution of the
// cookies its response sets. The table is bounded.
func (r *recorder) noteHostLocked(id proto.NetworkRequestID, raw string) {
	if len(r.hosts) >= maxSetCookieNotes {
		clear(r.hosts)
	}
	if i := strings.Index(raw, "://"); i >= 0 {
		rest := raw[i+3:]
		if j := strings.IndexAny(rest, "/?#"); j >= 0 {
			rest = rest[:j]
		}
		if k := strings.LastIndex(rest, "@"); k >= 0 {
			rest = rest[k+1:]
		}
		host := rest
		if !strings.HasPrefix(host, "[") {
			host, _, _ = strings.Cut(host, ":")
		}
		r.hosts[id] = strings.ToLower(host)
	}
}

// classifyCookie fills a cookie's tracker from the catalog, which must
// never panic; if it does, the cookie is simply not a tracker's.
func (r *recorder) classifyCookie(c *consent.Cookie) {
	cl := r.stages.Classifier
	if cl == nil {
		return
	}
	defer func() { _ = recover() }()
	if h := cl.Cookie(c); h != nil {
		c.Tracker, c.Category, c.Need = h.Tracker, h.Category, h.Need
	}
}

// classifyStorage is classifyCookie for a storage key.
func (r *recorder) classifyStorage(k *consent.StorageKey) {
	cl := r.stages.Classifier
	if cl == nil {
		return
	}
	defer func() { _ = recover() }()
	if h := cl.Storage(k); h != nil {
		k.Tracker, k.Category, k.Need = h.Tracker, h.Category, h.Need
	}
}

// toCookie is a stored cookie without its value.
func toCookie(c *proto.NetworkCookie) consent.Cookie {
	out := consent.Cookie{Name: consent.SafeText(c.Name, 256), Domain: c.Domain, Path: c.Path, HTTPOnly: c.HTTPOnly,
		Secure: c.Secure, SameSite: string(c.SameSite), Length: len(c.Value)}
	if !c.Session && c.Expires > 0 {
		out.Expires = int64(c.Expires)
	}
	return out
}

// fingerprint tells a changed cookie from the one seen before. It is
// kept in memory only and never written.
func fingerprint(c *proto.NetworkCookie) uint64 {
	h := fnv.New64a()
	for _, s := range []string{c.Value, c.Domain, c.Path, string(c.SameSite)} {
		_, _ = h.Write([]byte(s))
		_, _ = h.Write([]byte{0})
	}
	var flags byte
	if c.HTTPOnly {
		flags |= 1
	}
	if c.Secure {
		flags |= 2
	}
	_, _ = h.Write([]byte{flags})
	return h.Sum64()
}

// diffCookies records each cookie that is new or changed since the last
// snapshot. A cookie a response set since then is the response's, at the
// time the response came; any other is the page's script's, at the time
// of this snapshot.
func (r *recorder) diffCookies(cookies []*proto.NetworkCookie, at int64) {
	r.mu.Lock()
	since := r.lastSnapshot
	var events []consent.Event
	for _, c := range cookies {
		if c == nil {
			continue
		}
		key := cookieKey(c.Name, c.Domain, c.Path)
		fp := fingerprint(c)
		if old, ok := r.cookies[key]; ok && old == fp {
			continue
		}
		r.cookies[key] = fp
		ck := toCookie(c)
		ck.SetBy, ck.AtMS = "script", at
		if n, ok := r.setCookies[noteKey(c.Name, c.Domain)]; ok && n.at >= since {
			ck.SetBy, ck.AtMS = "response:"+n.host, min(n.at, at)
		}
		events = append(events, consent.Event{Kind: consent.EvCookie, AtMS: ck.AtMS, Cookie: &ck})
	}
	r.mu.Unlock()
	for _, e := range events {
		r.classifyCookie(e.Cookie)
		// Classified by its real name, written with its ID masked.
		e.Cookie.Name = consent.MaskCookieNames(e.Cookie.Name, r.plan.ShowIDs)
		r.emit(e)
	}
}

// storageAnswer is what storageJS returns for one frame.
type storageAnswer struct {
	Origin string            `json:"origin"`
	Keys   []json.RawMessage `json:"keys"`
	Cut    bool              `json:"cut"`
}

// diffStorage records each storage key of origin that is new or whose
// value changed length since the last snapshot.
func (r *recorder) diffStorage(a storageAnswer, at int64) {
	origin := strings.ToLower(a.Origin)
	if origin == "" || origin == "null" {
		return
	}
	var events []consent.Event
	r.mu.Lock()
	for i, raw := range a.Keys {
		if i >= maxStorageKeys {
			break
		}
		var row []json.RawMessage
		var area, key string
		var length int
		if json.Unmarshal(raw, &row) != nil || len(row) != 3 || json.Unmarshal(row[0], &area) != nil ||
			json.Unmarshal(row[1], &key) != nil || json.Unmarshal(row[2], &length) != nil {
			continue
		}
		if area != "local" && area != "session" && area != "indexeddb" {
			continue
		}
		id := area + "\x00" + origin + "\x00" + key
		if old, ok := r.storage[id]; ok && old == length {
			continue
		}
		r.storage[id] = length
		k := &consent.StorageKey{Area: area, Origin: origin, Key: consent.SafeText(key, 256), Length: max(length, 0), AtMS: at}
		events = append(events, consent.Event{Kind: consent.EvStorage, AtMS: at, Storage: k})
	}
	r.mu.Unlock()
	for _, e := range events {
		r.classifyStorage(e.Storage)
		r.emit(e)
	}
}

// snapshot reads the context's cookies (HttpOnly included) and the
// storage keys of every frame's origin and records what is new or
// changed since the last snapshot. The run calls it after every step.
func (s *session) snapshot(ctx context.Context) error {
	root := s.h.root
	root.ctx = ctx
	at := s.rec.now()
	res, err := proto.StorageGetCookies{BrowserContextID: s.ctx.BrowserContextID}.Call(root)
	if err != nil {
		return err
	}
	s.rec.diffCookies(res.Cookies, at)
	frames, err := s.frames(ctx)
	if err != nil {
		return err
	}
	done := map[string]bool{}
	for _, f := range frames {
		o := origin(f.url)
		if o == "" || done[o] {
			continue
		}
		done[o] = true
		raw, err := s.evalIsolated(ctx, f, storageJS)
		if err != nil {
			if ctx.Err() != nil {
				return ctx.Err()
			}
			continue // the frame went away or forbids storage
		}
		var a storageAnswer
		if json.Unmarshal(raw, &a) == nil {
			s.rec.diffStorage(a, at)
		}
	}
	s.rec.mu.Lock()
	s.rec.lastSnapshot = at
	s.rec.mu.Unlock()
	return nil
}
