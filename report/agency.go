// Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
// SPDX-License-Identifier: MIT

package report

import (
	"bytes"
	"cmp"
	"context"
	_ "embed"
	"encoding/base64"
	"errors"
	"fmt"
	"html/template"
	"io"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"slices"
	"strings"
	"time"

	"github.com/RhythmShahriar/echo/model"
)

//go:embed agency.html.tmpl
var agencySource string

var agencyTemplate = template.Must(template.New("agency").Parse(agencySource))

// agencyCSP allows no script, nothing remote and only data: images (the
// logo), so the page looks the same opened from a mail attachment, a
// shared drive or Chrome printing it to PDF offline.
const agencyCSP = "default-src 'none'; style-src 'unsafe-inline'; img-src data:; base-uri 'none'; form-action 'none'"

// defaultBrandColor is Echo's own accent, used when Brand.Color is empty
// or isn't a hex color.
const defaultBrandColor = "#E8603A"

var hexColor = regexp.MustCompile(`^#(?:[0-9a-fA-F]{3}|[0-9a-fA-F]{6})$`)

// Brand is who the agency report is from: the agency's name, its logo
// (the image file's bytes: PNG, JPEG, GIF, WebP or SVG) and an accent
// color as "#RGB" or "#RRGGBB". Every field is optional.
type Brand struct {
	Name  string
	Logo  []byte
	Color string
}

// AgencyInput is what an agency report covers: one site's runs over a
// period. Runs are usually comparisons (a run checked against the
// snapshot taken before an update); a run without one is listed as
// checked only. Runs started before Since or after Until are left out;
// the rest are shown oldest first.
type AgencyInput struct {
	Site  string    // the site's name or URL, for the title
	Since time.Time // start of the period
	Until time.Time // end of the period; zero means the last run's start
	Runs  []Result  // in any order
	Brand Brand
	Zone  *time.Location // dates are shown in this zone; nil means time.Local
}

type agencyData struct {
	CSP, Title, Version, Site, Period, Brand string
	Logo                                     template.URL
	Color                                    template.CSS
	Verdict, Tone, Sub                       string
	Totals                                   []htmlChip
	Rows                                     []agencyRow
	Details                                  []agencyRow
}

type agencyRow struct {
	Date, Against, Result, Tone, Messages string
	Changes                               []htmlItem
}

// Agency writes a client-ready HTML page for a site: did anything break
// after the updates in the period, with one row per run and the changes
// each comparison found. It is styled for print (A4, @page margins) and
// has no script, so it can be sent as is or turned into a PDF with PDF.
// Bodies and payloads are left out: the client gets the verdict and what
// changed, not the captured mail.
func Agency(w io.Writer, in AgencyInput) error {
	zone := cmp.Or(in.Zone, time.Local)
	runs := make([]Result, 0, len(in.Runs))
	for _, r := range in.Runs {
		if r.Run == nil || r.Run.Started.Before(in.Since) {
			continue
		}
		// Until labels the period, so a run after it would contradict the
		// page's own heading.
		if !in.Until.IsZero() && r.Run.Started.After(in.Until) {
			continue
		}
		runs = append(runs, r)
	}
	slices.SortStableFunc(runs, func(a, b Result) int { return a.Run.Started.Compare(b.Run.Started) })

	color := defaultBrandColor
	if hexColor.MatchString(in.Brand.Color) {
		color = in.Brand.Color
	}
	d := agencyData{CSP: agencyCSP, Site: in.Site, Brand: strings.TrimSpace(in.Brand.Name), Logo: logoURL(in.Brand.Logo),
		Color: template.CSS(color)} //nolint:gosec // a hex color, checked just above
	until := in.Until
	if until.IsZero() && len(runs) > 0 {
		until = runs[len(runs)-1].Run.Started
	}
	d.Period = period(in.Since, until, zone)

	totals := map[string]int{}
	compares, failed, messages := 0, 0, 0
	for _, r := range runs {
		d.Version = cmp.Or(r.Run.Version, d.Version)
		row := agencyRow{Date: r.Run.Started.In(zone).Format("Mon 2 Jan 2006, 15:04"), Messages: Tally(r.Run), Tone: "ok"}
		for _, a := range r.Run.Actions {
			messages += len(a.Messages)
		}
		n, _ := failedActions(r.Run)
		if n > 0 {
			failed++
		}
		switch {
		case r.Compared():
			compares++
			c := r.Comparison
			row.Against, row.Result, row.Tone = againstLabel(c.Before, zone), verdict(r), toneOf(c)
			if n > 0 {
				row.Tone = model.Broke
			}
			for _, ch := range c.Changes {
				totals[ch.Class]++
				it := htmlItem{Key: ch.Class, Label: classInfo[ch.Class].Label, Title: ch.Title, Where: countLabel(ch.Actions),
					BeforeAfter: beforeAfter(ch)}
				row.Changes = append(row.Changes, it)
			}
		default:
			counts := Counts(Issues(r.Run))
			row.Against, row.Result = "not compared", "Checked: "+runVerdict(r.Run, counts)
			switch {
			case n > 0 || counts[model.Error] > 0:
				row.Tone = model.Broke
			case counts[model.Warning] > 0:
				row.Tone = model.New
			}
		}
		d.Rows = append(d.Rows, row)
		if len(row.Changes) > 0 {
			d.Details = append(d.Details, row)
		}
	}
	for _, class := range model.Classes {
		info := classInfo[class]
		d.Totals = append(d.Totals, htmlChip{class, info.Label, info.Glyph, totals[class]})
	}

	d.Verdict, d.Tone = "Nothing broke after updates", "ok"
	switch {
	case totals[model.Broke] > 0:
		d.Verdict, d.Tone = "Something broke after updates", model.Broke
	case failed > 0:
		d.Verdict, d.Tone = "Some checks could not run", model.Broke
	case compares == 0:
		d.Verdict, d.Tone = "No comparisons in this period", "none"
	case totals[model.New] > 0 || totals[model.Changed] > 0:
		d.Verdict, d.Tone = "Nothing broke; some messages changed", model.Changed
	}
	d.Sub = fmt.Sprintf("%s · %s · %s", plural(compares, "comparison", "comparisons"), plural(len(runs), "run", "runs"),
		plural(messages, "message checked", "messages checked"))
	if failed > 0 {
		d.Sub += " · " + plural(failed, "run with failed actions", "runs with failed actions")
	}
	d.Title = cmp.Or(in.Site, "Site") + " · " + d.Verdict
	return agencyTemplate.Execute(w, d)
}

