// Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
// SPDX-License-Identifier: MIT

package browser

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
	"testing"
)

// needChrome skips tests that launch a real browser where none is
// installed.
func needChrome(t *testing.T) {
	t.Helper()
	if _, _, err := Find(Options{NoDownload: true}); err != nil {
		t.Skip("no Chrome installed:", err)
	}
}

func TestLaunchLogsTheVersion(t *testing.T) {
	needChrome(t)
	var logs []string
	b, err := Launch(context.Background(), Options{NoDownload: true, Log: func(m string) { logs = append(logs, m) }})
	if err != nil {
		t.Fatal(err)
	}
	defer b.Close()
	if !regexp.MustCompile(`^\d+\.\d+\.\d+\.\d+$`).MatchString(b.Version) {
		t.Errorf("Version = %q", b.Version)
	}
	if len(logs) == 0 || logs[len(logs)-1] != "Browser: Chrome "+b.Version+" ("+b.Bin+")" {
		t.Errorf("logs = %q", logs)
	}
	if strings.Contains(strings.Join(logs, "\n"), "WARNING") {
		t.Errorf("warned with the sandbox on: %q", logs)
	}
}

// TestLaunchHintsAtTheSandboxSetting starts a "Chrome" that fails the
// way Chrome does in a container that blocks its sandbox.
func TestLaunchHintsAtTheSandboxSetting(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("the fake browser is a shell script")
	}
	old := geteuid
	geteuid = func() int { return 1000 }
	t.Cleanup(func() { geteuid = old })
	t.Setenv(NoSandboxEnv, "")
	t.Setenv(SoundcheckNoSandboxEnv, "")
	bin := filepath.Join(t.TempDir(), "chrome")
	script := "#!/bin/sh\necho '[1:1:FATAL:zygote_host_impl_linux.cc(127)] No usable sandbox!' >&2\nexit 1\n"
	if err := os.WriteFile(bin, []byte(script), 0o700); err != nil { //nolint:gosec // an executable test fixture
		t.Fatal(err)
	}
	_, err := Launch(t.Context(), Options{Bin: bin})
	if err == nil || !strings.Contains(err.Error(), "No usable sandbox") || !strings.Contains(err.Error(), "pass --no-sandbox, or set "+NoSandboxEnv+"=1, if this container can't give Chrome its sandbox") {
		t.Fatalf("err = %v", err)
	}
}

// A Chrome that exits before it opens DevTools reads as such, not as
// rod's "Failed to get the debug url: " with nothing after the colon,
// in the error and in the retry note.
func TestStartReason(t *testing.T) {
	for in, want := range map[string]string{
		"[launcher] Failed to get the debug url: ":        "it exited before it opened DevTools",
		"[launcher] Failed to get the debug url: crashed": "it exited before it opened DevTools: crashed",
		"fork/exec /x: permission denied":                 "fork/exec /x: permission denied",
		"something: ":                                     "something",
	} {
		if got := StartReason(errors.New(in)); got != want {
			t.Errorf("StartReason(%q) = %q, want %q", in, got, want)
		}
	}
	se := &StartError{Bin: "/bin/true", Err: errors.New("[launcher] Failed to get the debug url: ")}
	if got := se.Error(); got != "starting /bin/true: it exited before it opened DevTools" {
		t.Errorf("StartError: %q", got)
	}
	if got := retryNote("/bin/true", se); got != "Chrome at /bin/true didn't start (it exited before it opened DevTools); starting it again" {
		t.Errorf("retry note: %q", got)
	}
}
