// Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
// SPDX-License-Identifier: MIT

package catcher

import (
	"errors"
	"fmt"
	"regexp"
	"slices"
	"strings"

	"github.com/RhythmShahriar/echo/model"
)

// Query is a parsed search over messages, as GET /v1/messages takes it in
// q. The zero Query matches every message.
type Query struct {
	terms []term
}

type term struct {
	field string         // "" for free text
	value string         // lowercased
	re    *regexp.Regexp // a case-insensitive substring match, for text fields
}

// queryFields are the field names a search takes, each as field:value.
var queryFields = []string{"to", "from", "subject", "channel", "action", "provider", "check", "site", "has"}

// hasValues are what has: takes.
var hasValues = []string{"attachment", "issue"}

// ParseQuery reads a search: words separated by spaces, all of which must
// match. A word is field:value or free text; either may be quoted to hold
// spaces (subject:"order shipped", "thanks for your order"). Fields:
//
//	to:       part of a recipient (To, Cc or Bcc)
//	from:     part of the sender
//	subject:  part of the subject
//	channel:  a channel, or several joined by commas: channel:email,sms
//	action:   the action that caused the message (late mail included)
//	provider: twilio, sendgrid, slack…
//	check:    a problem found in the message: check:content/dev-link, or
//	          check:content for every content check
//	site:     part of the sending site's name or host
//	has:      attachment or issue
//
// Free text is looked for in the subject, text, HTML, addresses, URL,
// event and body. Case never matters. A word whose prefix isn't a field
// (https://shop.test) is free text.
func ParseQuery(s string) (Query, error) {
	words, err := splitQuery(s)
	if err != nil {
		return Query{}, err
	}
	var q Query
	for _, w := range words {
		field, value := w.field, w.text
		if !w.quoted {
			if k, v, ok := strings.Cut(w.text, ":"); ok && slices.Contains(queryFields, strings.ToLower(k)) {
				field, value = strings.ToLower(k), v
			}
		}
		t, err := newTerm(field, value)
		if err != nil {
			return Query{}, err
		}
		q.terms = append(q.terms, t)
	}
	return q, nil
}

func newTerm(field, value string) (term, error) {
	if value == "" {
		if field == "" {
			return term{}, errors.New(`empty quotes in the search`)
		}
		return term{}, fmt.Errorf("%s: needs a value", field)
	}
	t := term{field: field, value: strings.ToLower(value)}
	switch field {
	case "channel":
		for c := range strings.SplitSeq(t.value, ",") {
			if !slices.Contains(model.Channels, model.Channel(c)) {
				return term{}, fmt.Errorf("channel:%s: not a channel (%s)", c, channelNames())
			}
		}
	case "has":
		t.value = strings.TrimSuffix(t.value, "s") // has:attachments, has:issues
		if !slices.Contains(hasValues, t.value) {
			return term{}, fmt.Errorf("has:%s: has takes %s", value, strings.Join(hasValues, " or "))
		}
	case "action", "provider", "check":
	default: // free text and the fields matched as part of a text
		t.re = regexp.MustCompile("(?i)" + regexp.QuoteMeta(value))
	}
	return t, nil
}

func channelNames() string {
	names := make([]string, len(model.Channels))
	for i, c := range model.Channels {
		names[i] = string(c)
	}
	return strings.Join(names, ", ")
}

// word is one word of a search before it's read as a term.
type word struct {
	text   string
	quoted bool   // text was in quotes: it's a value, whatever colons it has
	field  string // the field before the quotes: subject:"a b"
}

// splitQuery splits s at spaces outside double quotes.
func splitQuery(s string) ([]word, error) {
	var words []word
	for {
		s = strings.TrimLeft(s, " \t\r\n")
		if s == "" {
			return words, nil
		}
		end := strings.IndexAny(s, " \t\r\n\"")
		if end < 0 {
			return append(words, word{text: s}), nil
		}
		if s[end] != '"' {
			words, s = append(words, word{text: s[:end]}), s[end:]
			continue
		}
		// A quote starts the word (free text) or follows field: in it.
		prefix := s[:end]
		closing := strings.IndexByte(s[end+1:], '"')
		if closing < 0 {
			return nil, errors.New("a quote in the search isn't closed")
		}
		value := s[end+1 : end+1+closing]
		rest := s[end+2+closing:]
		switch k, ok := strings.CutSuffix(prefix, ":"); {
		case prefix == "":
			words = append(words, word{text: value, quoted: true})
		case ok && slices.Contains(queryFields, strings.ToLower(k)):
			words = append(words, word{text: value, field: strings.ToLower(k), quoted: true})
		default:
			return nil, fmt.Errorf("a quote in the middle of %s", prefix+`"`)
		}
		if rest != "" && !strings.ContainsAny(rest[:1], " \t\r\n") {
			return nil, errors.New("a closing quote must end the word")
		}
		s = rest
	}
}

// Match reports whether m matches every term of q.
func (q Query) Match(m model.Message) bool {
	for _, t := range q.terms {
		if !t.match(m) {
			return false
		}
	}
	return true
}

func (t term) match(m model.Message) bool {
	switch t.field {
	case "":
		return t.any(m.Subject, m.Text, m.HTML, m.From, m.ReplyTo, m.URL, m.Event, m.Body) ||
			t.any(m.To...) || t.any(m.CC...) || t.any(m.BCC...)
	case "to":
		return t.any(m.To...) || t.any(m.CC...) || t.any(m.BCC...)
	case "from":
		return t.any(m.From)
	case "subject":
		return t.any(m.Subject)
	case "site":
		return t.any(m.Source.Site, m.Source.Host)
	case "channel":
		return slices.Contains(strings.Split(t.value, ","), string(m.Channel))
	case "action":
		return strings.EqualFold(m.Action, t.value) || m.Attribution == model.Late && strings.EqualFold(m.LateFor, t.value)
	case "provider":
		return strings.EqualFold(m.Provider, t.value)
	case "check":
		return slices.ContainsFunc(m.Issues, func(i model.Issue) bool {
			c := strings.ToLower(i.Check)
			return c == t.value || strings.HasPrefix(c, t.value+"/")
		})
	case "has":
		if t.value == "issue" {
			return len(m.Issues) > 0
		}
		return len(m.Attachments) > 0
	}
	return false
}

// any reports whether any of vs contains t's value.
func (t term) any(vs ...string) bool {
	return slices.ContainsFunc(vs, t.re.MatchString)
}
