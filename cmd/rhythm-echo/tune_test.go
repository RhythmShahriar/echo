// Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
// SPDX-License-Identifier: MIT

package main

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/RhythmShahriar/echo/catcher"
	"github.com/RhythmShahriar/echo/client"
	"github.com/RhythmShahriar/echo/compare"
	"github.com/RhythmShahriar/echo/model"
	"github.com/RhythmShahriar/echo/wire"
)

// signupConfig is one action that posts to the shop's /signup.
func signupConfig(site, extra string) string {
	return "site: " + site + "\nquiet: 200ms\ntimeout: 3s\n" + extra + "actions:\n" +
		"  - name: Sign up\n    http: {method: POST, url: /signup, form: {email: ann@shop.test}}\n"
}

// TestRulesAndChecksReachRun: the config's rules: add issues to a run and
// its checks: turn checks off or change their level, so the exit code
// follows them. An unknown check ID is refused.
func TestRulesAndChecksReachRun(t *testing.T) {
	t.Chdir(t.TempDir())
	srv := httptest.NewServer(catcher.NewServer(catcher.NewInbox(100), catcher.Config{Version: "test"}))
	defer srv.Close()
	shop := shopSite(t, srv.URL, "Hi Ann, SECRET-PROJECT is live.")
	writeConfig(t, signupConfig(shop.URL, "rules:\n  - {id: brand/secret, match: SECRET-PROJECT, level: error}\n"))
	if code, out, errOut := runCLI(t, "run", "--url", srv.URL, "--plain"); code != exitFindings || !strings.Contains(out, "Matched brand/secret") {
		t.Errorf("a rule's match: %d\n%s\n%s", code, out, errOut)
	}
	writeConfig(t, signupConfig(shop.URL, "rules:\n  - {id: brand/secret, match: SECRET-PROJECT, level: error}\nchecks:\n  brand/secret: notice\n"))
	if code, out, _ := runCLI(t, "run", "--url", srv.URL, "--plain"); code != exitOK || !strings.Contains(out, "Matched brand/secret") {
		t.Errorf("checks: notice on a rule: %d\n%s", code, out)
	}

	placeholder := shopSite(t, srv.URL, "Hi {first_name}, welcome.")
	writeConfig(t, signupConfig(placeholder.URL, "checks:\n  content/placeholder: off\n"))
	if code, out, _ := runCLI(t, "run", "--url", srv.URL, "--plain"); code != exitOK || strings.Contains(out, "Unfilled placeholder") {
		t.Errorf("checks: off: %d\n%s", code, out)
	}
	writeConfig(t, signupConfig(placeholder.URL, "checks:\n  content/unfilled-placeholder: off\n"))
	if code, _, errOut := runCLI(t, "run", "--url", srv.URL, "--plain"); code != exitUsage ||
		!strings.Contains(errOut, "checks: content/unfilled-placeholder isn't a check; rhythm echo checks lists them") {
		t.Errorf("an unknown check: %d %s", code, errOut)
	}
}

// TestCompareTunesAndAccepts: compare counts changes with the config's
// checks: and ignore: and --accept takes a change out by its key.
func TestCompareTunesAndAccepts(t *testing.T) {
	t.Chdir(t.TempDir())
	srv := httptest.NewServer(catcher.NewServer(catcher.NewInbox(1000), catcher.Config{Version: "test"}))
	defer srv.Close()
	writeConfig(t, signupConfig(shopSite(t, srv.URL, "Hi Ann, welcome.").URL, ""))
	if code, _, errOut := runCLI(t, "snapshot", "--url", srv.URL, "--name", "before", "-q"); code != exitOK {
		t.Fatalf("snapshot: %d %s", code, errOut)
	}
	broken := shopSite(t, srv.URL, "Hi {first_name}, welcome.")
	writeConfig(t, signupConfig(broken.URL, ""))
	code, out, _ := runCLI(t, "compare", "--url", srv.URL, "--json")
	var c comparisonData
	decodeEnvelope(t, []byte(out), kindComparison, &c)
	if code != exitFindings || c.Comparison == nil || c.Count(model.Broke) == 0 {
		t.Fatalf("compare: %d\n%s", code, out)
	}
	var keys []string
	for _, ch := range c.Changes {
		if ch.Class == model.Broke {
			keys = append(keys, "--accept", compare.Key(ch))
		}
	}
	code, out, errOut := runCLI(t, append([]string{"compare", "--url", srv.URL, "--plain", "--accept", "deadbeef"}, keys...)...)
	if code != exitOK || !strings.Contains(out, "accepted as intended") || !strings.Contains(errOut, "--accept deadbeef matched no change") {
		t.Errorf("compare --accept: %d\n%s\n%s", code, out, errOut)
	}
	writeConfig(t, signupConfig(broken.URL, "checks:\n  content/placeholder: off\n  message/text: off\n"))
	if code, out, _ := runCLI(t, "compare", "--url", srv.URL, "--plain", "--fail-on", "changed"); code != exitOK {
		t.Errorf("compare with checks: off: %d\n%s", code, out)
	}
	if code, _, errOut := runCLI(t, "compare", "--url", srv.URL, "--parallel", "-1"); code != exitUsage || !strings.Contains(errOut, "--parallel -1") {
		t.Errorf("--parallel -1: %d %s", code, errOut)
	}
}

