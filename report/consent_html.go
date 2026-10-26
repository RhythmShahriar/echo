// Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
// SPDX-License-Identifier: MIT

package report

import (
	"bytes"
	"cmp"
	_ "embed"
	"encoding/base64"
	"fmt"
	"html"
	"html/template"
	"image/png"
	"io"
	"math"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"github.com/RhythmShahriar/echo/consent"
)

//go:embed consent.html.tmpl
var consentHTMLSource string

var consentHTMLTemplate = template.Must(template.New("consent").Parse(consentHTMLSource))

// consentCSP is the evidence report's policy: nothing remote at all,
// screenshots as data: images, inline styles and only the one inline
// script, by hash. Unlike the inbox report it never loads remote
// images, since nothing in it comes from a mail body.
var consentCSP = "default-src 'none'; img-src data:; style-src 'unsafe-inline'; script-src '" + scriptHash(consentHTMLSource) +
	"'; base-uri 'none'; form-action 'none'; frame-src 'none'"

// maxScreenshot caps a screenshot the report embeds; a viewport PNG is
// far smaller, so a bigger file isn't one Echo wrote.
const maxScreenshot = 8 << 20

// paletteSize is the number of tracker colors, c0 to c11 in the
// template's style: trackers take them in the order they first appear
// and the thirteenth and later are grey (cg).
const paletteSize = 12

// The HTML report's data. Every string is plain text that html/template
// escapes; template.HTML and template.URL values are built here from
// escaped text and numbers only.
type (
	consentHTMLData struct {
		CSP, Title, Version, Target, Started string
		Head, Tone, Line                     string
		Chips                                []htmlChip
		Flags                                []chFlag
		Hash                                 string
		Kinds                                []string
		Matrix                               []chMatrixGroup
		Levels                               []chLevel
		Ignored                              []chIssue
		Inconclusive                         []chInconclusive
		Pages                                []chPage
		Runs                                 []chRun
		ConsentMode                          []chConsentMode
		GCDNote                              bool
		Exempt                               []chExempt
		Details                              []chFact
		Comparison                           *chComparison
		Limits                               []template.HTML
	}
	chFlag struct {
		Text string
		Warn bool // it changes what the result means
	}
	chLegend struct {
		Class, Name string
	}
	chMatrixGroup struct {
		Site string
		Rows []chMatrixRow
	}
	chMatrixRow struct {
		Page, Banner string
		Cells        []chCell
	}
	chCell struct {
		Class, Text, Anchor string
	}
	chLevel struct {
		Name, Class string
		Issues      []chIssue
	}
	chIssue struct {
		Check, Level, Page, Run, Title, Detail, Fix, Anchor, Key string
		Docs, DocsName                                           string
		Ignored                                                  string
	}
	chInconclusive struct {
		Page, Run, Detail, Anchor string
	}
	chPage struct {
		ID, URL, Banner, Detected string
		Choices                   []chChoice
	}
	chChoice struct {
		Run, How string
		Shots    []chShot
	}
	chShot struct {
		Label, Missing string
		Src            template.URL
		Box            *chBox
	}
	chBox struct {
		Left, Top, Width, Height float64
	}
	chRun struct {
		ID, Title, Summary string
		SVG                template.HTML
		Legend             []chLegend
		Events             []chEvent
	}
	chEvent struct {
		ID, Time, Type, Tracker, Visit, What string
		Late                                 bool
	}
	chConsentMode struct {
		Page     string
		Summary  string
		Commands []chCommand
		Hits     []chGoogleHit
	}
	chCommand struct {
		Run, Time, Command, State string
	}
	chGoogleHit struct {
		Run, Time, Tracker, GCS, GCSDecoded, GCD, GCDDecoded string
	}
	chExempt struct {
		Name, Category, Need, Docs string
		Hits                       int
	}
	chFact struct {
		Name, Value string
	}
	chComparison struct {
		Against string
		Changes []chChange
	}
	chChange struct {
		Class, Word, Title, Page, Run, BeforeAfter string
	}
)

// consentHTML builds the report's data from one result.
type consentHTML struct {
	t      *consentText
	r      *consent.Result
	o      ConsentHTMLOptions
	colors map[string]int // tracker id -> palette index, in order of first appearance
	docs   map[string]consent.TrackerInfo
	shots  map[string]chShot // read once per path
}

// WriteConsentHTML writes the HTML evidence report of a consent check
// (PRD 9.6): one standalone file with its own CSP, nothing remote, the
// screenshots read from EvidenceDir as data: images, captured URLs as
// text only and a timeline per run drawn as inline SVG. It ends with
// consent.HonestLimits.
func WriteConsentHTML(w io.Writer, r *consent.Result, o ConsentHTMLOptions) error {
	if r == nil {
		r = &consent.Result{}
	}
	h := &consentHTML{t: &consentText{w: &strings.Builder{}, r: r, o: ConsentTextOptions{FailOn: o.FailOn}, tn: trackerNames(r)},
		r: r, o: o, colors: map[string]int{}, docs: catalogDocs(), shots: map[string]chShot{}}
	d := h.data()
	var buf bytes.Buffer
	if err := consentHTMLTemplate.Execute(&buf, d); err != nil {
		return err
	}
	_, err := w.Write(buf.Bytes())
	return err
}

