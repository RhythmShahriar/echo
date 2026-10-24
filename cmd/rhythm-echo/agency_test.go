// Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
// SPDX-License-Identifier: MIT

package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/RhythmShahriar/echo/hub"
	"github.com/RhythmShahriar/echo/model"
	"github.com/RhythmShahriar/echo/store"
)

// hubWindow stores one update window of site in the hub store at root, as
// rhythm echo schedule keeps it.
func hubWindow(t *testing.T, root, site string, at time.Time, broke bool) {
	t.Helper()
	dir := filepath.Join(hub.SiteStore(root, site).Root, "runs", store.Stamp(at))
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	run := &model.Run{Schema: model.Schema, Tool: "rhythm-echo", Started: at, Actions: []model.Action{{ID: "action:order", Label: "Order"}}}
	c := &model.Comparison{Schema: model.Schema, Before: model.RunRef{Name: "pre-update"}, Actions: 1}
	if broke {
		c.Changes = []model.Change{{Class: model.Broke, Check: "message/missing", Actions: []string{"Order"}, Title: "Email to admin@shop.test is missing"}}
	}
	if err := store.SaveRun(dir, run); err != nil {
		t.Fatal(err)
	}
	if err := store.SaveComparison(dir, c); err != nil {
		t.Fatal(err)
	}
	b, _ := json.Marshal(hub.Result{Site: site, Started: at, Finished: at.Add(time.Minute), Dir: dir, Snapshot: "pre-update"})
	if err := os.WriteFile(filepath.Join(dir, "schedule.json"), b, 0o600); err != nil {
		t.Fatal(err)
	}
}

func TestAgencyReport(t *testing.T) {
	t.Chdir(t.TempDir())
	root := t.TempDir()
	now := time.Now().UTC()
	hubWindow(t, root, "client-a", now.Add(-60*24*time.Hour), false) // before the period
	hubWindow(t, root, "client-a", now.Add(-7*24*time.Hour), true)
	hubWindow(t, root, "client-a", now.Add(-24*time.Hour), false)
	code, out, errOut := runCLI(t, "report", "--site", "client-a", "--store", root, "--brand-name", "Northwind Studio", "--brand-color", "#2563EB")
	if code != exitOK || !strings.Contains(out, "Agency report for client-a: client-a-report.html (2 update windows since") {
		t.Fatalf("report --site: %d\n%s\n%s", code, out, errOut)
	}
	b, err := os.ReadFile("client-a-report.html")
	if err != nil || !strings.Contains(string(b), "Northwind Studio") || !strings.Contains(string(b), "Something broke") {
		t.Errorf("the report: %v\n%.400s", err, b)
	}
	code, out, _ = runCLI(t, "report", "--site", "client-a", "--store", root, "--since", "90d", "-o", "q3.html")
	if code != exitOK || !strings.Contains(out, "3 update windows") {
		t.Errorf("--since 90d: %d %s", code, out)
	}

	t.Setenv("PATH", t.TempDir()) // no Chrome
	code, _, errOut = runCLI(t, "report", "--site", "client-a", "--store", root, "--pdf", "-o", "pdf.html")
	if _, err := os.Stat("pdf.html"); code != exitError || err != nil || !strings.Contains(errOut, "no Chrome or Chromium found on PATH") ||
		!strings.Contains(errOut, "the HTML report is pdf.html") {
		t.Errorf("--pdf without Chrome: %d %v %s", code, err, errOut)
	}
	// -o client.pdf names the PDF; the HTML goes beside it.
	code, _, errOut = runCLI(t, "report", "--site", "client-a", "--store", root, "-o", "client.pdf")
	if _, err := os.Stat("client.html"); code != exitError || err != nil || !strings.Contains(errOut, "the HTML report is client.html") {
		t.Errorf("-o client.pdf without Chrome: %d %v %s", code, err, errOut)
	}

	for _, c := range []struct {
		args []string
		want string
	}{
		{[]string{"report", "--site", "client-a"}, "name the hub's store with --store"},
		{[]string{"report", "--site", "client-a", "--store", root, "--brand-color", "blue"}, `--brand-color "blue": want #RGB or #RRGGBB`},
		{[]string{"report", "--site", "client-a", "--store", root, "--json"}, "the agency report is HTML"},
		{[]string{"report", "--pdf"}, "add --site <id>"},
		{[]string{"report", "--site", "../x", "--store", root}, `--site "../x": a hub site's id is letters, digits`},
		{[]string{"report", "--site", "a/b", "--store", root}, `--site "a/b"`},
		{[]string{"report", "-o", "x.pdf"}, "can't tell the format of x.pdf"},
	} {
		if code, _, errOut := runCLI(t, c.args...); code != exitUsage || !strings.Contains(errOut, c.want) {
			t.Errorf("%v: %d %s", c.args, code, errOut)
		}
	}
	if code, _, errOut := runCLI(t, "report", "--site", "client-b", "--store", root); code != exitError || !strings.Contains(errOut, `no update windows for site "client-b"`) {
		t.Errorf("a site with no windows: %d %s", code, errOut)
	}
}