// TestRunnerOptionsFromFlags: --parallel, --quiet and --timeout reach the
// runner and the run is signed when Echo verifies signed runs.
func TestRunnerOptionsFromFlags(t *testing.T) {
	t.Chdir(t.TempDir())
	t.Setenv("RHYTHM_ECHO_TOKEN", "echo-token-1")
	t.Setenv(writeTokenEnv, "")
	writeConfig(t, "actions:\n  - {name: a, command: \"true\"}\n")
	c, _ := lookup("compare")
	p, err := parse(c, []string{"--parallel", "3", "--quiet", "500ms", "--timeout", "4s"})
	if err != nil {
		t.Fatal(err)
	}
	s := p.begin(&strings.Builder{}, &strings.Builder{})
	defer s.stop()
	signed := client.Health{Capabilities: []string{"leases", "signed-runs"}}
	o := s.runnerOptions("", signed)
	if o.Parallel != 3 || o.Quiet != 500*time.Millisecond || o.Timeout != 4*time.Second || o.RunKey != "echo-token-1" {
		t.Errorf("options: parallel %d, quiet %v, timeout %v, key %q", o.Parallel, o.Quiet, o.Timeout, o.RunKey)
	}
	if o := s.runnerOptions("", client.Health{}); o.RunKey != "" {
		t.Errorf("an Echo without signed-runs gets a signature: %q", o.RunKey)
	}
	t.Setenv(writeTokenEnv, "site-write-token")
	if o := s.runnerOptions("", signed); o.RunKey != "site-write-token" {
		t.Errorf("with %s: %q", writeTokenEnv, o.RunKey)
	}
}

// TestSignedRunReachesATokenConnector: a site whose connector has a
// token only trusts a signed X-Echo-Run, as the K5 connectors do. The run
// signs it with Echo's token, so the site sees a run value it trusts and
// its capture names the action.
func TestSignedRunReachesATokenConnector(t *testing.T) {
	t.Chdir(t.TempDir())
	const token = "connector-token-21300"
	t.Setenv("RHYTHM_ECHO_TOKEN", token)
	t.Setenv(writeTokenEnv, "")
	srv := httptest.NewServer(catcher.NewServer(catcher.NewInbox(100), catcher.Config{Version: "test", Token: token}))
	defer srv.Close()
	echo := client.New(srv.URL, token)
	var trusted, unsigned atomic.Int32
	shop := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		run := r.Header.Get(wire.RunHeader)
		if _, _, ok := wire.VerifyRun(token, run); ok {
			trusted.Add(1)
		} else {
			unsigned.Add(1)
			run = "" // a connector with a token ignores an unsigned run
		}
		_, _ = echo.Capture(r.Context(), wire.Capture{Kind: "email", Run: run, Source: wire.Source{Via: "test"},
			Email: &wire.Email{To: []string{"ann@shop.test"}, Subject: "Welcome", Text: "Hi Ann"}})
	}))
	defer shop.Close()
	writeConfig(t, signupConfig(shop.URL, ""))
	if code, out, errOut := runCLI(t, "run", "--url", srv.URL, "--plain"); code != exitOK || !strings.Contains(out, "1 email") {
		t.Fatalf("run: %d\n%s\n%s", code, out, errOut)
	}
	if trusted.Load() != 1 || unsigned.Load() != 0 {
		t.Errorf("the site saw %d signed and %d unsigned run values", trusted.Load(), unsigned.Load())
	}
	ms, err := echo.Messages(context.Background(), client.Filter{})
	if err != nil || len(ms) != 1 || ms[0].Action != "action:sign-up" || strings.Contains(ms[0].Action, ".") {
		t.Errorf("the capture: %+v %v", ms, err)
	}
}
