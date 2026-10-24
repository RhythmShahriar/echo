// Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
// SPDX-License-Identifier: MIT

package config

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"gopkg.in/yaml.v3"
)

func write(t *testing.T, s string) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), "config.yaml")
	if err := os.WriteFile(p, []byte(s), 0o600); err != nil {
		t.Fatal(err)
	}
	return p
}

func TestTemplateLoadsAsDefaults(t *testing.T) {
	c, err := Load(write(t, Template))
	if err != nil {
		t.Fatal(err)
	}
	d := Default()
	if c.Mode != d.Mode || c.Listen != d.Listen || c.SMTP != d.SMTP || c.Quiet != d.Quiet || c.Timeout != d.Timeout || len(c.Actions) != 0 {
		t.Errorf("template differs from defaults: %+v", c)
	}
}

func TestMissingAndEmpty(t *testing.T) {
	if _, err := Load(filepath.Join(t.TempDir(), "nope.yaml")); err != nil {
		t.Error(err)
	}
	if _, err := Load(write(t, "")); err != nil {
		t.Error(err)
	}
}

func TestErrors(t *testing.T) {
	for in, want := range map[string]string{
		"mode: loud\n":                     "mode",
		"modee: seal\n":                    "modee",
		"quiet: soon\n":                    "duration",
		"actions: [{name: a}]\n":           "exactly one",
		"actions: [{http: {url: /x}}]\n":   "needs a name",
		"actions: [{name: a, http: {}}]\n": "needs a url",
		"actions: [{name: a, command: x}, {name: a, command: y}]\n": "twice",
	} {
		_, err := Load(write(t, in))
		if err == nil || !strings.Contains(err.Error(), want) {
			t.Errorf("%q: got %v, want %q", in, err, want)
		}
	}
}

func TestIsProduction(t *testing.T) {
	c := Config{Production: []string{"https://shop.example", "admin.other.test:8443", "https://multi.test/live"}}
	for raw, want := range map[string]bool{
		"https://shop.example/checkout":     true,
		"HTTP://shop.example/":              true, // the scheme doesn't make it another site
		"https://SHOP.example":              true,
		"https://www.shop.example":          true,
		"https://shop.example:443/":         true,
		"http://shop.example:80":            true,
		"https://shop.example.:8080/x":      true, // an entry without a port covers every port
		"shop.example":                      true,
		"https://admin.other.test:8443/wp":  true,
		"https://multi.test/LIVE/cart":      true,
		"https://shop.example.staging.test": false,
		"https://myshop.example":            false,
		"https://staging-shop.example":      false,
		"https://admin.other.test/":         false, // the entry names a port
		"https://multi.test/staging":        false,
		"https://multi.test/lively":         false,
		"":                                  false,
		"/relative":                         false,
	} {
		if got := c.IsProduction(raw); got != want {
			t.Errorf("IsProduction(%q) = %v, want %v", raw, got, want)
		}
	}
	if r := c.ProductionReason("https://www.shop.example/"); r != `it matches "https://shop.example" under production` {
		t.Errorf("reason = %q", r)
	}
	if r := c.ProductionReason("https://staging.test/"); r != "" {
		t.Errorf("reason for staging = %q", r)
	}
	if _, err := Load(write(t, "production:\n  - shop.example\n  - \"https://\"\n")); err == nil || !strings.Contains(err.Error(), `line 3: production: "https://" isn't a URL or host name`) {
		t.Errorf("an entry without a host: %v", err)
	}
}

// full sets every key Echo reads.
const full = `site: https://staging.shop.test
mode: observe
listen: 127.0.0.1:18025
smtp: 127.0.0.1:11025
proxy: 127.0.0.1:8026
mitm: true
allow: [api.stripe.com]
allow_recipients: ["*@shop.test"]
staging: [https://staging.shop.test]
production: [https://shop.test]
flush: [wp cron event run --due-now]
quiet: 1s
timeout: 10s
checks:
  content/dev-link: off
  message/fields-lost: broke
rules:
  - id: brand/old-phone
    channels: [email, sms]
    match: "0800 123 456"
    level: error
    message: The old phone number is back
    fixtures: {match: ["Call 0800 123 456"], no_match: ["Call 0800 999 000"]}
  - id: brand/old-name
    regex: (?i)acme ltd
inbox: {store: .echo/inbox, keep: 50000, keep_for: 14d}
relay: {url: "smtp://user:pass@smtp.example.test:587", allow: ["*@client-a.test"]}
hub: {sites: [{id: client-a}]}
actions:
  - name: Checkout
    http: {method: POST, url: /checkout, form: {email: "${fake:email}"}}
    expect: {email: 2, analytics: {purchase: 1}, payment: 1}
    ignore: [body.order_id, "header:X-Request-Id", "*@example.test"]
    quiet: 500ms
    parallel: false
  - name: Report
    command: wp eval 'do_action("x");'
    env: {A: b}
  - name: Paid
    replay: {event: payment_intent.succeeded, to: /hook, set: {data.object.amount: "1999"}, secret_env: S}
`

