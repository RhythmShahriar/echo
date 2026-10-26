// Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
// SPDX-License-Identifier: MIT

package consent_test

import (
	"slices"
	"testing"

	"github.com/RhythmShahriar/echo/consent"
)

func TestParseRuns(t *testing.T) {
	R := func(ks ...consent.RunKind) []consent.RunKind { return ks }
	for _, c := range []struct {
		in   string
		want []consent.RunKind
	}{
		{"", consent.DefaultRuns},
		{"none,accept,reject", consent.DefaultRuns},
		{"+gpc", R(consent.RunNone, consent.RunAccept, consent.RunReject, consent.RunGPC)},
		{"+gpc,+withdraw", consent.AllRuns},
		{"+withdraw", R(consent.RunNone, consent.RunAccept, consent.RunReject, consent.RunWithdraw)},
		{"gpc", R(consent.RunGPC)},
		{"reject, none", R(consent.RunNone, consent.RunReject)},
		{"accept,+gpc", R(consent.RunAccept, consent.RunGPC)},
		{"Reject,REJECT", R(consent.RunReject)},
	} {
		got, err := consent.ParseRuns(c.in)
		if err != nil || !slices.Equal(got, c.want) {
			t.Errorf("ParseRuns(%q) = %v, %v; want %v", c.in, got, err, c.want)
		}
	}
	for _, c := range []struct{ in, msg string }{
		{"decline", `--runs "decline": want none, accept, reject, gpc or withdraw (+gpc adds one); did you mean reject?`},
		{"none,rejct", `--runs "rejct": want none, accept, reject, gpc or withdraw (+gpc adds one); did you mean reject?`},
		{"+allow", `--runs "+allow": want none, accept, reject, gpc or withdraw (+gpc adds one); did you mean accept?`},
		{"zzzzzz", `--runs "zzzzzz": want none, accept, reject, gpc or withdraw (+gpc adds one)`},
		{"none,,reject", `--runs "": want none, accept, reject, gpc or withdraw (+gpc adds one)`},
		{"+", `--runs "+": want none, accept, reject, gpc or withdraw (+gpc adds one)`},
	} {
		_, err := consent.ParseRuns(c.in)
		if err == nil || err.Error() != c.msg {
			t.Errorf("ParseRuns(%q) err = %v; want %s", c.in, err, c.msg)
		}
	}
	// DefaultRuns itself is never handed out to be changed.
	got, _ := consent.ParseRuns("")
	got[0] = consent.RunGPC
	if consent.DefaultRuns[0] != consent.RunNone {
		t.Fatal("ParseRuns returned DefaultRuns itself")
	}
}

func TestRunKindLabel(t *testing.T) {
	want := []string{"No choice", "Accept", "Reject", "GPC", "Withdraw"}
	for i, k := range consent.AllRuns {
		if got := k.Label(); got != want[i] {
			t.Errorf("%s.Label() = %q; want %q", k, got, want[i])
		}
	}
	if got := consent.RunKind("other").Label(); got != "other" {
		t.Errorf("unknown kind label = %q", got)
	}
}

func TestParsePolicy(t *testing.T) {
	for in, want := range map[string]consent.Policy{"": consent.PolicyAdvanced, "advanced": consent.PolicyAdvanced,
		"basic": consent.PolicyBasic, "Strict": consent.PolicyStrict, " basic ": consent.PolicyBasic} {
		if got, err := consent.ParsePolicy(in); err != nil || got != want {
			t.Errorf("ParsePolicy(%q) = %q, %v; want %q", in, got, err, want)
		}
	}
	for _, in := range []string{"loose", "none", "advanced,basic"} {
		_, err := consent.ParsePolicy(in)
		if err == nil || err.Error() != `--consent-mode "`+in+`": want advanced, basic or strict` {
			t.Errorf("ParsePolicy(%q) err = %v", in, err)
		}
	}
}

func TestParseClickMode(t *testing.T) {
	for in, want := range map[string]consent.ClickMode{"": consent.ClickButton, "button": consent.ClickButton, "api": consent.ClickAPI,
		"button,api": consent.ClickButtonAPI, "button, api": consent.ClickButtonAPI} {
		if got, err := consent.ParseClickMode(in); err != nil || got != want {
			t.Errorf("ParseClickMode(%q) = %q, %v; want %q", in, got, err, want)
		}
	}
	for _, in := range []string{"mouse", "api,button", "button,"} {
		_, err := consent.ParseClickMode(in)
		if err == nil || err.Error() != `--click "`+in+`": want button, api or button,api` {
			t.Errorf("ParseClickMode(%q) err = %v", in, err)
		}
	}
}
