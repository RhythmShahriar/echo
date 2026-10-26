// Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
// SPDX-License-Identifier: MIT

package tracker

import (
	"errors"
	"fmt"
	"slices"
	"strings"

	"github.com/RhythmShahriar/echo/consent"
)

// checkOverlap refuses a catalog where a script rule and a hit rule can
// both fit one URL. A hit is held; holding a tag script would make
// nothing fire and the site falsely pass, so no script may ever look
// like a hit, whichever entry either rule belongs to.
//
// The check is on hosts, methods and paths only. Parameter rules are
// left out on purpose: a script URL can carry any parameter, so they
// can't keep two rules apart. The overlap also hides real hits: within
// a tier scripts are tried first, so a hit that fits a script rule
// would be reported as a script load and never counted.
func checkOverlap(entries []*entry) error {
	var scripts, hits []*rule
	for _, e := range entries {
		for _, r := range e.rules {
			switch r.kind {
			case consent.HitScript:
				scripts = append(scripts, r)
			case consent.HitBeacon:
				hits = append(hits, r)
			}
		}
	}
	var errs []error
	for _, s := range scripts {
		for _, h := range hits {
			p, ok := overlaps(s, h)
			switch {
			case !ok:
			case h.e.custom && !s.e.custom:
				// Told from the config's side, so every error about the
				// config's own trackers starts with its prefix.
				errs = append(errs, fmt.Errorf("%s: a hit of %s and a script of %s (%s) both fit %s; a script must never look like a hit",
					h.e.where(h.line), h.e.info.ID, s.e.info.ID, s.e.where(s.line), p))
			default:
				errs = append(errs, fmt.Errorf("%s: a script of %s and a hit of %s (%s) both fit %s; a script must never look like a hit",
					s.e.where(s.line), s.e.info.ID, h.e.info.ID, h.e.where(h.line), p))
			}
		}
	}
	return errors.Join(errs...)
}

// overlaps reports whether a script rule and a hit rule can fit one
// request, with a pattern pair that shows it.
func overlaps(s, h *rule) (string, bool) {
	if !getAllowed(s.methods) || !getAllowed(h.methods) {
		// Scripts load with GET; a hit rule that never takes GET can't
		// see a script load.
		return "", false
	}
	if h.anyHost != s.anyHost {
		// Rules on any host stand for first-party and proxied traffic and
		// are tried after every host rule. A vendor's own hosts follow
		// that vendor's rules: a Matomo loader path on Criteo's pixel
		// host, or Adobe's beacon path on Adobe's script CDN, never
		// happens. Host rules are checked against host rules (the same
		// host is where a real script and a real hit meet) and any-host
		// rules against any-host rules.
		return "", false
	}
	host, ok := hostsOverlap(s, h)
	if !ok {
		return "", false
	}
	for _, a := range orAny(s.paths) {
		for _, b := range orAny(h.paths) {
			if globsMeet(a, b) {
				return fmt.Sprintf("%s (script path %s, hit path %s)", host, show(a), show(b)), true
			}
		}
	}
	return "", false
}

// getAllowed reports whether a rule's methods let a GET through.
func getAllowed(methods []string) bool {
	return len(methods) == 0 || slices.Contains(methods, "GET")
}

// orAny is a rule's paths, or the one pattern "" (every path) when it
// has none.
func orAny(paths []string) []string {
	if len(paths) == 0 {
		return []string{""}
	}
	return paths
}

// show names a path pattern in an error.
func show(p string) string {
	if p == "" {
		return "(any)"
	}
	return p
}

// hostsOverlap reports whether some host fits both rules and names it.
func hostsOverlap(a, b *rule) (string, bool) {
	if a.anyHost && b.anyHost {
		return "any host", true
	}
	for _, x := range a.exact {
		if slices.Contains(b.exact, x) || slices.ContainsFunc(b.suffix, func(s string) bool { return strings.HasSuffix(x, s) }) {
			return x, true
		}
	}
	for _, s := range a.suffix {
		for _, x := range b.exact {
			if strings.HasSuffix(x, s) {
				return x, true
			}
		}
		for _, t := range b.suffix {
			if strings.HasSuffix(s, t) {
				return "*" + s, true
			}
			if strings.HasSuffix(t, s) {
				return "*" + t, true
			}
		}
	}
	return "", false
}

