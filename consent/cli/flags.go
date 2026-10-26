// Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
// SPDX-License-Identifier: MIT

package cli

import (
	"errors"
	"flag"
	"fmt"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"time"

	"github.com/RhythmShahriar/echo/config"
	"github.com/RhythmShahriar/echo/internal/flagtext"
	"github.com/RhythmShahriar/echo/model"
	"github.com/RhythmShahriar/echo/report"
	kitcli "github.com/RhythmShahriar/rhythm/kit/cli"
)

// sub names a consent subcommand; subCheck is the check itself.
type sub string

// The subcommands, by the word that picks them.
const (
	subCheck    sub = ""
	subDetect   sub = "detect"
	subReport   sub = "report"
	subVerify   sub = "verify"
	subCmps     sub = "cmps"
	subTrackers sub = "trackers"
	subPrune    sub = "prune"
)

// subcommands are the words that pick a subcommand, in help order.
var subcommands = []sub{subDetect, subReport, subVerify, subCmps, subTrackers, subPrune}

// isSub reports whether word picks a subcommand.
func isSub(word string) bool { return slices.Contains(subcommands, sub(word)) }

// command is how messages name s: "consent" or "consent cmps".
func (s sub) command() string {
	if s == subCheck {
		return "consent"
	}
	return "consent " + string(s)
}

// subSet is the set of subcommands that read a flag.
type subSet uint8

// The members of a subSet.
const (
	inCheck subSet = 1 << iota
	inDetect
	inReport
	inVerify
	inCmps
	inTrackers
	inPrune
)

// bit is s's member of a subSet.
func (s sub) bit() subSet {
	switch s {
	case subDetect:
		return inDetect
	case subReport:
		return inReport
	case subVerify:
		return inVerify
	case subCmps:
		return inCmps
	case subTrackers:
		return inTrackers
	case subPrune:
		return inPrune
	}
	return inCheck
}

// options are the parsed flags of one consent command line.
type options struct {
	// What to check
	runs, cmp, acceptSel, rejectSel, click, consentMode string
	pages, retries, parallel                            int
	settle, bannerWait, timeout                         time.Duration
	exempt, mapHost                                     kitcli.RepeatFlag
	geo, locale, timezone, viewport, sites              string
	echo                                                echoFlag
	sendHits, mapOnly                                   bool

	// Compare
	save, against string
	accept        kitcli.RepeatFlag

	// Requests and browser
	headers, cookies                     kitcli.RepeatFlag
	userAgent, proxy, browser            string
	insecure, insecurePublic, followHost bool
	noDownload, noSandbox, showBrowser   bool

	// Output
	json, md, junit, html                   bool
	outputs                                 kitcli.RepeatFlag
	har, noScreenshots, showIDs, open, live bool
	quiet, plain, noColor, verbose          bool
	signKey, failOn                         string

	// Settings
	configPath, storeDir string

	// Subcommands
	key  string // verify --key
	keep int    // prune --keep

	set map[string]bool // the long names of the flags given
}

// echoFlag is --echo[=<url>]: a switch that may also carry Echo's
// address.
type echoFlag struct {
	on  bool
	url string
}

func (e *echoFlag) String() string {
	if e == nil || !e.on {
		return "false"
	}
	if e.url != "" {
		return e.url
	}
	return "true"
}

// IsBoolFlag lets --echo stand alone.
func (*echoFlag) IsBoolFlag() bool { return true }

// Set takes true, false or Echo's URL.
func (e *echoFlag) Set(v string) error {
	switch strings.ToLower(v) {
	case "true", "1", "on":
		e.on, e.url = true, ""
	case "false", "0", "off":
		e.on, e.url = false, ""
	default:
		if !strings.HasPrefix(v, "http://") && !strings.HasPrefix(v, "https://") {
			return errors.New("want Echo's address, e.g. --echo=http://127.0.0.1:8025")
		}
		e.on, e.url = true, v
	}
	return nil
}

