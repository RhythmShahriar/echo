// Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
// SPDX-License-Identifier: MIT

package tracker

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"path"
	"regexp"
	"slices"
	"sort"
	"strings"

	"gopkg.in/yaml.v3"

	"github.com/RhythmShahriar/echo/consent"
)

// entrySpec is one catalog file as written.
type entrySpec struct {
	ID       string     `yaml:"id"`
	Name     string     `yaml:"name"`
	Vendor   string     `yaml:"vendor"`
	Category string     `yaml:"category"`
	Consent  string     `yaml:"consent"`
	AnyHost  bool       `yaml:"any_host"`
	Hits     []ruleSpec `yaml:"hits"`
	Scripts  []ruleSpec `yaml:"scripts"`
	Frames   []ruleSpec `yaml:"frames"`
	Cookies  []nameSpec `yaml:"cookies"`
	Storage  []nameSpec `yaml:"storage"`
	Params   paramSpec  `yaml:"params"`
	Fix      string     `yaml:"fix"`
	Docs     string     `yaml:"docs"`
	Sources  []string   `yaml:"sources"`
}

// ruleSpec is one hits, scripts or frames rule as written.
type ruleSpec struct {
	Host        string            `yaml:"host"`
	Path        string            `yaml:"path"`
	Hosts       []string          `yaml:"hosts"`
	Paths       []string          `yaml:"paths"`
	Methods     []string          `yaml:"methods"`
	ParamsAll   []string          `yaml:"params_all"`
	ParamsAny   []string          `yaml:"params_any"`
	ParamsMatch map[string]string `yaml:"params_match"`
	NotParams   map[string]string `yaml:"not_params"`
	BodyJSON    []string          `yaml:"body_json"`
}

// paramSpec says which parameters hold what.
type paramSpec struct {
	Event    []string `yaml:"event"`
	ID       []string `yaml:"id"`
	IDPath   string   `yaml:"id_path"`
	Personal []string `yaml:"personal"`
}

// nameSpec is a cookie or storage name pattern: a plain string, or a
// mapping with name and domain (a cookie only counts on that domain and
// its subdomains, for names as generic as IDE or MUID).
type nameSpec struct {
	Name   string
	Domain string
	line   int
}

// UnmarshalYAML reads a name pattern, refusing unknown keys.
func (n *nameSpec) UnmarshalYAML(v *yaml.Node) error {
	n.line = v.Line
	switch v.Kind {
	case yaml.ScalarNode:
		n.Name = v.Value
		return nil
	case yaml.MappingNode:
		for i := 0; i+1 < len(v.Content); i += 2 {
			k, val := v.Content[i], v.Content[i+1]
			if val.Kind != yaml.ScalarNode {
				return lineErr(val.Line, "%s must be a string", k.Value)
			}
			switch k.Value {
			case "name":
				n.Name = val.Value
			case "domain":
				n.Domain = val.Value
			default:
				return lineErr(k.Line, "unknown key %q", k.Value)
			}
		}
		return nil
	}
	return lineErr(v.Line, "want a name or {name, domain}")
}

// lineErr is an error yaml.v3 passes through with its line, so located
// can name it.
func lineErr(line int, format string, args ...any) error {
	return &yaml.TypeError{Errors: []string{fmt.Sprintf("line %d: %s", line, fmt.Sprintf(format, args...))}}
}

var (
	idRe        = regexp.MustCompile(`^[a-z0-9]+(-[a-z0-9]+)*$`)
	hostRe      = regexp.MustCompile(`^(\*\.)?[a-z0-9]([a-z0-9-]*[a-z0-9])?(\.[a-z0-9]([a-z0-9-]*[a-z0-9])?)*$`)
	paramNameRe = regexp.MustCompile(`^[A-Za-z0-9_.\[\]-]+$`)
	cookieRe    = regexp.MustCompile(`^[A-Za-z0-9_.\-*]+$`)
	yamlLine    = regexp.MustCompile(`^(?:yaml: )?line (\d+): (.*)$`)
	unknownKey  = regexp.MustCompile(`^field (\S+) not found in type \S+$`)
)

// The categories and consent needs a file may name.
var (
	categories = []consent.Category{consent.CatAnalytics, consent.CatAdvertising, consent.CatSessionReplay,
		consent.CatSocial, consent.CatEmbed, consent.CatFunctional, consent.CatNecessary}
	needs   = []consent.ConsentNeed{consent.NeedNeeded, consent.NeedExempt, consent.NeedNo}
	methods = []string{"GET", "POST", "HEAD", "PUT"}
)

