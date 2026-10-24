// Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
// SPDX-License-Identifier: MIT

package wordpress

import (
	"archive/zip"
	"bytes"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"testing"
)

// The zip WordPress.org and Plugins > Upload get carries the plugin and
// nothing else: no tests, no dotfiles, no development configs.
func TestPluginZipListsOnlyAllowedFiles(t *testing.T) {
	var buf bytes.Buffer
	if err := PluginZip(&buf); err != nil {
		t.Fatal(err)
	}
	z, err := zip.NewReader(bytes.NewReader(buf.Bytes()), int64(buf.Len()))
	if err != nil {
		t.Fatal(err)
	}
	var got []string
	files := map[string][]byte{}
	for _, f := range z.File {
		got = append(got, f.Name)
		r, err := f.Open()
		if err != nil {
			t.Fatal(err)
		}
		var b bytes.Buffer
		_, _ = b.ReadFrom(r)
		_ = r.Close()
		files[f.Name] = b.Bytes()
	}
	want := []string{
		"echo-staging-seal/echo-staging-seal.php",
		"echo-staging-seal/includes/class-echo-staging-seal-cli.php",
		"echo-staging-seal/includes/class-echo-staging-seal.php",
		"echo-staging-seal/includes/connector.php",
		"echo-staging-seal/languages/echo-staging-seal.pot",
		"echo-staging-seal/license.txt",
		"echo-staging-seal/readme.txt",
		"echo-staging-seal/uninstall.php",
	}
	sort.Strings(got)
	if strings.Join(got, "\n") != strings.Join(want, "\n") {
		t.Errorf("zip lists\n%s\nwant\n%s", strings.Join(got, "\n"), strings.Join(want, "\n"))
	}
	if !bytes.Equal(files["echo-staging-seal/includes/connector.php"], Plugin()) {
		t.Error("the plugin's connector is not echo.php")
	}

	// The same source gives the same zip.
	var again bytes.Buffer
	if err := PluginZip(&again); err != nil || !bytes.Equal(buf.Bytes(), again.Bytes()) {
		t.Errorf("a second build differs: %v", err)
	}

	// The readme's stable tag is the plugin's version, as WordPress.org
	// requires. Both declare the same license.
	version := regexp.MustCompile(`(?m)^ \* Version:\s+(\S+)`).FindSubmatch(files["echo-staging-seal/echo-staging-seal.php"])
	stable := regexp.MustCompile(`(?m)^Stable tag:\s+(\S+)`).FindSubmatch(files["echo-staging-seal/readme.txt"])
	if version == nil || stable == nil || string(version[1]) != string(stable[1]) {
		t.Errorf("version %q, stable tag %q", version, stable)
	}
	for _, name := range []string{"echo-staging-seal/echo-staging-seal.php", "echo-staging-seal/readme.txt"} {
		if !bytes.Contains(files[name], []byte("License URI: https://opensource.org/licenses/MIT")) && !bytes.Contains(files[name], []byte("License URI:       https://opensource.org/licenses/MIT")) {
			t.Errorf("%s has no license statement", name)
		}
	}
}

func TestInstallPlugin(t *testing.T) {
	dir := site(t)
	s := Settings{URL: "http://echo:8025", Token: "tok", Bin: "/usr/local/bin/rhythm-echo", Project: "/srv/shop"}
	if err := InstallPlugin(dir, s); err != nil {
		t.Fatal(err)
	}
	if !PluginInstalled(dir) {
		t.Fatal("PluginInstalled = false")
	}
	content := filepath.Join(dir, "wp-content")
	main, err := os.ReadFile(filepath.Join(content, PluginDir, "echo-staging-seal.php"))
	if err != nil || !bytes.Contains(main, []byte("Plugin Name:       Echo Staging Seal")) {
		t.Fatalf("main file: %v", err)
	}
	settings, err := os.ReadFile(filepath.Join(content, SettingsPath))
	if err != nil || !bytes.Contains(settings, []byte(`'bin' => '/usr/local/bin/rhythm-echo'`)) || !bytes.Contains(settings, []byte(`'project' => '/srv/shop'`)) {
		t.Fatalf("settings: %v\n%s", err, settings)
	}

	// The mu-plugin and the plugin share the settings file: removing one
	// keeps it for the other.
	if err := Install(dir, s); err != nil {
		t.Fatal(err)
	}
	if err := Uninstall(dir); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(content, SettingsPath)); err != nil {
		t.Errorf("uninstalling the mu-plugin took the plugin's settings: %v", err)
	}
	if err := UninstallPlugin(dir); err != nil {
		t.Fatal(err)
	}
	if PluginInstalled(dir) {
		t.Error("still installed")
	}
	if _, err := os.Stat(filepath.Join(content, SettingsPath)); !os.IsNotExist(err) {
		t.Errorf("settings left behind: %v", err)
	}
	if err := UninstallPlugin(dir); !IsNotInstalled(err) {
		t.Errorf("second uninstall: %v", err)
	}

	// Someone else's plugins/echo-staging-seal is left alone.
	other := filepath.Join(content, PluginDir)
	if err := os.MkdirAll(other, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(other, "echo-staging-seal.php"), []byte("<?php // someone else's\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := InstallPlugin(dir, Settings{}); err == nil {
		t.Error("overwrote a plugin folder Echo didn't write")
	}
	if err := UninstallPlugin(dir); err == nil || IsNotInstalled(err) {
		t.Errorf("removed a plugin folder Echo didn't write: %v", err)
	}
}

