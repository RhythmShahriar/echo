// Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
// SPDX-License-Identifier: MIT

package verdict

import (
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/RhythmShahriar/echo/consent"
)

// choices raises the checks on how the choice was made and kept
// (reject-not-kept, reject-broken, no-first-layer-reject, no-banner,
// click-fallback, tcf-mismatch, server-side) and lists the page's
// inconclusive runs. It runs after the tracker checks: no-banner names
// their trackers.
func (j *judge) choices(pg *consent.Page) {
	if run := runOf(pg, consent.RunReject); run != nil {
		j.rejectNotKept(pg, run)
		j.rejectBroken(pg, run)
		j.noFirstLayerReject(pg, run)
	}
	j.noBanner(pg)
	for i := range pg.Runs {
		run := &pg.Runs[i]
		j.clickFallback(pg, run)
		j.tcfMismatch(pg, run)
		j.serverSide(pg, run)
	}
	for i := range pg.Runs {
		run := &pg.Runs[i]
		if run.Inconclusive() {
			j.r.Inconclusive = append(j.r.Inconclusive, consent.Inconclusive{
				Page: j.pageName(pg), Site: pg.Site, Run: run.Kind, Reason: run.Reason, Detail: j.inconclusiveDetail(pg, run),
			})
		}
	}
}

// rejectNotKept raises consent/reject-not-kept: after a confirmed
// Reject, a later page or the reload showed the banner again, or the
// CMP's state read after the reload is undecided or grants a category
// the visitor rejected.
func (j *judge) rejectNotKept(pg *consent.Page, run *consent.Run) {
	c := run.Choice
	if c == nil || !c.Verified {
		return // a banner that never closed is click_missed
	}
	var parts []string
	title := ""
	for _, v := range run.Visits {
		if v.Step == "load" || v.AtMS <= c.AtMS || !v.Banner {
			continue
		}
		p := pagePath(v.URL)
		if title == "" {
			title = "After Reject, the banner came back on " + p
		}
		what := "the next page"
		if v.Step == "reload" {
			what = "the reload"
		}
		parts = append(parts, fmt.Sprintf("the banner showed again on %s (%s) at %s", what, p, secs(v.AtMS)))
	}
	if fs := run.FinalState; fs != nil {
		last := pagePath(pg.URL)
		if n := len(run.Visits); n > 0 {
			last = pagePath(run.Visits[n-1].URL)
		}
		var why string
		if !fs.Decided {
			why = "has no choice"
		} else if c.State != nil {
			var granted []string
			for cat, ok := range c.State.Categories {
				if !ok && fs.Categories[cat] {
					granted = append(granted, cat)
				}
			}
			slices.Sort(granted)
			if len(granted) > 0 {
				why = "grants " + joinAnd(granted)
			}
		}
		if why != "" {
			if title == "" {
				title = "After Reject, the CMP forgot the choice on " + last
			}
			src := consent.SafeText(fs.Source, 80)
			if src == "" {
				src = "the CMP's state"
			}
			parts = append(parts, fmt.Sprintf("after the reload %s %s", src, why))
		}
	}
	if title == "" {
		return
	}
	j.add(pg, consent.CheckRejectNotKept, run.Kind, "", title, strings.Join(parts, "; "), "", nil)
}

// rejectBroken raises consent/reject-broken: the Reject button was
// clicked without effect and only the CMP's API could reject.
func (j *judge) rejectBroken(pg *consent.Page, run *consent.Run) {
	c := run.Choice
	if c == nil || !c.APIWorked {
		return
	}
	detail := fmt.Sprintf("clicked %s %s and the choice wasn't confirmed; %s's API reject worked",
		buttonName(c), times(c.Clicks), cmpName(pg))
	j.add(pg, consent.CheckRejectBroken, run.Kind, "", "The Reject button doesn't work; only the CMP's API could reject", detail, "", nil)
}

// noFirstLayerReject raises consent/no-first-layer-reject: Reject was
// found only after opening the banner's settings. A --reject-selector of
// one step names a first-layer button, so it is never raised then.
func (j *judge) noFirstLayerReject(pg *consent.Page, run *consent.Run) {
	c := run.Choice
	if c == nil || c.Layer < 2 {
		return
	}
	if j.p != nil && len(j.p.RejectSelector) == 1 {
		return
	}
	detail := fmt.Sprintf("Reject was on layer %d: Echo clicked %s", c.Layer, buttonName(c))
	j.add(pg, consent.CheckNoFirstLayerReject, run.Kind, "", "Reject isn't on the banner: Echo had to open settings", detail, "", nil)
}

