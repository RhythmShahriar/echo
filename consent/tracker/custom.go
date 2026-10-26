// Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
// SPDX-License-Identifier: MIT

package tracker

import (
	"errors"
	"fmt"
	"slices"
	"strings"

	"gopkg.in/yaml.v3"

	"github.com/RhythmShahriar/echo/consent"
)

// A site's own trackers come from .echo/config.yaml, the same shape as
// a catalog file plus the fixtures that prove it:
//
//	consent:
//	  trackers:
//	    - id: shop-pixel             # not a built-in id
//	      name: Shop pixel
//	      category: advertising
//	      hits:
//	        - host: px.shop.example
//	          path: /collect
//	      cookies: [_shpx]
//	      fixtures:
//	        match: ["https://px.shop.example/collect?e=view"]
//	        no_match: ["https://px.shop.example/pixel.js"]
//	        cookies: {match: [_shpx], no_match: [_shop]}    # optional
//
// vendor defaults to the name, consent to needed (no for necessary),
// fix to defaultCustomFix; docs is optional; sources is always config.
// An entry with hits, scripts or frames needs URL fixtures on both
// sides; one with only cookies or storage needs name fixtures on both
// sides. A match fixture must be classified as the entry (any kind), a
// no_match one not at all. Custom entries are tried before the
// built-in ones, so a site can claim a request the catalog would name
// otherwise.

// customPrefix starts every message about the config's own trackers.
const customPrefix = "consent: trackers: "

// Bounds on the config's own trackers: generous for a real site, small
// enough that a hostile config can't make loading slow.
const (
	maxCustomYAML     = 1 << 20
	maxCustomTrackers = 200
	maxCustomFixtures = 100
)

// defaultCustomFix is the fix line of a custom tracker that needs
// consent and names none.
const defaultCustomFix = "Load it only after the visitor consents"

// fixtureReporter is the optional interface the CLI type-asserts on the
// registered Classifier for `rhythm echo checks --test`.
var _ interface {
	FixtureReport([]byte) ([]string, bool)
} = (*Catalog)(nil)

// customSpec is one entry of consent: trackers: as written.
type customSpec struct {
	entrySpec `yaml:",inline"`
	Fixtures  *customFixtures `yaml:"fixtures"`
}

// customFixtures are a custom entry's fixtures: URLs, then optional
// cookie and storage names.
type customFixtures struct {
	Match   []reqFixture `yaml:"match"`
	NoMatch []reqFixture `yaml:"no_match"`
	Cookies nameFixtures `yaml:"cookies"`
	Storage nameFixtures `yaml:"storage"`
}

// custom is one entry of consent: trackers:, compiled.
type custom struct {
	label string   // the id, or "entry N" when there is no usable id
	e     *entry   // nil when err is set
	one   *Catalog // e alone, for the fixtures
	fix   customFixtures
	err   error // the entry doesn't load
}

// fixtureRun is one fixture of a custom entry, run.
type fixtureRun struct {
	id   string
	kind string // "" for a URL, "cookie" or "storage"
	text string // printable: the masked URL ("POST <url>" for another method) or the name
	want bool   // match (true) or no_match
	got  bool
}

// ok reports whether the fixture came out as declared.
func (f fixtureRun) ok() bool { return f.want == f.got }

// verdict says how a failing fixture failed.
func (f fixtureRun) verdict() string {
	if f.want {
		return "should match but doesn't"
	}
	return "should not match but does"
}

// err is the AddCustom error for a failing fixture.
func (f fixtureRun) err() error {
	kind := "fixture"
	if f.kind != "" {
		kind = f.kind + " fixture"
	}
	return fmt.Errorf("%s%s: %s %q %s", customPrefix, f.id, kind, f.text, f.verdict())
}

// line is the fixture's line in FixtureReport: status, id, side, text.
func (f fixtureRun) line(width int) string {
	status, side, text := "ok", "no_match", f.text
	if f.want {
		side = "match"
	}
	if f.kind != "" {
		text = f.kind + " " + text
	}
	if !f.ok() {
		status, text = "FAIL", text+"  "+f.verdict()
	}
	return fmt.Sprintf("%-4s %-*s  %-8s %s", status, width, f.id, side, text)
}

