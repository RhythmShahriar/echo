// Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
// SPDX-License-Identifier: MIT

package report

import (
	"cmp"
	"fmt"
	"io"
	"net/url"
	"slices"
	"strings"
	"unicode/utf8"

	"github.com/charmbracelet/lipgloss"

	"github.com/RhythmShahriar/echo/consent"
	"github.com/RhythmShahriar/echo/model"
	"github.com/RhythmShahriar/rhythm/kit/ui"
)

// The notes the terminal shows as lines of their own, or not at all:
// the first note closes the output, held or sent is the Hits line and
// the gcd note matters only next to a gcd, which -v shows.
const (
	noteNotLegalAdvice = "Evidence, not legal advice."
	noteHeld           = "Hits held in the browser."
	noteSent           = "Tracker hits were sent for real (--send-hits)."
	noteGCD            = "gcd decoded with an unofficial, community-documented mapping."
)

// The Hits lines of the consent header.
const (
	hitsHeldLine = "Hits held: tracking hits are recorded and stopped in the browser. Nothing reached Google, Meta or any other tracker."
	hitsSentLine = "Hits sent: tracker hits were sent for real (--send-hits)."
)

// nothingToConsent is the page line of a page that needs no consent
// (PRD 11.2).
const nothingToConsent = "No banner and no trackers: nothing to consent to"

// The hints under a check that couldn't choose because no banner
// showed (PRD 9.1).
var noBannerHints = []string{
	"Many banners show only to visitors in some countries: try --geo de, or an EU --proxy.",
	"A banner Echo doesn't know: --accept-selector and --reject-selector (consent detect helps).",
}

// defaultConsentWidth is the width issue details and fixes wrap at when
// the options don't give one.
const defaultConsentWidth = 100

// consentText writes one consent result for a terminal.
type consentText struct {
	w  *strings.Builder
	r  *consent.Result
	o  ConsentTextOptions
	tn map[string]string // tracker id -> name, from the hits
}

// WriteConsentText writes the terminal output of a consent check (PRD
// 9.1): the header, each page with its runs, the verdict line, the
// issues (or, comparing, the changes), the runs that couldn't choose
// and the closing lines. Quiet keeps the verdict line and the issues;
// Verbose adds each run's timeline and the issue and change keys. Every
// string from the page passes Safe.
func WriteConsentText(w io.Writer, r *consent.Result, o ConsentTextOptions) error {
	if r == nil {
		r = &consent.Result{}
	}
	if o.Width <= 0 {
		o.Width = defaultConsentWidth
	}
	if o.Plain {
		o.Color = false
	}
	t := &consentText{w: &strings.Builder{}, r: r, o: o, tn: trackerNames(r)}
	if !o.Quiet {
		t.header()
		t.pages()
	}
	t.verdict()
	if r.Comparison != nil {
		t.changes()
	} else {
		t.issues()
	}
	if !o.Quiet {
		t.inconclusive()
		t.footer()
	}
	_, err := io.WriteString(w, t.w.String())
	return err
}

// trackerNames maps tracker ids to the names the hits give them.
func trackerNames(r *consent.Result) map[string]string {
	out := map[string]string{}
	for _, pg := range r.Pages {
		for _, run := range pg.Runs {
			for _, h := range run.Hits {
				if h.Name != "" && out[h.Tracker] == "" {
					out[h.Tracker] = h.Name
				}
			}
		}
	}
	return out
}

// name is a tracker's name for output: from the hits, the catalog when
// it is linked, else its id. Always safe to print.
func (t *consentText) name(id string) string {
	if n := t.tn[id]; n != "" {
		return Safe(n)
	}
	if c := consent.Linked().Classifier; c != nil {
		for _, ti := range c.Trackers() {
			if ti.ID == id && ti.Name != "" {
				t.tn[id] = ti.Name
				return Safe(ti.Name)
			}
		}
	}
	return Safe(id)
}

// line writes one line.
func (t *consentText) line(format string, a ...any) {
	fmt.Fprintf(t.w, format+"\n", a...)
}

// paint colors s when color is on.
func (t *consentText) paint(c lipgloss.Color, s string) string {
	if !t.o.Color {
		return s
	}
	return lipgloss.NewStyle().Foreground(c).Render(s)
}

