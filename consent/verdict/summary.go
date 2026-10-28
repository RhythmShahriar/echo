// Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
// SPDX-License-Identifier: MIT

package verdict

import (
	"maps"
	"math"
	"strings"

	"github.com/RhythmShahriar/echo/consent"
)

// NothingToConsent is the detail of an Accept, Reject or withdraw run on
// a page that needs no consent (PRD 11.2): it ended no-banner and is
// not inconclusive.
const NothingToConsent = "No banner and no trackers: nothing to consent to"

// cookielessWindow is how long after a Google hit a Google cookie still
// makes it a hit with cookies: tags write _ga a few ms after the first
// collect, so a cookie close behind the ping belongs to it.
const cookielessWindow = 1000

// summarize fills the page's Consent Mode summary, marks cookieless
// Google hits and decides NeedsNone. Every check reads what it sets, so
// Evaluate calls it first for every page.
func (j *judge) summarize(pg *consent.Page) {
	pg.ConsentMode = j.consentModeSummary(pg)
	for i := range pg.Runs {
		markCookieless(&pg.Runs[i])
	}
	pg.NeedsNone = j.needsNone(pg)
	if !pg.NeedsNone {
		return
	}
	for i := range pg.Runs {
		run := &pg.Runs[i]
		switch run.Kind {
		case consent.RunAccept, consent.RunReject, consent.RunWithdraw:
			if run.Status == consent.StatusNoBanner {
				run.Reason, run.Detail = consent.ReasonNone, NothingToConsent
			}
		}
	}
}

// cmRun is the run the Consent Mode summary is read from: the none run,
// else the first run that shows any Consent Mode signal or Google hit,
// else the first run.
func cmRun(pg *consent.Page) *consent.Run {
	if run := runOf(pg, consent.RunNone); run != nil {
		return run
	}
	for i := range pg.Runs {
		if hasConsentMode(&pg.Runs[i]) {
			return &pg.Runs[i]
		}
	}
	if len(pg.Runs) > 0 {
		return &pg.Runs[0]
	}
	return nil
}

// consentModeCommand reports whether s is a Consent Mode default or
// update (gtag or dataLayer).
func consentModeCommand(s *consent.Signal, command string) bool {
	return (s.Source == "gtag" || s.Source == "datalayer") && (command == "" || s.Command == command) &&
		(s.Command == "default" || s.Command == "update")
}

// hasConsentMode reports whether run saw a Consent Mode command or a
// Google hit carrying gcs or gcd.
func hasConsentMode(run *consent.Run) bool {
	for i := range run.Signals {
		if consentModeCommand(&run.Signals[i], "") {
			return true
		}
	}
	for i := range run.Hits {
		h := &run.Hits[i]
		if googleHit(h) && h.Google != nil && (h.Google.GCS != "" || h.Google.GCD != "") {
			return true
		}
	}
	return false
}

// firstGoogleHit is the run's earliest Google hit, or nil.
func firstGoogleHit(run *consent.Run) *consent.HitRef {
	var first *consent.HitRef
	for i := range run.Hits {
		h := &run.Hits[i]
		if googleHit(h) && (first == nil || h.AtMS < first.AtMS) {
			first = h
		}
	}
	return first
}

// consentModeSummary reads the page's Consent Mode setup from cmRun,
// with the default that applies to the --geo region.
func (j *judge) consentModeSummary(pg *consent.Page) *consent.ConsentMode {
	cm := &consent.ConsentMode{}
	run := cmRun(pg)
	if run == nil {
		return cm
	}
	cm.Present = hasConsentMode(run)
	state, at, ok := pageDefault(run, j.geo)
	first := firstGoogleHit(run)
	if first != nil {
		cm.FirstGoogleHitMS = first.AtMS
	}
	if !ok {
		return cm
	}
	cm.Default, cm.DefaultAtMS = state, at
	cm.V2 = set(state["ad_user_data"]) && set(state["ad_personalization"])
	cm.DefaultBeforeFirstHit = first == nil || at <= first.AtMS
	if s := defaultSignal(run.Signals, state, at); s != nil {
		cm.WaitForUpdateMS = s.WaitForUpdateMS
		cm.Region = append([]string(nil), s.Region...)
	}
	return cm
}

// pageDefault is the Consent Mode default of the run's first page load
// that sent one, for region. Each visit (--pages, the reload after the
// choice) is a new page that sends its own default again, which doesn't
// replace an earlier page's: Google's "the last default wins" holds
// within one page, so taking it across visits would put the default
// seconds after the first Google hit.
//
// A run interrupted while it loaded a second page may hold that page's
// signals without its visit: with one visit, a canceled run takes the
// earliest default and what came with it, so a later load's default
// isn't read as the first page's, seconds late.
func pageDefault(run *consent.Run, region string) (map[string]consent.Value, int64, bool) {
	if len(run.Visits) < 2 {
		if run.Reason != consent.ReasonCanceled {
			return consent.EffectiveDefault(run.Signals, region)
		}
		first := int64(math.MaxInt64)
		for i := range run.Signals {
			if s := &run.Signals[i]; consentModeCommand(s, "default") && s.AtMS < first {
				first = s.AtMS
			}
		}
		var sigs []consent.Signal
		for _, s := range run.Signals {
			if s.AtMS <= first {
				sigs = append(sigs, s)
			}
		}
		return consent.EffectiveDefault(sigs, region)
	}
	for i, v := range run.Visits {
		end := int64(math.MaxInt64)
		if i+1 < len(run.Visits) {
			end = run.Visits[i+1].AtMS
		}
		var sigs []consent.Signal
		for _, s := range run.Signals {
			if s.AtMS >= v.AtMS && s.AtMS < end || i == 0 && s.AtMS < v.AtMS {
				sigs = append(sigs, s)
			}
		}
		if state, at, ok := consent.EffectiveDefault(sigs, region); ok {
			return state, at, true
		}
	}
	return nil, 0, false
}

