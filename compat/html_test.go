// Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
// SPDX-License-Identifier: MIT

package compat

import (
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

func fixture(t *testing.T, name string) string {
	t.Helper()
	b, err := os.ReadFile(filepath.Join("testdata", name))
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

// got is a compact view of findings: "id@line" and "x" and the count.
func got(fs []Finding) []string {
	var out []string
	for _, f := range fs {
		out = append(out, fmt.Sprintf("%s@%02dx%02d", f.ID, f.Line, f.Count))
	}
	return out
}

func find(fs []Finding, id string) (Finding, bool) {
	i := slices.IndexFunc(fs, func(f Finding) bool { return f.ID == id })
	if i < 0 {
		return Finding{}, false
	}
	return fs[i], true
}

func TestTableLoads(t *testing.T) {
	if len(table) < 40 {
		t.Fatalf("the table has %d features; it should cover the 40 most common", len(table))
	}
	for _, f := range table {
		if strings.Contains(f.Note, "\u2014") || strings.Contains(f.Title, "\u2014") {
			t.Errorf("%s: no em dashes in user-facing text", f.ID)
		}
	}
}

func TestLoadRejectsBrokenTables(t *testing.T) {
	for name, doc := range map[string]string{
		"unknown client": "clients: {gmail: Gmail}\nfeatures:\n- {id: a, title: A, note: N, none: [hotmail], match: {at: media}}",
		"no client":      "clients: {gmail: Gmail}\nfeatures:\n- {id: a, title: A, note: N, match: {at: media}}",
		"two matches":    "clients: {gmail: Gmail}\nfeatures:\n- {id: a, title: A, note: N, none: [gmail], match: {at: media, pseudo: hover}}",
		"no match":       "clients: {gmail: Gmail}\nfeatures:\n- {id: a, title: A, note: N, none: [gmail], match: {}}",
		"bad regex":      "clients: {gmail: Gmail}\nfeatures:\n- {id: a, title: A, note: N, none: [gmail], match: {css: [x], value: '('}}",
		"duplicate":      "clients: {gmail: Gmail}\nfeatures:\n- {id: a, title: A, note: N, none: [gmail], match: {at: media}}\n- {id: a, title: B, note: N, none: [gmail], match: {at: import}}",
		"no note":        "clients: {gmail: Gmail}\nfeatures:\n- {id: a, title: A, none: [gmail], match: {at: media}}",
	} {
		if _, _, err := load([]byte(doc)); err == nil {
			t.Errorf("%s: loaded", name)
		}
	}
}

func TestFlexbox(t *testing.T) {
	fs := HTML(fixture(t, "flexbox.html"))
	want := []string{"html-style@04x01", "css-display-flex@05x03", "css-gap@05x01", "css-hover@06x01", "css-at-media@07x01", "css-border-radius@14x01"}
	if g := got(fs); !slices.Equal(g, want) {
		t.Fatalf("findings\n got %v\nwant %v", g, want)
	}
	flex, _ := find(fs, "css-display-flex")
	if !slices.Equal(flex.Unsupported, []string{"Outlook for Windows"}) || !slices.Equal(flex.Partial, []string{"Gmail"}) {
		t.Errorf("flex clients: %v / %v", flex.Unsupported, flex.Partial)
	}
	if s := flex.String(); s != "display: flex (line 5, 3 times): not in Outlook for Windows; partly in Gmail" {
		t.Errorf("String: %q", s)
	}
	if flex.Note == "" {
		t.Error("no note")
	}
}

func TestGrid(t *testing.T) {
	fs := HTML(fixture(t, "grid.html"))
	want := []string{
		"html-link-stylesheet@04x01", "html-style@05x01", "css-at-import@07x01", "css-display-grid@08x01",
		"css-variables@09x01", "html-img-svg@14x01", "html-img-data-uri@15x01", "html-video@17x01", "html-script@18x01",
	}
	if g := got(fs); !slices.Equal(g, want) {
		t.Fatalf("findings\n got %v\nwant %v", g, want)
	}
	// A comment and a quoted string mentioning flex are not uses.
	if _, ok := find(fs, "css-display-flex"); ok {
		t.Error("flex found in a comment or string")
	}
	grid, _ := find(fs, "css-display-grid")
	if s := grid.String(); s != "display: grid (line 8): not in Outlook for Windows, Gmail, Outlook.com; partly in Yahoo Mail" {
		t.Errorf("String: %q", s)
	}
}

func TestCleanTableLayout(t *testing.T) {
	if fs := HTML(fixture(t, "table.html")); len(fs) != 0 {
		t.Fatalf("a clean table layout gave %v", got(fs))
	}
	if fs := HTML(""); len(fs) != 0 {
		t.Fatalf("empty HTML gave %v", got(fs))
	}
}

func TestOutlookConditionals(t *testing.T) {
	fs := HTML(fixture(t, "mso.html"))
	want := []string{"css-display-flex@08x01", "css-max-width@08x02"}
	if g := got(fs); !slices.Equal(g, want) {
		t.Fatalf("findings\n got %v\nwant %v", g, want)
	}
	flex, _ := find(fs, "css-display-flex")
	if len(flex.Unsupported) != 0 || !slices.Equal(flex.Partial, []string{"Gmail"}) {
		t.Errorf("flex hidden from Outlook still blames it: %v / %v", flex.Unsupported, flex.Partial)
	}
	mw, _ := find(fs, "css-max-width")
	if !slices.Equal(mw.Unsupported, []string{"Outlook for Windows"}) {
		t.Errorf("max-width outside the conditional: %v", mw.Unsupported)
	}
	// A feature only Outlook lacks, used only where Outlook can't see it,
	// is no finding at all.
	if fs := HTML(`<!--[if !mso]><!--><div style="max-width:600px">x</div><!--<![endif]-->`); len(fs) != 0 {
		t.Errorf("got %v", got(fs))
	}
}

func TestInlineOnlyRules(t *testing.T) {
	// padding is fine on a td, not on a div; in a <style> block the
	// element isn't known, so it isn't held against it.
	fs := HTML(`<td style="padding:8px">a</td><div style="padding:8px">b</div><style>.x{padding:8px}</style>`)
	if g := got(fs); !slices.Equal(g, []string{"css-padding-block@01x01", "html-style@01x01"}) {
		t.Errorf("got %v", g)
	}
	if fs := HTML(`<td style="color:red !important">a</td>`); !slices.Equal(got(fs), []string{"css-important@01x01"}) {
		t.Errorf("!important: %v", got(fs))
	}
}

func TestStyleSize(t *testing.T) {
	big := "<style>" + strings.Repeat(".a{color:#000}\n", 1200) + "</style>"
	if _, ok := find(HTML(big), "html-style-size"); !ok {
		t.Error("a 17 KB style block isn't flagged")
	}
	if _, ok := find(HTML("<style>.a{color:#000}</style>"), "html-style-size"); ok {
		t.Error("a small style block is flagged")
	}
}

func TestScanCSS(t *testing.T) {
	items := scanCSS("<!-- @media screen{a:hover{color:red}} @font-face{font-family:x} @import 'x.css'; b{background:url(\"a;b.png\")} -->", 1)
	var kinds []string
	for _, it := range items {
		kinds = append(kinds, []string{"decl", "at", "sel"}[it.kind]+":"+it.name+"="+it.value)
	}
	want := []string{
		"at:media=screen", "sel:=a:hover", "decl:color=red", "at:font-face=", "decl:font-family=x",
		"at:import='x.css'", "sel:=b", `decl:background=url("a;b.png")`,
	}
	if !slices.Equal(kinds, want) {
		t.Errorf("\n got %q\nwant %q", kinds, want)
	}
	lines := scanCSS("a{\n/* one\ntwo */\ncolor: red;\n}", 3)
	if len(lines) != 2 || lines[1].line != 6 {
		t.Errorf("lines: %+v", lines)
	}
}
