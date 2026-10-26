// Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
// SPDX-License-Identifier: MIT

package report

import (
	"cmp"
	"fmt"
	"slices"
	"strings"

	"github.com/RhythmShahriar/echo/consent"
)

// timelineEntry is one line of a run's -v timeline.
type timelineEntry struct {
	at   int64
	typ  string // load, gtag, script, hit, cookie, banner, click, page, reload
	text string // safe to print
}

// timeline writes a run's timeline under its run line (PRD 9.1 -v):
// `     0.62s  hit    GA4 page_view gcs=G100 gcd=13p3p3p3p5 (held)`. It
// is built from the run's lists, so a result read back from consent.json
// shows the same as the run that made it.
func (t *consentText) timeline(pg *consent.Page, run *consent.Run) {
	for _, e := range t.timelineEntries(pg, run) {
		t.line("    %6s  %-6s %s", twoDecimals(e.at), e.typ, e.text)
	}
}

// timelineEntries lists a run's visits, Consent Mode commands, tracker
// scripts, hits and cookies, the banner and the clicks in time order.
func (t *consentText) timelineEntries(pg *consent.Page, run *consent.Run) []timelineEntry {
	var out []timelineEntry
	pages := 0
	for _, v := range run.Visits {
		if v.Step == "page" {
			pages++
		}
	}
	page := 0
	for _, v := range run.Visits {
		switch v.Step {
		case "page":
			page++
			out = append(out, timelineEntry{v.AtMS, "page", fmt.Sprintf("%s (link %d of %d)", Safe(consentPagePath(v.URL)), page, pages)})
		case "reload":
			out = append(out, timelineEntry{v.AtMS, "reload", Safe(v.URL)})
		default:
			out = append(out, timelineEntry{v.AtMS, "load", Safe(v.URL)})
		}
	}
	if pg.CMP.Found && run.Status != consent.StatusNoBanner {
		out = append(out, timelineEntry{pg.CMP.BannerAtMS, "banner", Safe(cmpLabel(pg.CMP)) + " shown"})
	}
	for _, c := range []*consent.Choice{run.Choice, run.Withdraw} {
		if c != nil {
			out = append(out, timelineEntry{c.AtMS, "click", clickText(pg, c)})
		}
	}
	for _, s := range run.Signals {
		if s.Source != "gtag" && s.Source != "datalayer" {
			continue
		}
		cmd := Safe(s.Command)
		text := "consent " + padRight(cmd, 8) + " " + timelineState(s.State)
		if len(s.Region) > 0 {
			text += " region " + Safe(strings.Join(s.Region, ","))
		}
		out = append(out, timelineEntry{s.AtMS, "gtag", strings.TrimRight(text, " ")})
	}
	for _, s := range run.Scripts {
		out = append(out, timelineEntry{s.AtMS, "script", Safe(shortConsentURL(s.URL))})
	}
	for _, h := range run.Hits {
		if h.Kind == consent.HitScript {
			continue
		}
		out = append(out, timelineEntry{h.AtMS, "hit", t.hitText(&h)})
	}
	for _, c := range run.Cookies {
		if c.Tracker == "" {
			continue
		}
		text := Safe(c.Name) + " (" + t.name(c.Tracker) + ")"
		if c.Blocked != "" {
			text += " refused: " + Safe(c.Blocked)
		}
		out = append(out, timelineEntry{c.AtMS, "cookie", text})
	}
	slices.SortStableFunc(out, func(a, b timelineEntry) int { return cmp.Compare(a.at, b.at) })
	return out
}

// clickText is a click as the timeline shows it: the button's text and
// selector, or the CMP's API.
func clickText(pg *consent.Page, c *consent.Choice) string {
	if c.How == "api" {
		return Safe(cmpLabel(pg.CMP)) + "'s API"
	}
	text := `"` + Safe(c.Text) + `"`
	if c.Selector != "" {
		text += " (" + Safe(c.Selector) + ")"
	}
	return text
}

// timelineState writes a Consent Mode command's state: "all four
// denied", or each signal with its value.
func timelineState(state map[string]consent.Value) string {
	if len(state) == 0 {
		return ""
	}
	words := stateWords(state)
	if rest, ok := strings.CutPrefix(words, "all four signals "); ok {
		return "all four " + rest
	}
	keys := sortedSignals(state)
	parts := make([]string, len(keys))
	for i, k := range keys {
		parts[i] = Safe(k + "=" + string(state[k]))
	}
	return strings.Join(parts, " ")
}

// hitText is a hit as the timeline shows it: `GA4 page_view gcs=G100
// gcd=13p3p3p3p5 (held)`.
func (t *consentText) hitText(h *consent.HitRef) string {
	parts := []string{t.name(h.Tracker)}
	if len(h.Events) > 0 {
		ev := h.Events
		if len(ev) > 3 {
			ev = append(slices.Clone(ev[:3]), "...")
		}
		parts = append(parts, Safe(strings.Join(ev, ",")))
	}
	if g := h.Google; g != nil {
		if g.GCS != "" {
			parts = append(parts, "gcs="+Safe(g.GCS))
		}
		if g.GCD != "" {
			parts = append(parts, "gcd="+Safe(g.GCD))
		}
	}
	if h.Cookieless {
		parts = append(parts, "cookieless")
	}
	if h.Held {
		parts = append(parts, "(held)")
	}
	return strings.Join(parts, " ")
}

// shortConsentURL drops the scheme and www. of a URL, as the timeline
// shows scripts.
func shortConsentURL(raw string) string {
	s := raw
	for _, p := range []string{"https://", "http://"} {
		s = strings.TrimPrefix(s, p)
	}
	return strings.TrimPrefix(s, "www.")
}
