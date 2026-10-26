// Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
// SPDX-License-Identifier: MIT

package tracker

import (
	"bytes"
	"encoding/json"
	"net"
	"net/url"
	"path"
	"regexp"
	"slices"
	"strconv"
	"strings"

	"golang.org/x/net/idna"
	"golang.org/x/net/publicsuffix"

	"github.com/RhythmShahriar/echo/consent"
)

// The host tiers rules are tried in: every rule for the exact host
// first, then *.suffix rules, then rules on any host. A vendor's own
// host always wins over a pattern that only fits by parameters.
const (
	tierExact = iota
	tierSuffix
	tierAny
)

// rule is one compiled hits, scripts or frames rule.
type rule struct {
	e        *entry
	kind     consent.HitKind
	exact    []string
	suffix   []string // ".google-analytics.com"
	anyHost  bool
	paths    []string
	methods  []string
	all      []string
	anyOf    []string
	match    []paramRe
	not      []paramVal
	bodyJSON []string
}

type paramRe struct {
	name string
	re   *regexp.Regexp
}

type paramVal struct{ name, value string }

// masked is the prefix consent.MaskID and consent.Hashed give a value.
// The recorder hands the classifier masked URLs, so a measurement ID
// may read ••••0001: such a value can't be checked against a pattern
// and counts as present.
const masked = "••••"

// maxPairs bounds how many parameters one query or body line is read
// for, so a hostile request can't make classifying it slow.
const maxPairs = 512

// maxValue caps one stored value (an event name, a parameter).
const maxValue = 256

// patterns lists the rule's host and path patterns for summaries.
func (r *rule) patterns() []string {
	var hosts []string
	hosts = append(hosts, r.exact...)
	for _, s := range r.suffix {
		hosts = append(hosts, "*"+s)
	}
	if r.anyHost {
		hosts = append(hosts, "any host ")
	}
	paths := r.paths
	if len(paths) == 0 {
		paths = []string{""}
	}
	var out []string
	for _, h := range hosts {
		for _, p := range paths {
			out = append(out, h+p)
		}
	}
	return out
}

// request is a request taken apart once for matching.
type request struct {
	method string
	host   string
	path   string
	query  map[string]string
	body   []byte

	parsed bool
	form   map[string]string // body as form lines, or top-level JSON strings
	lines  []map[string]string
	json   map[string]json.RawMessage
}

// newRequest parses r; ok is false when its URL has no host.
func newRequest(r *consent.Request, body []byte) (*request, bool) {
	if r == nil {
		return nil, false
	}
	u, err := url.Parse(r.URL)
	if err != nil || u.Host == "" {
		return nil, false
	}
	host := normHost(u.Hostname())
	if host == "" {
		return nil, false
	}
	p := u.Path
	if p == "" {
		p = "/"
	}
	m := strings.ToUpper(r.Method)
	if m == "" {
		m = "GET"
	}
	return &request{method: m, host: host, path: p, query: parseQuery(u.RawQuery), body: body}, true
}

// normHost lowercases a host, drops a trailing dot and writes an
// internationalized name in its ASCII form, the form catalog hosts use.
func normHost(h string) string {
	h = strings.TrimSuffix(strings.ToLower(h), ".")
	if a, err := idna.Lookup.ToASCII(h); err == nil {
		h = a
	}
	return h
}

// parseQuery reads a query string or one form line: the first value of
// each name. It never fails; an undecodable part is kept as written.
func parseQuery(q string) map[string]string {
	out := map[string]string{}
	for i, p := range strings.Split(q, "&") {
		if i == maxPairs {
			break
		}
		if p == "" {
			continue
		}
		k, v, _ := strings.Cut(p, "=")
		k, v = unescape(k), unescape(v)
		if _, seen := out[k]; !seen && k != "" {
			out[k] = v
		}
	}
	return out
}

// unescape decodes a query component, keeping it as written when it
// isn't valid.
func unescape(s string) string {
	if d, err := url.QueryUnescape(s); err == nil {
		return d
	}
	return s
}

