// Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
// SPDX-License-Identifier: MIT

package cli

import (
	"cmp"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"gopkg.in/yaml.v3"

	"github.com/RhythmShahriar/echo/config"
	"github.com/RhythmShahriar/echo/consent"
	"github.com/RhythmShahriar/echo/sendmail"
	"github.com/RhythmShahriar/echo/store"
)

// msgNoPage is PRD 10.2 message 1: no URL, no --sites and no
// consent.urls in the config.
const msgNoPage = "consent needs a page: rhythm echo consent https://staging.shop.test, or list pages under consent: urls in .echo/config.yaml"

// The messages of the plan's own refusals (PRD 10.2 and the lane spec).
const (
	msgShowIDs = "--show-ids keeps tracking IDs unmasked; it can't be used with Markdown or JUnit output, which are meant to be shared"
	// defaultEcho is Echo's API when neither --echo=<url> nor
	// RHYTHM_ECHO_URL says where it is.
	defaultEcho = "http://127.0.0.1:8025"
)

// setup is a consent check ready to run: a plan per group of pages
// (one, or one per --sites site, since a site's args change its plan),
// the reports to write, the --fail-on level and where results go.
type setup struct {
	plans   []*consent.Plan
	outs    []output
	failOn  string
	cfg     config.Config
	cfgPath string // the config read, "" for none
	store   store.Store
	notes   []string // said on stderr before the check: a config not used
	warn    []string // warnings printed once before the check
}

// pages counts the pages of every plan.
func (st *setup) pages() int {
	n := 0
	for _, p := range st.plans {
		n += len(p.Targets)
	}
	return n
}

// prepare reads the check's command line, the config and the sites
// file into plans, checking everything a run would trip over before
// any browser starts. Nothing is written yet.
func (c *cmd) prepare(pos []string) (*setup, error) {
	o := c.o
	for _, a := range pos {
		if isSub(a) {
			return nil, usagef("consent %q: %s is a subcommand; put it first (rhythm echo consent %s), or write the host as a URL (http://%s/)", a, a, a, a)
		}
	}
	outs, err := o.reports()
	if err != nil {
		return nil, err
	}
	if o.showIDs && (wants(outs, fmtMD) || wants(outs, fmtJUnit)) {
		return nil, usagef("%s", msgShowIDs)
	}
	st := &setup{outs: outs}
	if st.failOn, err = checkFailOn(o.failOn, o.against != ""); err != nil {
		return nil, err
	}
	if err := st.loadConfig(o.configPath); err != nil {
		return nil, err
	}
	base := defaultPlan(c.env.Version)
	if err := applyConfig(base, st.cfg.Consent); err != nil {
		return nil, err
	}
	if err := applyFlags(base, o); err != nil {
		return nil, err
	}
	var block *config.Consent
	if st.cfg.Consent != nil {
		block = st.cfg.Consent
	} else {
		block = &config.Consent{}
	}
	if base.MapHosts, err = parseMapHosts(block.MapHosts, o.mapHost); err != nil {
		return nil, err
	}
	var sec secrets
	if err := sec.resolve(block.Headers, o.headers, o.cookies); err != nil {
		return nil, err
	}
	base.Headers, base.Cookies = nonEmpty(sec.headers), sec.cookies
	base.CheckLevels = checkLevels(st.cfg)
	if o.echo.on {
		base.Echo = &consent.EchoLink{URL: cmp.Or(o.echo.url, os.Getenv("RHYTHM_ECHO_URL"), defaultEcho), Token: os.Getenv("RHYTHM_ECHO_TOKEN")}
	}
	if err := st.customTrackers(base, block); err != nil {
		return nil, err
	}

	switch {
	case o.sites != "" && len(pos) > 0:
		return nil, usagef("give pages or --sites, not both")
	case o.sites != "":
		sites, err := loadSites(o.sites)
		if err != nil {
			return nil, err
		}
		for _, s := range sites {
			p, err := sitePlan(base, s, &sec)
			if err != nil {
				return nil, err
			}
			st.plans = append(st.plans, p)
		}
	default:
		urls := pos
		if len(urls) == 0 {
			urls = block.URLs
		}
		for _, raw := range urls {
			u, err := target(raw, st.cfg.Site)
			if err != nil {
				return nil, err
			}
			base.Targets = append(base.Targets, consent.Target{URL: u})
		}
		if len(base.Targets) == 0 {
			return nil, usagef("%s", msgNoPage)
		}
		st.plans = []*consent.Plan{base}
	}
	st.warn = sec.warn
	if n := st.pages(); n > consent.DefaultLimits.TotalPages {
		return nil, usagef("%d pages is more than %d for one check; split the sites file", n, consent.DefaultLimits.TotalPages)
	}
	for _, p := range st.plans {
		if err := st.check(p); err != nil {
			return nil, err
		}
	}
	st.store = store.Store{Root: storeRoot(o.storeDir, st.cfgPath)}
	return st, nil
}

