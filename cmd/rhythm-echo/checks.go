// Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
// SPDX-License-Identifier: MIT

package main

import (
	"fmt"
	"io"
	"slices"

	"gopkg.in/yaml.v3"

	"github.com/RhythmShahriar/echo/check"
	"github.com/RhythmShahriar/echo/config"
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
		consentOK, err := consentFixtures(s.stdout, s.stderr, s.cfg)
		if err != nil {
			return s.fail(err)
		}
		if !ok || !consentOK {
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

// fixtureReporter is the optional method of the consent tracker
// catalog that tries the config's own trackers on their fixtures: one
// line per fixture in the format of the rules' table; ok when all
// pass.
type fixtureReporter interface {
	FixtureReport(yaml []byte) (lines []string, ok bool)
}

// consentFixtures tries the fixtures of the config's consent trackers
// (consent: trackers:) and prints their lines; ok is false when one
// fails. A config without them prints nothing.
func consentFixtures(stdout, stderr io.Writer, cfg config.Config) (ok bool, err error) {
	if cfg.Consent == nil || cfg.Consent.Trackers.Kind == 0 || len(cfg.Consent.Trackers.Content) == 0 {
		return true, nil
	}
	data, err := yaml.Marshal(&cfg.Consent.Trackers)
	if err != nil {
		return false, err
	}
	fr, can := consent.Linked().Classifier.(fixtureReporter)
	if !can {
		fmt.Fprintln(stderr, "rhythm echo: consent: trackers: this build can't try their fixtures")
		return true, nil
	}
	lines, ok := fr.FixtureReport(data)
	for _, l := range lines {
		if _, err := fmt.Fprintln(stdout, l); err != nil {
			return false, err
		}
	}
	return ok, nil
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
