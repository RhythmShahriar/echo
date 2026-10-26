// Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
// SPDX-License-Identifier: MIT

package cmp

import (
	"embed"
	"strings"

	"github.com/RhythmShahriar/echo/consent"
)

// jsFS holds the isolated-world scripts. They are data reviewed as
// such: each only reads the page.
//
//go:embed js/*.js
var jsFS embed.FS

// The isolated-world scripts, built once. Each is a function expression
// the Tab calls with one argument (the input below); its first line
// names it, which is how consenttest.FakeTab answers it.
var (
	scanJS = buildScript("scan")
)

// buildScript joins lib.js and js/<name>.js into one function
// expression named cmp-<name>.
func buildScript(name string) string {
	lib, err := jsFS.ReadFile("js/lib.js")
	if err != nil {
		panic(err)
	}
	body, err := jsFS.ReadFile("js/" + name + ".js")
	if err != nil {
		panic(err)
	}
	return "// cmp-" + name + "\n(input) => {\n\"use strict\";\n" + string(lib) + "\n" + string(body) + "\n}"
}

// keyed is a locator with the key the script answers it under.
type keyed struct {
	Key string          `json:"key"`
	Loc consent.Locator `json:"loc"`
}

// locKey is a locator written the way --accept-selector reads it, which
// is also its key in a script's answer.
func locKey(l consent.Locator) string {
	parts := append([]string{}, l.Shadow...)
	s := strings.Join(append(parts, l.CSS), " ::shadow ")
	if l.Frame != "" {
		s = "frame(" + l.Frame + ") " + s
	}
	return s
}

// scanRule is one rule as the scan script gets it.
type scanRule struct {
	ID        string           `json:"id"`
	Selectors []keyed            `json:"selectors"`
	Banners   []consent.Locator `json:"banners,omitempty"`
}

type scanInput struct {
	Rules   []scanRule `json:"rules"`
	Scripts []string   `json:"scripts"`
}

type scanRuleResult struct {
	Matched []string `json:"matched"`
	Banner  bool     `json:"banner"`
}

type scanResult struct {
	Rules   map[string]scanRuleResult `json:"rules"`
	Scripts []string                  `json:"scripts"`
}