// load reads every catalog file under dir, checks each against its
// fixtures and returns the catalog. Errors name the file and line; all
// of them are returned at once, joined.
func load(fsys fs.FS, dir string) (*Catalog, error) {
	files, err := fs.Glob(fsys, dir+"/*.yaml")
	if err != nil {
		return nil, err
	}
	sort.Strings(files)
	data := map[string][]byte{}
	for _, f := range files {
		b, err := fs.ReadFile(fsys, f)
		if err != nil {
			return nil, err
		}
		data[f] = b
	}
	var (
		errs    []error
		entries []*entry
	)
	for _, f := range files {
		if strings.HasSuffix(f, ".fixtures.yaml") {
			if _, ok := data[strings.TrimSuffix(f, ".fixtures.yaml")+".yaml"]; !ok {
				errs = append(errs, fmt.Errorf("%s: fixtures without a catalog file", f))
			}
			continue
		}
		fix := strings.TrimSuffix(f, ".yaml") + ".fixtures.yaml"
		fb, ok := data[fix]
		if !ok {
			errs = append(errs, fmt.Errorf("%s: no fixtures: add %s", f, path.Base(fix)))
			continue
		}
		e, err := loadEntry(f, data[f], fix, fb, true)
		if err != nil {
			errs = append(errs, err)
			continue
		}
		entries = append(entries, e)
	}
	if len(errs) > 0 {
		return nil, errors.Join(errs...)
	}
	c, err := newCatalog(entries)
	if err != nil {
		return nil, err
	}
	c.info = consent.DataInfo{Version: consent.DataVersion, SHA256: hashFiles(files, data), Count: len(entries)}
	return c, nil
}

// hashFiles is the catalog's SHA-256: each file's path, a NUL, its
// bytes and a NUL, in path order. The names are in it so a renamed
// file changes the hash too.
func hashFiles(files []string, data map[string][]byte) string {
	h := sha256.New()
	for _, f := range files {
		h.Write([]byte(f))
		h.Write([]byte{0})
		h.Write(data[f])
		h.Write([]byte{0})
	}
	return hex.EncodeToString(h.Sum(nil))
}

// loadEntry reads one catalog file and its fixtures, compiles the entry
// and runs the fixtures against it alone. byName makes the id match the
// file name (the built-in catalog; custom entries have no file).
func loadEntry(file string, b []byte, fixFile string, fb []byte, byName bool) (*entry, error) {
	var spec entrySpec
	doc, err := decodeStrict(b, &spec)
	if err != nil {
		return nil, located(file, err)
	}
	fail := func(line int, format string, args ...any) error {
		return fmt.Errorf("%s:%d: %s", file, line, fmt.Sprintf(format, args...))
	}
	at := func(p ...any) int { return lineAt(doc, p...) }
	if err := checkSpec(&spec, fail, at); err != nil {
		return nil, err
	}
	if byName {
		if base := strings.TrimSuffix(path.Base(file), ".yaml"); spec.ID != base {
			return nil, fail(at("id"), "id %q must match the file name (%s.yaml)", spec.ID, base)
		}
	}
	e, err := compile(&spec, file, fail, at)
	if err != nil {
		return nil, err
	}
	if err := checkFixtures(e, &spec, fixFile, fb); err != nil {
		return nil, err
	}
	return e, nil
}

