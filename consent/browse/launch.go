// Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
// SPDX-License-Identifier: MIT

package browse

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"regexp"
	"strings"

	"github.com/RhythmShahriar/echo/consent"
	"github.com/RhythmShahriar/echo/internal/browser"
)

// ImageEnv is set to 1 in Echo's container image, which ships without a
// browser: a check there that finds none says so (PRD 10.2 message 16)
// instead of downloading one.
const ImageEnv = "RHYTHM_ECHO_IMAGE"

// bypassLoopback is Chrome's proxy bypass rule that removes the implicit
// loopback bypass, so requests to 127.0.0.1 and localhost go through the
// egress guard like any other.
const bypassLoopback = "<-loopback>"

// launchFlags are the Chrome switches a consent check adds (PRD 13.1).
// internalAddr is the refuse-all listener every request outside a run's
// own context reaches; each run's context names its own guard proxy.
// WebRTC may use only proxied UDP (none) and QUIC is off, so no page
// traffic goes around the proxies. Background services, component
// updates and sync stay quiet so Chrome itself sends as little as it
// can.
func launchFlags(internalAddr, locale string) []string {
	return []string{
		"--proxy-server=http://" + internalAddr,
		"--proxy-bypass-list=" + bypassLoopback,
		"--force-webrtc-ip-handling-policy=disable_non_proxied_udp",
		"--disable-quic",
		"--no-first-run",
		"--disable-background-networking",
		"--disable-component-update",
		"--disable-sync",
		"--disable-features=OptimizationHints,MediaRouter,Translate",
		"--lang=" + locale,
	}
}

// launchOptions turns the plan's browser settings into internal/browser
// options. Proxy stays empty: the egress listener goes in as a switch
// with the loopback rule beside it.
func launchOptions(p *consent.Plan, internalAddr, locale string) browser.Options {
	return browser.Options{
		Bin:        p.Browser.Bin,
		NoDownload: p.Browser.NoDownload,
		NoSandbox:  p.Browser.NoSandbox,
		Insecure:   p.Insecure,
		Visible:    p.Browser.Visible,
		Log:        lineWriter(p.Browser.Log),
		Flags:      launchFlags(internalAddr, locale),
	}
}

// lineWriter turns the plan's log writer into the one-line callback the
// browser package takes; nil stays nil, so warnings still reach stderr.
func lineWriter(w io.Writer) func(string) {
	if w == nil {
		return nil
	}
	return func(s string) { fmt.Fprintln(w, consent.SafeText(s, 0)) }
}

// launchBrowser is browser.Launch; tests replace it.
var launchBrowser = browser.Launch

// findBrowser is browser.FindContext; tests replace it.
var findBrowser = browser.FindContext

// Launch starts the one Chrome a consent command uses, with the egress
// listener at internalAddr as its browser-wide proxy. The "Browser: ..."
// line goes to the plan's log. A missing browser, a root user without
// --no-sandbox and the container image without a browser come back as
// a *consent.FatalError.
func Launch(ctx context.Context, p *consent.Plan, internalAddr string) (*browser.Browser, error) {
	opts := launchOptions(p, internalAddr, planLocale(p))
	if os.Getenv(ImageEnv) == "1" && opts.Bin == "" {
		// The image has no browser and shouldn't fetch one into a
		// throwaway layer: look for an installed one only.
		probe := opts
		probe.NoDownload = true
		if _, _, err := findBrowser(ctx, probe); err != nil {
			return nil, &consent.FatalError{Code: consent.FatalImage, Detail: consent.MaskURL(err.Error(), false), Err: err}
		}
	}
	b, err := launchBrowser(ctx, opts)
	if err != nil {
		return nil, launchError(err)
	}
	return b, nil
}

// launchError maps internal/browser's errors to the fatal codes the CLI
// turns into PRD 10.2 messages 9 and 10. Anything else stays as it is,
// masked: Chrome's own output can carry the command line.
func launchError(err error) error {
	switch {
	case errors.Is(err, context.Canceled), errors.Is(err, context.DeadlineExceeded):
		return err
	case errors.Is(err, browser.ErrSandboxRoot):
		return &consent.FatalError{Code: consent.FatalSandboxRoot, Detail: consent.MaskURL(err.Error(), false), Err: err}
	case errors.Is(err, browser.ErrNoBrowser):
		return &consent.FatalError{Code: consent.FatalNoBrowser, Detail: consent.MaskURL(err.Error(), false), Err: err}
	}
	return fmt.Errorf("consent: starting Chrome: %s", consent.MaskURL(err.Error(), false))
}

// localeTag is the shape of a BCP 47 tag as Chrome's --lang and
// Accept-Language take it.
var localeTag = regexp.MustCompile(`^[A-Za-z]{2,8}(-[A-Za-z0-9]{1,8})*$`)

// osGetenv is os.Getenv; machineLocale reads the locale through it.
var osGetenv = os.Getenv

// machineLocale reads a POSIX locale such as de_DE.UTF-8 as a BCP 47
// tag (de-DE). C and POSIX, which say nothing about a language, give
// en-US.
func machineLocale(getenv func(string) string) string {
	for _, name := range []string{"LC_ALL", "LC_MESSAGES", "LANG"} {
		v := getenv(name)
		if v == "" {
			continue
		}
		v, _, _ = strings.Cut(v, ".")
		v, _, _ = strings.Cut(v, "@")
		if v == "C" || v == "POSIX" || v == "" {
			return "en-US"
		}
		return strings.ReplaceAll(v, "_", "-")
	}
	return "en-US"
}
