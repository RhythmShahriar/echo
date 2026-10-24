// Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
// SPDX-License-Identifier: MIT

//go:build testbed

package main

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/RhythmShahriar/echo/model"
)

// stagingShield is the Staging Shield column: the free WordPress plugin
// from wordpress.org (make up TOOLS=staging-shield bakes it into the
// WordPress image, inactive). It detects a copy of a site and stops its
// email there. It keeps no record of what a site sends and nothing to
// compare with, so the question for it is only the staging one: with the
// plugin active, does the email to a real customer still leave?
type stagingShield struct {
	testbed string
	mail    mailpit
	work    string
	// run runs a command in dir: WP-CLI through bin/wp.
	run func(ctx context.Context, dir string, args ...string) (int, string)
	// start starts the benchmark's Echo in a mode; action runs one of
	// the testbed's actions through it, in dir.
	start  func(ctx context.Context, mode string) error
	action func(ctx context.Context, dir, action string) error
}

// stagingShieldVersion is the plugin version the benchmark is run with:
// STAGING_SHIELD in testbed/Makefile.
const stagingShieldVersion = "1.0.2"

func (stagingShield) Name() string  { return "staging-shield" }
func (stagingShield) Title() string { return "Staging Shield" }

func (stagingShield) About() string {
	return "**Staging Shield** (" + stagingShieldVersion + ", free, from wordpress.org) is a WordPress plugin that detects a staging copy and stops its email. " +
		"It keeps no record of what a site sends and no baseline, so a lost, changed or new message is **can't see** for it. " +
		"For the staging scenarios the benchmark activates it in the testbed's WordPress, runs the scenario's action in its broken state with Echo observing " +
		"(so mail really goes out, to the testbed's Mailpit) and counts it **caught** when the email to the real address did not leave. " +
		"A control run first, with the plugin inactive, must deliver that email to Mailpit, or the scenario is an error rather than a catch."
}

// wp runs WP-CLI in the testbed's WordPress.
func (t stagingShield) wp(ctx context.Context, args ...string) (int, string) {
	return t.run(ctx, t.testbed, append([]string{filepath.Join(t.testbed, "bin", "wp")}, args...)...)
}

// Ready reports whether the plugin is installed in the testbed.
func (t stagingShield) Ready(ctx context.Context) error {
	code, out := t.wp(ctx, "plugin", "get", "staging-shield", "--field=version")
	if code != 0 {
		return fmt.Errorf("staging-shield isn't installed in the testbed's WordPress (make -C testbed up TOOLS=staging-shield): %s", strings.TrimSpace(out))
	}
	if v := strings.TrimSpace(out); v != stagingShieldVersion {
		return fmt.Errorf("staging-shield %s is installed, the benchmark is run with %s (make -C testbed up TOOLS=staging-shield)", v, stagingShieldVersion)
	}
	return nil
}

// Judge decides the Staging Shield column for one scenario.
func (t stagingShield) Judge(ctx context.Context, s Scenario, _, _ *model.Run, _ string) Result {
	if s.Area != "staging" || s.Mailpit == "" {
		return Result{Outcome: CantSee, Detail: "Staging Shield stops email on a staging copy; it doesn't capture, show or compare what a site sends"}
	}
	dir := filepath.Join(t.work, s.ID, "staging-shield")
	if err := os.MkdirAll(dir, 0o750); err != nil {
		return Result{Outcome: Failed, Detail: err.Error()}
	}
	if err := Toggle(t.testbed, s.ID, "broken"); err != nil {
		return Result{Outcome: Failed, Detail: err.Error()}
	}
	defer func() { _ = Toggle(t.testbed, s.ID, "good") }()
	if err := t.start(ctx, s.Mode); err != nil {
		return Result{Outcome: Failed, Detail: "starting Echo: " + err.Error()}
	}
	// The positive control: with the plugin inactive the email must reach
	// Mailpit, or a later "did not leave" would say nothing about the
	// plugin.
	if code, out := t.wp(ctx, "plugin", "deactivate", "staging-shield"); code != 0 && !strings.Contains(out, "already deactivated") {
		return Result{Outcome: Failed, Detail: "deactivating it for the control run: " + strings.TrimSpace(out)}
	}
	reached, err := t.delivered(ctx, filepath.Join(dir, "control"), s)
	if err != nil {
		return Result{Outcome: Failed, Detail: "control run: " + err.Error()}
	}
	if !reached {
		return Result{Outcome: Failed, Detail: fmt.Sprintf("with the plugin inactive, the email to %s didn't reach the testbed's mail server either, so a block can't be credited to it", s.Mailpit)}
	}
	if code, out := t.wp(ctx, "plugin", "activate", "staging-shield"); code != 0 {
		return Result{Outcome: Failed, Detail: "activating it: " + strings.TrimSpace(out)}
	}
	defer func() { _, _ = t.wp(context.WithoutCancel(ctx), "plugin", "deactivate", "staging-shield") }()
	if reached, err = t.delivered(ctx, filepath.Join(dir, "active"), s); err != nil {
		return Result{Outcome: Failed, Detail: err.Error()}
	}
	if reached {
		return Result{Outcome: Missed, Detail: fmt.Sprintf("with the plugin active, the email to %s still reached the testbed's mail server", s.Mailpit)}
	}
	return Result{Outcome: Caught, Found: fmt.Sprintf("with the plugin active, the email to %s did not leave (with it inactive, it did)", s.Mailpit)}
}

// delivered runs the scenario's action in dir and reports whether a new
// email to the scenario's address reached Mailpit.
func (t stagingShield) delivered(ctx context.Context, dir string, s Scenario) (bool, error) {
	if err := os.MkdirAll(dir, 0o750); err != nil {
		return false, err
	}
	before, err := t.sentTo(ctx, s.Mailpit)
	if err != nil {
		return false, err
	}
	if err := t.action(ctx, dir, s.Action); err != nil {
		return false, err
	}
	after, err := t.sentTo(ctx, s.Mailpit)
	if err != nil {
		return false, err
	}
	return after > before, nil
}

// sentTo counts the messages Mailpit holds for address.
func (t stagingShield) sentTo(ctx context.Context, address string) (int, error) {
	time.Sleep(300 * time.Millisecond) // Mailpit stores asynchronously
	var list struct {
		Total *int `json:"messages_count"`
	}
	if err := getJSON(ctx, t.mail.api+"/api/v1/search?limit=1&query="+url.QueryEscape(`to:"`+address+`"`), &list); err != nil {
		return 0, err
	}
	if list.Total == nil {
		return 0, errors.New("mailpit's search answer has no messages_count")
	}
	return *list.Total, nil
}
