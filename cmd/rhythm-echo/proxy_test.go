// Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
// SPDX-License-Identifier: MIT

package main

import (
	"bytes"
	"log/slog"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/RhythmShahriar/echo/proxy"
	kitconfig "github.com/RhythmShahriar/rhythm/kit/config"
)

func TestViewHandlerShowsWarnings(t *testing.T) {
	var got []string
	l := slog.New(viewHandler{log: func(s string) { got = append(got, s) }})
	l.Debug("tunnel", "host", "a.test")
	l.With("via", "connect").Warn("refused a tunnel", "host", "api.stripe.com")
	if len(got) != 1 || got[0] != "proxy: refused a tunnel via=connect host=api.stripe.com" {
		t.Errorf("got %q", got)
	}
}

// caEnv points the config and cache directories at a fresh temp home.
func caEnv(t *testing.T) string {
	t.Helper()
	home := t.TempDir()
	for _, v := range []string{"HOME", "XDG_CONFIG_HOME", "XDG_CACHE_HOME", "AppData", "LocalAppData"} {
		t.Setenv(v, filepath.Join(home, v))
	}
	return home
}

func TestCmdCAMovesTheCacheCA(t *testing.T) {
	caEnv(t)
	// The folders each OS uses (XDG on Linux, Library on macOS, AppData on
	// Windows), all under the temp home caEnv set.
	cache, err := kitconfig.CacheDir("echo")
	if err != nil {
		t.Fatal(err)
	}
	legacy := filepath.Join(cache, "ca")
	dir, err := proxy.DefaultCADir()
	if err != nil {
		t.Fatal(err)
	}
	old, err := proxy.LoadCA(legacy)
	if err != nil {
		t.Fatal(err)
	}
	var out, errOut bytes.Buffer
	if code := cmdCA(nil, &out, &errOut); code != exitOK {
		t.Fatalf("exit %d: %s", code, errOut.String())
	}
	want := filepath.Join(dir, "ca.pem")
	if strings.TrimSpace(out.String()) != want {
		t.Errorf("path = %q, want %q", out.String(), want)
	}
	if !strings.Contains(errOut.String(), "rhythm echo: moved Echo's CA from "+legacy) {
		t.Errorf("stderr = %q", errOut.String())
	}
	b, err := os.ReadFile(want) //nolint:gosec // the test's own file
	if err != nil || !bytes.Equal(b, old.PEM) {
		t.Errorf("moved CA differs: %v", err)
	}

	out.Reset()
	errOut.Reset()
	if code := cmdCA([]string{"--pem"}, &out, &errOut); code != exitOK || !bytes.Equal(out.Bytes(), old.PEM) || errOut.Len() != 0 {
		t.Errorf("--pem: %d %q %q", code, out.String(), errOut.String())
	}
}

func TestCmdCAUntrust(t *testing.T) {
	home := caEnv(t)
	var out, errOut bytes.Buffer
	if code := cmdCA([]string{"--untrust"}, &out, &errOut); code != exitOK {
		t.Fatalf("exit %d", code)
	}
	if !strings.HasPrefix(out.String(), "No Echo CA on this machine.") || !strings.Contains(out.String(), "update-ca-certificates") {
		t.Errorf("no CA: %q", out.String())
	}
	if strings.Contains(out.String(), "rm -r") || strings.Contains(out.String(), "rmdir") {
		t.Errorf("removal of a missing directory offered: %q", out.String())
	}
	if ents, _ := os.ReadDir(home); len(ents) != 0 {
		t.Errorf("--untrust created %v", ents)
	}

	ca, err := proxy.LoadCA("")
	if err != nil {
		t.Fatal(err)
	}
	out.Reset()
	if code := cmdCA([]string{"--untrust"}, &out, &errOut); code != exitOK {
		t.Fatalf("exit %d", code)
	}
	got := out.String()
	for _, w := range []string{
		"To stop trusting Echo's CA",
		"rhythm-echo CA, serial " + strings.ToUpper(ca.Cert.SerialNumber.Text(16)),
		"sudo security delete-certificate -Z <SHA-1> /Library/Keychains/System.keychain",
		"certutil -delstore Root <SHA-1>",
		removeDir(filepath.Dir(ca.CertPath), runtime.GOOS),
	} {
		if !strings.Contains(got, w) {
			t.Errorf("missing %q in:\n%s", w, got)
		}
	}

	if code := cmdCA([]string{"--pem", "--untrust"}, &out, &errOut); code != exitUsage {
		t.Errorf("--pem --untrust: exit %d", code)
	}
}

func TestShellQuote(t *testing.T) {
	for in, want := range map[string]string{
		"/home/a/.config/rhythm/echo/ca":       "/home/a/.config/rhythm/echo/ca",
		"/Users/a/Library/Application Support": "'/Users/a/Library/Application Support'",
		"/tmp/it's":                            `'/tmp/it'\''s'`,
	} {
		if got := shellQuote(in); got != want {
			t.Errorf("shellQuote(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestRemoveDirPerOS(t *testing.T) {
	if got := removeDir("/home/a b/.cache/rhythm/echo", "linux"); got != "rm -r '/home/a b/.cache/rhythm/echo'" {
		t.Errorf("linux: %q", got)
	}
	if got := removeDir(`C:\Users\a b\AppData\Local\rhythm\echo`, "windows"); got != `rmdir /s /q "C:\Users\a b\AppData\Local\rhythm\echo"` {
		t.Errorf("windows: %q", got)
	}
}