// consentTarget names what a check covered: the page URL, the first
// one with the count when there are several ("https://shop.test/ (3
// pages)"), or with --sites the count of sites ("2 sites (5 pages)").
// It is "" for a result with no page.
func consentTarget(r *consent.Result) string {
	if len(r.Pages) == 0 {
		return ""
	}
	var sites []string
	for _, pg := range r.Pages {
		if pg.Site != "" && !slices.Contains(sites, pg.Site) {
			sites = append(sites, pg.Site)
		}
	}
	if len(sites) > 1 {
		return fmt.Sprintf("%d sites (%d pages)", len(sites), len(r.Pages))
	}
	what := Safe(r.Pages[0].URL)
	if len(r.Pages) > 1 {
		what += fmt.Sprintf(" (%d pages)", len(r.Pages))
	}
	return what
}

// header writes the first lines: what was checked, the browser on a
// first run, whether hits were held and the notes that change what the
// result means.
func (t *consentText) header() {
	r := t.r
	what := "consent check of " + consentTarget(r)
	if c := r.Comparison; c != nil {
		what += " against " + Safe(cmp.Or(c.Against, "a baseline"))
		if !c.BaseAt.IsZero() {
			what += " from " + c.BaseAt.UTC().Format("2006-01-02 15:04 UTC")
		}
	}
	t.line("rhythm echo %s - %s", Safe(cmp.Or(r.Echo, ToolVersion)), what)
	t.line("")
	if t.o.FirstRun && r.Browser != "" {
		t.line("Browser: %s", Safe(r.Browser))
	}
	if r.Held {
		t.line("%s", hitsHeldLine)
	} else {
		t.line("%s", hitsSentLine)
	}
	for i, n := range r.Notes {
		if i == 0 && n == noteNotLegalAdvice || n == noteHeld || n == noteSent || n == noteGCD && !t.o.Verbose {
			continue
		}
		t.line("%s", Safe(n))
	}
	t.line("")
}

// pages writes every page, grouped by --sites name worst first when the
// check had sites.
func (t *consentText) pages() {
	groups := t.siteGroups()
	for _, g := range groups {
		if g.site != "" {
			site := Safe(g.site)
			if t.o.Color {
				site = bold.Render(site)
			}
			t.line("%s", site)
		}
		for _, pg := range g.pages {
			t.page(pg)
		}
	}
}

// siteGroup is the pages of one --sites name.
type siteGroup struct {
	site                   string
	pages                  []*consent.Page
	errors, warnings, inco int
}

// siteGroups groups the pages by site. Without sites there is one group
// in page order; with sites the groups go worst first: errors, then
// warnings, then inconclusive runs, then the site name.
func (t *consentText) siteGroups() []*siteGroup {
	var groups []*siteGroup
	by := map[string]*siteGroup{}
	for i := range t.r.Pages {
		pg := &t.r.Pages[i]
		g := by[pg.Site]
		if g == nil {
			g = &siteGroup{site: pg.Site}
			by[pg.Site] = g
			groups = append(groups, g)
		}
		g.pages = append(g.pages, pg)
		for _, run := range pg.Runs {
			if run.Inconclusive() {
				g.inco++
			}
		}
	}
	for _, is := range t.r.Issues {
		g := by[is.Site]
		if g == nil || is.Ignored != nil {
			continue
		}
		switch is.Level {
		case model.Error:
			g.errors++
		case model.Warning:
			g.warnings++
		}
	}
	if len(groups) < 2 && (len(groups) == 0 || groups[0].site == "") {
		return groups
	}
	slices.SortStableFunc(groups, func(a, b *siteGroup) int {
		return cmp.Or(cmp.Compare(b.errors, a.errors), cmp.Compare(b.warnings, a.warnings),
			cmp.Compare(b.inco, a.inco), cmp.Compare(a.site, b.site))
	})
	return groups
}

// page writes one page: its URL, the banner, Consent Mode and a line per
// run.
func (t *consentText) page(pg *consent.Page) {
	t.line("%s", Safe(pg.URL))
	t.line("  %s", t.bannerLine(pg))
	if cm := consentModeLine(pg.ConsentMode); cm != "" {
		t.line("  %s", cm)
	}
	for i := range pg.Runs {
		run := &pg.Runs[i]
		mark, text := t.runSummary(pg, run)
		t.line("  [%d/%d] %-11s %s", i+1, len(pg.Runs), run.Kind.Label(), t.marked(mark, text))
		if t.o.Verbose {
			t.timeline(pg, run)
		}
	}
	t.line("")
}