// AddCustom loads the config's own trackers (the YAML of consent:
// trackers:, a list) into the catalog, replacing any loaded before; an
// empty list removes them. Each must load and pass its fixtures, or the
// catalog stays as it was and the error says which and why, starting
// with "consent: trackers: ". Call it before the catalog classifies
// anything: it is not safe to run alongside matching.
func (c *Catalog) AddCustom(b []byte) error {
	n, err := c.withCustom(b)
	if err != nil {
		return err
	}
	*c = *n
	return nil
}

// FixtureReport runs the fixtures of the config's own trackers (the
// YAML AddCustom takes) and returns one line per fixture in the style
// of `rhythm echo checks --test`, with false when any fixture fails or
// any tracker doesn't load. It never changes the catalog.
func (c *Catalog) FixtureReport(b []byte) ([]string, bool) {
	cs, err := parseCustom(b, c.base)
	if err != nil {
		return failLines(err), false
	}
	width := 0
	for _, x := range cs {
		width = max(width, len(x.label))
	}
	var lines []string
	ok := true
	entries := slices.Clone(c.base)
	for _, x := range cs {
		if x.err != nil {
			msg := strings.TrimPrefix(x.err.Error(), customPrefix)
			if rest, ok := strings.CutPrefix(msg, x.label); ok && (strings.HasPrefix(rest, ":") || strings.HasPrefix(rest, " ")) {
				msg = strings.TrimLeft(strings.TrimPrefix(rest, ":"), " ")
			}
			lines = append(lines, fmt.Sprintf("FAIL %-*s  %s", width, x.label, msg))
			ok = false
			continue
		}
		for _, r := range x.runs() {
			lines = append(lines, r.line(width))
			ok = ok && r.ok()
		}
		entries = append(entries, x.e)
	}
	if ok && len(cs) > 0 {
		// The fixtures pass one entry at a time; the catalog as a whole
		// can still refuse them (a denied cookie, a script that looks
		// like a built-in hit).
		if _, err := c.combine(entries); err != nil {
			return append(lines, failLines(err)...), false
		}
	}
	return lines, ok
}

// failLines is an error as FixtureReport lines.
func failLines(err error) []string {
	var out []string
	for l := range strings.SplitSeq(err.Error(), "\n") {
		out = append(out, "FAIL "+l)
	}
	return out
}

// withCustom is the catalog with the config's own trackers in place of
// any it has; c is not changed.
func (c *Catalog) withCustom(b []byte) (*Catalog, error) {
	cs, err := parseCustom(b, c.base)
	if err != nil {
		return nil, err
	}
	entries := slices.Clone(c.base)
	for _, x := range cs {
		if x.err != nil {
			return nil, x.err
		}
		for _, r := range x.runs() {
			if !r.ok() {
				return nil, r.err()
			}
		}
		entries = append(entries, x.e)
	}
	return c.combine(entries)
}

// combine is a catalog of entries with c's deny list, built-in entries
// and info, checked as a whole.
func (c *Catalog) combine(entries []*entry) (*Catalog, error) {
	n, err := newCatalog(entries)
	if err != nil {
		return nil, err
	}
	n.base, n.deny, n.info = c.base, c.deny, c.info
	if err := n.checkDeny(); err != nil {
		return nil, err
	}
	return n, nil
}

