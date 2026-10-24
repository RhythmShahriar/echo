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

	"github.com/RhythmShahriar/echo/client"
	"github.com/RhythmShahriar/echo/config"
	"github.com/RhythmShahriar/echo/model"
	"github.com/RhythmShahriar/echo/store"
	"github.com/RhythmShahriar/rhythm/kit/cli"
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
	force                  bool   // snapshot: save even when every action failed; with --import, replace
	export, importFile     string // snapshot: write one to, or add one from, a .tgz
	against                string // compare: a baseline from anywhere

	seal, observe bool
	listen, smtp  string
	proxy         string
	mitm          bool
	allow         cli.ListFlag

	channel, runID, to string
	since              time.Duration // ls and watch: also the messages from this long ago

	// check, headers and replay
	fromMail, provider, selector, returnPath, smtpHost, resolver string
	mtaSTSFetch, withDNS, hops, list                             bool
	sets, headers                                                cli.RepeatFlag
	secretEnv, scheme, typ                                       string
	wait                                                         time.Duration
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
	both("q", "quiet", func(n string) { fs.BoolVar(&o.quiet, n, false, "") })
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

	fs.BoolVar(&o.seal, "seal", false, "")
	fs.BoolVar(&o.observe, "observe", false, "")
	fs.StringVar(&o.listen, "listen", "", "")
	fs.StringVar(&o.smtp, "smtp", "", "")
	fs.StringVar(&o.proxy, "proxy", "", "")
	fs.BoolVar(&o.mitm, "mitm", false, "")
	fs.Var(&o.allow, "allow", "")

	fs.StringVar(&o.channel, "channel", "", "")
	fs.StringVar(&o.runID, "run", "", "")
	fs.StringVar(&o.to, "to", "", "")
	fs.DurationVar(&o.since, "since", 0, "")

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
	return fs
}

// plan is a command's validated settings.
type plan struct {
	o       options
	args    []string
	cfg     config.Config
	cfgPath string // the config file read; "" when there is none and the defaults apply
	format  string
	st      store.Store
	echo    *client.Client
}

