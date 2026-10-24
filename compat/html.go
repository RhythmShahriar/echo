// Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
// SPDX-License-Identifier: MIT

package compat

import (
	"cmp"
	"regexp"
	"slices"
	"strings"

	"golang.org/x/net/html"
)

// outlookWindows is the client key that conditional comments target.
const outlookWindows = "outlook-windows"

// HTML lists the features in an email's HTML body that some clients don't
// support, ordered by where each first appears. A clean table layout with
// inline styles gives none.
//
// Content inside <!--[if !mso]><!--> ... <!--<![endif]--> is hidden from
// Outlook for Windows, so a feature used only there isn't held against it;
// content inside <!--[if mso]> ... <![endif]--> is Outlook's own fallback
// and isn't checked at all.
func HTML(body string) []Finding {
	var w walker
	w.run(body)
	return w.findings()
}

// use is one place the email uses a feature.
type use struct {
	f     *feature
	line  int
	noMso bool // hidden from Outlook for Windows
}

type walker struct {
	uses  []use
	noMso bool
}

func (w *walker) add(f *feature, line int) {
	w.uses = append(w.uses, use{f: f, line: line, noMso: w.noMso})
}

// notMso opens a block hidden from Outlook: [if !mso] (also [if !mso | IE]).
var notMso = regexp.MustCompile(`(?i)^\s*\[if\s+!\s*mso\b`)

func (w *walker) run(body string) {
	z := html.NewTokenizer(strings.NewReader(body))
	line := 1
	var style *strings.Builder // the open <style> block's text
	styleLine := 0
	for {
		tt := z.Next()
		if tt == html.ErrorToken {
			return
		}
		// Raw must be read before TagName or TagAttr, which may rewrite
		// the buffer it points into.
		at := line
		line += strings.Count(string(z.Raw()), "\n")
		switch tt {
		case html.CommentToken:
			data := string(z.Text())
			switch {
			case notMso.MatchString(data):
				w.noMso = true
			case strings.Contains(data, "[endif]"):
				w.noMso = false
			}
		case html.StartTagToken, html.SelfClosingTagToken:
			name, hasAttr := z.TagName()
			tag := string(name)
			attrs := map[string]string{}
			for hasAttr {
				var k, v []byte
				k, v, hasAttr = z.TagAttr()
				attrs[string(k)] = string(v)
			}
			w.element(tag, attrs, at)
			if s, ok := attrs["style"]; ok {
				for _, it := range scanCSS(s, at) {
					w.css(it, tag)
				}
			}
			if tag == "style" && tt == html.StartTagToken {
				style, styleLine = &strings.Builder{}, at
			}
		case html.TextToken:
			if style != nil {
				if style.Len() == 0 {
					styleLine = at
				}
				style.Write(z.Raw())
			}
		case html.EndTagToken:
			if name, _ := z.TagName(); string(name) == "style" && style != nil {
				w.styleBlock(style.String(), styleLine)
				style = nil
			}
		}
	}
}

// element matches a start tag against the element features.
func (w *walker) element(tag string, attrs map[string]string, line int) {
	for _, f := range table {
		m := &f.Match
		if !slices.Contains(m.Element, tag) {
			continue
		}
		if m.Attr != "" {
			v, ok := attrs[m.Attr]
			if !ok || (f.value != nil && !f.value.MatchString(v)) {
				continue
			}
		}
		w.add(f, line)
	}
}

// styleBlock checks the CSS of one <style> element.
func (w *walker) styleBlock(css string, line int) {
	for _, f := range table {
		if f.Match.StyleOver > 0 && len(css) > f.Match.StyleOver {
			w.add(f, line)
		}
	}
	for _, it := range scanCSS(css, line) {
		w.css(it, "")
	}
}

// css matches one CSS item; elem is the element of an inline style, empty
// in a <style> block.
func (w *walker) css(it cssItem, elem string) {
	for _, f := range table {
		m := &f.Match
		var ok bool
		switch it.kind {
		case cssDecl:
			switch {
			case len(m.CSS) > 0:
				ok = has(m.CSS, it.name) &&
					(f.value == nil || f.value.MatchString(it.value)) &&
					(len(m.On) == 0 || (elem != "" && has(m.On, elem)))
			case m.Function != "":
				ok = f.value.MatchString(it.value)
			}
		case cssAt:
			ok = m.At != "" && m.At == it.name
		case cssSelector:
			ok = m.Pseudo != "" && f.value.MatchString(it.value)
		}
		if ok {
			w.add(f, it.line)
		}
	}
}

func (w *walker) findings() []Finding {
	byID := map[string]int{} // feature ID to index in out
	var out []Finding
	var first []use
	allNoMso := map[string]bool{}
	for _, u := range w.uses {
		i, ok := byID[u.f.ID]
		if !ok {
			i = len(out)
			byID[u.f.ID] = i
			out = append(out, Finding{ID: u.f.ID, Feature: u.f.Title, Note: u.f.Note, Line: u.line})
			first = append(first, u)
			allNoMso[u.f.ID] = true
		}
		out[i].Count++
		out[i].Line = min(out[i].Line, u.line)
		allNoMso[u.f.ID] = allNoMso[u.f.ID] && u.noMso
	}
	kept := out[:0]
	for i, f := range out {
		skip := ""
		if allNoMso[f.ID] {
			skip = outlookWindows
		}
		f.Unsupported = names(first[i].f.None, skip)
		f.Partial = names(first[i].f.Partial, skip)
		if len(f.Unsupported)+len(f.Partial) > 0 {
			kept = append(kept, f)
		}
	}
	slices.SortStableFunc(kept, func(a, b Finding) int { return cmp.Compare(a.Line, b.Line) })
	return kept
}

// names turns client keys into display names, leaving out skip.
func names(keys []string, skip string) []string {
	var out []string
	for _, k := range keys {
		if k != skip {
			out = append(out, clients[k])
		}
	}
	return out
}