// The marks a run line starts with.
const (
	markNone = iota
	markIssue
	markInconclusive
)

// marked puts a run line's mark in front of its text: ✗ or ?, or FAIL,
// INCONCLUSIVE and ok with --plain.
func (t *consentText) marked(mark int, text string) string {
	if t.o.Plain {
		switch mark {
		case markIssue:
			return "FAIL " + text
		case markInconclusive:
			return "INCONCLUSIVE " + text
		}
		return "ok " + text
	}
	switch mark {
	case markIssue:
		return t.paint(lipgloss.Color(ui.Danger), "✗") + " " + text
	case markInconclusive:
		return t.paint(lipgloss.Color(ui.High), "?") + " " + text
	}
	return text
}

// bannerLine says what the banner was: `Banner: Cookiebot, found after
// 0.8s, Reject on the banner`, `Banner: none found within 10s
// (Cookiebot's script loaded)` or the page needs no consent.
func (t *consentText) bannerLine(pg *consent.Page) string {
	if pg.NeedsNone {
		return nothingToConsent
	}
	if !pg.CMP.Found {
		return "Banner: none found within " + waitText(t.o.BannerWait) + scriptLoaded(pg.CMP)
	}
	s := "Banner: " + Safe(cmpLabel(pg.CMP)) + ", found after " + oneDecimal(pg.CMP.BannerAtMS)
	layer := pg.CMP.RejectLayer
	if run := pageRun(pg, consent.RunReject); run != nil && run.Choice != nil && run.Choice.Layer > 0 {
		layer = run.Choice.Layer
	}
	switch {
	case layer == 1:
		s += ", Reject on the banner"
	case layer > 1:
		s += ", Reject in settings"
	}
	return s
}

// pageRun is pg's run of kind k, or nil.
func pageRun(pg *consent.Page, k consent.RunKind) *consent.Run {
	for i := range pg.Runs {
		if pg.Runs[i].Kind == k {
			return &pg.Runs[i]
		}
	}
	return nil
}

// fourSignals are the Consent Mode v2 signals the Consent Mode line
// names together.
var fourSignals = []string{"ad_storage", "analytics_storage", "ad_user_data", "ad_personalization"}

// consentModeLine says what the page's Consent Mode setup was: `Consent
// Mode: v2 default, all four signals denied at 0.18s, before the first
// Google hit`. "" when the page has no Consent Mode.
func consentModeLine(cm *consent.ConsentMode) string {
	if cm == nil || !cm.Present {
		return ""
	}
	if len(cm.Default) == 0 {
		return "Consent Mode: no default; Google hits carry consent state"
	}
	s := "Consent Mode: "
	if cm.V2 {
		s += "v2 "
	}
	s += "default, " + stateWords(cm.Default) + " at " + twoDecimals(cm.DefaultAtMS)
	if cm.FirstGoogleHitMS > 0 {
		if cm.DefaultBeforeFirstHit {
			s += ", before the first Google hit"
		} else {
			s += ", after the first Google hit"
		}
	}
	return s
}

// stateWords writes a consent state: "all four signals denied",
// "ad_storage and analytics_storage denied" or each signal with its
// value.
func stateWords(state map[string]consent.Value) string {
	keys := sortedSignals(state)
	same := true
	for _, k := range keys {
		same = same && state[k] == state[keys[0]]
	}
	if same && len(keys) > 0 {
		all := len(keys) == len(fourSignals)
		for _, k := range fourSignals {
			_, ok := state[k]
			all = all && ok
		}
		if all {
			return "all four signals " + Safe(string(state[keys[0]]))
		}
		return Safe(andList(keys)) + " " + Safe(string(state[keys[0]]))
	}
	parts := make([]string, len(keys))
	for i, k := range keys {
		parts[i] = Safe(k + "=" + string(state[k]))
	}
	return strings.Join(parts, " ")
}