// toneOf is a comparison's most urgent class, or "ok".
func toneOf(c *model.Comparison) string {
	for _, class := range []string{model.Broke, model.New, model.Changed} {
		if c.Count(class) > 0 {
			return class
		}
	}
	return "ok"
}

// againstLabel is the snapshot a run was compared with: its name and the
// day it was taken, "before-update (24 Oct)".
func againstLabel(ref model.RunRef, zone *time.Location) string {
	name := cmp.Or(ref.Name, filepath.Base(ref.Dir), "snapshot")
	if ref.Started.IsZero() {
		return name
	}
	return name + " (" + ref.Started.In(zone).Format("2 Jan") + ")"
}

// period is "1 Oct to 24 Oct 2026", or one side of it when the other is
// unknown.
func period(since, until time.Time, zone *time.Location) string {
	const day = "2 Jan 2006"
	switch {
	case since.IsZero() && until.IsZero():
		return ""
	case since.IsZero():
		return "up to " + until.In(zone).Format(day)
	case until.IsZero():
		return "since " + since.In(zone).Format(day)
	}
	from := since.In(zone).Format("2 Jan")
	if since.In(zone).Year() != until.In(zone).Year() {
		from = since.In(zone).Format(day)
	}
	return from + " to " + until.In(zone).Format(day)
}

// logoURL embeds a logo as a data: URL, or returns "" for anything that
// isn't an image a browser shows in <img>. SVG is safe there: an image
// never runs its scripts or loads what it links to.
func logoURL(b []byte) template.URL {
	if len(b) == 0 {
		return ""
	}
	typ := http.DetectContentType(b)
	switch {
	case typ == "image/png", typ == "image/jpeg", typ == "image/gif", typ == "image/webp":
	case strings.HasPrefix(typ, "text/") && bytes.Contains(bytes.ToLower(b[:min(len(b), 1024)]), []byte("<svg")):
		typ = "image/svg+xml"
	default:
		return ""
	}
	return template.URL("data:" + typ + ";base64," + base64.StdEncoding.EncodeToString(b)) //nolint:gosec // a sniffed image, base64 encoded
}

// ErrNoBrowser is returned by PDF when it finds no Chrome or Chromium.
var ErrNoBrowser = errors.New("no Chrome or Chromium found on PATH: install one, or open the HTML report in a browser and print it to PDF")

// browsers are the names Chrome and Chromium go by on PATH, most common
// first.
var browsers = []string{"google-chrome", "google-chrome-stable", "chromium", "chromium-browser", "chrome", "msedge"}

// lookPath finds a program; tests replace it.
var lookPath = exec.LookPath

