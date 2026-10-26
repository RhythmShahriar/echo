// Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
// SPDX-License-Identifier: MIT

package main

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"testing"

	"github.com/charmbracelet/huh"

	"github.com/RhythmShahriar/echo/consent/consenttest"
	"github.com/RhythmShahriar/echo/internal/browser"
	"github.com/RhythmShahriar/rhythm/kit"
	"github.com/RhythmShahriar/rhythm/kit/cli"
)

// The consent lines of rhythm echo checks, against a golden file.
func TestChecksConsentGolden(t *testing.T) {
	t.Chdir(t.TempDir())
	_, out, _ := runCLI(t, "checks")
	var lines []string
	for l := range strings.SplitSeq(out, "\n") {
		if strings.HasPrefix(l, "consent/") {
			lines = append(lines, l)
		}
	}
	goldenFile(t, "checks-consent.golden", strings.Join(lines, "\n")+"\n")
}

// reportingClassifier tries custom trackers' fixtures as the real
// catalog does: a fixture naming "fail" fails.
type reportingClassifier struct{ consenttest.FakeClassifier }

func (*reportingClassifier) FixtureReport(yaml []byte) ([]string, bool) {
	if strings.Contains(string(yaml), "fail") {
		return []string{"FAIL acme-pixel  match    https://px.acme.test/fail"}, false
	}
	return []string{"ok   acme-pixel  match    https://px.acme.test/hit"}, true
}

func TestChecksTestConsentFixtures(t *testing.T) {
	t.Chdir(t.TempDir())
	s := consenttest.Fakes()
	s.Classifier = &reportingClassifier{}
	consenttest.Use(t, s)
	writeConfig(t, "consent:\n  trackers:\n    - id: acme-pixel\n      fixtures: {match: [\"https://px.acme.test/hit\"]}\n")
	if code, out, _ := runCLI(t, "checks", "--test"); code != exitOK || !strings.Contains(out, "ok   acme-pixel  match    https://px.acme.test/hit\n") {
		t.Errorf("passing fixture: %d\n%s", code, out)
	}
	writeConfig(t, "consent:\n  trackers:\n    - id: acme-pixel\n      fixtures: {match: [\"https://px.acme.test/fail\"]}\n")
	if code, out, _ := runCLI(t, "checks", "--test"); code != exitFindings || !strings.Contains(out, "FAIL acme-pixel") {
		t.Errorf("failing fixture: %d\n%s", code, out)
	}
	// Without consent trackers, nothing about them is printed.
	writeConfig(t, "site: https://shop.test\n")
	if code, out, _ := runCLI(t, "checks", "--test"); code != exitOK || strings.Contains(out, "acme") {
		t.Errorf("no trackers: %d\n%s", code, out)
	}
}

// fakeChrome sets what doctor's finder answers for the rest of t.
func fakeChrome(t *testing.T, bin string, downloaded bool, err error, ver string) {
	t.Helper()
	oldFind, oldVer := chromeFinder, chromeVersion
	t.Cleanup(func() { chromeFinder, chromeVersion = oldFind, oldVer })
	chromeFinder = func() (string, bool, error) { return bin, downloaded, err }
	chromeVersion = func(string) string { return ver }
}

func TestDoctorChromeLine(t *testing.T) {
	t.Setenv("CI", "")
	t.Setenv("RHYTHM_OFFLINE", "")
	pinned := majorVersion(browser.PinnedVersion())
	for _, tc := range []struct {
		name       string
		bin        string
		downloaded bool
		err        error
		ver        string
		env        string
		want       string
	}{
		{"installed", "/usr/bin/google-chrome", false, nil, "153", "", "ok    Chrome for consent checks: found (Chrome 153 at /usr/bin/google-chrome)"},
		{"no version", "C:/Chrome/chrome.exe", false, nil, "", "", "ok    Chrome for consent checks: found (C:/Chrome/chrome.exe)"},
		{"downloaded", "/cache/chrome", true, nil, "", "", "ok    Chrome for consent checks: found (Chrome for Testing " + pinned + " at /cache/chrome)"},
		{"will download", "", false, errors.New("no Chrome"), "", "", "note  Chrome for consent checks: will download Chrome for Testing " + pinned},
		{"offline", "", false, errors.New("no Chrome"), "", "RHYTHM_OFFLINE", "note  Chrome for consent checks: missing (--no-download)"},
		{"ci", "", false, errors.New("no Chrome"), "", "CI", "note  Chrome for consent checks: missing (--no-download)"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if tc.env != "" {
				t.Setenv(tc.env, "true")
			}
			fakeChrome(t, tc.bin, tc.downloaded, tc.err, tc.ver)
			t.Chdir(t.TempDir())
			writeConfig(t, "smtp: \"off\"\n")
			_, out, _ := runCLI(t, "doctor", "--url", "http://127.0.0.1:1", "--plain")
			if !strings.Contains(out, tc.want+"\n") {
				t.Errorf("doctor:\n%s\nwant %q", out, tc.want)
			}
		})
	}
	// --json gains the item; it never counts as a failure.
	fakeChrome(t, "/usr/bin/google-chrome", false, nil, "153")
	t.Chdir(t.TempDir())
	writeConfig(t, "smtp: \"off\"\n")
	code, out, _ := runCLI(t, "doctor", "--url", "http://127.0.0.1:1", "--json")
	var env struct{ Data []doctorCheck }
	if err := json.Unmarshal([]byte(out), &env); err != nil {
		t.Fatal(err)
	}
	i := slices.IndexFunc(env.Data, func(c doctorCheck) bool { return c.Name == "Chrome for consent checks" })
	if code != exitError || i < 0 || env.Data[i].Status != "ok" || env.Data[i].Detail != "found (Chrome 153 at /usr/bin/google-chrome)" {
		t.Errorf("doctor --json: %d %+v", code, env.Data)
	}
	if majorVersion("Google Chrome 153.0.8010.52 \n") != "153" || majorVersion("Chromium") != "" {
		t.Error("majorVersion")
	}
}

