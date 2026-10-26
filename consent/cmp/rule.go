// Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
// SPDX-License-Identifier: MIT

package cmp

import (
	"bytes"
	"errors"
	"fmt"
	"net/url"
	"regexp"
	"slices"
	"strconv"
	"strings"

	"gopkg.in/yaml.v3"

	"github.com/RhythmShahriar/echo/consent"
)

// ruleFile is one rule file as written in rules/<id>.yaml. Every key is
// listed so an unknown one is a mistake the loader names, never a field
// silently ignored.
type ruleFile struct {
	ID       string `yaml:"id"`
	Name     string `yaml:"name"`
	Priority int    `yaml:"priority"`
	Detect   struct {
		Selectors []string `yaml:"selectors"`
		Globals   []string `yaml:"globals"`
		Scripts   []string `yaml:"scripts"`
	} `yaml:"detect"`
	Banner struct {
		Selector oneOrMany `yaml:"selector"`
	} `yaml:"banner"`
	Accept []string `yaml:"accept"`
	Reject struct {
		FirstLayer  []string `yaml:"first_layer"`
		SecondLayer *struct {
			Open       []string  `yaml:"open"`
			TogglesOff oneOrMany `yaml:"toggles_off"`
			PressOff   oneOrMany `yaml:"press_off"`
			Save       []string  `yaml:"save"`
		} `yaml:"second_layer"`
	} `yaml:"reject"`
	Reopen struct {
		Selectors []string `yaml:"selectors"`
		API       string   `yaml:"api"`
	} `yaml:"reopen"`
	API struct {
		Accept string `yaml:"accept"`
		Reject string `yaml:"reject"`
		Reopen string `yaml:"reopen"`
		State  string `yaml:"state"`
	} `yaml:"api"`
	Verify struct {
		Parser   string          `yaml:"parser"`
		Cookies  []string        `yaml:"cookies"`
		Storage  []string        `yaml:"storage"`
		StateJS  string          `yaml:"state_js"`
		Accepted map[string]bool `yaml:"accepted"`
		Rejected map[string]bool `yaml:"rejected"`
		TCF      bool            `yaml:"tcf"`
	} `yaml:"verify"`
	GeoOverride *struct {
		Param     string `yaml:"param"`
		ValueFrom string `yaml:"value_from"`
	} `yaml:"geo_override"`
	Sources  []string `yaml:"sources"`
	Fixtures []string `yaml:"fixtures"`
}

// oneOrMany is a YAML value written as one string or a list of them:
// a banner or a set of toggles some CMPs draw in two ways (Usercentrics
// v2 and v3).
type oneOrMany []string

// UnmarshalYAML reads a string as a list of one.
func (o *oneOrMany) UnmarshalYAML(n *yaml.Node) error {
	switch {
	case n.Kind == yaml.ScalarNode && n.Tag == "!!null":
		*o = nil
	case n.Kind == yaml.ScalarNode:
		*o = oneOrMany{n.Value}
	default:
		var l []string
		if err := n.Decode(&l); err != nil {
			return err
		}
		*o = l
	}
	return nil
}

// chain is one Accept or Reject path: the steps clicked in order, with
// the text the rule wrote it as (what Choice.Selector shows).
type chain struct {
	Raw   string
	Steps []consent.Locator
}

// secondLayer is a Reject that lives in the settings: open them, switch
// every toggle off, save.
type secondLayer struct {
	Open    []chain
	Toggles []consent.Locator // switched off when on
	Press   []consent.Locator // buttons that switch a purpose off, pressed when not yet
	Save    []chain
}

// apiCalls are the fixed calls a rule may make in the page's main world.
type apiCalls struct {
	Accept, Reject, Reopen, State string
}

// geoOverride is a vendor's test parameter that forces a country's
// banner, added to the start URL when --geo is set.
type geoOverride struct {
	Param, ValueFrom string
}

// rule is a parsed and checked CMP rule.
type rule struct {
	ID, Name   string
	Priority   int
	File       string
	Selectors  []consent.Locator // detect selectors, one step each
	SelRaw     []string          // as written, for DetectedBy
	Globals    []string
	Scripts    []string
	Banners    []consent.Locator // any one visible is the banner shown
	BannerRaw  string            // the first, as written
	Accept     []chain
	FirstLayer []chain
	Second     *secondLayer
	ReopenSel  []chain
	ReopenAPI  string // the api entry Reopen calls: "reopen" or ""
	API        apiCalls
	Verify     consent.VerifySpec
	Accepted   map[string]bool
	Rejected   map[string]bool
	TCF        bool // the TC string (euconsent-v2) verifies when the CMP's own state is unread
	Geo        *geoOverride
	Sources    []string
	Fixtures   []string
}

