// Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
// SPDX-License-Identifier: MIT

package main

import (
	"cmp"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"time"

	"github.com/RhythmShahriar/echo/check"
	"github.com/RhythmShahriar/echo/client"
	"github.com/RhythmShahriar/echo/config"
	"github.com/RhythmShahriar/echo/model"
	"github.com/RhythmShahriar/echo/runner"
	"github.com/RhythmShahriar/echo/sendmail"
	"github.com/RhythmShahriar/echo/store"
	"github.com/RhythmShahriar/rhythm/kit/cli"
	"gopkg.in/yaml.v3"
)

type options struct {
	json, md, junit, html          bool
	output                         string
	open                           bool
	quiet, plain, noColor, verbose bool
	failOn                         string

	configPath, url, token string
	storeDir               string // --store: where snapshots and runs go
	name, snapshot         string
	actions                cli.RepeatFlag
	force                  bool           // snapshot: save even when every action failed; with --import, replace
	export, importFile     string         // snapshot: write one to, or add one from, a .tgz
	against                string         // compare: a baseline from anywhere
	accept                 cli.RepeatFlag // compare: keys of changes that are intended
	parallel               int            // snapshot, compare and run: actions at once

	seal, observe bool
	listen, smtp  string
	proxy         string
	mitm          bool
	allow         cli.ListFlag

	// up: where and how the catcher may listen, who may view it and
	// where it keeps what it catches
	privateNetwork, freePorts bool
	login                     string
	keep                      int
	keepFor                   time.Duration
	spamassassin              string
	sites                     string       // up: sites.yaml with a write token per site
	hostnames                 cli.ListFlag // up: more names Echo answers to without a token

	channel, runID, to string
	since              time.Duration // ls and watch: also the messages from this long ago; report --site: the period

	// check, headers and replay
	fromMail, provider, selector, returnPath, smtpHost, resolver string
	mtaSTSFetch, withDNS, hops, list                             bool
	sets, headers                                                cli.RepeatFlag
	secretEnv, scheme, typ                                       string
	wait                                                         time.Duration

	// snapshot, compare and run: --quiet <d> and --timeout <d> override
	// the config's quiet: and timeout:. Each set flag is true when given.
	quietFor, timeout       time.Duration
	quietForSet, timeoutSet bool

	test bool // checks: try the config's rules on their fixtures

	// report --site: the agency report over a hub site's update windows
	site, brandName, brandLogo, brandColor string
	pdf                                    bool

	// latest
	code, link bool
	kind       string
	channels   []model.Channel // --channel, which latest takes as a comma-separated list
	waitSet    bool            // --wait was given: latest's default differs from replay's
}

