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

func (stagingShield) Name() string  { return "staging-shield" }
func (stagingShield) Title() string { return "Staging Shield" }

func (stagingShield) About() string {
	return "**Staging Shield** (free, from wordpress.org; its version is in the run's log) is a WordPress plugin that detects a staging copy and stops its email. " +
		"It keeps no record of what a site sends and no baseline, so a lost, changed or new message is **can't see** for it. " +
		"For the staging scenarios the benchmark activates it in the testbed's WordPress, runs the scenario's action in its broken state with Echo observing " +
		"(so mail really goes out, to the testbed's Mailpit) and counts it **caught** when the email to the real address did not leave."
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
	fmt.Fprintf(os.Stderr, "staging-shield %s\n", strings.TrimSpace(out))
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
	if code, out := t.wp(ctx, "plugin", "activate", "staging-shield"); code != 0 {
		return Result{Outcome: Failed, Detail: "activating it: " + strings.TrimSpace(out)}
	}
	defer func() { _, _ = t.wp(context.WithoutCancel(ctx), "plugin", "deactivate", "staging-shield") }()
	before, err := t.sentTo(ctx, s.Mailpit)
	if err != nil {
		return Result{Outcome: Failed, Detail: err.Error()}
	}
	if err := t.action(ctx, dir, s.Action); err != nil {
		return Result{Outcome: Failed, Detail: err.Error()}
	}
	after, err := t.sentTo(ctx, s.Mailpit)
	if err != nil {
		return Result{Outcome: Failed, Detail: err.Error()}
	}
	if after > before {
		return Result{Outcome: Missed, Detail: fmt.Sprintf("with the plugin active, the email to %s still reached the testbed's mail server", s.Mailpit)}
	}
	return Result{Outcome: Caught, Found: fmt.Sprintf("with the plugin active, the email to %s did not leave", s.Mailpit)}
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
