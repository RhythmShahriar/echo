// Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
// SPDX-License-Identifier: MIT

package mask

import (
	"regexp"
	"strings"
)

// Credentials counts the masks in s that stand for a credential: an API
// key, a password, a bearer token or a key Echo recognised by its shape.
// Masks placed over a one-time code, the token of a reset, verify or
// magic link and the signature of a signed URL don't count: they change
// on every send and are meant for the reader, so a payload that carries
// one doesn't leak anything the site should keep to itself.
func Credentials(s string) int {
	n := 0
	for i := 0; ; {
		j := strings.Index(s[i:], Hidden)
		if j < 0 {
			return n
		}
		// What decides is just before the mask.
		if credentialAt(s[max(0, i+j-256) : i+j]) {
			n++
		}
		i += j + len(Hidden)
	}
}

var (
	// keptTail is what the masking rules leave of a secret: up to eight
	// leading characters ("sk_live_", "ab") right before the mask.
	keptTail = regexp.MustCompile(`[^\s"'=:/&?;<>()\[\]{},]{1,8}$`)
	// oneTimeBefore ends text that names a one-time code: "Your code is "
	// ("Your+code+is+" in a form body).
	oneTimeBefore = regexp.MustCompile(`(?i)\b(?:code|otp|passcode|pin)\b(?:[\s+]+is)?[\s+:#-]{0,3}$`)
	// authBefore ends an Authorization-style token: "Bearer ",
	// "Authorization: ", "x-api-key=".
	authBefore = regexp.MustCompile(`(?i)(?:\b(?:bearer|basic|token)\s+|\b(?:authorization|x-postmark-server-token|x-api-key|api-key)["']?\s*[:=]\s*["']?)$`)
	// privateKeyBefore ends the head of a PEM private key.
	privateKeyBefore = regexp.MustCompile(`PRIVATE KEY-----\s*$`)
	// paramBefore ends a URL or form query key: "?api_key=".
	paramBefore = regexp.MustCompile(`[?&;]([\w.-]{1,64})=$`)
	// jsonBefore ends a JSON key and the quote of its string value.
	jsonBefore = regexp.MustCompile(`"((?:[^"\\]|\\.){1,64})"\s*:\s*"$`)
	// assignBefore ends a name given a value: "DB_PASSWORD=", "pass: ".
	assignBefore = regexp.MustCompile(`(?:^|[\s,;{(])["']?([\w.-]{1,64})["']?\s*[=:]\s*["']?$`)
	// keyBefore ends an IFTTT-style /key/ path segment or the bot part
	// of a Telegram bot path (Echo keeps a little of the token after it).
	keyBefore = regexp.MustCompile(`(?i)(?:/key/|\bbot\d+:)$`)
)

// credentialAt reports whether the mask that follows pre stands for a
// credential, judging by what precedes it.
func credentialAt(pre string) bool {
	if oneTimeBefore.MatchString(pre) {
		return false
	}
	kept := keptTail.FindString(pre)
	pre = pre[:len(pre)-len(kept)]
	switch {
	case authBefore.MatchString(pre), privateKeyBefore.MatchString(pre), keyBefore.MatchString(pre):
		return true
	case strings.HasSuffix(pre, "/"):
		// A path segment: Echo hides a link token there whole, while the
		// kit keeps a little of a Slack or Discord webhook's secret. A
		// JSON Web Token in a path signs a reader in from a link.
		return kept != "" && !strings.HasPrefix(kept, "eyJ")
	}
	if m := paramBefore.FindStringSubmatch(pre); m != nil {
		return credentialParam(m[1])
	}
	if m := jsonBefore.FindStringSubmatch(pre); m != nil {
		if name := fieldName(m[1]); readerToken(name) {
			return false
		} else if name == "key" || secretField(name, "") {
			return true
		}
		return kept != ""
	}
	if m := assignBefore.FindStringSubmatch(pre); m != nil {
		if name := fieldName(m[1]); readerToken(name) {
			return false
		} else if name == "key" || secretField(name, "") {
			return true
		}
	}
	// With no name before it, what is kept tells: a key the kit knows by
	// its shape (sk_live_, AKIA, ghp_) or a JSON Web Token keeps a few
	// characters, while a code or link token Echo hid wherever it
	// appeared keeps none.
	return kept != ""
}

// credentialParams are the query keys whose value is a credential, in
// lower case. The other keys mask.URL hides (code, otp, reset, token,
// sig, signature, hash, ticket, magic, login_token, X-Amz-Signature)
// carry a one-time code, a link token or a signature.
var credentialParams = map[string]bool{"key": true, "k": true, "apikey": true, "auth": true, "password": true,
	"pass": true, "pwd": true, "secret": true, "access_token": true, "auth_token": true, "api_token": true,
	"bearer_token": true, "refresh_token": true, "id_token": true}

// credentialParam reports whether the query key name holds a credential.
func credentialParam(name string) bool {
	name = strings.ToLower(name)
	if credentialParams[name] {
		return true
	}
	for _, s := range []string{"_key", "-key", "secret", "password"} {
		if strings.HasSuffix(name, s) {
			return true
		}
	}
	return false
}

// readerToken reports whether a field name (lowercased, no separators)
// holds something meant for the reader: a one-time code ("login_code",
// "otp", "pin") or a link token ("login_token", "reset_token").
func readerToken(name string) bool {
	if oneTimeField(name, "4821") {
		return true
	}
	if !strings.Contains(name, "token") {
		return false
	}
	for _, w := range []string{"login", "magic", "reset", "verif", "confirm", "invite", "signin", "onetime"} {
		if strings.Contains(name, w) {
			return true
		}
	}
	return false
}