func flagSet(o *options) *flag.FlagSet {
	fs := flag.NewFlagSet("echo", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	both := func(short, long string, f func(string)) { f(short); f(long) }
	fs.BoolVar(&o.json, "json", false, "")
	fs.BoolVar(&o.md, "md", false, "")
	fs.BoolVar(&o.junit, "junit", false, "")
	fs.BoolVar(&o.html, "html", false, "")
	both("o", "output", func(n string) { fs.StringVar(&o.output, n, "", "") })
	fs.BoolVar(&o.open, "open", false, "")
	both("q", "quiet", func(n string) { fs.Var(quietFlag{o}, n, "") })
	fs.BoolVar(&o.plain, "plain", false, "")
	fs.BoolVar(&o.noColor, "no-color", false, "")
	both("v", "verbose", func(n string) { fs.BoolVar(&o.verbose, n, false, "") })
	fs.StringVar(&o.failOn, "fail-on", "", "")

	fs.StringVar(&o.configPath, "config", "", "")
	fs.StringVar(&o.storeDir, "store", "", "")
	fs.StringVar(&o.url, "url", "", "")
	fs.StringVar(&o.token, "token", "", "")
	fs.StringVar(&o.name, "name", "", "")
	fs.StringVar(&o.snapshot, "snapshot", "", "")
	fs.Var(&o.actions, "action", "")
	fs.BoolVar(&o.force, "force", false, "")
	fs.StringVar(&o.export, "export", "", "")
	fs.StringVar(&o.importFile, "import", "", "")
	fs.StringVar(&o.against, "against", "", "")
	fs.Var(&o.accept, "accept", "")
	fs.IntVar(&o.parallel, "parallel", 0, "")

	fs.BoolVar(&o.seal, "seal", false, "")
	fs.BoolVar(&o.observe, "observe", false, "")
	fs.StringVar(&o.listen, "listen", "", "")
	fs.StringVar(&o.smtp, "smtp", "", "")
	fs.StringVar(&o.proxy, "proxy", "", "")
	fs.BoolVar(&o.mitm, "mitm", false, "")
	fs.Var(&o.allow, "allow", "")
	fs.BoolVar(&o.privateNetwork, "private-network", false, "")
	fs.BoolVar(&o.freePorts, "free-ports", false, "")
	fs.StringVar(&o.login, "login", "", "")
	fs.IntVar(&o.keep, "keep", 0, "")
	fs.Var(daysFlag{&o.keepFor, errDaysValue}, "keep-for", "")
	fs.StringVar(&o.spamassassin, "spamassassin", "", "")
	fs.StringVar(&o.sites, "sites", "", "")
	fs.Var(&o.hostnames, "hostname", "")

	fs.StringVar(&o.channel, "channel", "", "")
	fs.StringVar(&o.runID, "run", "", "")
	fs.StringVar(&o.to, "to", "", "")
	fs.Var(daysFlag{&o.since, errSinceValue}, "since", "")

	fs.StringVar(&o.fromMail, "from-mail", "", "")
	fs.StringVar(&o.provider, "provider", "", "")
	fs.StringVar(&o.selector, "selector", "", "")
	fs.StringVar(&o.returnPath, "return-path", "", "")
	fs.StringVar(&o.smtpHost, "smtp-host", "", "")
	fs.StringVar(&o.resolver, "resolver", "", "")
	fs.BoolVar(&o.mtaSTSFetch, "mta-sts-fetch", false, "")
	fs.BoolVar(&o.withDNS, "check", false, "")
	fs.BoolVar(&o.hops, "hops", false, "")
	fs.BoolVar(&o.list, "list", false, "")
	fs.Var(&o.sets, "set", "")
	both("H", "header", func(n string) { fs.Var(&o.headers, n, "") })
	fs.StringVar(&o.secretEnv, "secret-env", "", "")
	fs.StringVar(&o.scheme, "scheme", "", "")
	fs.StringVar(&o.typ, "type", "", "")
	fs.DurationVar(&o.wait, "wait", 5*time.Second, "")
	fs.DurationVar(&o.timeout, "timeout", 0, "")
	fs.BoolVar(&o.test, "test", false, "")
	fs.StringVar(&o.site, "site", "", "")
	fs.BoolVar(&o.pdf, "pdf", false, "")
	fs.StringVar(&o.brandName, "brand-name", "", "")
	fs.StringVar(&o.brandLogo, "brand-logo", "", "")
	fs.StringVar(&o.brandColor, "brand-color", "", "")
	fs.BoolVar(&o.code, "code", false, "")
	fs.BoolVar(&o.link, "link", false, "")
	fs.StringVar(&o.kind, "kind", "", "")
	return fs
}

// plan is a command's validated settings.
type plan struct {
	o       options
	args    []string
	cfg     config.Config
	began   time.Time     // when the command started, before anything could hold it up
	checks  check.Options // what the config expects of every message: its site, rules: and checks:
	cfgPath string        // the config file read, absolute; "" when there is none and the defaults apply
	notes   []string      // said on stderr before the command runs
	// fixtures is the config's failing rule fixture, which only checks
	// reads past (config.ErrFixtures); nil otherwise.
	fixtures error
	format   string
	st       store.Store
	echo     *client.Client
}

func parse(c cmdSpec, args []string) (*plan, error) {
	p := &plan{began: time.Now()}
	o := &p.o
	fs := flagSet(o)
	if c.accepts("timeout") {
		args = joinQuietValue(args)
	}
	pos, err := cli.ParseInterspersed(fs, args)
	if err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return nil, err
		}
		return nil, flagError(err, c)
	}
	if bad := c.unused(fs); len(bad) > 0 {
		return nil, noEffect(c.name, bad)
	}
	o.timeoutSet, o.waitSet = visited(fs, "timeout"), visited(fs, "wait")
	if o.quietForSet && !c.accepts("timeout") {
		return nil, cli.Usagef("--quiet %s: only snapshot, compare and run take a duration; -q alone means results only", shortDuration(o.quietFor))
	}
	p.args = pos
	path := o.configPath
	if path != "" {
		if _, err := os.Stat(path); err != nil {
			return nil, cli.Usagef("--config %s: %v", path, err)
		}
	} else if path, err = findConfig("."); errors.Is(err, errNoConfig) {
		path = "" // the defaults; commands that need actions say so
	} else if ue := (*sendmail.UntrustedError)(nil); errors.As(err, &ue) {
		// Fall back to the defaults, saying why, rather than refuse: ls
		// or latest shouldn't stop working because of someone else's file.
		path, p.notes = "", append(p.notes, fmt.Sprintf("%v (someone else could have put it there); "+
			"pass --config %s if you trust it", ue, ue.Path))
	} else if err != nil {
		return nil, err
	}
	if path != "" {
		if path, err = filepath.Abs(path); err != nil {
			return nil, err
		}
	}
	p.cfgPath = path
	// snapshot --export and --import run no actions, so where they would
	// run doesn't matter.
	if runsCommands[c.name] && o.configPath == "" && path != "" && !o.quiet && o.export == "" && o.importFile == "" {
		if wd, err := os.Getwd(); err == nil && p.projectDir() != wd {
			p.notes = append(p.notes, fmt.Sprintf("using %s; its flush and command actions run in %s", path, p.projectDir()))
		}
	}
	if p.cfg, err = loadConfig(path); errors.Is(err, config.ErrFixtures) && c.name == "checks" {
		p.fixtures = err // checks reports it; its config is complete
	} else if err != nil {
		return nil, cli.UsageError{Msg: err.Error()}
	}
	switch {
	case o.seal && o.observe:
		return nil, cli.Usagef("pick --seal or --observe, not both")
	case o.seal:
		p.cfg.Mode = "seal"
	case o.observe:
		p.cfg.Mode = "observe"
	}
	if o.listen != "" {
		p.cfg.Listen = o.listen
	}
	if o.smtp != "" {
		p.cfg.SMTP = o.smtp
	}
	if o.proxy != "" {
		p.cfg.Proxy = o.proxy
	}
	if o.mitm {
		p.cfg.MITM = true
	}
	p.cfg.Allow = append(p.cfg.Allow, o.allow...)
	if p.checks, err = runner.CheckOptions(p.cfg); err != nil {
		return nil, cli.UsageError{Msg: fmt.Sprintf("%s: %v", p.configName(), err)}
	}
	if o.parallel < 0 {
		return nil, cli.Usagef("--parallel %d: give how many actions may run at once, e.g. 4", o.parallel)
	}
	if err := p.overrideWaits(); err != nil {
		return nil, err
	}

	var chosen []string
	for name, on := range map[string]bool{"json": o.json, "md": o.md, "junit": o.junit, "html": o.html} {
		if on {
			chosen = append(chosen, name)
		}
	}
	switch {
	case len(chosen) > 1:
		slices.Sort(chosen)
		return nil, cli.Usagef("pick one output format, not --%s", strings.Join(chosen, " and --"))
	case len(chosen) == 1:
		p.format = chosen[0]
	case o.output != "":
		if p.format = formatFromExt(o.output); p.format == "" {
			return nil, cli.Usagef("can't tell the format of %s; add --json, --md, --junit or --html", o.output)
		}
	}
	if c.name == "latest" {
		for _, ch := range strings.Split(o.channel, ",") {
			if ch = strings.TrimSpace(ch); ch == "" {
				continue
			}
			if !slices.Contains(channels, model.Channel(ch)) {
				return nil, cli.Usagef("--channel %q: use one or more of %s, comma-separated", ch, joinChannels())
			}
			o.channels = append(o.channels, model.Channel(ch))
		}
	} else if o.channel != "" && !slices.Contains(channels, model.Channel(o.channel)) {
		return nil, cli.Usagef("--channel %q: use one of %s", o.channel, joinChannels())
	}
	if o.since < 0 {
		return nil, cli.Usagef("--since %s: give how long ago, e.g. 10m", shortDuration(o.since))
	}
	if o.name != "" && !snapshotName(o.name) {
		return nil, cli.Usagef("--name %q: use letters, digits, dots, dashes and underscores (LATEST is taken)", o.name)
	}
	p.st = store.Store{Root: storeRoot(o.storeDir, path)}
	url := cmp.Or(o.url, os.Getenv("RHYTHM_ECHO_URL"), "http://"+p.cfg.Listen)
	p.echo = client.New(url, cmp.Or(o.token, os.Getenv("RHYTHM_ECHO_TOKEN")))
	return p, nil
}

