// Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
// SPDX-License-Identifier: MIT

package cli

import (
	"errors"
	"flag"
	"fmt"
	"io"
	"net/url"
	"os"
	"regexp"
	"slices"
	"strings"

	"gopkg.in/yaml.v3"

	kitcli "github.com/RhythmShahriar/rhythm/kit/cli"
)

// maxSitesFile bounds the sites file read: thousands of sites fit; a
// stray log or binary passed by mistake doesn't fill the memory.
const maxSitesFile = 4 << 20

// site is one entry of a --sites file: the file Soundcheck fleet reads
// (PRD 8.2). Echo reads name, url and consent; Soundcheck's own keys
// are accepted and left alone.
type site struct {
	Name    string       `yaml:"name"`
	URL     string       `yaml:"url"`
	Consent *siteConsent `yaml:"consent"`

	// Soundcheck's keys, read by Soundcheck fleet only.
	Config    string   `yaml:"config"`
	EnvPrefix string   `yaml:"env_prefix"`
	Args      []string `yaml:"args"`
}

// siteConsent is a site's consent: map: its pages and its own flags.
type siteConsent struct {
	URLs []string `yaml:"urls"`
	Args []string `yaml:"args"`
}

// The keys each level of a sites file may have.
var (
	siteKeys        = []string{"name", "url", "consent", "config", "env_prefix", "args"}
	siteConsentKeys = []string{"urls", "args"}
)

