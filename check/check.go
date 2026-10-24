// Package check finds problems in captured messages: placeholders that
// were never filled in, PHP errors printed into an email, links to a
// development machine, SMS that cost several parts, webhooks that fail.
// Issue titles are stable across runs, so a comparison can tell a new
// problem from an old one.
package check

import (
	"encoding/json"
	"fmt"
	"net"
	"net/url"
	"path"
	"regexp"
	"slices"
	"strings"
	"unicode/utf8"

	"github.com/RhythmShahriar/echo/model"
)

// Options says what's expected of messages.
type Options struct {
	SiteHost        string   // the site being checked: links to it are fine
	SiteDomain      string   // the domain mail should come from; defaults to SiteHost without "www."
	AllowRecipients []string // globs of addresses or numbers that may really be messaged (observe mode)
}

// Message returns the problems in m.
func Message(m model.Message, o Options) []model.Issue {
	var out []model.Issue
	add := func(i model.Issue) {
		i.Message = m.ID
		i.Identify()
		out = append(out, i)
	}
	what := Describe(m)

	if m.Status == model.Failed {
		title := fmt.Sprintf("%s fails", capitalize(what))
		if m.Response != nil && m.Response.Status > 0 {
			title = fmt.Sprintf("%s fails (HTTP %d)", capitalize(what), m.Response.Status)
		}
		add(model.Issue{Check: "send/failed", Level: model.Error, Title: title, Detail: firstNonEmpty(m.Error, responseText(m)),
			Fix: "Check the sender's settings (API key, SMTP login, endpoint URL) and the provider's error above."})
	}

	text := visibleText(m)
	if m.Channel == model.Email || m.Channel == model.SMS || m.Channel == model.WhatsApp || m.Channel == model.Chat {
		for _, tag := range placeholders(m.Subject + "\n" + text) {
			add(model.Issue{Check: "content/placeholder", Level: model.Error,
				Title:  fmt.Sprintf("Unfilled placeholder %s in %s", tag, what),
				Detail: "The template still contains the placeholder, so readers see it instead of their data.",
				Fix:    "Check the tag's spelling against the plugin's list of tags, and that the data it needs exists when the message is sent."})
		}
		if e := phpError(text); e != "" {
			add(model.Issue{Check: "content/php-error", Level: model.Error, Title: fmt.Sprintf("PHP error printed in %s", what), Detail: e,
				Fix: "Fix the code the error points at, and turn display_errors off so errors never reach messages."})
		}
		if bad := mojibake(m.Subject + text); bad != "" {
			add(model.Issue{Check: "content/encoding", Level: model.Warning, Title: fmt.Sprintf("Garbled characters in %s", what),
				Detail: fmt.Sprintf("found %q: text was encoded twice", bad),
				Fix:    "Make sure the message is built and sent as UTF-8 (the mailer's charset and the template file's encoding)."})
		}
		for _, h := range devLinks(m, o) {
			add(model.Issue{Check: "content/dev-link", Level: model.Warning, Title: fmt.Sprintf("%s links to a development address (%s)", capitalize(what), h),
				Fix: "Build links from the site's configured URL, not a hard-coded or local one."})
		}
	}

	switch m.Channel {
	case model.Email:
		if strings.TrimSpace(m.Subject) == "" {
			add(model.Issue{Check: "email/no-subject", Level: model.Warning, Title: "Email has no subject",
				Fix: "Give the notification a subject; many filters treat mail without one as spam."})
		}
		if strings.TrimSpace(m.Text) == "" && strings.TrimSpace(m.HTML) == "" {
			add(model.Issue{Check: "email/empty", Level: model.Error, Title: "Email is empty",
				Fix: "The template rendered nothing: check the notification's message setting and the variables it uses."})
		}
		if m.HTML != "" && strings.TrimSpace(m.Text) == "" {
			add(model.Issue{Check: "email/no-text", Level: model.Notice, Title: "Email has no plain-text part",
				Fix: "Send a text alternative too; some clients and spam filters prefer it."})
		}
		if len(m.HTML) > 102*1024 {
			add(model.Issue{Check: "email/clipped", Level: model.Warning, Title: "Email is too big for Gmail and gets clipped",
				Detail: fmt.Sprintf("%d KB of HTML; Gmail clips messages over 102 KB", len(m.HTML)/1024),
				Fix:    "Trim inline CSS and markup, or move content behind links."})
		}
		if d := domainOf(m.From); d != "" && o.domain() != "" && !isDevHost(o.domain()) && net.ParseIP(o.domain()) == nil && !sameOrg(d, o.domain()) {
			add(model.Issue{Check: "email/from-domain", Level: model.Notice, Title: fmt.Sprintf("Email is sent from another domain (%s)", d),
				Detail: "the site is " + o.domain(),
				Fix:    "Send from an address on your own domain, or make sure its SPF, DKIM and DMARC cover this sender."})
		}
	case model.SMS, model.WhatsApp:
		for _, to := range m.To {
			if to != "" && !e164.MatchString(to) {
				add(model.Issue{Check: "sms/number", Level: model.Warning, Title: "Message sent to a number that isn't in international format",
					Detail: to, Fix: "Store and send numbers in E.164 form, e.g. +4915112345678."})
			}
		}
		if m.Channel == model.SMS && m.Text != "" {
			enc, parts := segments(m.Text)
			switch {
			case parts > 3:
				add(model.Issue{Check: "sms/parts", Level: model.Warning, Title: "SMS needs more than 3 parts",
					Detail: fmt.Sprintf("%d parts (%s); each part is charged", parts, enc), Fix: "Shorten the message, or avoid characters that force UCS-2 (emoji, curly quotes)."})
			case parts > 1:
				add(model.Issue{Check: "sms/parts", Level: model.Notice, Title: "SMS needs more than one part",
					Detail: fmt.Sprintf("%d parts (%s); each part is charged", parts, enc), Fix: "Keep SMS under 160 GSM-7 characters (70 with emoji or other Unicode)."})
			}
		}
	case model.Webhook:
		if isJSON(m) && !validJSON(m.Body) && !strings.HasSuffix(m.Body, "…(truncated)") {
			add(model.Issue{Check: "webhook/json", Level: model.Warning, Title: fmt.Sprintf("%s sends invalid JSON", capitalize(what)),
				Fix: "The payload says it's JSON but doesn't parse; check how it's built (json_encode errors, stray output)."})
		}
		if m.Provider == "woocommerce" && m.Headers["x-wc-webhook-signature"] == "" {
			add(model.Issue{Check: "webhook/signature", Level: model.Warning, Title: "WooCommerce webhook is sent without a signature",
				Fix: "Set a secret on the webhook (WooCommerce → Settings → Advanced → Webhooks) so receivers can verify it."})
		}
	}
	if m.Channel != model.Email && strings.Contains(m.Body, "•••••") && m.Status != model.Faked {
		add(model.Issue{Check: "content/secret", Level: model.Warning, Title: fmt.Sprintf("%s carries a credential in its body", capitalize(what)),
			Fix: "Don't put API keys or passwords in payloads; send them in an Authorization header, or not at all."})
	}

	if m.Status == model.Sent {
		for _, r := range append(append(slices.Clone(m.To), m.CC...), m.BCC...) {
			if realRecipient(r) && !o.allowed(r) {
				add(model.Issue{Check: "seal/real-recipient", Level: model.Warning, Title: fmt.Sprintf("%s really sent to an address outside the allowlist", capitalize(what)),
					Detail: r, Fix: "Run sealed on staging (rhythm echo up), or add test recipients to allow_recipients."})
				break
			}
		}
	}
	return out
}

