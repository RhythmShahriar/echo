// Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
// SPDX-License-Identifier: MIT

package cli

import (
	"maps"
	"regexp"
	"slices"
	"strings"
	"time"

	"github.com/RhythmShahriar/echo/config"
	"github.com/RhythmShahriar/echo/consent"
	"github.com/RhythmShahriar/echo/model"
)

// The defaults of a consent check (PRD 7).
const (
	defaultPages      = 1
	defaultSettle     = 3 * time.Second
	defaultBannerWait = 10 * time.Second
	defaultTimeout    = 90 * time.Second
	defaultRetries    = 1
	defaultParallel   = 3
)

// defaultPlan is a plan with every default of PRD 7 and no targets.
// Locale and Timezone stay "" for the machine's own: the browser fills
// them, after --geo, which an explicit --locale or --timezone beats.
func defaultPlan(version string) *consent.Plan {
	return &consent.Plan{
		Runs: slices.Clone(consent.DefaultRuns), Pages: defaultPages, Settle: defaultSettle, BannerWait: defaultBannerWait,
		Timeout: defaultTimeout, Retries: defaultRetries, Parallel: defaultParallel, CMP: "auto", Click: consent.ClickButton,
		Policy: consent.PolicyAdvanced, Hold: true, Viewport: "desktop", Screenshots: true, Limits: consent.DefaultLimits,
		Version: version,
	}
}

// applyFlags sets on p every flag of o that was given; flags beat the
// config, which beats the defaults.
func applyFlags(p *consent.Plan, o *options) error {
	set := o.set
	var err error
	if set["runs"] {
		if p.Runs, err = consent.ParseRuns(o.runs); err != nil {
			return usagef("%v", err)
		}
	}
	if set["pages"] {
		p.Pages = o.pages
	}
	if set["settle"] {
		p.Settle = o.settle
	}
	if set["banner-wait"] {
		p.BannerWait = o.bannerWait
	}
	if set["timeout"] {
		p.Timeout = o.timeout
	}
	if set["retries"] {
		p.Retries = o.retries
	}
	if set["parallel"] {
		p.Parallel = o.parallel
	}
	if set["cmp"] {
		if p.CMP, err = parseCMP("--cmp", o.cmp); err != nil {
			return err
		}
	}
	if set["accept-selector"] {
		if p.AcceptSelector, err = parseSelector("--accept-selector", o.acceptSel); err != nil {
			return err
		}
	}
	if set["reject-selector"] {
		if p.RejectSelector, err = parseSelector("--reject-selector", o.rejectSel); err != nil {
			return err
		}
	}
	if set["click"] {
		if p.Click, err = consent.ParseClickMode(o.click); err != nil {
			return usagef("%v", err)
		}
	}
	if set["consent-mode"] {
		if p.Policy, err = consent.ParsePolicy(o.consentMode); err != nil {
			return usagef("%v", err)
		}
	}
	for _, e := range o.exempt {
		p.Exempt = appendNew(p.Exempt, strings.TrimSpace(e))
	}
	if set["geo"] {
		if p.Geo, err = parseGeo("--geo", o.geo); err != nil {
			return err
		}
	}
	if set["locale"] {
		if p.Locale, err = parseLocale("--locale", o.locale); err != nil {
			return err
		}
	}
	if set["timezone"] {
		if p.Timezone, err = parseTimezone("--timezone", o.timezone); err != nil {
			return err
		}
	}
	if set["viewport"] {
		if p.Viewport, err = parseViewport("--viewport", o.viewport); err != nil {
			return err
		}
	}
	if set["send-hits"] {
		p.Hold = !o.sendHits
	}
	if set["map-only"] {
		p.MapOnly = o.mapOnly
	}
	if set["user-agent"] {
		p.UserAgent = o.userAgent
	}
	if set["proxy"] {
		p.Proxy = o.proxy
	}
	p.Insecure = p.Insecure || o.insecure
	p.InsecurePublic = p.InsecurePublic || o.insecurePublic
	p.FollowHost = p.FollowHost || o.followHost
	if set["browser"] {
		p.Browser.Bin = o.browser
	}
	p.Browser.NoDownload = p.Browser.NoDownload || o.noDownload
	p.Browser.NoSandbox = p.Browser.NoSandbox || o.noSandbox
	p.Browser.Visible = p.Browser.Visible || o.showBrowser
	if o.noScreenshots {
		p.Screenshots = false
	}
	p.HAR = p.HAR || o.har
	p.ShowIDs = p.ShowIDs || o.showIDs
	return nil
}

