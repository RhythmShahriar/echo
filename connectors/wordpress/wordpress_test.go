// Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
// SPDX-License-Identifier: MIT

package wordpress

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func site(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, "wp-content", "plugins"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "wp-config.php"), []byte("<?php\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	return dir
}

func TestInstallWritesPluginAndSettings(t *testing.T) {
	dir := site(t)
	s := Settings{URL: "http://host.docker.internal:8025", Token: `t'o\k`, Mode: "seal", Force: true,
		Proxy: "http://127.0.0.1:8026", CA: "/etc/echo/ca.pem"}
	if err := Install(dir, s); err != nil {
		t.Fatal(err)
	}
	got, err := os.ReadFile(filepath.Join(dir, "wp-content", PluginPath))
	if err != nil || string(got) != string(Plugin()) {
		t.Fatalf("plugin not written: %v", err)
	}
	if fi, _ := os.Stat(filepath.Join(dir, "wp-content", PluginPath)); fi.Mode().Perm() != 0o644 {
		t.Errorf("plugin mode = %v, want 0644 so the web server can read it", fi.Mode().Perm())
	}
	settings := filepath.Join(dir, "wp-content", SettingsPath)
	if !Installed(dir) {
		t.Error("Installed = false")
	}
	// The settings file must be PHP that returns exactly s.
	if php, err := exec.LookPath("php"); err == nil {
		out, err := exec.Command(php, "-r", `echo json_encode(include $argv[1]);`, settings).CombinedOutput()
		if err != nil {
			t.Fatalf("php: %v\n%s", err, out)
		}
		var m map[string]any
		if err := json.Unmarshal(out, &m); err != nil {
			t.Fatalf("settings: %s", out)
		}
		if m["url"] != s.URL || m["token"] != s.Token || m["mode"] != "seal" || m["force"] != true ||
			m["proxy"] != s.Proxy || m["ca"] != s.CA {
			t.Errorf("settings = %v", m)
		}
	}

	// Reinstalling with no settings drops the file.
	if err := Install(filepath.Join(dir, "wp-content"), Settings{}); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(settings); !os.IsNotExist(err) {
		t.Errorf("settings left behind: %v", err)
	}

	if err := Uninstall(dir); err != nil {
		t.Fatal(err)
	}
	if Installed(dir) {
		t.Error("still installed")
	}
	if err := Uninstall(dir); !IsNotInstalled(err) {
		t.Errorf("second uninstall: %v", err)
	}
}

func TestInstallLeavesForeignFilesAlone(t *testing.T) {
	dir := site(t)
	path := filepath.Join(dir, "wp-content", PluginPath)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("<?php // someone else's\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := Install(dir, Settings{}); err == nil {
		t.Error("overwrote a file Echo didn't write")
	}
	if err := Uninstall(dir); err == nil {
		t.Error("removed a file Echo didn't write")
	}
}

func TestContentDir(t *testing.T) {
	if _, err := ContentDir(t.TempDir()); err == nil {
		t.Error("an empty directory isn't WordPress")
	}
	dir := site(t)
	for _, d := range []string{dir, filepath.Join(dir, "wp-content")} {
		got, err := ContentDir(d)
		if err != nil || got != filepath.Join(dir, "wp-content") {
			t.Errorf("ContentDir(%s) = %s, %v", d, got, err)
		}
	}
}

func TestSettingsRejectUnknownMode(t *testing.T) {
	if _, err := SettingsPHP(Settings{Mode: "loud"}); err == nil {
		t.Error("accepted mode loud")
	}
	if err := Install(site(t), Settings{Mode: "loud"}); err == nil {
		t.Error("installed with mode loud")
	}
}

func TestPHPIsValid(t *testing.T) {
	php, err := exec.LookPath("php")
	if err != nil {
		t.Skip("php not installed")
	}
	dir := t.TempDir()
	script, err := Script(Settings{URL: "http://echo:8025", Token: "abc"})
	if err != nil {
		t.Fatal(err)
	}
	for name, src := range map[string]string{"echo.php": string(Plugin()), "install.php": script, "uninstall.php": UninstallScript()} {
		p := filepath.Join(dir, name)
		if err := os.WriteFile(p, []byte(src), 0o644); err != nil {
			t.Fatal(err)
		}
		if out, err := exec.Command(php, "-l", p).CombinedOutput(); err != nil {
			t.Errorf("%s: %s", name, out)
		}
	}
}