// sortedSignals lists a state's signals: the four v2 signals in their
// usual order, then the rest by name.
func sortedSignals(state map[string]consent.Value) []string {
	var keys, rest []string
	for _, k := range fourSignals {
		if _, ok := state[k]; ok {
			keys = append(keys, k)
		}
	}
	for k := range state {
		if !slices.Contains(fourSignals, k) {
			rest = append(rest, k)
		}
	}
	slices.Sort(rest)
	return append(keys, rest...)
}

// twoDecimals writes a run clock time as seconds with two decimals.
func twoDecimals(ms int64) string {
	return fmt.Sprintf("%.2fs", float64(ms)/1000)
}

// runIssues are the counted issues of one run of pg.
func (t *consentText) runIssues(pg *consent.Page, run *consent.Run) []consent.Issue {
	path := consent.PageName(t.r, pg)
	var out []consent.Issue
	for _, is := range t.r.Issues {
		if is.Ignored == nil && is.Run == run.Kind && is.Page == path && is.Site == pg.Site {
			out = append(out, is)
		}
	}
	return out
}

// consentPagePath is the path (and query) of a page URL, the way the
// reports show pages; consent.PageName is the form issues name them by.
func consentPagePath(raw string) string {
	u, err := url.Parse(raw)
	if err != nil || u.Host == "" {
		return raw
	}
	p := u.EscapedPath()
	if p == "" {
		p = "/"
	}
	if u.RawQuery != "" {
		p += "?" + u.RawQuery
	}
	return p
}

// reasonWords says in a few words why a run couldn't choose.
var reasonWords = map[consent.Reason]string{
	consent.ReasonNoBanner:       "no banner",
	consent.ReasonCMPNotFound:    "the named CMP's banner wasn't found",
	consent.ReasonNoButton:       "no button found",
	consent.ReasonClickMissed:    "the click didn't take",
	consent.ReasonStateUnchanged: "the CMP didn't store the choice",
	consent.ReasonNavigation:     "the page didn't load",
	consent.ReasonTimeout:        "timed out",
	consent.ReasonLimit:          "stopped at the request limit",
	consent.ReasonCanceled:       "interrupted",
}

// runReasonWords is reasonWords for run: a withdraw run whose banner
// couldn't be reopened says so rather than "no button found".
func runReasonWords(run *consent.Run) string {
	if run.ReopenFailed() {
		return "the banner couldn't be reopened"
	}
	return reasonWords[run.Reason]
}

// issuePhrase is how a tracker check reads in a run line, after the
// tracker names.
func issuePhrase(is consent.Issue) string {
	switch is.Check {
	case consent.CheckBeforeChoice:
		return "fired before the choice"
	case consent.CheckAfterReject:
		return "fired after Reject"
	case consent.CheckAfterWithdraw:
		return "fired after consent was withdrawn"
	case consent.CheckGPCIgnored:
		return "fired with GPC on"
	case consent.CheckEmbed:
		if is.Run == consent.RunNone || strings.Contains(is.Title, "before") {
			return "loaded before the choice"
		}
		return "loaded after Reject"
	case consent.CheckScriptBefore:
		if strings.Contains(is.Title, "after Reject") {
			return "script loaded after Reject"
		}
		return "script loaded before the choice"
	}
	return ""
}

// relevant reports whether an entry of phase ph is what a run's line is
// about: everything in the no-choice and GPC runs, what came after the
// choice in the others.
func relevant(k consent.RunKind, ph consent.Phase) bool {
	switch k {
	case consent.RunAccept:
		return ph == consent.PhaseAfterAccept
	case consent.RunReject:
		return ph == consent.PhaseAfterReject
	case consent.RunWithdraw:
		return ph == consent.PhaseAfterWithdraw
	}
	return true
}

