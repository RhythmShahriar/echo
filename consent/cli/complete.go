// Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
// SPDX-License-Identifier: MIT

package cli

import (
	"flag"
	"strings"

	kitcli "github.com/RhythmShahriar/rhythm/kit/cli"
)

// Complete answers shell completion for the words after "consent", the
// last one being the word under the cursor: the subcommands in first
// place, then the flags of the subcommand the line picks. A word after
// a flag that takes a value gets nothing, so the shell offers file
// names. It reads only the flag definitions: no disk, no network.
func Complete(words []string) []string {
	if len(words) == 0 {
		words = []string{""}
	}
	cur, before := words[len(words)-1], words[:len(words)-1]
	fs := newFlagSet(&options{})
	s := subOf(fs, before)
	var flags []kitcli.HelpFlag
	for _, d := range flagDefs {
		if d.subs&s.bit() == 0 {
			continue
		}
		takes := !isBoolFlag(fs.Lookup(d.name))
		flags = append(flags, kitcli.HelpFlag{Name: d.name, TakesValue: takes})
		if d.short != "" {
			flags = append(flags, kitcli.HelpFlag{Name: d.short, TakesValue: takes})
		}
	}
	if strings.HasPrefix(cur, "-") {
		return kitcli.FilterPrefix(kitcli.FlagWords(flags), cur)
	}
	if len(before) > 0 && kitcli.TakesValue(flags, before[len(before)-1]) {
		return nil
	}
	if s == subCheck && !hasPositional(fs, before) {
		names := make([]string, len(subcommands))
		for i, c := range subcommands {
			names[i] = string(c)
		}
		return kitcli.FilterPrefix(names, cur)
	}
	return nil
}

// hasPositional reports whether words hold a word that isn't a flag or
// a flag's value: once a URL is typed, no subcommand can follow.
func hasPositional(fs *flag.FlagSet, words []string) bool {
	for i := 0; i < len(words); i++ {
		a := words[i]
		if a == "--" {
			return true
		}
		if len(a) > 1 && strings.HasPrefix(a, "-") {
			name, _, hasValue := strings.Cut(strings.TrimLeft(a, "-"), "=")
			if f := fs.Lookup(name); f != nil && !hasValue && !isBoolFlag(f) {
				i++
			}
			continue
		}
		return true
	}
	return false
}
