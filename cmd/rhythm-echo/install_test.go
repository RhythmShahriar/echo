// Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
// SPDX-License-Identifier: MIT

package main

import (
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/RhythmShahriar/echo/catcher"
	"github.com/RhythmShahriar/echo/config"
)

func TestInstallWordPressOnDisk(t *testing.T) {
	site := t.TempDir()
	if err := os.MkdirAll(filepath.Join(site, "wp-content"), 0o750); err != nil {
		t.Fatal(err)
	}
	_ = os.WriteFile(filepath.Join(site, "wp-config.php"), []byte("<?php\n"), 0o600)
	code, out, errOut := runCLI(t, "install", "wordpress", site, "--url", "http://127.0.0.1:8025", "--token", "t0k3n")
	if code != 0 || !strings.Contains(out, "Installed the Echo connector") {
		t.Fatalf("install: %d\n%s\n%s", code, out, errOut)
	}
	if strings.Contains(out, "t0k3n") {
		t.Error("the token must never be printed")
	}
	if _, err := os.Stat(filepath.Join(site, "wp-content", "mu-plugins", "rhythm-echo.php")); err != nil {
		t.Fatal(err)
	}
	if code, out, _ := runCLI(t, "install", "wordpress", site, "--uninstall"); code != 0 || !strings.Contains(out, "Removed") {
		t.Errorf("uninstall: %d %s", code, out)
	}
}

func TestInstallInstructions(t *testing.T) {
	for stack, want := range map[string]string{"laravel": "rhythmsh/echo-laravel", "node": "@rhythmsh/echo/register", "generic": "sendmail_path"} {
		if code, out, _ := runCLI(t, "install", stack); code != 0 || !strings.Contains(out, want) {
			t.Errorf("%s: %d\n%s", stack, code, out)
		}
	}
	if code, _, _ := runCLI(t, "install", "cobol"); code != exitUsage {
		t.Error("unknown stack")
	}
	dir := t.TempDir()
	_ = os.WriteFile(filepath.Join(dir, "package.json"), []byte("{}"), 0o600)
	if code, out, _ := runCLI(t, "install", "next", dir); code != 0 || !strings.Contains(out, "Wrote instrumentation.ts") {
		t.Errorf("next: %d\n%s", code, out)
	}
	if b, _ := os.ReadFile(filepath.Join(dir, "instrumentation.ts")); !strings.Contains(string(b), "await register();") {
		t.Errorf("register() is async and must be awaited:\n%s", b)
	}
	if code, out, _ := runCLI(t, "install", "next", dir); code != 0 || !strings.Contains(out, "already starts Echo's connector") {
		t.Errorf("next again: %d\n%s", code, out)
	}
	// Someone else's instrumentation file gets the lines to add, awaited.
	_ = os.WriteFile(filepath.Join(dir, "instrumentation.ts"), []byte("export async function register() {}\n"), 0o600)
	if code, out, _ := runCLI(t, "install", "next", dir); code != 0 || !strings.Contains(out, "add this inside its register()") ||
		!strings.Contains(out, "await registerEcho();") {
		t.Errorf("next with an existing file: %d\n%s", code, out)
	}
}

func TestInstallUsageAndConfig(t *testing.T) {
	t.Chdir(t.TempDir())
	for _, args := range [][]string{
		{"install", "wordpress", t.TempDir()},            // not WordPress
		{"install", "wordpress", "--mode", "bogus", "."}, // bad mode
		{"install"},                            // no stack
		{"install", "laravel", "--frobnicate"}, // unknown flag
	} {
		if code, _, errOut := runCLI(t, args...); code != exitUsage {
			t.Errorf("%v: exit %d, want %d\n%s", args, code, exitUsage, errOut)
		}
	}
	_ = os.MkdirAll(config.Dir, 0o750)
	_ = os.WriteFile(config.Path(), []byte("listen: 127.0.0.1:9125\nsmtp: 127.0.0.1:9126\n"), 0o600)
	t.Setenv("RHYTHM_ECHO_URL", "")
	if code, out, _ := runCLI(t, "install", "generic"); code != 0 || !strings.Contains(out, "port 9126") || !strings.Contains(out, "rhythm-echo") {
		t.Errorf("generic ignores the config: %d\n%s", code, out)
	}
	if code, out, _ := runCLI(t, "install", "laravel"); code != 0 || !strings.Contains(out, "RHYTHM_ECHO_URL=http://127.0.0.1:9125") ||
		strings.Contains(out, "require --dev") {
		t.Errorf("laravel: %d\n%s", code, out)
	}
}

