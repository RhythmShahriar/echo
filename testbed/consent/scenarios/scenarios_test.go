// Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
// SPDX-License-Identifier: MIT

package scenarios

import (
	"fmt"
	"net/http/httptest"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/RhythmShahriar/echo/consent"
	"github.com/RhythmShahriar/echo/testbed/consent/server"
)

// root is the testbed/consent folder.
var root = filepath.Join("..")

func load(t *testing.T) *Manifest {
	t.Helper()
	m, err := Load(root)
	if err != nil {
		t.Fatal(err)
	}
	return m
}

func TestScenarioCount(t *testing.T) {
	m := load(t)
	var bad, good []string
	for _, s := range m.Scenarios {
		if s.Good() {
			good = append(good, s.ID)
		} else {
			bad = append(bad, s.ID)
		}
	}
	want := func(prefix string, n int) []string {
		var ids []string
		for i := 1; i <= n; i++ {
			ids = append(ids, fmt.Sprintf("%s%02d", prefix, i))
		}
		return ids
	}
	if !slices.Equal(bad, want("c", 25)) {
		t.Errorf("bad scenarios = %v, want c01 to c25 in order", bad)
	}
	if !slices.Equal(good, want("g", 15)) {
		t.Errorf("good scenarios = %v, want g01 to g15 in order", good)
	}
}

var (
	levels   = []string{"error", "warning", "notice"}
	classes  = []string{consent.ClassBroke, consent.ClassNew, consent.ClassChanged, consent.ClassFixed}
	runKinds = []string{string(consent.RunNone), string(consent.RunAccept), string(consent.RunReject), string(consent.RunGPC), string(consent.RunWithdraw)}
	statuses = []string{string(consent.StatusOK), string(consent.StatusNoBanner), string(consent.StatusNoButton), string(consent.StatusClickFailed),
		string(consent.StatusLoadFailed), string(consent.StatusTimeout), string(consent.StatusNavigatedAway), string(consent.StatusLimit), string(consent.StatusCanceled)}
	reasons = []string{string(consent.ReasonNoBanner), string(consent.ReasonCMPNotFound), string(consent.ReasonNoButton), string(consent.ReasonClickMissed),
		string(consent.ReasonStateUnchanged), string(consent.ReasonNavigation), string(consent.ReasonTimeout), string(consent.ReasonLimit), string(consent.ReasonCanceled)}
)

func checkIDs() []string {
	var ids []string
	for _, c := range consent.Checks {
		ids = append(ids, c.ID)
	}
	return ids
}

// TestEveryCheckCovered mirrors qa.md 7: every consent check but
// ignore-expired has a bad scenario that expects it and at least one
// good scenario where it is forbidden (good states forbid every error
// and warning plus every notice they don't allow).
func TestEveryCheckCovered(t *testing.T) {
	m := load(t)
	ids := checkIDs()
	expected := map[string]bool{}
	for _, s := range m.Scenarios {
		for _, e := range append(append([]Match(nil), s.Expect...), s.Forbid...) {
			validMatch(t, s.ID, e, ids)
		}
		for _, e := range s.Expect {
			expected[e.Check] = true
		}
		for _, a := range s.Allow {
			if !slices.Contains(ids, a) {
				t.Errorf("%s: allow %q isn't a consent check", s.ID, a)
			}
		}
	}
	for _, id := range ids {
		if id == consent.CheckIgnoreExpired {
			continue
		}
		if !expected[id] {
			t.Errorf("no scenario expects %s", id)
		}
		forbidden := false
		for _, s := range m.Scenarios {
			if s.Good() && !slices.Contains(s.Allow, id) {
				forbidden = true
			}
		}
		if !forbidden {
			t.Errorf("every good scenario allows %s, so nothing proves it stays quiet", id)
		}
	}
}