// bannerShown reports whether any run of pg saw the banner.
func bannerShown(pg *consent.Page) bool {
	if pg.CMP.Found {
		return true
	}
	for _, run := range pg.Runs {
		for _, v := range run.Visits {
			if v.Banner {
				return true
			}
		}
	}
	return false
}

// noBanner raises consent/no-banner: no banner showed while trackers
// that need consent fired (the page's before-choice issues).
func (j *judge) noBanner(pg *consent.Page) {
	if bannerShown(pg) {
		return
	}
	path := j.pageName(pg)
	var names []string
	var ev []int64
	for _, is := range j.r.Issues {
		if is.Check != consent.CheckBeforeChoice || is.Page != path || is.Site != pg.Site {
			continue
		}
		if n := j.name(is.Tracker); !slices.Contains(names, n) {
			names = append(names, n)
		}
		for _, s := range is.Evidence {
			if !slices.Contains(ev, s) {
				ev = append(ev, s)
			}
		}
	}
	if len(names) == 0 {
		return
	}
	verb := " are"
	if len(names) == 1 {
		verb = " is"
	}
	who := joinAnd(names) + verb + " on the page but no consent banner showed"
	detail := who + " within " + durationText(j.bannerWait()) + ". Many banners show only to visitors in some countries: try --geo de, " +
		"or an EU --proxy. A banner Echo doesn't know: --accept-selector and --reject-selector"
	j.add(pg, consent.CheckNoBanner, consent.RunNone, "", who, detail, "", ev)
}

// clickFallback raises consent/click-fallback when the CMP's API made a
// choice the button couldn't.
func (j *judge) clickFallback(pg *consent.Page, run *consent.Run) {
	fell := false
	for _, c := range []*consent.Choice{run.Choice, run.Withdraw} {
		fell = fell || (c != nil && c.Fallback)
	}
	if !fell {
		return
	}
	title := "Echo couldn't click the button and used " + cmpName(pg) + "'s API, so the button itself wasn't tested"
	detail := fmt.Sprintf("%s: no %s button was found with --click button,api", run.Kind.Label(), choiceWord(run.Kind))
	j.add(pg, consent.CheckClickFallback, run.Kind, "", title, detail, "", nil)
}

// tcfMismatch raises consent/tcf-mismatch: on a TCF CMP, the last TC
// string read after the choice grants purposes after Reject or none
// after Accept. A string Echo couldn't read is a note instead.
func (j *judge) tcfMismatch(pg *consent.Page, run *consent.Run) {
	if !pg.CMP.TCF || run.Choice == nil || (run.Kind != consent.RunAccept && run.Kind != consent.RunReject) {
		return
	}
	var last *consent.Signal
	for i := range run.Signals {
		if s := &run.Signals[i]; s.Source == "tcf" && s.AtMS >= run.Choice.AtMS {
			last = s
		}
	}
	if last == nil {
		return
	}
	if last.TCF == nil || !last.TCF.Valid {
		note := fmt.Sprintf("%s on %s: the TCF string after the choice couldn't be read, so consent/tcf-mismatch wasn't checked", run.Kind.Label(), j.pageName(pg))
		if !slices.Contains(j.r.Notes, note) {
			j.r.Notes = append(j.r.Notes, note)
		}
		return
	}
	purposes := last.TCF.PurposesConsent
	var title string
	switch {
	case run.Kind == consent.RunReject && len(purposes) > 0:
		var list []string
		for _, p := range purposes {
			list = append(list, fmt.Sprint(p))
		}
		title = "The TCF string grants purposes " + joinAnd(list) + " after Reject"
	case run.Kind == consent.RunAccept && len(purposes) == 0:
		title = "The TCF string grants no purpose after Accept"
	default:
		return
	}
	detail := fmt.Sprintf("TC string read at %s (CMP %d, %d vendors with consent)", secs(last.AtMS), last.TCF.CMPID, last.TCF.VendorsConsent)
	var ev []int64
	if seq := seqOfSignal(run, last); seq > 0 {
		ev = []int64{seq}
	}
	j.add(pg, consent.CheckTCFMismatch, run.Kind, "", title, detail, "", ev)
}

// serverProviders name the server-side APIs Echo catches.
var serverProviders = map[string]string{"meta-capi": "Meta Conversions API", "ga4-mp": "GA4 Measurement Protocol"}