func parse(c cmdSpec, args []string) (*plan, error) {
	p := &plan{}
	o := &p.o
	fs := flagSet(o)
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
	p.args = pos
	path := o.configPath
	if path != "" {
		if _, err := os.Stat(path); err != nil {
			return nil, cli.Usagef("--config %s: %v", path, err)
		}
	} else if path, err = findConfig("."); errors.Is(err, errNoConfig) {
		path = "" // the defaults; commands that need actions say so
	} else if err != nil {
		return nil, err
	}
	p.cfgPath = path
	if p.cfg, err = loadConfig(path); err != nil {
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
	if o.channel != "" && !slices.Contains(channels, model.Channel(o.channel)) {
		return nil, cli.Usagef("--channel %q: use one of %s", o.channel, joinChannels())
	}
	if o.since < 0 {
		return nil, cli.Usagef("--since %s: give how long ago, e.g. 10m", o.since)
	}
	if o.name != "" && !store.ValidName(o.name) {
		return nil, cli.Usagef("--name %q: use letters, digits, dots, dashes and underscores", o.name)
	}
	p.st = store.Store{Root: storeRoot(o.storeDir, path)}
	url := cmp.Or(o.url, os.Getenv("RHYTHM_ECHO_URL"), "http://"+p.cfg.Listen)
	p.echo = client.New(url, cmp.Or(o.token, os.Getenv("RHYTHM_ECHO_TOKEN")))
	return p, nil
}

// errNoConfig is the error when no folder from here up has a
// .echo/config.yaml. It stands in for config.ErrNoConfig until that is
// released, with the same words.
var errNoConfig = errors.New("no .echo/config.yaml here or above; run rhythm echo init")

// findConfig returns the absolute path of the nearest .echo/config.yaml
// in start or a folder above it, as git finds .git, so Echo works from
// any subfolder of a project. The search ends at the repository root
// (the folder holding .git, which is searched) and below $HOME (which
// isn't, unless start is $HOME): a stray ~/.echo would otherwise catch
// every project outside a repository.
//
// This stands in for config.Find until that is released.
func findConfig(start string) (string, error) {
	dir, err := filepath.Abs(start)
	if err != nil {
		return "", err
	}
	home := ""
	if h, err := os.UserHomeDir(); err == nil && h != "" {
		if h, err := filepath.Abs(h); err == nil {
			home = h
		}
	}
	for {
		p := filepath.Join(dir, config.Dir, "config.yaml")
		if fi, err := os.Stat(p); err == nil && fi.Mode().IsRegular() {
			return p, nil
		}
		// .git is a folder in a clone and a file in a worktree or submodule.
		if _, err := os.Lstat(filepath.Join(dir, ".git")); err == nil {
			return "", errNoConfig
		}
		parent := filepath.Dir(dir)
		if parent == dir || parent == home {
			return "", errNoConfig
		}
		dir = parent
	}
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
	"against":  {"--against <path>", "compare with this run instead: a run folder, its run.json\nor a .tgz written by snapshot's export"},

	"seal":    {"--seal", "nothing leaves; Echo answers for every service (default)"},
	"observe": {"--observe", "record everything and send it for real"},
	"listen":  {"--listen <addr>", "API address (default 127.0.0.1:8025)"},
	"smtp":    {"--smtp <addr>", `SMTP sink (default 127.0.0.1:1025; "off" disables)`},
	"proxy":   {"--proxy <addr>", "also run an HTTP(S) proxy, e.g. 127.0.0.1:8026"},
	"mitm":    {"--mitm", "decrypt HTTPS in the proxy (trust the CA: rhythm echo ca)"},
	"allow":   {"--allow <hosts>", "hosts reachable even when sealed, e.g. api.stripe.com"},

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

	"set":        {"--set <path=value>", "override a field, e.g. data.object.amount=1999 (repeatable)"},
	"header":     {"-H, --header <h>", `extra header, "Name: value" (repeatable)`},
	"secret-env": {"--secret-env <NAME>", "env var with the signing secret (default per scheme,\ne.g. ECHO_STRIPE_WEBHOOK_SECRET)"},
	"scheme":     {"--scheme <s>", "for JSON files: stripe, standard, github, woocommerce, hmac"},
	"type":       {"--type <t>", `for JSON files: the event type (default the payload's "type")`},
	"list":       {"--list", "list the built-in events"},
	"wait":       {"--wait <d>", "how long to collect what the site sends back (default 5s)"},
}

// Flag groups several commands share.
var (
	reportFlags = []flagUse{{"json", ""}, {"md", ""}, {"junit", ""}, {"html", ""}, {"output", ""}, {"open", ""},
		{"quiet", ""}, {"plain", ""}, {"no-color", ""}, {"verbose", ""}}
	echoFlags     = []flagUse{{"config", ""}, {"url", ""}, {"token", ""}}
	storeFlags    = []flagUse{{"config", ""}, {"store", ""}}
	messageFlags  = []flagUse{{"channel", ""}, {"run", ""}, {"to", ""}}
	terminalFlags = []flagUse{{"quiet", ""}, {"plain", ""}, {"no-color", ""}}
)

func flags(groups ...[]flagUse) []flagUse { return slices.Concat(groups...) }

const (
	failOnIssues  = "exit 1 on issues at or above error (the default), warning or notice;\nnone: never"
	failOnChanges = "exit 1 on changes at or above broke (the default), new or changed;\nnone: never. A failed action also exits 1, unless none"
)

// commands lists every command in help order.
var commands = []cmdSpec{
	{name: "up", aliases: []string{"start"}, use: "up [flags]", about: "start the catcher (API :8025, SMTP :1025)",
		flags: flags([]flagUse{{"seal", ""}, {"observe", ""}, {"listen", ""}, {"smtp", ""}, {"proxy", ""}, {"mitm", ""}, {"allow", ""},
			{"token", "require this token. Needed when the API, SMTP sink or proxy\nlistens beyond loopback; the SMTP sink then asks for it as\nthe password. Prefer RHYTHM_ECHO_TOKEN: other users can\nread a flag in ps"},
			{"config", ""}}, terminalFlags, []flagUse{{"verbose", ""}})},
	{name: "install", use: "install <stack> [path]", about: "connect a site: wordpress, laravel, next, node, generic", own: true},
	{name: "doctor", use: "doctor", about: "check that every capture layer works", flags: flags(echoFlags, terminalFlags)},
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
			{"export", ""}, {"import", ""}, {"fail-on", ""}}, reportFlags)},
	{name: "compare", aliases: []string{"cmp"}, use: "compare [--snapshot N]", about: "run the actions again and show what changed since a snapshot",
		flags: flags(echoFlags, []flagUse{{"store", ""}, {"action", ""}, {"snapshot", ""}, {"against", ""},
			{"fail-on", failOnChanges}}, reportFlags)},
	{name: "run", use: "run", about: "run the actions once, no comparison",
		flags: flags(echoFlags, []flagUse{{"store", ""}, {"action", ""}, {"fail-on", ""}}, reportFlags)},
	{name: "report", use: "report [run | snapshot]", about: "show the last result again (--open, -o)",
		flags: flags(storeFlags, []flagUse{{"fail-on", "as compare for a comparison, as run otherwise; none: never"}}, reportFlags)},
	{name: "snapshots", use: "snapshots [delete <name>]", about: "list snapshots, or delete one", flags: storeFlags},
	{name: "replay", use: "replay <event> --to <url>", about: "send a signed test webhook (Stripe, Standard Webhooks…)",
		flags: flags([]flagUse{{"to", "where to send (absolute, or relative to site:)"}, {"set", ""}, {"header", ""}, {"secret-env", ""},
			{"scheme", ""}, {"type", ""}, {"list", ""}, {"wait", ""}}, echoFlags,
			[]flagUse{{"json", "the response and what the site sent, as JSON"}}, terminalFlags, []flagUse{{"verbose", ""}})},
	{name: "check", use: "check <domain>", about: "SPF, DKIM, DMARC and MX for the domain you send from",
		flags: flags([]flagUse{{"from-mail", ""}, {"provider", ""}, {"selector", ""}, {"return-path", ""}, {"smtp-host", ""},
			{"mta-sts-fetch", ""}, {"resolver", ""}, {"fail-on", failOnIssues}}, reportFlags)},
	{name: "headers", use: "headers <file.eml | ->", about: "check a real received email's authentication",
		flags: flags([]flagUse{{"check", ""}, {"hops", ""}, {"resolver", ""}, {"mta-sts-fetch", ""}, {"fail-on", failOnIssues}}, reportFlags)},
	{name: "init", use: "init [--stack s] [--force]", about: "write .echo/config.yaml with an action for this stack (--force replaces it)", own: true},
	{name: "ca", use: "ca [--pem]", about: "print the proxy's CA certificate path (--pem: the certificate)", own: true},
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
		case strings.Contains(msg, "boolean"):
			why = "want true or false"
		case why == "parse error":
			why = "want a duration like 10m or 5s"
		}
		return cli.Usagef("%s %q: %s", dash(m[2]), m[1], why)
	}
	return cli.UsageError{Msg: msg}
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