// runSummary is a run line's text and mark: what fired, the issues, the
// Consent Mode update and whether the choice was kept.
func (t *consentText) runSummary(pg *consent.Page, run *consent.Run) (int, string) {
	if run.Inconclusive() {
		verb := "couldn't check"
		if run.Kind == consent.RunAccept || run.Kind == consent.RunReject || run.Kind == consent.RunWithdraw {
			verb = "couldn't choose"
		}
		return markInconclusive, verb + ": " + cmp.Or(runReasonWords(run), Safe(string(run.Reason)))
	}
	mark := markNone
	var parts []string
	told := map[string]bool{}
	// Issues by phrase, tracker names in the order the issues are listed.
	var phrases []string
	byPhrase := map[string][]string{}
	for _, is := range t.runIssues(pg, run) {
		if is.Level == model.Error || is.Level == model.Warning {
			mark = markIssue
		}
		ph := issuePhrase(is)
		switch {
		case ph != "" && is.Tracker != "":
			if !slices.Contains(phrases, ph) {
				phrases = append(phrases, ph)
			}
			if n := t.name(is.Tracker); !slices.Contains(byPhrase[ph], n) {
				byPhrase[ph] = append(byPhrase[ph], n)
			}
			told[is.Tracker] = true
		case is.Check == consent.CheckCookielessPing:
		case is.Level != model.Notice:
			parts = append(parts, Safe(is.Title))
		}
	}
	var lead []string
	for _, ph := range phrases {
		s := andList(byPhrase[ph]) + " " + ph
		if ph == "fired after Reject" || ph == "fired after consent was withdrawn" {
			s += t.pagesOf(run, byPhrase[ph])
		}
		lead = append(lead, s)
	}
	parts = append(lead, parts...)
	parts = append(parts, t.firedParts(pg, run, told)...)
	if u := updateWords(run); u != "" {
		parts = append(parts, u)
	}
	if k := keptWords(run); k != "" {
		parts = append(parts, k)
	}
	return mark, strings.Join(parts, "; ")
}

// pagesOf says on how many of the pages after the choice the named
// trackers fired: " on 2 of 3 pages"; "" with one page.
func (t *consentText) pagesOf(run *consent.Run, names []string) string {
	c := run.Choice
	if run.Kind == consent.RunWithdraw {
		c = run.Withdraw
	}
	if c == nil || len(run.Visits) < 2 {
		return ""
	}
	first := 0
	for i, v := range run.Visits {
		if v.AtMS <= c.AtMS {
			first = i
		}
	}
	n := len(run.Visits) - first
	if n < 2 {
		return ""
	}
	seen := map[int]bool{}
	for _, h := range run.Hits {
		if relevant(run.Kind, h.Phase) && h.Phase != consent.PhaseBefore && slices.Contains(names, t.name(h.Tracker)) && h.Kind != consent.HitScript {
			seen[h.Visit] = true
		}
	}
	return fmt.Sprintf(" on %d of %d pages", len(seen), n)
}

// firedParts lists what fired that no issue named: the trackers that
// fired, the cookieless pings, exempt trackers and the trackers whose
// script loaded but held back. "nothing fired" when nothing did.
func (t *consentText) firedParts(pg *consent.Page, run *consent.Run, told map[string]bool) []string {
	var order []string
	by := map[string]*firedTracker{}
	cookies := 0
	for _, h := range run.Hits {
		if h.Kind == consent.HitScript || !relevant(run.Kind, h.Phase) || h.Need == consent.NeedNo {
			continue
		}
		s := by[h.Tracker]
		if s == nil {
			s = &firedTracker{}
			by[h.Tracker] = s
			order = append(order, h.Tracker)
		}
		s.hits++
		s.exempt = h.Need == consent.NeedExempt
		if h.Cookieless && t.r.Policy == consent.PolicyAdvanced {
			s.cookieless++
			if s.gcs == "" && h.Google != nil {
				s.gcs = h.Google.GCS
			}
		}
	}
	for _, c := range run.Cookies {
		if c.Tracker != "" && c.Blocked == "" && c.Need == consent.NeedNeeded && relevant(run.Kind, c.Phase) {
			cookies++
		}
	}
	var fired, exempt, pings []string
	for _, id := range order {
		s := by[id]
		switch {
		case told[id]:
		case s.exempt:
			exempt = append(exempt, t.name(id))
		case s.cookieless == s.hits:
			p := fmt.Sprintf("%s: %s", t.name(id), plural(s.hits, "cookieless ping", "cookieless pings"))
			if s.gcs != "" {
				p += " (gcs " + Safe(s.gcs) + ")"
			}
			pings = append(pings, p)
		default:
			fired = append(fired, t.name(id))
		}
	}
	var out []string
	if len(fired) > 0 {
		out = append(out, andCommaList(fired)+" fired")
	}
	out = append(out, pings...)
	if len(exempt) > 0 {
		out = append(out, andCommaList(exempt)+" fired (exempt)")
	}
	if len(told) == 0 && len(fired) == 0 && len(pings) == 0 {
		out = append(out, "nothing fired")
	}
	if len(pings) > 0 && len(fired) == 0 && cookies == 0 && len(told) == 0 {
		out = append(out, "no tracker cookies")
	}
	if held := t.heldBack(pg, run, by); len(held) > 0 {
		out = append(out, andList(held)+" held back")
	}
	return out
}