// genericID is the rule that recognizes a banner by its words; it is
// always tried last and only when --cmp is auto.
const genericID = "generic"

// parsers are the verify parsers the state reader knows (consent/signal).
var parsers = []string{"cookiebot", "onetrust", "cookieyes", "complianz", "usercentrics", "borlabs", "didomi", "iubenda", "tcf", "banner"}

// deniedJS are calls a rule's JavaScript may never make: it runs in the
// page with the page's privileges, so it must only read state or call
// the CMP's own documented function. Matched with spaces removed.
var deniedJS = []string{"eval(", "Function(", "fetch(", "XMLHttpRequest", "document.cookie=", "localStorage.setItem"}

var (
	ruleID     = regexp.MustCompile(`^[a-z][a-z0-9-]{0,31}$`)
	identifier = regexp.MustCompile(`^[A-Za-z_$][A-Za-z0-9_$]{0,63}$`)
	cookieName = regexp.MustCompile(`^[A-Za-z0-9_.\-]{1,64}\*?$`)
	paramName  = regexp.MustCompile(`^[A-Za-z0-9_\-]{1,32}$`)
	yamlLine   = regexp.MustCompile(`^line (\d+): (.*)$`)
	notFound   = regexp.MustCompile(`^field (\S+) not found in type .*$`)
)

// maxRuleBytes bounds a rule file: a rule is a page of selectors, never
// a data set.
const maxRuleBytes = 64 << 10

// lineError is a rule error at a line of its file.
func lineError(file string, line int, format string, a ...any) error {
	if line < 1 {
		line = 1
	}
	return fmt.Errorf("%s: line %d: %s", file, line, fmt.Sprintf(format, a...))
}

// parseRule reads one rule file. Every error names the file and a line:
// an unknown key, a selector the --accept-selector syntax refuses, a
// call the JavaScript deny list catches or a missing field.
func parseRule(file string, data []byte) (*rule, error) {
	if len(data) > maxRuleBytes {
		return nil, lineError(file, 1, "rule is %d bytes; keep it under %d", len(data), maxRuleBytes)
	}
	var doc yaml.Node
	if err := yaml.Unmarshal(data, &doc); err != nil {
		return nil, yamlError(file, err)
	}
	if len(doc.Content) == 0 || doc.Content[0].Kind != yaml.MappingNode {
		return nil, lineError(file, 1, "want a rule: a mapping with id, name, detect, banner, accept and reject")
	}
	var f ruleFile
	dec := yaml.NewDecoder(bytes.NewReader(data))
	dec.KnownFields(true)
	if err := dec.Decode(&f); err != nil {
		return nil, yamlError(file, err)
	}
	c := &checker{file: file, root: doc.Content[0]}
	r := c.rule(&f)
	if c.err != nil {
		return nil, c.err
	}
	return r, nil
}

// yamlError rewrites a yaml.v3 error as file: line N: text, so every
// message reads alike and names the key in the rule's own words.
func yamlError(file string, err error) error {
	var te *yaml.TypeError
	if errors.As(err, &te) && len(te.Errors) > 0 {
		return yamlLineText(file, te.Errors[0])
	}
	return yamlLineText(file, strings.TrimPrefix(err.Error(), "yaml: "))
}

func yamlLineText(file, msg string) error {
	m := yamlLine.FindStringSubmatch(msg)
	if m == nil {
		return lineError(file, 1, "%s", msg)
	}
	n, _ := strconv.Atoi(m[1])
	text := m[2]
	if k := notFound.FindStringSubmatch(text); k != nil {
		text = fmt.Sprintf("unknown key %q", k[1])
	}
	return lineError(file, n, "%s", text)
}

// checker validates a decoded rule and keeps the first error, with the
// line of the key it is about.
type checker struct {
	file string
	root *yaml.Node
	err  error
}

// line finds the line of a dotted key path (api.reject, accept.1) in
// the file; the nearest parent that exists when the key doesn't.
func (c *checker) line(path string) int {
	n := c.root
	line := n.Line
	for _, part := range strings.Split(path, ".") {
		var next *yaml.Node
		switch n.Kind {
		case yaml.MappingNode:
			for i := 0; i+1 < len(n.Content); i += 2 {
				if n.Content[i].Value == part {
					line = n.Content[i].Line
					next = n.Content[i+1]
					break
				}
			}
		case yaml.SequenceNode:
			if i, err := strconv.Atoi(part); err == nil && i >= 0 && i < len(n.Content) {
				next = n.Content[i]
				line = next.Line
			}
		}
		if next == nil {
			return line
		}
		n = next
	}
	return line
}

