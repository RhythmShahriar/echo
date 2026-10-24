// Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
// SPDX-License-Identifier: MIT

// Package codes finds what an end-to-end test needs from a message it
// just caused: the one-time code in a login or checkout email or SMS and
// the links that sign someone in, verify an address, reset a password or
// unsubscribe.
//
// Echo runs it on every message as it's captured, before masking and
// keeps the result in memory beside the inbox only: it's served by GET
// /v1/messages/latest and never stored with the message, in a run or in
// a report.
package codes

import (
	"cmp"
	"html"
	"net/url"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/RhythmShahriar/echo/model"
)

// Link kinds.
const (
	Magic       = "magic"       // signs in by itself: a login link with a token
	Verify      = "verify"      // confirms an address or activates an account
	Reset       = "reset"       // sets a new password
	Login       = "login"       // a sign-in page
	Unsubscribe = "unsubscribe" // stops the mail
)

// Link is a link of a kind a test may follow.
type Link struct {
	Kind string `json:"kind"`
	URL  string `json:"url"`
}

// maxCodes is the most codes read from one field: a code is one or two
// words in a message, not a table.
const maxCodes = 10

// maxScan is how much of each field is read: what Echo keeps of a field
// by default. Codes and the links that matter sit near the top; a 20 MiB
// newsletter isn't read whole.
const maxScan = 256 << 10

var (
	// label before a number makes it a reference, not a code.
	label = regexp.MustCompile(`(?i)(?:\b(?:order|invoice|ref|reference|ticket|account|acct|booking|tracking|confirmation|receipt|transaction|customer|case|no|number|nr|id)|#)\s*[:#.]?\s*$`)
	// money after a number makes it a price.
	money = regexp.MustCompile(`(?i)^\s*(?:usd|eur|gbp|aud|cad|inr|jpy|dollars?|euros?|pounds?|[$€£¥%])`)
	// before and after are how far a keyword may be from a code. "Your
	// code is 482913" and "482913 is your verification code" both count.
	before, after = 60, 30
)

// Extract returns the one-time codes in m (most likely first) and the
// links of a known kind, in the order they appear. A code is 4 to 8
// digits, or 6 to 8 capital letters and digits mixed, within a few words
// of "code", "OTP", "PIN", "verification" or "one-time", or near the top
// of a message whose subject says so. Order numbers, prices, years, phone
// numbers and dates are left out.
func Extract(m model.Message) (codes []string, links []Link) {
	htmlText := ""
	if m.HTML != "" {
		htmlText = textOf(cut(m.HTML))
	}
	subject, named := find(cut(m.Subject), 0)
	codes = subject
	// A subject that names a code ("Your sign-in code") vouches for a code
	// near the top of the body, however the body words it.
	lead := 0
	if named {
		lead = subjectLead
	}
	for _, s := range []string{m.Text, htmlText} {
		found, _ := find(cut(s), lead)
		for _, c := range found {
			if !slices.Contains(codes, c) {
				codes = append(codes, c)
			}
		}
	}
	seen := map[string]bool{}
	add := func(raw string) {
		raw = strings.TrimRight(raw, ".,;:!?)]}'\"")
		if seen[raw] {
			return
		}
		seen[raw] = true
		if kind := kindOf(raw); kind != "" {
			links = append(links, Link{Kind: kind, URL: raw})
		}
	}
	for _, h := range href.FindAllStringSubmatch(cut(m.HTML), -1) {
		add(html.UnescapeString(h[1]))
	}
	for _, s := range []string{m.Text, htmlText} {
		for _, u := range urls(cut(s)) {
			add(u)
		}
	}
	for _, u := range headerURL.FindAllStringSubmatch(m.Headers["list-unsubscribe"], -1) {
		if seen[u[1]] {
			continue
		}
		seen[u[1]] = true
		links = append(links, Link{Kind: Unsubscribe, URL: u[1]}) // whatever its path says
	}
	return codes, links
}

func cut(s string) string {
	if len(s) > maxScan {
		return s[:maxScan]
	}
	return s
}

var (
	tags  = regexp.MustCompile(`(?is)<(?:style|script|head)\b.*?</(?:style|script|head)>|<[^>]*>`)
	space = regexp.MustCompile(`[ \t\r\n\f\v]+`)
)

