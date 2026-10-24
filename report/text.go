// Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
// SPDX-License-Identifier: MIT

package report

import (
	"encoding/json"
	"encoding/xml"
	"fmt"
	"io"
	"slices"
	"strings"
	"time"

	"github.com/RhythmShahriar/echo/check"
	"github.com/RhythmShahriar/echo/compare"
	"github.com/RhythmShahriar/echo/model"
)

// JSON writes the comparison, or the run, bare in the rhythm-echo/v1
// schema, as run.json and comparison.json store them. Output for people
// and scripts goes through WriteJSON, which wraps it in the Envelope.
func JSON(w io.Writer, r Result) error {
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	enc.SetEscapeHTML(false)
	if r.Compared() {
		return enc.Encode(r.Comparison)
	}
	return enc.Encode(r.Run)
}

// Markdown writes a report for pull requests and CI summaries.
func Markdown(w io.Writer, r Result) error {
	var b strings.Builder
	run := r.Run
	b.WriteString("## ♪ Echo\n\n")
	if r.Compared() {
		c := r.Comparison
		count, with := comparedWith(r)
		fmt.Fprintf(&b, "**%s.** %s %s %s; this run sent %s.\n\n", verdict(r), count, with, snapshotAt(c.Before), Tally(run))
		b.WriteString("| Broke | New | Changed | Fixed |\n|---:|---:|---:|---:|\n|")
		for _, class := range model.Classes {
			fmt.Fprintf(&b, " %d |", c.Count(class))
		}
		b.WriteString("\n\n")
		for _, class := range model.Classes {
			var cs []model.Change
			for _, ch := range c.Changes {
				if ch.Class == class {
					cs = append(cs, ch)
				}
			}
			if len(cs) == 0 {
				continue
			}
			info := classInfo[class]
			fmt.Fprintf(&b, "### %s %s (%d)\n\n_%s._\n\n| Change | Actions | Before → after | Fix |\n|---|---|---|---|\n", info.Glyph, info.Label, len(cs), check.Capitalize(info.Blurb))
			for _, ch := range cs {
				title := cell(ch.Title)
				if ch.Detail != "" {
					title += "<br><sub>" + cell(firstLine(ch.Detail)) + "</sub>"
				}
				fix := ""
				if class != model.Fixed {
					fix = cell(firstLine(ch.Fix))
				}
				fmt.Fprintf(&b, "| %s | %s | %s | %s |\n", title, cell(actionsLabel(ch.Actions, 3)), cell(beforeAfter(ch)), fix)
			}
			b.WriteString("\n")
			for _, ch := range cs {
				markdownDiff(&b, ch)
			}
		}
		attachmentTable(&b, run)
		notes(&b, c.Warnings)
	} else {
		issues := Issues(run)
		counts := Counts(issues)
		fmt.Fprintf(&b, "**%s.** %s run; %s sent.\n\n", check.Capitalize(runVerdict(run, counts)), plural(len(run.Actions), "action", "actions"), Tally(run))
		b.WriteString("| Action | Sent | Issues |\n|---|---|---:|\n")
		for _, a := range run.Actions {
			sent := Tally(&model.Run{Actions: []model.Action{a}})
			if a.Error != "" {
				sent = "failed: " + cell(firstLine(a.Error))
			}
			fmt.Fprintf(&b, "| %s | %s | %d |\n", cell(a.Label), sent, len(a.Issues))
		}
		b.WriteString("\n")
		if len(issues) > 0 {
			b.WriteString("| Level | Issue | Actions | Fix |\n|---|---|---|---|\n")
			for _, i := range issues {
				fmt.Fprintf(&b, "| %s | %s | %s | %s |\n", i.Level, cell(i.Title), cell(actionsLabel(i.Actions, 3)), cell(firstLine(i.Fix)))
			}
			b.WriteString("\n")
		}
		attachmentTable(&b, run)
		notes(&b, run.Warnings)
	}
	fmt.Fprintf(&b, "<sub>rhythm-echo %s · %s mode · %s</sub>\n", run.Version, run.Mode, run.Started.UTC().Format("2006-01-02 15:04 MST"))
	_, err := io.WriteString(w, b.String())
	return err
}

