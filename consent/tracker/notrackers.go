// Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
// SPDX-License-Identifier: MIT

package tracker

import (
	"fmt"
	"path"
	"strings"

	"github.com/RhythmShahriar/echo/consent"
)

// notTrackersFile is the deny list in the catalog folder: cookie and
// storage names that are never trackers, whatever pattern an entry
// has. Consent managers, sessions, CSRF tokens and CDN bot checks are
// set on every visit before any choice, so one false claim among them
// would fail every site that uses them:
//
//	cookies: [CookieConsent, "cmplz_*", PHPSESSID]
//	storage: [didomi_token, "uc_*"]
//
// Its fixtures (_not_trackers.fixtures.yaml) run against the whole
// catalog: cookies.no_match and storage.no_match must never come back
// as trackers and cookies.match must still be one (the list doesn't
// reach too far).
const notTrackersFile = "_not_trackers"

// notSpec is _not_trackers.yaml as written.
type notSpec struct {
	Cookies []nameSpec `yaml:"cookies"`
	Storage []nameSpec `yaml:"storage"`
}

// notFixtures is _not_trackers.fixtures.yaml.
type notFixtures struct {
	Cookies nameFixtures `yaml:"cookies"`
	Storage nameFixtures `yaml:"storage"`
}

// denyList is the compiled deny list.
type denyList struct {
	file     string
	cookies  names
	storage  names
	fixtures notFixtures
	fixFile  string
}

// denyEntry stands for the deny list where names wants an entry.
var denyEntry = &entry{info: consent.TrackerInfo{ID: notTrackersFile}}

// loadDeny reads the deny list and its fixtures. The fixtures run later,
// once the catalog is built (see checkDeny).
func loadDeny(file string, b []byte, fixFile string, fb []byte) (*denyList, error) {
	var s notSpec
	doc, err := decodeStrict(b, &s)
	if err != nil {
		return nil, located(file, err)
	}
	d := &denyList{file: file, cookies: newNames(), storage: newNames(), fixFile: fixFile}
	for _, g := range []struct {
		key  string
		list []nameSpec
		to   *names
	}{{"cookies", s.Cookies, &d.cookies}, {"storage", s.Storage, &d.storage}} {
		for _, n := range g.list {
			if n.Domain != "" {
				return nil, fmt.Errorf("%s:%d: %s: %q: the deny list holds names only, never a domain", file, n.line, g.key, n.Name)
			}
			if err := checkName(n, false); err != nil {
				return nil, fmt.Errorf("%s:%d: %s: %w", file, n.line, g.key, err)
			}
			if err := g.to.add(denyEntry, n); err != nil {
				return nil, fmt.Errorf("%s:%d: %s: %q is listed twice", file, n.line, g.key, n.Name)
			}
		}
	}
	if len(s.Cookies) == 0 {
		return nil, fmt.Errorf("%s:%d: the deny list needs cookies", file, lineAt(doc))
	}
	fdoc, err := decodeStrict(fb, &d.fixtures)
	if err != nil {
		return nil, located(fixFile, err)
	}
	f := d.fixtures
	switch {
	case len(f.Cookies.Match) == 0 || len(f.Cookies.NoMatch) == 0:
		return nil, fmt.Errorf("%s:%d: %s needs at least one cookies.match and one cookies.no_match fixture", fixFile, lineAt(fdoc, "cookies"), notTrackersFile)
	case len(s.Storage) > 0 && len(f.Storage.NoMatch) == 0:
		return nil, fmt.Errorf("%s:%d: %s needs at least one storage.no_match fixture", fixFile, lineAt(fdoc, "storage"), notTrackersFile)
	}
	return d, nil
}

// denied reports whether a name pattern of an entry is all but taken
// by the deny list: the shortest name it fits and one a little longer
// are both denied. Such a pattern could never match, so it is a mistake
// in the entry or in the list.
func (n *names) denied(p nameSpec) bool {
	before, after, glob := strings.Cut(p.Name, "*")
	if !glob {
		return n.find(p.Name, "") != nil
	}
	return n.find(before+after, "") != nil && n.find(before+"x0"+after, "") != nil
}

// checkDeny refuses entries that claim a denied name, then runs the
// deny list's fixtures against the whole catalog.
func (c *Catalog) checkDeny() error {
	d := c.deny
	if d == nil {
		return nil
	}
	for _, e := range c.entries {
		for _, g := range []struct {
			key  string
			list []nameSpec
			deny *names
		}{{"cookies", e.cookies, &d.cookies}, {"storage", e.storage, &d.storage}} {
			for _, n := range g.list {
				if g.deny.denied(n) {
					return fmt.Errorf("%s:%d: %s: %q is listed in %s as never a tracker", e.file, n.line, g.key, n.Name, path.Base(d.file))
				}
			}
		}
	}
	fail := func(n nameSpec, format string, args ...any) error {
		return fmt.Errorf("%s:%d: %s", d.fixFile, n.line, fmt.Sprintf(format, args...))
	}
	for _, n := range d.fixtures.Cookies.Match {
		if c.Cookie(&consent.Cookie{Name: n.Name, Domain: n.Domain}) == nil {
			return fail(n, "cookies.match %q is no tracker: the deny list reaches too far", n.Name)
		}
	}
	for _, n := range d.fixtures.Cookies.NoMatch {
		if h := c.Cookie(&consent.Cookie{Name: n.Name, Domain: n.Domain}); h != nil {
			return fail(n, "cookies.no_match %q is classified as %s", n.Name, h.Tracker)
		}
	}
	for _, n := range d.fixtures.Storage.Match {
		if c.Storage(&consent.StorageKey{Key: n.Name}) == nil {
			return fail(n, "storage.match %q is no tracker: the deny list reaches too far", n.Name)
		}
	}
	for _, n := range d.fixtures.Storage.NoMatch {
		if h := c.Storage(&consent.StorageKey{Key: n.Name}); h != nil {
			return fail(n, "storage.no_match %q is classified as %s", n.Name, h.Tracker)
		}
	}
	return nil
}