// A glob is matched against paths by a small automaton; two globs meet
// when the product of their automata reaches a state both accept.
// Character classes are a literal byte, any byte but "/" (what * and ?
// stand for in path.Match) or any byte at all (below /**). A [class]
// counts as any byte but "/", which can only report an overlap that
// isn't there, never miss one.
const (
	clsLit = iota
	clsSeg
	clsAll
)

// edge is one transition of a glob automaton.
type edge struct {
	cls int
	c   byte
	to  int
}

// globNFA is a glob compiled to an automaton: state 0 starts and
// accept marks the accepting states. A * is a self-loop, so there are
// no free moves: every step reads one byte.
type globNFA struct {
	edges  [][]edge
	accept []bool
}

// newState adds a state and returns its number.
func (n *globNFA) newState() int {
	n.edges = append(n.edges, nil)
	n.accept = append(n.accept, false)
	return len(n.edges) - 1
}

// compileGlob builds the automaton of a catalog path pattern as
// matchPath reads it: path.Match, "" for every path, /** for a path and
// everything below it and one trailing slash forgiven.
func compileGlob(p string) *globNFA {
	n := &globNFA{}
	cur := n.newState()
	if p == "" {
		n.edges[cur] = append(n.edges[cur], edge{cls: clsAll, to: cur})
		n.accept[cur] = true
		return n
	}
	base, below := strings.CutSuffix(p, "/**")
	for i := 0; i < len(base); i++ {
		switch c := base[i]; c {
		case '*':
			n.edges[cur] = append(n.edges[cur], edge{cls: clsSeg, to: cur})
		case '?':
			next := n.newState()
			n.edges[cur] = append(n.edges[cur], edge{cls: clsSeg, to: next})
			cur = next
		case '[':
			if j := strings.IndexByte(base[i+1:], ']'); j >= 0 {
				i += j + 1
			}
			next := n.newState()
			n.edges[cur] = append(n.edges[cur], edge{cls: clsSeg, to: next})
			cur = next
		default:
			if c == '\\' && i+1 < len(base) {
				i++
				c = base[i]
			}
			next := n.newState()
			n.edges[cur] = append(n.edges[cur], edge{cls: clsLit, c: c, to: next})
			cur = next
		}
	}
	n.accept[cur] = true
	slash := n.newState()
	n.edges[cur] = append(n.edges[cur], edge{cls: clsLit, c: '/', to: slash})
	n.accept[slash] = true
	if below {
		n.edges[slash] = append(n.edges[slash], edge{cls: clsAll, to: slash})
	}
	return n
}

// meet reports whether two character classes share a byte.
func meet(a, b edge) bool {
	switch {
	case a.cls == clsLit && b.cls == clsLit:
		return a.c == b.c
	case a.cls == clsLit && b.cls == clsSeg:
		return a.c != '/'
	case a.cls == clsSeg && b.cls == clsLit:
		return b.c != '/'
	}
	return true
}

// globsMeet reports whether some path fits both patterns.
func globsMeet(a, b string) bool {
	x, y := compileGlob(a), compileGlob(b)
	type pair struct{ i, j int }
	seen := map[pair]bool{}
	stack := []pair{{0, 0}}
	for len(stack) > 0 {
		p := stack[len(stack)-1]
		stack = stack[:len(stack)-1]
		if seen[p] {
			continue
		}
		seen[p] = true
		if x.accept[p.i] && y.accept[p.j] {
			return true
		}
		for _, ea := range x.edges[p.i] {
			for _, eb := range y.edges[p.j] {
				if meet(ea, eb) {
					stack = append(stack, pair{ea.to, eb.to})
				}
			}
		}
	}
	return false
}
