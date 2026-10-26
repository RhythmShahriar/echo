// Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
// SPDX-License-Identifier: MIT

package browse

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"testing"

	"github.com/RhythmShahriar/echo/consent"
	"github.com/RhythmShahriar/echo/consent/consenttest"
	"github.com/RhythmShahriar/echo/internal/browser"
)

// The switches a consent check adds in this order and the command
// line Chrome would get: the egress listener as its proxy with loopback
// through it, none of the switches that open the browser's walls and an
// ephemeral DevTools port.
func TestConsentLaunchFlags(t *testing.T) {
	want := []string{
		"--proxy-server=http://127.0.0.1:35060",
		"--proxy-bypass-list=<-loopback>",
		"--force-webrtc-ip-handling-policy=disable_non_proxied_udp",
		"--disable-quic",
		"--no-first-run",
		"--disable-background-networking",
		"--disable-component-update",
		"--disable-sync",
		"--disable-features=OptimizationHints,MediaRouter,Translate",
		"--lang=de-DE",
	}
	p := consenttest.NewPlan(t)
	p.Locale = "de-DE"
	opts := launchOptions(p, "127.0.0.1:35060", planLocale(p))
	if !slices.Equal(opts.Flags, want) {
		t.Fatalf("flags = %q\nwant    %q", opts.Flags, want)
	}
	if opts.Proxy != "" {
		t.Errorf("Proxy = %q; the egress listener goes in as a switch", opts.Proxy)
	}
	args, err := browser.Args(opts)
	if err != nil {
		t.Fatal(err)
	}
	for _, w := range want {
		if !slices.Contains(args, w) {
			t.Errorf("command line has no %s: %q", w, args)
		}
	}
	for _, a := range args {
		name, _, _ := strings.Cut(a, "=")
		switch name {
		case "--disable-web-security", "--allow-file-access-from-files", "--remote-allow-origins", "--disable-site-isolation-trials":
			t.Errorf("command line has %s", a)
		case "--remote-debugging-port":
			if a != "--remote-debugging-port=0" {
				t.Errorf("DevTools on a fixed port: %s", a)
			}
		case "--ignore-certificate-errors":
			t.Errorf("TLS errors ignored without -k: %s", a)
		}
	}
	if !slices.Contains(args, "--remote-debugging-port=0") {
		t.Errorf("no ephemeral DevTools port: %q", args)
	}

	// -k turns certificate checks off; nothing else changes.
	p.Insecure = true
	args, _ = browser.Args(launchOptions(p, "127.0.0.1:35060", "de-DE"))
	if !slices.Contains(args, "--ignore-certificate-errors") {
		t.Errorf("-k: %q", args)
	}
}

func TestPlanLocale(t *testing.T) {
	t.Setenv("LC_ALL", "")
	t.Setenv("LC_MESSAGES", "")
	t.Setenv("LANG", "fr_FR.UTF-8")
	p := consenttest.NewPlan(t)
	for in, want := range map[string]string{
		"de-DE": "de-DE", " pt-BR ": "pt-BR", "": "fr-FR",
		"de,--disable-web-security": "en-US", "x": "en-US", "en US": "en-US",
	} {
		p.Locale = in
		if got := planLocale(p); got != want {
			t.Errorf("planLocale(%q) = %q; want %q", in, got, want)
		}
	}
	for env, want := range map[string]string{"C": "en-US", "POSIX": "en-US", "de_AT.UTF-8@euro": "de-AT", "": "en-US", "sv_SE": "sv-SE"} {
		if got := machineLocale(func(n string) string {
			if n == "LANG" {
				return env
			}
			return ""
		}); got != want {
			t.Errorf("machineLocale(LANG=%q) = %q; want %q", env, got, want)
		}
	}
	if got := machineLocale(func(n string) string { return map[string]string{"LC_ALL": "nb_NO.UTF-8", "LANG": "de_DE"}[n] }); got != "nb-NO" {
		t.Errorf("LC_ALL loses to LANG: %q", got)
	}
}

// fakeChrome writes a "Chrome" that exits at once, so Launch gets past
// the sandbox rule and fails on starting it.
func fakeChrome(t *testing.T) string {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("the fake browser is a shell script")
	}
	bin := filepath.Join(t.TempDir(), "chrome")
	if err := os.WriteFile(bin, []byte("#!/bin/sh\necho crashed >&2\nexit 1\n"), 0o700); err != nil {
		t.Fatal(err)
	}
	return bin
}

