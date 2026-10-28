// Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
// SPDX-License-Identifier: MIT

package config

import (
	"fmt"
	"maps"
	"net"
	"net/url"
	"reflect"
	"regexp"
	"slices"
	"strings"
	"time"

	"gopkg.in/yaml.v3"

	"github.com/RhythmShahriar/echo/consent"
	kitcli "github.com/RhythmShahriar/rhythm/kit/cli"
)

// Consent is the consent: block of the config (PRD 8.1): what rhythm
// echo consent checks and how, each key the config form of a flag.
// Levels stay in the top-level checks: map.
type Consent struct {
	URLs           []string          `yaml:"urls,omitempty"` // read against site:; absolute URLs work too
	Runs           []string          `yaml:"runs,omitempty"` // none, accept, reject, gpc, withdraw
	Pages          *int              `yaml:"pages,omitempty"`
	Settle         *Duration         `yaml:"settle,omitempty"`
	BannerWait     *Duration         `yaml:"banner_wait,omitempty"`
	Timeout        *Duration         `yaml:"timeout,omitempty"`
	Retries        *int              `yaml:"retries,omitempty"`
	CMP            string            `yaml:"cmp,omitempty"`
	Click          string            `yaml:"click,omitempty"` // button, api or button,api
	AcceptSelector string            `yaml:"accept_selector,omitempty"`
	RejectSelector string            `yaml:"reject_selector,omitempty"`
	ConsentMode    string            `yaml:"consent_mode,omitempty"` // advanced, basic or strict
	Geo            string            `yaml:"geo,omitempty"`
	Locale         string            `yaml:"locale,omitempty"`
	Timezone       string            `yaml:"timezone,omitempty"`
	Viewport       string            `yaml:"viewport,omitempty"`
	Parallel       int               `yaml:"parallel,omitempty"`
	Exempt         []string          `yaml:"exempt,omitempty"`
	Headers        map[string]string `yaml:"headers,omitempty"`   // values must be ${env:RHYTHM_ECHO_*}
	MapHosts       map[string]string `yaml:"map_hosts,omitempty"` // host or *.host to an address
	Ignore         []consent.Ignore  `yaml:"ignore,omitempty"`    // known issues, each with a reason and an expiry
	Trackers       yaml.Node         `yaml:"trackers,omitempty"`  // custom trackers, the catalog's shape; the classifier reads them
}

// ConsentCMPs are the values consent: cmp and --cmp take: auto, then
// every CMP rule PRD 7 names.
var ConsentCMPs = []string{"auto", "cookiebot", "onetrust", "cookieyes", "complianz", "usercentrics", "borlabs", "didomi", "iubenda", "generic"}

// consentNow is the clock ignore: until dates are held to; tests set it.
var consentNow = time.Now

// MaxIgnoreDays is how far ahead an ignore: entry's until may be: a
// known issue gets reviewed at least once a year.
const MaxIgnoreDays = 365

// UnmarshalYAML refuses a key the block doesn't have, naming its line
// and the key meant; then it decodes the rest. An unknown key in an ignore:
// entry is refused the same way.
func (c *Consent) UnmarshalYAML(n *yaml.Node) error {
	if n.Kind != yaml.MappingNode {
		if n.Tag == "!!null" {
			return nil
		}
		return fmt.Errorf("line %d: consent: want keys like urls: and runs:, e.g. consent: {urls: [/]}", n.Line)
	}
	if err := knownKeys(n, "consent", yamlKeys(reflect.TypeFor[Consent]())); err != nil {
		return err
	}
	for i := 0; i+1 < len(n.Content); i += 2 {
		if n.Content[i].Value != "ignore" || n.Content[i+1].Kind != yaml.SequenceNode {
			continue
		}
		for _, e := range n.Content[i+1].Content {
			if e.Kind == yaml.MappingNode {
				if err := knownKeys(e, "consent: ignore", yamlKeys(reflect.TypeFor[consent.Ignore]())); err != nil {
					return err
				}
			}
		}
	}
	type plain Consent // without this method
	return n.Decode((*plain)(c))
}