// runsCommands are the commands that run the config's flush: and
// command actions. From a subfolder they say which config they use, as
// it may not be the one the user expects.
var runsCommands = map[string]bool{"snapshot": true, "compare": true, "run": true, "doctor": true}

// errNoConfig is the error when no folder from here up has a
// .echo/config.yaml. It stands in for config.ErrNoConfig until that is
// released, with the same words.
var errNoConfig = errors.New("no .echo/config.yaml here or above; run rhythm echo init")

// findConfig returns the absolute path of the nearest .echo/config.yaml
// in start or a folder above it, within sendmail.FindConfig's bounds:
// the repository root, below $HOME and only folders that are yours and
// that others can't write to. A config it won't use is a
// *sendmail.UntrustedError.
//
// This stands in for config.Find until that is released.
func findConfig(start string) (string, error) {
	path, err := sendmail.FindConfig(start)
	if errors.Is(err, sendmail.ErrNoConfig) {
		return "", errNoConfig
	}
	return path, err
}

// loadConfig reads the config at path, or returns the defaults when
// path is "".
func loadConfig(path string) (config.Config, error) {
	if path == "" {
		return config.Default(), nil
	}
	return config.Load(path)
}

// projectDir is the folder the config belongs to: proj for
// proj/.echo/config.yaml, else the working folder (a --config file kept
// elsewhere, or none at all).
func (p *plan) projectDir() string {
	if p.cfgPath != "" && filepath.Base(filepath.Dir(p.cfgPath)) == config.Dir {
		return filepath.Dir(filepath.Dir(p.cfgPath))
	}
	return "."
}

// storeRoot is where snapshots and runs are kept: --store when given,
// else the folder the config is in (proj/.echo for proj/.echo/config.yaml,
// found from proj/sub or named with --config), else ./.echo.
func storeRoot(flag, cfgPath string) string {
	switch {
	case flag != "":
		return flag
	case cfgPath != "":
		return filepath.Dir(cfgPath)
	}
	return config.Dir
}

// needConfig is errNoConfig, as a usage error, when no config was found:
// snapshot, compare, run and doctor have nothing to do without one.
func (p *plan) needConfig() error {
	if p.cfgPath == "" {
		return cli.UsageError{Msg: errNoConfig.Error()}
	}
	return nil
}

// configName is the config file as messages name it: relative to the
// working folder when it's below it.
func (p *plan) configName() string {
	if p.cfgPath == "" {
		return config.Path()
	}
	return rel(p.cfgPath)
}

// channels are the values --channel accepts.
var channels = []model.Channel{model.Email, model.SMS, model.WhatsApp, model.Webhook, model.Chat, model.Analytics,
	model.Payment, model.Push, model.HTTP}

func joinChannels() string {
	var names []string
	for _, c := range channels {
		names = append(names, string(c))
	}
	return strings.Join(names, ", ")
}

func formatFromExt(path string) string {
	switch strings.ToLower(filepath.Ext(path)) {
	case ".json":
		return "json"
	case ".md", ".markdown":
		return "md"
	case ".xml":
		return "junit"
	case ".html", ".htm":
		return "html"
	}
	return ""
}

// tokenFlagWarning is printed when --token is given on the command line,
// where any local user can read it with ps. The environment variable is
// only visible to the same user (and root).
const tokenFlagWarning = "rhythm echo: --token shows in ps to every user on this machine; set RHYTHM_ECHO_TOKEN instead" //nolint:gosec // a message about tokens, not one

// warnTokenFlag prints tokenFlagWarning when the token came from the flag.
func warnTokenFlag(stderr io.Writer, token string, quiet bool) {
	if token != "" && !quiet {
		fmt.Fprintln(stderr, tokenFlagWarning)
	}
}

func usageErr(format string, a ...any) error { return cli.UsageError{Msg: fmt.Sprintf(format, a...)} }

// cmdSpec describes one command: its names, its help and the shared
// flags it reads. parse rejects any other shared flag, so a flag that
// would do nothing (ls --fail-on) is an error rather than ignored.
type cmdSpec struct {
	name    string
	aliases []string
	use     string    // what follows "rhythm echo" on the usage line
	about   string    // what it does, in a line
	flags   []flagUse // the shared flags it reads, in help order
	own     bool      // parses its own flags (install, init, ca, sendmail)
	hidden  bool      // works but isn't listed (update)
}

// flagUse is a flag a command accepts. doc replaces the flag's general
// help line when the command uses it differently; "" keeps flagDocs'.
type flagUse struct{ name, doc string }

// flagDoc is a flag's help: what goes left (with its value) and the text.
type flagDoc struct{ left, text string }