// textOf turns HTML into the text a reader sees, one space for every run
// of tags and blanks, so a code in <strong> stands as a word.
func textOf(h string) string {
	return space.ReplaceAllString(html.UnescapeString(tags.ReplaceAllString(h, " ")), " ")
}

// notCodeWords before "code" make it something else: a zip code, a
// discount code.
var notCodeWords = []string{"zip", "postal", "post", "area", "country", "promo", "promotion", "discount", "coupon", "voucher",
	"referral", "gift", "source", "status", "error", "dial", "dialing"}

// keywords name a one-time code, as whole words in lower case.
// "one-time" and "two-factor" are two words each (see pairs).
var keywords = []string{"code", "passcode", "otp", "pin", "verification", "verify", "2fa", "mfa"}

// pairs are two-word keywords: the first word, then the second after one
// space or hyphen.
var pairs = map[string]string{"time": "one", "factor": "two"}

// subjectLead is how far into the body a code counts when the subject
// names one.
const subjectLead = 500

// find returns the codes in s and whether s names a code at all. A code
// before lead needs no keyword near it. It reads s once, word by word:
// regular expressions took 100 ms over 512 KiB.
func find(s string, lead int) (codes []string, named bool) {
	ws := words(s)
	var kws [][2]int
	for i, w := range ws {
		word := strings.ToLower(s[w[0]:w[1]])
		switch {
		case slices.Contains(keywords, word):
			if word == "code" && i > 0 && strings.TrimSpace(s[ws[i-1][1]:w[0]]) == "" &&
				slices.Contains(notCodeWords, strings.ToLower(s[ws[i-1][0]:ws[i-1][1]])) {
				continue
			}
			kws = append(kws, w)
		case pairs[word] != "" && i > 0:
			p := ws[i-1]
			if w[0]-p[1] == 1 && strings.ContainsRune(" -", rune(s[p[1]])) && strings.EqualFold(s[p[0]:p[1]], pairs[word]) {
				kws = append(kws, [2]int{p[0], w[1]})
			}
		}
	}
	if len(kws) == 0 && lead == 0 {
		return nil, false
	}
	for _, w := range ws {
		start, end := w[0], w[1]
		word := s[start:end]
		if end-start < 4 || end-start > 8 || !shape(word) || start >= lead && !near(kws, start, end) || reference(s, start, end) {
			continue
		}
		if codes = append(codes, word); len(codes) == maxCodes {
			break
		}
	}
	return codes, len(kws) > 0
}

// words returns where the words of s are: runs of ASCII letters, digits
// and underscores, as \b in a regular expression sees them.
func words(s string) [][2]int {
	var out [][2]int
	start := -1
	for i := 0; i <= len(s); i++ {
		in := i < len(s) && isWord(s[i])
		switch {
		case in && start < 0:
			start = i
		case !in && start >= 0:
			out = append(out, [2]int{start, i})
			start = -1
		}
	}
	return out
}

// shape reports whether word looks like a code on its own: 4 to 8 digits
// that aren't a year, or 6 to 8 capitals and digits with both.
func shape(word string) bool {
	digits, upper := 0, 0
	for _, r := range word {
		switch {
		case unicode.IsDigit(r):
			digits++
		case unicode.IsUpper(r):
			upper++
		default:
			return false // lower case: a word, not a code
		}
	}
	if digits == len(word) {
		n, _ := strconv.Atoi(word)
		return len(word) != 4 || n < 1900 || n > 2099
	}
	return len(word) >= 6 && digits > 0 && upper > 0
}

// near reports whether a keyword is close to the word at [start, end).
// kws are in order and don't overlap the word.
func near(kws [][2]int, start, end int) bool {
	// The first keyword that ends after start-before; keywords before it
	// are too far.
	i, _ := slices.BinarySearchFunc(kws, start-before, func(k [2]int, at int) int { return cmp.Compare(k[1], at) })
	for ; i < len(kws) && kws[i][0] <= end+after; i++ {
		if k := kws[i]; k[1] <= start || k[0] >= end {
			return true
		}
	}
	return false
}