// flagDef is one flag of rhythm echo consent: how the help shows it and
// which subcommands read it. Every flag is defined for every
// subcommand, so one a subcommand doesn't read is "has no effect"
// rather than unknown.
type flagDef struct {
	name   string // long name
	short  string // one letter, "" when none
	left   string // the help's left column, e.g. "--runs <list>"
	help   string // the help text; \n continues it under itself
	group  string // the help group of the check itself
	subs   subSet
	define func(fs *flag.FlagSet, o *options, name string)
}

// The help groups of the check, in order.
const (
	groupWhat    = "What to check"
	groupCompare = "Compare"
	groupBrowser = "Requests and browser"
	groupOutput  = "Output"
	groupSet     = "Settings"
	groupSub     = "" // a subcommand's own flag
)

// helpGroups are the groups the check's help prints, in order.
var helpGroups = []string{groupWhat, groupCompare, groupBrowser, groupOutput, groupSet}

func str(p func(*options) *string) func(*flag.FlagSet, *options, string) {
	return func(fs *flag.FlagSet, o *options, n string) { fs.StringVar(p(o), n, "", "") }
}

func boolean(p func(*options) *bool) func(*flag.FlagSet, *options, string) {
	return func(fs *flag.FlagSet, o *options, n string) { fs.BoolVar(p(o), n, false, "") }
}

func integer(p func(*options) *int) func(*flag.FlagSet, *options, string) {
	return func(fs *flag.FlagSet, o *options, n string) { fs.IntVar(p(o), n, 0, "") }
}

func duration(p func(*options) *time.Duration) func(*flag.FlagSet, *options, string) {
	return func(fs *flag.FlagSet, o *options, n string) { fs.DurationVar(p(o), n, 0, "") }
}

func repeat(p func(*options) *kitcli.RepeatFlag) func(*flag.FlagSet, *options, string) {
	return func(fs *flag.FlagSet, o *options, n string) { fs.Var(p(o), n, "") }
}

// The subcommand sets flags share.
const (
	browserSubs = inCheck | inDetect                                             // the request and browser flags
	formatSubs  = inCheck | inReport | inVerify | inCmps | inTrackers            // --json
	reportSubs  = inCheck | inReport                                             // --md, --junit, --html, -o, --open, -v, --fail-on
	textSubs    = inCheck | inDetect | inReport | inVerify | inCmps | inTrackers // --plain, --no-color
	quietSubs   = textSubs | inPrune                                             // -q
	configSubs  = inCheck | inDetect | inReport | inTrackers | inPrune           // --config
	storeSubs   = inCheck | inDetect | inReport | inPrune                        // --store
)

