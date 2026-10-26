// Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
// SPDX-License-Identifier: MIT

package main

import (
	"fmt"
	"slices"

	"github.com/RhythmShahriar/echo/check"
	"github.com/RhythmShahriar/echo/consent"
	"github.com/RhythmShahriar/echo/model"
	"github.com/RhythmShahriar/echo/runner"
)

// cmdChecks lists every check, the config's own rules: after the
// built-in ones; with --test it tries each rule on its fixtures and exits
// 1 when one fails. A failing fixture stops every other command with exit
// 2 (config.ErrFixtures), so checks reads past it: plain checks lists the
// rules, says which one fails and exits 1 too.
func cmdChecks(s *session) int {
	if len(s.args) > 0 {
		return s.fail(usageErr("checks takes no arguments; --test tries the config's rules"))
	}
	if s.o.test {
		ok, err := check.WriteFixtures(s.stdout, runner.Customs(s.cfg))
		if err != nil {
			return s.fail(err)
		}
		if !ok {
			return exitFindings
		}
		return exitOK
	}
	if err := check.WriteRules(s.stdout, slices.Concat(check.Rules(), consentRules(), s.checks.Rules)); err != nil {
		return s.fail(err)
	}
	if len(s.cfg.Checks) > 0 && !s.o.quiet {
		fmt.Fprintf(s.stdout, "\n%s sets the level of %s under checks:.\n", s.configName(), plural(len(s.cfg.Checks), "check"))
	}
	if s.fixtures != nil {
		fmt.Fprintf(s.stderr, "rhythm echo: %v\n  rhythm echo checks --test shows every fixture; other commands refuse the config until it passes\n", s.fixtures)
		return exitFindings
	}
	return exitOK
}

// consentRules lists the consent checks for the checks table. They look
// at pages, not messages, so they are listed only: never registered and
// with no Run. Their channel reads "consent".
func consentRules() []check.Rule {
	out := make([]check.Rule, 0, len(consent.Checks))
	for _, c := range consent.Checks {
		out = append(out, check.Rule{ID: c.ID, Level: c.Level, Channels: []model.Channel{"consent"}, Doc: c.Doc})
	}
	return out
}