// checkSpec checks the fields of an entry that don't need compiling.
func checkSpec(s *entrySpec, fail func(int, string, ...any) error, at func(...any) int) error {
	switch {
	case !idRe.MatchString(s.ID):
		return fail(at("id"), "id %q must be lowercase letters, digits and dashes", s.ID)
	case strings.TrimSpace(s.Name) == "":
		return fail(at("name"), "name is required")
	case strings.TrimSpace(s.Vendor) == "":
		return fail(at("vendor"), "vendor is required")
	case !slices.Contains(categories, consent.Category(s.Category)):
		return fail(at("category"), "category %q: want analytics, advertising, session-replay, social, embed, functional or necessary", s.Category)
	case !slices.Contains(needs, consent.ConsentNeed(s.Consent)):
		return fail(at("consent"), "consent %q: want needed, exempt or no", s.Consent)
	case s.Category == string(consent.CatNecessary) && s.Consent != string(consent.NeedNo):
		return fail(at("consent"), "a necessary entry has consent: no")
	case len(s.Hits)+len(s.Scripts)+len(s.Frames)+len(s.Cookies)+len(s.Storage) == 0:
		return fail(at(), "%s matches nothing: add hits, scripts, frames, cookies or storage", s.ID)
	case s.Consent == string(consent.NeedExempt) && len(s.Cookies) > 0:
		// Exempt means cookieless analytics: an entry that sets cookies
		// can't be allowed before consent on that ground.
		return fail(at("cookies"), "an exempt entry sets no cookies")
	case s.Consent == string(consent.NeedNeeded) && strings.TrimSpace(s.Fix) == "":
		return fail(at("fix"), "fix is required when consent is needed")
	case strings.ContainsAny(s.Fix, "\r\n"):
		return fail(at("fix"), "fix is one line")
	case !strings.HasPrefix(s.Docs, "https://"):
		return fail(at("docs"), "docs must be an https:// URL")
	case len(s.Sources) == 0:
		return fail(at("sources"), "sources lists where the patterns come from")
	}
	for i, src := range s.Sources {
		if !strings.HasPrefix(src, "https://") && src != "config" {
			return fail(at("sources", i), "source %q must be an https:// URL", src)
		}
	}
	return nil
}

// compile turns a checked spec into an entry.
func compile(s *entrySpec, file string, fail func(int, string, ...any) error, at func(...any) int) (*entry, error) {
	e := &entry{
		file: file,
		info: consent.TrackerInfo{ID: s.ID, Name: s.Name, Vendor: s.Vendor, Category: consent.Category(s.Category),
			Need: consent.ConsentNeed(s.Consent), Fix: strings.TrimSpace(s.Fix), Docs: s.Docs, Sources: slices.Clone(s.Sources)},
	}
	for _, g := range []struct {
		key   string
		kind  consent.HitKind
		rules []ruleSpec
	}{{"scripts", consent.HitScript, s.Scripts}, {"frames", consent.HitFrame, s.Frames}, {"hits", consent.HitBeacon, s.Hits}} {
		for i := range g.rules {
			r, err := compileRule(&g.rules[i], g.kind, s.AnyHost, func(key string, format string, args ...any) error {
				if key == "" {
					return fail(at(g.key, i), format, args...)
				}
				return fail(at(g.key, i, key), format, args...)
			})
			if err != nil {
				return nil, err
			}
			r.e = e
			e.rules = append(e.rules, r)
		}
	}
	for key, list := range map[string][]string{"event": s.Params.Event, "id": s.Params.ID, "personal": s.Params.Personal} {
		for _, n := range list {
			if !paramNameRe.MatchString(n) {
				return nil, fail(at("params", key), "params: %s: %q is not a parameter name", key, n)
			}
		}
	}
	e.events, e.ids, e.personal = slices.Clone(s.Params.Event), slices.Clone(s.Params.ID), slices.Clone(s.Params.Personal)
	if s.Params.IDPath != "" {
		re, err := regexp.Compile(s.Params.IDPath)
		if err != nil {
			return nil, fail(at("params", "id_path"), "params: id_path: %v", err)
		}
		if re.NumSubexp() != 1 {
			return nil, fail(at("params", "id_path"), "params: id_path needs exactly one capture group")
		}
		e.idPath = re
	}
	for key, list := range map[string][]nameSpec{"cookies": s.Cookies, "storage": s.Storage} {
		for _, n := range list {
			if err := checkName(n, key == "cookies"); err != nil {
				return nil, fmt.Errorf("%s:%d: %s: %w", file, n.line, key, err)
			}
		}
	}
	e.cookies, e.storage = slices.Clone(s.Cookies), slices.Clone(s.Storage)
	e.info.Matches = summary(e)
	return e, nil
}

