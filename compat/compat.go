// Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
// SPDX-License-Identifier: MIT

// Package compat checks a captured email the way a comparison page expects
// a mail catcher to: HTML reports the HTML and CSS features that common
// clients drop (flexbox in Outlook for Windows, a <style> block Gmail
// throws away and similar) and Spam asks a SpamAssassin daemon for a
// score. Neither renders the email; both are hints at notice level, not
// proof that something broke.
//
// The feature table is features.yaml, embedded at build time. It is a
// curated subset of caniemail.com's data (MIT license, see
// CANIEMAIL-LICENSE).
package compat

import (
	_ "embed"
	"fmt"
	"regexp"
	"slices"
	"strings"

	"gopkg.in/yaml.v3"
)

//go:embed features.yaml
var featuresYAML []byte

// Finding is one feature an email uses that some clients don't support.
type Finding struct {
	// ID names the feature in the table, like "css-display-flex".
	ID string `json:"id"`
	// Feature is what the author wrote, like "display: flex".
	Feature string `json:"feature"`
	// Unsupported lists the clients that drop the feature.
	Unsupported []string `json:"unsupported,omitempty"`
	// Partial lists the clients that support it only in some cases.
	Partial []string `json:"partial,omitempty"`
	// Note says what happens in those clients or what to do instead.
	Note string `json:"note"`
	// Count is how often the email uses it and Line (1-based) where first.
	Count int `json:"count"`
	Line  int `json:"line"`
}

// String is the finding on one line, for a check's message:
//
//	display: flex (line 12, 3 times): not in Outlook for Windows; partly in Gmail
func (f Finding) String() string {
	where := fmt.Sprintf("line %d", f.Line)
	if f.Count > 1 {
		where += fmt.Sprintf(", %d times", f.Count)
	}
	var parts []string
	if len(f.Unsupported) > 0 {
		parts = append(parts, "not in "+strings.Join(f.Unsupported, ", "))
	}
	if len(f.Partial) > 0 {
		parts = append(parts, "partly in "+strings.Join(f.Partial, ", "))
	}
	return fmt.Sprintf("%s (%s): %s", f.Feature, where, strings.Join(parts, "; "))
}

// feature is one row of the table, compiled.
type feature struct {
	ID      string   `yaml:"id"`
	Title   string   `yaml:"title"`
	None    []string `yaml:"none"`
	Partial []string `yaml:"partial"`
	Note    string   `yaml:"note"`
	Match   struct {
		CSS       []string `yaml:"css"`
		On        []string `yaml:"on"`
		Function  string   `yaml:"function"`
		At        string   `yaml:"at"`
		Pseudo    string   `yaml:"pseudo"`
		Element   []string `yaml:"element"`
		Attr      string   `yaml:"attr"`
		Value     string   `yaml:"value"`
		StyleOver int      `yaml:"style_over"`
	} `yaml:"match"`

	value *regexp.Regexp // Match.Value, or the function or pseudo pattern
}

// table is the feature table, loaded once; clients maps a client's key to
// its display name.
var table, clients = mustLoad(featuresYAML)

func mustLoad(data []byte) ([]*feature, map[string]string) {
	t, c, err := load(data)
	if err != nil {
		// The table ships inside the binary and a test loads it, so this
		// only fires for a broken build.
		panic("compat: features.yaml: " + err.Error())
	}
	return t, c
}

func load(data []byte) ([]*feature, map[string]string, error) {
	var doc struct {
		Clients  map[string]string `yaml:"clients"`
		Features []*feature        `yaml:"features"`
	}
	if err := yaml.Unmarshal(data, &doc); err != nil {
		return nil, nil, err
	}
	seen := map[string]bool{}
	for _, f := range doc.Features {
		if f.ID == "" || f.Title == "" || f.Note == "" {
			return nil, nil, fmt.Errorf("feature %q: wants id, title and note", f.ID)
		}
		if seen[f.ID] {
			return nil, nil, fmt.Errorf("feature %q: listed twice", f.ID)
		}
		seen[f.ID] = true
		if len(f.None)+len(f.Partial) == 0 {
			return nil, nil, fmt.Errorf("feature %q: names no client", f.ID)
		}
		for _, c := range append(slices.Clone(f.None), f.Partial...) {
			if _, ok := doc.Clients[c]; !ok {
				return nil, nil, fmt.Errorf("feature %q: unknown client %q", f.ID, c)
			}
		}
		m := &f.Match
		kinds := 0
		for _, set := range []bool{len(m.CSS) > 0, m.Function != "", m.At != "", m.Pseudo != "", len(m.Element) > 0, m.StyleOver > 0} {
			if set {
				kinds++
			}
		}
		if kinds != 1 {
			return nil, nil, fmt.Errorf("feature %q: wants exactly one of css, function, at, pseudo, element or style_over", f.ID)
		}
		pattern := m.Value
		switch {
		case m.Function != "":
			pattern = `\b` + m.Function + `\s*\(`
		case m.Pseudo != "":
			// One colon only: ::hover would be a pseudo-element.
			pattern = `(^|[^:]):` + regexp.QuoteMeta(m.Pseudo) + `\b`
		}
		if pattern != "" {
			re, err := regexp.Compile("(?i)" + pattern)
			if err != nil {
				return nil, nil, fmt.Errorf("feature %q: %w", f.ID, err)
			}
			f.value = re
		}
		m.CSS = lower(m.CSS)
		m.On = lower(m.On)
		m.Element = lower(m.Element)
		m.Attr = strings.ToLower(m.Attr)
		m.At = strings.ToLower(m.At)
	}
	return doc.Features, doc.Clients, nil
}

func lower(s []string) []string {
	for i := range s {
		s[i] = strings.ToLower(s[i])
	}
	return s
}

// has reports whether set holds s or the wildcard "*".
func has(set []string, s string) bool {
	return slices.Contains(set, s) || slices.Contains(set, "*")
}
