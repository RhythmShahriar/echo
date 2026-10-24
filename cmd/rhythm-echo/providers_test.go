// Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
// SPDX-License-Identifier: MIT

package main

import (
	"strings"
	"testing"
)

func TestProvidersCommand(t *testing.T) {
	code, out, _ := runCLI(t, "providers")
	for _, want := range []string{"EMAIL (", "SendGrid", "sendgrid", "api.sendgrid.com", "Mailjet", "(YAML)",
		"Stripe", "live calls refused", "services recognized"} {
		if code != exitOK || !strings.Contains(out, want) {
			t.Errorf("providers lacks %q: %d\n%s", want, code, out)
		}
	}
	code, out, _ = runCLI(t, "providers", "--json")
	var entries []providerEntry
	decodeEnvelope(t, []byte(out), kindProviders, &entries)
	if code != exitOK || len(entries) < 40 {
		t.Fatalf("providers --json: %d, %d entries", code, len(entries))
	}
	for _, e := range entries {
		if e.Provider == "stripe" && e.Payment == "" {
			t.Errorf("stripe isn't marked as a payment gateway: %+v", e)
		}
	}
	if code, _, _ := runCLI(t, "providers", "--fail-on", "error"); code != exitUsage {
		t.Errorf("a flag providers doesn't read: %d", code)
	}
}