// As root Chrome can't use its sandbox: without the opt-in the check
// stops with the sandbox-root code before Chrome starts; with it the
// sandbox rule lets it through and says so on the log.
func TestConsentRefusesRootWithoutOptIn(t *testing.T) {
	t.Cleanup(browser.SwapGeteuid(func() int { return 0 }))
	t.Setenv(browser.NoSandboxEnv, "")
	t.Setenv(browser.SoundcheckNoSandboxEnv, "")
	t.Setenv(ImageEnv, "")
	p := consenttest.NewPlan(t)
	p.Browser.Bin = fakeChrome(t)
	var log bytes.Buffer
	p.Browser.Log = &log
	_, err := Launch(t.Context(), p, "127.0.0.1:35061")
	var fe *consent.FatalError
	if !errors.As(err, &fe) || fe.Code != consent.FatalSandboxRoot {
		t.Fatalf("root without --no-sandbox: %v", err)
	}
	if log.Len() != 0 {
		t.Errorf("logged before refusing: %q", log.String())
	}

	p.Browser.NoSandbox = true
	_, err = Launch(t.Context(), p, "127.0.0.1:35061")
	if err == nil || errors.As(err, &fe) {
		t.Fatalf("root with --no-sandbox: %v", err)
	}
	if !strings.Contains(log.String(), "WARNING: Chrome is running without its sandbox") {
		t.Errorf("no sandbox warning: %q", log.String())
	}
}

// RHYTHM_ECHO_IMAGE=1 with no browser is the image's own message, not
// a download; with a browser found, or --browser given, the check goes
// on.
func TestImageNoBrowser(t *testing.T) {
	oldFind, oldLaunch := findBrowser, launchBrowser
	t.Cleanup(func() { findBrowser, launchBrowser = oldFind, oldLaunch })
	var findOpts []browser.Options
	findBrowser = func(_ context.Context, o browser.Options) (string, bool, error) {
		findOpts = append(findOpts, o)
		return "", false, errors.New("no Chrome or Chromium found")
	}
	launched := 0
	launchBrowser = func(context.Context, browser.Options) (*browser.Browser, error) {
		launched++
		return &browser.Browser{}, nil
	}
	p := consenttest.NewPlan(t)

	t.Setenv(ImageEnv, "1")
	_, err := Launch(t.Context(), p, "127.0.0.1:35062")
	var fe *consent.FatalError
	if !errors.As(err, &fe) || fe.Code != consent.FatalImage {
		t.Fatalf("image without a browser: %v", err)
	}
	if launched != 0 || len(findOpts) != 1 || !findOpts[0].NoDownload {
		t.Errorf("launched %d, find options %+v; want a look without a download", launched, findOpts)
	}

	// --browser in the image skips the look.
	p.Browser.Bin = "/opt/chrome/chrome"
	if _, err := Launch(t.Context(), p, "127.0.0.1:35062"); err != nil || launched != 1 || len(findOpts) != 1 {
		t.Errorf("--browser in the image: %v, launched %d", err, launched)
	}

	// Outside the image the same failure is no-browser, from Launch.
	p.Browser.Bin = ""
	t.Setenv(ImageEnv, "")
	launchBrowser = func(context.Context, browser.Options) (*browser.Browser, error) {
		return nil, browser.ErrNoBrowser
	}
	if _, err := Launch(t.Context(), p, "127.0.0.1:35062"); !errors.As(err, &fe) || fe.Code != consent.FatalNoBrowser {
		t.Errorf("no browser outside the image: %v", err)
	}
}

// A --browser path that doesn't exist is no-browser; other start
// failures are plain errors with their text masked.
func TestLaunchErrorMapping(t *testing.T) {
	t.Setenv(ImageEnv, "")
	t.Setenv(browser.NoSandboxEnv, "1")
	p := consenttest.NewPlan(t)
	p.Browser.Bin = filepath.Join(t.TempDir(), "missing-chrome")
	p.Browser.Log = &bytes.Buffer{}
	_, err := Launch(t.Context(), p, "127.0.0.1:35063")
	var fe *consent.FatalError
	if !errors.As(err, &fe) || fe.Code != consent.FatalNoBrowser || !strings.Contains(fe.Detail, "missing-chrome") {
		t.Fatalf("missing --browser: %v", err)
	}

	token := "ghp_" + strings.Repeat("A1b2C3d4", 5) // assembled at runtime
	err = launchError(errors.New("starting chrome: " + token))
	if errors.As(err, &fe) || strings.Contains(err.Error(), token) {
		t.Errorf("launchError leaks or misfiles: %v", err)
	}
	if err := launchError(context.Canceled); !errors.Is(err, context.Canceled) {
		t.Errorf("canceled: %v", err)
	}
}

// The browser's one-line messages go to the plan's writer, one per
// line, with control characters made harmless.
func TestLineWriter(t *testing.T) {
	if lineWriter(nil) != nil {
		t.Error("a nil writer must stay nil, so warnings reach stderr")
	}
	var b bytes.Buffer
	w := lineWriter(&b)
	w("Browser: Chrome 153.0.8010.52 (/usr/bin/google-chrome)")
	w("bad \x1b[31mred")
	if got := b.String(); got != "Browser: Chrome 153.0.8010.52 (/usr/bin/google-chrome)\nbad \\x1b[31mred\n" {
		t.Errorf("log = %q", got)
	}
}