// doctor never runs chrome.exe --version on Windows (it opens the
// browser there) and reads the version from a Chrome elsewhere.
func TestAskVersion(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("needs a shell script as the fake Chrome")
	}
	dir := t.TempDir()
	ran := filepath.Join(dir, "ran")
	bin := filepath.Join(dir, "chrome")
	if err := os.WriteFile(bin, []byte("#!/bin/sh\ntouch '"+ran+"'\necho 'Google Chrome 153.0.8010.52'\n"), 0o700); err != nil { //nolint:gosec // a test script
		t.Fatal(err)
	}
	if v := askVersion("windows", bin); v != "" {
		t.Errorf("windows: %q", v)
	}
	if _, err := os.Stat(ran); err == nil {
		t.Error("windows: the browser ran")
	}
	if v := askVersion("linux", bin); v != "153" {
		t.Errorf("linux: %q", v)
	}
	if v := askVersion("linux", filepath.Join(dir, "missing")); v != "" {
		t.Errorf("missing: %q", v)
	}
}

// The picker always offers a consent check and passes the page on.
func TestPickerHasConsent(t *testing.T) {
	for _, ps := range []pickState{{}, {running: true, hasConfig: true, hasActions: true, hasResult: true}} {
		if !slices.ContainsFunc(pickOptions(ps), func(o huh.Option[string]) bool {
			return o.Value == "consent" && o.Key == "Check cookie consent on a page (no choice, Accept, Reject)"
		}) {
			t.Errorf("no consent option for %+v", ps)
		}
	}
	t.Chdir(t.TempDir())
	writeConfig(t, "site: https://staging.shop.test\n")
	if ps := loadPickState(); ps.site != "https://staging.shop.test" {
		t.Errorf("site prefill: %q", ps.site)
	}
}

// The top-level help lists the consent forms and the consent meaning
// of exit 3.
func TestHelpExitCode3(t *testing.T) {
	_, out, _ := runCLI(t, "help")
	for _, want := range []string{
		"  3    couldn't run or couldn't check (a consent run that couldn't make its choice):",
		"  rhythm echo consent <url>...           check cookie consent",
		"  rhythm echo consent detect <url>       find the banner",
		"  rhythm echo consent report|verify|cmps|trackers|prune\n",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("help lacks %q", want)
		}
	}
}

// version --json is the kit's rhythm-version/v1 document with the same
// keys kit cli.Main prints, which the rhythm router checks before it
// sends __complete.
func TestVersionJSON(t *testing.T) {
	code, out, _ := runCLI(t, "version", "--json")
	var doc map[string]string
	if err := json.Unmarshal([]byte(out), &doc); code != exitOK || err != nil {
		t.Fatalf("exit %d %v\n%s", code, err, out)
	}
	var keys []string
	for k := range doc {
		keys = append(keys, k)
	}
	slices.Sort(keys)
	if strings.Join(keys, " ") != "commit kit_version name schema version" {
		t.Errorf("keys: %v", keys)
	}
	if doc["schema"] != cli.VersionSchema || doc["name"] != "rhythm-echo" || doc["version"] != version || doc["kit_version"] != kit.Version() || doc["kit_version"] == "" {
		t.Errorf("version --json: %v", doc)
	}
	if code, out, _ := runCLI(t, "version"); code != exitOK || out != "rhythm-echo "+version+"\n" {
		t.Errorf("version: %d %q", code, out)
	}
	if code, _, errOut := runCLI(t, "version", "--yaml"); code != exitUsage || !strings.Contains(errOut, "version takes only --json") {
		t.Errorf("version --yaml: %d %q", code, errOut)
	}
}

func TestComplete(t *testing.T) {
	wd := t.TempDir()
	t.Chdir(wd)
	for _, tc := range []struct {
		words []string
		want  string
	}{
		{[]string{"consent", "--ru"}, "--runs"},
		{[]string{"consent", "d"}, "detect"},
		{[]string{"consent", "verify", "--k"}, "--key"},
		{[]string{"cons"}, "consent"},
		{[]string{"ver"}, "version"},
		{[]string{"help", "cons"}, "consent"},
		{[]string{"version", "--j"}, "--json"},
		{[]string{"compare", "--snap"}, "--snapshot"},
		{[]string{"compare", "--snapshot", ""}, ""},
		{[]string{"report", "-o", ""}, ""},
		{[]string{"install", "w"}, "wordpress"},
		{[]string{"init", "--con"}, "--consent"},
		{[]string{"upd"}, ""},
		{[]string{"nope", "--x"}, ""},
	} {
		code, out, _ := runCLI(t, append([]string{"__complete"}, tc.words...)...)
		if got := strings.Join(strings.Fields(out), " "); code != exitOK || got != tc.want {
			t.Errorf("__complete %q = %q, want %q", tc.words, got, tc.want)
		}
	}
	// Completion never writes: the folder stays empty.
	if entries, _ := os.ReadDir(wd); len(entries) != 0 {
		t.Errorf("__complete wrote %v", entries)
	}
	if _, out, _ := runCLI(t, "help"); strings.Contains(out, "__complete") {
		t.Error("help lists __complete")
	}
}