// catalogDocs maps tracker ids to their catalog entries, when the
// catalog is linked into this build.
func catalogDocs() map[string]consent.TrackerInfo {
	out := map[string]consent.TrackerInfo{}
	if c := consent.Linked().Classifier; c != nil {
		for _, ti := range c.Trackers() {
			out[ti.ID] = ti
		}
	}
	return out
}

// docsURL is a tracker's vendor documentation, only when the compiled-in
// catalog gives an https:// URL; a result file can't add links.
func (h *consentHTML) docsURL(tracker string) string {
	u := h.docs[tracker].Docs
	if !strings.HasPrefix(u, "https://") || strings.ContainsAny(u, " \"'<>\\") {
		return ""
	}
	return u
}

// data fills every section in PRD 9.6 order.
func (h *consentHTML) data() consentHTMLData {
	r := h.r
	d := consentHTMLData{CSP: consentCSP, Version: Safe(cmp.Or(r.Echo, ToolVersion)), Hash: Safe(h.o.ConsentSHA256)}
	if len(r.Pages) > 0 {
		d.Target = Safe(r.Pages[0].URL)
		if len(r.Pages) > 1 {
			d.Target += fmt.Sprintf(" (%d pages)", len(r.Pages))
		}
	}
	d.Title = "Echo consent check of " + cmp.Or(d.Target, "no page")
	if !r.Started.IsZero() {
		d.Started = r.Started.UTC().Format("2006-01-02 15:04 UTC")
	}
	h.verdict(&d)
	d.Kinds, d.Matrix = h.matrix()
	d.Levels, d.Ignored = h.issues()
	d.Inconclusive = h.inconclusive()
	d.Pages = h.pages()
	d.Runs = h.runs()
	d.ConsentMode = h.consentMode()
	d.GCDNote = slices.Contains(r.Notes, noteGCD)
	d.Exempt = h.exempt()
	d.Details = h.details()
	d.Comparison = h.comparison()
	for p := range strings.SplitSeq(consent.HonestLimits, "\n\n") {
		d.Limits = append(d.Limits, codeSpans(p))
	}
	return d
}

// codeSpans writes text with `code` spans as HTML: every part escaped,
// the spans in <code>.
func codeSpans(s string) template.HTML {
	var b strings.Builder
	for i, part := range strings.Split(s, "`") {
		if i%2 == 1 {
			b.WriteString("<code>" + html.EscapeString(part) + "</code>")
		} else {
			b.WriteString(html.EscapeString(part))
		}
	}
	return template.HTML(b.String()) //nolint:gosec // built from escaped text
}

// verdict fills the verdict bar: the word and tone, the counts, what
// was checked and the flags that change what the result means.
func (h *consentHTML) verdict(d *consentHTMLData) {
	r := h.r
	s := r.Summary()
	if c := r.Comparison; c != nil {
		d.Head, d.Tone = "No changes", "ok"
		switch {
		case s.Broke > 0:
			d.Head, d.Tone = "Broke", "broke"
		case s.New > 0:
			d.Head, d.Tone = "New", "new"
		case s.Changed > 0:
			d.Head, d.Tone = "Changed", "changed"
		}
		d.Chips = []htmlChip{{"broke", "broke", "", s.Broke}, {"new", "new", "", s.New}, {"changed", "changed", "", s.Changed}, {"fixed", "fixed", "", s.Fixed}}
	} else {
		d.Head, d.Tone = "All clear", "ok"
		switch r.ExitCode(h.o.FailOn) {
		case 1:
			d.Head, d.Tone = "Findings", "broke"
		case 3:
			d.Head, d.Tone = "Couldn't check", "changed"
		}
		d.Chips = []htmlChip{{"error", "errors", "", s.Errors}, {"warning", "warnings", "", s.Warnings}, {"notice", "notices", "", s.Notices},
			{"changed", "couldn't check", "", s.InconclusiveRuns}}
	}
	d.Line = fmt.Sprintf("%s, %s, %s, policy %s", plural(s.Pages, "page", "pages"), plural(s.Runs, "run", "runs"),
		plural(s.Trackers, "tracker", "trackers"), Safe(string(cmp.Or(r.Policy, consent.PolicyAdvanced))))
	if r.Held {
		d.Flags = append(d.Flags, chFlag{"Hits held in the browser", false})
	} else {
		d.Flags = append(d.Flags, chFlag{"Tracker hits were sent for real (--send-hits)", true})
	}
	if !r.TLSVerified {
		d.Flags = append(d.Flags, chFlag{"TLS not verified", true})
	}
	if !r.Sandbox {
		d.Flags = append(d.Flags, chFlag{"sandbox off", true})
	}
	if r.Mapped {
		d.Flags = append(d.Flags, chFlag{"Hosts mapped with --map-host", true})
	}
}

