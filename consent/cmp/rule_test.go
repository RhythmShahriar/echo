// Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
// SPDX-License-Identifier: MIT

package cmp

import (
	"crypto/sha256"
	"encoding/hex"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"testing"
	"testing/fstest"

	"gopkg.in/yaml.v3"

	"github.com/RhythmShahriar/echo/consent"
)

// acmeRule is a small valid rule the tests change one key of at a time.
const acmeRule = `id: acme
name: Acme CMP
priority: 20
detect:
  selectors: ["#acme-banner"]
  globals: [Acme]
  scripts: [cdn.acme.test/cmp.js]
banner:
  selector: "#acme-banner"
accept: ["#acme-accept"]
reject:
  first_layer: ["#acme-reject"]
  second_layer:
    open: ["#acme-settings"]
    toggles_off: "#acme-prefs input.toggle:not([disabled])"
    save: ["#acme-save"]
reopen:
  api: reopen
api:
  accept: Acme.acceptAll()
  reject: Acme.rejectAll()
  reopen: Acme.show()
  state: JSON.stringify(Acme.state)
verify:
  parser: cookiebot
  cookies: [acme_consent]
  accepted: {statistics: true}
  rejected: {statistics: false}
sources: [https://docs.acme.test/cmp]
fixtures: [testdata/acme/first-layer.html]
`

func mapFS(files map[string]string) fstest.MapFS {
	m := fstest.MapFS{}
	for name, body := range files {
		m["rules/"+name] = &fstest.MapFile{Data: []byte(body)}
	}
	return m
}

func TestLoadAll(t *testing.T) {
	d, err := load(rulesFS, "rules")
	if err != nil {
		t.Fatal(err)
	}
	files, _ := fs.Glob(rulesFS, "rules/*.yaml")
	if d.Info().Count != len(files) || len(d.Rules()) != len(files) {
		t.Fatalf("Count %d, Rules %d; want %d", d.Info().Count, len(d.Rules()), len(files))
	}
	if d.Info().Version != consent.DataVersion {
		t.Errorf("Version = %q", d.Info().Version)
	}
	for _, r := range d.rules {
		if len(r.Sources) == 0 || len(r.Fixtures) == 0 {
			t.Errorf("%s: no sources or fixtures", r.ID)
		}
	}
	if _, ok := consent.Linked().CMP.(*driver); !ok {
		t.Fatalf("init didn't register the driver: %T", consent.Linked().CMP)
	}
}

func TestLoadOrdersByPriorityGenericLast(t *testing.T) {
	gen := strings.NewReplacer("id: acme", "id: generic", "priority: 20", "priority: 0").Replace(acmeRule)
	first := strings.NewReplacer("id: acme", "id: zeta", "priority: 20", "priority: 5").Replace(acmeRule)
	d, err := load(mapFS(map[string]string{"acme.yaml": acmeRule, "generic.yaml": gen, "zeta.yaml": first}), "rules")
	if err != nil {
		t.Fatal(err)
	}
	var ids []string
	for _, r := range d.rules {
		ids = append(ids, r.ID)
	}
	if want := []string{"zeta", "acme", "generic"}; !slices.Equal(ids, want) {
		t.Fatalf("order %v; want %v", ids, want)
	}
}

func TestLoadSkipsOtherFiles(t *testing.T) {
	d, err := load(mapFS(map[string]string{"acme.yaml": acmeRule, "README.md": "# not a rule: [", "notes.txt": "x"}), "rules")
	if err != nil || d.Info().Count != 1 {
		t.Fatalf("Count %v, err %v", d, err)
	}
}

func TestLoadRefusesDuplicateID(t *testing.T) {
	// Two files can't both be acme: the id must match the file name.
	_, err := load(mapFS(map[string]string{"acme.yaml": acmeRule, "other.yaml": acmeRule}), "rules")
	if err == nil || !strings.Contains(err.Error(), `other.yaml: line 1: id: "acme" doesn't match the file name other.yaml`) {
		t.Fatalf("err = %v", err)
	}
}