func TestEveryKeyParses(t *testing.T) {
	c, err := Load(write(t, full))
	if err != nil {
		t.Fatal(err)
	}
	co := c.Actions[0]
	switch {
	case c.Checks["content/dev-link"] != Off || c.Checks["message/fields-lost"] != Broke:
		t.Errorf("checks = %v", c.Checks)
	case len(c.Rules) != 2 || c.Rules[0].Level != Error || len(c.Rules[0].Channels) != 2 || c.Rules[0].Fixtures == nil ||
		len(c.Rules[0].Fixtures.NoMatch) != 1 || c.Rules[1].Regex == "":
		t.Errorf("rules = %+v", c.Rules)
	case c.Inbox == nil || c.Inbox.Store != ".echo/inbox" || c.Inbox.Keep != 50000 || c.Inbox.KeepFor.Duration != 14*24*time.Hour:
		t.Errorf("inbox = %+v", c.Inbox)
	case c.Relay == nil || len(c.Relay.Allow) != 1 || c.Relay.URL == "":
		t.Errorf("relay = %+v", c.Relay)
	case c.Hub == nil:
		t.Error("hub is reserved and must be accepted")
	case co.Expect["email"].N != 2 || co.Expect["analytics"].Events["purchase"] != 1 || co.Expect.Total() != 4:
		t.Errorf("expect = %+v", co.Expect)
	case len(co.Ignore) != 3 || co.Quiet == nil || co.Quiet.Duration != 500*time.Millisecond || co.Parallel == nil || *co.Parallel:
		t.Errorf("checkout = %+v", co)
	case c.Actions[1].Parallel != nil || c.Actions[1].Quiet != nil || c.Actions[1].Expect != nil:
		t.Errorf("unset per-action keys must stay unset: %+v", c.Actions[1])
	}
	re, err := c.Rules[0].Pattern()
	if err != nil || !re.MatchString(c.Rules[0].Fixtures.Match[0]) || re.MatchString(c.Rules[0].Fixtures.NoMatch[0]) {
		t.Errorf("literal rule pattern: %v, %v", re, err)
	}
	if re, _ := c.Rules[1].Pattern(); !re.MatchString("Thanks, ACME Ltd") {
		t.Error("regex rule pattern")
	}
}

// TestRoundTrip writes a loaded config back out: it must load again and
// write the same YAML, so tools can rewrite configs without losing keys.
func TestRoundTrip(t *testing.T) {
	c, err := Load(write(t, full))
	if err != nil {
		t.Fatal(err)
	}
	once, err := yaml.Marshal(c)
	if err != nil {
		t.Fatal(err)
	}
	c2, err := Load(write(t, string(once)))
	if err != nil {
		t.Fatalf("%v\n%s", err, once)
	}
	twice, err := yaml.Marshal(c2)
	if err != nil {
		t.Fatal(err)
	}
	if string(once) != string(twice) {
		t.Errorf("round trip changed the config:\n%s\n---\n%s", once, twice)
	}
	c.Hub, c2.Hub, c.lines, c2.lines = nil, nil, nil, nil // node positions differ by design
	if !reflect.DeepEqual(c, c2) {
		t.Errorf("round trip changed values:\n%+v\n%+v", c, c2)
	}
}