// knownKeys refuses a key of the mapping n that isn't in keys.
func knownKeys(n *yaml.Node, where string, keys []string) error {
	for i := 0; i+1 < len(n.Content); i += 2 {
		k := n.Content[i]
		if slices.Contains(keys, k.Value) {
			continue
		}
		msg := fmt.Sprintf("line %d: %s: %s isn't a key", k.Line, where, k.Value)
		if s := kitcli.Suggest(k.Value, keys); s != "" {
			return fmt.Errorf("%s; did you mean %s?", msg, s)
		}
		return fmt.Errorf("%s; use %s", msg, strings.Join(keys, ", "))
	}
	return nil
}

// yamlKeys lists the yaml names of t's fields.
func yamlKeys(t reflect.Type) []string {
	var keys []string
	for f := range t.Fields() {
		name, _, _ := strings.Cut(f.Tag.Get("yaml"), ",")
		if name != "" && name != "-" {
			keys = append(keys, name)
		}
	}
	return keys
}

var (
	consentGeo      = regexp.MustCompile(`^[A-Za-z]{2}$`)
	consentLocale   = regexp.MustCompile(`^[A-Za-z]{2,3}([-_][A-Za-z0-9]{2,8})*$`)
	consentTimezone = regexp.MustCompile(`^(UTC|[A-Za-z][A-Za-z0-9_+-]*(/[A-Za-z0-9_+-]+)+)$`)
	consentEnvRef   = regexp.MustCompile(`^\$\{env:RHYTHM_ECHO_[A-Z0-9_]+\}$`)
	headerName      = regexp.MustCompile("^[!#$%&'*+.^_`|~0-9A-Za-z-]+$")
	untilDate       = regexp.MustCompile(`^\d{4}-\d{2}-\d{2}$`)
)

// validate checks the consent: block and the consent check IDs under
// checks:. It is nil-safe: a config without the block still has its
// checks: entries checked.
func (c *Consent) validate(cfg *Config) error {
	if err := consentChecks(cfg); err != nil {
		return err
	}
	if c == nil {
		return nil
	}
	at := func(path ...any) string { return cfg.at(append([]any{"consent"}, path...)...) }
	for i, u := range c.URLs {
		if err := consentURL(u, cfg.Site); err != nil {
			return fmt.Errorf("%sconsent: urls: %w", at("urls", i), err)
		}
	}
	if len(c.Runs) > 0 {
		if _, err := consent.ParseRuns(strings.Join(c.Runs, ",")); err != nil {
			return fmt.Errorf("%sconsent: runs %s", at("runs"), strings.TrimPrefix(err.Error(), "--runs "))
		}
	}
	if err := c.ranges(at); err != nil {
		return err
	}
	if c.CMP != "" && !slices.Contains(ConsentCMPs, strings.ToLower(c.CMP)) {
		return fmt.Errorf("%sconsent: cmp %q: want auto or one of %s", at("cmp"), c.CMP, strings.Join(ConsentCMPs[1:], ", "))
	}
	if _, err := consent.ParseClickMode(c.Click); err != nil {
		return fmt.Errorf("%sconsent: click %s", at("click"), strings.TrimPrefix(err.Error(), "--click "))
	}
	if _, err := consent.ParsePolicy(c.ConsentMode); err != nil {
		return fmt.Errorf("%sconsent: consent_mode %s", at("consent_mode"), strings.TrimPrefix(err.Error(), "--consent-mode "))
	}
	for key, sel := range map[string]string{"accept_selector": c.AcceptSelector, "reject_selector": c.RejectSelector} {
		if sel == "" {
			continue
		}
		if _, err := consent.ParseSelector(sel); err != nil {
			return fmt.Errorf("%sconsent: %s: %w", at(key), key, err)
		}
	}
	switch {
	case c.Geo != "" && !consentGeo.MatchString(c.Geo):
		return fmt.Errorf("%sconsent: geo %q: want a two-letter country code (ISO 3166-1 alpha-2), e.g. de", at("geo"), c.Geo)
	case c.Locale != "" && !consentLocale.MatchString(c.Locale):
		return fmt.Errorf("%sconsent: locale %q: want a language tag like de-DE or en-US", at("locale"), c.Locale)
	case c.Timezone != "" && (!consentTimezone.MatchString(c.Timezone) || len(c.Timezone) > 64):
		return fmt.Errorf("%sconsent: timezone %q: want an IANA time zone like Europe/Berlin or UTC", at("timezone"), c.Timezone)
	case c.Viewport != "" && c.Viewport != "desktop" && c.Viewport != "mobile":
		return fmt.Errorf("%sconsent: viewport %q: want desktop or mobile", at("viewport"), c.Viewport)
	}
	for i, e := range c.Exempt {
		if strings.TrimSpace(e) == "" {
			return fmt.Errorf("%sconsent: exempt: an empty tracker id; rhythm echo consent trackers lists them", at("exempt", i))
		}
	}
	for _, name := range slices.Sorted(maps.Keys(c.Headers)) {
		if !headerName.MatchString(name) {
			return fmt.Errorf("%sconsent: headers: %q isn't a header name", at("headers", name), name)
		}
		// Message 7 of PRD 10.2. The value is never quoted back: it may
		// be the secret itself.
		if !consentEnvRef.MatchString(strings.TrimSpace(c.Headers[name])) {
			return fmt.Errorf("%sconsent: headers: %s: use ${env:RHYTHM_ECHO_NAME}, not the value itself; secrets never sit in the config", at("headers", name), name)
		}
	}
	for _, host := range slices.Sorted(maps.Keys(c.MapHosts)) {
		if err := ParseMapHost(host, c.MapHosts[host]); err != nil {
			return fmt.Errorf("%sconsent: map_hosts: %w", at("map_hosts", host), err)
		}
	}
	for i, ig := range c.Ignore {
		if err := consentIgnore(ig); err != nil {
			return fmt.Errorf("%sconsent: ignore: %w", at("ignore", i), err)
		}
	}
	if c.Trackers.Kind != 0 && c.Trackers.Kind != yaml.SequenceNode && c.Trackers.Tag != "!!null" {
		return fmt.Errorf("%sconsent: trackers: want a list of trackers, each like the catalog's (id, name, category, hits, fixtures)", at("trackers"))
	}
	return nil
}

