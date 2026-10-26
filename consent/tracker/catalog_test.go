// Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
// SPDX-License-Identifier: MIT

package tracker

import (
	"errors"
	"io/fs"
	"maps"
	"path"
	"reflect"
	"regexp"
	"slices"
	"strings"
	"testing"
	"testing/fstest"

	"github.com/RhythmShahriar/echo/consent"
)

// prdIDs are the 47 entries of PRD 12.1.
var prdIDs = []string{
	"ga4", "universal-analytics", "google-tag-manager", "google-ads", "floodlight", "google-remarketing", "google-ccm",
	"meta-pixel", "tiktok", "linkedin", "microsoft-uet", "microsoft-clarity", "hotjar", "pinterest", "snap", "x-pixel",
	"reddit", "hubspot", "criteo", "taboola", "outbrain", "quora", "amazon-ads", "yandex-metrica", "matomo", "piwik-pro",
	"adobe-analytics", "segment", "mixpanel", "amplitude", "posthog", "heap", "fullstory", "mouseflow", "lucky-orange",
	"klaviyo", "plausible", "fathom", "youtube", "vimeo", "google-maps",
	"stripe", "paypal", "recaptcha", "hcaptcha", "turnstile", "google-fonts",
}

// pendingIDs are the PRD entries later items add. TestCatalogCount
// drops them from this list as they land.
var pendingIDs = slices.DeleteFunc(slices.Clone(prdIDs), func(id string) bool {
	return slices.Index(prdIDs, id) < 7 || slices.Contains(advertisingIDs, id)
})

// advertisingIDs are the advertising and social entries.
var advertisingIDs = []string{"meta-pixel", "tiktok", "linkedin", "microsoft-uet", "pinterest", "snap", "x-pixel", "reddit",
	"quora", "amazon-ads", "criteo", "taboola", "outbrain", "hubspot", "klaviyo"}

// minimal is a valid entry and its fixtures, for the loader tests to
// break one piece at a time.
const (
	minimalEntry = `id: shop-pixel
name: Shop pixel
vendor: Shop
category: advertising
consent: needed
hits:
  - host: px.shop.example
    path: /collect
cookies: [_shpx]
fix: Load the shop pixel after consent
docs: https://shop.example/docs
sources: ["https://shop.example/docs"]
`
	minimalFixtures = `hits:
  match: ["https://px.shop.example/collect?e=view"]
  no_match: ["https://px.shop.example/pixel.js"]
cookies:
  match: [_shpx]
  no_match: [_shop]
`
)

// catalogOf builds an in-memory catalog folder from file name and
// content pairs.
func catalogOf(files map[string]string) fs.FS {
	m := fstest.MapFS{}
	for name, body := range files {
		m["catalog/"+name] = &fstest.MapFile{Data: []byte(body)}
	}
	return m
}

func TestLoadAll(t *testing.T) {
	c, err := load(catalogFS, catalogDir)
	if err != nil {
		t.Fatal(err)
	}
	files, _ := fs.Glob(catalogFS, "catalog/*.yaml")
	entries := 0
	for _, f := range files {
		if !strings.HasSuffix(f, ".fixtures.yaml") {
			entries++
		}
	}
	if len(c.entries) != entries || entries == 0 {
		t.Fatalf("loaded %d entries from %d catalog files", len(c.entries), entries)
	}
	if consent.Linked().Classifier != consent.Classifier(builtin) {
		t.Fatal("the built-in catalog isn't the registered Classifier")
	}
}

func TestUnknownKey(t *testing.T) {
	entry := "id: bad\nname: Bad\nhits:\n  - hostz: [px.shop.example]\n"
	_, err := load(catalogOf(map[string]string{"bad.yaml": entry, "bad.fixtures.yaml": minimalFixtures}), "catalog")
	if err == nil || !strings.Contains(err.Error(), `catalog/bad.yaml:4: unknown key "hostz"`) {
		t.Fatalf("err = %v, want catalog/bad.yaml:4: unknown key \"hostz\"", err)
	}
	// The same in a fixtures file and in a fixture mapping.
	for _, c := range []struct{ fixtures, want string }{
		{"hits:\n  match: [\"https://px.shop.example/collect\"]\n  nomatch: []\n", `catalog/shop-pixel.fixtures.yaml:3: unknown key "nomatch"`},
		{"hits:\n  match:\n    - {url: \"https://px.shop.example/collect\", bodyy: x}\n", `catalog/shop-pixel.fixtures.yaml:3: unknown key "bodyy"`},
		{"cookies:\n  match: [{name: _shpx, domian: x}]\n", `catalog/shop-pixel.fixtures.yaml:2: unknown key "domian"`},
	} {
		_, err := load(catalogOf(map[string]string{"shop-pixel.yaml": minimalEntry, "shop-pixel.fixtures.yaml": c.fixtures}), "catalog")
		if err == nil || !strings.Contains(err.Error(), c.want) {
			t.Errorf("err = %v, want %s", err, c.want)
		}
	}
}