func TestErrorsNameTheLine(t *testing.T) {
	for in, want := range map[string]string{
		"checks:\n  content/dev-link: loud\n":                                                     "line 2: checks: content/dev-link: \"loud\" isn't off, notice, warning, error, broke or changed",
		"rules:\n  - id: a\n    match: x\n    level: broke\n":                                     "line 4: rule \"a\": level \"broke\" isn't notice, warning or error",
		"rules:\n  - id: a\n    regex: \"([\"\n":                                                  "line 3: rule \"a\": regex",
		"rules:\n  - id: a\n":                                                                     "line 2: rule \"a\": set exactly one of match or regex",
		"rules:\n  - id: a\n    match: x\n    regex: y\n":                                         "exactly one of match or regex",
		"rules:\n  - match: x\n":                                                                  "line 2: rules[0]: needs an id",
		"rules:\n  - {id: a, match: x}\n  - {id: a, match: y}\n":                                  "line 3: rule \"a\" is listed twice",
		"rules:\n  - id: a\n    match: x\n    channels: [mail]\n":                                 "line 4: rule \"a\": \"mail\" isn't a channel",
		"actions:\n  - name: a\n    command: x\n    ignore: [\"body.[\"]\n":                       "line 4: action \"a\": ignore: \"body.[\" isn't a valid pattern",
		"actions:\n  - name: a\n    command: x\n    ignore: [\"\"]\n":                             "line 4: action \"a\": ignore: an empty pattern",
		"actions:\n  - name: a\n    command: x\n    expect: {mail: 1}\n":                          "line 4: expect: \"mail\" isn't a channel",
		"actions:\n  - name: a\n    command: x\n    expect:\n      email: -1\n":                   "line 5: expect: email wants a count",
		"actions:\n  - name: a\n    command: x\n    expect:\n      analytics: {purchase: lots}\n": "line 5: expect: analytics: \"purchase\" wants a count",
		"actions:\n  - name: a\n    command: x\n    quiet: soon\n":                                "line 4: \"soon\" isn't a duration",
		"actions:\n  - name: a\n    command: x\n    parallel: maybe\n":                            "line 4",
		"actions:\n  - name: a\n    command: x\n    parallels: false\n":                           "line 4: field parallels not found",
		"inbox: {keep: -1}\n":                                                                     "line 1: inbox: keep can't be negative",
		"inbox: {store: x, keepfor: 1d}\n":                                                        "field keepfor not found",
		"relay:\n  url: http://user:secret@x.test\n":                                              "line 2: relay: url wants smtp://",
		"relay:\n  allow: [\"[a\"]\n":                                                             "line 2: relay: allow: \"[a\" isn't a valid pattern",
		"mode: loud\n":                                                                            "line 1: mode",
		"actions:\n  - name: a\n    command: x\n  - name: a\n    command: y\n":                    "line 4: action \"a\" is listed twice (line 2)",
	} {
		_, err := Load(write(t, in))
		if err == nil || !strings.Contains(err.Error(), want) {
			t.Errorf("%q:\n got %v\nwant %q", in, err, want)
		}
		if err != nil && strings.Contains(err.Error(), "secret") {
			t.Errorf("%q: the error quotes a password: %v", in, err)
		}
	}
}

func TestDurationDays(t *testing.T) {
	for in, want := range map[string]time.Duration{"14d": 14 * 24 * time.Hour, "1d12h": 36 * time.Hour, "90s": 90 * time.Second} {
		if got, err := parseDuration(in); err != nil || got != want {
			t.Errorf("%s = %v, %v", in, got, err)
		}
	}
	for _, in := range []string{"d", "1dd", "x1d", "99999999999d"} {
		if _, err := parseDuration(in); err == nil {
			t.Errorf("%s parsed", in)
		}
	}
}

func TestValidateWithoutAFile(t *testing.T) {
	c := Default()
	c.Checks = map[string]Level{"x": "loud"}
	if err := c.Validate(); err == nil || strings.Contains(err.Error(), "line") {
		t.Errorf("a config built in code has no lines: %v", err)
	}
}

// TestActionIDsAreUniqueAndNotEmpty: compare matches actions by ID, so
// two that share one, or one with none, would mix up their messages.
func TestActionIDsAreUniqueAndNotEmpty(t *testing.T) {
	for in, want := range map[string]string{
		"actions:\n  - name: Contact Form\n    command: x\n  - name: contact-form\n    command: y\n":               `line 4: action "contact-form" and action "Contact Form" (line 2) both get the ID action:contact-form; rename one`,
		"actions:\n  - name: a\n    command: x\n  - name: b\n    command: x\n  - name: \" A! \"\n    command: y\n": `line 6: action " A! " and action "a" (line 2) both get the ID action:a; rename one`,
		"actions:\n  - name: \"!!!\"\n    command: x\n":                                                            `line 2: action "!!!": needs a letter or digit from a to z or 0 to 9 in its name, so compare can tell it apart`,
		"actions:\n  - name: ok\n    command: x\n  - name: \"???\"\n    command: y\n":                              `line 4: action "???": needs a letter or digit`,
		"actions:\n  - name: 注文\n    command: x\n":                                                                 `line 2: action "注文": needs a letter or digit`,
	} {
		_, err := Load(write(t, in))
		if err == nil || !strings.Contains(err.Error(), want) {
			t.Errorf("%q:\n got %v\nwant %q", in, err, want)
		}
	}

	c := Default()
	c.Actions = []Action{{Name: "Contact Form", Command: "x"}, {Name: "contact form", Command: "y"}}
	if err := c.Validate(); err == nil || err.Error() != `action "contact form" and action "Contact Form" both get the ID action:contact-form; rename one` {
		t.Errorf("without a file: %v", err)
	}

	c.Actions = []Action{{Name: "Contact form", Command: "x"}, {Name: "Contact form 2", Command: "y"}, {Name: "Checkout", Command: "z"}}
	if err := c.Validate(); err != nil {
		t.Errorf("distinct names: %v", err)
	}
}

func TestActionID(t *testing.T) {
	for name, want := range map[string]string{
		"Contact form":           "action:contact-form",
		"  Nightly report!":      "action:nightly-report",
		"WooCommerce: Order #12": "action:woocommerce-order-12",
		"!!!":                    "action:",
	} {
		if got := ActionID(name); got != want {
			t.Errorf("ActionID(%q) = %q, want %q", name, got, want)
		}
	}
}