// attachmentTable lists the files the run's emails carry, when any do, so
// a reviewer sees the invoice is still attached.
func attachmentTable(b *strings.Builder, run *model.Run) {
	var rows []string
	for _, a := range run.Actions {
		for _, m := range a.Messages {
			if files := attachmentList(m); files != "" {
				rows = append(rows, fmt.Sprintf("| %s | %s | %s |\n", cell(check.Capitalize(check.Describe(m))+" to "+strings.Join(m.To, ", ")), cell(a.Label), cell(files)))
			}
		}
	}
	if len(rows) == 0 {
		return
	}
	b.WriteString("| Email | Action | Attachments |\n|---|---|---|\n")
	b.WriteString(strings.Join(rows, ""))
	b.WriteString("\n")
}

// mdHunks caps the hunks per change in Markdown: a PR comment has a size
// limit while the HTML report has them all.
const mdHunks = 10

// markdownDiff writes a text or payload change's hunks as a collapsed
// diff block, each hunk headed "@@ line 9 added: … @@".
func markdownDiff(b *strings.Builder, ch model.Change) {
	hs := compare.ChangeDiff(ch)
	if len(hs) == 0 {
		return
	}
	var body []string
	for i, h := range hs {
		if i == mdHunks {
			body = append(body, fmt.Sprintf("@@ +%d more in the HTML report @@", len(hs)-mdHunks))
			break
		}
		body = append(body, "@@ "+Safe(h.Summary())+" @@")
		body = append(body, hunkText(h)...)
	}
	text := strings.Join(body, "\n")
	// A fence longer than any run of backticks in the text, so a captured
	// line can't close it.
	fence := strings.Repeat("`", max(3, longestRun(text, '`')+1))
	fmt.Fprintf(b, "<details><summary>Diff: %s</summary>\n\n%sdiff\n%s\n%s\n\n</details>\n\n", cell(ch.Title), fence, text, fence)
}

func longestRun(s string, c byte) int {
	best, n := 0, 0
	for i := 0; i < len(s); i++ {
		if s[i] == c {
			n++
			best = max(best, n)
		} else {
			n = 0
		}
	}
	return best
}

func notes(b *strings.Builder, ns []string) {
	if len(ns) == 0 {
		return
	}
	fmt.Fprintf(b, "<details><summary>%s</summary>\n\n", plural(len(ns), "note", "notes"))
	for _, n := range ns {
		fmt.Fprintf(b, "- %s\n", cell(n))
	}
	b.WriteString("\n</details>\n\n")
}

// cell makes text safe for a Markdown table cell: one line, no controls
// and nothing that starts a column, code span, link or HTML tag.
func cell(s string) string {
	return Safe(cellEscaper.Replace(s))
}

var cellEscaper = strings.NewReplacer("|", `\|`, "`", "\\`", "[", `\[`, "]", `\]`, "\n", " ", "\r", "", "<", "&lt;", ">", "&gt;")

type junitSuites struct {
	XMLName  xml.Name     `xml:"testsuites"`
	Name     string       `xml:"name,attr"`
	Tests    int          `xml:"tests,attr"`
	Failures int          `xml:"failures,attr"`
	Time     float64      `xml:"time,attr"`
	Suites   []junitSuite `xml:"testsuite"`
}

type junitSuite struct {
	Name      string      `xml:"name,attr"`
	Tests     int         `xml:"tests,attr"`
	Failures  int         `xml:"failures,attr"`
	Timestamp string      `xml:"timestamp,attr"`
	Cases     []junitCase `xml:"testcase"`
}

