// Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
// SPDX-License-Identifier: MIT

package tracker

import (
	"fmt"
	"slices"
	"strings"

	"github.com/RhythmShahriar/echo/consent"
)

// namePat is one compiled cookie or storage name pattern.
type namePat struct {
	e              *entry
	pattern        string
	prefix, suffix string // around the "*" of a pattern that has one
	glob           bool
	domain         string // cookies only: the domain it must be set on
}

// names indexes name patterns: exact names in a map, patterns with a
// "*" by how much literal text they fix, longest first, so
// _hjSessionUser_123 goes to the most specific pattern.
type names struct {
	exact map[string][]*namePat
	globs []*namePat
}

func newNames() names { return names{exact: map[string][]*namePat{}} }

// add indexes one pattern, refusing a pattern and domain another entry
// already claims.
func (n *names) add(e *entry, s nameSpec) error {
	p := &namePat{e: e, pattern: s.Name, domain: strings.ToLower(s.Domain)}
	clash := func(o *namePat) bool { return o.pattern == p.pattern && o.domain == p.domain }
	if before, after, ok := strings.Cut(s.Name, "*"); ok {
		p.glob, p.prefix, p.suffix = true, before, after
		if i := slices.IndexFunc(n.globs, clash); i >= 0 {
			return fmt.Errorf("%q is already claimed by %s", s.Name, n.globs[i].e.info.ID)
		}
		n.globs = append(n.globs, p)
		return nil
	}
	if i := slices.IndexFunc(n.exact[s.Name], clash); i >= 0 {
		return fmt.Errorf("%q is already claimed by %s", s.Name, n.exact[s.Name][i].e.info.ID)
	}
	n.exact[s.Name] = append(n.exact[s.Name], p)
	return nil
}

// sort orders the patterns once all are added: domain-bound patterns
// before free ones, then the longest literal text first, then by id so
// the order never depends on load order.
func (n *names) sort() {
	cmp := func(a, b *namePat) int {
		if (a.domain != "") != (b.domain != "") {
			if a.domain != "" {
				return -1
			}
			return 1
		}
		if la, lb := len(a.prefix)+len(a.suffix), len(b.prefix)+len(b.suffix); la != lb {
			return lb - la
		}
		if c := strings.Compare(a.e.info.ID, b.e.info.ID); c != 0 {
			return c
		}
		return strings.Compare(a.pattern, b.pattern)
	}
	slices.SortStableFunc(n.globs, cmp)
	for k := range n.exact {
		slices.SortStableFunc(n.exact[k], cmp)
	}
}

// find returns the entry a name set on domain belongs to: exact names
// first, then the most specific pattern. domain may be "" (storage).
func (n *names) find(name, domain string) *entry {
	domain = strings.TrimPrefix(strings.ToLower(domain), ".")
	for _, p := range n.exact[name] {
		if p.onDomain(domain) {
			return p.e
		}
	}
	for _, p := range n.globs {
		if len(name) >= len(p.prefix)+len(p.suffix) && strings.HasPrefix(name, p.prefix) &&
			strings.HasSuffix(name, p.suffix) && p.onDomain(domain) {
			return p.e
		}
	}
	return nil
}

// onDomain reports whether a cookie on domain fits the pattern's domain.
func (p *namePat) onDomain(domain string) bool {
	return p.domain == "" || domain == p.domain || strings.HasSuffix(domain, "."+p.domain)
}

// Cookie classifies a cookie by its name (and, for names as generic as
// IDE, its domain). The cookie is not changed; nil means it isn't a
// tracker cookie.
func (c *Catalog) Cookie(k *consent.Cookie) *consent.Hit {
	if k == nil {
		return nil
	}
	return nameHit(c.cookies.find(k.Name, k.Domain))
}

// Storage classifies a localStorage, sessionStorage or IndexedDB name
// as Cookie does a cookie.
func (c *Catalog) Storage(k *consent.StorageKey) *consent.Hit {
	if k == nil {
		return nil
	}
	return nameHit(c.storage.find(k.Key, ""))
}

// nameHit is the Hit for a cookie or storage key of e.
func nameHit(e *entry) *consent.Hit {
	if e == nil {
		return nil
	}
	return &consent.Hit{Tracker: e.info.ID, Name: e.info.Name, Category: e.info.Category, Need: e.info.Need}
}