// runAnchor is the id of a run's timeline.
func runAnchor(page int, k consent.RunKind) string {
	return fmt.Sprintf("run-%d-%s", page+1, anchorPart(string(k)))
}

// anchorPart keeps the letters, digits and dashes of s, so a kind read
// from a result file can't break an id.
func anchorPart(s string) string {
	return strings.Map(func(r rune) rune {
		if r >= 'a' && r <= 'z' || r >= '0' && r <= '9' || r == '-' {
			return r
		}
		return -1
	}, strings.ToLower(s))
}

// pageIndex is the index of the page an issue or inconclusive run names,
// or -1.
func (h *consentHTML) pageIndex(site, path string) int {
	for i, pg := range h.r.Pages {
		if pg.Site == site && consentPagePath(pg.URL) == path {
			return i
		}
	}
	for i, pg := range h.r.Pages {
		if consentPagePath(pg.URL) == path {
			return i
		}
	}
	return -1
}

// matrix builds the page by run matrix, one group per --sites name worst
// first: green, red, amber or grey cells linking to their runs.
func (h *consentHTML) matrix() ([]string, []chMatrixGroup) {
	md := &consentMD{t: h.t, r: h.r}
	kinds := md.runKinds()
	labels := make([]string, len(kinds))
	for i, k := range kinds {
		labels[i] = k.Label()
	}
	var groups []chMatrixGroup
	for _, g := range h.t.siteGroups() {
		mg := chMatrixGroup{Site: Safe(g.site)}
		for _, pg := range g.pages {
			idx := 0
			for i := range h.r.Pages {
				if &h.r.Pages[i] == pg {
					idx = i
				}
			}
			row := chMatrixRow{Page: Safe(pg.URL), Banner: htmlBanner(pg)}
			for _, k := range kinds {
				run := pageRun(pg, k)
				c := h.t.matrixCell(pg, run)
				cell := chCell{Anchor: runAnchor(idx, k)}
				switch c.state {
				case cellSkipped:
					cell.Class, cell.Text, cell.Anchor = "skip", "didn't run", ""
				case cellInconclusive:
					cell.Class, cell.Text = "inco", "? "+c.why
				case cellIssue:
					cell.Class, cell.Text = "bad", "✗ "+andCommaList(c.names)
					if c.pages > 1 {
						cell.Text += fmt.Sprintf(" (%d pages)", c.pages)
					}
				default:
					names := slices.Clone(c.names)
					for i := range names {
						if i < len(c.cookieless) && c.cookieless[i] {
							names[i] += " (cookieless)"
						}
					}
					cell.Class, cell.Text = "ok", cmp.Or(andCommaList(names), "nothing fired")
				}
				row.Cells = append(row.Cells, cell)
			}
			mg.Rows = append(mg.Rows, row)
		}
		groups = append(groups, mg)
	}
	return labels, groups
}

// htmlBanner is the matrix's banner cell.
func htmlBanner(pg *consent.Page) string {
	switch {
	case pg.NeedsNone:
		return "nothing to consent to"
	case !pg.CMP.Found:
		return "none"
	}
	return Safe(cmpLabel(pg.CMP))
}

// issues lists the counted issues by level and the ignored ones apart.
func (h *consentHTML) issues() ([]chLevel, []chIssue) {
	var levels []chLevel
	var ignored []chIssue
	for _, lv := range mdLevels {
		l := chLevel{Name: lv.head, Class: string(lv.level)}
		for _, is := range h.r.Issues {
			if is.Level != lv.level {
				continue
			}
			ci := h.issue(is)
			if ig := is.Ignored; ig != nil {
				ci.Ignored = strings.TrimSpace("until " + Safe(ig.Until) + ": " + Safe(ig.Reason))
				ignored = append(ignored, ci)
				continue
			}
			l.Issues = append(l.Issues, ci)
		}
		if len(l.Issues) > 0 {
			levels = append(levels, l)
		}
	}
	return levels, ignored
}

