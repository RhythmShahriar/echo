// Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
// SPDX-License-Identifier: MIT

package consent

import (
	"fmt"
	"net/url"
	"regexp"
	"slices"
	"strings"
	"sync"
	"unicode/utf8"

	"github.com/RhythmShahriar/echo/mask"
	"github.com/RhythmShahriar/echo/recognize"
)

// Hashed is what a personal value or a hash of one reads as once masked:
// a SHA-256 of an email is still the same person on every site, so it
// is pseudonymised, never shown.
const Hashed = "••••(sha256)"

// idShown is how many trailing characters of an ID MaskID keeps.
const idShown = 4

// personalParams are tracker parameters that carry personal data: Meta's
// advanced matching (em, ph, fn, ln, ge, db, ct, st, zp) whether hashed
// or not. ud[...] (the same, in a Pixel's user data) is matched by prefix.
var personalParams = map[string]bool{"em": true, "ph": true, "fn": true, "ln": true, "ge": true, "db": true, "ct": true, "st": true, "zp": true}

// idParams are measurement, pixel and client IDs: they keep their last 4
// characters. showIDs keeps the ones in shownIDs whole.
var idParams = map[string]bool{"cid": true, "tid": true, "id": true, "sid": true, "uid": true, "user_id": true, "external_id": true,
	"_ga": true, "_gid": true, "_fbp": true, "_fbc": true, "fbp": true, "fbc": true, "ecid": true, "_p": true, "gclid": true,
	"dclid": true, "fbclid": true, "msclkid": true, "ttclid": true, "li_fat_id": true,
	// The built-in catalog's ID params whose names say so on any host
	// (TikTok, Matomo and Piwik PRO, Outbrain, Klaviyo, X). Short ones
	// (a, pid, ti) are masked on their trackers' hosts only: AddIDRule.
	"sdkid": true, "pixel_code": true, "idsite": true, "marketerid": true, "company_id": true, "txn_id": true}

// IDRule names IDs a tracker sends: Params (any case) and group 1 of Path
// in the URL path are masked to their last 4 characters on the hosts
// listed (Hosts exact, Suffixes like ".tiktok.com" for subdomains), or
// on every host with AnyHost. A tracker catalog adds one per entry with
// params: id: or id_path:, so a custom tracker's ID is masked too.
type IDRule struct {
	Hosts    []string
	Suffixes []string
	AnyHost  bool
	Params   []string
	Path     *regexp.Regexp
}

// idRules are the rules AddIDRule added, at most maxIDRules.
var idRules = struct {
	sync.RWMutex
	list []IDRule
	seen map[string]bool
}{seen: map[string]bool{}}

// maxIDRules bounds how many rules masking tries on a URL.
const maxIDRules = 1024

// AddIDRule makes MaskURL mask the IDs r names. Rules are only ever
// added; one already added, one with no params, path or host, a Path
// without exactly one group and any past the 1024th are ignored.
func AddIDRule(r IDRule) {
	if r.Path != nil && r.Path.NumSubexp() != 1 {
		r.Path = nil
	}
	if (len(r.Params) == 0 && r.Path == nil) || (len(r.Hosts) == 0 && len(r.Suffixes) == 0 && !r.AnyHost) {
		return
	}
	r = IDRule{Hosts: slices.Clone(r.Hosts), Suffixes: slices.Clone(r.Suffixes), AnyHost: r.AnyHost, Params: slices.Clone(r.Params), Path: r.Path}
	for i, p := range r.Params {
		r.Params[i] = strings.ToLower(p)
	}
	path := ""
	if r.Path != nil {
		path = r.Path.String()
	}
	key := fmt.Sprintf("%q %q %v %q %q", r.Hosts, r.Suffixes, r.AnyHost, r.Params, path)
	idRules.Lock()
	defer idRules.Unlock()
	if !idRules.seen[key] && len(idRules.list) < maxIDRules {
		idRules.seen[key] = true
		idRules.list = append(idRules.list, r)
	}
}