// serverSide raises consent/server-side: Echo caught a server-side event
// tagged with this run after the Reject or withdraw click.
func (j *judge) serverSide(pg *consent.Page, run *consent.Run) {
	type msgs struct {
		events []string
		ids    []string
		first  int64
	}
	var order []string
	by := map[string]*msgs{}
	for _, m := range run.Server {
		if m.Phase != consent.PhaseAfterReject && m.Phase != consent.PhaseAfterWithdraw {
			continue
		}
		g := by[m.Provider]
		if g == nil {
			g = &msgs{first: m.AtMS}
			by[m.Provider] = g
			order = append(order, m.Provider)
		}
		if e := consent.SafeText(m.Event, 60); e != "" && !slices.Contains(g.events, e) {
			g.events = append(g.events, e)
		}
		g.ids = append(g.ids, consent.SafeText(m.ID, 40))
	}
	when := "after Reject"
	if run.Kind == consent.RunWithdraw {
		when = "after consent was withdrawn"
	}
	for _, prov := range order {
		g := by[prov]
		name := serverProviders[prov]
		if name == "" {
			name = consent.SafeText(prov, 40)
		}
		event := joinAnd(g.events)
		if event == "" {
			event = "an event"
		}
		title := fmt.Sprintf("%s received %s from the server %s", name, event, when)
		detail := fmt.Sprintf("%s caught by Echo, the first at %s (%s)", plural(len(g.ids), "message"), secs(g.first), strings.Join(firstN(g.ids, 5), ", "))
		j.add(pg, consent.CheckServerSide, run.Kind, prov, title, detail, "", nil)
	}
}

// firstN returns at most n items of s.
func firstN(s []string, n int) []string {
	if len(s) > n {
		return append(slices.Clone(s[:n]), "...")
	}
	return s
}

// cmpName is the page's CMP name for messages, "the CMP" when none was
// named.
func cmpName(pg *consent.Page) string {
	if n := consent.SafeText(pg.CMP.Name, 60); n != "" {
		return n
	}
	return "the CMP"
}

// choiceWord is the button a run kind looks for: Accept, or Reject for
// the reject and withdraw runs.
func choiceWord(k consent.RunKind) string {
	if k == consent.RunAccept {
		return "Accept"
	}
	return "Reject"
}

// buttonName writes a clicked control: `"Reject all" (#onetrust-reject)`.
func buttonName(c *consent.Choice) string {
	text, sel := consent.SafeText(c.Text, 120), consent.SafeText(c.Selector, 200)
	switch {
	case text != "" && sel != "":
		return fmt.Sprintf("%q (%s)", text, sel)
	case text != "":
		return fmt.Sprintf("%q", text)
	case sel != "":
		return sel
	}
	return "the button"
}

// times writes a click count: "once", "2 times".
func times(n int) string {
	if n <= 1 {
		return "once"
	}
	return fmt.Sprintf("%d times", n)
}

// bannerWait is --banner-wait, 10s without a plan.
func (j *judge) bannerWait() time.Duration {
	if j.p != nil && j.p.BannerWait > 0 {
		return j.p.BannerWait
	}
	return 10 * time.Second
}

// durationText writes a duration as the flags take it: 10s, 90s, 2m.
func durationText(d time.Duration) string {
	if d%time.Minute == 0 && d >= 2*time.Minute {
		return fmt.Sprintf("%dm", d/time.Minute)
	}
	if d%time.Second == 0 {
		return fmt.Sprintf("%ds", d/time.Second)
	}
	return d.String()
}

// noButtonDetail is the no_button message. A --accept-selector or
// --reject-selector that was given matched nothing (or isn't CSS the
// browser takes), so the message names it instead of asking for it.
// The --click button,api hint is only there when the CMP's rule has an
// API call for this choice and --click doesn't use it already. The
// generic rule's banner is "the banner".
func (j *judge) noButtonDetail(pg *consent.Page, k consent.RunKind, label, word string) string {
	banner := cmpName(pg) + "'s banner"
	if pg.CMP.ID == genericRule || pg.CMP.ID == "" && pg.CMP.Name == "" {
		banner = "the banner"
	}
	flag := "--" + strings.ToLower(word) + "-selector"
	given := j.p != nil && len(j.p.AcceptSelector) > 0
	if k != consent.RunAccept {
		given = j.p != nil && len(j.p.RejectSelector) > 0
	}
	if given {
		return fmt.Sprintf("%s: %s showed but %s matched no visible %s button: check that it is valid CSS and "+
			"that it finds the button (rhythm echo consent detect shows what Echo sees)", label, banner, flag, word)
	}
	s := fmt.Sprintf("%s: %s showed but Echo found no %s on it or in its settings. Set %s "+
		"(rhythm echo consent detect shows what Echo sees)", label, banner, word, flag)
	if ruleHasAPI(pg.CMP.ID, k) && (j.p == nil || j.p.Click == "" || j.p.Click == consent.ClickButton) {
		s += fmt.Sprintf(", or --click button,api to use %s's API", cmpName(pg))
	}
	return s
}

