// Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
// SPDX-License-Identifier: MIT

package verdict

import (
	"fmt"
	"slices"
	"strings"

	"github.com/RhythmShahriar/echo/consent"
)

// GCDNote is the result note every report carries when a check or a
// timeline read a decoded gcd: Google doesn't document the parameter.
const GCDNote = "gcd decoded with an unofficial, community-documented mapping."

// storageSignals are the two Consent Mode signals that decide whether
// Google may use cookies, in the order titles name them.
var storageSignals = []string{"ad_storage", "analytics_storage"}

// consentMode raises the Consent Mode checks of pg: no-default,
// default-late, v2-signals, accept-not-granted and reject-granted. A
// page without Google hits has nothing to check.
func (j *judge) consentMode(pg *consent.Page) {
	firstRun, first := pageFirstGoogleHit(pg)
	if first == nil {
		return
	}
	if pageUsesGCD(pg) && !slices.Contains(j.r.Notes, GCDNote) {
		j.r.Notes = append(j.r.Notes, GCDNote)
	}
	j.noDefaultOrLate(pg, firstRun, first)
	j.v2Signals(pg)
	if run := runOf(pg, consent.RunAccept); run != nil {
		j.acceptNotGranted(pg, run)
	}
	if run := runOf(pg, consent.RunReject); run != nil {
		j.rejectGranted(pg, run)
	}
}

// pageFirstGoogleHit is the first Google hit of the Consent Mode run,
// else of the first run (in run order) that has one.
func pageFirstGoogleHit(pg *consent.Page) (*consent.Run, *consent.HitRef) {
	if run := cmRun(pg); run != nil {
		if h := firstGoogleHit(run); h != nil {
			return run, h
		}
	}
	for i := range pg.Runs {
		if h := firstGoogleHit(&pg.Runs[i]); h != nil {
			return &pg.Runs[i], h
		}
	}
	return nil, nil
}

// pageUsesGCD reports whether any Google hit of pg carries a decoded gcd.
func pageUsesGCD(pg *consent.Page) bool {
	for _, run := range pg.Runs {
		for i := range run.Hits {
			if h := &run.Hits[i]; googleHit(h) && h.Google != nil && h.Google.GCDDecoded != nil {
				return true
			}
		}
	}
	return false
}

// hasGCS reports whether a hit carries a gcs parameter at all.
func hasGCS(h *consent.HitRef) bool { return h.Google != nil && h.Google.GCS != "" }

// noDefaultOrLate raises consent/no-default when no run saw a Consent
// Mode default and the first Google hit carries no gcs, else
// consent/default-late when the none run's default came after its first
// Google hit. The two never come together.
func (j *judge) noDefaultOrLate(pg *consent.Page, firstRun *consent.Run, first *consent.HitRef) {
	seen := slices.ContainsFunc(pg.Runs, func(run consent.Run) bool {
		return slices.ContainsFunc(run.Signals, func(s consent.Signal) bool { return consentModeCommand(&s, "default") })
	})
	if !seen {
		if hasGCS(first) {
			return // the tags say what state they ran with
		}
		detail := fmt.Sprintf("%s sent %s at %s with no gcs; no gtag('consent', 'default', ...) was seen in any run",
			j.name(first.Tracker), shortURL(first.URL), secs(first.AtMS))
		j.add(pg, consent.CheckNoDefault, firstRun.Kind, "", "Google tags ran with no Consent Mode default", detail, "", []int64{first.Seq})
		return
	}
	cm := pg.ConsentMode
	run := cmRun(pg)
	if cm == nil || cm.Default == nil || run == nil {
		return
	}
	h := firstGoogleHit(run)
	if h == nil || cm.DefaultAtMS <= h.AtMS {
		return
	}
	title := fmt.Sprintf("The Consent Mode default came %d ms after the first Google hit", cm.DefaultAtMS-h.AtMS)
	detail := fmt.Sprintf("first Google hit %s (%s) at %s; default at %s", j.name(h.Tracker), shortURL(h.URL), secs(h.AtMS), secs(cm.DefaultAtMS))
	ev := []int64{h.Seq}
	if s := defaultSignal(run.Signals, cm.Default, cm.DefaultAtMS); s != nil {
		if seq := seqOfSignal(run, s); seq > 0 {
			ev = append(ev, seq)
		}
	}
	j.add(pg, consent.CheckDefaultLate, run.Kind, "", title, detail, "", ev)
}