func TestUnknownKey(t *testing.T) {
	bad := strings.Replace(acmeRule, "priority: 20\n", "priority: 20\nshade: red\n", 1)
	_, err := parseRule("acme.yaml", []byte(bad))
	if err == nil || err.Error() != `acme.yaml: line 4: unknown key "shade"` {
		t.Fatalf("err = %v", err)
	}
	nested := strings.Replace(acmeRule, "  globals: [Acme]\n", "  globals: [Acme]\n  frames: [x]\n", 1)
	_, err = parseRule("acme.yaml", []byte(nested))
	if err == nil || err.Error() != `acme.yaml: line 7: unknown key "frames"` {
		t.Fatalf("err = %v", err)
	}
}

func TestAPIJSDenyList(t *testing.T) {
	for _, call := range []string{`eval("x")`, `new Function("x")()`, `fetch("/x")`, `new XMLHttpRequest()`,
		`document.cookie = "a=1"`, `localStorage.setItem("a", "1")`, "`${location}`", `Acme.a(); Acme.b()`,
		`Acme.reject("x" + location.href)`} {
		bad := strings.Replace(acmeRule, "reject: Acme.rejectAll()", "reject: '"+strings.ReplaceAll(call, "'", "''")+"'", 1)
		_, err := parseRule("acme.yaml", []byte(bad))
		if err == nil || !strings.HasPrefix(err.Error(), "acme.yaml: line 21: api.reject: ") {
			t.Errorf("%s: err = %v", call, err)
		}
	}
	if _, err := parseRule("acme.yaml", []byte(strings.Replace(acmeRule, "state: JSON.stringify(Acme.state)",
		`state: "JSON.stringify(Acme.list().map(s => ({id: s.id})))"`, 1))); err != nil {
		t.Errorf("an arrow function in a constant expression was refused: %v", err)
	}
	_, err := parseRule("acme.yaml", []byte(strings.Replace(acmeRule, "rejected: {statistics: false}",
		"rejected: {statistics: false}\n  state_js: 'eval(\"x\")'", 1)))
	if err == nil || !strings.Contains(err.Error(), "verify.state_js: ") {
		t.Errorf("state_js: err = %v", err)
	}
}

