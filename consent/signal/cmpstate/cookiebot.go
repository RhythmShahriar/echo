// Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
// SPDX-License-Identifier: MIT

package cmpstate

import (
	"strings"

	"github.com/RhythmShahriar/echo/consent"
)

// cookiebotCookie is where Cookiebot keeps the choice.
const cookiebotCookie = "CookieConsent"

// cookiebotCategories are Cookiebot's own category names.
var cookiebotCategories = []string{"necessary", "preferences", "statistics", "marketing"}

// Cookiebot reads the CookieConsent cookie: a JavaScript object literal
// such as {stamp:'...',necessary:true,preferences:false,statistics:false,
// marketing:false,method:'explicit',ver:1,utc:...,region:'de'}, read by a
// small tokenizer (never evaluated). It is decided when method is
// explicit or implied and stamp is set. The value -1 means the visitor's
// region needs no consent: decided, no categories. JSON (the
// Cookiebot.consent object as the API state gives it) reads the same.
// Format: https://www.cookiebot.com/en/developer/ (the Cookiebot.consent
// properties and the CookieConsent cookie).
func Cookiebot(values map[string]string) (*consent.CMPState, error) {
	v, ok, long := value(values, cookiebotCookie)
	if !ok {
		return nil, nil
	}
	source := "cookie:" + cookiebotCookie
	if long {
		return unreadable("cookiebot", source, cookiebotCookie)
	}
	for _, d := range decodings(v) {
		if strings.TrimSpace(d) == "-1" {
			return &consent.CMPState{CMP: "cookiebot", Source: source, Decided: true}, nil
		}
		obj, ok := jsObject(d)
		if !ok {
			continue
		}
		return cookiebotState(obj, source), nil
	}
	return unreadable("cookiebot", source, cookiebotCookie)
}

// cookiebotState turns a read object into a state.
func cookiebotState(obj map[string]jsValue, source string) *consent.CMPState {
	cats := categories{}
	for _, name := range cookiebotCategories {
		if b, ok := obj[name]; ok && b.isBool {
			cats.set(name, b.b)
		}
	}
	method := obj["method"].s
	stamp := obj["stamp"].s
	decided := (method == "explicit" || method == "implied") && stamp != "" && stamp != "0"
	return &consent.CMPState{CMP: "cookiebot", Source: source, Decided: decided, Categories: cats.orNil()}
}

// jsValue is one scalar of an object literal: a string or a boolean.
// Numbers, null and nested values are kept as their kind only.
type jsValue struct {
	s      string
	b      bool
	isBool bool
}

// maxJSDepth bounds how deep a nested value inside the object may go.
const maxJSDepth = 8

// jsObject reads a flat JavaScript object literal: keys bare or quoted,
// values strings in single or double quotes, true, false, null, numbers
// or nested objects and arrays (skipped). ok is false for anything else.
func jsObject(s string) (map[string]jsValue, bool) {
	t := &jsTok{s: strings.TrimSpace(s)}
	if !t.eat('{') {
		return nil, false
	}
	out := map[string]jsValue{}
	t.space()
	if t.eat('}') {
		return out, t.end()
	}
	for {
		key, ok := t.key()
		if !ok || !t.eat(':') {
			return nil, false
		}
		v, ok := t.value(0)
		if !ok {
			return nil, false
		}
		if len(out) < maxCategories {
			out[key] = v
		}
		if t.eat(',') {
			continue
		}
		if t.eat('}') {
			return out, t.end()
		}
		return nil, false
	}
}

// jsTok walks an object literal.
type jsTok struct {
	s string
	i int
}

func (t *jsTok) space() {
	for t.i < len(t.s) && (t.s[t.i] == ' ' || t.s[t.i] == '\t' || t.s[t.i] == '\n' || t.s[t.i] == '\r') {
		t.i++
	}
}

// eat skips spaces and c, reporting whether c was there.
func (t *jsTok) eat(c byte) bool {
	t.space()
	if t.i < len(t.s) && t.s[t.i] == c {
		t.i++
		return true
	}
	return false
}

// end reports whether only spaces are left.
func (t *jsTok) end() bool {
	t.space()
	return t.i == len(t.s)
}

// key reads a bare identifier or a quoted key.
func (t *jsTok) key() (string, bool) {
	t.space()
	if t.i < len(t.s) && (t.s[t.i] == '\'' || t.s[t.i] == '"') {
		return t.str()
	}
	start := t.i
	for t.i < len(t.s) && ident(t.s[t.i]) {
		t.i++
	}
	return t.s[start:t.i], t.i > start
}

// ident reports whether c may be part of a bare key.
func ident(c byte) bool {
	return c == '_' || c == '$' || (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z') || (c >= '0' && c <= '9')
}

// str reads a quoted string; a backslash keeps the next byte as it is.
func (t *jsTok) str() (string, bool) {
	q := t.s[t.i]
	t.i++
	var b strings.Builder
	for t.i < len(t.s) {
		c := t.s[t.i]
		t.i++
		switch c {
		case q:
			return b.String(), true
		case '\\':
			if t.i < len(t.s) {
				b.WriteByte(t.s[t.i])
				t.i++
			}
		default:
			b.WriteByte(c)
		}
	}
	return "", false
}

// value reads one value at nesting depth d.
func (t *jsTok) value(d int) (jsValue, bool) {
	t.space()
	if t.i >= len(t.s) {
		return jsValue{}, false
	}
	switch t.s[t.i] {
	case '\'', '"':
		s, ok := t.str()
		return jsValue{s: s}, ok
	case '{', '[':
		return jsValue{}, t.skip(d + 1)
	}
	start := t.i
	for t.i < len(t.s) && (ident(t.s[t.i]) || t.s[t.i] == '.' || t.s[t.i] == '-' || t.s[t.i] == '+') {
		t.i++
	}
	switch word := t.s[start:t.i]; word {
	case "true":
		return jsValue{b: true, isBool: true}, true
	case "false":
		return jsValue{isBool: true}, true
	case "":
		return jsValue{}, false
	default:
		return jsValue{s: word}, true // a number, null or undefined
	}
}

// skip passes over a nested object or array at depth d.
func (t *jsTok) skip(d int) bool {
	if d > maxJSDepth {
		return false
	}
	open := t.s[t.i]
	closing := byte('}')
	if open == '[' {
		closing = ']'
	}
	t.i++
	if t.eat(closing) {
		return true
	}
	for {
		if open == '{' {
			if _, ok := t.key(); !ok || !t.eat(':') {
				return false
			}
		}
		if _, ok := t.value(d); !ok {
			return false
		}
		if t.eat(',') {
			continue
		}
		return t.eat(closing)
	}
}