// Describe names a message for titles: "email", "Twilio SMS", "webhook to
// hooks.zapier.com". Titles must stay stable, so no IDs or times.
func Describe(m model.Message) string {
	p := providerName(m.Provider)
	switch m.Channel {
	case model.Email:
		if p != "" {
			return p + " email"
		}
		return "email"
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
		return "push notification"
	}
	if u, err := url.Parse(m.URL); err == nil && u.Host != "" {
		return "webhook to " + u.Hostname()
	}
	return "request"
}

var providerNames = map[string]string{"twilio": "Twilio", "slack": "Slack", "discord": "Discord", "woocommerce": "WooCommerce",
	"sendgrid": "SendGrid", "mailgun": "Mailgun", "postmark": "Postmark", "resend": "Resend", "ses": "Amazon SES", "brevo": "Brevo",
	"vonage": "Vonage", "ga4": "GA4", "meta-capi": "Meta", "stripe": "Stripe", "paypal": "PayPal", "fcm": "FCM", "teams": "Teams"}

func providerName(p string) string {
	if n, ok := providerNames[p]; ok {
		return n
	}
	return p
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

// sameOrg compares the last two labels of each domain: good enough to
// tell mail.shop.com from shop.com, without a public suffix list here.
func sameOrg(a, b string) bool { return lastLabels(a, 2) == lastLabels(b, 2) }

func lastLabels(d string, n int) string {
	parts := strings.Split(d, ".")
	if len(parts) <= n {
		return d
	}
	return strings.Join(parts[len(parts)-n:], ".")
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

func capitalize(s string) string {
	if s == "" {
		return s
	}
	return strings.ToUpper(s[:1]) + s[1:]
}