// issue is one issue for the report, linked to the first event behind
// it in its run's timeline (or the run).
func (h *consentHTML) issue(is consent.Issue) chIssue {
	ci := chIssue{Check: Safe(is.Check), Level: string(is.Level), Page: Safe(is.Page), Run: Safe(is.Run.Label()), Title: Safe(is.Title),
		Detail: Safe(is.Detail), Fix: Safe(is.Fix), Key: Safe(is.Key)}
	if is.Site != "" {
		ci.Page = Safe(is.Site) + " " + ci.Page
	}
	if u := h.docsURL(is.Tracker); u != "" {
		ci.Docs, ci.DocsName = u, Safe(cmp.Or(h.docs[is.Tracker].Name, is.Tracker))
	}
	idx := h.pageIndex(is.Site, is.Page)
	if idx < 0 {
		return ci
	}
	ci.Anchor = runAnchor(idx, is.Run)
	if run := pageRun(&h.r.Pages[idx], is.Run); run != nil && len(is.Evidence) > 0 {
		for _, hit := range run.Hits {
			if hit.Seq == is.Evidence[0] {
				ci.Anchor = eventAnchor(idx, is.Run, hit.Seq)
			}
		}
		for _, s := range run.Scripts {
			if s.Seq == is.Evidence[0] && s.Seq != 0 {
				ci.Anchor = eventAnchor(idx, is.Run, s.Seq)
			}
		}
	}
	return ci
}

// eventAnchor is the id of one event's row in a run's table.
func eventAnchor(page int, k consent.RunKind, seq int64) string {
	return fmt.Sprintf("%s-e%d", runAnchor(page, k), seq)
}

// inconclusive lists the runs that couldn't be checked.
func (h *consentHTML) inconclusive() []chInconclusive {
	var out []chInconclusive
	for _, in := range h.r.Inconclusive {
		ci := chInconclusive{Page: Safe(in.Page), Run: Safe(in.Run.Label()), Detail: Safe(in.Detail)}
		if idx := h.pageIndex("", in.Page); idx >= 0 {
			ci.Anchor = runAnchor(idx, in.Run)
		}
		out = append(out, ci)
	}
	return out
}

// pages builds each page's banner panel: the CMP, how it was found and
// for each run its screenshots and how the choice was made.
func (h *consentHTML) pages() []chPage {
	var out []chPage
	for i := range h.r.Pages {
		pg := &h.r.Pages[i]
		p := chPage{ID: fmt.Sprintf("page-%d", i+1), URL: Safe(pg.URL)}
		switch {
		case pg.NeedsNone:
			p.Banner = nothingToConsent
		case pg.CMP.Found:
			p.Banner = Safe(cmpLabel(pg.CMP)) + ", found after " + oneDecimal(pg.CMP.BannerAtMS)
			if pg.CMP.TCF {
				p.Banner += " (TCF)"
			}
		default:
			p.Banner = "none found" + scriptLoaded(pg.CMP)
		}
		if pg.CMP.DetectedBy != "" {
			p.Detected = "detected by " + Safe(detectedBy(pg.CMP.DetectedBy))
		}
		for j := range pg.Runs {
			run := &pg.Runs[j]
			c := chChoice{Run: run.Kind.Label(), How: choiceWords(pg, run)}
			for _, name := range []string{"banner", "after"} {
				rel := run.Screenshots[name]
				if rel == "" && name == "after" && run.Choice == nil {
					continue // a run without a choice has nothing after it
				}
				shot := h.screenshot(rel)
				shot.Label = map[string]string{"banner": "Banner", "after": "After the choice"}[name]
				if name == "banner" && run.Choice != nil && run.Choice.Box != nil && shot.Src != "" {
					shot.Box = h.box(rel, run.Choice.Box)
				}
				c.Shots = append(c.Shots, shot)
			}
			p.Choices = append(p.Choices, c)
		}
		out = append(out, p)
	}
	return out
}

// choiceWords says how a run made its choice: `Clicked "Deny"
// (.cmplz-deny) on the first layer, 1 click at 1.98s, confirmed by
// cookie:cmplz_marketing`.
func choiceWords(pg *consent.Page, run *consent.Run) string {
	if run.Inconclusive() {
		return "Couldn't choose: " + cmp.Or(reasonWords[run.Reason], Safe(string(run.Reason)))
	}
	c := run.Choice
	if run.Kind == consent.RunWithdraw && run.Withdraw != nil {
		c = run.Withdraw
	}
	if c == nil {
		return "No choice made"
	}
	var s string
	if c.How == "api" {
		s = "Used " + Safe(cmpLabel(pg.CMP)) + "'s API"
	} else {
		s = "Clicked " + clickText(pg, c) + " on the " + layerWords(c.Layer) + ", " + plural(max(c.Clicks, 1), "click", "clicks")
	}
	s += " at " + twoDecimals(c.AtMS)
	if c.Fallback {
		s += " (the button failed, so the API was used)"
	}
	if c.Verified {
		s += ", confirmed by " + Safe(cmp.Or(c.VerifiedBy, "the CMP"))
	} else {
		s += ", not confirmed"
	}
	return s
}

