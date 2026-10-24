// Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
// SPDX-License-Identifier: MIT

// Package check finds problems in captured messages: placeholders that
// were never filled in, PHP errors printed into an email, links to a
// development machine, SMS that cost several parts, webhooks that fail.
// Issue titles are stable across runs, so a comparison can tell a new
// problem from an old one.
package check

import (
	"cmp"
	"encoding/json"
	"fmt"
	"net/url"
	"path"
	"regexp"
	"slices"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/RhythmShahriar/echo/model"
	"github.com/RhythmShahriar/echo/recognize"
)

// Options says what's expected of messages.
type Options struct {
	SiteHost        string   // the site being checked: links to it are fine
	SiteDomain      string   // the domain mail should come from; defaults to SiteHost without "www."
	AllowRecipients []string // globs of addresses or numbers that may really be messaged (observe mode)
	Rules           []Rule   // custom rules, run after the registered ones; CustomRules makes them from the config
}

// Message returns the problems in m: what every registered rule that
// looks at m's channel finds, then o.Rules (custom rules from the
// project's config).
func Message(m model.Message, o Options) []model.Issue {
	var out []model.Issue
	for _, rs := range [][]Rule{Rules(), o.Rules} {
		for _, r := range rs {
			if !r.Applies(m.Channel) {
				continue
			}
			for _, i := range r.Run(m, o) {
				i.Check = cmp.Or(i.Check, r.ID)
				i.Level = cmp.Or(i.Level, r.Level)
				i.Message = m.ID
				i.Identify()
				out = append(out, i)
			}
		}
	}
	return out
}

// Describe names a message for titles: "email", "Twilio SMS", "Zapier
// webhook", "webhook to crm.shop.com". Titles must stay stable, so no IDs
// or times.
func Describe(m model.Message) string {
	what := Kind(m)
	switch {
	case m.Channel != model.Webhook && m.Channel != model.HTTP:
	case m.Provider != "" && m.Provider != "woocommerce": // the provider says where it goes
	default:
		if u, err := url.Parse(m.URL); err == nil && u.Host != "" {
			return what + " to " + u.Hostname()
		}
	}
	return what
}

// Kind names what a message is without where it went, for lines that
// show the destination next to it: "Amazon SES email", "webhook",
// "request".
func Kind(m model.Message) string {
	p := recognize.Name(m.Provider)
	switch m.Channel {
	case model.Email:
		return strings.TrimSpace(p + " email")
	case model.SMS:
		return strings.TrimSpace(p + " SMS")
	case model.WhatsApp:
		return "WhatsApp message"
	case model.Chat:
		return strings.TrimSpace(p + " message")
	case model.Analytics:
		return strings.TrimSpace(p + " event")
	case model.Payment:
		return strings.TrimSpace(p + " call")
	case model.Push:
		return strings.TrimSpace(p + " push notification")
	case model.Webhook:
		return strings.TrimSpace(p + " webhook")
	}
	if m.Method == "CONNECT" {
		return "HTTPS connection"
	}
	return "request"
}

var (
	placeholderRe = regexp.MustCompile(`\{\{\s*[\w.$-]+\s*\}\}|\*\|[A-Z0-9_:]+\|\*|%%[A-Za-z0-9_]+%%|\{[a-z][a-z0-9_]*\}|\[(?:your|_)[a-z0-9-]+\]`)
	phpErrRe      = regexp.MustCompile(`(?i)(?:<b>)?(Fatal error|Parse error|Warning|Notice|Deprecated)(?:</b>)?\s*:\s+(.{3,160}?)\s+in\s+(?:<b>)?(\S+\.php)(?:</b>)?\s+on line\s+(?:<b>)?(\d+)`)
	e164          = regexp.MustCompile(`^\+[1-9]\d{6,14}$`)
	mojibakeRe    = regexp.MustCompile(`Ã[\x{0080}-\x{00BF}]|â€[\x{0080}-\x{00BF}™œ“”˜]|Â[\x{00A0}-\x{00BF}]`)
	tagRe         = regexp.MustCompile(`<[^>]+>`)
	hrefRe        = regexp.MustCompile(`(?i)(?:href|src)\s*=\s*["']([^"']+)["']|https?://[^\s"'<>)]+`)
)

func placeholders(s string) []string {
	var out []string
	for _, t := range placeholderRe.FindAllString(s, -1) {
		if !slices.Contains(out, t) && !strings.HasPrefix(t, "{\"") {
			out = append(out, t)
		}
	}
	return out
}

func phpError(s string) string {
	if m := phpErrRe.FindStringSubmatch(s); m != nil {
		return fmt.Sprintf("PHP %s: %s (%s line %s)", m[1], strings.TrimSpace(m[2]), shortPath(m[3]), m[4])
	}
	return ""
}

func shortPath(p string) string {
	if i := strings.Index(p, "wp-content/"); i >= 0 {
		return p[i+len("wp-content/"):]
	}
	return path.Base(p)
}

