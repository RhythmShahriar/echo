// Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
// SPDX-License-Identifier: MIT

package compat

import "strings"

type cssKind int

const (
	cssDecl     cssKind = iota // name: value
	cssAt                      // @name, value is the rest of the prelude
	cssSelector                // value is the selector
)

// cssItem is one declaration, at-rule or selector, with its line.
type cssItem struct {
	kind  cssKind
	name  string // lower case: the property or at-rule
	value string
	line  int
}

// scanCSS splits CSS into the items the table matches. It is not a full
// parser: it only needs to tell a selector from a declaration from an
// at-rule, skip comments and keep quoted strings whole, which is all an
// email's style sheet or style attribute asks of it. line is the line css
// starts on.
func scanCSS(css string, line int) []cssItem {
	var items []cssItem
	var buf strings.Builder
	start := line // the line buf's first character is on

	emit := func(prelude bool) {
		text := strings.TrimSpace(buf.String())
		buf.Reset()
		if text == "" {
			return
		}
		if rest, ok := strings.CutPrefix(text, "@"); ok {
			name, value, _ := strings.Cut(rest, " ")
			if i := strings.IndexAny(name, "({;\t\n\"'"); i >= 0 {
				name, value = name[:i], name[i:]+" "+value
			}
			items = append(items, cssItem{kind: cssAt, name: strings.ToLower(name), value: strings.TrimSpace(value), line: start})
			return
		}
		if prelude {
			items = append(items, cssItem{kind: cssSelector, value: text, line: start})
			return
		}
		if name, value, ok := strings.Cut(text, ":"); ok {
			items = append(items, cssItem{kind: cssDecl, name: strings.ToLower(strings.TrimSpace(name)), value: strings.TrimSpace(value), line: start})
		}
	}
	write := func(s string) {
		if buf.Len() == 0 {
			start = line
		}
		buf.WriteString(s)
		line += strings.Count(s, "\n")
	}

	for i := 0; i < len(css); i++ {
		c := css[i]
		switch {
		case strings.HasPrefix(css[i:], "/*"):
			end := strings.Index(css[i+2:], "*/")
			skip := css[i:]
			if end >= 0 {
				skip = css[i : i+2+end+2]
			}
			line += strings.Count(skip, "\n")
			i += len(skip) - 1
		case strings.HasPrefix(css[i:], "<!--"), strings.HasPrefix(css[i:], "-->"):
			// Old mail clients needed the style sheet wrapped in an HTML
			// comment; CSS ignores the markers.
			if c == '<' {
				i += 3
			} else {
				i += 2
			}
		case c == '"' || c == '\'':
			j := i + 1
			for j < len(css) && css[j] != c {
				if css[j] == '\\' {
					j++
				}
				j++
			}
			j = min(j, len(css)-1)
			write(css[i : j+1])
			i = j
		case c == '{':
			emit(true)
		case c == ';' || c == '}':
			emit(false)
		case c == ' ' || c == '\t' || c == '\n' || c == '\r':
			if c == '\n' {
				line++
			}
			if buf.Len() > 0 {
				buf.WriteByte(' ')
			}
		default:
			write(string(c))
		}
	}
	emit(false) // a style attribute's last declaration needs no semicolon
	return items
}
