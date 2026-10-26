// Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
// SPDX-License-Identifier: MIT

package browser

import (
	"errors"
	"strings"
	"testing"
)

// Extra switches pass through as name and value; the ones that open the
// browser's walls are refused, in any case and with or without a value.
func TestSplitFlag(t *testing.T) {
	for _, c := range []struct{ in, name, value string }{
		{"--lang=de-DE", "lang", "de-DE"},
		{"--disable-quic", "disable-quic", ""},
		{"--proxy-server=http://127.0.0.1:1234", "proxy-server", "http://127.0.0.1:1234"},
		{"--proxy-bypass-list=<-loopback>", "proxy-bypass-list", "<-loopback>"},
	} {
		name, value, err := splitFlag(c.in)
		if err != nil || name != c.name || value != c.value {
			t.Errorf("splitFlag(%q) = %q, %q, %v; want %q, %q", c.in, name, value, err, c.name, c.value)
		}
	}
	for _, in := range []string{
		"--disable-web-security", "--allow-file-access-from-files", "--remote-allow-origins=*",
		"--disable-site-isolation-trials", "--remote-debugging-port=9222", "--Disable-Web-Security",
		"lang=de", "-lang=de", "--", "",
	} {
		if _, _, err := splitFlag(in); err == nil {
			t.Errorf("splitFlag(%q) was allowed", in)
		}
	}
}

// Launch refuses a denied switch before it looks for a browser, so the
// error is the same on a machine with no Chrome at all.
func TestLaunchRefusesDeniedFlags(t *testing.T) {
	t.Setenv(NoSandboxEnv, "1") // as root in CI, the sandbox rule would answer first
	_, err := Launch(t.Context(), Options{Bin: "/nonexistent/chrome", Flags: []string{"--lang=en", "--disable-web-security"}, Log: func(string) {}})
	if err == nil || !strings.Contains(err.Error(), "--disable-web-security isn't allowed") {
		t.Fatalf("err = %v", err)
	}
}

// RHYTHM_ECHO_NO_SANDBOX and Soundcheck's own variable both turn the
// sandbox off; anything but 1 leaves it on.
func TestSandboxEnvNames(t *testing.T) {
	old := geteuid
	t.Cleanup(func() { geteuid = old })
	geteuid = func() int { return 1000 }
	for _, c := range []struct {
		echo, soundcheck string
		off              bool
	}{
		{"1", "", true},
		{"", "1", true},
		{"", "", false},
		{"0", "true", false},
	} {
		t.Setenv(NoSandboxEnv, c.echo)
		t.Setenv(SoundcheckNoSandboxEnv, c.soundcheck)
		off, err := sandbox(Options{Log: func(string) {}})
		if err != nil || off != c.off {
			t.Errorf("echo=%q soundcheck=%q: off = %v, err = %v; want %v", c.echo, c.soundcheck, off, err, c.off)
		}
	}
}

// A marked error keeps its own text and matches its sentinel.
func TestMarkedErrors(t *testing.T) {
	err := mark(errors.New("no Chrome or Chromium found"), ErrNoBrowser)
	if err.Error() != "no Chrome or Chromium found" || !errors.Is(err, ErrNoBrowser) || errors.Is(err, ErrSandboxRoot) {
		t.Errorf("mark: %v", err)
	}
	if mark(nil, ErrNoBrowser) != nil {
		t.Error("mark(nil) isn't nil")
	}
	old := geteuid
	t.Cleanup(func() { geteuid = old })
	geteuid = func() int { return 0 }
	t.Setenv(NoSandboxEnv, "")
	t.Setenv(SoundcheckNoSandboxEnv, "")
	if _, err := sandbox(Options{}); !errors.Is(err, ErrSandboxRoot) {
		t.Errorf("root: %v", err)
	}
	if _, _, err := Find(Options{Bin: "/nonexistent/chrome"}); !errors.Is(err, ErrNoBrowser) {
		t.Errorf("missing --browser: %v", err)
	}
	restore := SwapGeteuid(func() int { return 42 })
	if geteuid() != 42 {
		t.Error("SwapGeteuid didn't swap")
	}
	restore()
	if geteuid() != 0 {
		t.Error("SwapGeteuid didn't restore")
	}
}
