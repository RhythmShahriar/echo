// Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
// SPDX-License-Identifier: MIT

package report

import (
	"bytes"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/RhythmShahriar/echo/model"
)

// agencyInput is two weekly compares of one site: the first clean, the
// second after a plugin update that stopped an email.
func agencyInput() AgencyInput {
	week1 := comparedRun()
	week1.Started = stamp.Add(-7 * 24 * time.Hour)
	week2 := comparedRun()
	clean := &model.Comparison{Schema: model.Schema, Version: "1.0.0", Actions: 2,
		Before: model.RunRef{Name: "before-update", Started: week1.Started.Add(-time.Hour)}}
	broke := &model.Comparison{Schema: model.Schema, Version: "1.0.0", Actions: 2,
		Before: model.RunRef{Name: "before-update", Started: stamp.Add(-time.Hour)},
		Changes: []model.Change{
			{Class: model.Broke, Check: "message/missing", Title: "Email to admin@shop.test is no longer sent", Actions: []string{"Contact form"},
				Before: "sent", After: "not sent"},
			{Class: model.Changed, Check: "message/subject", Title: "Email to the test submitter: subject changed", Actions: []string{"Contact form", "Quote form"},
				Before: "Thanks", After: "Thank you"},
		}}
	// A run before the period, which the report leaves out.
	old := comparedRun()
	old.Started = stamp.Add(-60 * 24 * time.Hour)
	return AgencyInput{Site: "https://staging.shop.test", Since: stamp.Add(-30 * 24 * time.Hour), Zone: time.UTC,
		Brand: Brand{Name: "Northwind Studio", Color: "#2563EB"},
		Runs:  []Result{{Run: week2, Comparison: broke}, {Run: old, Comparison: broke}, {Run: week1, Comparison: clean}}}
}