// checkName checks one cookie or storage name pattern.
func checkName(n nameSpec, cookie bool) error {
	switch {
	case n.Name == "":
		return errors.New("a name is required")
	case !cookieRe.MatchString(n.Name):
		return fmt.Errorf("%q: letters, digits, _ . - and one * only", n.Name)
	case strings.Count(n.Name, "*") > 1:
		return fmt.Errorf("%q: at most one *", n.Name)
	case n.Name == "*":
		return errors.New(`"*" would match every name`)
	case n.Domain != "" && !cookie:
		return fmt.Errorf("%q: domain applies to cookies only", n.Name)
	case n.Domain != "" && (strings.HasPrefix(n.Domain, "*") || !hostRe.MatchString(n.Domain)):
		return fmt.Errorf("%q: domain %q is not a host name", n.Name, n.Domain)
	}
	return nil
}

// compileRule checks and compiles one hits, scripts or frames rule.
// fail names a key of the rule ("" for the rule itself).
func compileRule(s *ruleSpec, kind consent.HitKind, anyHost bool, fail func(key, format string, args ...any) error) (*rule, error) {
	hosts, paths := s.Hosts, s.Paths
	if s.Host != "" {
		if len(hosts) > 0 {
			return nil, fail("host", "use host or hosts, not both")
		}
		hosts = []string{s.Host}
	}
	if s.Path != "" {
		if len(paths) > 0 {
			return nil, fail("path", "use path or paths, not both")
		}
		paths = []string{s.Path}
	}
	r := &rule{kind: kind}
	for _, h := range hosts {
		switch {
		case !hostRe.MatchString(h):
			return nil, fail("hosts", "host %q: want a lowercase host name or *.suffix", h)
		case strings.HasPrefix(h, "*."):
			r.suffix = append(r.suffix, h[1:])
		default:
			r.exact = append(r.exact, h)
		}
	}
	for _, p := range paths {
		base := strings.TrimSuffix(p, "/**")
		if _, err := path.Match(base, "/"); err != nil || !strings.HasPrefix(p, "/") {
			return nil, fail("paths", "path %q must start with / (and * stands for part of one segment)", p)
		}
		if base != p && strings.ContainsAny(base, "*?[") {
			return nil, fail("paths", "path %q: /** follows a plain path", p)
		}
		if strings.Contains(base, "**") {
			return nil, fail("paths", "path %q: ** only at the end, as /**", p)
		}
	}
	r.paths = slices.Clone(paths)
	for _, m := range s.Methods {
		m = strings.ToUpper(m)
		if !slices.Contains(methods, m) {
			return nil, fail("methods", "method %q: want GET, POST, HEAD or PUT", m)
		}
		r.methods = append(r.methods, m)
	}
	for _, list := range [][]string{s.ParamsAll, s.ParamsAny, s.BodyJSON} {
		for _, n := range list {
			if !paramNameRe.MatchString(n) {
				return nil, fail("", "%q is not a parameter name", n)
			}
		}
	}
	r.all, r.anyOf, r.bodyJSON = slices.Clone(s.ParamsAll), slices.Clone(s.ParamsAny), slices.Clone(s.BodyJSON)
	for _, k := range sortedKeys(s.ParamsMatch) {
		re, err := regexp.Compile(s.ParamsMatch[k])
		if err != nil {
			return nil, fail("params_match", "params_match: %s: %v", k, err)
		}
		r.match = append(r.match, paramRe{k, re})
	}
	for _, k := range sortedKeys(s.NotParams) {
		r.not = append(r.not, paramVal{k, s.NotParams[k]})
	}
	proof := len(r.all) > 0 || len(r.match) > 0 || len(r.bodyJSON) > 0
	if len(hosts) == 0 {
		switch {
		case !anyHost:
			return nil, fail("", "a rule without hosts needs any_host: true on the entry")
		case len(paths) == 0:
			return nil, fail("", "a rule on any host needs paths")
		case kind == consent.HitBeacon && !proof:
			// Any host by path alone would flag a shop's own /collect
			// endpoint; the parameters are what prove it's the tracker.
			return nil, fail("", "a hit on any host needs params_all, params_match or body_json to prove it")
		}
		r.anyHost = true
	}
	return r, nil
}

// sortedKeys returns m's keys in order, so rules check parameters in
// the same order on every run.
func sortedKeys(m map[string]string) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

