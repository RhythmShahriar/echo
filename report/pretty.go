package report

import (
	"fmt"
	"io"
	"strings"
	"time"

	"github.com/charmbracelet/lipgloss"

	"github.com/RhythmShahriar/echo/check"
	"github.com/RhythmShahriar/echo/model"
	"github.com/RhythmShahriar/rhythm/kit/ui"
)

const (
	width      = 76
	badgeWidth = 10
	indent     = badgeWidth + 2
)

type PrettyOptions struct {
	Plain   bool
	Quiet   bool
	Verbose bool
}

// Pretty renders Echo for a terminal.
type Pretty struct {
	w, errw io.Writer
	o       PrettyOptions
}

func NewPretty(w, errw io.Writer, o PrettyOptions) *Pretty { return &Pretty{w: w, errw: errw, o: o} }

var (
	muted = lipgloss.NewStyle().Foreground(lipgloss.Color(ui.Muted))
	bold  = lipgloss.NewStyle().Bold(true)
)

// ClassColor is the theme color of a change class.
func ClassColor(class string) lipgloss.Color {
	switch class {
	case model.Broke:
		return lipgloss.Color(ui.Broke)
	case model.New:
		return lipgloss.Color(ui.New)
	case model.Changed:
		return lipgloss.Color(ui.Changed)
	case model.Fixed:
		return lipgloss.Color(ui.Fixed)
	}
	return lipgloss.Color(ui.Muted)
}

// LevelColor is the theme color of an issue level.
func LevelColor(l model.Level) lipgloss.Color {
	switch l {
	case model.Error:
		return lipgloss.Color(ui.Danger)
	case model.Warning:
		return lipgloss.Color(ui.High)
	}
	return lipgloss.Color(ui.Muted)
}

func statusColor(s model.Status) lipgloss.Color {
	switch s {
	case model.Failed:
		return lipgloss.Color(ui.Danger)
	case model.Sent:
		return lipgloss.Color(ui.High) // really sent: worth noticing on staging
	case model.Blocked:
		return lipgloss.Color(ui.Warn)
	}
	return lipgloss.Color(ui.Ok)
}

// Header starts output: tool, version and what's being checked.
func (p *Pretty) Header(version, target string) {
	if p.o.Quiet {
		return
	}
	if p.o.Plain {
		fmt.Fprintf(p.w, "rhythm echo %s - %s\n\n", version, target)
		return
	}
	left := ui.Brand.Render("♪") + " " + bold.Render("rhythm echo") + " " + muted.Render(version)
	gap := max(2, width-lipgloss.Width(left)-lipgloss.Width(target))
	fmt.Fprintf(p.w, "\n  %s%s%s\n  %s\n\n", left, strings.Repeat(" ", gap), muted.Render(target), muted.Render(strings.Repeat("─", width)))
}

// Step prints one labeled line.
func (p *Pretty) Step(label, value string) {
	if p.o.Quiet {
		return
	}
	if p.o.Plain {
		fmt.Fprintf(p.w, "%s: %s\n", label, value)
		return
	}
	fmt.Fprintf(p.w, "  %s %s %s\n", ui.Brand.Render("▸"), muted.Bold(true).Width(13).Render(label), value)
}

// Log prints a note to stderr.
func (p *Pretty) Log(msg string) {
	if p.o.Plain {
		fmt.Fprintln(p.errw, msg)
		return
	}
	fmt.Fprintln(p.errw, muted.Render("  · "+msg))
}

// Message prints one captured message as a line: time, channel, where it
// went, what it said, and what Echo did with it.
func (p *Pretty) Message(m model.Message) {
	to := strings.Join(m.To, ", ")
	if to == "" {
		to = hostOf(m.URL)
	}
	what := m.Subject
	if what == "" {
		what = firstLine(m.Text)
	}
	if what == "" && m.Event != "" {
		what = m.Event
	}
	if what == "" && m.Method != "" {
		what = m.Method
		if !strings.Contains(to, "/") { // the path isn't shown yet
			what += " " + pathOf(m.URL)
		}
	}
	kind := check.Kind(m) // the destination is shown next to it
	if p.o.Plain {
		fmt.Fprintf(p.w, "%s %s to %s: %s [%s]\n", m.At.Local().Format("15:04:05"), kind, to, what, m.Status)
		return
	}
	glyph := channelGlyph[m.Channel]
	head := fmt.Sprintf("  %s  %s %s", muted.Render(m.At.Local().Format("15:04:05")), ui.Brand.Render(glyph), bold.Render(ui.Truncate(kind, 22)))
	status := lipgloss.NewStyle().Foreground(statusColor(m.Status)).Render(string(m.Status))
	body := ui.Truncate("→ "+to, 30) + "  " + muted.Render(ui.Truncate(what, 40))
	fmt.Fprintln(p.w, ui.Pad(head, 40)+body+"  "+status)
	if m.Error != "" && p.o.Verbose {
		fmt.Fprintln(p.w, muted.Render("            "+m.Error))
	}
}

