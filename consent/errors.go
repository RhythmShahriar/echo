// Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
// SPDX-License-Identifier: MIT

package consent

import (
	"fmt"
	"net/url"
	"strings"
)

// FatalCode says why a whole check stopped. The CLI turns each into one
// of the PRD 10.2 messages 9 to 16.
type FatalCode string

// The fatal codes, with the PRD 10.2 message each one becomes.
const (
	FatalNoBrowser    FatalCode = "no-browser"       // message 9
	FatalSandboxRoot  FatalCode = "sandbox-root"     // message 10
	FatalUnreachable  FatalCode = "unreachable"      // message 11 (Detail: the dial error)
	FatalHTTPStatus   FatalCode = "http-status"      // message 12 (Status set)
	FatalBotWall      FatalCode = "bot-wall"         // message 13 (Detail: "Cloudflare", "Akamai", "DataDome", "PerimeterX", "a bot check")
	FatalRedirectHost FatalCode = "redirect-host"    // message 14 (To set)
	FatalTLS          FatalCode = "tls"              // message 15 (Detail: the x509 error)
	FatalImage        FatalCode = "no-browser-image" // message 16 (RHYTHM_ECHO_IMAGE=1 and no browser)
	FatalRedirectLoop FatalCode = "redirect-loop"
	FatalWrite        FatalCode = "write"
	FatalSelector     FatalCode = "selector" // a usage error: Detail names the flag and the CSS the browser refused
	FatalRefused      FatalCode = "refused"  // a usage error: Echo itself never loads the start page (Detail: RefusedMetadata or RefusedNotMapped)
)

// Why Echo itself refuses a start page, as FatalRefused's Detail.
const (
	RefusedMetadata  = "metadata"   // a cloud metadata address, refused for every target
	RefusedNotMapped = "not mapped" // --map-only and no --map-host covers the host
)

// FatalError stops a whole check: no browser, the site didn't answer, a
// bot wall. Every string in it is masked.
type FatalError struct {
	Code   FatalCode
	URL    string // masked
	To     string // masked, for redirect-host
	Status int
	Detail string // masked
	Err    error
}

// Error is a plain description for logs; the CLI writes the user-facing
// message from the fields instead.
func (e *FatalError) Error() string {
	parts := []string{"consent: " + string(e.Code)}
	if e.URL != "" {
		parts = append(parts, e.URL)
	}
	if e.To != "" {
		parts = append(parts, "to "+e.To)
	}
	if e.Status != 0 {
		parts = append(parts, fmt.Sprintf("status %d", e.Status))
	}
	if e.Detail != "" {
		parts = append(parts, e.Detail)
	} else if e.Err != nil {
		parts = append(parts, e.Err.Error())
	}
	return strings.Join(parts, ": ")
}

// Unwrap returns the underlying error.
func (e *FatalError) Unwrap() error { return e.Err }

// RefusedText is the message for a FatalRefused e: what Echo refused
// and why, never the site's fault. A URL that doesn't parse is named
// whole.
func RefusedText(e *FatalError) string {
	host := e.URL
	if u, err := url.Parse(e.URL); err == nil && u.Hostname() != "" {
		host = u.Hostname()
	}
	if e.Detail == RefusedMetadata {
		return host + " is a cloud metadata address; Echo never loads it"
	}
	return e.URL + " isn't covered by --map-host and --map-only refuses it; add a --map-host for " + host
}