// newCatalog indexes compiled entries, refusing two entries that claim
// one id or one cookie or storage pattern.
func newCatalog(entries []*entry) (*Catalog, error) {
	slices.SortFunc(entries, func(a, b *entry) int { return strings.Compare(a.info.ID, b.info.ID) })
	c := &Catalog{entries: entries, cookies: newNames(), storage: newNames()}
	for i := 1; i < len(entries); i++ {
		if prev, e := entries[i-1], entries[i]; prev.info.ID == e.info.ID {
			return nil, fmt.Errorf("%s: id %q is already used by %s", e.file, e.info.ID, prev.file)
		}
	}
	// Scripts before frames before hits within a tier: a script URL is
	// never held, so it must win over any hit rule that also fits.
	for _, kind := range []consent.HitKind{consent.HitScript, consent.HitFrame, consent.HitBeacon} {
		for _, e := range entries {
			for _, r := range e.rules {
				if r.kind != kind {
					continue
				}
				if len(r.exact) > 0 {
					c.tiers[tierExact] = append(c.tiers[tierExact], r)
				}
				if len(r.suffix) > 0 {
					c.tiers[tierSuffix] = append(c.tiers[tierSuffix], r)
				}
				if r.anyHost {
					c.tiers[tierAny] = append(c.tiers[tierAny], r)
				}
			}
		}
	}
	for _, e := range entries {
		for _, n := range e.cookies {
			if err := c.cookies.add(e, n); err != nil {
				return nil, fmt.Errorf("%s:%d: cookies: %w", e.file, n.line, err)
			}
		}
		for _, n := range e.storage {
			if err := c.storage.add(e, n); err != nil {
				return nil, fmt.Errorf("%s:%d: storage: %w", e.file, n.line, err)
			}
		}
	}
	c.cookies.sort()
	c.storage.sort()
	return c, nil
}

// decodeStrict decodes one YAML document into v, refusing unknown keys,
// and returns the node tree for line numbers.
func decodeStrict(b []byte, v any) (*yaml.Node, error) {
	var doc yaml.Node
	if err := yaml.Unmarshal(b, &doc); err != nil {
		return nil, err
	}
	if doc.Kind == 0 || len(doc.Content) == 0 {
		return nil, errors.New("line 1: the file is empty")
	}
	if doc.Content[0].Kind != yaml.MappingNode {
		return nil, fmt.Errorf("line %d: want a mapping of keys", doc.Content[0].Line)
	}
	dec := yaml.NewDecoder(bytes.NewReader(b))
	dec.KnownFields(true)
	if err := dec.Decode(v); err != nil {
		return nil, err
	}
	var extra yaml.Node
	if err := dec.Decode(&extra); !errors.Is(err, io.EOF) {
		return nil, fmt.Errorf("line %d: one YAML document per file", max(extra.Line, 1))
	}
	return &doc, nil
}

// located turns a YAML error into "file:line: message" lines; an error
// without a line gets line 1, so every message points somewhere.
func located(file string, err error) error {
	var te *yaml.TypeError
	msgs := []string{err.Error()}
	if errors.As(err, &te) {
		msgs = te.Errors
	}
	out := make([]string, 0, len(msgs))
	for _, m := range msgs {
		m = strings.TrimSpace(m)
		if g := yamlLine.FindStringSubmatch(m); g != nil {
			msg := g[2]
			if k := unknownKey.FindStringSubmatch(msg); k != nil {
				msg = fmt.Sprintf("unknown key %q", k[1])
			}
			out = append(out, file+":"+g[1]+": "+msg)
		} else {
			out = append(out, file+":1: "+strings.TrimPrefix(m, "yaml: "))
		}
	}
	return errors.New(strings.Join(out, "\n"))
}

// lineAt returns the line of the node at p (map keys and sequence
// indexes) in doc, or of the deepest part of p that exists.
func lineAt(doc *yaml.Node, p ...any) int {
	n := doc
	if n != nil && n.Kind == yaml.DocumentNode && len(n.Content) > 0 {
		n = n.Content[0]
	}
	if n == nil {
		return 1
	}
	line := max(n.Line, 1)
	for _, step := range p {
		switch k := step.(type) {
		case string:
			if n.Kind != yaml.MappingNode {
				return line
			}
			found := false
			for i := 0; i+1 < len(n.Content); i += 2 {
				if n.Content[i].Value == k {
					line, n, found = n.Content[i].Line, n.Content[i+1], true
					break
				}
			}
			if !found {
				return line
			}
		case int:
			if n.Kind != yaml.SequenceNode || k >= len(n.Content) {
				return line
			}
			n = n.Content[k]
			line = n.Line
		}
	}
	return line
}