func TestRuleErrors(t *testing.T) {
	for _, tc := range []struct{ from, to, want string }{
		{"id: acme", "id: Acme", `line 1: id: "Acme": want lowercase letters, digits and -`},
		{"name: Acme CMP", "name: ''", "line 2: name: missing"},
		{"priority: 20", "priority: -1", "line 3: priority: -1: want 0 to 1000"},
		{`selectors: ["#acme-banner"]`, `selectors: ["#a >> #b"]`, "line 5: detect.selectors.0: want one step, not a chain with >>"},
		{`selectors: ["#acme-banner"]`, `selectors: ["[x"]`, "line 5: detect.selectors.0: selector \"[x\": step 1: missing ]"},
		{"globals: [Acme]", "globals: [\"a-b\"]", `line 6: detect.globals.0: "a-b": want a JavaScript name`},
		{"scripts: [cdn.acme.test/cmp.js]", "scripts: [\"a b c d\"]", `line 7: detect.scripts.0: "a b c d": want a URL substring of 4 to 200 characters`},
		{"  selector: \"#acme-banner\"", "  selector: ''", "line 9: banner.selector: missing"},
		{`accept: ["#acme-accept"]`, `accept: ["#a >> "]`, `line 10: accept.0: selector "#a >> ": step 2 is empty`},
		{"save: [\"#acme-save\"]", "save: []", "line 16: reject.second_layer.save: missing"},
		{"  api: reopen", "  api: close", `line 18: reopen.api: "close": want the name of a set api entry (reopen)`},
		{"parser: cookiebot", "parser: acme", "line 25: verify.parser: \"acme\": want one of"},
		{"cookies: [acme_consent]", "cookies: [\"a;b\"]", `line 26: verify.cookies.0: "a;b": want a cookie name`},
		{"sources: [https://docs.acme.test/cmp]", "sources: [http://docs.acme.test/cmp]", `line 29: sources.0: "http://docs.acme.test/cmp": want an https URL`},
		{"sources: [https://docs.acme.test/cmp]", "sources: []", "line 29: sources: missing"},
		{"fixtures: [testdata/acme/first-layer.html]", "fixtures: [../etc/passwd]", `line 30: fixtures.0: "../etc/passwd": want a path under testdata/`},
		{"sources:", "geo_override: {param: otgeo, value_from: ip}\nsources:", `line 29: geo_override.value_from: "ip": want country`},
		{"accept: [\"#acme-accept\"]\n", "", "line 1: accept: want at least one selector or api.accept"},
		{"priority: 20", "priority: twenty", "line 3: cannot unmarshal !!str `twenty` into int"},
		{"id: acme", "id: [acme", "line 1: did not find expected ',' or ']'"},
	} {
		src := strings.Replace(acmeRule, tc.from, tc.to, 1)
		if tc.from == "accept: [\"#acme-accept\"]\n" {
			src = strings.Replace(src, "  accept: Acme.acceptAll()\n", "", 1)
		}
		_, err := parseRule("acme.yaml", []byte(src))
		if err == nil || !strings.Contains(err.Error(), "acme.yaml: "+tc.want) {
			t.Errorf("%s -> %s: err = %v; want %q", tc.from, tc.to, err, tc.want)
		}
	}
	for _, src := range []string{"", "- a\n- b\n", "just text"} {
		if _, err := parseRule("acme.yaml", []byte(src)); err == nil || !strings.HasPrefix(err.Error(), "acme.yaml: line 1: ") {
			t.Errorf("%q: err = %v", src, err)
		}
	}
	if _, err := parseRule("acme.yaml", []byte(strings.Repeat("#", maxRuleBytes+1))); err == nil {
		t.Error("an oversized rule was read")
	}
}

func TestRuleParses(t *testing.T) {
	r, err := parseRule("acme.yaml", []byte(acmeRule))
	if err != nil {
		t.Fatal(err)
	}
	if r.Banners[0].CSS != "#acme-banner" || len(r.Accept) != 1 || r.Second == nil || r.Second.Toggles[0].CSS != "#acme-prefs input.toggle:not([disabled])" {
		t.Fatalf("rule = %+v", r)
	}
	if r.Verify.StateJS != "JSON.stringify(Acme.state)" || r.ReopenAPI != "reopen" || r.Verify.Cookies[0] != "acme_consent" {
		t.Fatalf("verify = %+v", r.Verify)
	}
	shadow, err := parseRule("acme.yaml", []byte(strings.Replace(acmeRule, `accept: ["#acme-accept"]`, `accept: ["#root::shadow #accept", "#more >> #all"]`, 1)))
	if err != nil {
		t.Fatal(err)
	}
	if got := shadow.Accept[0].Steps[0]; got.CSS != "#accept" || !slices.Equal(got.Shadow, []string{"#root"}) || len(shadow.Accept[1].Steps) != 2 {
		t.Fatalf("accept = %+v", shadow.Accept)
	}
}

