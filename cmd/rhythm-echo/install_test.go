package main

import (
	"net"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
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
	if code, out, _ := runCLI(t, "install", "next", dir); code != 0 || !strings.Contains(out, "already have instrumentation.ts") {
		t.Errorf("next again: %d\n%s", code, out)
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
