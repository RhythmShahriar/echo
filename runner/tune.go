// Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
// SPDX-License-Identifier: MIT

package runner

import (
	"fmt"

	"github.com/RhythmShahriar/echo/check"
	"github.com/RhythmShahriar/echo/compare"
	"github.com/RhythmShahriar/echo/config"
	"github.com/RhythmShahriar/echo/model"
)

// Customs returns the config's rules: as the check package's custom
// rules.
func Customs(cfg config.Config) []check.Custom {
	out := make([]check.Custom, 0, len(cfg.Rules))
	for _, r := range cfg.Rules {
		c := check.Custom{ID: r.ID, Channels: r.Channels, Match: r.Match, Regex: r.Regex,
			Level: model.Level(r.Level), Message: r.Message}
		if r.Fixtures != nil {
			c.Fixtures = check.Fixtures{Match: r.Fixtures.Match, NoMatch: r.Fixtures.NoMatch}
		}
		out = append(out, c)
	}
	return out
}

// Levels returns the config's checks: as the plain map check.Options and
// compare.Options take.
func Levels(cfg config.Config) map[string]string {
	if len(cfg.Checks) == 0 {
		return nil
	}
	out := make(map[string]string, len(cfg.Checks))
	for id, l := range cfg.Checks {
		out[id] = string(l)
	}
	return out
}

// CheckOptions are what cfg expects of every message: links to its site
// are fine, allow_recipients may really be messaged, its rules: add
// checks and its checks: set their levels. The runner, the catcher and
// watch all check messages with these, so they report the same issues.
func CheckOptions(cfg config.Config) (check.Options, error) {
	rules, err := check.CustomRules(Customs(cfg))
	if err != nil {
		return check.Options{}, fmt.Errorf("rules: %w", err)
	}
	return check.Options{SiteHost: host(cfg.Site), AllowRecipients: cfg.AllowRecipients, Rules: rules, Checks: Levels(cfg)}, nil
}

// CompareOptions are how compare counts changes for cfg: its checks:
// levels and each action's ignore: patterns, by the action's ID.
func CompareOptions(cfg config.Config) compare.Options {
	o := compare.Options{Checks: Levels(cfg)}
	for _, a := range cfg.Actions {
		if len(a.Ignore) == 0 {
			continue
		}
		if o.Ignore == nil {
			o.Ignore = map[string][]string{}
		}
		o.Ignore[ActionID(a.Name)] = a.Ignore
	}
	return o
}
