// Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
// SPDX-License-Identifier: MIT

package consent

import (
	"errors"
	"fmt"
	"strings"
)

// The bounds of a --accept-selector or --reject-selector: a banner has
// at most a few layers and a selector is a line, not a document.
const (
	maxSelectorBytes = 1024
	maxSelectorSteps = 4
	maxShadowHosts   = 8
)

// stepSep and shadowSep are the two operators of a selector chain.
const (
	stepSep   = ">>"
	shadowSep = "::shadow"
)

// ParseSelector reads --accept-selector/--reject-selector:
// "a >> b" is two steps; "host::shadow button" enters host's shadow root.
// Each step is one Locator, clicked in order. A step's CSS must be
// balanced in quotes, brackets and parentheses; the CSS itself is
// checked by the browser.
func ParseSelector(s string) ([]Locator, error) {
	if strings.TrimSpace(s) == "" {
		return nil, errors.New("selector is empty")
	}
	if len(s) > maxSelectorBytes {
		return nil, fmt.Errorf("selector is %d bytes; keep it under %d", len(s), maxSelectorBytes)
	}
	for _, r := range s {
		if r < 0x20 || r == 0x7f || bidiControl(r) {
			return nil, fmt.Errorf("selector %q has a control character", SafeText(s, 80))
		}
	}
	steps := splitOutside(s, stepSep)
	if len(steps) > maxSelectorSteps {
		return nil, fmt.Errorf("selector %q has %d steps; at most %d", SafeText(s, 80), len(steps), maxSelectorSteps)
	}
	out := make([]Locator, 0, len(steps))
	for i, step := range steps {
		parts := splitOutside(step, shadowSep)
		if len(parts)-1 > maxShadowHosts {
			return nil, fmt.Errorf("selector %q: step %d enters %d shadow roots; at most %d", SafeText(s, 80), i+1, len(parts)-1, maxShadowHosts)
		}
		var l Locator
		for j, p := range parts {
			p = strings.TrimSpace(p)
			if p == "" {
				what := fmt.Sprintf("step %d is empty", i+1)
				if len(parts) > 1 {
					what = fmt.Sprintf("step %d has an empty part around ::shadow", i+1)
				}
				return nil, fmt.Errorf("selector %q: %s", SafeText(s, 80), what)
			}
			if err := balanced(p); err != nil {
				return nil, fmt.Errorf("selector %q: step %d: %w", SafeText(s, 80), i+1, err)
			}
			if j < len(parts)-1 {
				l.Shadow = append(l.Shadow, p)
			} else {
				l.CSS = p
			}
		}
		out = append(out, l)
	}
	return out, nil
}

// splitOutside splits s at every sep that isn't inside quotes, brackets
// or parentheses, so [aria-label=">>"] stays one selector.
func splitOutside(s, sep string) []string {
	var out []string
	depth, start := 0, 0
	var quote byte
	for i := 0; i < len(s); i++ {
		c := s[i]
		switch {
		case quote != 0:
			switch c {
			case '\\':
				i++
			case quote:
				quote = 0
			}
		case c == '\\':
			i++
		case c == '"' || c == '\'':
			quote = c
		case c == '[' || c == '(':
			depth++
		case c == ']' || c == ')':
			if depth > 0 {
				depth--
			}
		case depth == 0 && strings.HasPrefix(s[i:], sep):
			out = append(out, s[start:i])
			start = i + len(sep)
			i += len(sep) - 1
		}
	}
	return append(out, s[start:])
}

// balanced checks that quotes, brackets and parentheses in css close in
// order.
func balanced(css string) error {
	var stack []byte
	var quote byte
	for i := 0; i < len(css); i++ {
		c := css[i]
		switch {
		case quote != 0:
			switch c {
			case '\\':
				i++
			case quote:
				quote = 0
			}
		case c == '\\':
			i++
		case c == '"' || c == '\'':
			quote = c
		case c == '[':
			stack = append(stack, ']')
		case c == '(':
			stack = append(stack, ')')
		case c == ']' || c == ')':
			if len(stack) == 0 || stack[len(stack)-1] != c {
				return fmt.Errorf("unexpected %c", c)
			}
			stack = stack[:len(stack)-1]
		}
	}
	switch {
	case quote != 0:
		return fmt.Errorf("unclosed %c", quote)
	case len(stack) > 0:
		return fmt.Errorf("missing %c", stack[len(stack)-1])
	}
	return nil
}