// parseBody reads the body once: a JSON object, or form lines (a GA4
// batch is one event per line).
func (q *request) parseBody() {
	if q.parsed {
		return
	}
	q.parsed = true
	q.form = map[string]string{}
	b := bytes.TrimSpace(q.body)
	if len(b) == 0 {
		return
	}
	if b[0] == '{' {
		var obj map[string]json.RawMessage
		if json.Unmarshal(b, &obj) != nil {
			return
		}
		q.json = obj
		for k, raw := range obj {
			var s string
			if json.Unmarshal(raw, &s) == nil {
				q.form[k] = s
			} else if f, err := strconv.ParseFloat(string(raw), 64); err == nil {
				q.form[k] = strconv.FormatFloat(f, 'f', -1, 64)
			}
		}
		return
	}
	if b[0] == '[' {
		return
	}
	for line := range strings.SplitSeq(string(b), "\n") {
		line = strings.TrimSuffix(line, "\r")
		if line == "" {
			continue
		}
		f := parseQuery(line)
		q.lines = append(q.lines, f)
		for k, v := range f {
			if _, seen := q.form[k]; !seen {
				q.form[k] = v
			}
		}
	}
}

// get returns a parameter from the query, else from the body.
func (q *request) get(name string) (string, bool) {
	if v, ok := q.query[name]; ok {
		return v, true
	}
	if len(q.body) == 0 {
		return "", false
	}
	q.parseBody()
	v, ok := q.form[name]
	return v, ok
}

// matches reports whether the rule fits the request on everything but
// the host.
func (r *rule) matches(q *request) bool {
	if len(r.methods) > 0 && !slices.Contains(r.methods, q.method) {
		return false
	}
	if len(r.paths) > 0 && !matchPath(r.paths, q.path) {
		return false
	}
	for _, n := range r.all {
		if _, ok := q.get(n); !ok {
			return false
		}
	}
	if len(r.anyOf) > 0 && !slices.ContainsFunc(r.anyOf, func(n string) bool { _, ok := q.get(n); return ok }) {
		return false
	}
	for _, m := range r.match {
		v, ok := q.get(m.name)
		if !ok {
			return false
		}
		if !strings.HasPrefix(v, masked) && !m.re.MatchString(v) {
			return false
		}
	}
	for _, n := range r.not {
		if v, ok := q.get(n.name); ok && v == n.value {
			return false
		}
	}
	if len(r.bodyJSON) > 0 {
		q.parseBody()
		if q.json == nil {
			return false
		}
		for _, k := range r.bodyJSON {
			if _, ok := q.json[k]; !ok {
				return false
			}
		}
	}
	return true
}

// matchPath reports whether p fits one of the patterns: path.Match, a
// trailing slash forgiven (/pagead/conversion/123/ fits
// /pagead/conversion/*); /x/** stands for /x and every path below.
func matchPath(pats []string, p string) bool {
	trimmed := p
	if len(p) > 1 {
		trimmed = strings.TrimSuffix(p, "/")
	}
	for _, pat := range pats {
		if base, ok := strings.CutSuffix(pat, "/**"); ok {
			if p == base || strings.HasPrefix(p, base+"/") {
				return true
			}
			continue
		}
		if ok, _ := path.Match(pat, p); ok {
			return true
		}
		if ok, _ := path.Match(pat, trimmed); ok {
			return true
		}
	}
	return false
}

// find returns the first rule that fits q, trying the tiers in order.
func (c *Catalog) find(q *request) *rule {
	for _, r := range c.tiers[tierExact] {
		if slices.Contains(r.exact, q.host) && r.matches(q) {
			return r
		}
	}
	for _, r := range c.tiers[tierSuffix] {
		if slices.ContainsFunc(r.suffix, func(s string) bool { return strings.HasSuffix(q.host, s) }) && r.matches(q) {
			return r
		}
	}
	for _, r := range c.tiers[tierAny] {
		if r.matches(q) {
			return r
		}
	}
	return nil
}

// Request classifies a request: the tracker it belongs to and whether
// it is a beacon, a script or a frame. body is the parse window and may
// be nil; site is the page's URL (for FirstParty) and may be nil. nil
// means not a tracker.
func (c *Catalog) Request(r *consent.Request, body []byte, site *url.URL) *consent.Hit {
	q, ok := newRequest(r, body)
	if !ok {
		return nil
	}
	m := c.find(q)
	if m == nil {
		return nil
	}
	e := m.e
	h := &consent.Hit{Tracker: e.info.ID, Name: e.info.Name, Category: e.info.Category, Need: e.info.Need, Kind: m.kind}
	h.FirstParty = site != nil && sameSite(q.host, normHost(site.Hostname()))
	h.ID = e.id(q)
	if m.kind == consent.HitBeacon {
		h.Events = e.eventNames(q)
		h.Params = e.params(q)
		h.Google = google(q)
	}
	return h
}

