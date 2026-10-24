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
// word would be close to up and ls.
func Closest(typed string, candidates []string) string {
	typed = strings.ToLower(typed)
	best, bestD := "", min(3, len([]rune(typed)))
	for _, c := range candidates {
		if d := EditDistance(typed, c); d < bestD {
			best, bestD = c, d
		}
	}
	return best
}

// EditDistance is the Levenshtein distance between a and b.
func EditDistance(a, b string) int {
	ra, rb := []rune(a), []rune(b)
	prev := make([]int, len(rb)+1)
	cur := make([]int, len(rb)+1)
	for j := range prev {
		prev[j] = j
	}
	for i := 1; i <= len(ra); i++ {
		cur[0] = i
		for j := 1; j <= len(rb); j++ {
			cost := 1
			if ra[i-1] == rb[j-1] {
				cost = 0
			}
			cur[j] = min(prev[j]+1, cur[j-1]+1, prev[j-1]+cost)
		}
		prev, cur = cur, prev
	}
	return prev[len(rb)]
}