// screenshot reads one screenshot from the evidence folder as a data:
// URL. The path comes from the result file, so it must stay inside the
// folder: os.Root refuses .. and links that leave it.
func (h *consentHTML) screenshot(rel string) chShot {
	if s, ok := h.shots[rel]; ok {
		return s
	}
	shot := chShot{Missing: "screenshot not taken"}
	if rel != "" && h.o.EvidenceDir != "" && filepath.IsLocal(filepath.FromSlash(rel)) {
		if data, err := readEvidence(h.o.EvidenceDir, rel); err == nil {
			if _, err := png.DecodeConfig(bytes.NewReader(data)); err == nil {
				shot = chShot{Src: template.URL("data:image/png;base64," + base64.StdEncoding.EncodeToString(data))} //nolint:gosec // a checked PNG, base64
			} else {
				shot.Missing = "screenshot isn't a PNG"
			}
		}
	}
	h.shots[rel] = shot
	return shot
}

// readEvidence reads rel inside dir, at most maxScreenshot bytes.
func readEvidence(dir, rel string) ([]byte, error) {
	root, err := os.OpenRoot(dir)
	if err != nil {
		return nil, err
	}
	defer func() { _ = root.Close() }()
	f, err := root.Open(filepath.FromSlash(rel))
	if err != nil {
		return nil, err
	}
	defer func() { _ = f.Close() }()
	data, err := io.ReadAll(io.LimitReader(f, maxScreenshot+1))
	if err != nil {
		return nil, err
	}
	if len(data) > maxScreenshot {
		return nil, fmt.Errorf("%s is over %d bytes", rel, maxScreenshot)
	}
	return data, nil
}

// box places the clicked button's outline on a screenshot, in percent
// of the image. The screenshot is the viewport at one image pixel per
// CSS pixel, as browse takes it, so the box's CSS pixels map straight
// onto the image.
func (h *consentHTML) box(rel string, b *consent.Box) *chBox {
	data, err := readEvidence(h.o.EvidenceDir, rel)
	if err != nil {
		return nil
	}
	cfg, err := png.DecodeConfig(bytes.NewReader(data))
	if err != nil || cfg.Width == 0 || cfg.Height == 0 || b.W <= 0 || b.H <= 0 {
		return nil
	}
	pct := func(v float64, of int) float64 {
		p := v / float64(of) * 100
		if math.IsNaN(p) || math.IsInf(p, 0) {
			return 0
		}
		return math.Round(min(max(p, 0), 100)*100) / 100
	}
	out := &chBox{Left: pct(b.X, cfg.Width), Top: pct(b.Y, cfg.Height), Width: pct(b.W, cfg.Width), Height: pct(b.H, cfg.Height)}
	out.Width = min(out.Width, 100-out.Left)
	out.Height = min(out.Height, 100-out.Top)
	return out
}

// color is a tracker's palette class: c0 to c11 in order of first
// appearance, then cg (grey).
func (h *consentHTML) color(tracker string) string {
	if tracker == "" {
		return "cg"
	}
	i, ok := h.colors[tracker]
	if !ok {
		i = len(h.colors)
		h.colors[tracker] = i
	}
	if i >= paletteSize {
		return "cg"
	}
	return fmt.Sprintf("c%d", i)
}

// runs builds each run's timeline and event table.
func (h *consentHTML) runs() []chRun {
	var out []chRun
	for i := range h.r.Pages {
		pg := &h.r.Pages[i]
		for j := range pg.Runs {
			run := &pg.Runs[j]
			cr := chRun{ID: runAnchor(i, run.Kind), Title: Safe(consentPagePath(pg.URL)) + " · " + run.Kind.Label()}
			if pg.Site != "" {
				cr.Title = Safe(pg.Site) + " " + cr.Title
			}
			_, cr.Summary = h.t.runSummary(pg, run)
			cr.SVG = h.timelineSVG(i, run)
			cr.Events = h.events(i, pg, run)
			seen := map[string]bool{}
			for _, hit := range run.Hits {
				if !seen[hit.Tracker] {
					seen[hit.Tracker] = true
					cr.Legend = append(cr.Legend, chLegend{h.color(hit.Tracker), h.t.name(hit.Tracker)})
				}
			}
			out = append(out, cr)
		}
	}
	return out
}

// late reports whether a hit came after the choice in a Reject or
// Withdraw run: the hits the timeline outlines in red.
func late(run *consent.Run, ph consent.Phase) bool {
	return run.Kind == consent.RunReject && ph == consent.PhaseAfterReject || run.Kind == consent.RunWithdraw && ph == consent.PhaseAfterWithdraw
}

// The timeline's drawing area, in SVG units.
const (
	svgWidth  = 760
	svgLeft   = 70
	svgRight  = 14
	svgHeight = 118
	rowHits   = 24
	rowCookie = 50
	rowSignal = 76
	rowAxis   = 96
)

