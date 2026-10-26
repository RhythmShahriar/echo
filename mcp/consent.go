// Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
// SPDX-License-Identifier: MIT

package mcp

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"

	consentcli "github.com/RhythmShahriar/echo/consent/cli"
)

// consentParallel is how many browser runs check_consent starts at once.
const consentParallel = "3"

// consentTool is check_consent in tools/list. There is no send_hits
// argument: an agent's check always holds tracker hits in the browser,
// and the schema refuses any argument it doesn't list.
func consentTool() tool {
	return tool{Name: "check_consent", Title: "Check cookie consent",
		Description: "Loads a page in a real browser as a new visitor with no choice, then Accept, then Reject on its cookie banner. " +
			"It reports every tracker that fired before the choice or after Reject, with Consent Mode decoded. " +
			"Tracking hits are held in the browser, never sent. Returns the consent JSON report: issues and inconclusive runs. " +
			"Only for sites you own or are authorised to test.",
		InputSchema: object([]string{"url"}, map[string]any{
			"url":          str("The page to check, like https://staging.shop.test/ (https:// is assumed; a path is read against site: in .echo/config.yaml)."),
			"runs":         str("Which runs: none, accept, reject, gpc, withdraw, comma-separated (default none,accept,reject); +gpc adds to the default."),
			"consent_mode": map[string]any{"type": "string", "enum": []string{"advanced", "basic", "strict"}, "description": "How Google Consent Mode pings count (default advanced: cookieless pings are a notice)."},
			"geo":          str("Look like a visitor from this country, a two-letter code like de: language and time zone, never the IP address."),
		}),
		// It loads the site and writes a result folder under .echo.
		Annotations: annotations(false, false, false)}
}

// consentArgs are check_consent's arguments.
type consentArgs struct {
	URL         string `json:"url"`
	Runs        string `json:"runs"`
	ConsentMode string `json:"consent_mode"`
	Geo         string `json:"geo"`
}

// commandLine is the consent check's command line for a: the same plan
// builder as rhythm echo consent, three runs at a time, results in the
// server's store and its config when there is one there.
func (s *Server) commandLine(a consentArgs) []string {
	args := []string{"--parallel", consentParallel}
	if s.Store.Root != "" {
		args = append(args, "--store", s.Store.Root)
		if p := filepath.Join(s.Store.Root, "config.yaml"); fileExists(p) {
			args = append(args, "--config", p)
		}
	}
	if a.Runs != "" {
		args = append(args, "--runs", a.Runs)
	}
	if a.ConsentMode != "" {
		args = append(args, "--consent-mode", a.ConsentMode)
	}
	if a.Geo != "" {
		args = append(args, "--geo", a.Geo)
	}
	// After --, so a URL that starts with a dash is a URL, never a flag.
	return append(args, "--", a.URL)
}

func fileExists(p string) bool {
	fi, err := os.Stat(p)
	return err == nil && fi.Mode().IsRegular()
}

// checkConsent runs check_consent and returns the consent envelope.
func (s *Server) checkConsent(ctx context.Context, a consentArgs) (any, error) {
	if strings.TrimSpace(a.URL) == "" {
		return nil, toolErrorf("url is empty: give the page to check, like https://staging.shop.test/")
	}
	out, err := consentcli.AgentCheck(ctx, s.commandLine(a), consentcli.Env{Version: s.Version, Now: s.Now})
	if err != nil {
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		return nil, toolError{err.Error()}
	}
	var env any
	if err := json.Unmarshal(out, &env); err != nil {
		return nil, toolErrorf("the consent check's JSON didn't parse: %v", err)
	}
	return env, nil
}
