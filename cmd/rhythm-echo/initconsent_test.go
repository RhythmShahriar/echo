// Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
// SPDX-License-Identifier: MIT

package main

import (
	"flag"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"gopkg.in/yaml.v3"

	"github.com/RhythmShahriar/echo/config"
)

var updateGolden = flag.Bool("update", false, "rewrite the golden files in testdata")

// goldenFile compares got with testdata/<name>, or rewrites it with
// -update. Line ends are normalised, so a CRLF checkout passes.
func goldenFile(t *testing.T, name, got string) {
	t.Helper()
	path := filepath.Join(goldenDir, name)
	if *updateGolden {
		if err := os.MkdirAll(goldenDir, 0o750); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(got), 0o600); err != nil {
			t.Fatal(err)
		}
		return
	}
	want, err := os.ReadFile(path) //nolint:gosec // a test's own golden file
	if err != nil {
		t.Fatalf("%v (go test -update writes it)", err)
	}
	if w := strings.ReplaceAll(string(want), "\r\n", "\n"); w != got {
		t.Errorf("%s differs from the golden file (go test -update rewrites it)\ngot:\n%s\nwant:\n%s", name, got, w)
	}
}

// goldenDir is the package's testdata folder, found before a test
// changes the working folder.
var goldenDir = func() string {
	wd, err := os.Getwd()
	if err != nil {
		panic(err)
	}
	return filepath.Join(wd, "testdata")
}()

// withoutUntil puts the {{until}} placeholder back for the ignore
// example's date, which init --consent sets to 30 days from today; it
// fails when that date isn't there.
func withoutUntil(t *testing.T, data string) string {
	t.Helper()
	day := time.Now().AddDate(0, 0, 30).Format(time.DateOnly)
	if !strings.Contains(data, "#     until: "+day+"\n") {
		// The test may have run across midnight: yesterday's +30 is fine.
		day = time.Now().AddDate(0, 0, 29).Format(time.DateOnly)
	}
	if !strings.Contains(data, "#     until: "+day+"\n") {
		t.Fatalf("no until: 30 days from today in\n%s", data)
	}
	return strings.Replace(data, day, "{{until}}", 1)
}

// The ignore example's date is 30 days after the day init runs, never
// a fixed date that would one day be in the past.
func TestConsentBlockUntil(t *testing.T) {
	got := consentBlockAt(time.Date(2027, 12, 15, 23, 0, 0, 0, time.UTC))
	if !strings.Contains(got, "#     until: 2028-01-14\n") || strings.Contains(got, "{{until}}") {
		t.Errorf("until:\n%s", got)
	}
	if strings.Contains(consentBlockAt(time.Now()), "2026-11-30") {
		t.Error("a fixed date is left in the block")
	}
}

// init --consent appends the commented block to an existing config,
// which still loads; a second one is refused.
func TestInitConsentAppends(t *testing.T) {
	t.Chdir(t.TempDir())
	writeConfig(t, "site: https://staging.shop.test\nproduction: [shop.example.com]")
	code, out, errOut := runCLI(t, "init", "--consent")
	if code != exitOK || errOut != "" || out != "Added a consent: block to .echo/config.yaml; uncomment a key to change its default.\n"+
		"Then: rhythm echo consent https://staging.shop.test (or list pages under consent: urls).\n" {
		t.Fatalf("init --consent: %d %q %q", code, out, errOut)
	}
	data, err := os.ReadFile(config.Path())
	if err != nil {
		t.Fatal(err)
	}
	goldenFile(t, "init-consent-appended.yaml", withoutUntil(t, string(data)))
	if c, err := config.Load(config.Path()); err != nil || c.Site != "https://staging.shop.test" {
		t.Errorf("the config doesn't load after init --consent: %v", err)
	}
	code, _, errOut = runCLI(t, "init", "--consent")
	if code != exitUsage || errOut != "rhythm echo: .echo/config.yaml already has consent:\n" {
		t.Errorf("a second init --consent: %d %q", code, errOut)
	}
	if after, _ := os.ReadFile(config.Path()); string(after) != string(data) {
		t.Error("the refused init --consent changed the config")
	}
	// A real consent: block counts too.
	writeConfig(t, "consent:\n  urls: [https://shop.test/]\n")
	if code, _, _ := runCLI(t, "init", "--consent"); code != exitUsage {
		t.Errorf("init --consent over a consent: block: %d", code)
	}
	if code, _, errOut := runCLI(t, "init", "--consent", "--stack", "wordpress"); code != exitUsage ||
		errOut != "rhythm echo: --consent writes only the consent: block; leave out --stack\n" {
		t.Errorf("--consent --stack: %d %q", code, errOut)
	}
}