// set reports whether a Consent Mode value was given at all.
func set(v consent.Value) bool { return v != "" && v != consent.Unset }

// defaultSignal finds the default command EffectiveDefault picked (the
// last one at that time with that state), for its wait and region.
func defaultSignal(signals []consent.Signal, state map[string]consent.Value, at int64) *consent.Signal {
	var pick *consent.Signal
	for i := range signals {
		s := &signals[i]
		if consentModeCommand(s, "default") && s.AtMS == at && maps.Equal(s.State, state) {
			pick = s
		}
	}
	return pick
}

// googleCookie reports whether a cookie name is one Google's tags write:
// _ga and _ga_<id>, _gid, _gcl_*, FPID (server-side tagging) and IDE.
func googleCookie(name string) bool {
	return strings.HasPrefix(name, "_ga") || name == "_gid" || strings.HasPrefix(name, "_gcl_") ||
		name == "FPID" || name == "IDE"
}

// markCookieless sets Hit.Cookieless on every Google hit sent with
// ad_storage and analytics_storage denied when no Google cookie was
// written in the same phase before it or within cookielessWindow after
// it. The events in memory get the same mark.
func markCookieless(run *consent.Run) {
	marks := map[int64]bool{}
	for i := range run.Hits {
		h := &run.Hits[i]
		h.Cookieless = googleHit(h) && deniedGCS(h.Google) && !googleCookieBy(run, h.Phase, h.AtMS+cookielessWindow)
		marks[h.Seq] = h.Cookieless
	}
	for i := range run.Events {
		ev := &run.Events[i]
		if ev.Hit != nil {
			if m, ok := marks[ev.Seq]; ok {
				ev.Hit.Cookieless = m
			}
		}
	}
}

// deniedGCS reports whether g carries a known gcs with both storage
// signals denied.
func deniedGCS(g *consent.GoogleConsent) bool {
	return g != nil && g.GCSDecoded != nil && g.GCSDecoded.Known &&
		g.GCSDecoded.AdStorage == consent.Denied && g.GCSDecoded.AnalyticsStorage == consent.Denied
}

// googleCookieBy reports whether the run stored a Google cookie in phase
// at or before atMS. A cookie the browser refused was never stored.
func googleCookieBy(run *consent.Run, phase consent.Phase, atMS int64) bool {
	for _, c := range run.Cookies {
		if c.Blocked == "" && c.Phase == phase && c.AtMS <= atMS && googleCookie(c.Name) {
			return true
		}
	}
	return false
}

// needsNone applies PRD 11.2: no run found a banner, no CMP is on the
// page and the none run had no consent-required hit, cookie, storage key
// or tracker script. Without a none run, the before phase of every run
// is read instead.
func (j *judge) needsNone(pg *consent.Page) bool {
	if pg.CMP.Found || pg.CMP.Present {
		return false
	}
	for _, run := range pg.Runs {
		if run.Choice != nil {
			return false
		}
		for _, v := range run.Visits {
			if v.Banner {
				return false
			}
		}
	}
	runs := pg.Runs
	none := runOf(pg, consent.RunNone)
	if none != nil {
		runs = []consent.Run{*none}
	}
	for _, run := range runs {
		counts := func(p consent.Phase) bool { return none != nil || p == consent.PhaseBefore }
		for _, h := range run.Hits {
			if counts(h.Phase) && j.needed(h.Tracker, h.Need) {
				return false
			}
		}
		for _, c := range run.Cookies {
			if counts(c.Phase) && c.Tracker != "" && j.needed(c.Tracker, c.Need) {
				return false
			}
		}
		for _, k := range run.Storage {
			if counts(k.Phase) && k.Tracker != "" && j.needed(k.Tracker, k.Need) {
				return false
			}
		}
		for _, s := range run.Scripts {
			if counts(s.Phase) && j.needed(s.Tracker, "") {
				return false
			}
		}
	}
	return true
}

// needed reports whether an entry of tracker id with recorded need n
// needs consent: --exempt ids never do; an entry without a need takes
// the tracker's.
func (j *judge) needed(id string, n consent.ConsentNeed) bool {
	if j.exempt[id] {
		return false
	}
	if n == "" {
		n = j.need(id)
	}
	return n == consent.NeedNeeded
}