func TestInstallWordPressEnvironmentHint(t *testing.T) {
	site := t.TempDir()
	_ = os.MkdirAll(filepath.Join(site, "wp-content"), 0o750)
	_ = os.WriteFile(filepath.Join(site, "wp-config.php"), []byte("<?php\n"), 0o600)
	_, out, _ := runCLI(t, "install", "wordpress", site)
	if !strings.Contains(out, "define( 'WP_ENVIRONMENT_TYPE', 'staging' );") || !strings.Contains(out, "unset WP_ENVIRONMENT_TYPE as production") {
		t.Errorf("no environment hint:\n%s", out)
	}
	_ = os.WriteFile(filepath.Join(site, "wp-config.php"), []byte("<?php\ndefine( 'WP_ENVIRONMENT_TYPE', 'staging' );\n"), 0o600)
	if _, out, _ := runCLI(t, "install", "wordpress", site); !strings.Contains(out, "sets WP_ENVIRONMENT_TYPE to staging") {
		t.Errorf("staging site:\n%s", out)
	}
}

func TestDoctorChecksEchoAndSMTP(t *testing.T) {
	t.Chdir(t.TempDir())
	s := catcher.NewServer(catcher.NewInbox(100), catcher.Config{Version: "test"})
	api := httptest.NewServer(s)
	defer api.Close()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	sink := catcher.NewSMTP(s, ln.Addr().String())
	go func() { _ = sink.Serve(ln) }()
	defer func() { _ = sink.Close() }()
	_ = os.MkdirAll(config.Dir, 0o750)
	_ = os.WriteFile(config.Path(), []byte("smtp: "+ln.Addr().String()+"\n"), 0o600)
	code, out, errOut := runCLI(t, "doctor", "--url", api.URL, "--plain")
	if code != 0 || !strings.Contains(out, "ok    SMTP: ") || strings.ContainsAny(out, "✓✗·") || !strings.Contains(out, "0 failed") {
		t.Fatalf("doctor: %d\n%s\n%s", code, out, errOut)
	}
	if code, out, _ := runCLI(t, "doctor", "--url", "http://127.0.0.1:1", "--plain"); code != exitFindings || !strings.Contains(out, "FAIL  Echo: ") {
		t.Errorf("doctor without Echo: %d\n%s", code, out)
	}
}

func TestDoctorFailsOnAProductionSite(t *testing.T) {
	t.Chdir(t.TempDir())
	api := httptest.NewServer(catcher.NewServer(catcher.NewInbox(100), catcher.Config{Version: "test"}))
	defer api.Close()
	var hits atomic.Int32
	shop := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { hits.Add(1) }))
	defer shop.Close()
	_ = os.MkdirAll(config.Dir, 0o750)
	write := func(prod string) {
		_ = os.WriteFile(config.Path(), []byte("smtp: off\nsite: "+shop.URL+"\nproduction:\n  - "+prod+"\n"), 0o600)
	}
	write(shop.URL)
	code, out, errOut := runCLI(t, "doctor", "--url", api.URL, "--plain")
	want := "FAIL  Site: " + shop.URL + " is listed under production: (" + shop.URL + "). http actions to it are refused, " +
		"but command actions still run against it"
	if code != exitFindings || !strings.Contains(out, want) {
		t.Fatalf("doctor: %d\n%s\n%s", code, out, errOut)
	}
	if strings.Contains(out, "replays refuse") || hits.Load() != 0 {
		t.Errorf("doctor promised a replay refusal or sent canaries to a live site (%d requests):\n%s", hits.Load(), out)
	}
	// A listing actions wouldn't match is still a live site, said plainly.
	write(strings.ToUpper(shop.URL))
	if code, out, _ := runCLI(t, "doctor", "--url", api.URL, "--plain"); code != exitFindings ||
		!strings.Contains(out, "looks like production: entry "+strings.ToUpper(shop.URL)+", but actions match an entry exactly as written") {
		t.Errorf("case variant: %d\n%s", code, out)
	}
	// Another site in the list is no reason to fail.
	write("https://shop.example")
	if _, out, _ := runCLI(t, "doctor", "--url", api.URL, "--plain"); strings.Contains(out, "FAIL  Site") || hits.Load() == 0 {
		t.Errorf("staging site:\n%s", out)
	}
}

