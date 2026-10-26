// Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
// SPDX-License-Identifier: MIT

package tracker

import (
	"fmt"
	"net/url"
	"strings"

	"gopkg.in/yaml.v3"

	"github.com/RhythmShahriar/echo/consent"
)

// Every entry proves its patterns with fixtures in <id>.fixtures.yaml:
//
//	hits:
//	  match:                       # each must be a beacon of this entry
//	    - https://www.google-analytics.com/g/collect?v=2&tid=G-TESTBED001
//	    - POST https://region1.google-analytics.com/g/collect?v=2&tid=G-TESTBED001
//	    - {method: POST, url: "https://...", body: "en=page_view\nen=scroll"}
//	  no_match:                    # none may be a beacon of this entry
//	    - https://shop.test/g/collect?q=shoes
//	scripts: {match: [...], no_match: [...]}
//	frames: {match: [...], no_match: [...]}
//	cookies: {match: [_ga, {name: IDE, domain: doubleclick.net}], no_match: [_gallery]}
//	storage: {match: [...], no_match: [...]}
//
// An entry with hits needs at least one hits match and one no_match;
// with cookies, one cookies match and one no_match; with scripts,
// frames or storage, one match of each. A request fixture may also set
// type (script, image, ...) and site (default https://shop.test/).

// defaultSite is the page fixtures are loaded on unless they say.
const defaultSite = "https://shop.test/"

// fixtureFile is a <id>.fixtures.yaml file.
type fixtureFile struct {
	Hits    reqFixtures  `yaml:"hits"`
	Scripts reqFixtures  `yaml:"scripts"`
	Frames  reqFixtures  `yaml:"frames"`
	Cookies nameFixtures `yaml:"cookies"`
	Storage nameFixtures `yaml:"storage"`
}

// reqFixtures are the request fixtures of one kind.
type reqFixtures struct {
	Match   []reqFixture `yaml:"match"`
	NoMatch []reqFixture `yaml:"no_match"`
}

// nameFixtures are the cookie or storage name fixtures.
type nameFixtures struct {
	Match   []nameSpec `yaml:"match"`
	NoMatch []nameSpec `yaml:"no_match"`
}

// reqFixture is one request: a URL ("METHOD URL" for another method
// than GET) or a mapping with method, url, body, type and site.
type reqFixture struct {
	Method, URL, Body, Type, Site string
	line                          int
}

// UnmarshalYAML reads a request fixture, refusing unknown keys.
func (f *reqFixture) UnmarshalYAML(v *yaml.Node) error {
	f.line = v.Line
	switch v.Kind {
	case yaml.ScalarNode:
		f.URL = strings.TrimSpace(v.Value)
		if m, rest, ok := strings.Cut(f.URL, " "); ok {
			f.Method, f.URL = m, strings.TrimSpace(rest)
		}
	case yaml.MappingNode:
		for i := 0; i+1 < len(v.Content); i += 2 {
			k, val := v.Content[i], v.Content[i+1]
			if val.Kind != yaml.ScalarNode {
				return lineErr(val.Line, "%s must be a string", k.Value)
			}
			switch k.Value {
			case "method":
				f.Method = val.Value
			case "url":
				f.URL = val.Value
			case "body":
				f.Body = val.Value
			case "type":
				f.Type = val.Value
			case "site":
				f.Site = val.Value
			default:
				return lineErr(k.Line, "unknown key %q", k.Value)
			}
		}
	default:
		return lineErr(v.Line, "want a URL or {method, url, body, type, site}")
	}
	f.Method = strings.ToUpper(f.Method)
	if f.Method == "" {
		f.Method = "GET"
	}
	u, err := url.Parse(f.URL)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
		return lineErr(v.Line, "%q is not an http or https URL", f.URL)
	}
	return nil
}

// request is the fixture as the recorder would hand it over.
func (f reqFixture) request() *consent.Request {
	return &consent.Request{Method: f.Method, URL: f.URL, Type: f.Type}
}

