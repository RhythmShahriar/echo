// Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
// SPDX-License-Identifier: MIT

package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestInstallWordPressPluginForAHub(t *testing.T) {
	t.Chdir(t.TempDir())
	t.Setenv("RHYTHM_ECHO_TOKEN", "")
	site := t.TempDir()
	if err := os.MkdirAll(filepath.Join(site, "wp-content"), 0o750); err != nil {
		t.Fatal(err)
	}
	t.Setenv(writeTokenEnv, "")
	if code, _, errOut := runCLI(t, "install", "wordpress", site, "--plugin", "--hub", "https://hub.agency.test:8025", "--site", "client-a"); code != exitUsage ||
		!strings.Contains(errOut, "--hub needs the site's write token: set RHYTHM_ECHO_WRITE_TOKEN") {
		t.Errorf("--hub without a write token: %d %s", code, errOut)
	}
	t.Setenv(writeTokenEnv, "write-token-aaaa")
	for _, c := range []struct {
		args []string
		want string
	}{
		{[]string{"--hub", "https://hub.agency.test:8025"}, "--hub needs --site"},
		{[]string{"--hub", "https://hub.agency.test:8025", "--site", "a b"}, `--site "a b": use letters`},
		{[]string{"--hub", "https://hub.agency.test:8025", "--site", "client-a", "--url", "http://x"}, "pick --hub or --url"},
		{[]string{"--plugin", "--wp-cli"}, "--plugin writes Echo Staging Seal into a WordPress on disk"},
	} {
		if code, _, errOut := runCLI(t, append([]string{"install", "wordpress", site}, c.args...)...); code != exitUsage || !strings.Contains(errOut, c.want) {
			t.Errorf("%v: %d %s", c.args, code, errOut)
		}
	}
	code, out, errOut := runCLI(t, "install", "wordpress", site, "--plugin", "--hub", "https://hub.agency.test:8025", "--site", "client-a",
		"--bin", "/usr/local/bin/rhythm-echo", "--project", "/srv/client-a")
	if code != exitOK || !strings.Contains(out, "Installed Echo Staging Seal (wp-content/plugins/echo-staging-seal), reporting to https://hub.agency.test:8025.") ||
		!strings.Contains(out, "It names itself client-a in every capture.") || !strings.Contains(out, "wp plugin activate echo-staging-seal") {
		t.Fatalf("install --plugin: %d\n%s\n%s", code, out, errOut)
	}
	settings, err := os.ReadFile(filepath.Join(site, "wp-content", "mu-plugins", "rhythm-echo", "settings.php"))
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"https://hub.agency.test:8025", "client-a", "write-token-aaaa", "/srv/client-a"} {
		if !strings.Contains(string(settings), want) {
			t.Errorf("settings.php lacks %q:\n%s", want, settings)
		}
	}
	if _, err := os.Stat(filepath.Join(site, "wp-content", "plugins", "echo-staging-seal")); err != nil {
		t.Errorf("the plugin isn't there: %v", err)
	}
	if code, out, _ := runCLI(t, "install", "wordpress", site, "--plugin", "--uninstall"); code != exitOK || !strings.Contains(out, "Removed Echo Staging Seal") {
		t.Errorf("--plugin --uninstall: %d %s", code, out)
	}
	if code, out, _ := runCLI(t, "install", "wordpress", site, "--plugin", "--uninstall"); code != exitOK || !strings.Contains(out, "isn't installed there") {
		t.Errorf("--plugin --uninstall again: %d %s", code, out)
	}
}

func TestHelpListsTheSealFlags(t *testing.T) {
	_, help, _ := runCLI(t, "help")
	for _, f := range []string{"--plugin ", "--hub <url>", "--bin <path>", "--project <dir>"} {
		if !strings.Contains(help, f) {
			t.Errorf("help lacks %s", f)
		}
	}
	if _, out, _ := runCLI(t, "install", "--help"); !strings.Contains(out, "--plugin (Echo Staging Seal)") || !strings.Contains(out, "--hub URL --site ID") {
		t.Errorf("install --help:\n%s", out)
	}
}