func TestIDMatchesFile(t *testing.T) {
	_, err := load(catalogOf(map[string]string{"other.yaml": minimalEntry, "other.fixtures.yaml": minimalFixtures}), "catalog")
	if err == nil || !strings.Contains(err.Error(), `catalog/other.yaml:1: id "shop-pixel" must match the file name (other.yaml)`) {
		t.Fatalf("err = %v", err)
	}
	if _, err := load(catalogOf(map[string]string{"shop-pixel.yaml": minimalEntry, "shop-pixel.fixtures.yaml": minimalFixtures}), "catalog"); err != nil {
		t.Fatalf("minimal entry: %v", err)
	}
}

func TestLoadRefuses(t *testing.T) {
	for _, c := range []struct{ name, from, to, want string }{
		{"bad id", "id: shop-pixel", "id: Shop_Pixel", `id "Shop_Pixel" must be lowercase`},
		{"category", "category: advertising", "category: ads", `category "ads": want analytics`},
		{"consent", "consent: needed", "consent: maybe", `consent "maybe": want needed, exempt or no`},
		{"necessary needs no consent", "category: advertising", "category: necessary", "a necessary entry has consent: no"},
		{"fix needed", "fix: Load the shop pixel after consent\n", "", "fix is required"},
		{"docs", "docs: https://shop.example/docs", "docs: http://shop.example/docs", "docs must be an https:// URL"},
		{"no sources", "sources: [\"https://shop.example/docs\"]", "sources: []", "sources lists where"},
		{"host", "host: px.shop.example", "host: PX.shop.example", `host "PX.shop.example"`},
		{"host and hosts", "host: px.shop.example", "host: px.shop.example\n    hosts: [a.example]", "use host or hosts, not both"},
		{"path", "path: /collect", "path: collect", `path "collect" must start with /`},
		{"double star", "path: /collect", "path: /c*/**", "/** follows a plain path"},
		{"method", "path: /collect", "path: /collect\n    methods: [FETCH]", `method "FETCH"`},
		{"regexp", "path: /collect", "path: /collect\n    params_match: {e: '('}", "params_match: e:"},
		{"no hosts without any_host", "  - host: px.shop.example\n    path: /collect\n", "  - path: /collect\n", "needs any_host: true"},
		{"any host needs proof", "consent: needed\n", "consent: needed\nany_host: true\n", ""},
		{"cookie pattern", "cookies: [_shpx]", "cookies: [\"_s*p*\"]", "at most one *"},
		{"cookie star", "cookies: [_shpx]", "cookies: [\"*\"]", "would match every name"},
		{"id_path groups", "fix:", "params: {id_path: '^/x/'}\nfix:", "exactly one capture group"},
		{"exempt with cookies", "consent: needed", "consent: exempt", "an exempt entry sets no cookies"},
		{"matches nothing", "hits:\n  - host: px.shop.example\n    path: /collect\ncookies: [_shpx]\n", "", "matches nothing"},
	} {
		if c.want == "" {
			continue
		}
		entry := strings.Replace(minimalEntry, c.from, c.to, 1)
		if entry == minimalEntry {
			t.Fatalf("%s: %q not in the minimal entry", c.name, c.from)
		}
		_, err := load(catalogOf(map[string]string{"shop-pixel.yaml": entry, "shop-pixel.fixtures.yaml": minimalFixtures}), "catalog")
		if err == nil || !strings.Contains(err.Error(), c.want) || !strings.HasPrefix(err.Error(), "catalog/shop-pixel.yaml:") {
			t.Errorf("%s: err = %v, want %q", c.name, err, c.want)
		}
	}
	anyHost := strings.Replace(minimalEntry, "  - host: px.shop.example\n    path: /collect\n", "  - path: /collect\n", 1)
	anyHost = strings.Replace(anyHost, "consent: needed\n", "consent: needed\nany_host: true\n", 1)
	_, err := load(catalogOf(map[string]string{"shop-pixel.yaml": anyHost, "shop-pixel.fixtures.yaml": minimalFixtures}), "catalog")
	if err == nil || !strings.Contains(err.Error(), "catalog/shop-pixel.yaml:8: a hit on any host needs params_all, params_match or body_json") {
		t.Errorf("any host by path alone: err = %v", err)
	}
}