// Action reports a finished action.
func (p *Pretty) Action(a *model.Action, done, total int) {
	if p.o.Quiet {
		return
	}
	got := "nothing sent"
	if n := len(a.Messages); n > 0 {
		got = Tally(&model.Run{Actions: []model.Action{*a}})
	}
	if p.o.Plain {
		line := fmt.Sprintf("[%d/%d] %s: %s", done, total, a.Label, got)
		if a.Error != "" {
			line += " (failed: " + a.Error + ")"
		}
		fmt.Fprintln(p.w, line)
		return
	}
	mark := ui.Good.Render("✓")
	if a.Error != "" {
		mark = lipgloss.NewStyle().Foreground(lipgloss.Color(ui.Danger)).Render("✗")
	}
	fmt.Fprintf(p.w, "  %s %s %s\n", mark, ui.Pad(bold.Render(ui.Truncate(a.Label, 34)), 36), muted.Render(got))
	if a.Error != "" {
		fmt.Fprintln(p.w, "    "+muted.Render(ui.Truncate(a.Error, width-4)))
	}
	if p.o.Verbose {
		for _, m := range a.Messages {
			p.Message(m)
		}
	}
}

// Summary prints a run or a comparison.
func (p *Pretty) Summary(r Result, hint string) {
	if r.Compared() {
		p.comparison(r, hint)
	} else {
		p.run(r, hint)
	}
}

func (p *Pretty) comparison(r Result, hint string) {
	c := r.Comparison
	groups := map[string][]model.Change{}
	for _, ch := range c.Changes {
		groups[ch.Class] = append(groups[ch.Class], ch)
	}
	if p.o.Plain {
		if len(c.Changes) > 0 {
			fmt.Fprintln(p.w)
		}
		for _, class := range model.Classes {
			for _, ch := range groups[class] {
				fmt.Fprintf(p.w, "[%s] %s - %s\n", classInfo[class].Label, ch.Title, actionsLabel(ch.Actions, 3))
				if ba := beforeAfter(ch); ba != "" {
					fmt.Fprintf(p.w, "  %s\n", ba)
				}
				if ch.Detail != "" {
					fmt.Fprintf(p.w, "  %s\n", firstLine(ch.Detail))
				}
				if fix := firstLine(ch.Fix); fix != "" && class != model.Fixed {
					fmt.Fprintf(p.w, "  fix: %s\n", fix)
				}
			}
		}
		if p.o.Quiet {
			return
		}
		var parts []string
		for _, class := range model.Classes {
			parts = append(parts, fmt.Sprintf("%d %s", c.Count(class), class))
		}
		fmt.Fprintf(p.w, "\n%s - %s - %s compared, %s\n", verdict(c), strings.Join(parts, ", "), plural(c.Actions, "action", "actions"), Tally(r.Run))
		p.notes(c.Warnings)
		if hint != "" {
			fmt.Fprintln(p.w, hint)
		}
		return
	}
	if len(c.Changes) > 0 {
		fmt.Fprintln(p.w)
	}
	for _, class := range model.Classes {
		cs := groups[class]
		if len(cs) == 0 {
			continue
		}
		fmt.Fprintln(p.w, "  "+ui.Section(classInfo[class].Label, len(cs), ClassColor(class), width)+"\n")
		for _, ch := range cs {
			if class == model.Broke || class == model.New {
				fmt.Fprintln(p.w, card(ui.Badge(classInfo[class].Label, ClassColor(class), true, badgeWidth), ClassColor(class), ch.Title,
					countLabel(ch.Actions), []string{actionsLabel(ch.Actions, 3), beforeAfter(ch), firstLine(ch.Detail)}, ch.Fix))
				fmt.Fprintln(p.w)
			} else {
				fmt.Fprintln(p.w, line(lipgloss.NewStyle().Foreground(ClassColor(class)).Bold(true).Render(classInfo[class].Glyph), ch.Title, countLabel(ch.Actions)))
			}
		}
		if class == model.Changed || class == model.Fixed {
			fmt.Fprintln(p.w)
		}
	}
	if p.o.Quiet {
		return
	}
	fmt.Fprintln(p.w, comparePanel(c))
	fmt.Fprintf(p.w, "  %s\n", muted.Render(fmt.Sprintf("%s compared with %s · %s", plural(c.Actions, "action", "actions"), snapshotName(c.Before), Tally(r.Run))))
	p.notes(c.Warnings)
	if hint != "" {
		fmt.Fprintf(p.w, "\n  %s %s\n", ui.Brand.Render("→"), muted.Render(hint))
	}
	fmt.Fprintln(p.w)
}