// siteName is Soundcheck's rule for a site's name: it names a folder.
var siteName = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]{0,63}$`)

// loadSites reads and checks a --sites file.
func loadSites(path string) ([]site, error) {
	f, err := os.Open(path) //nolint:gosec // the user's own sites file
	if err != nil {
		return nil, usagef("--sites %s: %v", path, err)
	}
	defer func() { _ = f.Close() }()
	data, err := io.ReadAll(io.LimitReader(f, maxSitesFile+1))
	if err != nil {
		return nil, usagef("--sites %s: %v", path, err)
	}
	if len(data) > maxSitesFile {
		return nil, usagef("--sites %s: the file is more than 4 MiB", path)
	}
	sites, err := parseSites(data)
	if err != nil {
		return nil, usagef("%s: %v", path, err)
	}
	return sites, nil
}

// parseSites reads a sites file: a list of sites, or a mapping with the
// list under sites:, as Soundcheck fleet takes it. Unknown keys are
// refused with their line, so a typo isn't silently ignored; names
// follow Soundcheck's rules and may not repeat in any case.
func parseSites(data []byte) ([]site, error) {
	var root yaml.Node
	if err := yaml.Unmarshal(data, &root); err != nil {
		return nil, err
	}
	if len(root.Content) == 0 {
		return nil, errors.New("no sites listed")
	}
	node := root.Content[0]
	if node.Kind == yaml.MappingNode {
		var list *yaml.Node
		for i := 0; i+1 < len(node.Content); i += 2 {
			k := node.Content[i]
			if k.Value != "sites" {
				return nil, keyError(k, "", []string{"sites"})
			}
			list = node.Content[i+1]
		}
		if list == nil {
			return nil, errors.New("no sites listed")
		}
		node = list
	}
	if node.Kind != yaml.SequenceNode {
		return nil, fmt.Errorf("line %d: want a list of sites, each with a name and a url", node.Line)
	}
	for _, e := range node.Content {
		if e.Kind != yaml.MappingNode {
			return nil, fmt.Errorf("line %d: a site is a mapping with a name and a url", e.Line)
		}
		for i := 0; i+1 < len(e.Content); i += 2 {
			k, v := e.Content[i], e.Content[i+1]
			if !slices.Contains(siteKeys, k.Value) {
				return nil, keyError(k, "a site", siteKeys)
			}
			if k.Value != "consent" || v.Kind != yaml.MappingNode {
				continue
			}
			for j := 0; j+1 < len(v.Content); j += 2 {
				if !slices.Contains(siteConsentKeys, v.Content[j].Value) {
					return nil, keyError(v.Content[j], "consent", siteConsentKeys)
				}
			}
		}
	}
	var sites []site
	if err := node.Decode(&sites); err != nil {
		return nil, err
	}
	if len(sites) == 0 {
		return nil, errors.New("no sites listed")
	}
	names := map[string]string{}
	for i, s := range sites {
		where := fmt.Sprintf("site %d", i+1)
		if s.Name != "" {
			where = fmt.Sprintf("site %q", s.Name)
		}
		if !siteName.MatchString(s.Name) {
			return nil, fmt.Errorf("%s: name must be letters, digits, dots, dashes and underscores (up to 64), starting with a letter or digit", where)
		}
		// Compared without case, as Soundcheck does: its folders on
		// macOS and Windows ignore case.
		if other, ok := names[strings.ToLower(s.Name)]; ok {
			return nil, fmt.Errorf("%s: the name is already used by site %q", where, other)
		}
		names[strings.ToLower(s.Name)] = s.Name
		if err := checkSiteURL(s.URL); err != nil {
			return nil, fmt.Errorf("%s: %w", where, err)
		}
	}
	return sites, nil
}

// keyError names an unknown key, its line and the key meant.
func keyError(k *yaml.Node, where string, keys []string) error {
	msg := fmt.Sprintf("line %d: %q isn't a key", k.Line, k.Value)
	if where != "" {
		msg = fmt.Sprintf("line %d: %s: %q isn't a key", k.Line, where, k.Value)
	}
	if s := kitcli.Suggest(k.Value, keys); s != "" {
		return fmt.Errorf("%s; did you mean %s?", msg, s)
	}
	return fmt.Errorf("%s; use %s", msg, strings.Join(keys, ", "))
}

// checkSiteURL checks a site's url: as Soundcheck does: http or https,
// a bare host allowed.
func checkSiteURL(raw string) error {
	if strings.TrimSpace(raw) == "" {
		return errors.New("url is missing")
	}
	s := raw
	if !strings.Contains(s, "://") {
		s = "https://" + s
	}
	u, err := url.Parse(s)
	if err != nil || u.Host == "" || (u.Scheme != "http" && u.Scheme != "https") {
		return fmt.Errorf("url %q: want an http or https URL", raw)
	}
	if u.User != nil {
		return errors.New("url: a URL with a login in it isn't allowed")
	}
	return nil
}

// siteArgFlags are the flags a site's consent.args may set: how its
// pages are checked. Everything that reads files, runs programs,
// sends hits, unmasks IDs, weakens TLS or changes where results go
// stays with the person running the command, since a sites file is
// shared.
var siteArgFlags = []string{"runs", "pages", "settle", "banner-wait", "timeout", "retries", "cmp", "accept-selector", "reject-selector",
	"click", "consent-mode", "exempt", "geo", "locale", "timezone", "viewport", "follow-host", "user-agent", "header", "cookie"}

// siteOptions parses a site's consent.args.
func siteOptions(s site) (*options, error) {
	if s.Consent == nil || len(s.Consent.Args) == 0 {
		return &options{set: map[string]bool{}}, nil
	}
	o := &options{set: map[string]bool{}}
	fs := newFlagSet(o)
	pos, err := kitcli.ParseInterspersed(fs, s.Consent.Args)
	if err != nil {
		return nil, usagef("site %q: consent: args: %v", s.Name, flagError(err, fs, subCheck))
	}
	if len(pos) > 0 {
		return nil, usagef("site %q: consent: args take flags only, not %q; list pages under consent: urls", s.Name, pos[0])
	}
	var refused string
	fs.Visit(func(f *flag.Flag) {
		name := longName(f.Name)
		o.set[name] = true
		if refused == "" && !slices.Contains(siteArgFlags, name) {
			refused = name
		}
	})
	if refused != "" {
		return nil, usagef("site %q: consent: args can't set --%s; only the command line can", s.Name, refused)
	}
	// Values come from the environment of the person running the check,
	// under Echo's prefix only: a shared file can't read files or other
	// variables into a request.
	for _, h := range slices.Concat(o.headers, o.cookies) {
		if !siteEnvValue(h) {
			return nil, usagef("site %q: consent: args: header and cookie values must be env:RHYTHM_ECHO_NAME, never a file or the value itself", s.Name)
		}
	}
	return o, nil
}

// siteEnvRef is a value read from one of Echo's variables.
var siteEnvRef = regexp.MustCompile(`^env:RHYTHM_ECHO_[A-Z0-9_]+$`)

// siteEnvValue reports whether a site's -H or --cookie value comes
// from one of Echo's variables.
func siteEnvValue(arg string) bool {
	if name, value, ok := strings.Cut(arg, ":"); ok && !strings.HasPrefix(arg, "env:") && headerToken.MatchString(strings.TrimSpace(name)) {
		return siteEnvRef.MatchString(strings.TrimSpace(value))
	}
	if _, value, ok := strings.Cut(arg, "="); ok {
		return siteEnvRef.MatchString(strings.TrimSpace(value))
	}
	return false
}