// parseCustom reads consent: trackers: and compiles each entry. An
// error is returned for the list as a whole; an entry that doesn't load
// carries its own.
func parseCustom(b []byte, builtin []*entry) ([]*custom, error) {
	if len(b) > maxCustomYAML {
		return nil, fmt.Errorf("%sthe list is more than %d KiB", customPrefix, maxCustomYAML>>10)
	}
	var doc yaml.Node
	if err := yaml.Unmarshal(b, &doc); err != nil {
		return nil, errors.New(customPrefix + yamlText(err))
	}
	if doc.Kind == 0 || len(doc.Content) == 0 {
		return nil, nil
	}
	root := doc.Content[0]
	if root.Kind == yaml.ScalarNode && root.Tag == "!!null" {
		return nil, nil
	}
	if root.Kind != yaml.SequenceNode {
		return nil, errors.New(customPrefix + "want a list of trackers, each with id, name, category, what it matches and fixtures")
	}
	if len(root.Content) > maxCustomTrackers {
		return nil, fmt.Errorf("%s%d trackers is more than %d", customPrefix, len(root.Content), maxCustomTrackers)
	}
	taken := map[string]bool{}
	for _, e := range builtin {
		taken[e.info.ID] = true
	}
	seen := map[string]bool{}
	out := make([]*custom, 0, len(root.Content))
	for i, n := range root.Content {
		x := compileCustom(i, n)
		if x.err == nil {
			id := x.e.info.ID
			switch {
			case taken[id]:
				x.err = fmt.Errorf("%s%q is a built-in tracker; pick another id", customPrefix, id)
			case seen[id]:
				x.err = fmt.Errorf("%s%q is listed twice; give each tracker its own id", customPrefix, id)
			}
			seen[id] = true
		}
		out = append(out, x)
	}
	return out, nil
}

// compileCustom checks and compiles entry i of consent: trackers:.
func compileCustom(i int, n *yaml.Node) *custom {
	x := &custom{label: fmt.Sprintf("entry %d", i+1)}
	// The id names the entry in every message, so it is read first.
	if n.Kind == yaml.MappingNode {
		for j := 0; j+1 < len(n.Content); j += 2 {
			if k, v := n.Content[j], n.Content[j+1]; k.Value == "id" && v.Kind == yaml.ScalarNode && idRe.MatchString(v.Value) {
				x.label = v.Value
			}
		}
	}
	msg := func(format string, args ...any) error {
		// The text comes from the config: escape anything that could
		// rewrite the terminal before it is printed.
		return errors.New(consent.SafeText(customPrefix+x.label+": "+fmt.Sprintf(format, args...), 1000))
	}
	failed := func(err error) *custom { x.err, x.e = err, nil; return x }
	b, err := yaml.Marshal(n)
	if err != nil {
		return failed(msg("%s", yamlText(err)))
	}
	var s customSpec
	if _, err := decodeStrict(b, &s); err != nil {
		return failed(msg("%s", yamlText(err)))
	}
	if len(s.Sources) > 0 {
		return failed(msg("leave sources out: your own trackers are listed with the source config"))
	}
	s.Name = strings.TrimSpace(s.Name)
	if strings.TrimSpace(s.Vendor) == "" {
		s.Vendor = s.Name
	}
	if s.Consent == "" {
		s.Consent = string(consent.NeedNeeded)
		if s.Category == string(consent.CatNecessary) {
			s.Consent = string(consent.NeedNo)
		}
	}
	if strings.TrimSpace(s.Fix) == "" && s.Consent == string(consent.NeedNeeded) {
		s.Fix = defaultCustomFix
	}
	s.Sources = []string{"config"}
	fail := func(_ int, format string, args ...any) error { return msg(format, args...) }
	at := func(...any) int { return 0 }
	if err := checkSpec(&s.entrySpec, true, fail, at); err != nil {
		return failed(err)
	}
	// Names and fix lines reach tables and reports as written.
	s.Name, s.Vendor, s.Fix = consent.SafeText(s.Name, 100), consent.SafeText(s.Vendor, 100), consent.SafeText(s.Fix, 300)
	e, err := compile(&s.entrySpec, "", fail, at)
	if err != nil {
		return failed(err)
	}
	e.custom = true
	x.e = e
	if err := x.checkFixtures(&s); err != nil {
		return failed(err)
	}
	if x.one, err = newCatalog([]*entry{e}); err != nil {
		return failed(err)
	}
	return x
}

