// Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
// SPDX-License-Identifier: MIT

// Package recognize turns an outbound HTTP request into a message: it
// works out the channel (SMS, chat, webhook…), the provider and the
// recipients and content, and says what a sealed staging site should be
// answered with. Connectors only report raw requests; everything
// provider-specific lives here, with a fixture test per recognizer.
package recognize

import (
	"encoding/json"
	"net/url"
	"slices"
	"strings"
	"unicode/utf8"

	"github.com/RhythmShahriar/echo/model"
	"github.com/RhythmShahriar/rhythm/kit/secrets"
)

// Request is an outbound HTTP request as a connector or the proxy saw it.
type Request struct {
	Method  string
	URL     *url.URL
	Headers map[string]string // lowercased names
	Body    []byte
}

// Header returns a header value (names are case-insensitive).
func (r Request) Header(name string) string {
	return r.Headers[strings.ToLower(name)]
}

// JSON decodes the body into v; it reports whether that worked.
func (r Request) JSON(v any) bool {
	return json.Unmarshal(r.Body, v) == nil
}

// Form decodes a form-encoded body (or the query string when the body is
// empty).
func (r Request) Form() url.Values {
	if len(r.Body) == 0 {
		return r.URL.Query()
	}
	v, err := url.ParseQuery(string(r.Body))
	if err != nil {
		return url.Values{}
	}
	return v
}

// Fake is the response Echo gives instead of the real service when a site
// is sealed. It looks enough like the provider's own answer that the
// site's code carries on normally (WooCommerce disables a webhook after
// five failures, SDKs throw on unexpected bodies).
type Fake struct {
	Status  int
	Headers map[string]string
	Body    string
}

// Recognizer understands one provider's API.
type Recognizer struct {
	// Provider is the slug stored on messages ("amazon-ses").
	Provider string
	// Name is the provider's display name ("Amazon SES"), for titles and
	// reports. A provider with several recognizers names it on each.
	Name    string
	Channel model.Channel
	// Priority orders recognizers: higher ones are tried first. Host and
	// path matchers use 0. One that matches on something any host can
	// carry (a header) goes below 0, so a request to a known host is
	// claimed by that host's recognizer first.
	Priority int
	// Match reports whether the request is for this provider.
	Match func(r Request) bool
	// Fill sets recipients, content and event on m from the request.
	Fill func(r Request, m *model.Message)
	// Fake is the sealed answer; nil gives a generic 200 {"ok":true}.
	Fake func(r Request, m *model.Message) Fake
	// Refuse, when set, reports that r must never reach the provider nor
	// get a success-shaped answer: a live-mode payment call. It applies in
	// every mode, even to allowlisted hosts. Fake then answers with the
	// provider's own error, and the message is counted as blocked.
	Refuse func(r Request) bool
	// SecretURL, when set, says the URL itself is the credential (catch
	// hooks): its last path segment is masked wherever it's stored.
	SecretURL bool
}

// registry holds every recognizer in the order Recognize tries them.
// Recognizers are added from init functions in this package's provider
// files. Go runs those in file-name order, so register sorts: by Priority,
// then by Provider. Only a provider's own recognizers keep the order they
// were registered in, which is the order within one file. Renaming a file
// can't change which recognizer wins.
var registry []Recognizer

func register(r Recognizer) {
	registry = append(registry, r)
	slices.SortStableFunc(registry, func(a, b Recognizer) int {
		if a.Priority != b.Priority {
			return b.Priority - a.Priority
		}
		return strings.Compare(a.Provider, b.Provider)
	})
}

// Recognize fills m from r: channel, provider, recipients, content. It
// returns the matching recognizer, or the generic one.
func Recognize(r Request, m *model.Message) Recognizer {
	for _, rec := range registry {
		if rec.Match(r) {
			m.Channel, m.Provider = rec.Channel, rec.Provider
			if rec.Fill != nil {
				rec.Fill(r, m)
			}
			return rec
		}
	}
	m.Channel, m.Provider = generic.Channel, generic.Provider
	generic.Fill(r, m)
	return generic
}

// FakeFor returns the sealed answer for a recognized request.
func FakeFor(rec Recognizer, r Request, m *model.Message) Fake {
	if rec.Fake != nil {
		return rec.Fake(r, m)
	}
	return Fake{Status: 200, Headers: map[string]string{"content-type": "application/json"}, Body: `{"ok":true}`}
}

// Providers lists the providers Echo knows, each once (a provider may have
// several recognizers, one per API), for docs and `rhythm echo doctor`.
func Providers() []string {
	out := make([]string, 0, len(registry))
	for _, r := range registry {
		if !slices.Contains(out, r.Provider) {
			out = append(out, r.Provider)
		}
	}
	return out
}

// Name returns a provider's display name ("amazon-ses" → "Amazon SES"),
// or the slug itself for one Echo doesn't know.
func Name(provider string) string {
	for _, r := range registry {
		if r.Provider == provider && r.Name != "" {
			return r.Name
		}
	}
	return provider
}