// ranges holds the numbers and durations of the block to what the
// flags take (PRD 7), in the flags' words with the config's key.
func (c *Consent) ranges(at func(path ...any) string) error {
	type bound struct {
		key  string
		d    *Duration
		max  time.Duration
		want string
	}
	if c.Pages != nil && (*c.Pages < 0 || *c.Pages > consent.MaxPages) {
		return fmt.Errorf("%sconsent: pages %d: want 0 to %d", at("pages"), *c.Pages, consent.MaxPages)
	}
	if c.Retries != nil && (*c.Retries < 0 || *c.Retries > consent.MaxRetries) {
		return fmt.Errorf("%sconsent: retries %d: want 0 to %d", at("retries"), *c.Retries, consent.MaxRetries)
	}
	if c.Parallel < 0 || c.Parallel > consent.MaxParallel {
		return fmt.Errorf("%sconsent: parallel %d: want 1 to %d", at("parallel"), c.Parallel, consent.MaxParallel)
	}
	for _, b := range []bound{{"settle", c.Settle, consent.MaxSettle, "30s"}, {"banner_wait", c.BannerWait, consent.MaxBannerWait, "60s"},
		{"timeout", c.Timeout, consent.MaxTimeout, "10m"}} {
		switch {
		case b.d == nil:
		case b.d.Duration < 0 || b.key == "timeout" && b.d.Duration == 0:
			return fmt.Errorf("%sconsent: %s %s: want more than 0 and at most %s", at(b.key), b.key, Short(b.d.Duration), b.want)
		case b.d.Duration > b.max:
			return fmt.Errorf("%sconsent: %s %s: want at most %s", at(b.key), b.key, Short(b.d.Duration), b.want)
		}
	}
	return nil
}

// consentChecks refuses a consent/ ID under checks: that isn't a
// consent check (naming the one meant) or a level a consent check
// can't have (broke and changed are comparison classes).
func consentChecks(cfg *Config) error {
	ids := make([]string, len(consent.Checks))
	for i, ch := range consent.Checks {
		ids[i] = ch.ID
	}
	for _, id := range slices.Sorted(maps.Keys(cfg.Checks)) {
		if !strings.HasPrefix(id, "consent/") {
			continue
		}
		if _, ok := consent.Check(id); !ok {
			msg := fmt.Sprintf("%schecks: %s isn't a check", cfg.at("checks", id), id)
			if s := kitcli.Suggest(id, ids); s != "" {
				return fmt.Errorf("%s; did you mean %s?", msg, s)
			}
			return fmt.Errorf("%s; rhythm echo checks lists them", msg)
		}
		if l := cfg.Checks[id]; !slices.Contains([]Level{Off, Notice, Warning, Error}, l) {
			return fmt.Errorf("%schecks: %s: %q isn't off, notice, warning or error", cfg.at("checks", id), id, l)
		}
	}
	return nil
}