// firedTracker counts one tracker's hits in a run line.
type firedTracker struct {
	hits, cookieless int
	exempt           bool
	gcs              string // the first cookieless ping's gcs
}

// andCommaList lists names as the run lines do: "GA4, Google Ads, Meta
// Pixel".
func andCommaList(names []string) string { return strings.Join(names, ", ") }

// heldBack names the trackers whose script loaded in run but that sent
// nothing, when they fire in another run of the page: they waited for
// consent, as they should.
func (t *consentText) heldBack(pg *consent.Page, run *consent.Run, fired map[string]*firedTracker) []string {
	var out []string
	for _, s := range run.Scripts {
		if fired[s.Tracker] != nil || !relevant(run.Kind, s.Phase) && run.Kind != consent.RunAccept {
			continue
		}
		sent := false
		for _, h := range run.Hits {
			sent = sent || h.Tracker == s.Tracker && h.Kind != consent.HitScript
		}
		if sent || !t.firesElsewhere(pg, run.Kind, s.Tracker) {
			continue
		}
		if n := t.name(s.Tracker); !slices.Contains(out, n) {
			out = append(out, n)
		}
	}
	return out
}

// firesElsewhere reports whether tracker sent a hit in another run of pg.
func (t *consentText) firesElsewhere(pg *consent.Page, k consent.RunKind, tracker string) bool {
	for _, run := range pg.Runs {
		if run.Kind == k {
			continue
		}
		for _, h := range run.Hits {
			if h.Tracker == tracker && h.Kind != consent.HitScript {
				return true
			}
		}
	}
	return false
}

// updateWords says what the first Consent Mode update after the choice
// said: "update granted at 2.3s" or "update denied at 2.1s".
func updateWords(run *consent.Run) string {
	c := run.Choice
	if run.Kind == consent.RunWithdraw {
		c = run.Withdraw
	}
	if c == nil {
		return ""
	}
	for _, s := range run.Signals {
		if (s.Source != "gtag" && s.Source != "datalayer") || s.Command != "update" || s.AtMS < c.AtMS {
			continue
		}
		v := "denied"
		for _, val := range s.State {
			if val == consent.Granted {
				v = "granted"
			}
		}
		return "update " + v + " at " + oneDecimal(s.AtMS)
	}
	return ""
}

// keptWords says on how many later pages the choice held: "kept on 2
// more pages".
func keptWords(run *consent.Run) string {
	c := run.Choice
	if run.Kind == consent.RunWithdraw {
		c = run.Withdraw
	}
	if c == nil || !c.Verified {
		return ""
	}
	n := 0
	for _, v := range run.Visits {
		if v.Step == "page" && v.AtMS > c.AtMS && !v.Banner {
			n++
		}
	}
	if n == 0 {
		return ""
	}
	return "kept on " + plural(n, "more page", "more pages")
}

// verdict writes the verdict line: `Findings - 2 errors, 0 warnings, 1
// notice - 3 runs, 2 trackers, policy advanced`, or the comparison's.
func (t *consentText) verdict() {
	s := t.r.Summary()
	if c := t.r.Comparison; c != nil {
		head := "No changes"
		switch {
		case s.Broke > 0:
			head = "Broke"
		case s.New > 0:
			head = "New"
		case s.Changed > 0:
			head = "Changed"
		}
		t.line("%s - %d broke, %d new, %d changed, %d fixed - %s, %s", t.paint(t.headColor(head), head),
			s.Broke, s.New, s.Changed, s.Fixed, plural(s.Runs, "run", "runs"), plural(s.Trackers, "tracker", "trackers"))
		return
	}
	head := "All clear"
	switch t.r.ExitCode(t.o.FailOn) {
	case 1:
		head = "Findings"
	case 3:
		head = "Couldn't check"
	}
	t.line("%s - %s, %s, %s - %s, %s, policy %s", t.paint(t.headColor(head), head),
		plural(s.Errors, "error", "errors"), plural(s.Warnings, "warning", "warnings"), plural(s.Notices, "notice", "notices"),
		plural(s.Runs, "run", "runs"), plural(s.Trackers, "tracker", "trackers"), Safe(string(cmp.Or(t.r.Policy, consent.PolicyAdvanced))))
}

