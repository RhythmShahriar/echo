// Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
// SPDX-License-Identifier: MIT

package main

import (
	"strings"
	"testing"
)

func TestChecksCommand(t *testing.T) {
	t.Chdir(t.TempDir())
	code, out, _ := runCLI(t, "checks")
	if code != exitOK || !strings.Contains(out, "CHECK") || !strings.Contains(out, "content/placeholder") || !strings.Contains(out, "compat/html") {
		t.Errorf("checks: %d\n%s", code, out)
	}
	if code, out, _ := runCLI(t, "checks", "--test"); code != exitOK || !strings.Contains(out, "No custom rules") {
		t.Errorf("checks --test without rules: %d\n%s", code, out)
	}
	writeConfig(t, "rules:\n  - id: brand/old-phone\n    match: \"0800 123 456\"\n    fixtures: {match: [\"Call 0800 123 456\"], no_match: [\"Call 0800 999 000\"]}\n"+
		"checks:\n  content/dev-link: off\n")
	code, out, _ = runCLI(t, "checks")
	if code != exitOK || !strings.Contains(out, "brand/old-phone") || !strings.Contains(out, "sets the level of 1 check under checks:") {
		t.Errorf("checks with a rule: %d\n%s", code, out)
	}
	if code, out, _ := runCLI(t, "checks", "--test"); code != exitOK || !strings.Contains(out, "1 rule, 2 fixtures, all pass.") {
		t.Errorf("checks --test: %d\n%s", code, out)
	}
	writeConfig(t, "rules:\n  - id: brand/old-phone\n    match: \"0800 123 456\"\n")
	if code, out, _ := runCLI(t, "checks", "--test"); code != exitFindings || !strings.Contains(out, "2 failed") {
		t.Errorf("checks --test with a rule without fixtures: %d\n%s", code, out)
	}
}
