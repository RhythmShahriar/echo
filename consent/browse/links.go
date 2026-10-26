// Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
// SPDX-License-Identifier: MIT

package browse

import (
	"net/url"
	"strings"
	"unicode"

	"github.com/RhythmShahriar/rhythm/kit/crawl"
)

// linksJS lists the page's links in DOM order, each as its href as
// written and its text, in Echo's isolated world. At most 2,000.
const linksJS = `() => Array.from(document.querySelectorAll("a[href]")).slice(0, 2000).map((a) =>
[String(a.getAttribute("href") || "").slice(0, 2048), String(a.textContent || "").trim().slice(0, 200)])`

// skipWords are the words of a link's path, query or text that mark a
// page a check must never visit: it would log the session out, start a
// login, touch a cart or open an account.
var skipWords = map[string]bool{
	"logout": true, "signout": true, "login": true, "cart": true, "basket": true, "checkout": true, "account": true,
}

// skipPairs are the two-word forms of the same: log-out, sign out,
// wp-admin (wp-login.php reads as wp, login, php).
var skipPairs = map[string]bool{"log out": true, "sign out": true, "wp admin": true}

// skipSchemes are links that aren't pages at all.
var skipSchemes = []string{"mailto:", "tel:", "javascript:"}

// pickLinks chooses up to n links to visit after the choice, in DOM
// order: http or https, a page rather than a file (kit crawl), on the
// same host as base, not base itself, nothing a visitor would regret
// (logout, login, cart, checkout, account) and no repeats. links are
// [href, text] pairs as linksJS gives them.
func pickLinks(base *url.URL, links [][2]string, n int) []string {
	if base == nil || n <= 0 {
		return nil
	}
	self, _ := crawl.Normalize(base, base.String())
	seen := map[string]bool{self: true}
	var out []string
	for _, l := range links {
		href, text := strings.TrimSpace(l[0]), l[1]
		lower := strings.ToLower(href)
		if hasAnyPrefix(lower, skipSchemes) {
			continue
		}
		norm, ok := crawl.Normalize(base, href)
		if !ok || seen[norm] {
			continue
		}
		u, err := url.Parse(norm)
		if err != nil || !strings.EqualFold(u.Host, base.Host) || !crawl.IsPage(u) {
			continue
		}
		if skipped(u.EscapedPath()+" "+u.RawQuery) || skipped(text) {
			continue
		}
		seen[norm] = true
		out = append(out, norm)
		if len(out) == n {
			break
		}
	}
	return out
}

// skipped reports whether s holds one of skipWords or skipPairs as
// whole words, so /cart is skipped and /cartography is not.
func skipped(s string) bool {
	words := strings.FieldsFunc(strings.ToLower(s), func(r rune) bool { return !unicode.IsLetter(r) && !unicode.IsDigit(r) })
	for i, w := range words {
		if skipWords[w] || strings.HasPrefix(w, "my") && skipWords[strings.TrimPrefix(w, "my")] {
			return true
		}
		if i+1 < len(words) && skipPairs[w+" "+words[i+1]] {
			return true
		}
	}
	return false
}

// hasAnyPrefix reports whether s starts with one of prefixes.
func hasAnyPrefix(s string, prefixes []string) bool {
	for _, p := range prefixes {
		if strings.HasPrefix(s, p) {
			return true
		}
	}
	return false
}