// flagDefs are every flag of the consent commands, in help order.
var flagDefs = []flagDef{
	{name: "runs", left: "--runs <list>", group: groupWhat, subs: inCheck, define: str(func(o *options) *string { return &o.runs }),
		help: "none, accept, reject, gpc, withdraw (default none,accept,reject);\n+gpc or +withdraw adds to the default"},
	{name: "pages", left: "--pages <n>", group: groupWhat, subs: inCheck, define: integer(func(o *options) *int { return &o.pages }),
		help: "after the choice, visit this many more same-site pages (default 1;\n0 only reloads; at most 10)"},
	{name: "settle", left: "--settle <d>", group: groupWhat, subs: inCheck, define: duration(func(o *options) *time.Duration { return &o.settle }),
		help: "how long the network must be quiet after each step (default 3s,\nat most 30s)"},
	{name: "banner-wait", left: "--banner-wait <d>", group: groupWhat, subs: inCheck | inDetect, define: duration(func(o *options) *time.Duration { return &o.bannerWait }),
		help: "how long to wait for a banner (default 10s, at most 60s)"},
	{name: "timeout", left: "--timeout <d>", group: groupWhat, subs: inCheck, define: duration(func(o *options) *time.Duration { return &o.timeout }),
		help: "longest time for one run (default 90s, at most 10m)"},
	{name: "retries", left: "--retries <n>", group: groupWhat, subs: inCheck, define: integer(func(o *options) *int { return &o.retries }),
		help: "run an inconclusive run again in a fresh browser (default 1; 0 to 3)"},
	{name: "cmp", left: "--cmp <name>", group: groupWhat, subs: inCheck | inDetect, define: str(func(o *options) *string { return &o.cmp }),
		help: "auto (default) or the banner to expect: " + strings.Join(cmpNames[1:], ", ") + "\n(rhythm echo consent cmps lists them)"},
	{name: "accept-selector", left: "--accept-selector <sel>", group: groupWhat, subs: inCheck | inDetect, define: str(func(o *options) *string { return &o.acceptSel }),
		help: "the Accept button, overriding the rule; a >> b clicks a then b,\nhost::shadow button enters a shadow root"},
	{name: "reject-selector", left: "--reject-selector <sel>", group: groupWhat, subs: inCheck | inDetect, define: str(func(o *options) *string { return &o.rejectSel }),
		help: "the Reject button, same syntax, e.g. \"#settings >> #reject-all\""},
	{name: "click", left: "--click <how>", group: groupWhat, subs: inCheck, define: str(func(o *options) *string { return &o.click }),
		help: "button (default), api or button,api (the API when no button is found)"},
	{name: "consent-mode", left: "--consent-mode <policy>", group: groupWhat, subs: inCheck, define: str(func(o *options) *string { return &o.consentMode }),
		help: "advanced (default: cookieless pings are a notice), basic (they\ncount as hits) or strict (tracker scripts count too)"},
	{name: "exempt", left: "--exempt <tracker>", group: groupWhat, subs: inCheck, define: repeat(func(o *options) *kitcli.RepeatFlag { return &o.exempt }),
		help: "a tracker allowed before consent, by catalog id (repeatable)"},
	{name: "geo", left: "--geo <cc>", group: groupWhat, subs: inCheck | inDetect, define: str(func(o *options) *string { return &o.geo }),
		help: "look like a visitor from this country, e.g. de: language and time\nzone, never the IP address"},
	{name: "locale", left: "--locale <tag>", group: groupWhat, subs: inCheck | inDetect, define: str(func(o *options) *string { return &o.locale }),
		help: "browser locale and Accept-Language, e.g. de-DE (default the machine's)"},
	{name: "timezone", left: "--timezone <tz>", group: groupWhat, subs: inCheck | inDetect, define: str(func(o *options) *string { return &o.timezone }),
		help: "IANA time zone, e.g. Europe/Berlin (default the machine's)"},
	{name: "viewport", left: "--viewport <v>", group: groupWhat, subs: inCheck | inDetect, define: str(func(o *options) *string { return &o.viewport }),
		help: "desktop (default, 1366x900) or mobile (390x844, touch)"},
	{name: "echo", left: "--echo[=<url>]", group: groupWhat, subs: inCheck, define: func(fs *flag.FlagSet, o *options, n string) { fs.Var(&o.echo, n, "") },
		help: "also ask Echo (RHYTHM_ECHO_URL, else http://127.0.0.1:8025) what the\nsite sent server-side during each run"},
	{name: "send-hits", left: "--send-hits", group: groupWhat, subs: inCheck, define: boolean(func(o *options) *bool { return &o.sendHits }),
		help: "send tracker hits for real instead of holding them in the browser;\nrefused on a production: host"},
	{name: "sites", left: "--sites <file>", group: groupWhat, subs: inCheck, define: str(func(o *options) *string { return &o.sites }),
		help: "check every site in a Soundcheck-style sites.yaml"},
	{name: "parallel", left: "--parallel <n>", group: groupWhat, subs: inCheck, define: integer(func(o *options) *int { return &o.parallel }),
		help: "browser runs at once (default 3; 1 to 8)"},
	{name: "map-host", left: "--map-host <host=addr>", group: groupWhat, subs: inCheck | inDetect, define: repeat(func(o *options) *kitcli.RepeatFlag { return &o.mapHost }),
		help: "answer host (or *.example.test) from addr instead of the network,\ne.g. *.shop.test=127.0.0.1:18950 (repeatable)"},
	{name: "map-only", left: "--map-only", group: groupWhat, subs: inCheck | inDetect, define: boolean(func(o *options) *bool { return &o.mapOnly }),
		help: "refuse every host --map-host doesn't cover"},

	{name: "save", left: "--save <name>", group: groupCompare, subs: inCheck, define: str(func(o *options) *string { return &o.save }),
		help: "also keep this result as the baseline <name>"},
	{name: "against", left: "--against <name|path|last>", group: groupCompare, subs: reportSubs, define: str(func(o *options) *string { return &o.against }),
		help: "compare with a baseline, a consent.json, a result folder or the\nprevious result (last)"},
	{name: "accept", left: "--accept <key>", group: groupCompare, subs: reportSubs, define: repeat(func(o *options) *kitcli.RepeatFlag { return &o.accept }),
		help: "take this change (its key from -v) as intended (repeatable)"},

	{name: "header", short: "H", left: "-H, --header <h>", group: groupBrowser, subs: browserSubs, define: repeat(func(o *options) *kitcli.RepeatFlag { return &o.headers }),
		help: "extra header for same-site requests, \"Name: value\"; the value may be\n@file or env:NAME (repeatable)"},
	{name: "cookie", left: "--cookie <c>", group: groupBrowser, subs: browserSubs, define: repeat(func(o *options) *kitcli.RepeatFlag { return &o.cookies }),
		help: "a cookie for the site, name=value; the value may be @file or\nenv:NAME (repeatable)"},
	{name: "user-agent", short: "A", left: "-A, --user-agent <ua>", group: groupBrowser, subs: browserSubs, define: str(func(o *options) *string { return &o.userAgent }),
		help: "the User-Agent (default the browser's own, without Headless, plus\nrhythm-echo-consent/<version>)"},
	{name: "proxy", left: "--proxy <url>", group: groupBrowser, subs: browserSubs, define: str(func(o *options) *string { return &o.proxy }),
		help: "send the browser's traffic on through this proxy, e.g. one in the EU"},
	{name: "insecure", short: "k", left: "-k, --insecure", group: groupBrowser, subs: browserSubs, define: boolean(func(o *options) *bool { return &o.insecure }),
		help: "don't verify TLS; only for loopback or private sites"},
	{name: "insecure-public", left: "--insecure-public", group: groupBrowser, subs: browserSubs, define: boolean(func(o *options) *bool { return &o.insecurePublic }),
		help: "with -k, allow it for a public site too"},
	{name: "follow-host", left: "--follow-host", group: groupBrowser, subs: browserSubs, define: boolean(func(o *options) *bool { return &o.followHost }),
		help: "keep going when the start page redirects to another host"},
	{name: "browser", left: "--browser <path>", group: groupBrowser, subs: browserSubs, define: str(func(o *options) *string { return &o.browser }),
		help: "the Chrome or Chromium to use"},
	{name: "no-download", left: "--no-download", group: groupBrowser, subs: browserSubs, define: boolean(func(o *options) *bool { return &o.noDownload }),
		help: "never download Chrome for Testing"},
	{name: "no-sandbox", left: "--no-sandbox", group: groupBrowser, subs: browserSubs, define: boolean(func(o *options) *bool { return &o.noSandbox }),
		help: "run Chrome without its sandbox (needed as root; also\nRHYTHM_ECHO_NO_SANDBOX=1); only for sites you trust"},
	{name: "show-browser", left: "--show-browser", group: groupBrowser, subs: browserSubs, define: boolean(func(o *options) *bool { return &o.showBrowser }),
		help: "show the browser window (one run at a time)"},

	{name: "json", left: "--json", group: groupOutput, subs: formatSubs | inDetect, define: boolean(func(o *options) *bool { return &o.json }),
		help: "write JSON to stdout"},
	{name: "md", left: "--md", group: groupOutput, subs: reportSubs, define: boolean(func(o *options) *bool { return &o.md }),
		help: "write Markdown to stdout"},
	{name: "junit", left: "--junit", group: groupOutput, subs: reportSubs, define: boolean(func(o *options) *bool { return &o.junit }),
		help: "write JUnit XML to stdout"},
	{name: "html", left: "--html", group: groupOutput, subs: reportSubs, define: boolean(func(o *options) *bool { return &o.html }),
		help: "write the HTML report to stdout"},
	{name: "output", short: "o", left: "-o, --output <file>", group: groupOutput, subs: reportSubs, define: repeat(func(o *options) *kitcli.RepeatFlag { return &o.outputs }),
		help: "also write a report to a file, its format from .json .md .xml or\n.html (repeatable)"},
	{name: "har", left: "--har", group: groupOutput, subs: inCheck, define: boolean(func(o *options) *bool { return &o.har }),
		help: "keep one masked HAR per run in the evidence folder"},
	{name: "no-screenshots", left: "--no-screenshots", group: groupOutput, subs: inCheck, define: boolean(func(o *options) *bool { return &o.noScreenshots }),
		help: "take no screenshots"},
	{name: "show-ids", left: "--show-ids", group: groupOutput, subs: inCheck, define: boolean(func(o *options) *bool { return &o.showIDs }),
		help: "keep tracking and client IDs unmasked (not with Markdown or JUnit)"},
	{name: "sign-key", left: "--sign-key <file>", group: groupOutput, subs: inCheck, define: str(func(o *options) *string { return &o.signKey }),
		help: "sign consent.json with this ed25519 private key (mode 0600)"},
	{name: "open", left: "--open", group: groupOutput, subs: reportSubs, define: boolean(func(o *options) *bool { return &o.open }),
		help: "open the HTML report when done"},
	{name: "quiet", short: "q", left: "-q, --quiet", group: groupOutput, subs: quietSubs, define: boolean(func(o *options) *bool { return &o.quiet }),
		help: "only the verdict and what failed"},
	{name: "plain", left: "--plain", group: groupOutput, subs: textSubs, define: boolean(func(o *options) *bool { return &o.plain }),
		help: "no color or glyphs (screen readers, logs)"},
	{name: "no-color", left: "--no-color", group: groupOutput, subs: textSubs, define: boolean(func(o *options) *bool { return &o.noColor }),
		help: "no color"},
	{name: "verbose", short: "v", left: "-v, --verbose", group: groupOutput, subs: reportSubs, define: boolean(func(o *options) *bool { return &o.verbose }),
		help: "add every run's timeline and the change keys"},
	{name: "live", left: "--live", group: groupOutput, subs: inCheck, define: boolean(func(o *options) *bool { return &o.live }),
		help: "print each tracker hit on stderr as it happens"},
	{name: "fail-on", left: "--fail-on <level>", group: groupOutput, subs: reportSubs, define: str(func(o *options) *string { return &o.failOn }),
		help: "exit 1 at or above error (the default), warning or notice; with\n--against broke (the default), new or changed; none: never"},

	{name: "config", left: "--config <file>", group: groupSet, subs: configSubs, define: str(func(o *options) *string { return &o.configPath }),
		help: "settings (default the nearest .echo/config.yaml; optional)"},
	{name: "store", left: "--store <dir>", group: groupSet, subs: storeSubs, define: str(func(o *options) *string { return &o.storeDir }),
		help: "where results are kept (default the config's folder, else ./.echo)"},

	{name: "key", left: "--key <file>", group: groupSub, subs: inVerify, define: str(func(o *options) *string { return &o.key }),
		help: "the ed25519 public key (PEM) to check consent.json.sig with"},
	{name: "keep", left: "--keep <n>", group: groupSub, subs: inPrune, define: integer(func(o *options) *int { return &o.keep }),
		help: "how many consent results to keep (default 20); baselines always stay"},
}