// timelineSVG draws a run as inline SVG: an axis from 0 to the run's
// duration, the choice as a vertical line, hits as filled marks colored
// by tracker (outlined red after the choice in Reject and Withdraw
// runs), cookies as hollow marks and signals as diamonds. Every text in
// it is escaped; everything else is numbers and fixed class names.
func (h *consentHTML) timelineSVG(page int, run *consent.Run) template.HTML {
	end := run.DurationMS
	for _, hit := range run.Hits {
		end = max(end, hit.AtMS)
	}
	for _, c := range run.Cookies {
		end = max(end, c.AtMS)
	}
	for _, s := range run.Signals {
		end = max(end, s.AtMS)
	}
	end = max(end, 1)
	x := func(ms int64) float64 {
		return math.Round((svgLeft+float64(max(ms, 0))/float64(end)*(svgWidth-svgLeft-svgRight))*10) / 10
	}
	var b strings.Builder
	fmt.Fprintf(&b, `<svg class="timeline" viewBox="0 0 %d %d" role="img" aria-label="%s">`, svgWidth, svgHeight,
		html.EscapeString("Timeline of the "+run.Kind.Label()+" run"))
	for _, row := range []struct {
		y    int
		name string
	}{{rowHits, "hits"}, {rowCookie, "cookies"}, {rowSignal, "signals"}} {
		fmt.Fprintf(&b, `<text class="lbl" x="4" y="%d">%s</text><line class="row" x1="%d" y1="%d" x2="%d" y2="%d"/>`,
			row.y+4, row.name, svgLeft, row.y, svgWidth-svgRight, row.y)
	}
	fmt.Fprintf(&b, `<line class="axis" x1="%d" y1="%d" x2="%d" y2="%d"/>`, svgLeft, rowAxis, svgWidth-svgRight, rowAxis)
	step := tickStep(end)
	for ms := int64(0); ms <= end; ms += step {
		fmt.Fprintf(&b, `<line class="tick" x1="%.1f" y1="%d" x2="%.1f" y2="%d"/><text class="tl" x="%.1f" y="%d">%s</text>`,
			x(ms), rowAxis, x(ms), rowAxis+4, x(ms), rowAxis+16, tickLabel(ms))
	}
	for _, c := range []*consent.Choice{run.Choice, run.Withdraw} {
		if c == nil {
			continue
		}
		fmt.Fprintf(&b, `<line class="choice" x1="%.1f" y1="8" x2="%.1f" y2="%d"><title>%s</title></line>`, x(c.AtMS), x(c.AtMS), rowAxis,
			html.EscapeString("choice at "+twoDecimals(c.AtMS)))
	}
	for _, hit := range run.Hits {
		cls := "hit " + h.color(hit.Tracker)
		if hit.Kind == consent.HitScript {
			cls = "hit script " + h.color(hit.Tracker)
		}
		if late(run, hit.Phase) {
			cls += " late"
		}
		fmt.Fprintf(&b, `<a href="#%s"><circle class="%s" cx="%.1f" cy="%d" r="5"><title>%s</title></circle></a>`,
			eventAnchor(page, run.Kind, hit.Seq), cls, x(hit.AtMS), rowHits,
			html.EscapeString(twoDecimals(hit.AtMS)+" "+h.t.hitText(&hit)))
	}
	for _, c := range run.Cookies {
		fmt.Fprintf(&b, `<circle class="cookie %s" cx="%.1f" cy="%d" r="5"><title>%s</title></circle>`, h.color(c.Tracker), x(c.AtMS), rowCookie,
			html.EscapeString(twoDecimals(c.AtMS)+" cookie "+Safe(c.Name)))
	}
	for _, s := range run.Signals {
		cx := x(s.AtMS)
		fmt.Fprintf(&b, `<polygon class="signal" points="%.1f,%d %.1f,%d %.1f,%d %.1f,%d"><title>%s</title></polygon>`,
			cx, rowSignal-6, cx+6, rowSignal, cx, rowSignal+6, cx-6, rowSignal,
			html.EscapeString(twoDecimals(s.AtMS)+" "+Safe(s.Source)+" "+Safe(s.Command)+" "+timelineState(s.State)))
	}
	b.WriteString(`</svg>`)
	return template.HTML(b.String()) //nolint:gosec // numbers, fixed names and escaped text
}

// tickStep picks an axis step that gives at most ten ticks.
func tickStep(end int64) int64 {
	for _, s := range []int64{500, 1000, 2000, 5000, 10000, 15000, 30000, 60000, 120000, 300000} {
		if end/s <= 10 {
			return s
		}
	}
	return 600000
}

// tickLabel is an axis label: 0s, 2s, 1.5s.
func tickLabel(ms int64) string {
	if ms%1000 == 0 {
		return fmt.Sprintf("%ds", ms/1000)
	}
	return fmt.Sprintf("%.1fs", float64(ms)/1000)
}