// flagDocs is the general help line of each shared flag.
var flagDocs = map[string]flagDoc{
	"json":     {"--json", "write the report as JSON"},
	"md":       {"--md", "write the report as Markdown"},
	"junit":    {"--junit", "write the report as JUnit XML, for CI test views"},
	"html":     {"--html", "write the report as HTML"},
	"output":   {"-o, --output <file>", "write the report to a file (format from the extension)"},
	"open":     {"--open", "open the HTML report when done"},
	"quiet":    {"-q, --quiet", "results only"},
	"plain":    {"--plain", "no color, boxes or glyphs (screen readers, logs)"},
	"no-color": {"--no-color", "no color"},
	"verbose":  {"-v, --verbose", "show every message"},
	"fail-on":  {"--fail-on <level>", "exit 1 on issues at or above error (the default), warning or notice;\nnone: never. A failed action also exits 1, unless none"},

	"config":   {"--config <file>", "settings (default the nearest .echo/config.yaml, here or above)"},
	"store":    {"--store <dir>", "where snapshots and runs are kept (default the config's folder)"},
	"url":      {"--url <url>", "where Echo runs (default http://127.0.0.1:8025; RHYTHM_ECHO_URL)"},
	"token":    {"--token <t>", "the token Echo was started with (prefer RHYTHM_ECHO_TOKEN)"},
	"name":     {"--name <name>", "the snapshot's name (default the date and time)"},
	"snapshot": {"--snapshot <name>", "which snapshot to compare with (default the latest)"},
	"action":   {"--action <name>", "run only this action (repeatable)"},
	"force":    {"--force", "save the snapshot even when every action failed"},
	"export":   {"--export <file.tgz>", "write a snapshot (--name, default the latest) to a .tgz,\nto compare with elsewhere; - writes it to stdout"},
	"import":   {"--import <file.tgz>", "add a snapshot from --export and make it the latest\n(--name renames it)"},
	"accept":   {"--accept <key>", "take the change with this key (compare -v) as intended (repeatable)"},
	"parallel": {"--parallel <n>", "run up to n actions at once (one with parallel: false runs alone)"},
	"against":  {"--against <path>", "compare with this run instead: a run folder, its run.json\nor a .tgz written by snapshot's export"},

	"seal":    {"--seal", "nothing leaves; Echo answers for every service (default)"},
	"observe": {"--observe", "record everything and send it for real"},
	"listen":  {"--listen <addr>", "API address (default 127.0.0.1:8025)"},
	"smtp":    {"--smtp <addr>", `SMTP sink (default 127.0.0.1:1025; "off" disables)`},
	"proxy":   {"--proxy <addr>", "also run an HTTP(S) proxy, e.g. 127.0.0.1:8026"},
	"mitm":    {"--mitm", "decrypt HTTPS in the proxy (trust the CA: rhythm echo ca)"},
	"allow":   {"--allow <hosts>", "hosts reachable even when sealed, e.g. api.stripe.com"},

	"private-network": {"--private-network", "listen beyond loopback without a token, on private (RFC 1918)\nand container networks only; prints a warning"},
	"free-ports":      {"--free-ports", "when 8025 or 1025 is taken (Mailpit in DDEV, Lando, Sail), move to\nthe next free pair from 18025/11025 and save it to the config\nwithout asking"},
	"login":           {"--login <user:pass>", "let viewers sign in to Echo Inbox, read only, for 8 hours\n(with relay: they may also release an email). Prefer\nRHYTHM_ECHO_LOGIN: other users can read a flag in ps"},
	"keep":            {"--keep <n>", "the most messages kept (default 10000; never more than\n256 MiB of them)"},
	"keep-for":        {"--keep-for <d>", "with --store: drop messages older than this, e.g. 14d"},
	"sites":           {"--sites <file>", "give each site its own write token (captures only), from a\nsites.yaml as rhythm echo hub reads it; its read token reads all"},
	"hostname":        {"--hostname <name>", "also answer to this name without a token, e.g. *.ddev.site\n(repeatable; RHYTHM_ECHO_HOSTNAMES, comma-separated)"},
	"spamassassin":    {"--spamassassin <addr>", "score each email with the SpamAssassin daemon at host:port\n(spamd; port 783 when left out)"},

	"channel": {"--channel <c>", "only email, sms, whatsapp, webhook, chat, analytics, payment,\npush or http"},
	"run":     {"--run <id>", "only messages from this run"},
	"to":      {"--to <address>", "only messages to this recipient"},
	"since":   {"--since <d>", "only messages from this long ago on, e.g. 10m"},

	"from-mail":     {"--from-mail <file.eml>", "learn the real sender from a received email"},
	"provider":      {"--provider <name>", "or name it: sendgrid, mailgun, postmark, ses, resend, brevo,\nmailchimp, mandrill, sparkpost, google, microsoft, php-mail"},
	"selector":      {"--selector <s>", "DKIM selector to check"},
	"return-path":   {"--return-path <domain>", "the envelope sender's domain, which SPF checks"},
	"smtp-host":     {"--smtp-host <host>", "the server that hands mail out, a name or an IP"},
	"mta-sts-fetch": {"--mta-sts-fetch", "also fetch the MTA-STS policy file over HTTPS"},
	"resolver":      {"--resolver <host:port>", "DNS server to ask (default the system's)"},
	"check":         {"--check", "also run the DNS checks for the sender"},
	"hops":          {"--hops", "show the route the message took"},

	"set":         {"--set <path=value>", "override a field, e.g. data.object.amount=1999 (repeatable)"},
	"header":      {"-H, --header <h>", `extra header, "Name: value" (repeatable)`},
	"secret-env":  {"--secret-env <NAME>", "env var with the signing secret (default per scheme,\ne.g. ECHO_STRIPE_WEBHOOK_SECRET)"},
	"scheme":      {"--scheme <s>", "for JSON files: stripe, standard, github, woocommerce, hmac"},
	"type":        {"--type <t>", `for JSON files: the event type (default the payload's "type")`},
	"list":        {"--list", "list the built-in events"},
	"wait":        {"--wait <d>", "how long to collect what the site sends back (default 5s)"},
	"code":        {"--code", "print the one-time code (the default, else the first link)"},
	"link":        {"--link", "print the first link instead (magic, verify, reset, login or\nunsubscribe)"},
	"kind":        {"--kind <k>", "with --link: only this kind of link, e.g. magic or reset"},
	"site":        {"--site <id>", "the agency report for this hub site: did its update windows\nbreak anything (--store is the hub's store)"},
	"pdf":         {"--pdf", "with --site: also print it to a PDF (needs Chrome or Chromium)"},
	"brand-name":  {"--brand-name <name>", "with --site: the agency's name on the report"},
	"brand-logo":  {"--brand-logo <file>", "with --site: the agency's logo (PNG, JPEG, GIF, WebP or SVG)"},
	"brand-color": {"--brand-color <#hex>", "with --site: the report's accent color, e.g. #1F6FEB"},
	"test":        {"--test", "try each of the config's rules on its fixtures; exit 1 when one fails"},
	"timeout":     {"--timeout <d>", "longest wait per action (overrides timeout:, at most 1m)"},
}