// idsFor returns the ID params and path patterns the rules give host
// (lower-case, no port).
func idsFor(host string) (params map[string]bool, paths []*regexp.Regexp) {
	idRules.RLock()
	defer idRules.RUnlock()
	for _, r := range idRules.list {
		if !r.AnyHost && !slices.Contains(r.Hosts, host) && !slices.ContainsFunc(r.Suffixes, func(s string) bool { return strings.HasSuffix(host, s) }) {
			continue
		}
		for _, p := range r.Params {
			if params == nil {
				params = map[string]bool{}
			}
			params[p] = true
		}
		if r.Path != nil {
			paths = append(paths, r.Path)
		}
	}
	return params, paths
}

// maskPathIDs masks group 1 of each pattern where it matches path.
func maskPathIDs(path string, res []*regexp.Regexp) string {
	for _, re := range res {
		m := re.FindStringSubmatchIndex(path)
		if m == nil || m[2] < 0 || m[3] <= m[2] {
			continue
		}
		path = path[:m[2]] + MaskID(path[m[2]:m[3]]) + path[m[3]:]
	}
	return path
}

// shownIDs are the IDs --show-ids keeps: they name the property or the
// pixel and the browser, never the person (uid and external_id do).
var shownIDs = map[string]bool{"cid": true, "tid": true, "id": true, "_ga": true, "_gid": true, "sid": true}

// nestedParams carry a whole URL (the page, the referrer), which may hold
// the site's own tokens, so they are masked as URLs themselves.
var nestedParams = map[string]bool{"dl": true, "dr": true, "dt": true, "ref": true, "rl": true, "url": true, "u": true,
	"page_location": true, "page_referrer": true}

var (
	hex64 = regexp.MustCompile(`(?i)\b[0-9a-f]{64}\b`)
	email = regexp.MustCompile(`[A-Za-z0-9._%+-]+@([A-Za-z0-9-]+\.[A-Za-z0-9.-]+)`)
	// A phone number starts with '+'; a '+' a query decodes as a space
	// counts too, so "phone=+4917..." is masked like "phone=%2B4917...".
	phone = regexp.MustCompile(`^[+ ][0-9][0-9 ().-]{6,20}$`)
)

// maxNesting bounds how deep MaskURL follows URLs inside parameters.
const maxNesting = 3

// MaskURL returns raw with credentials (recognize.RedactURL, echo/mask) and
// personal parameters masked, including URLs nested and double-encoded
// in dl, dr, dt, ref, rl, url, u, page_location. IDs keep their last 4
// characters; em, ph, fn, ln, ud[*] and 64-hex hashes become ••••(sha256).
// showIDs keeps cid/tid/id values and the IDs a tracker rule finds in a
// path (never credentials or personal data).
// A login in the URL (user:pass@) is masked whole. Text that isn't a URL
// is masked as text.
//
// Text over 64 KiB is cut first (at the last '&', '/' or space before
// the limit when there is one) and ends in "…". Masking is linear but
// slow enough on hostile megabytes to matter; no URL a check keeps is
// that long (the recorder cuts at 8 KiB).
func MaskURL(raw string, showIDs bool) string {
	if len(raw) > maxMaskInput {
		cut := raw[:maxMaskInput]
		if i := strings.LastIndexAny(cut, "&/ "); i > maxMaskInput-1024 {
			cut = cut[:i]
		}
		return maskURL(strings.ToValidUTF8(cut, ""), showIDs, 0) + "…"
	}
	return maskURL(raw, showIDs, 0)
}

// maxMaskInput is the most of a text MaskURL reads.
const maxMaskInput = 64 << 10

func maskURL(raw string, showIDs bool, depth int) string {
	if raw == "" {
		return ""
	}
	u, err := url.Parse(raw)
	if err != nil || u.Host == "" || u.Opaque != "" {
		return maskValue(mask.Text(raw))
	}
	out := u.Scheme + "://"
	if u.User != nil {
		out += mask.Hidden + "@"
	}
	// Path credentials first (a Telegram bot token, /key/<x>): RedactURL
	// on the bare path keeps the rest as it was written.
	path := recognize.RedactURL(recognize.Recognizer{}, u.Scheme+"://"+u.Host+u.EscapedPath())
	ids, idPaths := idsFor(strings.ToLower(u.Hostname()))
	path = strings.TrimPrefix(path, u.Scheme+"://"+u.Host)
	if !showIDs {
		// The IDs a path carries name the property or the conversion,
		// like the tid and id --show-ids keeps.
		path = maskPathIDs(path, idPaths)
	}
	out = out + u.Host + path
	if u.ForceQuery || u.RawQuery != "" {
		out += "?" + maskQuery(u.RawQuery, showIDs, depth, ids)
	}
	if u.Fragment != "" {
		frag := u.EscapedFragment()
		if strings.Contains(frag, "=") {
			frag = maskQuery(frag, showIDs, depth, ids)
		}
		out += "#" + frag
	}
	return mask.Text(out)
}