// cmpNames are the --cmp values: auto, then every CMP rule PRD 7 names.
var cmpNames = config.ConsentCMPs

// lookupDef returns the definition of the long flag name.
func lookupDef(name string) (flagDef, bool) {
	i := slices.IndexFunc(flagDefs, func(d flagDef) bool { return d.name == name })
	if i < 0 {
		return flagDef{}, false
	}
	return flagDefs[i], true
}

// longName maps a one-letter flag to its long name.
func longName(name string) string {
	for _, d := range flagDefs {
		if d.short == name {
			return d.name
		}
	}
	return name
}

// newFlagSet defines every consent flag on a new set writing into o.
func newFlagSet(o *options) *flag.FlagSet {
	fs := flag.NewFlagSet("consent", flag.ContinueOnError)
	fs.SetOutput(discard{})
	for _, d := range flagDefs {
		d.define(fs, o, d.name)
		if d.short != "" {
			d.define(fs, o, d.short)
		}
	}
	return fs
}

// discard is io.Discard for the flag package, which prints its own
// errors otherwise; the CLI words them itself.
type discard struct{}

func (discard) Write(p []byte) (int, error) { return len(p), nil }

// subOf finds the subcommand args ask for: the first word that isn't a
// flag or a flag's value, when it is a subcommand's name.
func subOf(fs *flag.FlagSet, args []string) sub {
	for i := 0; i < len(args); i++ {
		a := args[i]
		if a == "--" {
			return subCheck
		}
		if len(a) > 1 && strings.HasPrefix(a, "-") {
			name, _, hasValue := strings.Cut(strings.TrimLeft(a, "-"), "=")
			if f := fs.Lookup(name); f != nil && !hasValue && !isBoolFlag(f) {
				i++ // its value
			}
			continue
		}
		if isSub(a) {
			return sub(a)
		}
		return subCheck
	}
	return subCheck
}