func TestRuleLists(t *testing.T) {
	src := strings.NewReplacer(`  selector: "#acme-banner"`, "  selector: [\"#acme-banner\", \"#acme-root ::shadow .banner\"]",
		`toggles_off: "#acme-prefs input.toggle:not([disabled])"`, `toggles_off: ["#acme-prefs input.toggle", "#acme-root ::shadow input"]`).Replace(acmeRule)
	r, err := parseRule("acme.yaml", []byte(src))
	if err != nil {
		t.Fatal(err)
	}
	if len(r.Banners) != 2 || r.Banners[1].CSS != ".banner" || !slices.Equal(r.Banners[1].Shadow, []string{"#acme-root"}) || r.BannerRaw != "#acme-banner" {
		t.Fatalf("banners = %+v (%q)", r.Banners, r.BannerRaw)
	}
	if len(r.Second.Toggles) != 2 || r.Second.Toggles[1].CSS != "input" {
		t.Fatalf("toggles = %+v", r.Second.Toggles)
	}
	for _, tc := range []struct{ from, to, want string }{
		{`  selector: "#acme-banner"`, `  selector: ["#acme-banner", ""]`, "line 9: banner.selector.1: selector is empty"},
		{`  selector: "#acme-banner"`, `  selector: []`, "line 9: banner.selector: missing"},
		{`  selector: "#acme-banner"`, `  selector: ["#a >> #b"]`, "line 9: banner.selector: want one step, not a chain with >>"},
		{`  selector: "#acme-banner"`, `  selector: {a: b}`, "line 9: cannot unmarshal !!map into []string"},
	} {
		_, err := parseRule("acme.yaml", []byte(strings.Replace(acmeRule, tc.from, tc.to, 1)))
		if err == nil || !strings.Contains(err.Error(), "acme.yaml: "+tc.want) {
			t.Errorf("%s: err = %v; want %q", tc.to, err, tc.want)
		}
	}
	host := strings.NewReplacer(`  selector: "#acme-banner"`, `  selector: "#acme-root ::shadow .banner"`,
		`selectors: ["#acme-banner"]`, `selectors: ["#acme-root"]`).Replace(acmeRule)
	d, err := load(mapFS(map[string]string{"acme.yaml": host}), "rules")
	if err != nil {
		t.Fatal(err)
	}
	if got := d.Rules()[0].DetectedBy; got != "#acme-root (shadow DOM), window.Acme" {
		t.Errorf("DetectedBy = %q", got)
	}
}

func TestRulesHash(t *testing.T) {
	files, _ := fs.Glob(rulesFS, "rules/*.yaml")
	slices.Sort(files)
	sum := sha256.New()
	for _, f := range files {
		b, _ := fs.ReadFile(rulesFS, f)
		sum.Write(b)
	}
	lex, _ := os.ReadFile("generic_words.yaml")
	sum.Write(lex)
	d, err := load(rulesFS, "rules")
	if err != nil {
		t.Fatal(err)
	}
	if want := hex.EncodeToString(sum.Sum(nil)); d.Info().SHA256 != want {
		t.Fatalf("SHA256 = %s; want %s", d.Info().SHA256, want)
	}
	if consent.Linked().CMP.Info() != d.Info() {
		t.Error("the registered driver names another rule pack")
	}
	a, _ := load(mapFS(map[string]string{"acme.yaml": acmeRule}), "rules")
	b, _ := load(mapFS(map[string]string{"acme.yaml": acmeRule}), "rules")
	c, _ := load(mapFS(map[string]string{"acme.yaml": strings.Replace(acmeRule, "priority: 20", "priority: 21", 1)}), "rules")
	if a.Info().SHA256 != b.Info().SHA256 || a.Info().SHA256 == c.Info().SHA256 {
		t.Fatalf("hash not stable or not changed by one byte: %s %s %s", a.Info().SHA256, b.Info().SHA256, c.Info().SHA256)
	}
}