func mojibake(s string) string { return mojibakeRe.FindString(s) }

// visibleText is what a reader sees: the text part, else the HTML without
// tags.
func visibleText(m model.Message) string {
	if strings.TrimSpace(m.Text) != "" {
		return m.Text
	}
	return tagRe.ReplaceAllString(m.HTML, " ")
}

// devLinks returns development hosts linked from the message, when the
// site itself isn't one.
func devLinks(m model.Message, o Options) []string {
	if isDevHost(o.SiteHost) {
		return nil
	}
	var out []string
	for _, match := range hrefRe.FindAllStringSubmatch(m.Text+" "+m.HTML, -1) {
		raw := match[0]
		if match[1] != "" {
			raw = match[1]
		}
		u, err := url.Parse(raw)
		if err != nil || u.Host == "" {
			continue
		}
		if h := u.Hostname(); isDevHost(h) && !slices.Contains(out, h) {
			out = append(out, h)
		}
	}
	return out
}

func isDevHost(h string) bool {
	h = strings.ToLower(h)
	if h == "" {
		return false
	}
	if h == "localhost" || strings.HasPrefix(h, "127.") || strings.HasPrefix(h, "192.168.") || strings.HasPrefix(h, "10.") || h == "0.0.0.0" {
		return true
	}
	for _, s := range []string{".local", ".test", ".localhost", ".ddev.site", ".lndo.site", ".docker", ".invalid"} {
		if strings.HasSuffix(h, s) {
			return true
		}
	}
	return false
}

// segments returns the SMS encoding and how many parts text needs.
func segments(text string) (string, int) {
	gsm := true
	for _, r := range text {
		if !strings.ContainsRune(gsm7, r) {
			gsm = false
			break
		}
	}
	n := utf8.RuneCountInString(text)
	if gsm {
		for _, r := range text {
			if strings.ContainsRune(gsm7ext, r) {
				n++ // escape character
			}
		}
		if n <= 160 {
			return "GSM-7", 1
		}
		return "GSM-7", (n + 152) / 153
	}
	units := len(utf16(text))
	if units <= 70 {
		return "UCS-2", 1
	}
	return "UCS-2", (units + 66) / 67
}

const (
	gsm7    = "@£$¥èéùìòÇ\nØø\rÅåΔ_ΦΓΛΩΠΨΣΘΞÆæßÉ !\"#¤%&'()*+,-./0123456789:;<=>?¡ABCDEFGHIJKLMNOPQRSTUVWXYZÄÖÑÜ§¿abcdefghijklmnopqrstuvwxyzäöñüà" + gsm7ext
	gsm7ext = "^{}\\[~]|€\f"
)

func utf16(s string) []uint16 {
	var out []uint16
	for _, r := range s {
		if r >= 0x10000 {
			out = append(out, 0, 0)
		} else {
			out = append(out, 0)
		}
	}
	return out
}

func isJSON(m model.Message) bool {
	ct := strings.ToLower(m.Headers["content-type"])
	return strings.Contains(ct, "json")
}

func validJSON(s string) bool {
	s = strings.TrimSpace(s)
	if s == "" {
		return true
	}
	return json.Valid([]byte(s))
}

func responseText(m model.Message) string {
	if m.Response == nil {
		return ""
	}
	b := m.Response.Body
	if len(b) > 300 {
		b = b[:300] + "…"
	}
	return b
}

func domainOf(addr string) string {
	if i := strings.LastIndex(addr, "@"); i >= 0 {
		return strings.ToLower(strings.Trim(addr[i+1:], "> "))
	}
	return ""
}

func (o Options) domain() string {
	if o.SiteDomain != "" {
		return strings.ToLower(o.SiteDomain)
	}
	return strings.TrimPrefix(strings.ToLower(o.SiteHost), "www.")
}

// realRecipient reports whether a recipient looks like a real person rather than a
// test address.
func realRecipient(r string) bool {
	r = strings.ToLower(r)
	if d := domainOf(r); d != "" {
		return !isDevHost(d) && d != "example.com" && d != "example.org" && d != "example.net" && !strings.HasSuffix(d, ".example")
	}
	return e164.MatchString(r) && !strings.HasPrefix(r, "+1555") && !strings.HasPrefix(r, "+15005550")
}

func (o Options) allowed(r string) bool {
	for _, g := range o.AllowRecipients {
		if ok, _ := path.Match(strings.ToLower(g), strings.ToLower(r)); ok {
			return true
		}
	}
	return false
}

func firstNonEmpty(s ...string) string {
	for _, v := range s {
		if v != "" {
			return v
		}
	}
	return ""
}

// Capitalize upper-cases the first letter of s, a whole rune, so a title
// that starts with "élan" reads "Élan".
func Capitalize(s string) string {
	r, n := utf8.DecodeRuneInString(s)
	if n == 0 || r == utf8.RuneError {
		return s
	}
	return string(unicode.ToTitle(r)) + s[n:]
}