// Without a config, init --consent writes one with only the block.
func TestInitConsentFresh(t *testing.T) {
	t.Chdir(t.TempDir())
	code, out, _ := runCLI(t, "init", "--consent")
	if code != exitOK || !strings.HasPrefix(out, "Wrote .echo/config.yaml with a consent: block") {
		t.Fatalf("init --consent: %d %q", code, out)
	}
	data, err := os.ReadFile(config.Path())
	if err != nil {
		t.Fatal(err)
	}
	goldenFile(t, "init-consent-fresh.yaml", withoutUntil(t, string(data)))
	c, err := config.Load(config.Path())
	if err != nil || c.Consent != nil || len(c.Actions) != 0 {
		t.Errorf("the fresh config: %+v %v", c.Consent, err)
	}
	// Every key of the block is one the config takes: uncommented,
	// the example values load.
	var kept []string
	for _, l := range strings.Split(string(data), "\n") {
		if rest, ok := strings.CutPrefix(l, "  # "); ok {
			l = "  " + rest
		}
		if strings.HasPrefix(l, "  urls:") {
			l = "  urls: [https://staging.shop.test/]"
		}
		kept = append(kept, l)
	}
	writeConfig(t, strings.Join(kept, "\n"))
	if _, err := config.Load(config.Path()); err != nil {
		t.Errorf("the block with every key uncommented doesn't load: %v\n%s", err, strings.Join(kept, "\n"))
	}
	// --force over a config without a consent: block appends it.
	writeConfig(t, "site: https://old.test\n")
	if code, _, _ := runCLI(t, "init", "--consent", "--force"); code != exitOK {
		t.Errorf("init --consent --force: %d", code)
	}
	if after, _ := os.ReadFile(config.Path()); !strings.HasPrefix(string(after), "site: https://old.test\n\n# Cookie consent checks") {
		t.Errorf("--force without a consent: block didn't append it:\n%s", after)
	}
}

// --force with --consent replaces only the consent: block: the actions,
// the site and the comments of the other keys stay.
func TestInitConsentForceKeepsRest(t *testing.T) {
	t.Chdir(t.TempDir())
	rest := "site: https://staging.shop.test\n" +
		"actions:\n  - name: contact\n    http: {method: POST, url: /contact}\n"
	tail := "# where reports go\nproduction: [shop.example.com]\n"
	writeConfig(t, rest+"# my old consent settings\nconsent:\n  runs: [none, accept]\n  geo: fr\n\n"+tail)
	code, out, errOut := runCLI(t, "init", "--consent", "--force")
	if code != exitOK || errOut != "" || !strings.HasPrefix(out, "Replaced the consent: block in .echo/config.yaml; the rest of it is unchanged.\n") {
		t.Fatalf("init --consent --force: %d %q %q", code, out, errOut)
	}
	data, err := os.ReadFile(config.Path())
	if err != nil {
		t.Fatal(err)
	}
	got := string(data)
	if !strings.HasPrefix(got, rest+"\n"+tail+"\n# Cookie consent checks") || strings.Contains(got, "geo: fr") ||
		strings.Contains(got, "my old consent") || strings.Count(got, "\nconsent:") != 1 {
		t.Errorf("the config after --force:\n%s", got)
	}
	c, err := config.Load(config.Path())
	if err != nil || len(c.Actions) != 1 || c.Site != "https://staging.shop.test" || c.Consent != nil {
		t.Fatalf("the config doesn't keep its actions: %+v %v", c, err)
	}
	// Twice gives the same file: the generated block replaces itself.
	if code, _, _ := runCLI(t, "init", "--consent", "--force"); code != exitOK {
		t.Fatalf("a second --force: %d", code)
	}
	if again, _ := os.ReadFile(config.Path()); string(again) != got {
		t.Errorf("a second --force changed the file:\n%s\nwas\n%s", again, got)
	}
	// A config with only the block is written fresh.
	writeConfig(t, "consent:\n  geo: fr\n")
	if code, out, _ := runCLI(t, "init", "--consent", "--force"); code != exitOK || !strings.HasPrefix(out, "Wrote .echo/config.yaml with a consent: block") {
		t.Errorf("--force over a lone block: %d %q", code, out)
	}
	// A config that isn't YAML is never replaced.
	writeConfig(t, "site: [unclosed\n")
	if code, _, errOut := runCLI(t, "init", "--consent", "--force"); code != exitError || !strings.HasPrefix(errOut, "rhythm echo: .echo/config.yaml: ") {
		t.Errorf("--force over broken YAML: %d %q", code, errOut)
	}
	if after, _ := os.ReadFile(config.Path()); string(after) != "site: [unclosed\n" {
		t.Error("--force changed a broken config")
	}
}

// FuzzCutTopKey: cutting consent: from any YAML never panics and leaves
// a document without that key.
func FuzzCutTopKey(f *testing.F) {
	f.Add("site: a\nconsent:\n  geo: fr\nproduction: [b]\n")
	f.Add("# head\nconsent: {geo: fr}\n# tail\nsite: a\n")
	f.Add("consent:\n")
	f.Add("site: a\n\n# c\nconsent:\n  # x\n\n# y\nquiet: 2s")
	f.Fuzz(func(t *testing.T, data string) {
		var root yaml.Node
		if yaml.Unmarshal([]byte(data), &root) != nil || !hasKey(&root, "consent") || root.Content[0].Style&yaml.FlowStyle != 0 {
			return
		}
		out := cutTopKey([]byte(data), &root, "consent")
		var after yaml.Node
		if err := yaml.Unmarshal(out, &after); err != nil {
			// Cutting lines out of an exotic document (anchors used
			// below, multi-line keys) may break it; initConsent loads
			// the result before writing, so that is caught there.
			return
		}
		if hasKey(&after, "consent") && strings.Count(data, "consent") < 2 {
			t.Errorf("consent: is still there:\n%s", out)
		}
	})
}
