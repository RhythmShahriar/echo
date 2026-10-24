// Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
// SPDX-License-Identifier: MIT

// Package mask hides credentials in what Echo stores: headers, URLs, text,
// JSON payloads and raw MIME. It builds on the rhythm kit's secrets
// package and adds the rules Echo needs on top of it: one-time links and
// codes in email, credential headers named any way, bot tokens in API
// paths, secrets in JSON numbers and base64 MIME parts. The rules move to
// the kit once it has them.
//
// Every function keeps at most Keep characters of a secret, and none for
// values that change on every send (reset keys, login codes), so a
// comparison of two runs doesn't see them as changed.
package mask

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"regexp"
	"strings"
	"unicode"

	"github.com/emersion/go-message"

	"github.com/RhythmShahriar/rhythm/kit/secrets"
)

// Keep is the most leading characters of a secret left visible.
const Keep = 4

// Hidden is what a masked value reads as when nothing of it is kept.
const Hidden = "•••••"

// headerName matches header names whose value is a credential whatever
// its format: X-Auth-Token, X-Amz-Security-Token, Private-Token,
// X-Goog-Api-Key, X-Shopify-Access-Token, Cookie, Authorization.
var headerName = regexp.MustCompile(`(?i)token|secret|key|auth|signature|session|password|cookie|credential`)

// Header masks a header value: whole when the name says it holds a
// credential, otherwise only the secrets Text finds in it.
func Header(name, value string) string {
	if value == "" {
		return ""
	}
	if headerName.MatchString(name) {
		return Hidden
	}
	return Text(value)
}

// Headers masks every value of h (keys are kept as they are).
func Headers(h map[string]string) map[string]string {
	if h == nil {
		return nil
	}
	out := make(map[string]string, len(h))
	for k, v := range h {
		out[k] = Header(k, v)
	}
	return out
}

var (
	// Telegram bot API paths carry the bot token: /bot123456:AAH.../sendMessage.
	telegramBot = regexp.MustCompile(`(bot\d+:)([A-Za-z0-9_-]{30,})`)
	// IFTTT and similar put the key in a path segment: /with/key/<key>.
	keySegment = regexp.MustCompile(`(?i)(/key/)([^/?#\s"'<>]+)`)
	// Query values that open an account or prove who someone is. The
	// separator may be "&amp;" in HTML, hence ";".
	queryValue = regexp.MustCompile(`(?i)([?&;](?:key|k|code|otp|reset|token|sig|signature|hash)=)([^&#\s"'<>]+)`)
	// A URL in free text.
	urlPattern = regexp.MustCompile(`(?i)\b(?:https?|ftp)://[^\s"'<>]+`)
	// A one-time code named as one: "Your login code is 482913".
	oneTimeCode = regexp.MustCompile(`(?i)(\b(?:code|otp|passcode|pin)\b(?:\s+is)?[\s:#-]{0,3})(\d{4,10})\b`)
)

// URL masks the credential parts of a URL (or of a URL-like string such as
// "api.telegram.org/bot<token>/sendMessage"): a Telegram bot token, a
// /key/<x> path segment, and the values of query keys key, k, code, otp,
// reset, token, sig, signature and hash. Other query values (?page=2) are
// kept.
func URL(s string) string {
	s = telegramBot.ReplaceAllStringFunc(s, func(m string) string {
		p := telegramBot.FindStringSubmatch(m)
		return p[1] + keep(p[2])
	})
	s = keySegment.ReplaceAllStringFunc(s, func(m string) string {
		p := keySegment.FindStringSubmatch(m)
		return p[1] + keep(p[2])
	})
	return queryValue.ReplaceAllStringFunc(s, func(m string) string {
		p := queryValue.FindStringSubmatch(m)
		return p[1] + Hidden
	})
}

// Text masks free text (an email body, a subject, an SMS): the kit's rules,
// one-time codes, and URL on every URL in it.
func Text(s string) string {
	if s == "" {
		return ""
	}
	s = secrets.Redact(s)
	s = codes(s)
	return urlPattern.ReplaceAllStringFunc(s, URL)
}

// codes masks one-time codes in s, except a zip or postal code.
func codes(s string) string {
	ms := oneTimeCode.FindAllStringSubmatchIndex(s, -1)
	if ms == nil {
		return s
	}
	var b strings.Builder
	last := 0
	for _, m := range ms {
		before := strings.ToLower(s[max(0, m[0]-7):m[0]])
		if strings.HasSuffix(before, "zip ") || strings.HasSuffix(before, "postal ") {
			continue
		}
		b.WriteString(s[last:m[4]])
		b.WriteString(Hidden)
		last = m[5]
	}
	b.WriteString(s[last:])
	return b.String()
}

// jsonField matches a JSON key and its string or number value. It works on
// text that may not parse (a body cut to size), and keeps the original
// spacing around the colon.
var jsonField = regexp.MustCompile(`"((?:[^"\\]|\\.)*)"(\s*:\s*)("(?:[^"\\]|\\.)*"|-?\d[\d.eE+-]*)`)

// notSecretKeys are names ending in "key" that don't hold a credential.
var notSecretKeys = map[string]bool{"orderkey": true, "metakey": true, "cachekey": true, "idempotencykey": true,
	"sortkey": true, "primarykey": true, "foreignkey": true, "partitionkey": true, "objectkey": true, "routingkey": true}