// maskQuery masks each key=value pair of a query (or a fragment written
// like one), keeping the order and the encoding of the pairs it leaves.
// ids are the ID params the tracker rules add on this URL's host.
func maskQuery(q string, showIDs bool, depth int, ids map[string]bool) string {
	pairs := strings.Split(q, "&")
	for i, p := range pairs {
		k, v, ok := strings.Cut(p, "=")
		if !ok || v == "" {
			continue
		}
		key, err := url.QueryUnescape(k)
		if err != nil {
			key = k
		}
		val, err := url.QueryUnescape(v)
		if err != nil {
			val = v
		}
		lk := strings.ToLower(key)
		switch {
		case personalParams[lk] || strings.HasPrefix(lk, "ud["):
			pairs[i] = k + "=" + Hashed
		case idParams[lk] || ids[lk]:
			if !showIDs || !shownIDs[lk] {
				pairs[i] = k + "=" + MaskID(val)
			}
		case nestedParams[lk] && depth < maxNesting:
			inner := unescapeAll(val)
			if isHTTP(inner) {
				pairs[i] = k + "=" + url.QueryEscape(maskURL(inner, showIDs, depth+1))
			} else {
				pairs[i] = k + "=" + url.QueryEscape(maskValue(mask.Text(inner)))
			}
		default:
			if m := maskValue(val); m != val {
				pairs[i] = k + "=" + escapeMasked(m)
			} else if raw, err := url.PathUnescape(v); err == nil && phone.MatchString(raw) {
				pairs[i] = k + "=" + escapeMasked(maskValue(raw))
			}
		}
	}
	return strings.Join(pairs, "&")
}

// maskedReadable undoes the escapes QueryEscape puts on what a mask
// writes: the bullets, the @ of a masked address and the parentheses of
// (phone) and (sha256). They are all allowed in a query, so a masked
// value reads "••••@example.org" or "••••(phone)" like the id and token
// masks, while '&', '=', '+' and '#' stay escaped.
var maskedReadable = strings.NewReplacer("%E2%80%A2", "•", "%40", "@", "%28", "(", "%29", ")")

// escapeMasked escapes a masked query value, keeping the mask readable.
func escapeMasked(m string) string {
	return maskedReadable.Replace(url.QueryEscape(m))
}

// unescapeAll decodes a value encoded more than once, as a page URL
// inside a beacon inside another beacon is. It stops at the first level
// that reads as a URL, so the URL's own query keeps its encoding (a
// %2B stays a '+' rather than turning into a space).
func unescapeAll(s string) string {
	for range maxNesting {
		if isHTTP(s) {
			break
		}
		d, err := url.QueryUnescape(s)
		if err != nil || d == s {
			break
		}
		s = d
	}
	return s
}

// isHTTP reports whether s starts with http:// or https:// in any case.
func isHTTP(s string) bool {
	l := strings.ToLower(s)
	return strings.HasPrefix(l, "http://") || strings.HasPrefix(l, "https://")
}

// maskValue masks personal data in one value by its shape: 64-hex
// hashes, email addresses (the domain is kept) and phone numbers.
func maskValue(v string) string {
	v = hex64.ReplaceAllString(v, Hashed)
	v = email.ReplaceAllString(v, "••••@$1")
	if phone.MatchString(v) {
		return "••••(phone)"
	}
	return v
}

// MaskID masks an identifier to its last 4 characters: "••••5678". One
// of 4 characters or fewer is hidden whole.
func MaskID(v string) string {
	if v == "" {
		return ""
	}
	if strings.HasPrefix(v, "••••") {
		return v // already masked
	}
	n := utf8.RuneCountInString(v)
	if n <= idShown {
		return "••••"
	}
	r := []rune(v)
	return "••••" + string(r[n-idShown:])
}

