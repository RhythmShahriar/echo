// Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
// SPDX-License-Identifier: MIT

package web

import (
	"slices"
	"strings"
	"unicode"

	"github.com/RhythmShahriar/echo/model"
)

// Query is a parsed search, in the syntax the catcher's q= parameter
// will take: key:value terms and free words, all of which must match,
// case-insensitively. Keys are to, from, subject, channel, action,
// provider, check, site, status and has (has:attachment, has:issue).
// Values with spaces go in quotes (subject:"order shipped"); a leading
// "-" negates a term (-channel:analytics). An unknown key is searched as
// free text, so "http://shop.test" still finds the URL.
type Query struct {
	terms []term
}

type term struct {
	key, value string
	not        bool
}

// keys are the search keys Query knows.
var keys = []string{"to", "from", "subject", "channel", "action", "provider", "check", "site", "status", "has"}

// Parse reads a search string. It never fails: anything it doesn't
// understand is searched as text.
func Parse(s string) Query {
	var q Query
	ws := words(s)
	for i := 0; i < len(ws); i++ {
		w := ws[i]
		t := term{}
		if strings.HasPrefix(w, "-") && len(w) > 1 {
			t.not, w = true, w[1:]
		}
		// "to: ann" is to:ann, as typed with a space.
		if k, ok := strings.CutSuffix(w, ":"); ok && slices.Contains(keys, strings.ToLower(k)) && i+1 < len(ws) {
			i++
			w += ws[i]
		}
		if k, v, ok := strings.Cut(w, ":"); ok && slices.Contains(keys, strings.ToLower(k)) && v != "" {
			t.key, t.value = strings.ToLower(k), strings.ToLower(unquote(v))
		} else {
			t.value = strings.ToLower(unquote(w))
		}
		if t.value != "" {
			q.terms = append(q.terms, t)
		}
	}
	return q
}

// needsFacts reports whether matching needs a message's content checks
// or attachments.
func (q Query) needsFacts() bool {
	return slices.ContainsFunc(q.terms, func(t term) bool { return t.key == "check" || t.key == "has" })
}

// Empty reports whether q matches everything.
func (q Query) Empty() bool { return len(q.terms) == 0 }

// Match reports whether m, with its derived facts, matches every term.
func (q Query) Match(m model.Message, f facts) bool {
	for _, t := range q.terms {
		if t.match(m, f) == t.not {
			return false
		}
	}
	return true
}

func (t term) match(m model.Message, f facts) bool {
	has := func(s string) bool { return strings.Contains(strings.ToLower(s), t.value) }
	anyOf := func(l []string) bool { return slices.ContainsFunc(l, has) }
	switch t.key {
	case "to":
		return anyOf(m.To) || anyOf(m.CC) || anyOf(m.BCC)
	case "from":
		return has(m.From)
	case "subject":
		return has(m.Subject)
	case "channel":
		return strings.EqualFold(string(m.Channel), t.value)
	case "action":
		return has(m.Action) || has(m.Run)
	case "provider":
		return has(m.Provider)
	case "status":
		return strings.EqualFold(string(m.Status), t.value)
	case "site":
		return has(m.Source.Host)
	case "check":
		return slices.ContainsFunc(f.issues, func(i model.Issue) bool { return has(i.Check) })
	case "has":
		switch t.value {
		case "attachment", "attachments":
			return len(f.attachments) > 0
		case "issue", "issues":
			return len(f.issues) > 0
		case "html":
			return m.HTML != ""
		}
		return false
	}
	return has(m.Subject) || has(m.Text) || has(m.From) || anyOf(m.To) || anyOf(m.CC) || has(m.Event) ||
		has(m.URL) || has(m.Provider) || has(m.Action) || has(string(m.Channel)) || has(m.ID)
}

// words splits s on spaces outside double quotes.
func words(s string) []string {
	var out []string
	var b strings.Builder
	quoted := false
	for _, r := range s {
		switch {
		case r == '"':
			quoted = !quoted
			b.WriteRune(r)
		case unicode.IsSpace(r) && !quoted:
			if b.Len() > 0 {
				out = append(out, b.String())
				b.Reset()
			}
		default:
			b.WriteRune(r)
		}
	}
	if b.Len() > 0 {
		out = append(out, b.String())
	}
	return out
}

func unquote(s string) string { return strings.ReplaceAll(s, `"`, "") }
