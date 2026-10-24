// Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
// SPDX-License-Identifier: MIT

package main

import (
	"bufio"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/RhythmShahriar/echo/config"
)

// The stacks init and doctor tell apart, as detectStack names them.
// "" is a folder none of them matches.
const (
	stackWordPress = "wordpress"
	stackLaravel   = "laravel"
	stackNext      = "next"
	stackNode      = "node"
	stackPHP       = "php"
)

// stackNames are the values init --stack takes, aliases included.
var stackNames = map[string]string{
	"wordpress": stackWordPress,
	"wp":        stackWordPress,
	"laravel":   stackLaravel,
	"next":      stackNext,
	"nextjs":    stackNext,
	"node":      stackNode,
	"php":       stackPHP,
	"generic":   stackPHP,
}

// detectStack names the project in dir by its files: wordpress
// (wp-config.php, which WordPress also reads from the folder above),
// laravel (artisan), next (next.config.*), node (package.json), php (any
// .php file) or "" for none of them. The first match wins, so a Laravel
// app with a package.json for its assets is laravel.
func detectStack(dir string) string {
	has := func(pattern string) bool {
		m, _ := filepath.Glob(filepath.Join(dir, pattern))
		return len(m) > 0
	}
	switch {
	case has("wp-config.php") || has("wp-config-sample.php") || has("wp-load.php"):
		return stackWordPress
	case has("artisan"):
		return stackLaravel
	case has("next.config.*"):
		return stackNext
	case has("package.json"):
		return stackNode
	case has("*.php"):
		return stackPHP
	}
	return ""
}

// stackLabel is a stack's name in sentences.
func stackLabel(stack string) string {
	switch stack {
	case stackWordPress:
		return "WordPress"
	case stackLaravel:
		return "Laravel"
	case stackNext:
		return "Next.js"
	case stackNode:
		return "Node"
	case stackPHP:
		return "PHP"
	}
	return "this"
}

// starter is the action init writes for a stack, with the commented
// examples that follow it and the site: it can guess.
type starter struct {
	name     string // the action's name, for init's message
	site     string // site: to write, "" to leave it for the user
	actions  string // the actions: block, uncommented first action included
	needSite bool   // the action is an http request, so site: must be set
}

// actionsHeader introduces the actions in every config init writes.
const actionsHeader = `# What snapshot and compare run. Each action makes the site send something.
# Form submits and multi-step journeys are better run by Soundcheck with --echo.
`

// starterFor is what init writes for stack in dir.
func starterFor(stack, dir string) starter {
	switch stack {
	case stackWordPress:
		return starter{name: "Lost password", needSite: true, site: wpHome(dir), actions: `actions:
  - name: Lost password
    # WordPress emails a reset link. Change admin to a user on this site.
    http:
      method: POST
      url: /wp-login.php?action=lostpassword
      form: {user_login: admin}
    expect: {email: 1}
#  - name: Completed order
#    command: wp eval 'do_action("woocommerce_order_status_completed", 123);'
#  - name: Stripe payment succeeded
#    replay: {event: payment_intent.succeeded, to: /?wc-api=wc_stripe, secret_env: ECHO_STRIPE_WEBHOOK_SECRET}
`}
	case stackLaravel:
		return starter{name: "Connector canary", site: envValue(filepath.Join(dir, ".env"), "APP_URL"), actions: `actions:
  - name: Connector canary
    # The connector's own test: one email and one Http call, both caught.
    # Replace it with the artisan commands or routes that send your mail.
    command: php artisan echo:canary
    expect: {email: 1}
#  - name: Daily digest
#    command: php artisan schedule:test --name=digest
#  - name: Stripe payment succeeded
#    replay: {event: payment_intent.succeeded, to: /stripe/webhook, secret_env: ECHO_STRIPE_WEBHOOK_SECRET}
`}
	case stackNext, stackNode:
		return starter{name: "Sign-up", needSite: true, site: "http://localhost:3000", actions: `actions:
  - name: Sign-up
    # A placeholder: point it at a route of yours that sends an email or
    # calls an API (Resend, Twilio, Slack).
    http: {method: POST, url: /api/signup, json: {email: "${fake:email}", name: "${fake:name}"}}
#  - name: Nightly job
#    command: node scripts/digest.js
#  - name: Stripe payment succeeded
#    replay: {event: payment_intent.succeeded, to: /api/stripe/webhook, secret_env: ECHO_STRIPE_WEBHOOK_SECRET}
`}
	}
	return starter{name: "Home page", needSite: true, actions: `actions:
  - name: Home page
    # Works on any site, but sends little. A form's POST or a sign-up is
    # what to check: add those and remove this one.
    http: {method: GET, url: /}
#  - name: Contact form
#    http: {method: POST, url: /contact.php, form: {email: "${fake:email}", message: Hello}}
#  - name: Cron job
#    command: php cron.php
`}
}

// templateSite is the site: line of config.Template, value and padding.
var templateSite = regexp.MustCompile(`(?m)^site: ""( +)#`)

// initConfig is the config init writes for stack: config.Template with
// a working first action in place of its examples, and site: filled in
// when it can be found.
func initConfig(st starter) string {
	t := config.Template
	if i := strings.Index(t, actionsHeader); i >= 0 {
		t = t[:i]
	}
	if st.site != "" {
		value := fmt.Sprintf("%q", st.site)
		t = templateSite.ReplaceAllStringFunc(t, func(m string) string {
			pad := max(len(templateSite.FindStringSubmatch(m)[1])+2-len(value), 1)
			return "site: " + value + strings.Repeat(" ", pad) + "#"
		})
	}
	return t + actionsHeader + st.actions
}

var wpHomeDefine = regexp.MustCompile(`(?m)^\s*define\s*\(\s*['"](?:WP_HOME|WP_SITEURL)['"]\s*,\s*['"](https?://[^'"]+)['"]\s*\)`)

// wpHome is the site URL wp-config.php defines (WP_HOME or WP_SITEURL),
// or "" when it doesn't: WordPress then keeps it in the database.
func wpHome(dir string) string {
	for _, p := range []string{filepath.Join(dir, "wp-config.php"), filepath.Join(dir, "..", "wp-config.php")} {
		if b, err := os.ReadFile(p); err == nil { //nolint:gosec // the user's own site
			if m := wpHomeDefine.FindSubmatch(b); m != nil {
				return strings.TrimRight(string(m[1]), "/")
			}
		}
	}
	return ""
}

// envValue reads one KEY=value from a .env file, quotes removed; "" when
// it's not there.
func envValue(path, key string) string {
	f, err := os.Open(path) //nolint:gosec // the user's own project file
	if err != nil {
		return ""
	}
	defer func() { _ = f.Close() }()
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		k, v, ok := strings.Cut(strings.TrimSpace(sc.Text()), "=")
		if ok && strings.TrimSpace(k) == key {
			return strings.TrimRight(strings.Trim(strings.TrimSpace(v), `"'`), "/")
		}
	}
	return ""
}