func (p *Pretty) run(r Result, hint string) {
	issues := Issues(r.Run)
	counts := Counts(issues)
	if p.o.Plain {
		if len(issues) > 0 {
			fmt.Fprintln(p.w)
		}
		for _, i := range issues {
			fmt.Fprintf(p.w, "[%s] %s - %s\n", strings.ToUpper(string(i.Level)), i.Title, actionsLabel(i.Actions, 3))
			if i.Detail != "" {
				fmt.Fprintf(p.w, "  %s\n", firstLine(i.Detail))
			}
			if fix := firstLine(i.Fix); fix != "" && i.Level != model.Notice {
				fmt.Fprintf(p.w, "  fix: %s\n", fix)
			}
		}
		if p.o.Quiet {
			return
		}
		fmt.Fprintf(p.w, "\n%s - %s, %s, %s - %s, %s\n", capitalize(runVerdict(r.Run, counts)),
			plural(counts[model.Error], "error", "errors"), plural(counts[model.Warning], "warning", "warnings"),
			plural(counts[model.Notice], "notice", "notices"), plural(len(r.Run.Actions), "action", "actions"), Tally(r.Run))
		p.notes(r.Run.Warnings)
		if hint != "" {
			fmt.Fprintln(p.w, hint)
		}
		return
	}
	if len(issues) > 0 {
		fmt.Fprintln(p.w)
	}
	for _, level := range Levels {
		var group []model.Issue
		for _, i := range issues {
			if i.Level == level {
				group = append(group, i)
			}
		}
		if len(group) == 0 {
			continue
		}
		fmt.Fprintln(p.w, "  "+ui.Section(strings.ToUpper(string(level))+"S", len(group), LevelColor(level), width)+"\n")
		for _, i := range group {
			if level == model.Notice {
				fmt.Fprintln(p.w, line(lipgloss.NewStyle().Foreground(LevelColor(level)).Render(levelGlyph[level]), i.Title, countLabel(i.Actions)))
			} else {
				fmt.Fprintln(p.w, card(ui.Badge(strings.ToUpper(string(level)), LevelColor(level), true, badgeWidth), LevelColor(level), i.Title,
					countLabel(i.Actions), []string{firstLine(i.Detail)}, i.Fix))
				fmt.Fprintln(p.w)
			}
		}
		if level == model.Notice {
			fmt.Fprintln(p.w)
		}
	}
	if p.o.Quiet {
		return
	}
	fmt.Fprintln(p.w, runPanel(r.Run, counts))
	fmt.Fprintf(p.w, "  %s\n", muted.Render(fmt.Sprintf("%s · %s · %s · %s mode", plural(len(r.Run.Actions), "action", "actions"), Tally(r.Run),
		(time.Duration(r.Run.Duration)*time.Millisecond).Round(time.Second), r.Run.Mode)))
	p.notes(r.Run.Warnings)
	if hint != "" {
		fmt.Fprintf(p.w, "\n  %s %s\n", ui.Brand.Render("→"), muted.Render(hint))
	}
	fmt.Fprintln(p.w)
}

func (p *Pretty) notes(ns []string) {
	for i, n := range ns {
		if i == 5 {
			n = fmt.Sprintf("…and %d more notes in the report", len(ns)-5)
		}
		if p.o.Plain {
			fmt.Fprintf(p.w, "note: %s\n", n)
		} else {
			fmt.Fprintf(p.w, "  %s\n", muted.Render("! "+ui.Truncate(n, width-2)))
		}
		if i == 5 {
			break
		}
	}
}

// card renders an important change or issue with its details and fix.
func card(badge string, color lipgloss.Color, title, where string, details []string, fix string) string {
	titleCol := width - indent - 2
	head := badge + "  "
	listed := true
	if lipgloss.Width(title)+len(where)+2 <= titleCol {
		head += ui.Pad(bold.Render(title), titleCol-len(where)) + muted.Render(where)
	} else {
		head += bold.Render(ui.Truncate(title, titleCol))
		listed = false
	}
	body := []string{head}
	pad := strings.Repeat(" ", indent)
	for i, d := range details {
		if d == "" || (i == 0 && listed && d == where) {
			continue
		}
		body = append(body, pad+muted.Render(ui.Truncate(d, width-indent-2)))
	}
	if f := firstLine(fix); f != "" {
		body = append(body, pad+ui.Brand.Render("→")+" "+ui.Truncate(f, width-indent-4))
	}
	rail := lipgloss.NewStyle().Border(lipgloss.Border{Left: "▌"}, false, false, false, true).BorderForeground(color).PaddingLeft(1).MarginLeft(1)
	return rail.Render(strings.Join(body, "\n"))
}