// gcdUnset reports whether a decoded gcd shows ad_user_data or
// ad_personalization with letter l (not set).
func gcdUnset(h *consent.HitRef) []string {
	if h.Google == nil || h.Google.GCDDecoded == nil {
		return nil
	}
	var out []string
	d := h.Google.GCDDecoded
	if d.AdUserData.Letter == "l" {
		out = append(out, "ad_user_data")
	}
	if d.AdPersonalization.Letter == "l" {
		out = append(out, "ad_personalization")
	}
	return out
}

// v2Signals raises consent/v2-signals when the default that applies
// lacks ad_user_data or ad_personalization, or a Google hit's gcd shows
// either unset.
func (j *judge) v2Signals(pg *consent.Page) {
	var parts []string
	var ev []int64
	runKind := consent.RunNone
	if run := cmRun(pg); run != nil {
		runKind = run.Kind
		if cm := pg.ConsentMode; cm != nil && cm.Default != nil {
			var missing []string
			for _, k := range []string{"ad_user_data", "ad_personalization"} {
				if !set(cm.Default[k]) {
					missing = append(missing, k)
				}
			}
			if len(missing) > 0 {
				parts = append(parts, fmt.Sprintf("the default at %s doesn't set %s", secs(cm.DefaultAtMS), joinAnd(missing)))
				if s := defaultSignal(run.Signals, cm.Default, cm.DefaultAtMS); s != nil {
					if seq := seqOfSignal(run, s); seq > 0 {
						ev = append(ev, seq)
					}
				}
			}
		}
	}
	var hitPart string
	for i := range pg.Runs {
		run := &pg.Runs[i]
		for k := range run.Hits {
			h := &run.Hits[k]
			unset := gcdUnset(h)
			if !googleHit(h) || len(unset) == 0 {
				continue
			}
			if hitPart == "" {
				hitPart = fmt.Sprintf("gcd %s on %s at %s shows %s unset (l)",
					consent.SafeText(h.Google.GCD, 40), j.name(h.Tracker), secs(h.AtMS), joinAnd(unset))
				if len(parts) == 0 {
					runKind = run.Kind
				}
			}
			ev = append(ev, h.Seq)
		}
	}
	if hitPart != "" {
		parts = append(parts, hitPart)
	}
	if len(parts) == 0 {
		return
	}
	j.add(pg, consent.CheckV2Signals, runKind, "", "Consent Mode v2 signals ad_user_data and ad_personalization were never set",
		strings.Join(parts, "; "), "", ev)
}

// grants lists the storage signals a Consent Mode update grants.
func grants(s *consent.Signal) []string {
	var out []string
	for _, k := range storageSignals {
		if s.State[k] == consent.Granted {
			out = append(out, k)
		}
	}
	return out
}

// gcsGrants lists the storage signals a hit's known gcs grants.
func gcsGrants(h *consent.HitRef) []string {
	if !grantedGCS(h.Google) {
		return nil
	}
	var out []string
	if h.Google.GCSDecoded.AdStorage == consent.Granted {
		out = append(out, "ad_storage")
	}
	if h.Google.GCSDecoded.AnalyticsStorage == consent.Granted {
		out = append(out, "analytics_storage")
	}
	return out
}

// updatesAfter lists the run's Consent Mode updates at or after atMS.
func updatesAfter(run *consent.Run, atMS int64) []*consent.Signal {
	var out []*consent.Signal
	for i := range run.Signals {
		if s := &run.Signals[i]; consentModeCommand(s, "update") && s.AtMS >= atMS {
			out = append(out, s)
		}
	}
	return out
}

// commandName writes how a Consent Mode command was sent.
func commandName(s *consent.Signal) string {
	if s.Source == "datalayer" {
		return "dataLayer.push(['consent', '" + s.Command + "'])"
	}
	return "gtag('consent', '" + s.Command + "')"
}