// checkFixtures enforces the fixtures a custom entry needs.
func (x *custom) checkFixtures(s *customSpec) error {
	id := x.e.info.ID
	f := s.Fixtures
	if f == nil {
		f = &customFixtures{}
	}
	x.fix = *f
	switch {
	case len(s.Hits)+len(s.Scripts)+len(s.Frames) > 0:
		if len(f.Match) == 0 || len(f.NoMatch) == 0 {
			return fmt.Errorf("%s%s needs fixtures: match and no_match, each with at least one URL", customPrefix, id)
		}
	case len(s.Cookies) > 0 && (len(f.Cookies.Match) == 0 || len(f.Cookies.NoMatch) == 0):
		return fmt.Errorf("%s%s needs fixtures: cookies: match and no_match, each with at least one name", customPrefix, id)
	case len(s.Cookies) == 0 && (len(f.Storage.Match) == 0 || len(f.Storage.NoMatch) == 0):
		return fmt.Errorf("%s%s needs fixtures: storage: match and no_match, each with at least one name", customPrefix, id)
	}
	if n := len(f.Match) + len(f.NoMatch) + len(f.Cookies.Match) + len(f.Cookies.NoMatch) +
		len(f.Storage.Match) + len(f.Storage.NoMatch); n > maxCustomFixtures {
		return fmt.Errorf("%s%s: %d fixtures is more than %d", customPrefix, id, n, maxCustomFixtures)
	}
	for _, r := range slices.Concat(f.Match, f.NoMatch) {
		if len(r.URL) > maxPageURL {
			// Masking the URL for the report costs time in proportion
			// to its length.
			return fmt.Errorf("%s%s: a fixture URL is longer than %d bytes", customPrefix, id, maxPageURL)
		}
	}
	return nil
}

// runs runs the entry's fixtures against the entry alone.
func (x *custom) runs() []fixtureRun {
	id := x.e.info.ID
	var out []fixtureRun
	for _, side := range []struct {
		want bool
		list []reqFixture
	}{{true, x.fix.Match}, {false, x.fix.NoMatch}} {
		for _, f := range side.list {
			got := x.one.Request(f.request(), f.body(), f.site()) != nil
			out = append(out, fixtureRun{id: id, text: fixtureURL(f), want: side.want, got: got})
		}
	}
	for _, g := range []struct {
		kind string
		fix  nameFixtures
		find func(nameSpec) *consent.Hit
	}{
		{"cookie", x.fix.Cookies, func(n nameSpec) *consent.Hit {
			return x.one.Cookie(&consent.Cookie{Name: n.Name, Domain: n.Domain})
		}},
		{"storage", x.fix.Storage, func(n nameSpec) *consent.Hit { return x.one.Storage(&consent.StorageKey{Key: n.Name}) }},
	} {
		for _, side := range []struct {
			want bool
			list []nameSpec
		}{{true, g.fix.Match}, {false, g.fix.NoMatch}} {
			for _, n := range side.list {
				out = append(out, fixtureRun{id: id, kind: g.kind, text: consent.SafeText(n.Name, 100), want: side.want, got: g.find(n) != nil})
			}
		}
	}
	return out
}

// fixtureURL is a request fixture as the report shows it: the URL with
// credentials and personal parameters masked (IDs kept, the config's
// author wrote them), led by the method when it isn't GET. The mask's
// bullets come back percent-encoded in a query; they are shown as
// bullets, so a masked value reads as masked.
func fixtureURL(f reqFixture) string {
	u := strings.ReplaceAll(consent.MaskURL(f.URL, true), "%E2%80%A2", "•")
	if f.Method != "GET" {
		u = f.Method + " " + u
	}
	return consent.SafeText(u, 200)
}

// yamlText is a YAML error without line numbers: the YAML of consent:
// trackers: is handed over on its own, so its lines aren't the
// config's.
func yamlText(err error) string {
	var te *yaml.TypeError
	msgs := []string{err.Error()}
	if errors.As(err, &te) {
		msgs = te.Errors
	}
	out := make([]string, 0, len(msgs))
	for _, m := range msgs {
		m = strings.TrimSpace(m)
		if g := yamlLine.FindStringSubmatch(m); g != nil {
			m = g[2]
		} else {
			m = strings.TrimPrefix(m, "yaml: ")
		}
		if k := unknownKey.FindStringSubmatch(m); k != nil {
			m = fmt.Sprintf("unknown key %q", k[1])
		}
		out = append(out, m)
	}
	return strings.Join(out, "; ")
}