func isBoolFlag(f *flag.Flag) bool {
	b, ok := f.Value.(interface{ IsBoolFlag() bool })
	return ok && b.IsBoolFlag()
}

// parse reads args for subcommand s (whose word, when s isn't the
// check, is the first positional and is dropped). It returns
// flag.ErrHelp for -h and --help.
func parse(s sub, args []string) (*options, []string, error) {
	o := &options{set: map[string]bool{}}
	fs := newFlagSet(o)
	pos, err := kitcli.ParseInterspersed(fs, args)
	if err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return nil, nil, err
		}
		return nil, nil, flagError(err, fs, s)
	}
	if s != subCheck && len(pos) > 0 && pos[0] == string(s) {
		pos = pos[1:]
	}
	var bad []string
	fs.Visit(func(f *flag.Flag) {
		name := longName(f.Name)
		o.set[name] = true
		if d, ok := lookupDef(name); ok && d.subs&s.bit() == 0 && !slices.Contains(bad, name) {
			bad = append(bad, name)
		}
	})
	if len(bad) > 0 {
		return nil, nil, noEffect(s, bad)
	}
	return o, pos, nil
}

// flagError words an error from the flag package as Echo does. An
// unknown flag gets the one it most likely meant, from those s reads
// first.
func flagError(err error, fs *flag.FlagSet, s sub) error {
	if m := unknownFlag.FindStringSubmatch(err.Error()); m != nil {
		var own, all []string
		for _, d := range flagDefs {
			all = append(all, d.name)
			if d.subs&s.bit() != 0 {
				own = append(own, d.name)
			}
		}
		msg := "unknown flag " + flagtext.Dash(m[1])
		if hint := flagtext.Closest(m[1], own); hint != "" {
			msg += "; did you mean " + flagtext.Dash(hint) + "?"
		} else if hint := flagtext.Closest(m[1], all); hint != "" {
			msg += "; did you mean " + flagtext.Dash(hint) + "?"
		}
		return kitcli.UsageError{Msg: msg}
	}
	return kitcli.UsageError{Msg: flagtext.Error(err, fs)}
}

