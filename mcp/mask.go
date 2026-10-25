// Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
// SPDX-License-Identifier: MIT

package mcp

import (
	"bytes"
	"encoding/json"
	"regexp"
	"strings"

	"github.com/RhythmShahriar/rhythm/kit/secrets"
)

// Masking for tool results. What an agent reads can end up in a prompt
// log, a chat transcript or a model provider's servers, so results are
// masked on the way out, whatever the catcher already masked when it
// stored them. These rules cover what the kit's secrets.Redact misses in
// captured mail and requests: credential headers by name, one-time links
// (?code=, ?key=, ?token=…), Telegram bot tokens and /key/<x> paths. The
// mask package, which the catcher applies when it stores a message, has
// the full set; these rules stay a second pass over what an agent reads.

// keep is the most leading characters a masked value shows.
const keep = 4

var (
	credentialHeader = regexp.MustCompile(`(?i)token|secret|key|auth|signature|session|password|cookie|credential`)
	urlInText        = regexp.MustCompile(`(?i)\b[a-z][a-z0-9+.-]*://[^\s"'<>()]+`)
	telegramBot      = regexp.MustCompile(`bot(\d+:[A-Za-z0-9_-]{30,})`)
	keyPath          = regexp.MustCompile(`(?i)(/key/)([^/?#\s]+)`)
	secretQuery      = regexp.MustCompile(`(?i)([?&](?:key|k|code|otp|reset|token|sig|signature|hash)=)([^&#\s"']+)`)
)

// Mask returns v as plain JSON values (maps, slices, strings, numbers)
// with credentials masked in every string and the values of credential
// headers masked whole.
func Mask(v any) any {
	b, err := json.Marshal(v)
	if err != nil {
		return maskText(err.Error())
	}
	var plain any
	dec := json.NewDecoder(bytes.NewReader(b))
	dec.UseNumber() // keep numbers exactly as they were
	if err := dec.Decode(&plain); err != nil {
		return maskText(err.Error())
	}
	return walk(plain, false)
}

// walk masks plain in place. inHeaders is true for the members of a
// "headers" object, whose names say whether the value is a credential.
func walk(plain any, inHeaders bool) any {
	switch x := plain.(type) {
	case map[string]any:
		for k, v := range x {
			if s, ok := v.(string); ok && inHeaders && credentialHeader.MatchString(k) {
				x[k] = secrets.Mask(s, 0)
				continue
			}
			x[k] = walk(v, strings.EqualFold(k, "headers"))
		}
		return x
	case []any:
		for i := range x {
			x[i] = walk(x[i], false)
		}
		return x
	case string:
		return maskText(x)
	}
	return plain
}

// maskText runs the kit's rules over s, then masks the credential parts
// of every URL in it.
func maskText(s string) string {
	s = secrets.Redact(s)
	if !strings.Contains(s, "://") {
		return s
	}
	return urlInText.ReplaceAllStringFunc(s, maskURL)
}

// maskURL masks a Telegram bot token in the path, key-shaped path
// segments and secret query values, each showing at most keep leading
// characters.
func maskURL(u string) string {
	u = telegramBot.ReplaceAllStringFunc(u, func(m string) string { return "bot" + secrets.Mask(m[3:], keep) })
	u = keyPath.ReplaceAllStringFunc(u, func(m string) string {
		p := keyPath.FindStringSubmatch(m)
		return p[1] + secrets.Mask(p[2], keep)
	})
	return secretQuery.ReplaceAllStringFunc(u, func(m string) string {
		p := secretQuery.FindStringSubmatch(m)
		if strings.Contains(p[2], "•") {
			return m // the kit masked it already
		}
		return p[1] + secrets.Mask(p[2], keep)
	})
}