// The install script, run against a fake WordPress, writes both files.
func TestScriptInstalls(t *testing.T) {
	php, err := exec.LookPath("php")
	if err != nil {
		t.Skip("php not installed")
	}
	dir := t.TempDir()
	mu := filepath.Join(dir, "mu-plugins")
	script, err := Script(Settings{URL: "http://echo:8025"})
	if err != nil {
		t.Fatal(err)
	}
	// Strip "<?php" and run the body with WPMU_PLUGIN_DIR defined, as WP-CLI would.
	body := strings.TrimPrefix(script, "<?php\n")
	code := "define('WPMU_PLUGIN_DIR', $argv[1]);\n" + body
	if out, err := exec.Command(php, "-r", code, mu).CombinedOutput(); err != nil {
		t.Fatalf("%v: %s", err, out)
	}
	got, err := os.ReadFile(filepath.Join(mu, "rhythm-echo.php"))
	if err != nil || string(got) != string(Plugin()) {
		t.Fatalf("plugin: %v", err)
	}
	if b, err := os.ReadFile(filepath.Join(mu, "rhythm-echo", "settings.php")); err != nil || !strings.Contains(string(b), "http://echo:8025") {
		t.Fatalf("settings: %v %s", err, b)
	}
	un := strings.TrimPrefix(UninstallScript(), "<?php\n")
	if out, err := exec.Command(php, "-r", "define('WPMU_PLUGIN_DIR', $argv[1]);\n"+un, mu).CombinedOutput(); err != nil {
		t.Fatalf("%v: %s", err, out)
	}
	if _, err := os.Stat(filepath.Join(mu, "rhythm-echo.php")); !os.IsNotExist(err) {
		t.Error("uninstall script left the plugin")
	}
}

// The REST routes doctor uses exist only while the connector is active and
// open only to the token, an administrator or a direct loopback call when
// no token is set. Checked with the plugin loaded in a faked WordPress.
func TestRESTRoutesLocked(t *testing.T) {
	php, err := exec.LookPath("php")
	if err != nil {
		t.Skip("php not installed")
	}
	const ok, forbidden = "true", `{"code":"rhythm_echo_forbidden","status":403}`
	for _, tc := range []struct {
		name, env, token, addr, admin, headers string
		routes                                 bool
		want                                   string
	}{
		{"production has no routes", "production", "tok", "127.0.0.1", "0", `{}`, false, forbidden},
		{"token", "staging", "tok", "172.17.0.1", "0", `{"X-Echo-Token":"tok"}`, true, ok},
		{"no token from the Docker gateway", "staging", "tok", "172.17.0.1", "0", `{}`, true, forbidden},
		{"wrong token", "staging", "tok", "172.17.0.1", "0", `{"X-Echo-Token":"nope"}`, true, forbidden},
		{"loopback is not enough with a token set", "staging", "tok", "127.0.0.1", "0", `{}`, true, forbidden},
		{"administrator", "staging", "tok", "203.0.113.9", "1", `{}`, true, ok},
		{"no token: loopback", "development", "", "127.0.0.1", "0", `{}`, true, ok},
		{"no token: IPv6 loopback", "development", "", "::1", "0", `{}`, true, ok},
		{"no token: private address", "development", "", "172.17.0.1", "0", `{}`, true, forbidden},
		{"no token: 10/8", "local", "", "10.0.0.5", "0", `{}`, true, forbidden},
		{"no token: loopback behind a proxy", "development", "", "127.0.0.1", "0", `{"X-Forwarded-For":"198.51.100.7"}`, true, forbidden},
		{"no token: loopback with Forwarded", "development", "", "127.0.0.1", "0", `{"Forwarded":"for=198.51.100.7"}`, true, forbidden},
		{"no token: administrator", "development", "", "198.51.100.7", "1", `{}`, true, ok},
	} {
		t.Run(tc.name, func(t *testing.T) {
			out, err := exec.Command(php, "testdata/rest-harness.php", "echo.php", tc.env, tc.token, tc.addr, tc.admin, tc.headers).CombinedOutput()
			if err != nil {
				t.Fatalf("%v: %s", err, out)
			}
			var got struct {
				Routes  bool            `json:"routes"`
				Allowed json.RawMessage `json:"allowed"`
			}
			if err := json.Unmarshal(out, &got); err != nil {
				t.Fatalf("not JSON: %s", out)
			}
			if got.Routes != tc.routes || string(got.Allowed) != tc.want {
				t.Errorf("routes %v, allowed %s; want routes %v, allowed %s", got.Routes, got.Allowed, tc.routes, tc.want)
			}
		})
	}
}