// Flag groups several commands share.
var (
	reportFlags = []flagUse{{"json", ""}, {"md", ""}, {"junit", ""}, {"html", ""}, {"output", ""}, {"open", ""},
		{"quiet", ""}, {"plain", ""}, {"no-color", ""}, {"verbose", ""}}
	echoFlags     = []flagUse{{"config", ""}, {"url", ""}, {"token", ""}}
	storeFlags    = []flagUse{{"config", ""}, {"store", ""}}
	messageFlags  = []flagUse{{"channel", ""}, {"run", ""}, {"to", ""}}
	terminalFlags = []flagUse{{"quiet", ""}, {"plain", ""}, {"no-color", ""}}
	// actionFlags are reportFlags for the commands that run actions, where
	// --quiet also takes the quiet window.
	actionFlags = slices.Concat(withDoc(reportFlags, "quiet", quietWindowDoc), []flagUse{{"timeout", ""}, {"parallel", ""}})
)

// quietWindowDoc is --quiet's help where it also takes a duration.
const quietWindowDoc = "results only. With a duration (--quiet 500ms): how long nothing\n" +
	"must arrive before an action is done (overrides quiet:, at most 30s)"

// withDoc returns fl with name's help replaced by doc.
func withDoc(fl []flagUse, name, doc string) []flagUse {
	out := slices.Clone(fl)
	for i := range out {
		if out[i].name == name {
			out[i].doc = doc
		}
	}
	return out
}

func flags(groups ...[]flagUse) []flagUse { return slices.Concat(groups...) }

const (
	failOnIssues  = "exit 1 on issues at or above error (the default), warning or notice;\nnone: never"
	failOnChanges = "exit 1 on changes at or above broke (the default), new or changed;\nnone: never. A failed action also exits 1, unless none"
)