var unknownFlag = regexp.MustCompile(`^flag provided but not defined: -+(.+)$`)

// noEffect is Echo's error for flags a command doesn't read.
func noEffect(s sub, names []string) error {
	spelled := make([]string, len(names))
	for i, n := range names {
		spelled[i] = flagtext.Dash(n)
	}
	verb := "has"
	if len(names) > 1 {
		verb = "have"
	}
	return kitcli.Usagef("%s %s no effect on %s (rhythm echo %s --help lists its flags)", andList(spelled), verb, s.command(), s.command())
}

// andList joins items as "a", "a and b" or "a, b and c".
func andList(items []string) string {
	if len(items) < 2 {
		return strings.Join(items, "")
	}
	return strings.Join(items[:len(items)-1], ", ") + " and " + items[len(items)-1]
}

// failOnNone never fails for findings; an inconclusive run still exits 3.
const failOnNone = "none"

// checkFailOn reads --fail-on in any case as Echo's other commands do
// (the same function and the same words as cmd/rhythm-echo's
// checkFailOn, which this copies since that one lives in package main)
// and, when it isn't given, returns the default: broke when comparing,
// error otherwise.
func checkFailOn(failOn string, compared bool) (string, error) {
	f := strings.ToLower(strings.TrimSpace(failOn))
	switch f {
	case "":
		if compared {
			return model.Broke, nil
		}
		return string(model.Error), nil
	case failOnNone:
		return f, nil
	}
	if report.ValidFailOn(f, compared) != nil {
		if compared {
			return "", kitcli.Usagef("--fail-on %q: want none, broke, new or changed", f)
		}
		return "", kitcli.Usagef("--fail-on %q: want none, error, warning or notice (or broke, new, changed when comparing)", f)
	}
	return f, nil
}