// check holds one plan to the rules a run can't be trusted to keep:
// the ranges, no real hits to production, no unverified TLS to a
// public site; --show-browser runs one browser at a time.
func (st *setup) check(p *consent.Plan) error {
	if p.Browser.Visible {
		p.Parallel = 1
	}
	if err := p.Validate(); err != nil {
		return usagef("%v", err)
	}
	for _, t := range p.Targets {
		if !p.Hold {
			if e := config.ProductionEntry(st.cfg.Production, t.URL.String()); e != "" {
				return usagef("%s", msgSendHitsProduction(t.URL.Hostname(), e))
			}
		}
		if p.Insecure && !p.InsecurePublic && !privateTarget(t.URL, p.MapHosts) {
			return usagef("-k on %s, a public site: evidence from an unverified connection proves little; add --insecure-public if you mean it", consent.MaskURL(t.URL.String(), false))
		}
	}
	return nil
}

// sitePlan is the plan of one --sites site: the command's plan with
// the site's pages and its consent.args on top.
func sitePlan(base *consent.Plan, s site, sec *secrets) (*consent.Plan, error) {
	p := clonePlan(base)
	p.Targets = nil
	so, err := siteOptions(s)
	if err != nil {
		return nil, err
	}
	if err := applyFlags(p, so); err != nil {
		return nil, usagef("site %q: consent: args: %v", s.Name, err)
	}
	if len(so.headers) > 0 || len(so.cookies) > 0 {
		var own secrets
		own.headers = map[string]string{}
		for k, v := range p.Headers {
			own.headers[k] = v
		}
		own.cookies = p.Cookies
		if err := own.resolve(nil, so.headers, so.cookies); err != nil {
			return nil, usagef("site %q: consent: args: %v", s.Name, err)
		}
		p.Headers, p.Cookies = nonEmpty(own.headers), own.cookies
		sec.warn = append(sec.warn, own.warn...)
	}
	siteURL := s.URL
	if !strings.Contains(siteURL, "://") {
		siteURL = "https://" + siteURL
	}
	var urls []string
	if s.Consent != nil {
		urls = s.Consent.URLs
	}
	if len(urls) == 0 {
		urls = []string{siteURL}
	}
	var args []string
	if s.Consent != nil {
		args = s.Consent.Args
	}
	for _, raw := range urls {
		u, err := target(raw, siteURL)
		if err != nil {
			return nil, usagef("site %q: %v", s.Name, err)
		}
		p.Targets = append(p.Targets, consent.Target{Site: s.Name, URL: u, Args: args})
	}
	return p, nil
}

// nonEmpty is m, or nil when it is empty.
func nonEmpty(m map[string]string) map[string]string {
	if len(m) == 0 {
		return nil
	}
	return m
}

// loadConfig reads --config, or the nearest .echo/config.yaml Echo
// trusts; none is fine: every default applies.
func (st *setup) loadConfig(flagPath string) error {
	path := flagPath
	if path != "" {
		if _, err := os.Stat(path); err != nil {
			return usagef("--config %s: %v", path, err)
		}
	} else {
		found, err := sendmail.FindConfig(".")
		var ue *sendmail.UntrustedError
		switch {
		case errors.Is(err, sendmail.ErrNoConfig):
		case errors.As(err, &ue):
			st.notes = append(st.notes, fmt.Sprintf("%v (someone else could have put it there); pass --config %s if you trust it", ue, ue.Path))
		case err != nil:
			return err
		default:
			path = found
		}
	}
	st.cfg = config.Default()
	if path == "" {
		return nil
	}
	abs, err := filepath.Abs(path)
	if err != nil {
		return err
	}
	st.cfgPath = abs
	if st.cfg, err = config.Load(abs); err != nil {
		return usagef("%v", err)
	}
	return nil
}

// customTrackers hands the config's own trackers to the classifier,
// which checks their fixtures: one that fails is a usage error, like a
// custom rule's.
func (st *setup) customTrackers(p *consent.Plan, block *config.Consent) error {
	if block.Trackers.Kind == 0 || len(block.Trackers.Content) == 0 {
		return nil
	}
	data, err := yaml.Marshal(&block.Trackers)
	if err != nil {
		return err
	}
	p.ExtraTrackers = data
	if cl := consent.Linked().Classifier; cl != nil {
		if err := cl.AddCustom(data); err != nil {
			return usagef("consent: trackers: %v", err)
		}
	}
	return nil
}

// storeRoot is where results are kept: --store, else the folder the
// config is in, else ./.echo.
func storeRoot(flag, cfgPath string) string {
	switch {
	case flag != "":
		return flag
	case cfgPath != "":
		return filepath.Dir(cfgPath)
	}
	return config.Dir
}

// check runs rhythm echo consent itself.
func (c *cmd) check(pos []string) int {
	st, err := c.prepare(pos)
	if err != nil {
		return fail(c.env, err)
	}
	for _, n := range st.notes {
		fmt.Fprintln(c.env.Stderr, "rhythm echo:", n)
	}
	for _, w := range st.warn {
		fmt.Fprintln(c.env.Stderr, "rhythm echo:", w)
	}
	return c.notBuilt()
}