// SafeText makes page-supplied text harmless to print and caps it at max
// runes (an ellipsis marks a cut; max <= 0 means no cap). C0 controls
// (but tab), DEL, C1 controls, invalid UTF-8 and the bidi embedding,
// override and isolate controls are written as \xNN or \uNNNN, so a
// button text can't rewrite the terminal or reorder a line (the same
// rule as report.Safe, which this package can't import).
func SafeText(s string, max int) string {
	var b strings.Builder
	n := 0
	for i := 0; i < len(s); {
		if max > 0 && n == max {
			b.WriteString("…")
			break
		}
		r, size := utf8.DecodeRuneInString(s[i:])
		switch {
		case r == utf8.RuneError && size <= 1:
			fmt.Fprintf(&b, `\x%02x`, s[i])
		case r < 0x20 && r != '\t', r == 0x7f:
			fmt.Fprintf(&b, `\x%02x`, r)
		case r >= 0x80 && r <= 0x9f, bidiControl(r):
			fmt.Fprintf(&b, `\u%04x`, r)
		default:
			b.WriteString(s[i : i+size])
		}
		i += size
		n++
	}
	return b.String()
}

// bidiControl reports whether r is a bidi embedding, override, isolate
// or mark control.
func bidiControl(r rune) bool {
	return (r >= 0x202a && r <= 0x202e) || (r >= 0x2066 && r <= 0x2069) || r == 0x200e || r == 0x200f || r == 0x061c
}

// maxTextInput is the most of a page's text MaskText reads: a dialog
// is shown cut to 200 runes, so a longer one is cut here first and
// SafeText cuts the masked rest.
const maxTextInput = 8 << 10

var (
	// textURL is a URL inside free text, up to a space or a quote.
	textURL = regexp.MustCompile(`(?i)\bhttps?://[^\s"'<>]+`)
	// textPhone is a phone number inside free text: a '+', then at least
	// 7 digits with spaces, dots, dashes or parentheses between them.
	textPhone = regexp.MustCompile(`\+[0-9][0-9 ().-]{5,20}[0-9]`)
)

// MaskText masks free text a page wrote (an alert or confirm message):
// every URL in it as MaskURL masks a URL, the kit's secrets, JWTs and
// one-time codes (echo/mask), 64-hex hashes, email addresses (the
// domain is kept) and phone numbers. A URL loses the punctuation that
// ends a sentence before it is masked. Text over 8 KiB is cut first.
func MaskText(s string, showIDs bool) string {
	if s == "" {
		return ""
	}
	if len(s) > maxTextInput {
		s = strings.ToValidUTF8(s[:maxTextInput], "")
	}
	s = textURL.ReplaceAllStringFunc(s, func(u string) string {
		end := strings.TrimRight(u, ".,;:!?)]}")
		return MaskURL(end, showIDs) + u[len(end):]
	})
	s = mask.Text(s)
	s = hex64.ReplaceAllString(s, Hashed)
	s = email.ReplaceAllString(s, "••••@$1")
	return textPhone.ReplaceAllString(s, "••••(phone)")
}

// escapedMask is the mask bullets as URL encoding writes them.
var escapedMask = url.PathEscape("•")

// ShownPath is a URL's escaped path for output, with the mask bullets
// of a masked id_path back as bullets: ".../viewthroughconversion/••••0001/"
// rather than "%E2%80%A2%E2%80%A2...". Everything else stays escaped.
func ShownPath(u *url.URL) string {
	return strings.ReplaceAll(u.EscapedPath(), escapedMask, "•")
}

// gaCookie is a GA4 cookie name, _ga_ and the measurement ID without
// its G- (the cookie of property G-ABC123 is _ga_ABC123).
var gaCookie = regexp.MustCompile(`\b_ga_([A-Za-z0-9]{5,})\b`)

// MaskCookieNames masks the measurement ID in GA4 cookie names in s, a
// cookie name or a Cookie or Set-Cookie header, to its last 4
// characters the way a tid is masked: _ga_••••D001. showIDs keeps it.
func MaskCookieNames(s string, showIDs bool) string {
	if showIDs || !strings.Contains(s, "_ga_") {
		return s
	}
	return gaCookie.ReplaceAllStringFunc(s, func(m string) string {
		return "_ga_" + MaskID(m[len("_ga_"):])
	})
}