func TestAgencyGolden(t *testing.T) {
	var buf bytes.Buffer
	if err := Agency(&buf, agencyInput()); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join("testdata", "agency.html")
	if *update {
		if err := os.WriteFile(path, buf.Bytes(), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	want, err := os.ReadFile(path) //nolint:gosec // test fixture
	if err != nil {
		t.Fatal(err)
	}
	if buf.String() != string(want) {
		t.Errorf("agency report changed (go test ./report -run AgencyGolden -update to accept):\n%s", buf.String())
	}
}

func TestAgencyContent(t *testing.T) {
	var buf bytes.Buffer
	if err := Agency(&buf, agencyInput()); err != nil {
		t.Fatal(err)
	}
	out := buf.String()
	for _, want := range []string{"Something broke after updates", "24 Sep to 24 Oct 2026", "2 comparisons · 2 runs · 6 messages checked",
		"Northwind Studio", "--brand: #2563EB", "@page", "Everything is still sent the same way", "before-update (17 Oct)"} {
		if !strings.Contains(out, want) {
			t.Errorf("missing %q", want)
		}
	}
	for _, bad := range []string{"<script", "http://", "https://cdn", "Thanks {first_name}"} {
		if strings.Contains(out, bad) {
			t.Errorf("agency report has %q", bad)
		}
	}
	if strings.Count(out, "<tr class=") != 2 {
		t.Error("a run before Since is listed")
	}
	if strings.Index(out, "Sat 17 Oct") > strings.Index(out, "Sat 24 Oct") {
		t.Error("runs aren't oldest first")
	}
}

// TestAgencyUntil: a run after Until is left out, so the rows and totals
// agree with the period in the heading.
func TestAgencyUntil(t *testing.T) {
	in := agencyInput()
	in.Until = stamp.Add(-3 * 24 * time.Hour)
	var buf bytes.Buffer
	if err := Agency(&buf, in); err != nil {
		t.Fatal(err)
	}
	out := buf.String()
	if !strings.Contains(out, "24 Sep to 21 Oct 2026") {
		t.Error("the period doesn't end at Until")
	}
	if strings.Count(out, "<tr class=") != 1 || strings.Contains(out, "Sat 24 Oct") {
		t.Error("a run after Until is listed")
	}
	if !strings.Contains(out, "1 comparison") {
		t.Error("the totals count a run after Until")
	}
}

func TestAgencyVerdicts(t *testing.T) {
	cases := []struct {
		name string
		edit func(*AgencyInput)
		want string
	}{
		{"clean", func(in *AgencyInput) { in.Runs[0].Comparison.Changes = nil }, "Nothing broke after updates"},
		{"changed only", func(in *AgencyInput) { in.Runs[0].Comparison.Changes = in.Runs[0].Comparison.Changes[1:] }, "Nothing broke; some messages changed"},
		{"failed action", func(in *AgencyInput) {
			in.Runs[0].Comparison.Changes = nil
			in.Runs[0].Run.Actions[1].Error = "exit status 1"
		}, "Some checks could not run"},
		{"no compares", func(in *AgencyInput) { in.Runs = []Result{{Run: comparedRun()}} }, "No comparisons in this period"},
		{"no runs", func(in *AgencyInput) { in.Runs = nil }, "No runs in this period."},
	}
	for _, c := range cases {
		in := agencyInput()
		c.edit(&in)
		var buf bytes.Buffer
		if err := Agency(&buf, in); err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(buf.String(), c.want) {
			t.Errorf("%s: want %q", c.name, c.want)
		}
	}
}

func TestAgencyBrand(t *testing.T) {
	png := []byte("\x89PNG\r\n\x1a\n\x00\x00\x00\rIHDR")
	svg := []byte(`<?xml version="1.0"?><svg xmlns="http://www.w3.org/2000/svg"></svg>`)
	cases := []struct {
		brand Brand
		want  []string
		not   []string
	}{
		{Brand{Logo: png, Color: "#abc"}, []string{`src="data:image/png;base64,`, "--brand: #abc"}, nil},
		// html/template writes the + as &#43;, which the browser reads back.
		{Brand{Logo: svg}, []string{`src="data:image/svg&#43;xml;base64,`, "--brand: " + defaultBrandColor}, nil},
		// Not an image plus a color that would break out of the CSS.
		{Brand{Logo: []byte("<html><script>alert(1)</script>"), Color: "red;}body{display:none"},
			[]string{"--brand: " + defaultBrandColor}, []string{"<img", "display:none"}},
	}
	for i, c := range cases {
		in := agencyInput()
		in.Brand = c.brand
		var buf bytes.Buffer
		if err := Agency(&buf, in); err != nil {
			t.Fatal(err)
		}
		for _, w := range c.want {
			if !strings.Contains(buf.String(), w) {
				t.Errorf("case %d: missing %q", i, w)
			}
		}
		for _, n := range c.not {
			if strings.Contains(buf.String(), n) {
				t.Errorf("case %d: has %q", i, n)
			}
		}
	}
}

func TestPDFWithoutBrowser(t *testing.T) {
	lookPath = func(string) (string, error) { return "", exec.ErrNotFound }
	installed := installedBrowsers
	installedBrowsers = func() []string { return []string{filepath.Join(t.TempDir(), "Google Chrome")} }
	defer func() { lookPath, installedBrowsers = exec.LookPath, installed }()
	if err := PDF("report.html", "report.pdf"); !errors.Is(err, ErrNoBrowser) {
		t.Fatalf("PDF = %v, want ErrNoBrowser", err)
	}
}

// TestBrowserOutsidePATH: Chrome in its macOS bundle or its Windows
// program folder is found when nothing is on PATH.
func TestBrowserOutsidePATH(t *testing.T) {
	lookPath = func(string) (string, error) { return "", exec.ErrNotFound }
	installed := installedBrowsers
	defer func() { lookPath, installedBrowsers = exec.LookPath, installed }()
	app := filepath.Join(t.TempDir(), "Google Chrome")
	if err := os.WriteFile(app, nil, 0o700); err != nil { //nolint:gosec // a stand-in program
		t.Fatal(err)
	}
	installedBrowsers = func() []string { return []string{filepath.Join(t.TempDir(), "missing"), app} }
	if got := findBrowser(); got != app {
		t.Errorf("findBrowser = %q, want %q", got, app)
	}
}

func TestPDF(t *testing.T) {
	found := false
	for _, b := range browsers {
		if _, err := exec.LookPath(b); err == nil {
			found = true
			break
		}
	}
	if !found || testing.Short() {
		t.Skip("no Chrome or Chromium on PATH")
	}
	dir := t.TempDir()
	src, dst := filepath.Join(dir, "report.html"), filepath.Join(dir, "report.pdf")
	var buf bytes.Buffer
	if err := Agency(&buf, agencyInput()); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(src, buf.Bytes(), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := PDF(src, dst); err != nil {
		t.Fatal(err)
	}
	got, err := os.ReadFile(dst) //nolint:gosec // the test's own temp file
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.HasPrefix(got, []byte("%PDF")) {
		t.Errorf("not a PDF: %q", got[:min(len(got), 16)])
	}
}
