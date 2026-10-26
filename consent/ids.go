// Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
// SPDX-License-Identifier: MIT

package consent

import (
	"fmt"
	"slices"
	"strings"
)

// RunKind names one run of a page: the choice the visitor makes.
type RunKind string

// The run kinds, in the order a check makes them.
const (
	RunNone     RunKind = "none"     // no choice: the banner is left alone
	RunAccept   RunKind = "accept"   // Accept on the banner
	RunReject   RunKind = "reject"   // Reject on the banner
	RunGPC      RunKind = "gpc"      // no choice with Global Privacy Control on
	RunWithdraw RunKind = "withdraw" // Accept, then Reject in the reopened banner
)

// DefaultRuns are the runs a check makes when --runs isn't given.
var DefaultRuns = []RunKind{RunNone, RunAccept, RunReject}

// AllRuns lists every run kind, in the order a check makes them.
var AllRuns = []RunKind{RunNone, RunAccept, RunReject, RunGPC, RunWithdraw}

// runWords are the words people try for a run, mapped to the one meant,
// so a usage error can say "did you mean reject?" for "decline".
var runWords = map[string]RunKind{
	"decline": RunReject, "deny": RunReject, "refuse": RunReject, "reject-all": RunReject, "rejectall": RunReject, "necessary": RunReject,
	"allow": RunAccept, "agree": RunAccept, "accept-all": RunAccept, "acceptall": RunAccept, "consent": RunAccept, "grant": RunAccept,
	"nothing": RunNone, "no-choice": RunNone, "nochoice": RunNone, "default": RunNone, "baseline": RunNone, "before": RunNone,
	"revoke": RunWithdraw, "withdrawal": RunWithdraw, "retract": RunWithdraw,
	"dnt": RunGPC, "global-privacy-control": RunGPC, "opt-out": RunGPC,
}

// ParseRuns reads --runs: "none,accept,reject", "+gpc", "+gpc,+withdraw",
// "gpc". A word with + adds that run to the plain words given, or to
// DefaultRuns when there are none; plain words alone replace the
// default. The result is in AllRuns order without repeats. An empty s is
// DefaultRuns. Errors are usage text: `--runs "decline": want none,
// accept, reject, gpc or withdraw (+gpc adds one); did you mean reject?`
func ParseRuns(s string) ([]RunKind, error) {
	if strings.TrimSpace(s) == "" {
		return slices.Clone(DefaultRuns), nil
	}
	var plain, added []RunKind
	for w := range strings.SplitSeq(s, ",") {
		w = strings.TrimSpace(w)
		add := strings.HasPrefix(w, "+")
		word := strings.ToLower(strings.TrimPrefix(w, "+"))
		k := RunKind(word)
		if !slices.Contains(AllRuns, k) {
			msg := fmt.Sprintf("--runs %q: want none, accept, reject, gpc or withdraw (+gpc adds one)", w)
			if m := suggestRun(word); m != "" {
				msg += "; did you mean " + string(m) + "?"
			}
			return nil, fmt.Errorf("%s", msg)
		}
		if add {
			added = append(added, k)
		} else {
			plain = append(plain, k)
		}
	}
	base := plain
	if len(base) == 0 {
		base = DefaultRuns
	}
	var out []RunKind
	for _, k := range AllRuns {
		if slices.Contains(base, k) || slices.Contains(added, k) {
			out = append(out, k)
		}
	}
	return out, nil
}

// suggestRun names the run meant by word: a known synonym, or a run at
// most two edits away.
func suggestRun(word string) RunKind {
	if k, ok := runWords[word]; ok {
		return k
	}
	if word == "" {
		return ""
	}
	for _, k := range AllRuns {
		if editDistance(word, string(k)) <= 2 {
			return k
		}
	}
	return ""
}

// editDistance is the Levenshtein distance between a and b, by bytes;
// the words it compares are ASCII.
func editDistance(a, b string) int {
	prev := make([]int, len(b)+1)
	cur := make([]int, len(b)+1)
	for j := range prev {
		prev[j] = j
	}
	for i := 1; i <= len(a); i++ {
		cur[0] = i
		for j := 1; j <= len(b); j++ {
			cost := 1
			if a[i-1] == b[j-1] {
				cost = 0
			}
			cur[j] = min(prev[j]+1, cur[j-1]+1, prev[j-1]+cost)
		}
		prev, cur = cur, prev
	}
	return prev[len(b)]
}

// Label is the run's name in output: "No choice", "Accept", "Reject",
// "GPC" or "Withdraw". An unknown kind is shown as it is.
func (k RunKind) Label() string {
	switch k {
	case RunNone:
		return "No choice"
	case RunAccept:
		return "Accept"
	case RunReject:
		return "Reject"
	case RunGPC:
		return "GPC"
	case RunWithdraw:
		return "Withdraw"
	}
	return string(k)
}

// Phase is when an event happened in a run, relative to the choice.
type Phase string

