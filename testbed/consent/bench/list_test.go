// Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
// SPDX-License-Identifier: MIT

package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/RhythmShahriar/echo/testbed/consent/scenarios"
)

// generatedTables cuts the generated tables, markers included, out of
// a README.
func generatedTables(t *testing.T, readme string) string {
	t.Helper()
	start := strings.Index(readme, listBegin)
	end := strings.Index(readme, listEnd)
	if start < 0 || end < start {
		t.Fatalf("the README has no generated tables between %q and %q", listBegin, listEnd)
	}
	return readme[start : end+len(listEnd)+1]
}

// TestReadmeTableMatchesManifest fails when testbed/consent/README.md's
// scenario tables aren't what -list prints for scenarios.yaml.
func TestReadmeTableMatchesManifest(t *testing.T) {
	data, err := os.ReadFile(filepath.Join("..", "README.md"))
	if err != nil {
		t.Fatal(err)
	}
	readme := strings.ReplaceAll(string(data), "\r\n", "\n")
	var out, errOut bytes.Buffer
	if code := cli([]string{"-list", "-root", ".."}, &out, &errOut); code != 0 {
		t.Fatalf("-list: exit %d: %s", code, errOut.String())
	}
	if got := generatedTables(t, readme); got != out.String() {
		t.Errorf("README.md's scenario tables differ from scenarios.yaml; rerun go run -tags testbed ./testbed/consent/bench -list and paste them.\nREADME:\n%s\n-list:\n%s", got, out.String())
	}
}

// TestListCatchesAChange is the negative side: a manifest with one
// scenario renamed no longer matches the README.
func TestListCatchesAChange(t *testing.T) {
	data, err := os.ReadFile(filepath.Join("..", scenarios.File))
	if err != nil {
		t.Fatal(err)
	}
	changed := strings.Replace(string(data), "name: GA4 before consent", "name: GA4 | before consent", 1)
	if changed == string(data) {
		t.Fatal("c01's name isn't in scenarios.yaml as expected")
	}
	m, err := scenarios.Parse([]byte(changed))
	if err != nil {
		t.Fatal(err)
	}
	var out bytes.Buffer
	writeList(&out, m)
	readme, err := os.ReadFile(filepath.Join("..", "README.md"))
	if err != nil {
		t.Fatal(err)
	}
	if generatedTables(t, string(readme)) == out.String() {
		t.Error("a renamed scenario still matches the README")
	}
	// A pipe in a cell is escaped, so the row keeps its columns.
	if !strings.Contains(out.String(), `| c01 | GA4 \| before consent |`) {
		t.Errorf("the pipe isn't escaped:\n%s", out.String())
	}
}

// TestListShape checks the tables' rows: every scenario, variant, probe
// and fixture once, the any-of expectations joined by "or" and flag
// values with spaces quoted.
func TestListShape(t *testing.T) {
	m, err := scenarios.Load("..")
	if err != nil {
		t.Fatal(err)
	}
	var out bytes.Buffer
	writeList(&out, m)
	text := out.String()
	ids := m.Sites()
	for _, v := range m.Variants {
		ids = append(ids, v.ID)
	}
	for _, p := range m.Environment {
		ids = append(ids, p.ID)
	}
	for _, id := range ids {
		if n := strings.Count(text, "\n| "+id+" | "); n != 1 {
			t.Errorf("%s has %d rows", id, n)
		}
	}
	for _, want := range []string{
		"`run=reject status=click-failed`; or `check=consent/reject-broken level=error`",
		"`--click button,api`",
		"`-A \"Mozilla/5.0 (X11; Linux x86_64)",
		"; not `check=consent/before-choice tracker=ga4`",
		"| g02 | The correct setup in Consent Mode advanced | consent-mode | complianz |  | `consent/cookieless-ping` |",
	} {
		if !strings.Contains(text, want) {
			t.Errorf("-list lacks %q", want)
		}
	}
	var errOut bytes.Buffer
	if code := cli([]string{"-list", "-root", t.TempDir()}, &out, &errOut); code != 3 || !strings.Contains(errOut.String(), "bench: ") {
		t.Errorf("-list on a folder without a manifest: exit %d, %q", code, errOut.String())
	}
}
