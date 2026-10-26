// Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
// SPDX-License-Identifier: MIT

package main

import (
	"encoding/json"
	"fmt"
	"io"
	"runtime/debug"
	"strings"

	consentcli "github.com/RhythmShahriar/echo/consent/cli"
	"github.com/RhythmShahriar/rhythm/kit"
	"github.com/RhythmShahriar/rhythm/kit/cli"
)

// commit is the short commit Echo was built from: set with -ldflags
// "-X main.commit=...", else read from the build's VCS stamp.
var commit = ""

// buildCommit is commit, or the first 7 digits of the revision go build
// stamped into the binary, or "".
func buildCommit() string {
	if commit != "" {
		return commit
	}
	info, ok := debug.ReadBuildInfo()
	if !ok {
		return ""
	}
	for _, s := range info.Settings {
		if s.Key == "vcs.revision" && len(s.Value) >= 7 {
			return s.Value[:7]
		}
	}
	return ""
}

// versionDoc is version --json: the kit's rhythm-version/v1 document,
// field for field as kit cli.Main prints it, so the rhythm router can
// tell Echo is built on the kit and send it __complete.
type versionDoc struct {
	Schema     string `json:"schema"`
	Name       string `json:"name"`
	Version    string `json:"version"`
	Commit     string `json:"commit"`
	KitVersion string `json:"kit_version"`
}

// cmdVersion prints the version: "rhythm-echo <version>", or with
// --json the rhythm-version/v1 document.
func cmdVersion(args []string, stdout, stderr io.Writer) int {
	asJSON := false
	for _, a := range args {
		switch a {
		case "--json", "-json":
			asJSON = true
		case "-h", "--help":
			fmt.Fprint(stdout, "Usage: rhythm echo version [--json]\n\nPrints the version; --json prints the "+cli.VersionSchema+" JSON document.\n")
			return exitOK
		default:
			fmt.Fprintf(stderr, "rhythm echo: version takes only --json, got %q\n", a)
			return exitUsage
		}
	}
	if !asJSON {
		fmt.Fprintln(stdout, "rhythm-echo", version)
		return exitOK
	}
	v := version
	if v == "" {
		v = "dev"
	}
	data, err := json.MarshalIndent(versionDoc{cli.VersionSchema, "rhythm-echo", v, buildCommit(), kit.Version()}, "", "  ")
	if err != nil {
		fmt.Fprintln(stderr, "rhythm echo:", err)
		return exitError
	}
	_, _ = stdout.Write(append(data, '\n'))
	return exitOK
}

// completeCommand is the hidden command the rhythm router sends shell
// completion to (kit cli.CompleteCommand).
const completeCommand = cli.CompleteCommand

// cmdComplete answers `__complete <words...>`: the words after the
// tool's name, the last one under the cursor. It prints what may come
// next, one per line, from the commands table and each command's
// flags; it runs no command and touches neither the disk nor the
// network, since the shell calls it on every Tab.
func cmdComplete(words []string, stdout io.Writer) int {
	for _, w := range completions(words) {
		fmt.Fprintln(stdout, w)
	}
	return exitOK
}

// completions is what may follow words.
func completions(words []string) []string {
	if len(words) == 0 {
		words = []string{""}
	}
	cur := words[len(words)-1]
	if len(words) == 1 {
		return cli.FilterPrefix(commandNames(), cur)
	}
	name := words[0]
	switch name {
	case "help":
		if len(words) == 2 {
			return cli.FilterPrefix(commandNames(), cur)
		}
		return nil
	case "version":
		return cli.FilterPrefix([]string{"--json", "--help"}, cur)
	case "consent":
		return consentcli.Complete(words[1:])
	}
	c, ok := lookup(name)
	if !ok {
		return nil
	}
	args := words[1:]
	flags := commandFlags(c)
	if strings.HasPrefix(cur, "-") {
		return cli.FilterPrefix(cli.FlagWords(flags), cur)
	}
	if len(args) > 1 && cli.TakesValue(flags, args[len(args)-2]) {
		return nil
	}
	if c.name == "install" && len(args) == 1 {
		return cli.FilterPrefix([]string{"wordpress", "laravel", "next", "node", "generic"}, cur)
	}
	return nil
}

// commandNames are the commands help lists, help and version.
func commandNames() []string {
	var names []string
	for _, c := range commands {
		if !c.hidden {
			names = append(names, c.name)
		}
	}
	return append(names, "help", "version")
}

// ownHelp is the help of the commands that parse their own flags, for
// completion: the same text their --help prints.
var ownHelp = map[string]string{"install": installUsage, "init": initUsage, "ca": caUsage}

// commandFlags lists the flags c reads: from the shared flag table, or
// from the help of a command that parses its own.
func commandFlags(c cmdSpec) []cli.HelpFlag {
	if c.own {
		help := ownHelp[c.name]
		if u, ok := ownUsage[c.name]; ok {
			help = u
		}
		return cli.ParseHelpFlags(help)
	}
	var out []cli.HelpFlag
	for _, f := range c.flags {
		out = append(out, leftFlags(flagDocs[f.name].left)...)
	}
	return out
}

// leftFlags reads a help line's left column, "-o, --output <file>" or
// "--json", into its flags: each spelling takes a value when the column
// ends with one.
func leftFlags(left string) []cli.HelpFlag {
	fields := strings.Fields(left)
	takes := len(fields) > 0 && !strings.HasPrefix(fields[len(fields)-1], "-")
	var out []cli.HelpFlag
	for _, f := range fields {
		if !strings.HasPrefix(f, "-") {
			continue
		}
		name := strings.TrimLeft(strings.TrimRight(f, ","), "-")
		name, _, _ = strings.Cut(name, "[")
		if name != "" {
			out = append(out, cli.HelpFlag{Name: name, TakesValue: takes})
		}
	}
	return out
}