func (c *checker) fail(path, format string, a ...any) {
	if c.err == nil {
		c.err = lineError(c.file, c.line(path), "%s: %s", path, fmt.Sprintf(format, a...))
	}
}

// locator parses a one-step locator (a detect selector, the banner, the
// toggles).
func (c *checker) locator(path, s string) consent.Locator {
	steps, err := consent.ParseSelector(s)
	if err != nil {
		c.fail(path, "%v", err)
		return consent.Locator{}
	}
	if len(steps) != 1 {
		c.fail(path, "want one step, not a chain with >>")
		return consent.Locator{}
	}
	return steps[0]
}

// locators parses one-step locators written as one string or a list.
func (c *checker) locators(path string, raw oneOrMany) []consent.Locator {
	out := make([]consent.Locator, 0, len(raw))
	for i, s := range raw {
		p := path
		if len(raw) > 1 {
			p = fmt.Sprintf("%s.%d", path, i)
		}
		out = append(out, c.locator(p, s))
	}
	return out
}

// chains parses a list of click chains.
func (c *checker) chains(path string, raw []string) []chain {
	out := make([]chain, 0, len(raw))
	for i, s := range raw {
		steps, err := consent.ParseSelector(s)
		if err != nil {
			c.fail(fmt.Sprintf("%s.%d", path, i), "%v", err)
			continue
		}
		out = append(out, chain{Raw: s, Steps: steps})
	}
	return out
}

// js checks one of a rule's JavaScript calls: a single constant
// expression with nothing from the deny list.
func (c *checker) js(path, s string) string {
	if s == "" {
		return ""
	}
	if len(s) > 512 {
		c.fail(path, "is %d bytes; a call is a line, keep it under 512", len(s))
		return ""
	}
	flat := strings.Join(strings.Fields(s), "")
	for _, bad := range deniedJS {
		if strings.Contains(flat, bad) {
			c.fail(path, "%q is not allowed in a rule's JavaScript; rules make fixed calls only", bad)
			return ""
		}
	}
	switch {
	case strings.ContainsAny(s, "`"):
		c.fail(path, "template literals are not allowed; write one constant expression")
	case strings.Contains(s, ";"):
		c.fail(path, "want a single expression, not statements")
	case strings.ContainsAny(s, "\n\r"):
		c.fail(path, "want a single expression on one line")
	case strings.Contains(flat, "'+") || strings.Contains(flat, "+'") || strings.Contains(flat, "\"+") || strings.Contains(flat, "+\""):
		c.fail(path, "string concatenation is not allowed; write one constant expression")
	}
	return s
}

