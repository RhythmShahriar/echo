// Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
// SPDX-License-Identifier: MIT

package catcher

import (
	"context"
	"net/http"
	"regexp"
	"slices"
	"strings"
	"time"

	"github.com/RhythmShahriar/echo/codes"
	"github.com/RhythmShahriar/echo/mask"
	"github.com/RhythmShahriar/echo/model"
)

// found is what codes.Extract read from a message before it was masked.
type found struct {
	codes []string
	links []codes.Link
}

func extract(m model.Message) found {
	c, l := codes.Extract(m)
	return found{codes: c, links: l}
}

func (f found) empty() bool { return len(f.codes) == 0 && len(f.links) == 0 }

// hide takes the codes and one-time link tokens f found out of the masked
// message m. Masking catches "code: 482913" but not "482913 is your
// code", a token in a link's path (/reset-password/<token>,
// /login/<token>) or under a query key only a sign-in link uses for one
// (?t=); the code must never be stored. It also changes on every send, so
// a comparison would see it as a change.
func (f found) hide(m *model.Message) {
	var pairs []string
	for _, l := range f.links {
		if l.Kind == codes.Unsubscribe {
			continue // not a credential
		}
		// The link as masking left it, in text and in HTML.
		for _, u := range []string{l.URL, strings.ReplaceAll(l.URL, "&", "&amp;")} {
			if stored := mask.URL(u); hideLink(stored) != stored {
				pairs = append(pairs, stored, hideLink(stored))
			}
		}
	}

	var code *regexp.Regexp
	if len(f.codes) > 0 {
		// Whole words only: 4821 mustn't eat into 48210.
		quoted := make([]string, len(f.codes))
		for i, c := range f.codes {
			quoted[i] = regexp.QuoteMeta(c)
		}
		code = regexp.MustCompile(`\b(?:` + strings.Join(quoted, "|") + `)\b`)
	}
	if code == nil && pairs == nil {
		return
	}
	links := strings.NewReplacer(pairs...)
	replace := func(s string) string {
		if s == "" {
			return s
		}
		if code != nil {
			s = code.ReplaceAllLiteralString(s, mask.Hidden)
		}
		return links.Replace(s)
	}
	m.Subject, m.Text, m.HTML, m.Body, m.URL = replace(m.Subject), replace(m.Text), replace(m.HTML), replace(m.Body), replace(m.URL)
	for k, v := range m.Headers {
		m.Headers[k] = replace(v)
	}
	if m.Response != nil {
		m.Response.Body = replace(m.Response.Body)
	}
}

// randomSegment is a path segment that reads like a token: long, letters
// and digits mixed.
var randomSegment = regexp.MustCompile(`^[A-Za-z0-9_-]{16,}$`)

// hideLink masks the path segments of u that look like a token and the
// values of the query keys codes.TokenKey names. The query separator may
// be "&amp;" in HTML.
func hideLink(u string) string {
	head, query, hasQuery := strings.Cut(u, "?")
	parts := strings.Split(head, "/")
	for i := 3; i < len(parts); i++ { // after scheme://host
		p := parts[i]
		if randomSegment.MatchString(p) && strings.ContainsAny(p, "0123456789") && strings.ContainsFunc(p, isLetter) {
			parts[i] = mask.Hidden
		}
	}
	out := strings.Join(parts, "/")
	if !hasQuery {
		return out
	}
	query, frag, hasFrag := strings.Cut(query, "#")
	fields := strings.Split(query, "&")
	for i, kv := range fields {
		k, v, ok := strings.Cut(kv, "=")
		if ok && v != "" && v != mask.Hidden && codes.TokenKey(strings.TrimPrefix(k, "amp;")) {
			fields[i] = k + "=" + mask.Hidden
		}
	}
	out += "?" + strings.Join(fields, "&")
	if hasFrag {
		out += "#" + frag
	}
	return out
}

func isLetter(r rune) bool { return 'a' <= r && r <= 'z' || 'A' <= r && r <= 'Z' }

// Latest returns the newest message matching f and, when channels isn't
// empty, on one of them, with the codes and links read from it before
// masking. It waits for one until ctx ends.
func (in *Inbox) Latest(ctx context.Context, f Filter, channels []model.Channel) (m model.Message, cs []string, ls []codes.Link, ok bool) {
	for {
		in.mu.Lock()
		for i := len(in.msgs) - 1; i >= 0; i-- {
			e := in.msgs[i]
			if f.match(e.m) && (len(channels) == 0 || slices.Contains(channels, e.m.Channel)) {
				x := in.found[e.m.ID]
				in.mu.Unlock()
				return e.m, x.codes, x.links, true
			}
		}
		ch := in.changed
		in.mu.Unlock()
		select {
		case <-ctx.Done():
			return model.Message{}, nil, nil, false
		case <-ch:
		}
	}
}

// latestAnswer is what GET /v1/messages/latest answers.
type latestAnswer struct {
	Codes   []string      `json:"codes"`
	Links   []codes.Link  `json:"links"`
	Message model.Message `json:"message"`
}

// latest answers the newest message matching the filter (run, action,
// to, since, q) on one of the channels in channel (comma-separated), with
// the codes and links read from it before masking: what a login or
// checkout test needs next. With wait it waits up to that long for one,
// at most a minute; it answers 404 when none came.
func (s *Server) latest(w http.ResponseWriter, r *http.Request) {
	f, ok := filter(w, r)
	if !ok {
		return
	}
	q := r.URL.Query()
	var channels []model.Channel
	for c := range strings.SplitSeq(q.Get("channel"), ",") {
		if c = strings.TrimSpace(c); c == "" {
			continue
		}
		if !slices.Contains(model.Channels, model.Channel(c)) {
			http.Error(w, "channel: "+c+" is not a channel ("+channelNames()+")", http.StatusBadRequest)
			return
		}
		channels = append(channels, model.Channel(c))
	}
	f.Channel = "" // channels holds them
	wait := duration(q.Get("wait"), 0, time.Minute)
	ctx, cancel := context.WithTimeout(r.Context(), wait)
	defer cancel()
	m, cs, ls, ok := s.Inbox.Latest(ctx, f, channels)
	if !ok {
		msg := "no message matched"
		if wait > 0 {
			msg += " within " + wait.String()
		}
		http.Error(w, msg, http.StatusNotFound)
		return
	}
	writeJSON(w, http.StatusOK, latestAnswer{Codes: nonNilSlice(cs), Links: nonNilSlice(ls), Message: m})
}

func nonNilSlice[T any](s []T) []T {
	if s == nil {
		return []T{}
	}
	return s
}