// appendNew appends v to list unless it is empty or already there.
func appendNew(list []string, v string) []string {
	if v == "" || slices.Contains(list, v) {
		return list
	}
	return append(list, v)
}

// parseCMP reads --cmp (or consent: cmp, named by what): auto or a
// rule id.
func parseCMP(what, v string) (string, error) {
	id := strings.ToLower(strings.TrimSpace(v))
	if id == "" {
		return "auto", nil
	}
	if slices.Contains(cmpNames, id) {
		return id, nil
	}
	msg := what + " %q: want auto or one of " + strings.Join(cmpNames[1:], ", ")
	if s := closestWord(id, cmpNames); s != "" {
		return "", usagef(msg+"; did you mean %s?", v, s)
	}
	return "", usagef(msg, v)
}

// parseSelector reads an Accept or Reject selector, naming what in its
// error.
func parseSelector(what, v string) ([]consent.Locator, error) {
	l, err := consent.ParseSelector(v)
	if err != nil {
		return nil, usagef("%s: %v", what, err)
	}
	return l, nil
}

var (
	countryCode = regexp.MustCompile(`^[A-Za-z]{2}$`)
	localeTag   = regexp.MustCompile(`^[A-Za-z]{2,3}(-[A-Za-z0-9]{2,8})*$`)
	zoneName    = regexp.MustCompile(`^(UTC|[A-Za-z][A-Za-z0-9_+-]*(/[A-Za-z0-9_+-]+)+)$`)
)

// parseGeo reads a country code (ISO 3166-1 alpha-2), lower case.
func parseGeo(what, v string) (string, error) {
	g := strings.TrimSpace(v)
	if !countryCode.MatchString(g) {
		return "", usagef("%s %q: want a two-letter country code (ISO 3166-1 alpha-2), e.g. de", what, v)
	}
	return strings.ToLower(g), nil
}

// parseLocale reads a BCP 47 language tag such as de-DE.
func parseLocale(what, v string) (string, error) {
	l := strings.ReplaceAll(strings.TrimSpace(v), "_", "-")
	if !localeTag.MatchString(l) {
		return "", usagef("%s %q: want a language tag like de-DE or en-US", what, v)
	}
	return l, nil
}

// parseTimezone reads an IANA time zone name such as Europe/Berlin. The
// browser knows the zones; this only keeps out what can't be one.
func parseTimezone(what, v string) (string, error) {
	z := strings.TrimSpace(v)
	if !zoneName.MatchString(z) || len(z) > 64 {
		return "", usagef("%s %q: want an IANA time zone like Europe/Berlin or UTC", what, v)
	}
	return z, nil
}

// parseViewport reads desktop or mobile.
func parseViewport(what, v string) (string, error) {
	switch w := strings.ToLower(strings.TrimSpace(v)); w {
	case "desktop", "mobile":
		return w, nil
	}
	return "", usagef("%s %q: want desktop or mobile", what, v)
}

// closestWord is the word of options a mistyped one most likely meant,
// or "".
func closestWord(typed string, options []string) string {
	best, dist := "", 3
	for _, o := range options {
		if d := levenshtein(typed, o); d < dist && d < len(typed) {
			best, dist = o, d
		}
	}
	return best
}

// levenshtein is the edit distance between a and b, by runes.
func levenshtein(a, b string) int {
	ra, rb := []rune(a), []rune(b)
	prev := make([]int, len(rb)+1)
	cur := make([]int, len(rb)+1)
	for j := range prev {
		prev[j] = j
	}
	for i := 1; i <= len(ra); i++ {
		cur[0] = i
		for j := 1; j <= len(rb); j++ {
			cost := 1
			if ra[i-1] == rb[j-1] {
				cost = 0
			}
			cur[j] = min(prev[j]+1, cur[j-1]+1, prev[j-1]+cost)
		}
		prev, cur = cur, prev
	}
	return prev[len(rb)]
}