func line(marker, title, right string) string {
	right = ui.Truncate(right, 28)
	t := ui.Truncate(title, width-8-len(right)-2)
	return "    " + marker + "  " + ui.Pad(t, width-6-lipgloss.Width(right)) + muted.Render(right)
}

func comparePanel(c *model.Comparison) string {
	color := lipgloss.Color(ui.Ok)
	switch {
	case c.Count(model.Broke) > 0:
		color = ClassColor(model.Broke)
	case c.Count(model.New) > 0:
		color = ClassColor(model.New)
	case c.Count(model.Changed) > 0:
		color = ClassColor(model.Changed)
	}
	head := bold.Render("ECHO") + "   " + lipgloss.NewStyle().Foreground(color).Bold(true).Render(verdict(c))
	var parts []string
	for _, class := range model.Classes {
		n := c.Count(class)
		style := muted
		if n > 0 {
			style = lipgloss.NewStyle().Foreground(ClassColor(class)).Bold(true)
		}
		parts = append(parts, style.Render(fmt.Sprintf("%s %d", classInfo[class].Glyph, n))+" "+muted.Render(class))
	}
	return ui.Panel(head+"\n"+strings.Join(parts, "   "), color)
}

func runPanel(r *model.Run, counts map[model.Level]int) string {
	color := lipgloss.Color(ui.Ok)
	switch {
	case counts[model.Error] > 0:
		color = LevelColor(model.Error)
	case counts[model.Warning] > 0:
		color = LevelColor(model.Warning)
	}
	title := "ECHO"
	verdictText := capitalize(runVerdict(r, counts))
	if r.Name != "" {
		title, verdictText = "SNAPSHOT "+strings.ToUpper(r.Name), "saved"
	}
	head := bold.Render(title) + "   " + lipgloss.NewStyle().Foreground(color).Bold(true).Render(verdictText)
	var parts []string
	for _, l := range Levels {
		n := counts[l]
		style := muted
		if n > 0 {
			style = lipgloss.NewStyle().Foreground(LevelColor(l)).Bold(true)
		}
		parts = append(parts, style.Render(fmt.Sprintf("%s %d", levelGlyph[l], n))+" "+muted.Render(strings.TrimPrefix(plural(n, string(l), string(l)+"s"), fmt.Sprint(n)+" ")))
	}
	body := strings.Join(parts, "   ")
	if counts[model.Error]+counts[model.Warning]+counts[model.Notice] == 0 {
		body = ui.Good.Render("✓ no problems in what was sent")
	}
	return ui.Panel(head+"\n"+body, color)
}

func snapshotName(r model.RunRef) string { return refName(r, true) }

func snapshotAt(r model.RunRef) string { return refName(r, false) }

func refName(r model.RunRef, relative bool) string {
	when := ""
	switch {
	case r.Started.IsZero():
	case relative:
		when = " (" + Ago(r.Started) + ")"
	default:
		when = " (" + r.Started.UTC().Format("2 Jan 2006 15:04 MST") + ")"
	}
	if r.Name != "" {
		return fmt.Sprintf("snapshot %q%s", r.Name, when)
	}
	return "the earlier run" + when
}

// Ago is a short "3h ago".
func Ago(t time.Time) string {
	d := time.Since(t)
	switch {
	case d < time.Minute:
		return "just now"
	case d < time.Hour:
		return fmt.Sprintf("%dm ago", int(d.Minutes()))
	case d < 48*time.Hour:
		return fmt.Sprintf("%dh ago", int(d.Hours()))
	}
	return fmt.Sprintf("%dd ago", int(d.Hours()/24))
}

func hostOf(raw string) string {
	if i := strings.Index(raw, "://"); i >= 0 {
		raw = raw[i+3:]
	}
	h, _, _ := strings.Cut(raw, "/")
	return h
}

func pathOf(raw string) string {
	if i := strings.Index(raw, "://"); i >= 0 {
		raw = raw[i+3:]
	}
	_, p, _ := strings.Cut(raw, "/")
	return "/" + p
}