// reopenDetail is the message of a withdraw run whose banner couldn't
// be reopened after Accept: the reopen error (with its --click
// button,api hint when it has one), not the missing-button text.
func reopenDetail(label, detail string) string {
	why := strings.TrimPrefix(consent.SafeText(detail, 300), "consent: ")
	return fmt.Sprintf("%s: Accept worked but the banner couldn't be reopened to withdraw: %s", label, why)
}

// genericRule is the id of the CMP rule that finds banners by their
// button words.
const genericRule = "generic"

// ruleHasAPI reports whether the linked CMP rule id has an API call for
// k's choice (its listing reads "..., api"). Without the CMP stage it
// is false: the hint is only given when it is known to work.
func ruleHasAPI(id string, k consent.RunKind) bool {
	d := consent.Linked().CMP
	if d == nil || id == "" {
		return false
	}
	for _, r := range d.Rules() {
		if r.ID != id {
			continue
		}
		how := r.Reject
		if k == consent.RunAccept {
			how = r.Accept
		}
		return strings.HasSuffix(how, ", api") || how == "api"
	}
	return false
}

// inconclusiveDetail is the message an inconclusive run is listed with
// (PRD 10.2 messages 18 to 21 and the texts of the other reasons).
func (j *judge) inconclusiveDetail(pg *consent.Page, run *consent.Run) string {
	label, cmp := run.Kind.Label(), cmpName(pg)
	word := choiceWord(run.Kind)
	c := run.Choice
	if c == nil {
		c = &consent.Choice{}
	}
	switch run.Reason {
	case consent.ReasonNoButton:
		if run.ReopenFailed() {
			return reopenDetail(label, run.Detail)
		}
		return j.noButtonDetail(pg, run.Kind, label, word)
	case consent.ReasonClickMissed:
		return fmt.Sprintf("%s: clicked %s %s and the banner stayed. Something may cover it (a chat bubble, a newsletter popup); "+
			"--show-browser shows the page", label, buttonName(c), times(c.Clicks))
	case consent.ReasonStateUnchanged:
		state := cmp + "'s stored state didn't change"
		if c.State != nil && c.State.Source != "" {
			src := consent.SafeText(c.State.Source, 80)
			if _, name, ok := strings.Cut(src, ":"); ok {
				src = name
			}
			state += " (" + src + " still has no choice)"
		}
		verb := "rejecting"
		if run.Kind == consent.RunAccept {
			verb = "accepting"
		}
		button := buttonName(c)
		if t := consent.SafeText(c.Text, 120); t != "" {
			button = fmt.Sprintf("%q", t) // the selector is for finding a button; the message names it
		}
		return fmt.Sprintf("%s: clicked %s and the banner closed but %s. Hiding a banner is not %s", label, button, state, verb)
	case consent.ReasonTimeout:
		s := label + ": the run took longer than --timeout"
		if j.p != nil && j.p.Timeout > 0 {
			s += " " + durationText(j.p.Timeout)
		}
		if u := lastOpenRequest(run); u != "" {
			s += "; the last request still open was " + u
		}
		return s
	case consent.ReasonNoBanner:
		s := label + ": no banner showed within " + durationText(j.bannerWait())
		if pg.CMP.Present {
			s += " (" + cmp + "'s script loaded)"
		}
		return s
	case consent.ReasonCMPNotFound:
		name := "auto"
		if j.p != nil && j.p.CMP != "" {
			name = consent.SafeText(j.p.CMP, 60)
		}
		return fmt.Sprintf("%s: --cmp %s was named but its banner wasn't found", label, name)
	case consent.ReasonNavigation:
		if d := consent.SafeText(run.Detail, 300); d != "" {
			return d
		}
		return label + ": the page didn't load"
	case consent.ReasonLimit:
		n := consent.DefaultLimits.Requests
		if j.p != nil && j.p.Limits.Requests > 0 {
			n = j.p.Limits.Requests
		}
		return fmt.Sprintf("%s: stopped after %d requests", label, n)
	case consent.ReasonCanceled:
		return label + ": interrupted"
	}
	if d := consent.SafeText(run.Detail, 300); d != "" {
		return d
	}
	return label + ": " + string(run.Reason)
}

// lastOpenRequest is the URL of the run's last request event that never
// got a status, or "".
func lastOpenRequest(run *consent.Run) string {
	for i := len(run.Events) - 1; i >= 0; i-- {
		ev := run.Events[i]
		if ev.Kind == consent.EvRequest && ev.Request != nil && ev.Request.Status == 0 && ev.Request.Refused == "" && !ev.Request.Held {
			return consent.SafeText(ev.Request.URL, 300)
		}
	}
	return ""
}
