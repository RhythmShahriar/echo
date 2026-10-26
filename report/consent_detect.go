// Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
// SPDX-License-Identifier: MIT

package report

import (
	"fmt"
	"io"
	"regexp"
	"strings"
	"time"

	"github.com/RhythmShahriar/echo/consent"
)

// defaultBannerWait is --banner-wait's default, for the "none found
// within" line when the options don't say.
const defaultBannerWait = 10 * time.Second

// WriteConsentDetect writes what consent detect found, as lines or as a
// consent-detect envelope. The lines (PRD 6.1):
//
//	Banner:  Cookiebot, found after 0.8s (by #CybotCookiebotDialog and window.Cookiebot)
//	Accept:  "Allow all" (#CybotCookiebotDialogBodyLevelButtonLevelOptinAllowAll), first layer
//	Reject:  not found on the banner or in its settings
//	Screenshot: .echo/consent/detect/2026-10-26T09-14-03-a1b2c3.png
//
// With no banner the first line is `Banner:  none found within 10s`
// (with `(<CMP>'s script loaded)` when the CMP is on the page) and no
// button lines follow.
func WriteConsentDetect(w io.Writer, d *consent.DetectResult, asJSON bool, o ConsentTextOptions) error {
	if d == nil {
		d = &consent.DetectResult{}
	}
	if asJSON {
		return encodeJSON(w, detectEnvelope(d))
	}
	var b strings.Builder
	if d.CMP.Found {
		fmt.Fprintf(&b, "Banner:  %s, found after %s", Safe(cmpLabel(d.CMP)), oneDecimal(d.CMP.BannerAtMS))
		if by := detectedBy(d.CMP.DetectedBy); by != "" {
			fmt.Fprintf(&b, " (by %s)", Safe(by))
		}
		b.WriteString("\n")
		fmt.Fprintf(&b, "Accept:  %s\n", controlLine(d.Accept))
		fmt.Fprintf(&b, "Reject:  %s\n", controlLine(d.Reject))
	} else {
		fmt.Fprintf(&b, "Banner:  none found within %s%s\n", waitText(o.BannerWait), scriptLoaded(d.CMP))
	}
	if d.Screenshot != "" {
		fmt.Fprintf(&b, "Screenshot: %s\n", Safe(d.Screenshot))
	}
	_, err := io.WriteString(w, b.String())
	return err
}

// cmpLabel is the CMP's name, else its rule id, else "a banner".
func cmpLabel(c consent.CMPInfo) string {
	switch {
	case c.Name != "":
		return c.Name
	case c.ID != "":
		return c.ID
	}
	return "a banner"
}

// scriptLoaded is ` (<CMP>'s script loaded)` when the CMP is on the page
// without a banner, else "".
func scriptLoaded(c consent.CMPInfo) string {
	if !c.Present || c.Found || (c.Name == "" && c.ID == "") {
		return ""
	}
	return " (" + Safe(cmpLabel(c)) + "'s script loaded)"
}

// controlLine is one button: `"<text>" (<selector>), first layer`, or
// `not found on the banner or in its settings`.
func controlLine(c *consent.Choice) string {
	if c == nil {
		return "not found on the banner or in its settings"
	}
	var parts []string
	if c.Text != "" {
		parts = append(parts, `"`+Safe(c.Text)+`"`)
	}
	if c.Selector != "" {
		parts = append(parts, "("+Safe(c.Selector)+")")
	}
	if c.How == "api" && len(parts) == 0 {
		parts = append(parts, "the CMP's API")
	}
	head := strings.Join(parts, " ")
	if head == "" {
		head = "found"
	}
	return head + ", " + layerWords(c.Layer)
}

// layerWords names a banner layer.
func layerWords(n int) string {
	switch n {
	case 0, 1:
		return "first layer"
	case 2:
		return "second layer"
	}
	return fmt.Sprintf("layer %d", n)
}

// detectedBy turns a CMPInfo.DetectedBy list into words: the CMP
// driver writes "selector:#X and global:Y" (older results
// "selector:#X,global:Y"), which reads "#X and window.Y". A selector
// may hold spaces and commas of its own, so the list is cut only where
// the next kind: starts.
func detectedBy(s string) string {
	s = strings.TrimSpace(s)
	if s == "generic" {
		return "the generic rule"
	}
	starts := detectedKind.FindAllStringIndex(s, -1)
	if len(starts) == 0 {
		return s
	}
	var words []string
	if lead := trimDetectedSep(s[:starts[0][0]]); lead != "" {
		words = append(words, lead)
	}
	for i, st := range starts {
		end := len(s)
		if i+1 < len(starts) {
			end = starts[i+1][0]
		}
		kind, what, _ := strings.Cut(trimDetectedSep(s[st[0]:end]), ":")
		switch kind {
		case "selector":
			words = append(words, what)
		case "global":
			words = append(words, "window."+what)
		case "script":
			words = append(words, "its script on "+what)
		}
	}
	return andList(words)
}

// detectedKind finds where each part of a DetectedBy list starts: at
// the start or after " and ", a comma or a plus.
var detectedKind = regexp.MustCompile(`(?:^|\s+and\s+|\s*[,+]\s*)(?:selector|global|script):`)

// trimDetectedSep drops the separator a part of a DetectedBy list
// starts or ends with.
func trimDetectedSep(p string) string {
	p = strings.TrimSpace(p)
	p = strings.TrimPrefix(p, "and ")
	return strings.TrimSpace(strings.Trim(p, ",+"))
}

// andList lists words as "a", "a and b" or "a, b and c".
func andList(words []string) string {
	switch len(words) {
	case 0:
		return ""
	case 1:
		return words[0]
	}
	return strings.Join(words[:len(words)-1], ", ") + " and " + words[len(words)-1]
}

// oneDecimal writes a run clock time as seconds with one decimal: 0.8s.
func oneDecimal(ms int64) string {
	return fmt.Sprintf("%.1fs", float64(ms)/1000)
}

// waitText writes a wait as the flag takes it (10s, 90s, 2m); 0 is the
// --banner-wait default.
func waitText(d time.Duration) string {
	if d <= 0 {
		d = defaultBannerWait
	}
	if d%time.Minute == 0 && d >= 2*time.Minute {
		return fmt.Sprintf("%dm", d/time.Minute)
	}
	if d%time.Second == 0 {
		return fmt.Sprintf("%ds", d/time.Second)
	}
	return fmt.Sprintf("%gs", d.Seconds())
}
