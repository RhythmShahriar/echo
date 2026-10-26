// Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
// SPDX-License-Identifier: MIT

package main

import (
	"strings"
	"testing"

	"github.com/RhythmShahriar/echo/consent"
)

// Until the cli lane builds it, consent says so and exits 3.
func TestConsentPlaceholder(t *testing.T) {
	code, out, errOut := runCLI(t, "consent", "https://shop.test/")
	if code != exitError || out != "" || errOut != "rhythm echo: consent isn't built yet\n" {
		t.Errorf("consent: %d %q %q", code, out, errOut)
	}
	if code, out, _ := runCLI(t, "help", "consent"); code != exitOK || !strings.HasPrefix(out, "rhythm echo consent: ") {
		t.Errorf("help consent: %d %q", code, out)
	}
	if code, out, _ := runCLI(t, "help"); code != exitOK || !strings.Contains(out, "rhythm echo consent [<url>...]") {
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