type junitCase struct {
	Name      string        `xml:"name,attr"`
	Classname string        `xml:"classname,attr"`
	Failure   *junitFailure `xml:"failure,omitempty"`
	Out       string        `xml:"system-out,omitempty"`
}

type junitFailure struct {
	Message string `xml:"message,attr"`
	Type    string `xml:"type,attr"`
	Text    string `xml:",chardata"`
}

// JUnit writes one suite per change class (or per action, for a run).
// Cases at or above failOn fail, as does a failed action; with "none"
// nothing fails. "" means DefaultFailOn: BROKE for a comparison, errors
// for a run. Like FailOn it takes a Threshold or a plain string.
func JUnit[T ~string](w io.Writer, r Result, failOn T) error {
	t := Threshold(strings.ToLower(strings.TrimSpace(string(failOn))))
	if t == "" {
		t = DefaultFailOn(r.Compared())
	}
	out := junitSuites{Name: "echo", Time: r.Run.Duration / 1000}
	stamp := r.Run.Started.UTC().Format(time.RFC3339)
	if r.Compared() {
		limit := slices.Index(model.Classes, string(t)) // -1 for none: nothing fails
		for i, class := range model.Classes {
			s := junitSuite{Name: "echo." + class, Timestamp: stamp}
			for _, ch := range r.Comparison.Changes {
				if ch.Class != class {
					continue
				}
				text := strings.TrimSpace(strings.Join([]string{"actions: " + actionsLabel(ch.Actions, 10), beforeAfter(ch), ch.Detail, ch.Fix}, "\n"))
				text = safeLines(text)
				tc := junitCase{Name: Safe(ch.Title), Classname: strings.ReplaceAll(ch.Check, "/", "."), Out: text}
				if i <= limit && class != model.Fixed {
					tc.Failure, tc.Out = &junitFailure{Message: classInfo[class].Label + ": " + Safe(ch.Title), Type: class, Text: text}, ""
					s.Failures++
				}
				s.Cases = append(s.Cases, tc)
			}
			s.Tests = len(s.Cases)
			out.Suites = append(out.Suites, s)
		}
	} else {
		limit := t.levelRank()
		for _, a := range r.Run.Actions {
			s := junitSuite{Name: "echo." + a.ID, Timestamp: stamp}
			tc := junitCase{Name: Safe(a.Label) + " sends its messages", Classname: "echo.action", Out: Tally(&model.Run{Actions: []model.Action{a}})}
			if a.Error != "" && t != FailNone {
				tc.Failure, tc.Out = &junitFailure{Message: Safe(a.Error), Type: "action"}, ""
				s.Failures++
			}
			s.Cases = append(s.Cases, tc)
			for _, i := range a.Issues {
				c := junitCase{Name: Safe(i.Title), Classname: strings.ReplaceAll(i.Check, "/", "."), Out: safeLines(i.Detail)}
				if i.Level.Rank() <= limit {
					c.Failure, c.Out = &junitFailure{Message: Safe(i.Title), Type: string(i.Level), Text: safeLines(i.Detail + "\n" + i.Fix)}, ""
					s.Failures++
				}
				s.Cases = append(s.Cases, c)
			}
			s.Tests = len(s.Cases)
			out.Suites = append(out.Suites, s)
		}
	}
	for _, s := range out.Suites {
		out.Tests += s.Tests
		out.Failures += s.Failures
	}
	if _, err := io.WriteString(w, xml.Header); err != nil {
		return err
	}
	enc := xml.NewEncoder(w)
	enc.Indent("", "  ")
	if err := enc.Encode(out); err != nil {
		return err
	}
	_, err := io.WriteString(w, "\n")
	return err
}

// JSONValue writes any value as indented JSON (messages for `rhythm echo ls --json`).
func JSONValue(w io.Writer, v any) error {
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	enc.SetEscapeHTML(false)
	return enc.Encode(v)
}
