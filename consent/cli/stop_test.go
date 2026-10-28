// Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
// SPDX-License-Identifier: MIT

package cli

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/RhythmShahriar/echo/consent"
	"github.com/RhythmShahriar/echo/report"
)

// stopEnvelope reads the error envelope a check that stopped wrote.
func stopEnvelope(t *testing.T, out string) report.ConsentEnvelope {
	t.Helper()
	var e report.ConsentEnvelope
	if err := json.Unmarshal([]byte(out), &e); err != nil {
		t.Fatalf("stdout isn't one envelope: %v\n%s", err, out)
	}
	return e
}

// --json writes an envelope even when the check stops before a result:
// a usage error (exit 2) or a site that didn't answer (exit 3).
func TestJSONWhenTheCheckStops(t *testing.T) {
	t.Setenv("CI", "true")
	for _, tc := range []struct {
		name string
		args []string
		err  error
		code int
		msg  string
	}{
		{"unknown flag", []string{"--json", "--zzzzzzz", "https://shop.test/"}, nil, exitUsage, "unknown flag --zzzzzzz"},
		{"bad URL", []string{"--json", "mailto:shop"}, nil, exitUsage, `consent "mailto:shop": want an http or https URL`},
		{"site down", []string{"--json", "https://shop.test/"},
			&consent.FatalError{Code: consent.FatalUnreachable, URL: "https://shop.test/", Detail: "dial tcp 127.0.0.1:443: connection refused"}, exitUnavailable, "https://shop.test/"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			inDir(t, "")
			useRunner(t, &consent.Result{ID: "partial", Pages: []consent.Page{}}, tc.err)
			code, out, errOut := runMain(t, tc.args...)
			if code != tc.code {
				t.Fatalf("exit %d; want %d\n%s", code, tc.code, errOut)
			}
			e := stopEnvelope(t, out)
			if e.Kind != report.KindConsent || e.Verdict != report.VerdictError || e.Complete || e.Error == nil ||
				e.Error.Exit != tc.code || !strings.Contains(e.Error.Message, tc.msg) {
				t.Errorf("envelope %+v error %+v", e, e.Error)
			}
			if !strings.HasPrefix(errOut, "rhythm echo: ") {
				t.Errorf("stderr %q", errOut)
			}
		})
	}
	// Without --json, or with the JSON going to a file, stdout stays
	// empty.
	for _, args := range [][]string{{"mailto:shop"}, {"--json", "-o", "x.json", "mailto:shop"}, {"--json", "-o", "x.json", "--zzzz"}} {
		inDir(t, "")
		if code, out, _ := runMain(t, args...); code != exitUsage || out != "" {
			t.Errorf("%v: exit %d stdout %q", args, code, out)
		}
	}
}

// A line of flags kept in a variable and passed without eval keeps its
// quotes: the error says to use eval instead of naming an unknown flag.
func TestQuotedMapLine(t *testing.T) {
	want := `rhythm echo: --map-host '*.shop.test=...' kept its quotes; ` + msgUseEval + "\n"
	// zsh, or bash with "$map": the whole line is one argument.
	code, _, errOut := runMain(t, "https://c01.shop.test/", "--map-host '*.shop.test=127.0.0.1:23400' --map-host www.clarity.ms=127.0.0.1:23400 --map-only")
	if code != exitUsage || errOut != want {
		t.Errorf("one argument: exit %d %q\nwant %q", code, errOut, want)
	}
	// bash with $map unquoted: split into words, the quotes kept.
	code, _, errOut = runMain(t, "https://c01.shop.test/", "--map-host", "'*.shop.test=127.0.0.1:23400'", "--map-only")
	if code != exitUsage || errOut != want {
		t.Errorf("split: exit %d %q\nwant %q", code, errOut, want)
	}
	// Any other unknown flag with a space is still unknown.
	if got := quotedFlag("zzz '*.x"); got != "" {
		t.Errorf("quotedFlag of an unknown name: %q", got)
	}
	if got := quotedFlag("map-host *.x"); got != "" {
		t.Errorf("quotedFlag without a quote: %q", got)
	}
}

// A bare word with no scheme, dot or port isn't taken for a host.
func TestBareWordRefused(t *testing.T) {
	_, err := target("not-a-url", "")
	want := `consent "not-a-url": not-a-url isn't a URL; did you mean https://not-a-url.example/? A host without a dot needs its scheme: https://not-a-url/`
	if err == nil || err.Error() != want {
		t.Errorf("not-a-url: %v", err)
	}
	for _, ok := range []string{"localhost", "https://intranet/", "http://report/", "intranet:8080", "127.0.0.1", "[::1]:8443"} {
		if _, err := target(ok, ""); err != nil {
			t.Errorf("%s: %v", ok, err)
		}
	}
}
