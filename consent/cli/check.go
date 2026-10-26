// Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
// SPDX-License-Identifier: MIT

package cli

import (
	"net/url"

	"github.com/RhythmShahriar/echo/consent"
)

// msgNoPage is PRD 10.2 message 1: no URL, no --sites and no
// consent.urls in the config.
const msgNoPage = "consent needs a page: rhythm echo consent https://staging.shop.test, or list pages under consent: urls in .echo/config.yaml"

// setup is a consent check ready to run: its plan, the reports to write
// and the --fail-on level.
type setup struct {
	plan    *consent.Plan
	outs    []output
	failOn  string
	targets []*url.URL
}

// prepare reads the check's command line: the pages, the reports and
// --fail-on, then the plan, which it validates.
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
	failOn, err := checkFailOn(o.failOn, o.against != "")
	if err != nil {
		return nil, err
	}
	st := &setup{outs: outs, failOn: failOn, plan: defaultPlan(c.env.Version)}
	for _, raw := range pos {
		u, err := target(raw, "")
		if err != nil {
			return nil, err
		}
		st.targets = append(st.targets, u)
		st.plan.Targets = append(st.plan.Targets, consent.Target{URL: u})
	}
	if len(st.targets) == 0 {
		return nil, usagef("%s", msgNoPage)
	}
	if err := applyFlags(st.plan, o); err != nil {
		return nil, err
	}
	if err := st.plan.Validate(); err != nil {
		return nil, usagef("%v", err)
	}
	return st, nil
}

// check runs rhythm echo consent itself.
func (c *cmd) check(pos []string) int {
	if _, err := c.prepare(pos); err != nil {
		return fail(c.env, err)
	}
	return c.notBuilt()
}