// headColor is the color of a verdict word.
func (t *consentText) headColor(head string) lipgloss.Color {
	switch head {
	case "Findings", "Broke":
		return lipgloss.Color(ui.Danger)
	case "Couldn't check", "New", "Changed":
		return lipgloss.Color(ui.High)
	}
	return lipgloss.Color(ui.Ok)
}

// The issue layout: level and check in columns, the detail and fix
// under them.
const (
	levelWidth = 7
	checkWidth = 22
	textIndent = 10
)

// issues writes every issue: `  error   consent/before-choice   Meta
// Pixel fired before the visitor chose (No choice)`, then for errors and warnings
// the detail and the fix ("as above." when it repeats the one before),
// wrapped at the width. Ignored issues say until when.
func (t *consentText) issues() {
	if len(t.r.Issues) == 0 {
		return
	}
	if !t.o.Quiet {
		t.line("")
	}
	lastFix := ""
	pad := strings.Repeat(" ", textIndent)
	where := t.issueWhere()
	for _, is := range t.r.Issues {
		lvl := padRight(string(is.Level), levelWidth)
		check := padRight(Safe(is.Check), checkWidth)
		title := Safe(is.Title)
		if w := where(is); w != "" {
			title += " (" + w + ")"
		}
		if ig := is.Ignored; ig != nil {
			why := "ignored"
			if ig.Until != "" {
				why += " until " + Safe(ig.Until)
			}
			if ig.Reason != "" {
				why += ": " + Safe(ig.Reason)
			}
			title += " (" + why + ")"
		}
		t.line("  %s %s  %s", t.paint(LevelColor(is.Level), lvl), check, title)
		if t.o.Verbose {
			t.line("%s(key: %s)", pad, Safe(is.Key))
		}
		if is.Level == model.Notice && !t.o.Verbose || is.Ignored != nil {
			continue
		}
		if is.Detail != "" {
			t.wrapped(pad, pad, Safe(is.Detail))
		}
		if is.Fix != "" {
			fix := Safe(is.Fix)
			if is.Fix == lastFix {
				fix = "as above."
			}
			t.wrapped(pad+"fix: ", pad, fix)
			lastFix = is.Fix
		}
	}
}

// issueWhere returns what an issue line says about where the issue is:
// the run's label, then the --sites name and the page when the result
// has more than one site or page ("Reject, charlie /checkout"). An
// issue of the page as a whole names no run.
func (t *consentText) issueWhere() func(consent.Issue) string {
	sites := map[string]bool{}
	for _, pg := range t.r.Pages {
		sites[pg.Site] = true
	}
	for _, is := range t.r.Issues {
		sites[is.Site] = true
	}
	showPage, showSite := len(t.r.Pages) > 1, len(sites) > 1
	return func(is consent.Issue) string {
		var parts []string
		if is.Run != "" {
			parts = append(parts, is.Run.Label())
		}
		var at []string
		if showSite && is.Site != "" {
			at = append(at, Safe(is.Site))
		}
		if (showPage || showSite) && is.Page != "" {
			at = append(at, Safe(is.Page))
		}
		if len(at) > 0 {
			parts = append(parts, strings.Join(at, " "))
		}
		return strings.Join(parts, ", ")
	}
}

// padRight pads s with spaces to n columns.
func padRight(s string, n int) string {
	return s + strings.Repeat(" ", max(0, n-utf8.RuneCountInString(s)))
}