// Output formats, by the names the flags use.
const (
	fmtJSON  = "json"
	fmtMD    = "md"
	fmtJUnit = "junit"
	fmtHTML  = "html"
)

// output is one report to write: a format and a file, or stdout when
// path is "".
type output struct{ format, path string }

// formatFromExt is the format a file's extension asks for, or "".
func formatFromExt(path string) string {
	switch strings.ToLower(filepath.Ext(path)) {
	case ".json":
		return fmtJSON
	case ".md", ".markdown":
		return fmtMD
	case ".xml":
		return fmtJUnit
	case ".html", ".htm":
		return fmtHTML
	}
	return ""
}

// reports resolves -o and the format flags. Each -o takes its format
// from its extension; a format flag no -o of that format uses goes to
// stdout; only one may. A file whose extension says nothing takes
// the one format flag given, as -o does in Echo's other commands.
func (o *options) reports() ([]output, error) {
	flags := map[string]bool{fmtJSON: o.json, fmtMD: o.md, fmtJUnit: o.junit, fmtHTML: o.html}
	var out []output
	claimed := map[string]bool{}
	var unknown []string
	for _, p := range o.outputs {
		f := formatFromExt(p)
		if f == "" {
			unknown = append(unknown, p)
			continue
		}
		out = append(out, output{f, p})
		claimed[f] = true
	}
	var given []string
	for _, f := range []string{fmtHTML, fmtJSON, fmtJUnit, fmtMD} {
		if flags[f] {
			given = append(given, f)
		}
	}
	for _, p := range unknown {
		if len(given) != 1 {
			return nil, kitcli.Usagef("can't tell the format of %s; add --json, --md, --junit or --html", p)
		}
		out = append(out, output{given[0], p})
		claimed[given[0]] = true
	}
	var stdout []string
	for _, f := range given {
		if !claimed[f] {
			stdout = append(stdout, f)
		}
	}
	if len(stdout) > 1 {
		return nil, kitcli.Usagef("pick one output format, not --%s", strings.Join(stdout, " and --"))
	}
	if len(stdout) == 1 {
		out = append(out, output{stdout[0], ""})
	}
	return out, nil
}

// stdoutFormat is the format written to stdout, or "".
func stdoutFormat(outs []output) string {
	for _, o := range outs {
		if o.path == "" {
			return o.format
		}
	}
	return ""
}

// wants reports whether any output asks for format f.
func wants(outs []output, f string) bool {
	return slices.ContainsFunc(outs, func(o output) bool { return o.format == f })
}

// usagef is a usage error (exit 2).
func usagef(format string, a ...any) error { return kitcli.UsageError{Msg: fmt.Sprintf(format, a...)} }
