// Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
// SPDX-License-Identifier: MIT

package mcp

import (
	"io"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/RhythmShahriar/echo/consent"
	"github.com/RhythmShahriar/echo/consent/consenttest"
	"github.com/RhythmShahriar/echo/report"
	"github.com/RhythmShahriar/echo/store"
)

// consentServer is an MCP server for the consent tool in a fresh
// folder, its stages the fakes with a runner returning the clean result.
func consentServer(t *testing.T) (*session, *consenttest.FakeRunner) {
	t.Helper()
	t.Chdir(t.TempDir())
	t.Setenv("RHYTHM_CONFIG_DIR", t.TempDir())
	t.Setenv("RHYTHM_OFFLINE", "1")
	runner := &consenttest.FakeRunner{Result: consenttest.GoldenResult("clean")}
	s := consenttest.Fakes()
	s.Runner = runner
	consenttest.Use(t, s)
	srv := &Server{Store: store.Store{Root: ".echo"}, Version: "test",
		Now: func() time.Time { return time.Date(2026, 10, 26, 9, 20, 41, 0, time.UTC) }}
	return start(t, srv), runner
}

// writersBuilt reports whether the consent report writers are in this
// build: the verdict lane writes them, the skeleton has stubs.
func writersBuilt() bool {
	r := consenttest.GoldenResult("clean")
	return report.WriteConsentJSON(io.Discard, r, "error") == nil && report.WriteConsentHTML(io.Discard, r, report.ConsentHTMLOptions{}) == nil
}

func TestMCPCheckConsent(t *testing.T) {
	ss, runner := consentServer(t)
	res, text, isErr := ss.tool("check_consent", map[string]any{"url": "staging.shop.test", "runs": "+gpc", "consent_mode": "strict", "geo": "de"})
	if len(runner.Plans) != 1 {
		t.Fatalf("plans: %d (%s)", len(runner.Plans), text)
	}
	p := runner.Plans[0]
	if p.Parallel != 3 || !p.Hold || p.Policy != consent.PolicyStrict || p.Geo != "de" || !slices.Contains(p.Runs, consent.RunGPC) ||
		p.Targets[0].URL.String() != "https://staging.shop.test/" || !strings.Contains(filepath.ToSlash(p.Dir), ".echo/consent/") {
		t.Errorf("plan: parallel %d hold %v policy %s geo %s runs %v dir %s", p.Parallel, p.Hold, p.Policy, p.Geo, p.Runs, p.Dir)
	}
	if !writersBuilt() {
		t.Skip("the consent report writers aren't in this build yet")
	}
	if isErr || res["kind"] != "consent" {
		t.Fatalf("result: %v\n%s", res, text)
	}
	if _, ok := res["data"].(map[string]any)["issues"]; !ok {
		t.Errorf("no issues in the envelope: %s", text)
	}
}

// send_hits isn't an argument: the schema refuses it before anything
// runs, so an agent's check always holds hits.
func TestMCPNoSendHits(t *testing.T) {
	ss, runner := consentServer(t)
	resp := ss.request("tools/call", map[string]any{"name": "check_consent", "arguments": map[string]any{"url": "https://shop.test/", "send_hits": true}})
	if resp["error"] == nil || !strings.Contains(resp["error"].(map[string]any)["message"].(string), "send_hits") {
		t.Errorf("send_hits: %v", resp)
	}
	if len(runner.Plans) != 0 {
		t.Error("the browser was started")
	}
	for _, def := range toolList() {
		if def.Name == "check_consent" {
			props := def.InputSchema["properties"].(map[string]any)
			if _, ok := props["send_hits"]; ok || def.InputSchema["additionalProperties"] != false {
				t.Errorf("schema: %v", def.InputSchema)
			}
		}
	}
	// A URL spelled like the flag stays a URL: the hits stay held.
	ss.tool("check_consent", map[string]any{"url": "--send-hits"})
	for _, p := range runner.Plans {
		if !p.Hold || p.Targets[0].URL.Host != "--send-hits" {
			t.Errorf("--send-hits as the URL: hold %v, %s", p.Hold, p.Targets[0].URL)
		}
	}
}

// The check's refusals come back as the tool's error in the PRD's
// words, masked.
func TestMCPCheckConsentErrors(t *testing.T) {
	ss, runner := consentServer(t)
	for args, want := range map[string]string{
		"ftp://shop.test": `consent "ftp://shop.test": want an http or https URL`,
		"https://user:" + "hunter2" + "@shop.test/": "a URL with a login in it isn't allowed",
		"": "url is empty",
	} {
		_, text, isErr := ss.tool("check_consent", map[string]any{"url": args})
		if !isErr || !strings.Contains(text, want) || strings.Contains(text, "hunter2") || strings.HasPrefix(text, "rhythm echo:") {
			t.Errorf("%q: %v %s", args, isErr, text)
		}
	}
	if _, text, isErr := ss.tool("check_consent", map[string]any{"url": "https://shop.test/", "runs": "sideways"}); !isErr || !strings.Contains(text, "sideways") {
		t.Errorf("bad runs: %v %s", isErr, text)
	}
	if len(runner.Plans) != 0 {
		t.Error("the browser was started")
	}
}

// The command line reads the server's store and its config.
func TestConsentCommandLine(t *testing.T) {
	dir := t.TempDir()
	s := &Server{Store: store.Store{Root: dir}}
	if got := strings.Join(s.commandLine(consentArgs{URL: "shop.test"}), " "); got != "--parallel 3 --store "+dir+" -- shop.test" {
		t.Errorf("no config: %s", got)
	}
	if err := os.WriteFile(filepath.Join(dir, "config.yaml"), []byte("site: https://shop.test\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	got := strings.Join(s.commandLine(consentArgs{URL: "/", Runs: "accept", ConsentMode: "basic", Geo: "fr"}), " ")
	if got != "--parallel 3 --store "+dir+" --config "+filepath.Join(dir, "config.yaml")+" --runs accept --consent-mode basic --geo fr -- /" {
		t.Errorf("with config: %s", got)
	}
}