func validMatch(t *testing.T, id string, e Match, checks []string) {
	t.Helper()
	ok := func(field, v string, set []string) {
		if v != "" && !slices.Contains(set, v) {
			t.Errorf("%s: %s %q: want one of %s", id, field, v, strings.Join(set, ", "))
		}
	}
	ok("check", e.Check, checks)
	ok("level", e.Level, levels)
	ok("class", e.Class, classes)
	ok("run", e.Run, runKinds)
	ok("status", e.Status, statuses)
	ok("reason", e.Reason, reasons)
	if e.Check == "" && e.Class == "" && !e.IsRun() {
		t.Errorf("%s: %v names no check, class, status or reason", id, e)
	}
}

func TestEverySiteHasAFolder(t *testing.T) {
	m := load(t)
	listed := m.Sites()
	for _, id := range listed {
		if _, err := os.Stat(filepath.Join(root, "sites", id, "site.html")); err != nil {
			t.Errorf("%s has no sites/%s/site.html", id, id)
		}
	}
	entries, err := os.ReadDir(filepath.Join(root, "sites"))
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range entries {
		if e.IsDir() && !strings.HasPrefix(e.Name(), "_") && !slices.Contains(listed, e.Name()) {
			t.Errorf("sites/%s isn't in scenarios.yaml", e.Name())
		}
	}
}

// TestSitesRender renders both pages of every site in both states
// through the testbed server: no template error, the links a runner
// may and may not follow and a broken state that differs from the
// good one.
func TestSitesRender(t *testing.T) {
	m := load(t)
	dir := t.TempDir()
	if err := os.CopyFS(filepath.Join(dir, "sites"), os.DirFS(filepath.Join(root, "sites"))); err != nil {
		t.Fatal(err)
	}
	srv, err := server.New(server.Options{Root: dir, Admin: "127.0.0.1:23401"})
	if err != nil {
		t.Fatal(err)
	}
	h := srv.Handler()
	render := func(id, path string) string {
		r := httptest.NewRequest("GET", path, nil)
		r.Host = id + server.SiteSuffix
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		if w.Code != 200 {
			t.Errorf("%s%s: %d %s", id, path, w.Code, w.Body.String())
		}
		return w.Body.String()
	}
	for _, id := range m.Sites() {
		if strings.HasPrefix(id, "redirect-") {
			continue // the server answers / with redirects
		}
		var pages [2]string
		for i, broken := range []bool{false, true} {
			if err := srv.SetBroken(id, broken); err != nil {
				t.Fatal(err)
			}
			for _, p := range []string{"/", "/page-2/"} {
				body := render(id, p)
				for _, bad := range []string{"<no value>", "{{template", "{{if"} {
					if strings.Contains(body, bad) {
						t.Errorf("%s %s (broken %v) has %q", id, p, broken, bad)
					}
				}
				for _, link := range []string{`href="/page-2/"`, `href="/cart/"`, `href="/logout/"`} {
					if !strings.Contains(body, link) {
						t.Errorf("%s %s lacks %s", id, p, link)
					}
				}
				if p == "/" {
					pages[i] = body
				}
			}
		}
		s, isScenario := m.Find(id)
		// c18's bug is on the server, not in the page.
		if isScenario && !s.Good() && id != "c18" && pages[0] == pages[1] {
			t.Errorf("%s: the broken page is the same as the good one", id)
		}
		if err := srv.SetBroken(id, false); err != nil {
			t.Fatal(err)
		}
	}
}