// SecretHook reports whether a provider's URLs are catch hooks, where the
// URL names the destination (one Zap, one scenario) as well as being its
// credential.
func SecretHook(provider string) bool {
	for _, r := range registry {
		if r.Provider == provider {
			return r.SecretURL
		}
	}
	return false
}

// host reports whether r is for host or a subdomain of it.
func host(r Request, hosts ...string) bool {
	h := strings.ToLower(r.URL.Hostname())
	for _, want := range hosts {
		if h == want || strings.HasSuffix(h, "."+want) {
			return true
		}
	}
	return false
}

// generic is any HTTP call no recognizer claims: a webhook when it posts
// JSON or a form, plain HTTP otherwise.
var generic = Recognizer{
	Provider: "",
	Channel:  model.HTTP,
	Match:    func(Request) bool { return true },
	Fill: func(r Request, m *model.Message) {
		if r.Method == "POST" || r.Method == "PUT" || r.Method == "PATCH" {
			m.Channel = model.Webhook
		}
		m.To = []string{genericTo(r)}
	},
}

// genericTo names an unknown destination by host and path, with every
// segment that may be a credential replaced by "…": one longer than 20
// characters (tokens, UUIDs, hashes) or shaped like a token. The full URL
// is still stored, masked by RedactURL; To is what lists and compare show,
// so it must never hold a key, even one no rule recognizes.
func genericTo(r Request) string {
	segs := strings.Split(r.URL.Path, "/")
	for i, seg := range segs {
		if utf8.RuneCountInString(seg) > 20 || tokenShaped(segs, i) {
			segs[i] = "…"
		}
	}
	return r.URL.Host + strings.Join(segs, "/")
}

// keySegments are path words that say the next segment is a credential
// (IFTTT's /with/key/{key}, /token/{t} reset links).
var keySegments = []string{"key", "token", "secret", "apikey", "api_key", "access_token"}

// tokenShaped reports whether path segment i looks like a credential: a
// Telegram bot token, the segment after a key word, or a value
// secrets.Redact would mask.
func tokenShaped(segs []string, i int) bool {
	seg := segs[i]
	if seg == "" {
		return false
	}
	if telegramBot.MatchString(seg) {
		return true
	}
	if i > 0 && slices.Contains(keySegments, strings.ToLower(segs[i-1])) {
		return true
	}
	return secrets.Redact(seg) != seg
}

// secretQuery are query parameters that carry a URL's credential on top
// of what secrets.Redact finds: Logic Apps and Power Automate sign
// trigger URLs with sig.
var secretQuery = []string{"sig", "signature", "code"}

// RedactURL masks the credentials in a request URL for storing and
// showing it: what secrets.Redact finds (keys, Slack and Discord hook
// tokens), signature parameters, Telegram bot tokens, the segment after
// a key word (IFTTT's /with/key/{key}) and the secret last segment of
// catch hooks (Zapier, Make, n8n). The masked form is the same every
// run, so compare still pairs messages by URL.
func RedactURL(rec Recognizer, raw string) string {
	u, err := url.Parse(raw)
	if err != nil || u.Host == "" || u.User != nil {
		return secrets.Redact(raw)
	}
	changed := false
	// Credentials in the path, whatever the recognizer: a Telegram bot
	// token (bot<id>: is kept, it names the bot) and the segment after a
	// key word.
	segs := strings.Split(u.Path, "/")
	for i, seg := range segs {
		if t := telegramBot.FindStringSubmatch(seg); t != nil {
			segs[i], changed = "bot"+t[1]+":"+secrets.Mask(t[2], 2), true
		} else if seg != "" && i > 0 && slices.Contains(keySegments, strings.ToLower(segs[i-1])) && !strings.Contains(seg, "•") {
			segs[i], changed = secrets.Mask(seg, 2), true
		}
	}
	if rec.SecretURL {
		for i := len(segs) - 1; i >= 0; i-- {
			if segs[i] != "" {
				if !strings.Contains(segs[i], "•") {
					segs[i], changed = secrets.Mask(segs[i], 2), true
				}
				break
			}
		}
	}
	u.Path = strings.Join(segs, "/")
	pairs := strings.Split(u.RawQuery, "&")
	for i, p := range pairs {
		k, v, _ := strings.Cut(p, "=")
		if k, err := url.QueryUnescape(k); err == nil && v != "" && slices.Contains(secretQuery, strings.ToLower(k)) {
			pairs[i], changed = p[:len(p)-len(v)]+secrets.Mask(v, 2), true
		}
	}
	if !changed {
		return secrets.Redact(raw)
	}
	// Built by hand: url.URL.String would percent-encode the bullets.
	out := u.Scheme + "://" + u.Host + u.Path
	if u.RawQuery != "" {
		out += "?" + strings.Join(pairs, "&")
	}
	return secrets.Redact(out)
}

// Refuses reports whether rec refuses r, sealed or not (see Recognizer.Refuse).
func Refuses(rec Recognizer, r Request) bool {
	return rec.Refuse != nil && rec.Refuse(r)
}