// commands lists every command in help order.
var commands = []cmdSpec{
	{name: "up", aliases: []string{"start"}, use: "up [flags]", about: "start the catcher (API :8025, SMTP :1025)",
		flags: flags([]flagUse{{"seal", ""}, {"observe", ""}, {"listen", ""}, {"smtp", ""}, {"proxy", ""}, {"mitm", ""}, {"allow", ""},
			{"token", "require this token. Needed when the API, SMTP sink or proxy\nlistens beyond loopback, then 16 characters or more (openssl\nrand -hex 16); the SMTP sink asks for it as the password.\nPrefer RHYTHM_ECHO_TOKEN: other users can read a flag in ps"},
			{"private-network", ""}, {"hostname", ""}, {"free-ports", ""}, {"login", ""}, {"sites", ""},
			{"store", "keep what Echo catches in this folder, so a restart keeps it\n(default in memory; inbox: store: in the config)"},
			{"keep", ""}, {"keep-for", ""}, {"spamassassin", ""},
			{"config", ""}}, terminalFlags, []flagUse{{"verbose", ""}})},
	{name: "install", use: "install <stack> [path]", about: "connect a site: wordpress, laravel, next, node, generic", own: true},
	{name: "doctor", use: "doctor", about: "check that every capture layer works",
		flags: flags(echoFlags, []flagUse{{"json", "what was checked and how it went, as JSON"}}, terminalFlags)},
	{name: "watch", aliases: []string{"tail"}, use: "watch", about: "show messages as they arrive (--json: one per line)",
		flags: flags(echoFlags, messageFlags, []flagUse{{"since", "first show the messages from this long ago, e.g. 10m"},
			{"json", "one JSON object per line"}}, terminalFlags, []flagUse{{"verbose", ""}})},
	{name: "ls", aliases: []string{"list"}, use: "ls", about: "list captured messages (--json for all fields)",
		flags: flags(echoFlags, messageFlags, []flagUse{{"since", ""}, {"json", "every field, as JSON"}}, terminalFlags, []flagUse{{"verbose", ""}})},
	{name: "show", use: "show <id>", about: "one message, as JSON", flags: echoFlags},
	{name: "clear", use: "clear", about: "delete captured messages", flags: flags(echoFlags, messageFlags)},
	{name: "snapshot", aliases: []string{"snap"}, use: "snapshot [--name N]", about: "run the actions in .echo/config.yaml and save what they sent",
		flags: flags(echoFlags, []flagUse{{"store", ""}, {"action", ""}, {"name", ""},
			{"force", "save the snapshot even when every action failed; with --import,\nreplace one of the same name"},
			{"export", ""}, {"import", ""}, {"fail-on", ""}}, actionFlags)},
	{name: "compare", aliases: []string{"cmp"}, use: "compare [--snapshot N]", about: "run the actions again and show what changed since a snapshot",
		flags: flags(echoFlags, []flagUse{{"store", ""}, {"action", ""}, {"snapshot", ""}, {"against", ""},
			{"accept", ""}, {"fail-on", failOnChanges}}, actionFlags)},
	{name: "run", use: "run", about: "run the actions once, no comparison",
		flags: flags(echoFlags, []flagUse{{"store", ""}, {"action", ""}, {"fail-on", ""}}, actionFlags)},
	{name: "report", use: "report [run | snapshot] | --site <id>", about: "show the last result again (--open, -o), or a hub site's agency report",
		flags: flags(storeFlags, []flagUse{{"fail-on", "as compare for a comparison, as run otherwise; none: never"}}, reportFlags,
			[]flagUse{{"site", ""}, {"since", "with --site: the period, e.g. 90d (default 30d)"}, {"pdf", ""},
				{"brand-name", ""}, {"brand-logo", ""}, {"brand-color", ""}})},
	{name: "snapshots", use: "snapshots [delete <name>]", about: "list snapshots, or delete one",
		flags: flags(storeFlags, []flagUse{{"json", "the list as JSON: name, time, counts and which is the latest"}})},
	{name: "replay", use: "replay <event> --to <url>", about: "send a signed test webhook (Stripe, Standard Webhooks…)",
		flags: flags([]flagUse{{"to", "where to send (absolute, or relative to site:)"}, {"set", ""}, {"header", ""}, {"secret-env", ""},
			{"scheme", ""}, {"type", ""}, {"list", ""}, {"wait", ""}}, echoFlags,
			[]flagUse{{"json", "the response and what the site sent, as JSON"}}, terminalFlags, []flagUse{{"verbose", ""}})},
	{name: "latest", use: "latest --to <address> [--code | --link]", about: "print the one-time code or link just sent to someone",
		flags: flags([]flagUse{{"to", "whose message: an email address or a phone number"},
			{"channel", "only these channels, comma-separated, e.g. email,sms"},
			{"action", "only messages from this action of a run (one)"}, {"run", ""},
			{"since", "only messages from this long ago on, e.g. 10s (default 1m,\nor any age with --run)"},
			{"wait", "how long to wait for a message (default 10s, at most 1m;\n0: don't wait)"},
			{"code", ""}, {"link", ""}, {"kind", ""},
			{"json", "the message, its codes and links, as JSON"}}, echoFlags)},
	{name: "release", use: "release <id> --to <address>", about: "send one caught email to a client through the relay",
		flags: flags([]flagUse{{"to", "who gets the email: an address on relay: allow"}}, echoFlags)},
	{name: "check", use: "check <domain>", about: "SPF, DKIM, DMARC and MX for the domain you send from",
		flags: flags([]flagUse{{"from-mail", ""}, {"provider", ""}, {"selector", ""}, {"return-path", ""}, {"smtp-host", ""},
			{"mta-sts-fetch", ""}, {"resolver", ""}, {"fail-on", failOnIssues}}, reportFlags)},
	{name: "headers", use: "headers <file.eml | ->", about: "check a real received email's authentication",
		flags: flags([]flagUse{{"check", ""}, {"hops", ""}, {"resolver", ""}, {"mta-sts-fetch", ""}, {"fail-on", failOnIssues}}, reportFlags)},
	{name: "providers", use: "providers", about: "list the services Echo recognizes, by channel",
		flags: flags([]flagUse{{"json", "the list as JSON: slug, name, channel and hosts"}}, terminalFlags)},
	{name: "checks", use: "checks [--test]", about: "list the checks, the config's own rules too (--test: try the rules' fixtures)",
		flags: []flagUse{{"config", ""}, {"test", ""}, {"quiet", ""}}},
	{name: "mcp", use: "mcp [--url URL] [--token T] [--config FILE]", about: "MCP server for coding agents, on stdin and stdout", own: true},
	{name: "hub", use: "hub --store DIR --sites FILE [flags]", about: "one Echo for many staging sites, each with its own write token", own: true},
	{name: "schedule", use: `schedule "<when>" --store DIR --after "<command>"`, about: "snapshot, update and compare the hub's sites in a window", own: true},
	{name: "conformance", use: "conformance [--listen addr]", about: "a strict stand-in Echo for a connector's end-to-end test", own: true},
	{name: "init", use: "init [--stack s] [--force]", about: "write .echo/config.yaml with an action for this stack (--force replaces it)", own: true},
	{name: "ca", use: "ca [--pem | --untrust]", about: "print the proxy's CA certificate path (--pem: the certificate; --untrust: how to remove it)", own: true},
	{name: "sendmail", use: "sendmail [-t] [-i] [-f from] [rcpt…]", about: "sendmail stand-in for sendmail_path; reads the message on stdin", own: true},
	{name: "update", use: "update", about: "nothing to refresh; kept for rhythm update", hidden: true},
}

// lookup finds a command by its name or an alias.
func lookup(name string) (cmdSpec, bool) {
	for _, c := range commands {
		if c.name == name || slices.Contains(c.aliases, name) {
			return c, true
		}
	}
	return cmdSpec{}, false
}

// accepts reports whether the command reads the shared flag name.
func (c cmdSpec) accepts(name string) bool {
	return slices.ContainsFunc(c.flags, func(f flagUse) bool { return f.name == name })
}

// shortFlags maps each one-letter flag to its long name.
var shortFlags = map[string]string{"o": "output", "q": "quiet", "v": "verbose", "H": "header"}

// dash spells a flag as it's typed: --name, or -o for a letter.
func dash(name string) string {
	if len(name) == 1 {
		return "-" + name
	}
	return "--" + name
}

// unused returns, in order, the flags set on the command line that c
// doesn't read, by their long names.
func (c cmdSpec) unused(fs *flag.FlagSet) []string {
	var out []string
	fs.Visit(func(f *flag.Flag) {
		name := cmp.Or(shortFlags[f.Name], f.Name)
		if !c.accepts(name) && !slices.Contains(out, name) {
			out = append(out, name)
		}
	})
	return out
}

