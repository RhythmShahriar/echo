// Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
// SPDX-License-Identifier: MIT

package runner

import (
	"slices"
	"strings"
	"testing"

	"github.com/RhythmShahriar/echo/check"
	"github.com/RhythmShahriar/echo/config"
	"github.com/RhythmShahriar/echo/model"
)

func TestCheckOptionsCarryRulesAndLevels(t *testing.T) {
	cfg := config.Default()
	cfg.Site = "https://shop.test"
	cfg.Rules = []config.Rule{{ID: "brand/secret", Match: "SECRET-PROJECT", Level: config.Error}}
	cfg.Checks = map[string]config.Level{"content/placeholder": config.Off, "content/php-error": config.Notice}
	o, err := CheckOptions(cfg)
	if err != nil {
		t.Fatal(err)
	}
	m := model.Message{ID: "m1", Channel: model.Email, Subject: "SECRET-PROJECT", Text: "Hi {first_name}\nWarning: Undefined variable $x in /var/www/a.php on line 3"}
	got := map[string]model.Level{}
	for _, i := range check.Message(m, o) {
		got[i.Check] = i.Level
	}
	if got["brand/secret"] != model.Error {
		t.Errorf("the rule: %v", got)
	}
	if _, on := got["content/placeholder"]; on {
		t.Errorf("checks: off: %v", got)
	}
	if got["content/php-error"] != model.Notice {
		t.Errorf("checks: notice: %v", got)
	}

	cfg.Rules = []config.Rule{{ID: "a", Regex: "x"}, {ID: "a", Regex: "y"}}
	if _, err := CheckOptions(cfg); err == nil || !strings.HasPrefix(err.Error(), "rules: ") {
		t.Errorf("two rules with one ID: %v", err)
	}
}

func TestCompareOptionsMapIgnoreByActionID(t *testing.T) {
	cfg := config.Default()
	cfg.Checks = map[string]config.Level{"message/text": config.Off}
	cfg.Actions = []config.Action{{Name: "Contact form", Command: "x", Ignore: []string{"*@agency.test"}}, {Name: "Other", Command: "y"}}
	o := CompareOptions(cfg)
	if o.Checks["message/text"] != "off" {
		t.Errorf("checks: %v", o.Checks)
	}
	if !slices.Equal(o.Ignore["action:contact-form"], []string{"*@agency.test"}) || len(o.Ignore) != 1 {
		t.Errorf("ignore: %v", o.Ignore)
	}
}