func TestProductionReason(t *testing.T) {
	cfg := config.Config{Production: []string{"", "https://shop.example"}}
	for site, want := range map[string]string{
		"https://shop.example":              "is listed under production: (https://shop.example)",
		"https://shop.example/":             "is listed under production:",
		"https://shop.example/shop":         "is listed under production:",
		"HTTPS://SHOP.example":              "looks like production: entry https://shop.example",
		"http://www.shop.example:8443":      "looks like production: entry",
		"https://shop.example.staging.test": "is another host, but it starts with production: entry https://shop.example",
		"https://staging.example":           "",
		"":                                  "",
	} {
		got, live := productionReason(cfg, site)
		if (want == "") != (got == "") || !strings.Contains(got, want) {
			t.Errorf("%q: %q, want %q", site, got, want)
		}
		if accident := strings.Contains(want, "another host"); got != "" && live == accident {
			t.Errorf("%q: live %v", site, live)
		}
	}
	slash := config.Config{Production: []string{"https://shop.example/"}}
	if got, live := productionReason(slash, "https://shop.example"); !live || !strings.Contains(got, "is listed") {
		t.Errorf("an entry with a / still refuses site's actions: %q", got)
	}
	if got, _ := productionReason(slash, "https://shop.example.staging.test"); got != "" {
		t.Errorf("a / ends the match: %q", got)
	}
}

func TestInstallGenericNamesAnOtherSink(t *testing.T) {
	t.Chdir(t.TempDir())
	if code, out, _ := runCLI(t, "install", "generic"); code != 0 || strings.Contains(out, "RHYTHM_ECHO_SMTP") {
		t.Errorf("the default sink needs no variable: %d\n%s", code, out)
	}
	cfg := filepath.Join(t.TempDir(), "echo.yaml")
	_ = os.WriteFile(cfg, []byte("smtp: 127.0.0.1:2525\n"), 0o600)
	code, out, errOut := runCLI(t, "install", "generic", "--config", cfg)
	if code != 0 {
		t.Fatalf("%d %s", code, errOut)
	}
	for _, want := range []string{
		`sendmail_path = "RHYTHM_ECHO_SMTP=127.0.0.1:2525 ` + binaryPath() + ` sendmail -t -i"`,
		"Mail (scripts):   RHYTHM_ECHO_SMTP=127.0.0.1:2525",
		"host 127.0.0.1, port 2525",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("missing %q:\n%s", want, out)
		}
	}
	win := sendmailLines("127.0.0.1:2525", `C:\echo\rhythm-echo.exe`, "windows", false)
	if strings.Contains(win, "RHYTHM_ECHO_SMTP=127.0.0.1:2525 C:") || !strings.Contains(win, "set RHYTHM_ECHO_SMTP=127.0.0.1:2525 in the environment") {
		t.Errorf("windows has no sh -c, so no prefix:\n%s", win)
	}
	if remote := sendmailLines("10.0.0.5:1025", "/bin/rhythm-echo", "linux", true); !strings.Contains(remote, "set RHYTHM_ECHO_TOKEN") {
		t.Errorf("a sink beyond loopback asks for the token:\n%s", remote)
	}
}