// Hold reports whether r may be answered locally instead of sent: only
// a beacon of a tracker that needs consent or is exempt. Scripts,
// frames and documents always load (holding a tag script would make
// nothing fire and the site falsely pass); a necessary service
// (payments, captchas) is never interfered with.
func (c *Catalog) Hold(r *consent.Request) bool {
	if r == nil || r.Type == "script" || r.Type == "document" {
		return false
	}
	h := c.Request(r, nil, nil)
	return h != nil && h.Kind == consent.HitBeacon && h.Need != consent.NeedNo
}

// sameSite reports whether host is site's registrable domain or below
// it (metrics.shop.test on https://shop.test).
func sameSite(host, site string) bool {
	if host == "" || site == "" {
		return false
	}
	if host == site {
		return true
	}
	if net.ParseIP(host) != nil || net.ParseIP(site) != nil {
		return false
	}
	a, err1 := publicsuffix.EffectiveTLDPlusOne(host)
	b, err2 := publicsuffix.EffectiveTLDPlusOne(site)
	return err1 == nil && err2 == nil && a == b
}

// id is the entry's measurement or pixel ID, masked to its last 4.
func (e *entry) id(q *request) string {
	for _, n := range e.ids {
		if v, ok := q.get(n); ok && v != "" {
			return consent.MaskID(consent.SafeText(v, maxValue))
		}
	}
	if e.idPath != nil {
		if g := e.idPath.FindStringSubmatch(q.path); g != nil && g[1] != "" {
			return consent.MaskID(consent.SafeText(g[1], maxValue))
		}
	}
	return ""
}

// eventNames lists the events a beacon carries: the first event
// parameter found in the query, then one per body line (a GA4 batch),
// at most consent.DefaultLimits.BatchEvents.
func (e *entry) eventNames(q *request) []string {
	if len(e.events) == 0 {
		return nil
	}
	limit := consent.DefaultLimits.BatchEvents
	var out []string
	first := func(get func(string) (string, bool)) {
		for _, n := range e.events {
			if v, ok := get(n); ok && v != "" {
				out = append(out, consent.SafeText(v, 100))
				return
			}
		}
	}
	first(func(n string) (string, bool) { v, ok := q.query[n]; return v, ok })
	if len(q.body) > 0 {
		q.parseBody()
		if q.json != nil {
			first(func(n string) (string, bool) { v, ok := q.form[n]; return v, ok })
		}
		for _, l := range q.lines {
			if len(out) >= limit {
				break
			}
			first(func(n string) (string, bool) { v, ok := l[n]; return v, ok })
		}
	}
	if len(out) > limit {
		out = out[:limit]
	}
	return out
}

// keptParams are the parameters a beacon's Params keeps; everything
// else is dropped, so personal data can't slip into a report through a
// parameter nobody thought of.
var keptParams = []string{"gcs", "gcd", "npa", "dma", "en", "ev", "tid", "id", "dl", "dr"}

// params is the selected, masked parameters of a beacon, with the names
// (never the values) of the personal fields it carried.
func (e *entry) params(q *request) map[string]string {
	out := map[string]string{}
	for _, n := range keptParams {
		if slices.Contains(e.personal, n) {
			continue
		}
		v, ok := q.get(n)
		if !ok || v == "" {
			continue
		}
		switch n {
		case "tid", "id":
			v = consent.MaskID(consent.SafeText(v, maxValue))
		case "dl", "dr":
			v = consent.SafeText(consent.MaskURL(v, false), 1024)
		default:
			v = consent.SafeText(v, maxValue)
		}
		out[n] = v
	}
	var personal []string
	for _, n := range e.personal {
		if _, ok := q.get(n); ok {
			personal = append(personal, n)
		}
	}
	if len(personal) > 0 {
		out["personal"] = strings.Join(personal, ",")
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

// google copies the Consent Mode parameters raw; the recorder decodes
// them with the signal decoders.
func google(q *request) *consent.GoogleConsent {
	g := &consent.GoogleConsent{}
	for _, f := range []struct {
		name string
		to   *string
	}{{"gcs", &g.GCS}, {"gcd", &g.GCD}, {"npa", &g.NPA}, {"dma", &g.DMA}} {
		if v, ok := q.get(f.name); ok && v != "" {
			*f.to = consent.SafeText(v, 64)
		}
	}
	if *g == (consent.GoogleConsent{}) {
		return nil
	}
	return g
}