func TestLoadRefusesClashes(t *testing.T) {
	other := strings.ReplaceAll(minimalEntry, "shop-pixel", "other-pixel")
	other = strings.Replace(other, "px.shop.example", "px.other.example", 1)
	otherFix := strings.ReplaceAll(minimalFixtures, "px.shop.example", "px.other.example")
	_, err := load(catalogOf(map[string]string{
		"shop-pixel.yaml": minimalEntry, "shop-pixel.fixtures.yaml": minimalFixtures,
		"other-pixel.yaml": other, "other-pixel.fixtures.yaml": otherFix,
	}), "catalog")
	if err == nil || !strings.Contains(err.Error(), `cookies: "_shpx" is already claimed by other-pixel`) {
		t.Fatalf("err = %v", err)
	}
	_, err = load(catalogOf(map[string]string{"shop-pixel.fixtures.yaml": minimalFixtures}), "catalog")
	if err == nil || !strings.Contains(err.Error(), "fixtures without a catalog file") {
		t.Fatalf("lone fixtures: err = %v", err)
	}
}

func TestCatalogHash(t *testing.T) {
	files, _ := fs.Glob(catalogFS, "catalog/*.yaml")
	data := map[string][]byte{}
	for _, f := range files {
		data[f], _ = fs.ReadFile(catalogFS, f)
	}
	h := hashFiles(files, data)
	if h != hashFiles(slices.Clone(files), maps.Clone(data)) {
		t.Fatal("the same files gave another hash")
	}
	changed := maps.Clone(data)
	b := slices.Clone(changed[files[0]])
	b[len(b)-1] ^= 1
	changed[files[0]] = b
	if hashFiles(files, changed) == h {
		t.Fatal("a changed byte kept the hash")
	}
	if got := builtin.Info(); got.SHA256 != h || got.Version != consent.DataVersion || got.Count != len(builtin.entries) {
		t.Fatalf("Info() = %+v, want hash %s", got, h)
	}
	// `consent trackers` and every result print what the registered
	// classifier says, which must be this catalog's hash.
	if got := consent.Linked().Classifier.Info().SHA256; got != h {
		t.Fatalf("registered classifier hash %s, want %s", got, h)
	}
	if len(h) != 64 {
		t.Fatalf("hash %q is not a SHA-256", h)
	}
}

func TestCatalogCount(t *testing.T) {
	var want []string
	for _, id := range prdIDs {
		if !slices.Contains(pendingIDs, id) {
			want = append(want, id)
		}
	}
	slices.Sort(want)
	var got []string
	for _, tr := range builtin.Trackers() {
		got = append(got, tr.ID)
	}
	if !slices.Equal(got, want) {
		t.Fatalf("catalog ids:\n got %v\nwant %v", got, want)
	}
	if len(prdIDs) != 47 || builtin.Info().Count != len(want) {
		t.Fatalf("%d PRD ids, count %d", len(prdIDs), builtin.Info().Count)
	}
}

func TestHostPathShorthand(t *testing.T) {
	compileOne := func(s ruleSpec) *rule {
		r, err := compileRule(&s, consent.HitBeacon, false, func(_, f string, a ...any) error { t.Fatalf(f, a...); return nil })
		if err != nil {
			t.Fatal(err)
		}
		return r
	}
	short := compileOne(ruleSpec{Host: "px.shop.example", Path: "/collect"})
	long := compileOne(ruleSpec{Hosts: []string{"px.shop.example"}, Paths: []string{"/collect"}})
	if !reflect.DeepEqual(short, long) {
		t.Fatalf("host:/path: gave %+v, hosts:/paths: %+v", short, long)
	}
	// The PRD 8.1 form loads unchanged (minimalEntry uses it).
	c, err := load(catalogOf(map[string]string{"shop-pixel.yaml": minimalEntry, "shop-pixel.fixtures.yaml": minimalFixtures}), "catalog")
	if err != nil {
		t.Fatal(err)
	}
	if h := c.Request(&consent.Request{Method: "GET", URL: "https://px.shop.example/collect?e=view"}, nil, nil); h == nil || h.Tracker != "shop-pixel" {
		t.Fatalf("shorthand entry didn't match: %+v", h)
	}
	if _, err := compileRule(&ruleSpec{Path: "/a", Paths: []string{"/b"}, Host: "a.example"}, consent.HitBeacon, false,
		func(_, f string, a ...any) error { return errors.New(f) }); err == nil {
		t.Fatal("path and paths together were accepted")
	}
}

