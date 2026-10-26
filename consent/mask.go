// Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
// SPDX-License-Identifier: MIT

package consent

import (
	"fmt"
	"net/url"
	"regexp"
	"strings"
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
	"dclid": true, "fbclid": true, "msclkid": true, "ttclid": true, "li_fat_id": true}

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
	phone = regexp.MustCompile(`^\+[0-9][0-9 ().-]{6,20}$`)
)

// maxNesting bounds how deep MaskURL follows URLs inside parameters.
const maxNesting = 3

// MaskURL returns raw with credentials (recognize.RedactURL, echo/mask) and
// personal parameters masked, including URLs nested and double-encoded
// in dl, dr, dt, ref, rl, url, u, page_location. IDs keep their last 4
// characters; em, ph, fn, ln, ud[*] and 64-hex hashes become ••••(sha256).
// showIDs keeps cid/tid/id values (never credentials or personal data).
// A login in the URL (user:pass@) is masked whole. Text that isn't a URL
// is masked as text.
//
// Text over 64 KiB is cut first, at the last '&', '/' or space before
// the limit when there is one, and ends in "…". Masking is linear but
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
	out = out + u.Host + strings.TrimPrefix(path, u.Scheme+"://"+u.Host)
	if u.ForceQuery || u.RawQuery != "" {
		out += "?" + maskQuery(u.RawQuery, showIDs, depth)
	}
	if u.Fragment != "" {
		frag := u.EscapedFragment()
		if strings.Contains(frag, "=") {
			frag = maskQuery(frag, showIDs, depth)
		}
		out += "#" + frag
	}
	return mask.Text(out)
}

// maskQuery masks each key=value pair of a query (or a fragment written
// like one), keeping the order and the encoding of the pairs it leaves.
func maskQuery(q string, showIDs bool, depth int) string {
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
		case idParams[lk]:
			if !showIDs || !shownIDs[lk] {
				pairs[i] = k + "=" + MaskID(val)
			}
		case nestedParams[lk] && depth < maxNesting:
			inner := unescapeAll(val)
			if strings.HasPrefix(strings.ToLower(inner), "http://") || strings.HasPrefix(strings.ToLower(inner), "https://") {
				pairs[i] = k + "=" + url.QueryEscape(maskURL(inner, showIDs, depth+1))
			} else {
				pairs[i] = k + "=" + url.QueryEscape(maskValue(mask.Text(inner)))
			}
		default:
			if m := maskValue(val); m != val {
				pairs[i] = k + "=" + url.QueryEscape(m)
			}
		}
	}
	return strings.Join(pairs, "&")
}

// unescapeAll decodes a value encoded more than once, as a page URL
// inside a beacon inside another beacon is.
func unescapeAll(s string) string {
	for range maxNesting {
		d, err := url.QueryUnescape(s)
		if err != nil || d == s {
			break
		}
		s = d
	}
	return s
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
