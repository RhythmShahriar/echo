// Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
// SPDX-License-Identifier: MIT

package main

import (
	"strings"
	"testing"

	"github.com/RhythmShahriar/echo/consent"
	consentcli "github.com/RhythmShahriar/echo/consent/cli"
	"github.com/RhythmShahriar/echo/consent/consenttest"
)

// consent is wired to the consent CLI: its help and its usage errors,
// none of which start a browser.
func TestConsentWired(t *testing.T) {
	t.Chdir(t.TempDir())
	code, out, errOut := runCLI(t, "consent")
	if code != exitUsage || out != "" || !strings.HasPrefix(errOut, "rhythm echo: consent needs a page: ") {
		t.Errorf("consent: %d %q %q", code, out, errOut)
	}
	if code, _, errOut := runCLI(t, "consent", "ftp://shop.test"); code != exitUsage || errOut != "rhythm echo: consent \"ftp://shop.test\": want an http or https URL\n" {
		t.Errorf("consent ftp://: %d %q", code, errOut)
	}
	for _, args := range [][]string{{"help", "consent"}, {"consent", "--help"}} {
		if code, out, _ := runCLI(t, args...); code != exitOK || out != consentcli.Usage {
			t.Errorf("%v: %d %q", args, code, out)
		}
	}
	if code, out, _ := runCLI(t, "help"); code != exitOK || !strings.Contains(out, "rhythm echo consent <url>...") {
		t.Errorf("help doesn't list consent: %d", code)
	}
}

// rhythm echo checks lists all 21 consent checks with the consent
// channel; checks: takes their IDs.
func TestChecksListsConsent(t *testing.T) {
	t.Chdir(t.TempDir())
	code, out, _ := runCLI(t, "checks")
	if code != exitOK {
		t.Fatalf("checks: %d\n%s", code, out)
	}
	n := 0
	for line := range strings.SplitSeq(out, "\n") {
		if strings.HasPrefix(line, "consent/") {
			n++
			f := strings.Fields(line)
			c, ok := consent.Check(f[0])
			if len(f) < 4 || !ok || f[1] != string(c.Level) || f[2] != "consent" {
				t.Errorf("consent check line %q", line)
			}
		}
	}
	if n != len(consent.Checks) || n != 21 {
		t.Errorf("checks lists %d consent checks; want 21\n%s", n, out)
	}
	writeConfig(t, "checks:\n  consent/before-choice: warning\n")
	if code, out, errOut := runCLI(t, "checks"); code != exitOK || !strings.Contains(out, "sets the level of 1 check under checks:") {
		t.Errorf("checks with consent/before-choice: warning: %d\n%s%s", code, out, errOut)
	}
}

// consent cmps and consent trackers print the linked stages' catalogs:
// every CMP rule and every built-in tracker by its ID. The fakes cover
// the CLI's side (consent/cli TestCmpsTrackers); this one needs the real
// cmp and tracker stages, so it skips until they are linked (and fails
// then under consenttest.RequireLinkedEnv).
func TestCmpsTrackersGolden(t *testing.T) {
	consenttest.Need(t, consent.StageCMP, consent.StageClassifier)
	t.Chdir(t.TempDir())
	l := consent.Linked()
	for _, tc := range []struct {
		sub string
		ids []string
	}{
		{"cmps", ruleIDs(l.CMP.Rules())},
		{"trackers", trackerIDs(l.Classifier.Trackers())},
	} {
		if len(tc.ids) == 0 {
			t.Errorf("consent %s: the linked stage lists nothing", tc.sub)
		}
		code, out, errOut := runCLI(t, "consent", tc.sub, "--json")
		if code != exitOK || !strings.Contains(out, `"consent-catalog"`) {
			t.Fatalf("consent %s --json: %d %q %q", tc.sub, code, out, errOut)
		}
		code, text, errOut := runCLI(t, "consent", tc.sub, "--plain")
		if code != exitOK {
			t.Fatalf("consent %s: %d %q", tc.sub, code, errOut)
		}
		for _, id := range tc.ids {
			if !strings.Contains(out, `"`+id+`"`) || !strings.Contains(text, id) {
				t.Errorf("consent %s doesn't list %q", tc.sub, id)
			}
		}
	}
}

func ruleIDs(rs []consent.CMPRuleInfo) []string {
	ids := make([]string, 0, len(rs))
	for _, r := range rs {
		ids = append(ids, r.ID)
	}
	return ids
}

func trackerIDs(ts []consent.TrackerInfo) []string {
	ids := make([]string, 0, len(ts))
	for _, tr := range ts {
		ids = append(ids, tr.ID)
	}
	return ids
}