func TestEveryEntryHasFixtures(t *testing.T) {
	for _, e := range builtin.entries {
		ff := readFixtures(t, e.info.ID)
		kinds := map[consent.HitKind]bool{}
		for _, r := range e.rules {
			kinds[r.kind] = true
		}
		if kinds[consent.HitBeacon] && (len(ff.Hits.Match) == 0 || len(ff.Hits.NoMatch) == 0) {
			t.Errorf("%s: hits need a match and a no_match fixture", e.info.ID)
		}
		if kinds[consent.HitScript] && len(ff.Scripts.Match) == 0 {
			t.Errorf("%s: scripts need a match fixture", e.info.ID)
		}
		if len(e.cookies) > 0 && (len(ff.Cookies.Match) == 0 || len(ff.Cookies.NoMatch) == 0) {
			t.Errorf("%s: cookies need a match and a no_match fixture", e.info.ID)
		}
	}
	// The loader enforces the same rule, so a new entry can't skip it.
	for _, c := range []struct{ fixtures, want string }{
		{"hits:\n  match: [\"https://px.shop.example/collect\"]\ncookies:\n  match: [_shpx]\n  no_match: [x]\n", "needs at least one hits.no_match fixture"},
		{"cookies:\n  match: [_shpx]\n  no_match: [x]\n", "needs at least one hits.match fixture"},
		{"hits:\n  match: [\"https://px.shop.example/collect\"]\n  no_match: [\"https://px.shop.example/x\"]\ncookies:\n  match: [_shpx]\n", "needs at least one cookies.match and one cookies.no_match"},
		{"hits:\n  match: [\"https://px.shop.example/other\"]\n  no_match: [\"https://px.shop.example/x\"]\ncookies:\n  match: [_shpx]\n  no_match: [x]\n",
			`catalog/shop-pixel.fixtures.yaml:2: hits.match "https://px.shop.example/other" doesn't match shop-pixel`},
		{"hits:\n  match: [\"https://px.shop.example/collect\"]\n  no_match: [\"https://px.shop.example/collect?x=1\"]\ncookies:\n  match: [_shpx]\n  no_match: [x]\n",
			`hits.no_match "https://px.shop.example/collect?x=1" matches shop-pixel`},
		{"hits:\n  match: [\"https://px.shop.example/collect\"]\n  no_match: [\"https://px.shop.example/x\"]\ncookies:\n  match: [_shpx]\n  no_match: [_shpx]\n",
			`cookies.no_match "_shpx" matches shop-pixel`},
		{"hits:\n  match: [\"ftp://px.shop.example/collect\"]\n", "is not an http or https URL"},
	} {
		_, err := load(catalogOf(map[string]string{"shop-pixel.yaml": minimalEntry, "shop-pixel.fixtures.yaml": c.fixtures}), "catalog")
		if err == nil || !strings.Contains(err.Error(), c.want) {
			t.Errorf("err = %v, want %q", err, c.want)
		}
	}
	scripts := strings.Replace(minimalEntry, "cookies: [_shpx]", "scripts: [{host: cdn.shop.example}]\ncookies: [_shpx]", 1)
	_, err := load(catalogOf(map[string]string{"shop-pixel.yaml": scripts, "shop-pixel.fixtures.yaml": minimalFixtures}), "catalog")
	if err == nil || !strings.Contains(err.Error(), "needs at least one scripts.match fixture") {
		t.Errorf("scripts without fixtures: err = %v", err)
	}
	_, err = load(catalogOf(map[string]string{"shop-pixel.yaml": minimalEntry}), "catalog")
	if err == nil || !strings.Contains(err.Error(), "catalog/shop-pixel.yaml: no fixtures: add shop-pixel.fixtures.yaml") {
		t.Errorf("no fixtures file: err = %v", err)
	}
}

