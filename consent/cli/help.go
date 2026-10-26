// Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
// SPDX-License-Identifier: MIT

package cli

import (
	"fmt"
	"strings"

	"github.com/RhythmShahriar/echo/consent"
	"github.com/RhythmShahriar/echo/internal/flagtext"
)

// Usage is the help for rhythm echo consent (consent --help and rhythm
// echo help consent).
var Usage = usage(subCheck)

// oneLine is the positioning line of PRD 3, shown at the top of the help.
const oneLine = "Does Reject really reject? Echo clicks your cookie banner three ways and shows\n" +
	"every tracker that fired before the choice or after Reject, with Consent Mode\n" +
	"decoded from the wire."

// subHelp is what each subcommand does and how it is called.
var subHelp = map[sub]struct{ use, about string }{
	subCheck:    {"consent [<url>...] [flags]", "check cookie consent with no choice, Accept and Reject"},
	subDetect:   {"consent detect <url> [flags]", "find the banner and the buttons to click; no verdict"},
	subReport:   {"consent report [<id>|latest] [flags]", "show a saved consent result again"},
	subVerify:   {"consent verify <folder|file> [--key <file>]", "check an evidence folder's hashes and signature"},
	subCmps:     {"consent cmps [--json]", "the consent banners Echo knows how to click"},
	subTrackers: {"consent trackers [--json]", "the trackers Echo recognizes"},
	subPrune:    {"consent prune [--keep <n>]", "delete old consent results, keeping the newest n (default 20)\nand every baseline"},
}

// exitLines are the exit codes of the consent commands, PRD 10.1 in
// Echo's words.
var exitLines = map[sub]string{
	subCheck: `Exit codes:
  0    every run made and confirmed its choice (or the page needed none) and no
       issue is at or above --fail-on
  1    an issue at or above --fail-on (with --against: a change at or above it);
       wins over 3
  2    usage error: wrong flags, a bad URL, an unknown consent: key or check
       ID, a literal header value in the config, a missing baseline,
       --send-hits on a production: host, --show-ids with Markdown or JUnit,
       an invalid selector
  3    couldn't check: no Chrome, the site didn't answer, a TLS error without
       -k, a bot wall, a redirect loop, a run that couldn't make its choice
       and nothing at or above --fail-on, a report that couldn't be written
  130  interrupted: finished runs are kept and the result is complete: false
`,
	subDetect: "Exit codes: 0 a banner and both buttons found, 1 a banner but a button missing,\n2 usage error, 3 no banner or couldn't run, 130 interrupted.\n",
	subReport: "Exit codes: as rhythm echo consent for that result: 0 ok, 1 findings, 2 usage\nerror, 3 couldn't check.\n",
	subVerify: "Exit codes: 0 every file and the signature match, 1 a hash or the signature\ndoesn't match, 2 usage error, 3 a file is missing or unreadable.\n",
}

// usage is the help of subcommand s: what it does, how to call it, the
// flags it reads, its exit codes and, for the check, the subcommands,
// examples and honest limits.
func usage(s sub) string {
	var b strings.Builder
	h := subHelp[s]
	about := strings.ReplaceAll(h.about, "\n", " ")
	// The title wraps at 80 columns like the rest; consent's own fits.
	fmt.Fprintf(&b, "%s\n\n", wrap(fmt.Sprintf("rhythm echo %s: %s", s.command(), about), 80))
	if s == subCheck {
		b.WriteString(oneLine + "\n\n")
	}
	fmt.Fprintf(&b, "Usage:\n  rhythm echo %s\n", h.use)
	if s == subCheck {
		for _, g := range helpGroups {
			fmt.Fprintf(&b, "\n%s:\n", g)
			if g == groupOutput {
				b.WriteString(helpLine("--json --md --junit --html", "the report on stdout, one format at a time"))
			}
			for _, d := range flagDefs {
				if d.group != g || d.subs&inCheck == 0 || (g == groupOutput && isFormatFlag(d.name)) {
					continue
				}
				b.WriteString(helpLine(d.left, d.help))
			}
		}
		b.WriteString("\nSubcommands (rhythm echo consent <subcommand> --help lists each one's flags):\n")
		for _, c := range subcommands {
			u := strings.TrimPrefix(subHelp[c].use, "consent ")
			u = strings.TrimSuffix(u, " [flags]")
			b.WriteString(subLine(u, subHelp[c].about))
		}
		b.WriteString("\n" + exitLines[s])
		b.WriteString("\nExamples:\n" +
			"  rhythm echo consent https://staging.shop.test\n" +
			"  rhythm echo consent --runs +gpc --geo de https://shop.example\n" +
			"  rhythm echo consent --against baseline --fail-on broke\n")
		b.WriteString("\n" + wrap(consent.HonestLimits, 80) + "\n\n")
		b.WriteString(flagtext.Footer)
		return b.String()
	}
	var own []flagDef
	for _, d := range flagDefs {
		if d.subs&s.bit() != 0 {
			own = append(own, d)
		}
	}
	if len(own) > 0 {
		b.WriteString("\nFlags:\n")
		for _, d := range own {
			b.WriteString(helpLine(d.left, d.help))
		}
	}
	lines, ok := exitLines[s]
	if !ok {
		lines = "Exit codes: 0 ok, 2 usage error, 3 couldn't run.\n"
	}
	b.WriteString("\n" + lines)
	b.WriteString("rhythm echo consent --help lists the consent check's flags.\n" + flagtext.Footer)
	return b.String()
}

// isFormatFlag reports whether name is one of --json --md --junit
// --html, which the check's help shows on one line.
func isFormatFlag(name string) bool {
	switch name {
	case fmtJSON, fmtMD, fmtJUnit, fmtHTML:
		return true
	}
	return false
}

// helpLine formats one flag as Echo's help does: the flag from column 2
// (column 6 without a short form), the text from column 29, wrapped to
// fit 80 columns, a flag too long for its column on a line of its own.
func helpLine(left, text string) string {
	if !strings.HasPrefix(left, "-") || strings.HasPrefix(left, "--") {
		left = "    " + left
	}
	return column(left, text)
}

// subLine formats one subcommand: its call from column 2, what it does
// from column 29.
func subLine(use, about string) string { return column(use, about) }

// textColumn is where help text starts; helpWidth is the widest line.
const (
	textColumn = 29
	helpWidth  = 80
)

// column writes left from column 2 and text from textColumn, wrapped.
func column(left, text string) string {
	pad := strings.Repeat(" ", textColumn)
	lines := strings.Split(wrap(strings.ReplaceAll(text, "\n", " "), helpWidth-textColumn), "\n")
	var b strings.Builder
	if len(left) >= textColumn-2 {
		fmt.Fprintf(&b, "  %s\n%s%s\n", left, pad, lines[0])
	} else {
		fmt.Fprintf(&b, "  %-*s%s\n", textColumn-2, left, lines[0])
	}
	for _, l := range lines[1:] {
		b.WriteString(pad + l + "\n")
	}
	return b.String()
}

// wrap breaks text into lines of at most width columns at spaces,
// keeping its blank lines between paragraphs.
func wrap(text string, width int) string {
	paras := strings.Split(text, "\n\n")
	for i, p := range paras {
		var lines []string
		line := ""
		for _, w := range strings.Fields(p) {
			switch {
			case line == "":
				line = w
			case len([]rune(line))+1+len([]rune(w)) <= width:
				line += " " + w
			default:
				lines = append(lines, line)
				line = w
			}
		}
		if line != "" {
			lines = append(lines, line)
		}
		paras[i] = strings.Join(lines, "\n")
	}
	return strings.Join(paras, "\n\n")
}
