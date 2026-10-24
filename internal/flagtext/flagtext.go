// Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
// SPDX-License-Identifier: MIT

// Package flagtext words flag errors the way every rhythm echo command
// does: flags spelled as the help spells them (--fail-on, not -fail-on)
// and an unknown one with the flag it most likely meant. The CLI uses it,
// as do the commands whose packages parse their own flags (hub,
// schedule, mcp and conformance).
package flagtext

import (
	"flag"
	"fmt"
	"regexp"
	"strings"

	"github.com/RhythmShahriar/rhythm/kit/cli"
)

// Footer ends every command's help.
const Footer = "rhythm echo help lists every command.\n"

var (
	unknownFlag = regexp.MustCompile(`^flag provided but not defined: -+(.+)$`)
	needsValue  = regexp.MustCompile(`^flag needs an argument: -+(.+)$`)
	badValue    = regexp.MustCompile(`^invalid (?:boolean )?value "(.*)" for (?:flag )?-+([^:]+): (.*)$`)
)

// Error rewords an error from fs.Parse. An unknown flag gets the one of
// fs's flags it most likely meant; a value the flag package could only
// call a "parse error" gets what the flag wants instead.
func Error(err error, fs *flag.FlagSet) string {
	msg := err.Error()
	if m := unknownFlag.FindStringSubmatch(msg); m != nil {
		var names []string
		fs.VisitAll(func(f *flag.Flag) { names = append(names, f.Name) })
		out := "unknown flag " + Dash(m[1])
		if s := Closest(m[1], names); s != "" {
			out += "; did you mean " + Dash(s) + "?"
		}
		return out
	}
	if m := needsValue.FindStringSubmatch(msg); m != nil {
		return Dash(m[1]) + " needs a value"
	}
	if m := badValue.FindStringSubmatch(msg); m != nil {
		why := m[3]
		if f := fs.Lookup(m[2]); f != nil && why == "parse error" {
			why = wants(f)
		}
		return fmt.Sprintf("%s %q: %s", Dash(m[2]), m[1], why)
	}
	return msg
}

// wants says what kind of value f takes.
func wants(f *flag.Flag) string {
	if b, ok := f.Value.(interface{ IsBoolFlag() bool }); ok && b.IsBoolFlag() {
		return "want true or false"
	}
	switch fmt.Sprintf("%T", f.Value) {
	case "*flag.durationValue":
		return "want a duration like 10m or 5s"
	case "*flag.intValue", "*flag.int64Value", "*flag.uintValue", "*flag.uint64Value":
		return "want a whole number"
	case "*flag.float64Value":
		return "want a number"
	}
	return "parse error"
}

// Dash spells a flag as the help does: -o for a one-letter flag,
// --fail-on otherwise.
func Dash(name string) string {
	if len(name) == 1 {
		return "-" + name
	}
	return "--" + name
}

// Closest returns the candidate within edit distance 2 of typed, the
// nearest first and the earlier on a tie; "" when none is that close.
// Two edits catch a swap (cmopare) or a missing and a wrong letter. The
// distance must also be less than typed's length, or every two-letter
// word would be close to up and ls. It is the kit's cli.Suggest on
// typed in lower case.
func Closest(typed string, candidates []string) string {
	return cli.Suggest(strings.ToLower(typed), candidates)
}
