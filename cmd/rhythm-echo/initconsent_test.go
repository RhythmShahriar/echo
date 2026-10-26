// Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
// SPDX-License-Identifier: MIT

package main

import (
	"flag"
	"os"
	"path/filepath"
	"strings"
	"testing"

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
	goldenFile(t, "init-consent-appended.yaml", string(data))
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
	goldenFile(t, "init-consent-fresh.yaml", string(data))
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
	// --force replaces an existing config with only the block.
	writeConfig(t, "site: https://old.test\n")
	if code, _, _ := runCLI(t, "init", "--consent", "--force"); code != exitOK {
		t.Errorf("init --consent --force: %d", code)
	}
	if after, _ := os.ReadFile(config.Path()); string(after) != string(data) {
		t.Error("--force didn't write the fresh block")
	}
}