// PHPStan at level 6 with the WordPress stubs. It needs a phpstan whose
// vendor directory has szepeviktor/phpstan-wordpress and
// phpstan/extension-installer, named by ECHO_PHPSTAN:
//
//	ECHO_PHPSTAN=/path/to/vendor/bin/phpstan go test -run PHPStan ./connectors/wordpress/
func TestPHPStan(t *testing.T) {
	phpstan := os.Getenv("ECHO_PHPSTAN")
	if phpstan == "" {
		t.Skip("set ECHO_PHPSTAN to a phpstan with the WordPress extension")
	}
	out, err := exec.Command(phpstan, "analyze", "--no-progress", "--memory-limit=1G", "-c", "phpstan.neon.dist").CombinedOutput()
	if err != nil {
		t.Fatalf("phpstan: %v\n%s", err, out)
	}
}

// callPHP runs one Rhythm_Echo method through testdata/call-harness.php,
// decodes its JSON result into out and returns what PHP logged.
func callPHP(t *testing.T, setup, method string, out any, args ...any) (logged string) {
	t.Helper()
	php, err := exec.LookPath("php")
	if err != nil {
		t.Skip("php not installed")
	}
	if args == nil {
		args = []any{}
	}
	a, err := json.Marshal(args)
	if err != nil {
		t.Fatal(err)
	}
	var stderr strings.Builder
	cmd := exec.Command(php, "testdata/call-harness.php", "echo.php", setup, method, string(a))
	cmd.Stderr = &stderr
	b, err := cmd.Output()
	if err != nil {
		t.Fatalf("%s: %v\n%s%s", method, err, b, stderr.String())
	}
	if err := json.Unmarshal(b, out); err != nil {
		t.Fatalf("%s: not JSON: %s", method, b)
	}
	return stderr.String()
}

func TestMaskHeader(t *testing.T) {
	const secret = "Zq9TOPSECRETvalue77abcdefghijkl"
	basic := func(s string) string { return "Basic " + base64.StdEncoding.EncodeToString([]byte(s)) }
	for _, tc := range []struct{ name, value, want string }{
		{"X-Auth-Token", secret, "•••••"},
		{"x-amz-security-token", secret, "•••••"},
		{"Private-Token", secret, "•••••"},
		{"X-Goog-Api-Key", secret, "•••••"},
		{"X-Shopify-Access-Token", secret, "•••••"},
		{"X-WC-Webhook-Signature", secret, "•••••"},
		{"X-Session-Id", secret, "•••••"},
		{"X-Db-Password", secret, "•••••"},
		{"Cookie", "sid=" + secret, "•••••"},
		{"X-Client-Credential", secret, "•••••"},
		{"X-Auth-Token", "", ""},
		{"Authorization", "Bearer " + secret, "Bearer •••••"},
		{"Authorization", "Bearer sk_test_" + secret, "Bearer sk_test_•••••"},
		{"Authorization", basic("sk_live_" + secret + ":"), "Basic sk_live_•••••"},
		{"Authorization", basic("user:" + secret), "Basic •••••"},
		{"Proxy-Authorization", secret, "•••••"},
		// negative fixtures: kept as they are
		{"X-Request-Id", "req-1234567890abcdef", "req-1234567890abcdef"},
		{"Content-Type", "application/json", "application/json"},
		{"X-Echo-Run", "r1/checkout.NhOp3HY0JQ76wSoPnn3sZg", "r1/checkout.NhOp3HY0JQ76wSoPnn3sZg"},
	} {
		var got string
		callPHP(t, `{}`, "mask_header", &got, tc.name, tc.value)
		if got != tc.want {
			t.Errorf("mask_header(%q, %q) = %q, want %q", tc.name, tc.value, got, tc.want)
		}
		// Beyond the scheme and a Stripe mode prefix, at most 4 characters show.
		shown := strings.ReplaceAll(got, "•", "")
		for _, p := range []string{"Bearer ", "Basic ", "sk_test_", "sk_live_"} {
			shown = strings.TrimPrefix(shown, p)
		}
		if tc.want != tc.value && len(shown) > 4 {
			t.Errorf("mask_header(%q) shows more than 4 characters: %q", tc.name, got)
		}
	}
}

// Email headers are masked by the same rule as request headers.
func TestMaskMailHeaders(t *testing.T) {
	var got struct {
		Headers map[string]string `json:"headers"`
	}
	callPHP(t, `{}`, "email_from_atts", &got, map[string]any{
		"to":      "ann@example.test",
		"subject": "Hi",
		"message": "Hello",
		"headers": []string{"X-Auth-Token: Zq9TOPSECRETvalue77", "X-Request-Id: req-1"},
	})
	if got.Headers["X-Auth-Token"] != "•••••" || got.Headers["X-Request-Id"] != "req-1" {
		t.Errorf("headers = %v", got.Headers)
	}
}