// acceptNotGranted raises consent/accept-not-granted: Accept was
// confirmed, no update granted ad_storage or analytics_storage and every
// Google hit after the choice still says both are denied. A gcs Echo
// can't read is never "denied".
func (j *judge) acceptNotGranted(pg *consent.Page, run *consent.Run) {
	c := run.Choice
	if c == nil || !c.Verified {
		return
	}
	for _, s := range updatesAfter(run, c.AtMS) {
		if len(grants(s)) > 0 {
			return
		}
	}
	var hits []*consent.HitRef
	for i := range run.Hits {
		h := &run.Hits[i]
		if !googleHit(h) || h.Phase != consent.PhaseAfterAccept {
			continue
		}
		if !deniedGCS(h.Google) {
			return
		}
		hits = append(hits, h)
	}
	if len(hits) == 0 {
		return
	}
	var ev []int64
	for _, h := range hits {
		ev = append(ev, h.Seq)
	}
	detail := fmt.Sprintf("%s after Accept, the first at %s, carried gcs %s; no Consent Mode update granted ad_storage or analytics_storage",
		plural(len(hits), "Google hit"), secs(hits[0].AtMS), consent.SafeText(hits[0].Google.GCS, 16))
	j.add(pg, consent.CheckAcceptNotGranted, run.Kind, "", "After Accept, Google was never told storage is granted", detail, "", ev)
}

// rejectGranted raises consent/reject-granted: after the Reject click a
// Consent Mode update granted ad_storage or analytics_storage, or a
// Google hit's known gcs says one is granted.
func (j *judge) rejectGranted(pg *consent.Page, run *consent.Run) {
	c := run.Choice
	if c == nil {
		return
	}
	var granted, parts []string
	var ev []int64
	addSignals := func(names []string) {
		for _, n := range names {
			if !slices.Contains(granted, n) {
				granted = append(granted, n)
			}
		}
	}
	for _, s := range updatesAfter(run, c.AtMS) {
		g := grants(s)
		if len(g) == 0 {
			continue
		}
		addSignals(g)
		parts = append(parts, fmt.Sprintf("%s at %s granted %s", commandName(s), secs(s.AtMS), joinAnd(g)))
		if seq := seqOfSignal(run, s); seq > 0 {
			ev = append(ev, seq)
		}
	}
	var hits []*consent.HitRef
	for i := range run.Hits {
		h := &run.Hits[i]
		if googleHit(h) && h.Phase == consent.PhaseAfterReject && len(gcsGrants(h)) > 0 {
			addSignals(gcsGrants(h))
			hits = append(hits, h)
			ev = append(ev, h.Seq)
		}
	}
	switch n := len(hits); {
	case n == 1:
		parts = append(parts, fmt.Sprintf("%s hit with gcs %s at %s", j.name(hits[0].Tracker), consent.SafeText(hits[0].Google.GCS, 16), secs(hits[0].AtMS)))
	case n > 1:
		parts = append(parts, fmt.Sprintf("%d Google hits with a granted gcs, the first %s with gcs %s at %s",
			n, j.name(hits[0].Tracker), consent.SafeText(hits[0].Google.GCS, 16), secs(hits[0].AtMS)))
	}
	if len(granted) == 0 {
		return
	}
	slices.SortFunc(granted, func(a, b string) int { return slices.Index(storageSignals, a) - slices.Index(storageSignals, b) })
	verb := " is granted"
	if len(granted) > 1 {
		verb = " are granted"
	}
	j.add(pg, consent.CheckRejectGranted, run.Kind, "", "After Reject, Google was told "+joinAnd(granted)+verb,
		strings.Join(parts, "; "), "", ev)
}

// seqOfSignal finds the event number of a recorded signal.
func seqOfSignal(run *consent.Run, s *consent.Signal) int64 {
	for _, ev := range run.Events {
		if ev.Kind == consent.EvSignal && ev.Signal != nil && ev.Signal.Source == s.Source &&
			ev.Signal.Command == s.Command && ev.Signal.AtMS == s.AtMS {
			return ev.Seq
		}
	}
	return 0
}