func (c *checker) rule(f *ruleFile) *rule {
	r := &rule{ID: f.ID, Name: f.Name, Priority: f.Priority, File: c.file}
	base := strings.TrimSuffix(c.file[strings.LastIndex(c.file, "/")+1:], ".yaml")
	switch {
	case f.ID == "":
		c.fail("id", "missing")
	case !ruleID.MatchString(f.ID):
		c.fail("id", "%q: want lowercase letters, digits and -", consent.SafeText(f.ID, 40))
	case f.ID != base:
		c.fail("id", "%q doesn't match the file name %s.yaml", f.ID, base)
	}
	if strings.TrimSpace(f.Name) == "" {
		c.fail("name", "missing")
	}
	if f.Priority < 0 || f.Priority > 1000 {
		c.fail("priority", "%d: want 0 to 1000", f.Priority)
	}

	for i, s := range f.Detect.Selectors {
		r.Selectors = append(r.Selectors, c.locator(fmt.Sprintf("detect.selectors.%d", i), s))
		r.SelRaw = append(r.SelRaw, s)
	}
	for i, g := range f.Detect.Globals {
		if !identifier.MatchString(g) {
			c.fail(fmt.Sprintf("detect.globals.%d", i), "%q: want a JavaScript name", consent.SafeText(g, 40))
		}
	}
	r.Globals = f.Detect.Globals
	for i, s := range f.Detect.Scripts {
		if len(s) < 4 || len(s) > 200 || strings.ContainsAny(s, " \t\n\"'<>") {
			c.fail(fmt.Sprintf("detect.scripts.%d", i), "%q: want a URL substring of 4 to 200 characters", consent.SafeText(s, 40))
		}
	}
	r.Scripts = f.Detect.Scripts
	if f.ID != genericID && len(r.Selectors)+len(r.Globals)+len(r.Scripts) == 0 {
		c.fail("detect", "want at least one selector, global or script")
	}

	switch {
	case f.ID == genericID && len(f.Banner.Selector) == 0:
		// The generic rule finds its banner by its words (generic.go).
	case len(f.Banner.Selector) == 0 || f.Banner.Selector[0] == "":
		c.fail("banner.selector", "missing")
	default:
		r.Banners = c.locators("banner.selector", f.Banner.Selector)
		r.BannerRaw = f.Banner.Selector[0]
	}
	r.Accept = c.chains("accept", f.Accept)
	r.FirstLayer = c.chains("reject.first_layer", f.Reject.FirstLayer)
	if s := f.Reject.SecondLayer; s != nil {
		r.Second = &secondLayer{Open: c.chains("reject.second_layer.open", s.Open), Save: c.chains("reject.second_layer.save", s.Save)}
		if len(s.Open) == 0 {
			c.fail("reject.second_layer.open", "missing")
		}
		if len(s.Save) == 0 {
			c.fail("reject.second_layer.save", "missing")
		}
		r.Second.Toggles = c.locators("reject.second_layer.toggles_off", s.TogglesOff)
		r.Second.Press = c.locators("reject.second_layer.press_off", s.PressOff)
	}
	if f.ID != genericID {
		if len(r.Accept) == 0 && f.API.Accept == "" {
			c.fail("accept", "want at least one selector or api.accept")
		}
		if len(r.FirstLayer) == 0 && r.Second == nil && f.API.Reject == "" {
			c.fail("reject", "want first_layer, second_layer or api.reject")
		}
	}

	r.API = apiCalls{
		Accept: c.js("api.accept", f.API.Accept),
		Reject: c.js("api.reject", f.API.Reject),
		Reopen: c.js("api.reopen", f.API.Reopen),
		State:  c.js("api.state", f.API.State),
	}
	r.ReopenSel = c.chains("reopen.selectors", f.Reopen.Selectors)
	if a := f.Reopen.API; a != "" {
		calls := map[string]string{"accept": r.API.Accept, "reject": r.API.Reject, "reopen": r.API.Reopen, "state": r.API.State}
		if call, ok := calls[a]; !ok || call == "" {
			c.fail("reopen.api", "%q: want the name of a set api entry (reopen)", consent.SafeText(a, 40))
		}
		r.ReopenAPI = a
	}

	v := f.Verify
	switch {
	case v.Parser == "":
		c.fail("verify.parser", "missing")
	case !slices.Contains(parsers, v.Parser):
		c.fail("verify.parser", "%q: want one of %s", consent.SafeText(v.Parser, 40), strings.Join(parsers, ", "))
	}
	for i, n := range v.Cookies {
		if !cookieName.MatchString(n) {
			c.fail(fmt.Sprintf("verify.cookies.%d", i), "%q: want a cookie name, a trailing * for a prefix", consent.SafeText(n, 40))
		}
	}
	for i, n := range v.Storage {
		if !cookieName.MatchString(n) || strings.HasSuffix(n, "*") {
			c.fail(fmt.Sprintf("verify.storage.%d", i), "%q: want a storage key", consent.SafeText(n, 40))
		}
	}
	stateJS := c.js("verify.state_js", v.StateJS)
	if stateJS == "" {
		stateJS = r.API.State
	}
	if v.Parser != "banner" && v.Parser != "" && len(v.Cookies)+len(v.Storage) == 0 && stateJS == "" {
		c.fail("verify", "want cookies, storage or state_js for parser %s", v.Parser)
	}
	r.Verify = consent.VerifySpec{Parser: v.Parser, Cookies: v.Cookies, Storage: v.Storage, StateJS: stateJS}
	r.Accepted, r.Rejected, r.TCF = v.Accepted, v.Rejected, v.TCF

	if g := f.GeoOverride; g != nil {
		if !paramName.MatchString(g.Param) {
			c.fail("geo_override.param", "%q: want a query parameter name", consent.SafeText(g.Param, 40))
		}
		if g.ValueFrom != "country" {
			c.fail("geo_override.value_from", "%q: want country", consent.SafeText(g.ValueFrom, 40))
		}
		r.Geo = &geoOverride{Param: g.Param, ValueFrom: g.ValueFrom}
	}

	if len(f.Sources) == 0 {
		c.fail("sources", "missing: name the vendor documentation the rule was written from")
	}
	for i, s := range f.Sources {
		if u, err := url.Parse(s); err != nil || u.Scheme != "https" || u.Host == "" {
			c.fail(fmt.Sprintf("sources.%d", i), "%q: want an https URL", consent.SafeText(s, 80))
		}
	}
	r.Sources = f.Sources
	for i, p := range f.Fixtures {
		if !strings.HasPrefix(p, "testdata/") || strings.Contains(p, "..") {
			c.fail(fmt.Sprintf("fixtures.%d", i), "%q: want a path under testdata/", consent.SafeText(p, 80))
		}
	}
	r.Fixtures = f.Fixtures
	return r
}
