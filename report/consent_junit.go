// Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
// SPDX-License-Identifier: MIT

package report

import (
	"encoding/xml"
	"fmt"
	"io"
	"strings"

	"github.com/RhythmShahriar/echo/consent"
	"github.com/RhythmShahriar/echo/model"
)

// The JUnit XML of a consent check: one suite per page, one case per
// run. Every suite carries the consent.json hash as a property, so
// report.xml states what it was written from (PRD 9.2).
type (
	consentJUnitSuites struct {
		XMLName  xml.Name            `xml:"testsuites"`
		Name     string              `xml:"name,attr"`
		Tests    int                 `xml:"tests,attr"`
		Failures int                 `xml:"failures,attr"`
		Skipped  int                 `xml:"skipped,attr"`
		Time     string              `xml:"time,attr"`
		Suites   []consentJUnitSuite `xml:"testsuite"`
	}
	consentJUnitSuite struct {
		Name       string             `xml:"name,attr"`
		Tests      int                `xml:"tests,attr"`
		Failures   int                `xml:"failures,attr"`
		Skipped    int                `xml:"skipped,attr"`
		Time       string             `xml:"time,attr"`
		Timestamp  string             `xml:"timestamp,attr,omitempty"`
		Properties *junitProperties   `xml:"properties,omitempty"`
		Cases      []consentJUnitCase `xml:"testcase"`
	}
	junitProperties struct {
		Property []junitProperty `xml:"property"`
	}
	junitProperty struct {
		Name  string `xml:"name,attr"`
		Value string `xml:"value,attr"`
	}
	consentJUnitCase struct {
		Name      string         `xml:"name,attr"`
		Classname string         `xml:"classname,attr"`
		Time      string         `xml:"time,attr"`
		Failures  []junitFailure `xml:"failure"`
		Skipped   *junitSkipped  `xml:"skipped,omitempty"`
		Out       string         `xml:"system-out,omitempty"`
	}
	junitSkipped struct {
		Message string `xml:"message,attr"`
		Text    string `xml:",chardata"`
	}
)

// consentJUnitClass is the classname of every consent test case.
const consentJUnitClass = "rhythm-echo.consent"

// WriteConsentJUnit writes a consent check as JUnit XML: one test suite
// per page named by its path (after its --sites name), one test case per
// run named `<page> · <Run>` with one <failure> per counted error-level
// issue of that page and run and <skipped message="inconclusive:
// <reason>"> for a run that couldn't choose. consentSHA256 is the
// consent.json.sha256 property of every suite.
func WriteConsentJUnit(w io.Writer, r *consent.Result, consentSHA256 string) error {
	if r == nil {
		r = &consent.Result{}
	}
	t := &consentText{w: &strings.Builder{}, r: r, tn: trackerNames(r)}
	// Every level's time is the sum of its runs' times: runs go in
	// parallel, so the check's wall time would be less than a suite's.
	out := consentJUnitSuites{Name: "rhythm-echo consent"}
	var all int64
	stamp := ""
	if !r.Started.IsZero() {
		stamp = r.Started.UTC().Format("2006-01-02T15:04:05Z")
	}
	for i := range r.Pages {
		pg := &r.Pages[i]
		name := Safe(consentPagePath(pg.URL))
		if pg.Site != "" {
			name = Safe(pg.Site) + " " + name
		}
		s := consentJUnitSuite{Name: name, Timestamp: stamp}
		if consentSHA256 != "" {
			s.Properties = &junitProperties{[]junitProperty{{Name: "consent.json.sha256", Value: Safe(consentSHA256)}}}
		}
		var total int64
		for j := range pg.Runs {
			run := &pg.Runs[j]
			total += run.DurationMS
			c := consentJUnitCase{Name: name + " · " + run.Kind.Label(), Classname: consentJUnitClass, Time: seconds(run.DurationMS)}
			for _, is := range t.runIssues(pg, run) {
				if is.Level != model.Error {
					continue
				}
				c.Failures = append(c.Failures, junitFailure{Message: Safe(is.Check) + ": " + Safe(is.Title), Type: string(is.Level),
					Text: safeLines(strings.TrimSpace(is.Detail + "\nfix: " + is.Fix))})
			}
			if run.Inconclusive() {
				c.Skipped = &junitSkipped{Message: "inconclusive: " + Safe(string(run.Reason)), Text: Safe(run.Detail)}
				s.Skipped++
			}
			if len(c.Failures) > 0 {
				s.Failures++
			}
			if len(c.Failures) == 0 && c.Skipped == nil {
				_, c.Out = t.runSummary(pg, run)
			}
			s.Cases = append(s.Cases, c)
		}
		s.Tests, s.Time = len(s.Cases), seconds(total)
		all += total
		out.Tests, out.Failures, out.Skipped = out.Tests+s.Tests, out.Failures+s.Failures, out.Skipped+s.Skipped
		out.Suites = append(out.Suites, s)
	}
	out.Time = seconds(all)
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

// seconds writes run clock milliseconds as JUnit's seconds: 14.100.
func seconds(ms int64) string {
	return fmt.Sprintf("%.3f", float64(max(ms, 0))/1000)
}
