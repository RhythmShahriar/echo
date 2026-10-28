// Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
// SPDX-License-Identifier: MIT

package cli

import (
	"cmp"
	"fmt"
	"net/http"
	"os"

	"github.com/RhythmShahriar/echo/consent"
)

// The run warnings, printed on stderr once per command.
const (
	msgNoSandbox = "Chrome is running without its sandbox (--no-sandbox); only check sites you trust"
	// msgSendHits is PRD 10.2 message 23.
	msgSendHits = "--send-hits: this run sends real tracking hits to Google, Meta and the other trackers on the page"
)

// notice is the once-only notice of PRD 9.1, before the first consent
// check on a machine.
const notice = "Note: use Echo on sites you own or are authorised to test. Consent checks load the site like\n" +
	"one new visitor: they never log in or submit forms and every tracking hit is recorded and\n" +
	"stopped in the browser. Results stay on this machine with IDs masked. This notice is shown once.\n"

// The tips, each shown once per machine (never in CI), under their
// FirstRun keys. The first that fits is shown: what explains a
// problem, then the next step after a clean check.
var tips = []struct{ key, text string }{
	{"echo-consent-tip-reject", "Tip: rhythm echo consent detect <url> shows the buttons Echo sees."},
	{"echo-consent-tip-geo", "Tip: banners that show only in some countries need --geo or an EU --proxy."},
	{"echo-consent-tip-save", "Tip: --save baseline keeps this as the baseline for the next check."},
	{"echo-consent-tip-gpc", "Tip: --runs +gpc also checks Global Privacy Control."},
}

// fatalMessage is the user's message for a check that stopped (PRD
// 10.2 messages 9 to 16). Every string in e is already masked.
func fatalMessage(e *consent.FatalError) string {
	url := cmp.Or(e.URL, "the site")
	detail := e.Detail
	if detail == "" && e.Err != nil {
		detail = consent.SafeText(consent.MaskURL(e.Err.Error(), false), 300)
	}
	switch e.Code {
	case consent.FatalNoBrowser:
		if os.Getenv("RHYTHM_ECHO_IMAGE") == "1" {
			return fatalMessage(&consent.FatalError{Code: consent.FatalImage})
		}
		return "no Chrome or Chromium found; install Chrome, pass --browser /path/to/chrome, or allow the one-time Chrome for Testing download"
	case consent.FatalSandboxRoot:
		return "Chrome won't start without its sandbox as root; run as another user, or pass --no-sandbox (only for sites you trust)"
	case consent.FatalUnreachable:
		return fmt.Sprintf("%s didn't answer: %s. Is the site running? A local site with a self-signed certificate needs -k", url, cmp.Or(detail, "no answer"))
	case consent.FatalHTTPStatus:
		status := fmt.Sprintf("%d %s", e.Status, http.StatusText(e.Status))
		if e.Status == http.StatusUnauthorized || e.Status == http.StatusProxyAuthRequired {
			return fmt.Sprintf("%s answered %s; pass the login with -H \"Authorization: env:RHYTHM_ECHO_STAGING_AUTH\" or consent: headers:", url, status)
		}
		return fmt.Sprintf("%s answered %s; check that the site serves its pages, then run the check again", url, status)
	case consent.FatalBotWall:
		if detail == "" || detail == "a bot check" {
			return fmt.Sprintf("%s showed a bot check instead of the page; allow this machine in the firewall for the check, or check staging", url)
		}
		return fmt.Sprintf("%s showed a bot check (%s) instead of the page; allow this machine in the firewall for the check, or check staging", url, detail)
	case consent.FatalRedirectHost:
		return fmt.Sprintf("%s redirects to %s; check that one, or pass --follow-host", url, cmp.Or(e.To, "another host"))
	case consent.FatalTLS:
		return fmt.Sprintf("%s uses a certificate this machine doesn't trust (%s); pass -k for a local or private site", url, cmp.Or(detail, "unknown certificate"))
	case consent.FatalImage:
		return "this image has no browser; run rhythm echo consent on the host, or pass --browser"
	case consent.FatalRedirectLoop:
		return fmt.Sprintf("%s redirects in a loop or through more than 10 hops; check the site's redirects", url)
	case consent.FatalSelector:
		return cmp.Or(detail, "a selector isn't CSS the browser accepts") + "; fix the selector (rhythm echo consent detect shows the buttons Echo sees)"
	case consent.FatalWrite:
		return fmt.Sprintf("couldn't write %s: %s", cmp.Or(e.Detail, e.URL, "the evidence"), errText(e.Err))
	}
	return consent.SafeText(e.Error(), 500)
}

// fatalExit is the exit code of a check that stopped with e: a selector
// the browser refused is a usage error (PRD 10.1), the rest mean the
// site or the browser couldn't be used.
func fatalExit(e *consent.FatalError) int {
	if e.Code == consent.FatalSelector {
		return exitUsage
	}
	return exitUnavailable
}

// errText is err's message, or "unknown error" for none.
func errText(err error) string {
	if err == nil {
		return "unknown error"
	}
	return err.Error()
}