// noEffect is the error for flags a command doesn't read.
func noEffect(cmd string, names []string) error {
	spelled := make([]string, len(names))
	for i, n := range names {
		spelled[i] = dash(n)
	}
	verb := "has"
	if len(names) > 1 {
		verb = "have"
	}
	return cli.Usagef("%s %s no effect on %s (rhythm echo %s --help lists its flags)", andList(spelled), verb, cmd, cmd)
}

// andList joins items as "a", "a and b" or "a, b and c".
func andList(items []string) string {
	if len(items) < 2 {
		return strings.Join(items, "")
	}
	return strings.Join(items[:len(items)-1], ", ") + " and " + items[len(items)-1]
}

var (
	errUnknownFlag = regexp.MustCompile(`^flag provided but not defined: -+(.+)$`)
	errNeedsValue  = regexp.MustCompile(`^flag needs an argument: -+(.+)$`)
	errBadValue    = regexp.MustCompile(`^invalid (?:boolean )?value "(.*)" for (?:flag )?-+([^:]+): (.*)$`)
)

// flagError rewords the flag package's errors, which spell flags with
// one dash (-fail-on), to spell them as the help does. An unknown flag
// gets the one it most likely meant.
func flagError(err error, c cmdSpec) error {
	msg := err.Error()
	if m := errUnknownFlag.FindStringSubmatch(msg); m != nil {
		out := "unknown flag " + dash(m[1])
		if s := c.suggestFlag(m[1]); s != "" {
			out += "; did you mean " + dash(s) + "?"
		}
		return cli.UsageError{Msg: out}
	}
	if m := errNeedsValue.FindStringSubmatch(msg); m != nil {
		return cli.Usagef("%s needs a value", dash(m[1]))
	}
	if m := errBadValue.FindStringSubmatch(msg); m != nil {
		// The flag package says only "parse error" for a duration or a
		// boolean, so say what it wants instead.
		why := m[3]
		switch {
		case m[2] == "quiet" || m[2] == "q":
			// -q is a boolean flag that also takes a duration.
		case strings.Contains(msg, "boolean"):
			why = "want true or false"
		case why == "parse error":
			why = "want a duration like 10m or 5s"
		}
		return cli.Usagef("%s %q: %s", dash(m[2]), m[1], why)
	}
	return cli.UsageError{Msg: msg}
}

// ownFlagError is flagError for a command that parses its own flags
// (init, install): the hint comes from the flags fs defines, which c's
// shared list doesn't hold, before the shared ones.
func ownFlagError(err error, fs *flag.FlagSet) error {
	var c cmdSpec
	fs.VisitAll(func(f *flag.Flag) { c.flags = append(c.flags, flagUse{name: f.Name}) })
	return flagError(err, c)
}

// suggestFlag is the flag a mistyped one most likely meant: the
// command's own first, then any shared flag. "" when none is close.
func (c cmdSpec) suggestFlag(typed string) string {
	var own []string
	for _, f := range c.flags {
		own = append(own, f.name)
	}
	if s := closest(typed, own); s != "" {
		return s
	}
	all := make([]string, 0, len(flagDocs))
	for n := range flagDocs {
		all = append(all, n)
	}
	slices.Sort(all)
	return closest(typed, all)
}

// suggestCommand is the command a mistyped name most likely meant.
func suggestCommand(typed string) string {
	var names []string
	for _, c := range commands {
		if !c.hidden {
			names = append(names, c.name)
		}
	}
	names = append(names, "help", "version")
	return closest(typed, names)
}

// closest returns the candidate within edit distance 2 of typed, the
// nearest first and the earlier on a tie; "" when none is that close.
// Two edits catch a swap (cmopare) or a missing and a wrong letter. The
// distance must also be less than typed's length, or every two-letter
// word would be close to up and ls.
func closest(typed string, candidates []string) string {
	typed = strings.ToLower(typed)
	best, bestD := "", min(3, len([]rune(typed)))
	for _, c := range candidates {
		if d := editDistance(typed, c); d < bestD {
			best, bestD = c, d
		}
	}
	return best
}