// secretField reports whether a JSON key names a credential. The words
// are pass, pwd, secret, token and auth anywhere in the name ("db_pwd",
// "access_token", "auth"), and key at its end ("apiKey", "license_key").
// A bare "key" counts only when its value looks random: meta_data lists
// use it for field names. Counts ("total_tokens") and "author" don't.
func secretField(name, value string) bool {
	n := strings.NewReplacer("_", "", "-", "", ".", "").Replace(strings.ToLower(name))
	switch {
	case strings.HasSuffix(n, "tokens"), strings.Contains(n, "passenger"):
		return false
	case strings.Contains(n, "pass"), strings.Contains(n, "pwd"), strings.Contains(n, "secret"), strings.Contains(n, "token"),
		strings.Contains(strings.ReplaceAll(n, "author", ""), "auth"):
		return true
	case n == "key":
		return randomLooking(value)
	case strings.HasSuffix(n, "key"):
		return !notSecretKeys[n]
	}
	return false
}

// randomLooking reports whether v reads like a generated credential
// rather than a name: long, no spaces, letters and digits mixed.
func randomLooking(v string) bool {
	if len(v) < 12 || strings.ContainsAny(v, " \t") || strings.HasPrefix(v, "_") {
		return false
	}
	return strings.ContainsAny(v, "0123456789") && strings.IndexFunc(v, unicode.IsLetter) >= 0
}

// JSON masks the string and number values of keys that name a credential
// (see secretField), then runs Text on
// the rest. A masked number becomes a string, so it can't be read back as
// the original.
func JSON(s string) string {
	s = jsonField.ReplaceAllStringFunc(s, func(m string) string {
		p := jsonField.FindStringSubmatch(m)
		v := strings.TrimSuffix(strings.TrimPrefix(p[3], `"`), `"`)
		if !secretField(p[1], v) {
			return m
		}
		return `"` + p[1] + `"` + p[2] + `"` + keep(v) + `"`
	})
	return Text(s)
}

// Body masks a request, response or webhook body: JSON when it looks like
// JSON, Text otherwise.
func Body(s string) string {
	if t := strings.TrimSpace(s); strings.HasPrefix(t, "{") || strings.HasPrefix(t, "[") {
		return JSON(s)
	}
	return Text(s)
}

// keep masks v, leaving at most Keep leading characters (and never more
// than a third of it).
func keep(v string) string {
	if strings.Contains(v, Hidden) {
		return v // already masked
	}
	return secrets.Mask(v, Keep)
}

// MIME masks a raw RFC 5322 message. Text parts are decoded (base64,
// quoted-printable) before masking, since a secret in base64 is invisible
// to every rule, and stored decoded as 8bit. Other parts are replaced by
// one line naming them:
//
//	[attachment invoice-42.pdf application/pdf 84211 sha256:9f86d0…]
//
// A message cut short is masked as far as it goes. One that doesn't parse
// as MIME is masked as text.
func MIME(raw string) string {
	e, err := message.Read(strings.NewReader(raw))
	if e == nil {
		return Text(raw)
	}
	_ = err // an unknown charset or encoding still gives a readable entity
	var b strings.Builder
	writeEntity(&b, e)
	return b.String()
}

// writeEntity writes e masked. It stops quietly where the input ends, so a
// message cut to size keeps what it had.
func writeEntity(b *strings.Builder, e *message.Entity) {
	mediaType, params, _ := e.Header.ContentType()
	disp, dparams, _ := e.Header.ContentDisposition()
	switch {
	case strings.HasPrefix(mediaType, "multipart/"):
		writeHeader(b, e.Header, false)
		boundary := params["boundary"]
		mr := e.MultipartReader()
		for {
			p, err := mr.NextPart()
			if p == nil || err != nil {
				break
			}
			b.WriteString("--" + boundary + "\r\n")
			writeEntity(b, p)
			b.WriteString("\r\n")
		}
		b.WriteString("--" + boundary + "--\r\n")
	case disp != "attachment" && mediaType == "message/rfc822":
		body, _ := io.ReadAll(e.Body)
		writeHeader(b, e.Header, true)
		b.WriteString(MIME(string(body))) // a forwarded message: mask it the same way
	case disp != "attachment" && (mediaType == "" || strings.HasPrefix(mediaType, "text/")):
		body, _ := io.ReadAll(e.Body)
		writeHeader(b, e.Header, true)
		b.WriteString(Text(string(body)))
	default:
		body, err := io.ReadAll(e.Body)
		name := dparams["filename"]
		if name == "" {
			name = params["name"]
		}
		if name == "" {
			name = "unnamed"
		}
		writeHeader(b, e.Header, true)
		if err != nil {
			fmt.Fprintf(b, "[attachment %s %s cut]", name, mediaType)
			return
		}
		sum := sha256.Sum256(body)
		fmt.Fprintf(b, "[attachment %s %s %d sha256:%s]", name, mediaType, len(body), hex.EncodeToString(sum[:]))
	}
}

// writeHeader writes h masked. A part whose body is written decoded says
// so: its transfer encoding becomes 8bit.
func writeHeader(b *strings.Builder, h message.Header, decoded bool) {
	fields := h.Fields()
	for fields.Next() {
		k := fields.Key()
		if raw, err := fields.Raw(); err == nil {
			if name, _, ok := strings.Cut(string(raw), ":"); ok {
				k = name // as written: Key() canonicalizes MIME-Version to Mime-Version
			}
		}
		if decoded && strings.EqualFold(k, "Content-Transfer-Encoding") {
			continue
		}
		v, err := fields.Text()
		if err != nil {
			v = fields.Value()
		}
		b.WriteString(k + ": " + Header(k, v) + "\r\n")
	}
	if decoded {
		b.WriteString("Content-Transfer-Encoding: 8bit\r\n")
	}
	b.WriteString("\r\n")
}