// events lists every event of a run for its table: visits, the banner
// and clicks, signals, scripts, hits, cookies and storage keys.
func (h *consentHTML) events(page int, pg *consent.Page, run *consent.Run) []chEvent {
	type ev struct {
		at int64
		e  chEvent
	}
	var evs []ev
	add := func(at int64, e chEvent) {
		e.Time = twoDecimals(at)
		evs = append(evs, ev{at, e})
	}
	for i, v := range run.Visits {
		add(v.AtMS, chEvent{Type: cmp.Or(Safe(v.Step), "load"), Visit: fmt.Sprint(i), What: Safe(v.URL)})
	}
	if pg.CMP.Found && run.Status != consent.StatusNoBanner {
		add(pg.CMP.BannerAtMS, chEvent{Type: "banner", What: Safe(cmpLabel(pg.CMP)) + " shown"})
	}
	for _, c := range []*consent.Choice{run.Choice, run.Withdraw} {
		if c != nil {
			add(c.AtMS, chEvent{Type: "click", What: clickText(pg, c)})
		}
	}
	for _, s := range run.Signals {
		what := Safe(s.Source) + " " + Safe(s.Command)
		if st := timelineState(s.State); st != "" {
			what += " " + st
		}
		add(s.AtMS, chEvent{Type: "signal", What: what})
	}
	for _, s := range run.Scripts {
		e := chEvent{Type: "script", Tracker: h.t.name(s.Tracker), What: Safe(s.URL)}
		if s.Seq != 0 {
			e.ID = eventAnchor(page, run.Kind, s.Seq)
		}
		add(s.AtMS, e)
	}
	for _, hit := range run.Hits {
		what := Safe(hit.URL)
		if hit.Held {
			what += " (held)"
		}
		add(hit.AtMS, chEvent{ID: eventAnchor(page, run.Kind, hit.Seq), Type: "hit", Tracker: h.t.name(hit.Tracker), Visit: fmt.Sprint(hit.Visit),
			What: what, Late: late(run, hit.Phase)})
	}
	for _, c := range run.Cookies {
		what := Safe(c.Name)
		if c.Domain != "" {
			what += " on " + Safe(c.Domain)
		}
		if c.Blocked != "" {
			what += " (refused: " + Safe(c.Blocked) + ")"
		}
		tr := ""
		if c.Tracker != "" {
			tr = h.t.name(c.Tracker)
		}
		add(c.AtMS, chEvent{Type: "cookie", Tracker: tr, What: what})
	}
	for _, k := range run.Storage {
		tr := ""
		if k.Tracker != "" {
			tr = h.t.name(k.Tracker)
		}
		add(k.AtMS, chEvent{Type: "storage", Tracker: tr, What: Safe(k.Area) + " " + Safe(k.Key)})
	}
	slices.SortStableFunc(evs, func(a, b ev) int { return cmp.Compare(a.at, b.at) })
	out := make([]chEvent, len(evs))
	for i, e := range evs {
		out[i] = e.e
	}
	return out
}

// consentMode builds the Consent Mode panel per page: the default and
// update commands of every run and each Google hit's gcs and gcd, raw
// and decoded.
func (h *consentHTML) consentMode() []chConsentMode {
	var out []chConsentMode
	for i := range h.r.Pages {
		pg := &h.r.Pages[i]
		cm := chConsentMode{Page: Safe(pg.URL), Summary: consentModeLine(pg.ConsentMode)}
		for _, run := range pg.Runs {
			for _, s := range run.Signals {
				if s.Source != "gtag" && s.Source != "datalayer" {
					continue
				}
				cm.Commands = append(cm.Commands, chCommand{Run: run.Kind.Label(), Time: twoDecimals(s.AtMS),
					Command: Safe(s.Source) + " consent " + Safe(s.Command), State: timelineState(s.State)})
			}
			for _, hit := range run.Hits {
				g := hit.Google
				if g == nil || g.GCS == "" && g.GCD == "" {
					continue
				}
				cm.Hits = append(cm.Hits, chGoogleHit{Run: run.Kind.Label(), Time: twoDecimals(hit.AtMS), Tracker: h.t.name(hit.Tracker),
					GCS: Safe(g.GCS), GCSDecoded: gcsWords(g.GCSDecoded), GCD: Safe(g.GCD), GCDDecoded: gcdWords(g.GCDDecoded)})
			}
		}
		if cm.Summary == "" && len(cm.Commands) == 0 && len(cm.Hits) == 0 {
			continue
		}
		out = append(out, cm)
	}
	return out
}

// gcsWords reads a decoded gcs: "ad_storage denied, analytics_storage
// granted", or "unknown".
func gcsWords(g *consent.GCS) string {
	if g == nil || !g.Known {
		return "unknown"
	}
	return "ad_storage " + Safe(string(g.AdStorage)) + ", analytics_storage " + Safe(string(g.AnalyticsStorage))
}