// editDistance is the Levenshtein distance between a and b.
func editDistance(a, b string) int {
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

// helpLine formats one flag for help: the flag and its value from
// column 2 (column 6 without a short form) and the text from column 29,
// continued lines lined up under it.
func helpLine(left, text string) string {
	if !strings.HasPrefix(left, "-") || strings.HasPrefix(left, "--") {
		left = "    " + left
	}
	pad := strings.Repeat(" ", 29)
	lines := strings.Split(text, "\n")
	var b strings.Builder
	if len(left) >= 27 {
		fmt.Fprintf(&b, "  %s %s\n", left, lines[0])
	} else {
		fmt.Fprintf(&b, "  %-27s%s\n", left, lines[0])
	}
	for _, l := range lines[1:] {
		b.WriteString(pad + l + "\n")
	}
	return b.String()
}

// commandHelp is one command's help: what it does, how to call it and
// only the flags it reads.
func commandHelp(w io.Writer, c cmdSpec) {
	fmt.Fprintf(w, "rhythm echo %s: %s\n\nUsage:\n  rhythm echo %s", c.name, c.about, c.use)
	if len(c.flags) > 0 && !strings.Contains(c.use, "[flags]") {
		fmt.Fprint(w, " [flags]")
	}
	fmt.Fprintln(w)
	if len(c.aliases) > 0 {
		fmt.Fprintf(w, "  (or: rhythm echo %s)\n", strings.Join(c.aliases, ", rhythm echo "))
	}
	if len(c.flags) > 0 {
		fmt.Fprint(w, "\nFlags:\n")
		for _, f := range c.flags {
			d := flagDocs[f.name]
			fmt.Fprint(w, helpLine(d.left, cmp.Or(f.doc, d.text)))
		}
	}
	fmt.Fprint(w, "\nExit codes: 0 ok, 1 findings, 2 usage error, 3 couldn't run, 130 interrupted.\n"+
		"rhythm echo help lists every command.\n")
}

// quietFlag is -q and --quiet. Alone it means results only, as in Tuner
// and Soundcheck; with a duration (--quiet=500ms, or --quiet 500ms on
// the commands that run actions) it is the quiet window instead, named
// after the config's quiet:. It is a boolean flag so that a bare -q
// still takes no value.
type quietFlag struct{ o *options }

func (f quietFlag) String() string {
	if f.o == nil || !f.o.quiet {
		return "false"
	}
	return "true"
}

// IsBoolFlag lets -q stand alone.
func (quietFlag) IsBoolFlag() bool { return true }

func (f quietFlag) Set(v string) error {
	// Only the words: "0" and "1" would otherwise turn results-only off
	// and on where a duration was meant.
	switch v {
	case "true":
		f.o.quiet = true
		return nil
	case "false":
		f.o.quiet = false
		return nil
	}
	d, err := time.ParseDuration(v)
	if err != nil {
		return errQuietValue
	}
	f.o.quietFor, f.o.quietForSet = d, true
	return nil
}

var errQuietValue = errors.New("want a duration like 500ms, or no value for results only")

// joinQuietValue rewrites "--quiet 500ms" as "--quiet=500ms": the flag
// package never gives a boolean flag the next argument. Only a word that
// reads as a duration (or starts like one) is joined, so -q followed by
// a positional argument keeps its meaning.
func joinQuietValue(args []string) []string {
	out := make([]string, 0, len(args))
	for i := 0; i < len(args); i++ {
		a := args[i]
		if a == "--" {
			return append(out, args[i:]...)
		}
		if (a == "-q" || a == "--quiet" || a == "-quiet") && i+1 < len(args) && looksLikeDuration(args[i+1]) {
			out = append(out, a+"="+args[i+1])
			i++
			continue
		}
		out = append(out, a)
	}
	return out
}

// looksLikeDuration reports whether v is a duration, or a number meant as
// one (5, -1s), which Set then explains.
func looksLikeDuration(v string) bool {
	if _, err := time.ParseDuration(v); err == nil {
		return true
	}
	v = strings.TrimPrefix(v, "-")
	return v != "" && v[0] >= '0' && v[0] <= '9'
}

// visited reports whether the flag name was set on the command line.
func visited(fs *flag.FlagSet, name string) bool {
	seen := false
	fs.Visit(func(f *flag.Flag) { seen = seen || f.Name == name })
	return seen
}

// overrideWaits applies --quiet <d> and --timeout <d> to the config,
// checked as .echo/config.yaml's quiet: and timeout: are: more than 0,
// quiet at most 30s and no longer than the timeout, the timeout at most
// 1m. Those are the limits Echo's wait API applies, so a larger value
// would be cut short without a word. A wrong value is a usage error
// (exit 2) before anything runs.
//
// The values also go to runner.Options.Quiet and Timeout
// (runnerOptions), so --quiet replaces an action's own quiet: too.
func (p *plan) overrideWaits() error {
	o := &p.o
	if !o.quietForSet && !o.timeoutSet {
		return nil
	}
	quiet, timeout := p.cfg.Quiet.Duration, p.cfg.Timeout.Duration
	quietFrom, timeoutFrom := "quiet: in "+p.configName(), "timeout: in "+p.configName()
	if o.quietForSet {
		switch q := o.quietFor; {
		case q <= 0:
			return cli.Usagef("--quiet %s: must be more than 0, like 2s", shortDuration(q))
		case q > maxQuiet:
			return cli.Usagef("--quiet %s: over the %s Echo waits for quiet at most", shortDuration(q), shortDuration(maxQuiet))
		}
		quiet = o.quietFor
	}
	if o.timeoutSet {
		switch t := o.timeout; {
		case t <= 0:
			return cli.Usagef("--timeout %s: must be more than 0, like 30s", shortDuration(t))
		case t > maxTimeout:
			return cli.Usagef("--timeout %s: over the %s Echo waits at most", shortDuration(t), shortDuration(maxTimeout))
		}
		timeout, timeoutFrom = o.timeout, "--timeout"
	}
	if quiet > timeout {
		if o.quietForSet {
			return cli.Usagef("--quiet %s is longer than the timeout of %s (%s), so no action could ever fall quiet",
				shortDuration(quiet), shortDuration(timeout), timeoutFrom)
		}
		return cli.Usagef("--timeout %s is shorter than the quiet of %s (%s), so no action could ever fall quiet; lower it with --quiet",
			shortDuration(timeout), shortDuration(quiet), quietFrom)
	}
	p.cfg.Quiet = config.Duration{Duration: quiet}
	p.cfg.Timeout = config.Duration{Duration: timeout}
	return nil
}

// The limits of Echo's wait API. They stand in for config.MaxQuiet and
// config.MaxTimeout until those are released.
const (
	maxQuiet   = 30 * time.Second
	maxTimeout = time.Minute
)

// shortDuration writes d as a person would: 1m rather than 1m0s.
func shortDuration(d time.Duration) string {
	s := d.String()
	if strings.HasSuffix(s, "m0s") {
		s = strings.TrimSuffix(s, "0s")
	}
	if strings.HasSuffix(s, "h0m") {
		s = strings.TrimSuffix(s, "0m")
	}
	return s
}

// daysFlag is a duration flag that also takes days, as the config's
// keep_for: does: 14d, 1d12h or 36h. why is the error for a value that
// isn't one.
type daysFlag struct {
	d   *time.Duration
	why error
}

func (f daysFlag) String() string {
	if f.d == nil {
		return "0s"
	}
	return f.d.String()
}

func (f daysFlag) Set(v string) error {
	var d config.Duration
	if err := d.UnmarshalYAML(&yaml.Node{Kind: yaml.ScalarNode, Value: v}); err != nil {
		return f.why
	}
	*f.d = d.Duration
	return nil
}

var (
	errDaysValue  = errors.New("want a duration like 14d or 12h")
	errSinceValue = errors.New("want a duration like 10m or 5s")
)