// wrapped writes text after first, wrapping at the width with rest in
// front of each further line. A word longer than a line is kept whole.
func (t *consentText) wrapped(first, rest, text string) {
	line := first
	start := utf8.RuneCountInString(first)
	cols := start
	for _, word := range strings.Fields(text) {
		n := utf8.RuneCountInString(word)
		if cols > start && cols+1+n > t.o.Width {
			t.line("%s", line)
			line, cols = rest, utf8.RuneCountInString(rest)
			start = cols
		}
		if cols > start {
			line += " "
			cols++
		}
		line += word
		cols += n
	}
	t.line("%s", line)
}

// classWords are the change classes as the change lines start.
var classWords = map[string]string{consent.ClassBroke: "BROKE", consent.ClassNew: "NEW", consent.ClassChanged: "CHANGED", consent.ClassFixed: "FIXED"}

// changeTitleWidth is the column the pages of a change line start at.
const changeTitleWidth = 50

// changes writes the comparison: one line per class and title with the
// pages (and runs) it covers: `  BROKE  TikTok Pixel now fires before
// the choice   / (No choice, Reject)`.
func (t *consentText) changes() {
	type group struct {
		class, title, key string
		pages             []string
		runs              []string
		before, after     string
		accepted          bool
		keys              []string
	}
	var groups []*group
	for _, c := range t.r.Comparison.Changes {
		var g *group
		for _, x := range groups {
			if x.class == c.Class && x.title == c.Title && x.accepted == c.Accepted && x.before == c.Before && x.after == c.After {
				g = x
			}
		}
		if g == nil {
			g = &group{class: c.Class, title: c.Title, key: c.Key, before: c.Before, after: c.After, accepted: c.Accepted}
			groups = append(groups, g)
		}
		p := Safe(c.Page)
		if c.Site != "" && p != "" {
			p = Safe(c.Site) + " " + p
		}
		if p != "" && !slices.Contains(g.pages, p) {
			g.pages = append(g.pages, p)
		}
		if c.Run != "" && !strings.HasPrefix(c.Key, "tracker|") && !slices.Contains(g.runs, c.Run.Label()) {
			g.runs = append(g.runs, c.Run.Label())
		}
		g.keys = append(g.keys, c.Key)
	}
	for _, g := range groups {
		where := strings.Join(g.pages, ", ")
		if len(g.runs) > 0 {
			where += " (" + strings.Join(g.runs, ", ") + ")"
		}
		title := Safe(g.title)
		if g.class == consent.ClassChanged && showBeforeAfter(g.key, g.before, g.after) {
			title += ": was " + Safe(g.before) + ", now " + Safe(g.after)
		}
		if g.accepted {
			title += " (accepted)"
		}
		word := padRight(classWords[g.class], 7)
		t.line("  %s %s  %s", t.paint(ClassColor(g.class), word), padRight(title, changeTitleWidth), where)
		if t.o.Verbose {
			for _, k := range g.keys {
				t.line("%s(key: %s)", strings.Repeat(" ", textIndent), Safe(k))
			}
		}
	}
}

// showBeforeAfter reports whether a changed line adds "was x, now y":
// not when there are no values, nor for a CMP change, whose title
// already names both CMPs.
func showBeforeAfter(key, before, after string) bool {
	if strings.HasPrefix(key, "cmp|") {
		return false
	}
	return before != "" || after != ""
}

// inconclusive writes the runs that couldn't choose, with the hints for
// a banner that didn't show.
func (t *consentText) inconclusive() {
	if len(t.r.Inconclusive) == 0 {
		return
	}
	t.line("")
	mark := t.paint(lipgloss.Color(ui.High), "?")
	if t.o.Plain {
		mark = "INCONCLUSIVE"
	}
	hint := false
	for _, in := range t.r.Inconclusive {
		at := Safe(in.Page)
		if in.Site != "" {
			at = Safe(in.Site) + " " + at
		}
		t.line("  %s %s  %s", mark, at, Safe(in.Detail))
		hint = hint || in.Reason == consent.ReasonNoBanner || in.Reason == consent.ReasonCMPNotFound
	}
	if hint {
		for _, h := range noBannerHints {
			t.line("  %s", h)
		}
	}
}

// footer closes the output: the legal line and where the full report is.
func (t *consentText) footer() {
	t.line("")
	t.line("%s", noteNotLegalAdvice)
	if t.o.ReportPath != "" {
		t.line("full report: %s", Safe(t.o.ReportPath))
	}
}