// PHP 7.4 and later must parse every plugin file.
func TestPluginPHPIsValid(t *testing.T) {
	php, err := exec.LookPath("php")
	if err != nil {
		t.Skip("php not installed")
	}
	files, err := PluginFiles()
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	for rel, b := range files {
		if !strings.HasSuffix(rel, ".php") {
			continue
		}
		p := filepath.Join(dir, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, b, 0o644); err != nil {
			t.Fatal(err)
		}
		if out, err := exec.Command(php, "-l", p).CombinedOutput(); err != nil {
			t.Errorf("%s: %s", rel, out)
		}
	}
}

// PHPStan at level max on the plugin's own code. See TestPHPStan.
func TestPluginPHPStan(t *testing.T) {
	phpstan := os.Getenv("ECHO_PHPSTAN")
	if phpstan == "" {
		t.Skip("set ECHO_PHPSTAN to a phpstan with the WordPress extension")
	}
	out, err := exec.Command(phpstan, "analyze", "--no-progress", "--memory-limit=1G", "-c", "phpstan-plugin.neon.dist").CombinedOutput()
	if err != nil {
		t.Fatalf("phpstan: %v\n%s", err, out)
	}
}

const pluginClass = `{"require":["plugin/echo-staging-seal/includes/class-echo-staging-seal.php"]}`

// Tools > Echo counts what Echo stopped by plugin and channel; sends that
// went out or failed are not counted.
func TestPluginStopped(t *testing.T) {
	msgs := []any{
		map[string]any{"status": "faked", "channel": "email", "source": map[string]any{"plugin": "woocommerce"}},
		map[string]any{"status": "faked", "channel": "email", "source": map[string]any{"plugin": "woocommerce"}},
		map[string]any{"status": "blocked", "channel": "email", "source": map[string]any{"plugin": "woocommerce"}},
		map[string]any{"status": "faked", "channel": "sms", "source": map[string]any{"plugin": "twilio-sms"}},
		map[string]any{"status": "sent", "channel": "sms", "source": map[string]any{"plugin": "twilio-sms"}},
		map[string]any{"status": "failed", "channel": "email", "source": map[string]any{"plugin": "woocommerce"}},
		map[string]any{"status": "blocked", "channel": "payment", "source": map[string]any{}},
		"not a message",
	}
	var got []struct {
		Plugin, Channel string
		Faked, Blocked  int
	}
	callPHP(t, pluginClass, "Echo_Staging_Seal::stopped", &got, msgs)
	want := "[{woocommerce email 2 1} { payment 0 1} {twilio-sms sms 1 0}]"
	if s := fmt.Sprint(got); s != want {
		t.Errorf("stopped = %s, want %s", s, want)
	}

	// On a hub, another site's messages are not this site's; ones without
	// a site (an older connector) still count.
	hub := []any{
		map[string]any{"status": "faked", "channel": "email", "source": map[string]any{"plugin": "woocommerce", "site": "client-a"}},
		map[string]any{"status": "faked", "channel": "email", "source": map[string]any{"plugin": "woocommerce", "site": "client-b"}},
		map[string]any{"status": "faked", "channel": "sms", "source": map[string]any{"plugin": "twilio-sms"}},
	}
	callPHP(t, pluginClass, "Echo_Staging_Seal::stopped", &got, hub, "client-a")
	if s := fmt.Sprint(got); s != "[{twilio-sms sms 1 0} {woocommerce email 1 0}]" {
		t.Errorf("stopped for client-a = %s", s)
	}
}

// The buttons and wp echo run rhythm-echo with these arguments. A
// snapshot name can never become a flag.
func TestPluginArgv(t *testing.T) {
	setup := `{"getenv":{"RHYTHM_ECHO_BIN":"/opt/rhythm-echo"},"require":["plugin/echo-staging-seal/includes/class-echo-staging-seal.php"]}`
	for _, tc := range []struct {
		command, name string
		want          []string
	}{
		{"snapshot", "before-updates", []string{"/opt/rhythm-echo", "snapshot", "--name", "before-updates"}},
		{"compare", "", []string{"/opt/rhythm-echo", "compare"}},
		{"compare", "v1.2_rc", []string{"/opt/rhythm-echo", "compare", "--snapshot", "v1.2_rc"}},
		{"snapshots", "", []string{"/opt/rhythm-echo", "snapshots"}},
		{"snapshot", "", nil},
		{"snapshot", "--force", nil},
		{"snapshot", "a b", nil},
		{"snapshot", "../x", nil},
		{"delete", "", nil},
	} {
		var got []string
		callPHP(t, setup, "Echo_Staging_Seal::argv", &got, tc.command, tc.name)
		if strings.Join(got, " ") != strings.Join(tc.want, " ") || (got == nil) != (tc.want == nil) {
			t.Errorf("argv(%q, %q) = %q, want %q", tc.command, tc.name, got, tc.want)
		}
	}
}
