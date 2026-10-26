// Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
// SPDX-License-Identifier: MIT

package tracker

import (
	"embed"
	"fmt"
	"regexp"
	"slices"
	"strings"

	"github.com/RhythmShahriar/echo/consent"
)

// The catalog is one vendor per file, catalog/<id>.yaml, with its
// fixtures beside it in catalog/<id>.fixtures.yaml:
//
//	id: ga4                        # [a-z0-9-]+, the file name too
//	name: Google Analytics 4
//	vendor: Google
//	category: analytics            # analytics, advertising, session-replay,
//	                               #   social, embed, functional, necessary
//	consent: needed                # needed, exempt or no
//	any_host: true                 # rules without hosts match on any host
//	hits:                          # requests that send data (held)
//	  - hosts: [www.google-analytics.com, "*.google-analytics.com"]
//	    paths: [/g/collect]        # path.Match globs; /x/** is /x and below
//	    methods: [GET, POST]       # optional
//	    params_all: [v, tid]       # every name present (query or body)
//	    params_any: [en]           # at least one present
//	    params_match: {v: '^2$'}   # RE2 on the first value
//	    not_params: {dnt: '1'}     # never when a parameter has this value
//	    body_json: [n, u]          # the body is a JSON object with these keys
//	  - host: px.shop.example      # host: and path: are one-value shorthands
//	    path: /collect
//	scripts: [{hosts: [...], paths: [...]}]   # script loads, never held
//	frames: [{hosts: [...], paths: [...]}]    # iframes (embeds)
//	cookies: [_ga, "_ga_*", {name: IDE, domain: doubleclick.net}]
//	storage: ["mp_*"]              # localStorage, sessionStorage, IndexedDB names
//	params:
//	  event: [en]                  # event name, first found wins
//	  id: [tid]                    # measurement or pixel ID (masked to its last 4)
//	  id_path: '^/pagead/conversion/([0-9]+)'   # or the ID from the path
//	  personal: [em, ph]           # never kept; their names are listed
//	fix: One line on how to fix it
//	docs: https://vendor.example/docs
//	sources: [https://vendor.example/docs]
//
// A name pattern has at most one "*", standing for any run of
// characters. Hosts are exact or "*.suffix" (subdomains only). Unknown
// keys are errors naming the file and line, so a typo can't switch a
// rule off without anyone noticing.

// The pattern names the files, so _not_trackers.yaml is embedded too
// (a directory pattern would skip names starting with _).
//
//go:embed catalog/*.yaml
var catalogFS embed.FS

// catalogDir is where the embedded catalog lives in catalogFS.
const catalogDir = "catalog"

// Catalog is the tracker catalog compiled from YAML: the consent
// Classifier. It is safe for concurrent use once AddCustom, if called,
// has returned: AddCustom is called once per command, before any page
// is checked.
type Catalog struct {
	entries []*entry // built-in and custom, sorted by id
	base    []*entry // the built-in entries alone
	tiers   [3][]*rule
	custom  [3][]*rule // the config's own rules, tried first
	cookies names
	storage names
	deny    *denyList // names that are never trackers; nil in tests
	info    consent.DataInfo
}

var _ consent.Classifier = (*Catalog)(nil)

// entry is one compiled catalog entry.
type entry struct {
	info     consent.TrackerInfo
	file     string
	custom   bool    // from the config's consent: trackers:
	rules    []*rule // scripts, frames, then hits, in file order
	cookies  []nameSpec
	storage  []nameSpec
	events   []string
	ids      []string
	idPath   *regexp.Regexp
	personal []string
}

// builtin is the embedded catalog, loaded once at init.
var builtin *Catalog

func init() {
	c, err := load(catalogFS, catalogDir)
	if err != nil {
		// The embedded catalog is checked by the tests, so this is a
		// build mistake, never something a user can cause.
		panic(err)
	}
	builtin = c
	consent.SetClassifier(c)
}

// where names a line of the entry for errors: file:line for the
// built-in catalog, "consent: trackers: <id>" for a custom entry, whose
// YAML came out of the config and has no lines of its own.
func (e *entry) where(line int) string {
	if e.custom {
		return customPrefix + e.info.ID
	}
	return fmt.Sprintf("%s:%d", e.file, line)
}

// Trackers lists the catalog sorted by id, custom entries included
// (their Sources read config).
func (c *Catalog) Trackers() []consent.TrackerInfo {
	out := make([]consent.TrackerInfo, 0, len(c.entries))
	for _, e := range c.entries {
		t := e.info
		t.Sources = slices.Clone(t.Sources)
		out = append(out, t)
	}
	return out
}

// Info names the compiled-in catalog: the data version, the SHA-256 of
// its files and the number of entries. Custom entries don't change it:
// the hash and count say which built-in data a result was judged with.
func (c *Catalog) Info() consent.DataInfo { return c.info }

// summary is the one-line Matches text of `consent trackers`: the first
// three host and path patterns (hits, then frames, then scripts), or the cookies when there are no rules.
func summary(e *entry) string {
	var parts []string
	for _, k := range []consent.HitKind{consent.HitBeacon, consent.HitFrame, consent.HitScript} {
		for _, r := range e.rules {
			if r.kind == k {
				parts = append(parts, r.patterns()...)
			}
		}
	}
	if len(parts) == 0 {
		for _, n := range e.cookies {
			parts = append(parts, "cookie "+n.Name)
		}
	}
	if len(parts) <= 3 {
		return strings.Join(parts, ", ")
	}
	return fmt.Sprintf("%s and %d more", strings.Join(parts[:3], ", "), len(parts)-3)
}
