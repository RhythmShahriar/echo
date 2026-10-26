// Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
// SPDX-License-Identifier: MIT

package report

import (
	"fmt"
	"io"
	"strings"
	"unicode/utf8"

	"github.com/RhythmShahriar/echo/consent"
)

// The footers of consent cmps and consent trackers (PRD 6.4).
const (
	cmpsHint     = "A banner none of these find: --accept-selector and --reject-selector."
	trackersHint = "Add your own under consent: trackers:."
)

// WriteConsentCatalog writes consent cmps (kind "cmps") or consent
// trackers (kind "trackers"): a table with a count, the data version
// and its hash under it, or with asJSON the consent-catalog envelope.
// The versions come from the linked CMP driver and tracker catalog.
func WriteConsentCatalog(w io.Writer, cmps []consent.CMPRuleInfo, trackers []consent.TrackerInfo, kind string, asJSON bool, o ConsentTextOptions) error {
	if kind != "cmps" && kind != "trackers" {
		return fmt.Errorf("report: consent catalog kind %q: want cmps or trackers", kind)
	}
	var data ConsentCatalog
	l := consent.Linked()
	if l.Classifier != nil {
		data.Catalog = l.Classifier.Info()
	}
	if l.CMP != nil {
		data.Rules = l.CMP.Info()
	}
	if kind == "cmps" {
		data.CMPs = cmps
	} else {
		data.Trackers = trackers
	}
	if asJSON {
		return encodeJSON(w, NewEnvelope(KindConsentCatalog, data))
	}
	var rows [][]string
	var foot string
	if kind == "cmps" {
		rows = append(rows, []string{"CMP", "DETECTED BY", "ACCEPT", "REJECT", "STATE FROM"})
		for _, c := range cmps {
			detected := Safe(c.DetectedBy)
			if c.ID == "generic" {
				// The generic rule's language count goes under its
				// description, as PRD 6.4 shows it.
				if i := strings.LastIndex(detected, " ("); i > 0 {
					detected = detected[:i] + "\n" + detected[i+1:]
				}
			}
			rows = append(rows, []string{Safe(c.ID), detected, Safe(c.Accept), Safe(c.Reject), Safe(c.StateFrom)})
		}
		foot = fmt.Sprintf("%s, rules version %s. %s", plural(len(cmps), "CMP", "CMPs"), versionWords(data.Rules), cmpsHint)
	} else {
		rows = append(rows, []string{"TRACKER", "NAME", "CATEGORY", "CONSENT", "MATCHES"})
		for _, t := range trackers {
			rows = append(rows, []string{Safe(t.ID), Safe(t.Name), Safe(string(t.Category)), Safe(string(t.Need)), Safe(t.Matches)})
		}
		foot = fmt.Sprintf("%s, catalogue version %s. %s", plural(len(trackers), "tracker", "trackers"), versionWords(data.Catalog), trackersHint) //nolint:misspell // PRD 6.4 wording
	}
	var b strings.Builder
	writeTable(&b, rows, o)
	b.WriteString(foot + "\n")
	_, err := io.WriteString(w, b.String())
	return err
}

// versionWords names a data version with the start of its hash: "1.2.0
// (sha256 9c1d...)"; "unknown" when it isn't linked.
func versionWords(d consent.DataInfo) string {
	v := Safe(d.Version)
	if v == "" {
		v = "unknown"
	}
	if d.SHA256 != "" {
		v += " (sha256 " + Safe(d.SHA256[:min(4, len(d.SHA256))]) + "...)"
	}
	return v
}

// writeTable writes rows in columns two spaces apart, the first row as
// the header (bold with color). A cell with a line break continues on
// the next line in its own column. Trailing spaces are trimmed.
func writeTable(b *strings.Builder, rows [][]string, o ConsentTextOptions) {
	var widths []int
	for _, row := range rows {
		for i, cell := range row {
			for line := range strings.SplitSeq(cell, "\n") {
				if i >= len(widths) {
					widths = append(widths, 0)
				}
				widths[i] = max(widths[i], utf8.RuneCountInString(line))
			}
		}
	}
	for n, row := range rows {
		lines := 1
		for _, cell := range row {
			lines = max(lines, strings.Count(cell, "\n")+1)
		}
		for l := range lines {
			var line strings.Builder
			for i, cell := range row {
				parts := strings.Split(cell, "\n")
				text := ""
				if l < len(parts) {
					text = parts[l]
				}
				if i < len(row)-1 {
					text = padRight(text, widths[i]+2)
				}
				line.WriteString(text)
			}
			s := strings.TrimRight(line.String(), " ")
			if n == 0 && o.Color && !o.Plain {
				s = bold.Render(s)
			}
			b.WriteString(s + "\n")
		}
	}
}