// applyConfig sets on p what the consent: block of the config says;
// flags then win over it. The config is already validated, so an error
// here only repeats what validation would say.
func applyConfig(p *consent.Plan, c *config.Consent) error {
	if c == nil {
		return nil
	}
	var err error
	if len(c.Runs) > 0 {
		if p.Runs, err = consent.ParseRuns(strings.Join(c.Runs, ",")); err != nil {
			return usagef("consent: runs %s", strings.TrimPrefix(err.Error(), "--runs "))
		}
	}
	if c.Pages != nil {
		p.Pages = *c.Pages
	}
	if c.Settle != nil {
		p.Settle = c.Settle.Duration
	}
	if c.BannerWait != nil {
		p.BannerWait = c.BannerWait.Duration
	}
	if c.Timeout != nil {
		p.Timeout = c.Timeout.Duration
	}
	if c.Retries != nil {
		p.Retries = *c.Retries
	}
	if c.Parallel != 0 {
		p.Parallel = c.Parallel
	}
	if c.CMP != "" {
		if p.CMP, err = parseCMP("consent: cmp", c.CMP); err != nil {
			return err
		}
	}
	if c.Click != "" {
		if p.Click, err = consent.ParseClickMode(c.Click); err != nil {
			return usagef("consent: click %s", strings.TrimPrefix(err.Error(), "--click "))
		}
	}
	if c.ConsentMode != "" {
		if p.Policy, err = consent.ParsePolicy(c.ConsentMode); err != nil {
			return usagef("consent: consent_mode %s", strings.TrimPrefix(err.Error(), "--consent-mode "))
		}
	}
	if c.AcceptSelector != "" {
		if p.AcceptSelector, err = parseSelector("consent: accept_selector", c.AcceptSelector); err != nil {
			return err
		}
	}
	if c.RejectSelector != "" {
		if p.RejectSelector, err = parseSelector("consent: reject_selector", c.RejectSelector); err != nil {
			return err
		}
	}
	if c.Geo != "" {
		if p.Geo, err = parseGeo("consent: geo", c.Geo); err != nil {
			return err
		}
	}
	if c.Locale != "" {
		if p.Locale, err = parseLocale("consent: locale", c.Locale); err != nil {
			return err
		}
	}
	if c.Timezone != "" {
		if p.Timezone, err = parseTimezone("consent: timezone", c.Timezone); err != nil {
			return err
		}
	}
	if c.Viewport != "" {
		if p.Viewport, err = parseViewport("consent: viewport", c.Viewport); err != nil {
			return err
		}
	}
	for _, e := range c.Exempt {
		p.Exempt = appendNew(p.Exempt, strings.TrimSpace(e))
	}
	p.Ignore = slices.Clone(c.Ignore)
	return nil
}

// checkLevels are the checks: levels of consent check IDs, which the
// verdict applies ("off" removes the check).
func checkLevels(cfg config.Config) map[string]model.Level {
	var out map[string]model.Level
	for id, l := range cfg.Checks {
		if !strings.HasPrefix(id, "consent/") {
			continue
		}
		if out == nil {
			out = map[string]model.Level{}
		}
		out[id] = model.Level(l)
	}
	return out
}

// clonePlan copies p deeply enough that a site's own flags can change
// the copy: the slices and maps a plan's fields hold.
func clonePlan(p *consent.Plan) *consent.Plan {
	q := *p
	q.Targets = slices.Clone(p.Targets)
	q.Runs = slices.Clone(p.Runs)
	q.AcceptSelector = slices.Clone(p.AcceptSelector)
	q.RejectSelector = slices.Clone(p.RejectSelector)
	q.Exempt = slices.Clone(p.Exempt)
	q.MapHosts = maps.Clone(p.MapHosts)
	q.Headers = maps.Clone(p.Headers)
	q.Cookies = slices.Clone(p.Cookies)
	q.Ignore = slices.Clone(p.Ignore)
	q.CheckLevels = maps.Clone(p.CheckLevels)
	return &q
}
