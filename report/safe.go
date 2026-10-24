// Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
// SPDX-License-Identifier: MIT

package report

import (
	"fmt"
	"strings"
	"unicode/utf8"
)

// Safe makes captured text harmless to print on a terminal. A subject,
// recipient or URL comes from the site (or from anyone who can POST to
// Echo), so it may carry escape sequences that clear the screen, rewrite
// earlier lines to hide a BROKE result, set the window title or plant OSC 8
// links. Safe writes C0 controls (except tab), DEL and invalid UTF-8 (which
// includes lone surrogates) as \xNN, and C1 controls as \u00NN. Everything
// else, emoji and right-to-left text included, is kept as is.
func Safe(s string) string {
	if !needsEscape(s) {
		return s
	}
	var b strings.Builder
	b.Grow(len(s) + 8)
	for i := 0; i < len(s); {
		r, size := utf8.DecodeRuneInString(s[i:])
		switch {
		case r == utf8.RuneError && size <= 1:
			fmt.Fprintf(&b, `\x%02x`, s[i])
		case r < 0x20 && r != '\t', r == 0x7f:
			fmt.Fprintf(&b, `\x%02x`, r)
		case r >= 0x80 && r <= 0x9f:
			fmt.Fprintf(&b, `\u%04x`, r)
		default:
			b.WriteString(s[i : i+size])
		}
		i += size
	}
	return b.String()
}

// safeLines is Safe for text where line breaks are meant (a JUnit body):
// each line is made safe and the newlines are kept.
func safeLines(s string) string {
	lines := strings.Split(s, "\n")
	for i, l := range lines {
		lines[i] = Safe(l)
	}
	return strings.Join(lines, "\n")
}

func needsEscape(s string) bool {
	for i := 0; i < len(s); {
		c := s[i]
		if c < utf8.RuneSelf {
			if (c < 0x20 && c != '\t') || c == 0x7f {
				return true
			}
			i++
			continue
		}
		r, size := utf8.DecodeRuneInString(s[i:])
		if (r == utf8.RuneError && size <= 1) || (r >= 0x80 && r <= 0x9f) {
			return true
		}
		i += size
	}
	return false
}