// gcdWords reads a decoded gcd, one signal after another: "ad_storage
// default denied, update unset (p)".
func gcdWords(g *consent.GCD) string {
	if g == nil || !g.Known {
		return "unknown"
	}
	var parts []string
	for _, s := range []struct {
		name string
		sig  consent.GCDSignal
	}{{"ad_storage", g.AdStorage}, {"analytics_storage", g.AnalyticsStorage}, {"ad_user_data", g.AdUserData}, {"ad_personalization", g.AdPersonalization}} {
		if !s.sig.Known {
			parts = append(parts, s.name+" unknown ("+Safe(s.sig.Letter)+")")
			continue
		}
		parts = append(parts, fmt.Sprintf("%s default %s, update %s (%s)", s.name, Safe(cmp.Or(string(s.sig.Default), "unset")),
			Safe(cmp.Or(string(s.sig.Update), "unset")), Safe(s.sig.Letter)))
	}
	return strings.Join(parts, "; ")
}

// exempt lists the services seen that need no consent: exempt
// (cookieless analytics, --exempt) and strictly necessary ones.
func (h *consentHTML) exempt() []chExempt {
	var order []string
	by := map[string]*chExempt{}
	for _, pg := range h.r.Pages {
		for _, run := range pg.Runs {
			for _, hit := range run.Hits {
				if hit.Need != consent.NeedExempt && hit.Need != consent.NeedNo {
					continue
				}
				e := by[hit.Tracker]
				if e == nil {
					need := "exempt"
					if hit.Need == consent.NeedNo {
						need = "not needed (necessary)"
					}
					e = &chExempt{Name: h.t.name(hit.Tracker), Category: Safe(string(hit.Category)), Need: need, Docs: h.docsURL(hit.Tracker)}
					by[hit.Tracker] = e
					order = append(order, hit.Tracker)
				}
				e.Hits++
			}
		}
	}
	out := make([]chExempt, len(order))
	for i, id := range order {
		out[i] = *by[id]
	}
	return out
}

// details lists how the check ran, every value from the result.
func (h *consentHTML) details() []chFact {
	r := h.r
	yes := func(b bool, y, n string) string {
		if b {
			return y
		}
		return n
	}
	facts := []chFact{
		{"Echo", Safe(cmp.Or(r.Echo, ToolVersion))},
		{"Browser", Safe(r.Browser)},
		{"OS", Safe(r.OS)},
		{"User agent", Safe(r.UserAgent)},
		{"Geo", Safe(cmp.Or(r.Geo, "none"))},
		{"Locale", Safe(r.Locale)},
		{"Time zone", Safe(r.Timezone)},
		{"Viewport", Safe(r.Viewport)},
		{"Started", ""},
		{"Duration", twoDecimals(r.DurationMS)},
		{"Policy", Safe(string(cmp.Or(r.Policy, consent.PolicyAdvanced)))},
		{"Tracker hits", yes(r.Held, "held in the browser", "sent for real (--send-hits)")},
		{"Hosts mapped", yes(r.Mapped, "yes (--map-host)", "no")},
		{"Sandbox", yes(r.Sandbox, "on", "off")},
		{"TLS", yes(r.TLSVerified, "verified", "not verified")},
		{"Tracker catalog", dataWords(r.Catalog)},
		{"CMP rules", dataWords(r.CMPRules)},
		{"Network", fmt.Sprintf("%d requests, %d held, %d refused, %d browser internal", r.Network.Requests, r.Network.Held, r.Network.Refused, r.Network.BrowserInternal)},
		{"Result", Safe(r.ID)},
		{"Evidence folder", Safe(r.Evidence.Folder)},
	}
	if !r.Started.IsZero() {
		facts[8].Value = r.Started.UTC().Format("2006-01-02 15:04:05 UTC")
	}
	out := facts[:0]
	for _, f := range facts {
		if f.Value != "" {
			out = append(out, f)
		}
	}
	return out
}

// dataWords names a data set: "1.2.0 (sha256 4be1..., 47 entries)".
func dataWords(d consent.DataInfo) string {
	if d.Version == "" && d.SHA256 == "" {
		return ""
	}
	return fmt.Sprintf("%s (sha256 %s..., %d entries)", Safe(d.Version), Safe(d.SHA256[:min(8, len(d.SHA256))]), d.Count)
}

// comparison lists the changes against the baseline.
func (h *consentHTML) comparison() *chComparison {
	c := h.r.Comparison
	if c == nil {
		return nil
	}
	out := &chComparison{Against: Safe(cmp.Or(c.Against, "a baseline"))}
	if !c.BaseAt.IsZero() {
		out.Against += " from " + c.BaseAt.UTC().Format("2006-01-02 15:04 UTC")
	}
	for _, ch := range c.Changes {
		cc := chChange{Class: Safe(ch.Class), Word: classWords[ch.Class], Title: Safe(ch.Title), Page: Safe(ch.Page)}
		if ch.Run != "" {
			cc.Run = ch.Run.Label()
		}
		if ch.Accepted {
			cc.Title += " (accepted)"
		}
		if ch.Before != "" || ch.After != "" {
			cc.BeforeAfter = "was " + Safe(cmp.Or(ch.Before, "none")) + ", now " + Safe(cmp.Or(ch.After, "none"))
		}
		out.Changes = append(out.Changes, cc)
	}
	return out
}
