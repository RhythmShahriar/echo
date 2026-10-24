// Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
// SPDX-License-Identifier: MIT

package main

import (
	"cmp"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"github.com/RhythmShahriar/echo/hub"
	"github.com/RhythmShahriar/echo/report"
	"github.com/RhythmShahriar/echo/store"
)

// agencyPeriod is how far back report --site looks without --since.
const agencyPeriod = 30 * 24 * time.Hour

var hexColor = regexp.MustCompile(`^#(?:[0-9a-fA-F]{3}|[0-9a-fA-F]{6})$`)

// agencyReport is report --site: one client-ready HTML page (and with
// --pdf a PDF) over a hub site's update windows in the period, from what
// rhythm echo schedule kept in the hub's store.
func (s *session) agencyReport() int {
	o := s.o
	switch {
	case !hub.ValidSiteID(o.site):
		return s.fail(usageErr("--site %q: a hub site's id is letters, digits, dots, dashes and underscores, as in sites.yaml", o.site))
	case o.storeDir == "":
		return s.fail(usageErr("--site reads the hub's results: name the hub's store with --store"))
	case len(s.args) > 0:
		return s.fail(usageErr("report --site takes no run folder or snapshot"))
	case s.format == "pdf":
		o.pdf = true // -o client.pdf: the PDF there, its HTML beside it
	case s.format != "" && s.format != "html":
		return s.fail(usageErr("the agency report is HTML (-o <file>.html), or a PDF with -o <file>.pdf"))
	case o.brandColor != "" && !hexColor.MatchString(o.brandColor):
		return s.fail(usageErr("--brand-color %q: want #RGB or #RRGGBB, e.g. #1F6FEB", o.brandColor))
	case o.failOn != "":
		return s.fail(usageErr("--fail-on has no effect on an agency report"))
	}
	brand := report.Brand{Name: o.brandName, Color: o.brandColor}
	if o.brandLogo != "" {
		b, err := os.ReadFile(o.brandLogo)
		if err != nil {
			return s.fail(usageErr("--brand-logo: %v", err))
		}
		brand.Logo = b
	}
	now := time.Now()
	since := now.Add(-cmp.Or(o.since, agencyPeriod))
	windows, err := hub.History(o.storeDir, o.site, since)
	if err != nil {
		return s.fail(err)
	}
	var runs []report.Result
	for _, w := range windows {
		if w.Dir == "" {
			continue // the window failed before it compared
		}
		run, err := store.LoadRun(w.Dir)
		if err != nil {
			fmt.Fprintf(s.stderr, "rhythm echo: left out the window of %s: %v\n", w.Started.Local().Format("2 Jan 2006"), err)
			continue
		}
		res := report.Result{Run: run, Dir: w.Dir}
		if c, err := store.LoadComparison(w.Dir); err == nil {
			res.Comparison = c
		}
		runs = append(runs, res)
	}
	if len(runs) == 0 {
		return s.fail(fmt.Errorf("no update windows for site %q in %s since %s; rhythm echo schedule runs them",
			o.site, o.storeDir, since.Local().Format("2 Jan 2006")))
	}
	out, pdf := cmp.Or(o.output, o.site+"-report.html"), ""
	if o.pdf {
		pdf = strings.TrimSuffix(out, filepath.Ext(out)) + ".pdf"
	}
	if s.format == "pdf" {
		pdf, out = out, strings.TrimSuffix(out, filepath.Ext(out))+".html"
	}
	in := report.AgencyInput{Site: o.site, Since: since, Runs: runs, Brand: brand}
	if err := writeFile(out, func(w io.Writer) error { return report.Agency(w, in) }); err != nil {
		return s.fail(err)
	}
	if !o.quiet {
		fmt.Fprintf(s.stdout, "Agency report for %s: %s (%s since %s)\n", o.site, out, plural(len(runs), "update window"),
			since.Local().Format("2 Jan 2006"))
	}
	open := out
	if pdf != "" {
		if err := report.PDF(out, pdf); err != nil {
			if errors.Is(err, report.ErrNoBrowser) {
				fmt.Fprintf(s.stderr, "rhythm echo: %v; the HTML report is %s\n", err, out)
				return exitError
			}
			return s.fail(err)
		}
		if !o.quiet {
			fmt.Fprintln(s.stdout, "PDF:", pdf)
		}
		open = pdf
	}
	if o.open {
		if err := openFile(open); err != nil {
			fmt.Fprintln(s.stderr, "rhythm echo: opening the report:", err)
		}
	}
	return exitOK
}