func TestRulesListing(t *testing.T) {
	d, err := load(mapFS(map[string]string{"acme.yaml": acmeRule}), "rules")
	if err != nil {
		t.Fatal(err)
	}
	got := d.Rules()[0]
	want := consent.CMPRuleInfo{ID: "acme", Name: "Acme CMP", DetectedBy: "#acme-banner, window.Acme", Accept: "button, api",
		Reject: "first layer or settings, api", StateFrom: "acme_consent cookie", Sources: []string{"https://docs.acme.test/cmp"}}
	if got.ID != want.ID || got.Name != want.Name || got.DetectedBy != want.DetectedBy || got.Accept != want.Accept ||
		got.Reject != want.Reject || got.StateFrom != want.StateFrom || !slices.Equal(got.Sources, want.Sources) {
		t.Fatalf("listing = %+v\nwant      %+v", got, want)
	}
	plain := strings.NewReplacer("  accept: Acme.acceptAll()\n", "", "  reject: Acme.rejectAll()\n", "",
		"  second_layer:\n    open: [\"#acme-settings\"]\n    toggles_off: \"#acme-prefs input.toggle:not([disabled])\"\n    save: [\"#acme-save\"]\n", "",
		`selectors: ["#acme-banner"]`, `selectors: ["#root::shadow #acme-banner"]`, "cookies: [acme_consent]", "cookies: [\"acme_*\"]").Replace(acmeRule)
	d, err = load(mapFS(map[string]string{"acme.yaml": plain}), "rules")
	if err != nil {
		t.Fatal(err)
	}
	if got := d.Rules()[0]; got.Accept != "button" || got.Reject != "first layer" || got.DetectedBy != "#root (shadow DOM), window.Acme" || got.StateFrom != "acme_* cookies" {
		t.Fatalf("listing = %+v", got)
	}
}

func TestStateFromWords(t *testing.T) {
	for _, tc := range []struct {
		v    consent.VerifySpec
		want string
	}{
		{consent.VerifySpec{Parser: "banner"}, "banner gone and stays gone"},
		{consent.VerifySpec{Parser: "usercentrics", Storage: []string{"uc_settings"}}, "uc_settings storage"},
		{consent.VerifySpec{Parser: "tcf"}, "TCF string"},
		{consent.VerifySpec{Parser: "borlabs", StateJS: "x"}, "CMP API"},
	} {
		if got := (&rule{Verify: tc.v}).stateFrom(); got != tc.want {
			t.Errorf("%+v: %q; want %q", tc.v, got, tc.want)
		}
	}
}

func TestCandidates(t *testing.T) {
	d, err := load(mapFS(map[string]string{"acme.yaml": acmeRule}), "rules")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := d.candidates(&consent.Plan{CMP: "nope"}); err == nil || err.Error() != `--cmp "nope": want auto or one of acme` {
		t.Fatalf("err = %v", err)
	}
	if rs, err := d.candidates(&consent.Plan{CMP: "acme"}); err != nil || len(rs) != 1 {
		t.Fatalf("rules %v, err %v", rs, err)
	}
}

// standardPages are the replica pages every named rule ships (qa.md
// 3.2); a rule may add one special-case page.
var standardPages = []string{"first-layer.html", "second-layer.html", "hidden.html", "late.html", "two-banners.html", "text-only.html", "script-no-banner.html"}

var captured = regexp.MustCompile(`(?i)captured (20\d\d-\d\d-\d\d)`)

func TestEveryRuleHasFixtures(t *testing.T) {
	d, err := load(rulesFS, "rules")
	if err != nil {
		t.Fatal(err)
	}
	for _, r := range d.rules {
		if r.ID == genericID {
			continue // the generic rule has its own language and negative pages
		}
		ex := readExpect(t, r.ID)
		rows := map[string]bool{}
		for _, p := range ex.Pages {
			rows[p.Page] = true
		}
		want := slices.Clone(standardPages)
		for _, f := range r.Fixtures {
			if name := filepath.Base(f); !slices.Contains(want, name) {
				want = append(want, name)
			}
		}
		if len(want) > len(standardPages)+1 {
			t.Errorf("%s: more than one special-case page: %v", r.ID, want[len(standardPages):])
		}
		for _, name := range want {
			path := filepath.Join("testdata", r.ID, name)
			b, err := os.ReadFile(path)
			if err != nil {
				t.Errorf("%s: %v", r.ID, err)
				continue
			}
			if !slices.Contains(r.Fixtures, "testdata/"+r.ID+"/"+name) {
				t.Errorf("%s: fixtures doesn't list %s", r.ID, name)
			}
			if !rows[name] {
				t.Errorf("%s: expect.yaml has no row for %s", r.ID, name)
			}
			if !strings.Contains(string(b), "Source: https://") || !captured.Match(b) {
				t.Errorf("%s: %s has no source URL and capture date comment", r.ID, name)
			}
		}
	}
}