// site is the page the fixture is loaded on.
func (f reqFixture) site() *url.URL {
	s := f.Site
	if s == "" {
		s = defaultSite
	}
	u, _ := url.Parse(s)
	return u
}

// body is the fixture's body, nil when it has none.
func (f reqFixture) body() []byte {
	if f.Body == "" {
		return nil
	}
	return []byte(f.Body)
}

// kindSet pairs a hit kind with its fixtures and the YAML key for both.
type kindSet struct {
	key  string
	kind consent.HitKind
	fix  reqFixtures
	has  bool
}

// kinds lists the request fixtures of a file by kind.
func (ff *fixtureFile) kinds(s *entrySpec) []kindSet {
	return []kindSet{
		{"hits", consent.HitBeacon, ff.Hits, len(s.Hits) > 0},
		{"scripts", consent.HitScript, ff.Scripts, len(s.Scripts) > 0},
		{"frames", consent.HitFrame, ff.Frames, len(s.Frames) > 0},
	}
}

// checkFixtures reads an entry's fixtures, enforces how many it needs
// and runs them against the entry alone.
func checkFixtures(e *entry, s *entrySpec, file string, b []byte) error {
	var ff fixtureFile
	doc, err := decodeStrict(b, &ff)
	if err != nil {
		return located(file, err)
	}
	fail := func(line int, format string, args ...any) error {
		return fmt.Errorf("%s:%d: %s", file, line, fmt.Sprintf(format, args...))
	}
	id := s.ID
	for _, k := range ff.kinds(s) {
		if k.has && len(k.fix.Match) == 0 {
			return fail(lineAt(doc, k.key), "%s needs at least one %s.match fixture", id, k.key)
		}
		if k.kind == consent.HitBeacon && k.has && len(k.fix.NoMatch) == 0 {
			return fail(lineAt(doc, k.key), "%s needs at least one hits.no_match fixture", id)
		}
	}
	switch {
	case len(s.Cookies) > 0 && (len(ff.Cookies.Match) == 0 || len(ff.Cookies.NoMatch) == 0):
		return fail(lineAt(doc, "cookies"), "%s needs at least one cookies.match and one cookies.no_match fixture", id)
	case len(s.Storage) > 0 && len(ff.Storage.Match) == 0:
		return fail(lineAt(doc, "storage"), "%s needs at least one storage.match fixture", id)
	}
	one, err := newCatalog([]*entry{e})
	if err != nil {
		return err
	}
	for _, k := range ff.kinds(s) {
		for _, f := range k.fix.Match {
			h := one.Request(f.request(), f.body(), f.site())
			if h == nil || h.Kind != k.kind {
				return fail(f.line, "%s.match %q doesn't match %s", k.key, f.URL, id)
			}
		}
		for _, f := range k.fix.NoMatch {
			if h := one.Request(f.request(), f.body(), f.site()); h != nil && h.Kind == k.kind {
				return fail(f.line, "%s.no_match %q matches %s", k.key, f.URL, id)
			}
		}
	}
	for _, g := range []struct {
		key  string
		fix  nameFixtures
		find func(nameSpec) *consent.Hit
	}{
		{"cookies", ff.Cookies, func(n nameSpec) *consent.Hit { return one.Cookie(&consent.Cookie{Name: n.Name, Domain: n.Domain}) }},
		{"storage", ff.Storage, func(n nameSpec) *consent.Hit { return one.Storage(&consent.StorageKey{Key: n.Name}) }},
	} {
		for _, n := range g.fix.Match {
			if g.find(n) == nil {
				return fail(n.line, "%s.match %q doesn't match %s", g.key, n.Name, id)
			}
		}
		for _, n := range g.fix.NoMatch {
			if g.find(n) != nil {
				return fail(n.line, "%s.no_match %q matches %s", g.key, n.Name, id)
			}
		}
	}
	return nil
}