func TestLoadRefusesBadManifests(t *testing.T) {
	const g01 = "scenarios:\n  - {id: g01, name: x, area: cmp, cmp: x, needs: go, toggle: x, allow: []}\n"
	cases := map[string]string{
		"unknown key":        "scenarios:\n  - {id: c01, name: x, area: cmp, cmp: x, needs: go, toggle: x, expect: [{check: a}], expct: []}\n",
		"bad area":           "scenarios:\n  - {id: c01, name: x, area: nope, cmp: x, needs: go, toggle: x, expect: [{check: a}]}\n",
		"bad needs":          "scenarios:\n  - {id: c01, name: x, area: cmp, cmp: x, needs: k8s, toggle: x, expect: [{check: a}]}\n",
		"no expect":          "scenarios:\n  - {id: c01, name: x, area: cmp, cmp: x, needs: go, toggle: x}\n",
		"good without allow": "scenarios:\n  - {id: g01, name: x, area: cmp, cmp: x, needs: go, toggle: x}\n",
		"duplicate":          "scenarios:\n  - {id: g01, name: x, area: cmp, cmp: x, needs: go, toggle: x, allow: []}\n  - {id: g01, name: x, area: cmp, cmp: x, needs: go, toggle: x, allow: []}\n",
		"variant of nothing": g01 + "variants:\n  - {id: c09-wp, of: c09, cmp: x, site: c09-wp, needs: docker}\n",
		"variant in go":      g01 + "variants:\n  - {id: g01-wp, of: g01, cmp: x, site: g01-wp, needs: go}\n",
		"variant no site":    g01 + "variants:\n  - {id: g01-wp, of: g01, cmp: x, needs: docker}\n",
		"variant id taken":   g01 + "variants:\n  - {id: g01, of: g01, cmp: x, site: g01-wp, needs: docker}\n",
		"probe no broken":    g01 + "environment:\n  - {id: e01, name: x, probe: p, needs: local, toggle: x, good: [{step: s}]}\n",
		"probe bad needs":    g01 + "environment:\n  - {id: e01, name: x, probe: p, needs: cloud, toggle: x, good: [{step: s}], broken: [{step: s}]}\n",
		"probe no step":      g01 + "environment:\n  - {id: e01, name: x, probe: p, needs: local, toggle: x, good: [{exit: 0}], broken: [{step: s}]}\n",
		"probe no probe":     g01 + "environment:\n  - {id: e01, name: x, needs: local, toggle: x, good: [{step: s}], broken: [{step: s}]}\n",
		"probe unknown key":  g01 + "environment:\n  - {id: e01, name: x, probe: p, needs: local, toggle: x, good: [{step: s, exitt: 0}], broken: [{step: s}]}\n",
	}
	for name, body := range cases {
		dir := t.TempDir()
		if err := os.WriteFile(filepath.Join(dir, File), []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
		if _, err := Load(dir); err == nil {
			t.Errorf("%s: loaded", name)
		}
	}
	dir := t.TempDir()
	good := g01 + "variants:\n  - {id: g01-wp, of: g01, cmp: x, site: g01-wp, needs: docker}\n" +
		"environment:\n  - {id: e01, name: x, probe: p, needs: local, toggle: x, good: [{step: s, exit: 0}], broken: [{step: s, says: [y]}]}\n"
	if err := os.WriteFile(filepath.Join(dir, File), []byte(good), 0o600); err != nil {
		t.Fatal(err)
	}
	m, err := Load(dir)
	if err != nil {
		t.Fatalf("a valid manifest: %v", err)
	}
	if p, ok := m.FindProbe("e01"); !ok || p.Broken[0].String() != `s: says "y"` || p.Good[0].String() != "s: exit 0" {
		t.Errorf("e01 = %+v", p)
	}
	if _, ok := m.FindProbe("e99"); ok {
		t.Error("found e99")
	}
}

// TestFlagValuesStayWhole guards against YAML's flow lists splitting a
// flag's value at its comma: [--click, button,api] is three items, so
// rhythm-echo would get "api" as a stray argument.
func TestFlagValuesStayWhole(t *testing.T) {
	m := load(t)
	for _, id := range []string{"c11", "c20"} {
		s, _ := m.Find(id)
		if want := []string{"--click", "button,api"}; !slices.Equal(s.Args, want) {
			t.Errorf("%s args = %q, want %q", id, s.Args, want)
		}
	}
	for _, s := range m.Scenarios {
		for i, a := range s.Args {
			if strings.HasPrefix(a, "--") && i+2 < len(s.Args) && !strings.HasPrefix(s.Args[i+2], "-") {
				t.Errorf("%s: %q after %s %s is a stray argument (quote a value that has a comma)", s.ID, s.Args[i+2], a, s.Args[i+1])
			}
		}
	}
}
