// Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
// SPDX-License-Identifier: MIT

package runner

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"slices"
	"strings"
	"testing"
	"time"

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

// TestErrorsHideTheTargetsSecrets: an action's URL may hold a key from
// ${env:...}; the errors that name the URL (in the terminal, run.json
// and the report) show it masked.
func TestErrorsHideTheTargetsSecrets(t *testing.T) {
	t.Setenv("ECHO_TEST_API_KEY", "supersecretvalue123")
	a := config.Action{Name: "x", HTTP: &config.HTTPAction{URL: "http://127.0.0.1:21349/x?api_key=${env:ECHO_TEST_API_KEY}&page=2"}}
	o := Options{Config: config.Default(), HTTP: &http.Client{Timeout: time.Second}}
	err := doHTTP(context.Background(), o, a, "run/x", newFake())
	if err == nil || strings.Contains(err.Error(), "supersecretvalue123") || !strings.Contains(err.Error(), "page=2") {
		t.Errorf("a failed request: %v", err)
	}
	cfg := config.Default()
	cfg.Production = []string{"127.0.0.1:21349"}
	err = refuse(cfg, a, "seal", newFake())
	if err == nil || strings.Contains(err.Error(), "supersecretvalue123") || !strings.Contains(err.Error(), "refusing to run against http://127.0.0.1:21349/x?api_key=") {
		t.Errorf("the production refusal: %v", err)
	}
	if got := hideURL(errors.New("GET http://a.test/?token=abc123def456 failed"), "http://a.test/?token=abc123def456"); strings.Contains(got.Error(), "abc123def456") {
		t.Errorf("a plain error: %v", got)
	}
	wrapped := hideURL(fmt.Errorf("x: %w", context.Canceled), "http://a.test/")
	if !errors.Is(wrapped, context.Canceled) {
		t.Errorf("the masked error lost what it wraps: %v", wrapped)
	}
}