// installedBrowsers are where Chrome, Chromium and Edge install
// themselves outside PATH: the macOS application bundles and the Windows
// program folders. %VAR% is the environment variable's value. Tests
// replace it.
var installedBrowsers = func() []string {
	switch runtime.GOOS {
	case "darwin":
		return []string{"/Applications/Google Chrome.app/Contents/MacOS/Google Chrome",
			"/Applications/Chromium.app/Contents/MacOS/Chromium",
			"/Applications/Microsoft Edge.app/Contents/MacOS/Microsoft Edge"}
	case "windows":
		var out []string
		for _, root := range []string{os.Getenv("ProgramFiles"), os.Getenv("ProgramFiles(x86)"), os.Getenv("LocalAppData")} {
			if root != "" {
				out = append(out, filepath.Join(root, "Google", "Chrome", "Application", "chrome.exe"),
					filepath.Join(root, "Microsoft", "Edge", "Application", "msedge.exe"))
			}
		}
		return out
	}
	return nil
}

// FindBrowser is the Chrome, Chromium or Edge that PDF prints with, ""
// when there is none.
func FindBrowser() string { return findBrowser() }

// findBrowser is the Chrome, Chromium or Edge PDF prints with: one on
// PATH, else one installed where installedBrowsers looks. "" when none.
func findBrowser() string {
	for _, name := range browsers {
		if p, err := lookPath(name); err == nil {
			return p
		}
	}
	for _, p := range installedBrowsers() {
		if fi, err := os.Stat(p); err == nil && !fi.IsDir() {
			return p
		}
	}
	return ""
}

// pdfTimeout bounds one print: a page with no script and nothing remote
// prints in a second or two, so a minute means Chrome hung.
const pdfTimeout = time.Minute

// PDF prints an HTML file to a PDF with headless Chrome or Chromium (on
// PATH, or where macOS and Windows install it), so Echo needs no PDF
// library. It returns ErrNoBrowser when there
// is none. Chrome runs with a throwaway profile and background networking
// off. The page itself loads nothing remote.
func PDF(htmlPath, pdfPath string) error {
	bin := findBrowser()
	if bin == "" {
		return ErrNoBrowser
	}
	src, err := filepath.Abs(htmlPath)
	if err != nil {
		return err
	}
	if _, err := os.Stat(src); err != nil {
		return err
	}
	out, err := filepath.Abs(pdfPath)
	if err != nil {
		return err
	}
	profile, err := os.MkdirTemp("", "echo-pdf-")
	if err != nil {
		return err
	}
	defer func() { _ = os.RemoveAll(profile) }()
	// Chrome writes the PDF with the umask's permissions. It lists a
	// client's recipients, so it prints into a private folder beside
	// pdfPath and is moved into place as 0600, like every report (X7).
	private, err := os.MkdirTemp(filepath.Dir(out), ".echo-pdf-")
	if err != nil {
		return err
	}
	defer func() { _ = os.RemoveAll(private) }()
	printed := filepath.Join(private, "report.pdf")

	args := []string{"--headless", "--disable-gpu", "--no-first-run", "--no-default-browser-check", "--disable-extensions",
		"--disable-background-networking", "--disable-component-update", "--disable-sync", "--user-data-dir=" + profile,
		// The first is today's flag, the second the older spelling;
		// Chrome ignores the one it doesn't know.
		"--no-pdf-header-footer", "--print-to-pdf-no-header",
		"--print-to-pdf=" + printed, (&url.URL{Scheme: "file", Path: filepath.ToSlash(src)}).String()}
	if os.Geteuid() == 0 {
		// Chrome refuses to start as root (a CI container) with its sandbox
		// on. The page has no script and loads nothing, so there is nothing
		// for the sandbox to contain.
		args = append([]string{"--no-sandbox"}, args...)
	}
	ctx, cancel := context.WithTimeout(context.Background(), pdfTimeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, bin, args...) //nolint:gosec // bin is a browser found on PATH
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		if why := lastLine(stderr.String()); why != "" {
			err = fmt.Errorf("%w: %s", err, why)
		}
		return fmt.Errorf("%s could not print %s: %w", filepath.Base(bin), htmlPath, err)
	}
	head := make([]byte, 5)
	f, err := os.Open(printed) //nolint:gosec // the path we asked Chrome to write
	if err != nil {
		return fmt.Errorf("%s did not write %s: %w", filepath.Base(bin), pdfPath, err)
	}
	_, err = io.ReadFull(f, head)
	_ = f.Close()
	if err != nil || string(head) != "%PDF-" {
		return fmt.Errorf("%s wrote %s but it isn't a PDF", filepath.Base(bin), pdfPath)
	}
	if err := os.Chmod(printed, 0o600); err != nil {
		return err
	}
	return os.Rename(printed, out)
}

// lastLine is the last non-empty line of s, the one that usually says why
// a program failed.
func lastLine(s string) string {
	lines := strings.Split(strings.TrimSpace(s), "\n")
	return strings.TrimSpace(lines[len(lines)-1])
}