func TestTrackersInfo(t *testing.T) {
	list := builtin.Trackers()
	if !slices.IsSortedFunc(list, func(a, b consent.TrackerInfo) int { return strings.Compare(a.ID, b.ID) }) {
		t.Fatal("Trackers() isn't sorted by id")
	}
	for _, tr := range list {
		if tr.Name == "" || tr.Vendor == "" || tr.Matches == "" || tr.Docs == "" || len(tr.Sources) == 0 {
			t.Errorf("%s: incomplete %+v", tr.ID, tr)
		}
		if tr.Need == consent.NeedNeeded && tr.Fix == "" {
			t.Errorf("%s: no fix", tr.ID)
		}
	}
	list[0].Sources[0] = "changed"
	if builtin.Trackers()[0].Sources[0] == "changed" {
		t.Fatal("Trackers() shares its Sources with the catalog")
	}
	ga := slices.IndexFunc(list, func(t consent.TrackerInfo) bool { return t.ID == "ga4" })
	if got := list[ga].Matches; !strings.HasPrefix(got, "www.google-analytics.com/g/collect, www.google-analytics.com/g/s/collect, region1.google-analytics.com/g/collect and ") {
		t.Fatalf("ga4 Matches = %q", got)
	}
}

func TestAddCustomNotYet(t *testing.T) {
	if err := builtin.AddCustom(nil); err != nil {
		t.Fatalf("no custom trackers: %v", err)
	}
	if err := builtin.AddCustom([]byte("- id: shop-pixel\n")); !errors.Is(err, errNoCustom) {
		t.Fatalf("custom trackers: %v", err)
	}
}

// located errors always start with file:line, so a fuzzed file can't
// produce a message that points nowhere.
var lineRe = regexp.MustCompile(`^catalog/f(\.fixtures)?\.yaml:[0-9]+: `)

func FuzzCatalogYAML(f *testing.F) {
	f.Add([]byte(minimalEntry), []byte(minimalFixtures))
	f.Add([]byte("id: bad\nname: Bad\nhits:\n  - hostz: [x]\n"), []byte(minimalFixtures))
	f.Add([]byte(minimalEntry), []byte("hits:\n  match: [\"https://px.shop.example/collect\"]\n"))
	f.Add([]byte(""), []byte(""))
	f.Add([]byte("- a\n- b\n"), []byte("{"))
	for _, id := range []string{"ga4", "meta-pixel", "google-ads"} {
		e, _ := fs.ReadFile(catalogFS, path.Join(catalogDir, id+".yaml"))
		x, _ := fs.ReadFile(catalogFS, path.Join(catalogDir, id+".fixtures.yaml"))
		f.Add(e, x)
	}
	f.Fuzz(func(t *testing.T, entry, fixtures []byte) {
		e, err := loadEntry("catalog/f.yaml", entry, "catalog/f.fixtures.yaml", fixtures, false)
		if err != nil {
			if !lineRe.MatchString(err.Error()) {
				t.Fatalf("error without a line: %q", err)
			}
			return
		}
		// Loaded: the fixture rule held.
		var ff fixtureFile
		if _, err := decodeStrict(fixtures, &ff); err != nil {
			t.Fatalf("fixtures loaded but don't decode: %v", err)
		}
		for _, r := range e.rules {
			if r.kind == consent.HitBeacon && (len(ff.Hits.Match) == 0 || len(ff.Hits.NoMatch) == 0) {
				t.Fatal("an entry with hits loaded without hits fixtures")
			}
			if r.kind == consent.HitScript && len(ff.Scripts.Match) == 0 {
				t.Fatal("an entry with scripts loaded without scripts fixtures")
			}
		}
		if len(e.cookies) > 0 && (len(ff.Cookies.Match) == 0 || len(ff.Cookies.NoMatch) == 0) {
			t.Fatal("an entry with cookies loaded without cookies fixtures")
		}
	})
}

// readFixtures reads an entry's fixtures file from the embedded catalog.
func readFixtures(t testing.TB, id string) fixtureFile {
	t.Helper()
	b, err := fs.ReadFile(catalogFS, path.Join(catalogDir, id+".fixtures.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	var ff fixtureFile
	if _, err := decodeStrict(b, &ff); err != nil {
		t.Fatal(err)
	}
	return ff
}