// reference reports whether the number at [start, end) of s is part of
// something else: a longer number (a phone number, a date, 1,250.00), a
// price or a labeled reference (order #482913).
func reference(s string, start, end int) bool {
	pre := s[max(0, start-24):start]
	post := s[end:min(len(s), end+12)]
	if label.MatchString(pre) || money.MatchString(post) {
		return true
	}
	// A currency sign or a plus just before: $1250, +4917…
	if r := lastRune(strings.TrimRight(pre, " ")); r == '+' || strings.ContainsRune("$€£¥", r) {
		return true
	}
	// A digit just across a separator: 555-1234, 24.10.2026, 1,250, 12 345.
	n := len(pre)
	if n >= 2 && strings.IndexByte(separators, pre[n-1]) >= 0 && isDigit(pre[n-2]) {
		return true
	}
	return len(post) >= 2 && strings.IndexByte(separators, post[0]) >= 0 && isDigit(post[1])
}

// separators join the parts of a number written in groups.
const separators = "-./,:() "

func isDigit(b byte) bool { return '0' <= b && b <= '9' }

func isWord(b byte) bool {
	return isDigit(b) || 'a' <= b && b <= 'z' || 'A' <= b && b <= 'Z' || b == '_'
}

func lastRune(s string) rune {
	r, _ := utf8.DecodeLastRuneInString(s)
	return r
}

var (
	href      = regexp.MustCompile(`(?i)\bhref\s*=\s*["']([^"']+)["']`)
	headerURL = regexp.MustCompile(`<(https?://[^>]+)>`)
)

// urls returns the http and https URLs in free text. It looks for "://"
// rather than run a regular expression over the whole text.
func urls(s string) []string {
	var out []string
	for i := 0; ; {
		j := strings.Index(s[i:], "://")
		if j < 0 {
			return out
		}
		at := i + j
		i = at + 3
		start := -1
		switch {
		case at >= 5 && strings.EqualFold(s[at-5:at], "https"):
			start = at - 5
		case at >= 4 && strings.EqualFold(s[at-4:at], "http"):
			start = at - 4
		}
		if start < 0 || start > 0 && isWord(s[start-1]) {
			continue
		}
		end := i
		for end < len(s) && !strings.ContainsRune(" \t\r\n\f\v\"'<>", rune(s[end])) {
			end++
		}
		if end > i {
			out = append(out, s[start:end])
		}
		i = end
	}
}

// kindWords name a link's kind by the words of its path and query (split
// at anything but letters and digits and also taken in pairs, so
// "sign-in" and "action=rp" count). They're checked in this order: a
// reset link is often under /login, as is a magic link.
var kindWords = []struct {
	kind  string
	words []string
}{
	{Unsubscribe, []string{"unsubscribe", "unsub", "optout"}},
	{Reset, []string{"reset", "resetpass", "resetpassword", "lostpassword", "forgot", "forgotpassword", "actionrp", "password"}},
	{Verify, []string{"verify", "verification", "verifyemail", "confirm", "activate", "activation", "validate"}},
	{Magic, []string{"magic", "magiclink", "passwordless"}},
	{Login, []string{"login", "signin", "logon", "sso", "auth", "authenticate"}},
}

// tokenKeys are query keys that carry a one-time credential: on a login
// link they make it a magic link.
var tokenKeys = []string{"token", "key", "code", "otp", "t", "hash", "signature", "sig", "ticket"}

// TokenKey reports whether the query key k carries a one-time credential
// on a login, magic, verify or reset link: ?t=, ?token=, ?ticket= and the
// rest. Elsewhere ?t= is often a timestamp, so only links of those kinds
// should be masked by it.
func TokenKey(k string) bool { return slices.Contains(tokenKeys, strings.ToLower(k)) }

var nonWord = regexp.MustCompile(`[^a-z0-9]+`)

// kindOf returns the kind of the link raw, or "" for any other link.
func kindOf(raw string) string {
	u, err := url.Parse(raw)
	if err != nil || u.Host == "" || u.Scheme != "http" && u.Scheme != "https" {
		return ""
	}
	words := nonWord.Split(strings.ToLower(u.EscapedPath()+"?"+u.RawQuery), -1)
	for i := range len(words) - 1 {
		words = append(words, words[i]+words[i+1])
	}
	for _, k := range kindWords {
		if slices.ContainsFunc(k.words, func(w string) bool { return slices.Contains(words, w) }) {
			if k.kind == Login && slices.ContainsFunc(tokenKeys, u.Query().Has) {
				return Magic
			}
			return k.kind
		}
	}
	return ""
}
