// Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
// SPDX-License-Identifier: MIT

package mcp

import (
	"html"
	"regexp"
	"slices"
	"strings"

	"github.com/RhythmShahriar/echo/model"
)

var (
	// codeWord marks where a one-time code is: "Your code is 482913",
	// "482913 is your verification code", "?code=482913".
	codeWord = regexp.MustCompile(`(?i)\b(?:code|codes|otp|passcode|pin|verification|verify|one[- ]time|2fa|two[- ]factor|security|login|sign[- ]in)\b`)
	// codeValue is a code: 4 to 8 digits, or 6 to 8 capitals and digits
	// with at least one digit ("K7Q2ZP"), or two such groups with a dash
	// ("ABC-123").
	codeValue = regexp.MustCompile(`\b(\d{4,8}|[A-Z0-9]{6,8}|[A-Z0-9]{3,4}-[A-Z0-9]{3,4})\b`)
	// linkCode is a code in a verify link, which stripping the HTML loses.
	linkCode  = regexp.MustCompile(`(?i)[?&](?:code|otp|pin)=(\d{4,8}|[A-Z0-9]{6,8})\b`)
	sixDigits = regexp.MustCompile(`\b\d{6}\b`)
	markup    = regexp.MustCompile(`(?s)<style.*?</style>|<script.*?</script>|<[^>]+>`)
	digit     = regexp.MustCompile(`\d`)
	year      = regexp.MustCompile(`^(?:19|20)\d\d$`)
)

// Code returns the one-time code in m, or "". It looks next to the words
// that announce a code (within 60 characters after one, or 40 before),
// then in verify links (?code=), then falls back to a lone six-digit
// number in a short text. Four-digit years are skipped.
func Code(m model.Message) string {
	text := m.Text
	if strings.TrimSpace(text) == "" {
		text = html.UnescapeString(markup.ReplaceAllString(m.HTML, " "))
	}
	for _, s := range []string{m.Subject, text} {
		if c := near(s); c != "" {
			return c
		}
	}
	if l := linkCode.FindStringSubmatch(m.Text + "\n" + m.HTML); l != nil {
		return l[1]
	}
	if len(text) < 500 {
		if all := sixDigits.FindAllString(m.Subject+"\n"+text, -1); len(all) == 1 {
			return all[0]
		}
	}
	return ""
}

func near(s string) string {
	for _, w := range codeWord.FindAllStringIndex(s, -1) {
		after := s[w[1]:min(len(s), w[1]+60)]
		if c := firstCode(after, false); c != "" {
			return c
		}
		before := s[max(0, w[0]-40):w[0]]
		if c := firstCode(before, true); c != "" {
			return c
		}
	}
	return ""
}

// firstCode returns the code in s nearest the keyword: the first one
// after it, or the last one before it.
func firstCode(s string, last bool) string {
	all := codeValue.FindAllString(s, -1)
	if last {
		slices.Reverse(all)
	}
	for _, c := range all {
		if !digit.MatchString(c) || year.MatchString(c) {
			continue
		}
		return c
	}
	return ""
}
