// Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
// SPDX-License-Identifier: MIT

package cmp

import (
	"context"
	"embed"
	"encoding/json"
	"fmt"
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

// maxAnswer is the most a CMP script may answer. The Tab caps every
// result at this size too; checking here keeps the driver safe with any
// Tab. The scripts stop lists at 200 entries and texts at 200
// characters, so a real answer is a few KiB.
const maxAnswer = 256 << 10

// isolated runs one of the scripts above in an isolated world and
// refuses an answer over maxAnswer.
func isolated(ctx context.Context, t consent.Tab, js string, args ...any) (json.RawMessage, error) {
	raw, err := t.EvalIsolated(ctx, js, args...)
	if err != nil {
		return nil, err
	}
	if len(raw) > maxAnswer {
		return nil, fmt.Errorf("consent: a CMP script answered %d bytes; the cap is %d", len(raw), maxAnswer)
	}
	return raw, nil
}

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
	ID        string            `json:"id"`
	Selectors []keyed           `json:"selectors"`
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