// consentURL checks one consent: urls entry: an http or https URL, or
// a path read against site:.
func consentURL(raw, site string) error {
	s := strings.TrimSpace(raw)
	if strings.HasPrefix(s, "/") && !strings.HasPrefix(s, "//") {
		if site == "" {
			return fmt.Errorf("%q is a path; set site: or give the whole URL", raw)
		}
		return nil
	}
	if !strings.Contains(s, "://") {
		s = "https://" + s
	}
	u, err := url.Parse(s)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
		return fmt.Errorf("%q: want an http or https URL, or a path like /shop/", consent.MaskURL(raw, false))
	}
	if u.User != nil {
		return fmt.Errorf("%q: a URL with a login in it isn't allowed; use headers: with ${env:RHYTHM_ECHO_NAME}", consent.MaskURL(raw, false))
	}
	return nil
}

// consentIgnore checks one ignore: entry: a consent check, a reason
// and an until date no more than a year away. An entry already past
// its date is fine: it stops applying and the check says so.
func consentIgnore(ig consent.Ignore) error {
	if strings.TrimSpace(ig.Check) == "" {
		return fmt.Errorf("needs check, a consent check ID like %s", consent.CheckBeforeChoice)
	}
	if _, ok := consent.Check(ig.Check); !ok {
		ids := make([]string, len(consent.Checks))
		for i, ch := range consent.Checks {
			ids[i] = ch.ID
		}
		msg := fmt.Sprintf("check %s isn't a consent check", ig.Check)
		if s := kitcli.Suggest(ig.Check, ids); s != "" {
			return fmt.Errorf("%s; did you mean %s?", msg, s)
		}
		return fmt.Errorf("%s; rhythm echo checks lists them", msg)
	}
	if ig.Run != "" && !slices.Contains(consent.AllRuns, ig.Run) {
		return fmt.Errorf("%s: run %q: want none, accept, reject, gpc or withdraw", ig.Check, ig.Run)
	}
	if strings.TrimSpace(ig.Reason) == "" {
		return fmt.Errorf("%s: needs a reason, e.g. reason: \"client signs off until the Nov launch\"", ig.Check)
	}
	if ig.Until == "" {
		return fmt.Errorf("%s: needs until, the date it stops applying, e.g. until: 2026-11-30", ig.Check)
	}
	day, err := time.Parse("2006-01-02", ig.Until)
	if err != nil || !untilDate.MatchString(ig.Until) {
		return fmt.Errorf("%s: until %q: want a date like 2026-11-30", ig.Check, ig.Until)
	}
	now := consentNow()
	today := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, time.UTC)
	if day.Sub(today) > MaxIgnoreDays*24*time.Hour {
		return fmt.Errorf("until %s is more than a year away; review it sooner", ig.Until)
	}
	return nil
}

// ParseMapHost checks one --map-host or consent: map_hosts entry: host
// is a name or *.name, addr is host:port or an http:// or https:// URL.
func ParseMapHost(host, addr string) error {
	h := strings.TrimPrefix(strings.ToLower(strings.TrimSpace(host)), "*.")
	if h == "" || strings.ContainsAny(h, "/:*@ ") {
		return fmt.Errorf("%q: want a host name or *.name, e.g. *.shop.test", host)
	}
	a := strings.TrimSpace(addr)
	if strings.HasPrefix(a, "http://") || strings.HasPrefix(a, "https://") {
		u, err := url.Parse(a)
		if err != nil || u.Host == "" || u.User != nil {
			return fmt.Errorf("%s: %q: want host:port or an http:// or https:// URL, e.g. 127.0.0.1:18950", host, addr)
		}
		return nil
	}
	if _, port, err := net.SplitHostPort(a); err != nil || port == "" {
		return fmt.Errorf("%s: %q: want host:port or an http:// or https:// URL, e.g. 127.0.0.1:18950", host, addr)
	}
	return nil
}