// expectFile is testdata/<cmp>/expect.yaml (format in testdata/README.md).
type expectFile struct {
	CMP   string       `yaml:"cmp"`
	Pages []expectPage `yaml:"pages"`
}

type expectPage struct {
	Page          string `yaml:"page"`
	Found         bool   `yaml:"found"`
	Present       bool   `yaml:"present"`
	BannerAfterMS int64  `yaml:"banner_after_ms"`
	DetectedBy    string `yaml:"detected_by"`
	AcceptLayer   int    `yaml:"accept_layer"`
	RejectLayer   int    `yaml:"reject_layer"`
	Fallback      bool   `yaml:"fallback"`      // no button to click: --click button,api uses the API
	TCF           bool   `yaml:"tcf"`           // the page defines __tcfapi
	Stores        string `yaml:"stores"`        // tcf: the choice is kept in the TC string only
	AcceptText    string `yaml:"accept_text"`   // generic: the Accept Find reports
	RejectText    string `yaml:"reject_text"`   // generic: the Reject (or settings opener) Find reports
	RejectReason  string `yaml:"reject_reason"` // generic: why the Reject isn't verified
	Sees          sees   `yaml:"sees"`
}

// sees is the page as the CMP scripts see it in a browser; the FakeTab
// tests answer the scripts from it.
type sees struct {
	Selectors []string     `yaml:"selectors"`
	Globals   []string     `yaml:"globals"`
	Scripts   []string     `yaml:"scripts"`
	Banner    bool         `yaml:"banner"`
	Controls  []string     `yaml:"controls"` // clickable on the first layer
	Settings  []string     `yaml:"settings"` // clickable once the settings are open
	Toggles   []bool       `yaml:"toggles"`  // each toggle's state when the settings open
	Decoys    []string     `yaml:"decoys"`   // other controls on the page, never clicked
	Closed    []string     `yaml:"closed"`   // controls behind a closed shadow root
	Generic   *genericSees `yaml:"generic"`  // what the generic script reports
}

func readExpect(t *testing.T, id string) expectFile {
	t.Helper()
	b, err := os.ReadFile(filepath.Join("testdata", id, "expect.yaml"))
	if err != nil {
		t.Fatalf("%s: %v", id, err)
	}
	var ex expectFile
	dec := yaml.NewDecoder(strings.NewReader(string(b)))
	dec.KnownFields(true)
	if err := dec.Decode(&ex); err != nil {
		t.Fatalf("%s/expect.yaml: %v", id, err)
	}
	if ex.CMP != id {
		t.Fatalf("%s/expect.yaml names cmp %q", id, ex.CMP)
	}
	return ex
}

var lineRef = regexp.MustCompile(`^fuzz\.yaml: line [1-9][0-9]*: `)

func FuzzCMPRuleYAML(f *testing.F) {
	f.Add([]byte(acmeRule))
	f.Add([]byte(""))
	f.Add([]byte("id: [\n"))
	f.Add([]byte("id: x\nname: y\nbanner: {selector: '#a'}\napi: {reject: 'eval(1)'}\n"))
	files, _ := fs.Glob(rulesFS, "rules/*.yaml")
	for _, name := range files {
		if b, err := fs.ReadFile(rulesFS, name); err == nil {
			f.Add(b)
		}
	}
	f.Fuzz(func(t *testing.T, data []byte) {
		r, err := parseRule("fuzz.yaml", data)
		if err != nil {
			if !lineRef.MatchString(err.Error()) {
				t.Fatalf("error names no line: %v", err)
			}
			return
		}
		if (r.BannerRaw == "" && r.ID != genericID) || r.Name == "" || len(r.Sources) == 0 {
			t.Fatalf("a rule without banner, name or sources was accepted: %+v", r)
		}
	})
}
