// Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
// SPDX-License-Identifier: MIT

package main

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"regexp"
	"strings"
	"time"

	"github.com/RhythmShahriar/echo/internal/browser"
)

// chromeLabel names doctor's line about the browser consent checks use.
const chromeLabel = "Chrome for consent checks"

// chromeFinder finds the Chrome a consent check would use without ever
// downloading one: doctor only says whether a check would have to.
// Tests replace it.
var chromeFinder = func() (bin string, downloaded bool, err error) {
	return browser.Find(browser.Options{NoDownload: true})
}

// chromeVersion asks an installed Chrome for its version
// ("Google Chrome 153.0.8010.52") and returns the major number, or ""
// when it doesn't say (Chrome on Windows prints nothing). Tests replace
// it.
var chromeVersion = func(bin string) string {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	out, err := exec.CommandContext(ctx, bin, "--version").Output() //nolint:gosec // the browser Find picked
	if err != nil {
		return ""
	}
	return majorVersion(string(out))
}

var versionRE = regexp.MustCompile(`(\d+)\.\d+\.\d+`)

// majorVersion is the first number of the first version in s, or "".
func majorVersion(s string) string {
	if m := versionRE.FindStringSubmatch(s); m != nil {
		return m[1]
	}
	return ""
}

// downloadRefused reports whether a consent check here would refuse the
// Chrome for Testing download: RHYTHM_OFFLINE keeps Rhythm tools off
// the network for their own needs and CI runs shouldn't fetch 200 MB
// unasked.
func downloadRefused() bool {
	if os.Getenv("RHYTHM_OFFLINE") != "" {
		return true
	}
	v := strings.ToLower(os.Getenv("CI"))
	return v != "" && v != "false" && v != "0"
}

// chrome adds the line about the browser consent checks use. It is
// informational and never fails doctor: Echo's capture layers don't
// need a browser.
func (d *doctor) chrome() {
	bin, downloaded, err := chromeFinder()
	switch {
	case err == nil && downloaded:
		d.add(doctorOK, chromeLabel, fmt.Sprintf("found (Chrome for Testing %s at %s)", majorVersion(browser.PinnedVersion()), bin))
	case err == nil:
		if v := chromeVersion(bin); v != "" {
			d.add(doctorOK, chromeLabel, fmt.Sprintf("found (Chrome %s at %s)", v, bin))
		} else {
			d.add(doctorOK, chromeLabel, fmt.Sprintf("found (%s)", bin))
		}
	case downloadRefused():
		d.add(doctorNote, chromeLabel, "missing (--no-download)")
	case browser.Platform() == "" || browser.PinnedVersion() == "":
		d.add(doctorNote, chromeLabel, "missing (no Chrome for Testing build for this system; install Chrome or Chromium)")
	default:
		d.add(doctorNote, chromeLabel, fmt.Sprintf("will download Chrome for Testing %s", majorVersion(browser.PinnedVersion())))
	}
}
