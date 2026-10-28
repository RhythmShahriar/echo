// Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
// SPDX-License-Identifier: MIT

package report

import (
	"cmp"
	"fmt"
	"html"
	"io"
	"regexp"
	"slices"
	"strings"

	"github.com/RhythmShahriar/echo/consent"
	"github.com/RhythmShahriar/echo/model"
)

// defaultMarkdownMax is where the Markdown summary is cut when the
// options give no size: GitHub refuses a job summary over 1 MiB, so the
// cut leaves room for the rest of the step's summary.
const defaultMarkdownMax = 900 << 10

// markdownCutLine closes a summary that was cut to fit.
const markdownCutLine = "_Cut to fit the job summary; the full report is in the artifact._"

// runOrder is the order runs take in matrices and tables.
var runOrder = []consent.RunKind{consent.RunNone, consent.RunAccept, consent.RunReject, consent.RunGPC, consent.RunWithdraw}

// mdEscaper backslash-escapes every character that could start Markdown
// in a page-supplied string: emphasis, links and images, HTML, headings,
// table columns and code spans. An @ gets a zero-width joiner after it,
// so a page title can't mention a GitHub user or team.
var mdEscaper = strings.NewReplacer(`\`, `\\`, "*", `\*`, "_", `\_`, "[", `\[`, "]", `\]`, "(", `\(`, ")", `\)`,
	"<", `\<`, ">", `\>`, "#", `\#`, "|", `\|`, "!", `\!`, "`", "\\`", "@", "@\u200d")

// htmlComment matches an HTML comment, or one left open to the end, so
// a page can't hide the rest of the summary in one.
var htmlComment = regexp.MustCompile(`(?s)<!--.*?(?:-->|$)`)

// lineBreaks are what md collapses to a space: a table row is one line.
var lineBreaks = strings.NewReplacer("\r\n", " ", "\n", " ", "\r", " ")

// md makes page-supplied text safe in Markdown: HTML comments gone, one
// line, no terminal controls and every Markdown character escaped.
func md(s string) string {
	s = htmlComment.ReplaceAllString(s, "")
	return mdEscaper.Replace(Safe(lineBreaks.Replace(s)))
}

// mdHTML makes page-supplied text safe inside the raw HTML of a
// <details> summary, where Markdown escapes don't apply: HTML escaped,
// the characters of links, images and code spans as entities and an @
// that mentions no one.
func mdHTML(s string) string {
	s = htmlComment.ReplaceAllString(s, "")
	return mdEntities.Replace(html.EscapeString(Safe(lineBreaks.Replace(s))))
}

// mdEntities writes the characters of links, images, code spans and
// table columns as entities.
var mdEntities = strings.NewReplacer("[", "&#91;", "]", "&#93;", "(", "&#40;", ")", "&#41;", "`", "&#96;", "!", "&#33;",
	"|", "&#124;", "@", "@\u200d")

// consentMD writes one consent result as Markdown.
type consentMD struct {
	b *strings.Builder
	t *consentText // tracker names and the run helpers the terminal uses
	r *consent.Result
	o ConsentMarkdownOptions
}

// WriteConsentMarkdown writes the Markdown summary of a consent check
// (PRD 9.5): the heading, a bold verdict sentence, a matrix of pages by
// runs (one per --sites name, worst first), the issues by level, the
// runs that couldn't be checked, Consent Mode, the comparison and an
// italic footer. Without Details pages are paths only, with no query
// strings, IDs or screenshots; Details adds the masked issue details and
// a collapsed timeline per run. Every page-supplied string is escaped.
// The output is cut at a line at MaxBytes (0 is 900 KiB).
func WriteConsentMarkdown(w io.Writer, r *consent.Result, o ConsentMarkdownOptions) error {
	if r == nil {
		r = &consent.Result{}
	}
	if o.MaxBytes <= 0 {
		o.MaxBytes = defaultMarkdownMax
	}
	t := &consentText{w: &strings.Builder{}, r: r, o: ConsentTextOptions{FailOn: o.FailOn}, tn: trackerNames(r)}
	m := &consentMD{b: &strings.Builder{}, t: t, r: r, o: o}
	m.line("## ♪ Echo consent")
	m.line("")
	m.verdict()
	m.matrices()
	m.issueTables()
	if o.Details {
		m.runDetails()
	}
	m.inconclusive()
	m.consentMode()
	m.comparison()
	m.footer()
	_, err := io.WriteString(w, cutMarkdown(m.b.String(), o.MaxBytes))
	return err
}

// line writes one line.
func (m *consentMD) line(format string, a ...any) {
	fmt.Fprintf(m.b, format+"\n", a...)
}

// page is how a page reads in the summary: the path, without its query
// string unless Details, after the --sites name when there is one.
func (m *consentMD) page(site, path string) string {
	if !m.o.Details {
		path, _, _ = strings.Cut(path, "?")
	}
	if site != "" {
		return md(site) + " " + md(path)
	}
	return md(path)
}

// verdict writes the bold verdict sentence: `**Findings.** 1 page, 3
// runs, 2 trackers, policy advanced: Meta Pixel fired before the choice
// and after Reject.`
func (m *consentMD) verdict() {
	s := m.r.Summary()
	head := "All clear"
	var tail string
	if c := m.r.Comparison; c != nil {
		head = "No changes"
		switch {
		case s.Broke > 0:
			head = "Broke"
		case s.New > 0:
			head = "New"
		case s.Changed > 0:
			head = "Changed"
		}
		tail = fmt.Sprintf("%d broke, %d new, %d changed, %d fixed against %s", s.Broke, s.New, s.Changed, s.Fixed, md(cmp.Or(c.Against, "a baseline")))
	} else {
		switch m.r.ExitCode(m.o.FailOn) {
		case 1:
			head = "Findings"
		case 3:
			head = "Couldn't check"
		}
		tail = m.findings()
		if tail == "" && s.InconclusiveRuns > 0 {
			tail = m.unchecked()
		}
	}
	policy := md(string(cmp.Or(m.r.Policy, consent.PolicyAdvanced)))
	line := fmt.Sprintf("**%s.** %s, %s, %s, policy %s", head, plural(s.Pages, "page", "pages"), plural(s.Runs, "run", "runs"),
		plural(s.Trackers, "tracker", "trackers"), policy)
	if tail != "" {
		line += ": " + tail
	}
	m.line("%s.", line)
	m.line("")
}

// findings sums up the worst counted issues: per tracker what it did
// ("Meta Pixel fired before the choice and after Reject"), then the
// titles of the others, at most three parts.
func (m *consentMD) findings() string {
	worst := model.Level("")
	for _, is := range m.r.Issues {
		if is.Ignored == nil && is.Level != model.Notice && (worst == "" || is.Level.Rank() < worst.Rank()) {
			worst = is.Level
		}
	}
	if worst == "" {
		return ""
	}
	var order []string
	phrases := map[string][]string{}
	var others []string
	for _, is := range m.r.Issues {
		if is.Ignored != nil || is.Level != worst {
			continue
		}
		ph := issuePhrase(is)
		if ph == "" || is.Tracker == "" {
			if t := md(is.Title); !slices.Contains(others, t) {
				others = append(others, t)
			}
			continue
		}
		if _, ok := phrases[is.Tracker]; !ok {
			order = append(order, is.Tracker)
		}
		if !slices.Contains(phrases[is.Tracker], ph) {
			phrases[is.Tracker] = append(phrases[is.Tracker], ph)
		}
	}
	var parts []string
	for _, id := range order {
		parts = append(parts, md(m.t.name(id))+" "+factorFired(phrases[id]))
	}
	parts = append(parts, others...)
	if len(parts) > 3 {
		parts = append(parts[:3], fmt.Sprintf("and %d more", len(parts)-3))
	}
	return strings.Join(parts, "; ")
}

// factorFired joins a tracker's phrases, saying "fired" once when they
// all start with it: "fired before the choice and after Reject".
func factorFired(phrases []string) string {
	rest := make([]string, len(phrases))
	for i, p := range phrases {
		r, ok := strings.CutPrefix(p, "fired ")
		if !ok {
			return andList(phrases)
		}
		rest[i] = r
	}
	return "fired " + andList(rest)
}

// unchecked says which runs couldn't be checked: "Accept and Reject
// couldn't be tested on 1 page".
func (m *consentMD) unchecked() string {
	var labels []string
	pages := 0
	for i := range m.r.Pages {
		pg := &m.r.Pages[i]
		hit := false
		for _, k := range runOrder {
			run := pageRun(pg, k)
			if run == nil || !run.Inconclusive() {
				continue
			}
			hit = true
			if l := k.Label(); !slices.Contains(labels, l) {
				labels = append(labels, l)
			}
		}
		if hit {
			pages++
		}
	}
	if len(labels) == 0 {
		return ""
	}
	return andList(labels) + " couldn't be tested on " + plural(pages, "page", "pages")
}

// matrices writes the page by run matrix, one per --sites name worst
// first.
func (m *consentMD) matrices() {
	kinds := m.runKinds()
	if len(kinds) == 0 {
		return
	}
	for _, g := range m.t.siteGroups() {
		if g.site != "" {
			m.line("**%s**", md(g.site))
			m.line("")
		}
		head := "| Page | Banner |"
		rule := "|---|---|"
		for _, k := range kinds {
			head += " " + k.Label() + " |"
			rule += "---|"
		}
		m.line("%s", head)
		m.line("%s", rule)
		for _, pg := range g.pages {
			row := "| " + m.page("", consentPagePath(pg.URL)) + " | " + m.banner(pg) + " |"
			for _, k := range kinds {
				row += " " + m.cell(pg, pageRun(pg, k)) + " |"
			}
			m.line("%s", row)
		}
		m.line("")
	}
}

// runKinds are the run kinds any page ran, in run order: the matrix
// columns.
func (m *consentMD) runKinds() []consent.RunKind {
	var out []consent.RunKind
	for _, k := range runOrder {
		for i := range m.r.Pages {
			if pageRun(&m.r.Pages[i], k) != nil {
				out = append(out, k)
				break
			}
		}
	}
	return out
}

// banner is the matrix's Banner cell.
func (m *consentMD) banner(pg *consent.Page) string {
	switch {
	case pg.NeedsNone:
		return "nothing to consent to"
	case !pg.CMP.Found:
		return "none"
	}
	return md(cmpLabel(pg.CMP))
}

// The states of a matrix cell.
const (
	cellClear        = iota // ran, nothing to report
	cellIssue               // an error or warning
	cellInconclusive        // couldn't choose or check
	cellSkipped             // the page didn't make this run
)

// matrixCell is one run of a page in the matrix, for Markdown and HTML.
// Names are safe to print and not escaped for either.
type matrixCell struct {
	state      int
	names      []string // the trackers (or checks) at issue, or the trackers that fired
	cookieless []bool   // per name when clear: it sent only cookieless pings
	pages      int      // with an issue after Reject or Withdraw: the pages it fired on
	why        string   // inconclusive: why, in a few words
}

// matrixCell sums up run for the matrix: the trackers of its errors and
// warnings (check names without consent/ for issues that name none), or
// what fired.
func (t *consentText) matrixCell(pg *consent.Page, run *consent.Run) matrixCell {
	if run == nil {
		return matrixCell{state: cellSkipped}
	}
	if run.Inconclusive() {
		return matrixCell{state: cellInconclusive, why: cmp.Or(reasonWords[run.Reason], Safe(string(run.Reason)))}
	}
	var c matrixCell
	var checks, ids []string
	for _, is := range t.runIssues(pg, run) {
		if is.Level == model.Notice {
			continue
		}
		if is.Tracker == "" {
			if ch := Safe(strings.TrimPrefix(is.Check, "consent/")); !slices.Contains(checks, ch) {
				checks = append(checks, ch)
			}
			continue
		}
		if n := t.name(is.Tracker); !slices.Contains(c.names, n) {
			c.names = append(c.names, n)
			ids = append(ids, is.Tracker)
		}
	}
	if len(c.names)+len(checks) > 0 {
		c.state = cellIssue
		c.names = append(c.names, checks...)
		c.pages = afterPages(run, ids)
		return c
	}
	seen := map[string]bool{}
	for _, h := range run.Hits {
		if h.Kind == consent.HitScript || !relevant(run.Kind, h.Phase) || h.Need == consent.NeedNo || seen[h.Tracker] {
			continue
		}
		seen[h.Tracker] = true
		c.names = append(c.names, t.name(h.Tracker))
		c.cookieless = append(c.cookieless, h.Cookieless && t.r.Policy == consent.PolicyAdvanced)
	}
	return c
}

// cell is one run of the matrix in Markdown: `-` when the run wasn't
// asked, `? <why>` when it couldn't choose, `✗ <trackers>` with an error
// or warning (and on how many pages after the choice), else what fired.
func (m *consentMD) cell(pg *consent.Page, run *consent.Run) string {
	c := m.t.matrixCell(pg, run)
	switch c.state {
	case cellSkipped:
		return "-"
	case cellInconclusive:
		return "? " + md(c.why)
	}
	names := make([]string, len(c.names))
	for i, n := range c.names {
		names[i] = md(n)
		if i < len(c.cookieless) && c.cookieless[i] {
			names[i] += " (cookieless)"
		}
	}
	if c.state == cellIssue {
		s := "✗ " + andCommaList(names)
		if c.pages > 1 {
			s += fmt.Sprintf(" (%d pages)", c.pages)
		}
		return s
	}
	return andCommaList(names)
}

// afterPages counts the visits after the choice of a Reject or Withdraw
// run where one of the trackers sent a hit.
func afterPages(run *consent.Run, trackers []string) int {
	if run.Kind != consent.RunReject && run.Kind != consent.RunWithdraw {
		return 0
	}
	visits := map[int]bool{}
	for _, h := range run.Hits {
		if h.Kind != consent.HitScript && h.Phase != consent.PhaseBefore && relevant(run.Kind, h.Phase) && slices.Contains(trackers, h.Tracker) {
			visits[h.Visit] = true
		}
	}
	return len(visits)
}

// The level tables, worst first.
var mdLevels = []struct {
	level model.Level
	head  string
}{{model.Error, "Errors"}, {model.Warning, "Warnings"}, {model.Notice, "Notices"}}

// issueTables writes one table per level, Check | Page | Run | What |
// Fix, then the ignored issues. A fix that repeats the one above reads "as
// above".
func (m *consentMD) issueTables() {
	for _, lv := range mdLevels {
		var rows []consent.Issue
		for _, is := range m.r.Issues {
			if is.Ignored == nil && is.Level == lv.level {
				rows = append(rows, is)
			}
		}
		if len(rows) == 0 {
			continue
		}
		m.line("### %s (%d)", lv.head, len(rows))
		m.line("")
		m.line("| Check | Page | Run | What | Fix |")
		m.line("|---|---|---|---|---|")
		lastFix := ""
		for _, is := range rows {
			what := md(is.Title)
			if m.o.Details && is.Detail != "" {
				what += " \\(" + md(is.Detail) + "\\)"
			}
			fix := md(is.Fix)
			if is.Fix != "" && is.Fix == lastFix {
				fix = "as above"
			}
			lastFix = is.Fix
			m.line("| %s | %s | %s | %s | %s |", md(is.Check), m.page(is.Site, is.Page), issueRun(is), what, fix)
		}
		m.line("")
	}
	var ignored []consent.Issue
	for _, is := range m.r.Issues {
		if is.Ignored != nil {
			ignored = append(ignored, is)
		}
	}
	if len(ignored) == 0 {
		return
	}
	m.line("### Ignored (%d)", len(ignored))
	m.line("")
	m.line("| Check | Page | Run | What | Until | Reason |")
	m.line("|---|---|---|---|---|---|")
	for _, is := range ignored {
		m.line("| %s | %s | %s | %s | %s | %s |", md(is.Check), m.page(is.Site, is.Page), issueRun(is), md(is.Title), md(is.Ignored.Until), md(is.Ignored.Reason))
	}
	m.line("")
}

// issueRun is an issue's Run cell: the run's label, "" for an issue of
// the page as a whole.
func issueRun(is consent.Issue) string {
	if is.Run == "" {
		return ""
	}
	return md(is.Run.Label())
}

// runDetails writes, with Details, a collapsed timeline per run: the
// same masked entries -v shows on a terminal.
func (m *consentMD) runDetails() {
	wrote := false
	for i := range m.r.Pages {
		pg := &m.r.Pages[i]
		for j := range pg.Runs {
			run := &pg.Runs[j]
			entries := m.t.timelineEntries(pg, run)
			if len(entries) == 0 {
				continue
			}
			if !wrote {
				m.line("### Runs")
				m.line("")
				wrote = true
			}
			where := consentPagePath(pg.URL)
			if pg.Site != "" {
				where = pg.Site + " " + where
			}
			m.line("<details><summary>%s · %s</summary>", mdHTML(where), run.Kind.Label())
			m.line("")
			for _, e := range entries {
				m.line("- %s %s %s", twoDecimals(e.at), e.typ, md(e.text))
			}
			m.line("")
			m.line("</details>")
			m.line("")
		}
	}
}

// inconclusive writes the runs that couldn't be checked: Page | Run |
// Why, the why in a few words (the full detail with Details), then the
// hints when a banner didn't show.
func (m *consentMD) inconclusive() {
	if len(m.r.Inconclusive) == 0 {
		return
	}
	m.line("### Couldn't check (%d)", len(m.r.Inconclusive))
	m.line("")
	m.line("| Page | Run | Why |")
	m.line("|---|---|---|")
	hint := false
	for _, in := range m.r.Inconclusive {
		why := cmp.Or(reasonWords[in.Reason], md(string(in.Reason)))
		if m.o.Details && in.Detail != "" {
			why = md(in.Detail)
		}
		m.line("| %s | %s | %s |", m.page("", in.Page), md(in.Run.Label()), why)
		hint = hint || in.Reason == consent.ReasonNoBanner || in.Reason == consent.ReasonCMPNotFound
	}
	m.line("")
	if hint {
		for _, h := range noBannerHints {
			m.line("%s", h)
			m.line("")
		}
	}
}

// consentMode writes one sentence per page with Consent Mode: the
// default, then each choice's first update and the gcd note.
func (m *consentMD) consentMode() {
	var paras []string
	for i := range m.r.Pages {
		pg := &m.r.Pages[i]
		cm := pg.ConsentMode
		if cm == nil || !cm.Present {
			continue
		}
		var s string
		if len(cm.Default) == 0 {
			s = "No default; Google hits carry consent state."
		} else {
			s = "Default: "
			if cm.V2 {
				s += "v2, "
			}
			s += md(stateWords(cm.Default)) + " at " + twoDecimals(cm.DefaultAtMS)
			if cm.FirstGoogleHitMS > 0 {
				if cm.DefaultBeforeFirstHit {
					s += ", before the first Google hit"
				} else {
					s += ", after the first Google hit"
				}
			}
			s += "."
		}
		for _, k := range []consent.RunKind{consent.RunAccept, consent.RunReject, consent.RunWithdraw} {
			if run := pageRun(pg, k); run != nil {
				if u := updateWords(run); u != "" {
					s += " " + k.Label() + ": " + u + "."
				}
			}
		}
		if len(m.r.Pages) > 1 {
			s = m.page(pg.Site, consentPagePath(pg.URL)) + ": " + s
		}
		paras = append(paras, s)
	}
	if len(paras) == 0 {
		return
	}
	if slices.Contains(m.r.Notes, noteGCD) {
		paras[len(paras)-1] += " gcd decoded with an unofficial mapping."
	}
	m.line("### Consent Mode")
	m.line("")
	for _, p := range paras {
		m.line("%s", p)
		m.line("")
	}
}

// comparison writes the changes against the baseline: Change | What |
// Page | Run | Before and after.
func (m *consentMD) comparison() {
	c := m.r.Comparison
	if c == nil || len(c.Changes) == 0 {
		return
	}
	m.line("### Changes against %s (%d)", md(cmp.Or(c.Against, "a baseline")), len(c.Changes))
	m.line("")
	m.line("| Change | What | Page | Run | Before and after |")
	m.line("|---|---|---|---|---|")
	for _, ch := range c.Changes {
		title := md(ch.Title)
		if ch.Accepted {
			title += " \\(accepted\\)"
		}
		ba := ""
		if ch.Class == consent.ClassChanged && showBeforeAfter(ch.Key, ch.Before, ch.After) {
			ba = "was " + md(ch.Before) + ", now " + md(ch.After)
		}
		run := ""
		if ch.Run != "" {
			run = md(ch.Run.Label())
		}
		m.line("| %s | %s | %s | %s | %s |", classWords[ch.Class], title, m.page(ch.Site, ch.Page), run, ba)
	}
	m.line("")
}

// footer writes the italic closing line: the legal line, Echo and
// Chrome, the time in UTC, held or sent, the notes that change what the
// result means (TLS not verified, no sandbox, mapped hosts, --geo) and
// consent.json's hash.
func (m *consentMD) footer() {
	parts := []string{noteNotLegalAdvice}
	var who []string
	who = append(who, "Echo "+md(cmp.Or(m.r.Echo, ToolVersion)))
	if m.r.Browser != "" {
		who = append(who, md(strings.Replace(browserName(m.r.Browser), "Chrome for Testing ", "Chrome ", 1)))
	}
	if !m.r.Started.IsZero() {
		who = append(who, m.r.Started.UTC().Format("2006-01-02 15:04")+" UTC")
	}
	parts = append(parts, strings.Join(who, ", ")+".")
	if m.r.Held {
		parts = append(parts, noteHeld)
	} else {
		parts = append(parts, md(noteSent))
	}
	for _, n := range m.r.Notes {
		switch n {
		case noteNotLegalAdvice, noteHeld, noteSent, noteGCD:
		default:
			parts = append(parts, md(n))
		}
	}
	if h := m.o.ConsentSHA256; h != "" {
		parts = append(parts, "consent.json sha256 "+md(h[:min(12, len(h))])+"...")
	}
	m.line("_%s_", strings.Join(parts, " "))
}

// cutMarkdown cuts s at the last line that leaves room for the cut line
// within limit, closing a <details> block it cut into.
func cutMarkdown(s string, limit int) string {
	if len(s) <= limit {
		return s
	}
	tail := "\n" + markdownCutLine + "\n"
	room := limit - len(tail) - len("</details>\n")
	cut := strings.LastIndexByte(s[:max(0, room)], '\n')
	if cut < 0 {
		return strings.TrimPrefix(tail, "\n")
	}
	head := s[:cut+1]
	if strings.Count(head, "<details>") > strings.Count(head, "</details>") {
		head += "</details>\n"
	}
	return head + tail
}

// browserName is a result's browser without a path in parentheses, as
// an older result may have it ("Chrome 153.0.8010.52 (/usr/bin/chrome)"):
// a shared report never names the user's folders.
func browserName(s string) string {
	if i := strings.LastIndex(s, " ("); i > 0 && strings.HasSuffix(s, ")") && strings.ContainsAny(s[i:], `/\`) {
		return s[:i]
	}
	return s
}