// runVectors is conformance/testdata/run-signature.json, shared with Echo's
// wire package and the Node and Laravel connectors.
type runVectors struct {
	Verify []struct {
		Name, Token, Value, Run, Action string
		OK                              bool
	}
	Accept []struct {
		Name, Token, Value, Remote, Run string
		Forwarded                       bool
	}
}

func loadRunVectors(t *testing.T) runVectors {
	t.Helper()
	b, err := os.ReadFile("../../conformance/testdata/run-signature.json")
	if err != nil {
		t.Fatal(err)
	}
	var v runVectors
	if err := json.Unmarshal(b, &v); err != nil {
		t.Fatal(err)
	}
	return v
}

func TestVerifyRun(t *testing.T) {
	for _, v := range loadRunVectors(t).Verify {
		var got []any
		callPHP(t, `{}`, "verify_run", &got, v.Token, v.Value)
		if want := []any{v.Run, v.Action, v.OK}; fmt.Sprint(got) != fmt.Sprint(want) {
			t.Errorf("%s: verify_run = %v, want %v", v.Name, got, want)
		}
		if v.OK {
			var signed string
			callPHP(t, `{}`, "sign_run", &signed, v.Token, v.Run+"/"+v.Action)
			if signed != v.Value {
				t.Errorf("%s: sign_run = %q, want %q", v.Name, signed, v.Value)
			}
		}
	}
}

func TestAcceptRun(t *testing.T) {
	for _, v := range loadRunVectors(t).Accept {
		setup, _ := json.Marshal(map[string]any{"token": v.Token})
		var got string
		callPHP(t, string(setup), "accept_run", &got, v.Value, v.Remote, v.Forwarded)
		if got != v.Run {
			t.Errorf("%s: accept_run = %q, want %q", v.Name, got, v.Run)
		}
	}
}

// TestRequestRun reads the run the way a request brings it: header first,
// then cookie, checked against REMOTE_ADDR and the forwarding headers.
func TestRequestRun(t *testing.T) {
	signed := "r1/checkout.NhOp3HY0JQ76wSoPnn3sZg"
	for _, tc := range []struct{ name, setup, want string }{
		{"signed header", `{"token":"secret","server":{"REMOTE_ADDR":"203.0.113.9","HTTP_X_ECHO_RUN":"` + signed + `"}}`, "r1/checkout"},
		{"signed cookie", `{"token":"secret","server":{"REMOTE_ADDR":"203.0.113.9"},"cookie":{"X-Echo-Run":"` + signed + `"}}`, "r1/checkout"},
		{"tampered header", `{"token":"secret","server":{"REMOTE_ADDR":"127.0.0.1","HTTP_X_ECHO_RUN":"r1/refund.NhOp3HY0JQ76wSoPnn3sZg"}}`, ""},
		{"unsigned from a public IP", `{"token":"","server":{"REMOTE_ADDR":"203.0.113.9","HTTP_X_ECHO_RUN":"r1/checkout"}}`, ""},
		{"unsigned from loopback", `{"token":"","server":{"REMOTE_ADDR":"127.0.0.1","HTTP_X_ECHO_RUN":"r1/checkout"}}`, "r1/checkout"},
		{"unsigned from loopback via a proxy", `{"token":"","server":{"REMOTE_ADDR":"127.0.0.1","HTTP_X_FORWARDED_FOR":"203.0.113.9","HTTP_X_ECHO_RUN":"r1/checkout"}}`, ""},
		{"environment is trusted", `{"token":"secret","getenv":{"RHYTHM_ECHO_RUN":"env1/cron"}}`, "env1/cron"},
		{"environment loses its signature", `{"token":"secret","getenv":{"RHYTHM_ECHO_RUN":"` + signed + `"}}`, "r1/checkout"},
	} {
		var got string
		callPHP(t, tc.setup, "run", &got)
		if got != tc.want {
			t.Errorf("%s: run = %q, want %q", tc.name, got, tc.want)
		}
	}
}

// An ignored value is logged once per request.
func TestRequestRunLogsOnce(t *testing.T) {
	var got string
	logged := callPHP(t, `{"token":"secret"}`, "accept_run,accept_run", &got, "r1/checkout", "127.0.0.1", false)
	if n := strings.Count(logged, "Rhythm Echo: ignored an unsigned X-Echo-Run: with a token set, only runs Echo signed with it count"); n != 1 {
		t.Errorf("logged %d times:\n%s", n, logged)
	}
}