// The phases of a run.
const (
	PhaseBefore        Phase = "before"         // before any choice (the whole of a none run)
	PhaseAfterAccept   Phase = "after-accept"   // from the Accept click on
	PhaseAfterReject   Phase = "after-reject"   // from the Reject click on
	PhaseGPC           Phase = "gpc"            // the whole gpc run
	PhaseAfterWithdraw Phase = "after-withdraw" // from the withdraw Reject click on
)

// Policy says how Google hits sent with storage denied count.
type Policy string

// The --consent-mode policies.
const (
	PolicyAdvanced Policy = "advanced" // default: cookieless pings are a notice
	PolicyBasic    Policy = "basic"    // cookieless pings count as hits
	PolicyStrict   Policy = "strict"   // as basic; tracker scripts before the choice or after Reject are errors too
)

// ParsePolicy reads --consent-mode. An empty s is PolicyAdvanced.
// Errors are usage text: `--consent-mode "loose": want advanced, basic
// or strict`.
func ParsePolicy(s string) (Policy, error) {
	switch p := Policy(strings.ToLower(strings.TrimSpace(s))); p {
	case "":
		return PolicyAdvanced, nil
	case PolicyAdvanced, PolicyBasic, PolicyStrict:
		return p, nil
	}
	return "", fmt.Errorf("--consent-mode %q: want advanced, basic or strict", s)
}

// ClickMode says how a choice is made: with the button, the CMP's API or
// the button with the API as a fallback.
type ClickMode string

// The --click modes.
const (
	ClickButton    ClickMode = "button"     // real mouse events on the visible button
	ClickAPI       ClickMode = "api"        // the CMP's API with fixed arguments
	ClickButtonAPI ClickMode = "button,api" // the button, then the API when no button is found
)

// ParseClickMode reads --click. An empty s is ClickButton. Errors are
// usage text: `--click "mouse": want button, api or button,api`.
func ParseClickMode(s string) (ClickMode, error) {
	norm := strings.ToLower(strings.ReplaceAll(strings.TrimSpace(s), " ", ""))
	switch m := ClickMode(norm); m {
	case "":
		return ClickButton, nil
	case ClickButton, ClickAPI, ClickButtonAPI:
		return m, nil
	}
	return "", fmt.Errorf("--click %q: want button, api or button,api", s)
}

// Category is what a tracker is for, as the catalog files it.
type Category string

// The tracker categories.
const (
	CatAnalytics     Category = "analytics"
	CatAdvertising   Category = "advertising"
	CatSessionReplay Category = "session-replay"
	CatSocial        Category = "social"
	CatEmbed         Category = "embed"
	CatFunctional    Category = "functional"
	CatNecessary     Category = "necessary"
)

// ConsentNeed says whether a tracker needs consent before it may run.
type ConsentNeed string

// The consent needs.
const (
	NeedNeeded ConsentNeed = "needed" // consent first
	NeedExempt ConsentNeed = "exempt" // allowed before consent (cookieless analytics, --exempt)
	NeedNo     ConsentNeed = "no"     // strictly necessary: never needs it
)

// HitKind says what a tracker request was.
type HitKind string

// The hit kinds.
const (
	HitBeacon HitKind = "beacon" // a request that sends data: a pixel, collect, sendBeacon
	HitScript HitKind = "script" // the tracker's script loading
	HitFrame  HitKind = "frame"  // an iframe: an embed or a sync frame
)

// RunStatus is how a run ended.
type RunStatus string

// The run statuses.
const (
	StatusOK            RunStatus = "ok"
	StatusNoBanner      RunStatus = "no-banner"
	StatusNoButton      RunStatus = "no-button"
	StatusClickFailed   RunStatus = "click-failed"
	StatusLoadFailed    RunStatus = "load-failed"
	StatusTimeout       RunStatus = "timeout"
	StatusNavigatedAway RunStatus = "navigated-away"
	StatusLimit         RunStatus = "limit-reached"
	StatusCanceled      RunStatus = "canceled"
)

// Reason is why a run is inconclusive (PRD 9.3); ReasonNone when it isn't.
type Reason string

// The inconclusive reasons.
const (
	ReasonNone           Reason = ""
	ReasonNoBanner       Reason = "no_banner"
	ReasonCMPNotFound    Reason = "cmp_not_found"
	ReasonNoButton       Reason = "no_button"
	ReasonClickMissed    Reason = "click_missed"
	ReasonStateUnchanged Reason = "state_unchanged"
	ReasonNavigation     Reason = "navigation_failed"
	ReasonTimeout        Reason = "timeout"
	ReasonLimit          Reason = "limit"
	ReasonCanceled       Reason = "canceled"
)

// Value is a consent signal's value.
type Value string

// The signal values. Unknown is a value Echo couldn't read; it never
// counts as granted.
const (
	Granted Value = "granted"
	Denied  Value = "denied"
	Unset   Value = "unset"
	Unknown Value = "unknown"
)
