// Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
// SPDX-License-Identifier: MIT

// Command rhythm-echo captures every email, SMS, webhook and API call a
// site sends, seals staging so none of it reaches real people and
// compares what each action sends before and after a change. It is
// normally run as `rhythm echo`; the rhythm CLI runs this binary.
//
// Usage:
//
//	rhythm-echo <command> [flags]
//
// The commands are up (the catcher), install, doctor, watch, ls, show,
// clear, snapshot, compare, run, report, snapshots, replay, latest,
// check, headers, providers, checks, init, ca, mcp, hub, schedule,
// conformance and sendmail; `rhythm-echo help` lists their flags and exit
// codes. Every command exits 0 when done, 1 for findings,
// 2 for a usage error and 3 when it couldn't run, as Tuner and Soundcheck
// do; sendmail uses sendmail's own codes instead.
package main

import (
	"fmt"
	"io"
	"os"

	"github.com/charmbracelet/x/term"

	"github.com/RhythmShahriar/echo/config"
	"github.com/RhythmShahriar/echo/conformance"
	"github.com/RhythmShahriar/echo/hub"
	"github.com/RhythmShahriar/echo/mcp"
	"github.com/RhythmShahriar/echo/runner"
	"github.com/RhythmShahriar/rhythm/kit"
)

var version = "dev"

// Exit codes, as in Tuner and Soundcheck. Every command returns one of
// these; the help text lists them under "Exit codes".
const (
	exitOK       = 0   // done and nothing at or above --fail-on
	exitFindings = 1   // --fail-on hit, an action failed, a doctor check failed, or the site rejected a replay
	exitUsage    = 2   // wrong command line, or .echo/config.yaml asks for what isn't there
	exitError    = 3   // couldn't run: Echo not reachable, every action failed, no DNS answer, a file or network error
	exitCanceled = 130 // interrupted
)

func main() { os.Exit(run(os.Args[1:], os.Stdin, os.Stdout, os.Stderr)) }

func run(args []string, stdin io.Reader, stdout, stderr io.Writer) int {
	if len(args) == 0 {
		// On a terminal, ask what to do; piped or in CI, show the usage.
		if stdout == io.Writer(os.Stdout) && term.IsTerminal(os.Stdin.Fd()) && term.IsTerminal(os.Stdout.Fd()) {
			return pick(stdin, stdout, stderr)
		}
		usage(stdout)
		return exitOK
	}
	switch args[0] {
	case "-h", "--help", "help":
		if len(args) > 1 {
			return help(args[1], stdin, stdout, stderr)
		}
		usage(stdout)
		return exitOK
	case "-v", "--version", "version":
		fmt.Fprintln(stdout, "rhythm-echo", version)
		return exitOK
	}
	c, ok := lookup(args[0])
	if !ok {
		return unknownCommand(args[0], stderr)
	}
	switch c.name {
	case "up":
		return command(c, cmdUp, args[1:], stdout, stderr)
	case "snapshot":
		return command(c, cmdSnapshot, args[1:], stdout, stderr)
	case "compare":
		return command(c, cmdCompare, args[1:], stdout, stderr)
	case "run":
		return command(c, cmdRun, args[1:], stdout, stderr)
	case "report":
		return command(c, cmdReport, args[1:], stdout, stderr)
	case "watch":
		return command(c, cmdWatch, args[1:], stdout, stderr)
	case "ls":
		return command(c, cmdList, args[1:], stdout, stderr)
	case "show":
		return command(c, cmdShow, args[1:], stdout, stderr)
	case "clear":
		return command(c, cmdClear, args[1:], stdout, stderr)
	case "doctor":
		return command(c, cmdDoctor, args[1:], stdout, stderr)
	case "check":
		return command(c, cmdCheck, args[1:], stdout, stderr)
	case "headers":
		return command(c, cmdHeaders, args[1:], stdout, stderr)
	case "replay":
		return command(c, cmdReplay, args[1:], stdout, stderr)
	case "snapshots":
		return command(c, cmdSnapshots, args[1:], stdout, stderr)
	case "latest":
		return command(c, cmdLatest, args[1:], stdout, stderr)
	case "release":
		return command(c, cmdRelease, args[1:], stdout, stderr)
	case "providers":
		return command(c, cmdProviders, args[1:], stdout, stderr)
	case "checks":
		return command(c, cmdChecks, args[1:], stdout, stderr)
	case "mcp":
		mcp.Version, mcp.Replay = version, ownReplayer
		return mcp.Main(args[1:], stdin, stdout, stderr)
	case "hub":
		hub.Version = version
		return hub.Main(args[1:], stdin, stdout, stderr)
	case "schedule":
		hub.Version, hub.Replay = version, ownReplayer
		return hub.ScheduleMain(args[1:], stdin, stdout, stderr)
	case "conformance":
		return conformance.Main(args[1:], stdin, stdout, stderr)
	case "init":
		return cmdInit(args[1:], stdout, stderr)
	case "install":
		return cmdInstall(args[1:], stdout, stderr)
	case "ca":
		return cmdCA(args[1:], stdout, stderr)
	case "sendmail":
		return cmdSendmail(args[1:], stdin, stderr)
	}
	return cmdUpdate(args[1:], stdout, stderr)
}

// help prints one command's help: rhythm echo help compare.
func help(name string, stdin io.Reader, stdout, stderr io.Writer) int {
	c, ok := lookup(name)
	if !ok {
		return unknownCommand(name, stderr)
	}
	if c.own {
		if c.name == "sendmail" {
			fmt.Fprintf(stdout, "rhythm echo sendmail: %s\n\nUsage:\n  rhythm echo %s\n", c.about, c.use)
			return exitOK
		}
		if u, ok := ownUsage[c.name]; ok {
			fmt.Fprint(stdout, u)
			return exitOK
		}
		return run([]string{c.name, "--help"}, stdin, stdout, stderr)
	}
	commandHelp(stdout, c)
	return exitOK
}

// ownUsage is the help of the commands another package runs, which
// print it on stderr for --help; help prints it on stdout.
var ownUsage = map[string]string{"mcp": mcp.Usage, "hub": hub.Usage, "schedule": hub.ScheduleUsage, "conformance": conformance.Usage}

// ownReplayer is the replayer mcp and schedule run replay actions with:
// the one snapshot and compare use, for the project in dir with its
// config at cfgPath.
func ownReplayer(cfg config.Config, dir, cfgPath string) runner.Replayer {
	return replayer(cfg, dir, cfgPath, "")
}

// unknownCommand names the command meant, when one is close, rather
// than print the whole help.
func unknownCommand(name string, stderr io.Writer) int {
	msg := fmt.Sprintf("rhythm echo: unknown command %q", name)
	if s := suggestCommand(name); s != "" {
		msg += "; did you mean " + s + "?"
	}
	fmt.Fprintln(stderr, msg)
	fmt.Fprintln(stderr, "rhythm echo help lists every command.")
	return exitUsage
}

func usage(w io.Writer) {
	fmt.Fprintln(w, kit.Banner("rhythm echo", version))
	fmt.Fprint(w, `Is your site still saying what it should? Capture every email, SMS, webhook
and API call a site sends, seal staging so nothing reaches real people and
compare what each action sends before and after a change.

rhythm echo and rhythm-echo are the same command: the rhythm CLI runs this binary.

Usage:
  rhythm echo                            pick what to do (on a terminal)
  rhythm echo up [flags]                 start the catcher (API :8025, SMTP :1025)
  rhythm echo install <stack> [path]     connect a site: wordpress, laravel, next, node, generic
  rhythm echo doctor                     check that every capture layer works
  rhythm echo watch                      show messages as they arrive (--json: one per line)
  rhythm echo ls                         list captured messages (--json for all fields)
  rhythm echo show <id>                  one message, as JSON
  rhythm echo clear                      delete captured messages
  rhythm echo snapshot [--name N]        run the actions in .echo/config.yaml and save what they sent
  rhythm echo compare [--snapshot N]     run them again and show what changed
  rhythm echo run                        run the actions once, no comparison
  rhythm echo report [run | snapshot]    show the last result again (--open, -o)
  rhythm echo report --site <id> --store <hub store> [--pdf]
                                         a hub site's agency report: did its updates break anything
  rhythm echo snapshots [delete <name>]  list snapshots, or delete one
  rhythm echo replay <event> --to <url>  send a signed test webhook (Stripe, Standard Webhooks…)
  rhythm echo latest --to <address> [--code | --link]
                                         print the one-time code or link just sent to someone
  rhythm echo release <id> --to <address>
                                         send one caught email to a client through the relay
  rhythm echo check <domain>             SPF, DKIM, DMARC and MX for the domain you send from
  rhythm echo headers <file.eml | ->     check a real received email's authentication
  rhythm echo init [--stack s] [--force]
                                         write .echo/config.yaml with an action for this stack
                                         (wordpress, laravel, next, node or php; --force replaces it)
  rhythm echo providers                  list the services Echo recognizes, by channel (--json)
  rhythm echo checks [--test]            list the checks and the config's rules (--test: try their fixtures)
  rhythm echo ca [--pem | --untrust]     print the proxy's CA certificate path (--pem: the certificate;
                                         --untrust: how to stop trusting it)
  rhythm echo mcp                        MCP server for coding agents: claude mcp add echo -- rhythm-echo mcp
  rhythm echo hub --store D --sites F    one Echo for many staging sites, each with its own write token
  rhythm echo schedule "<when>" --store D --after "<command>"
                                         snapshot, update and compare the hub's sites in a window
  rhythm echo conformance [--listen a]   a strict stand-in Echo for a connector's end-to-end test
  rhythm echo sendmail [-t] [-i] [-f from] [rcpt…]
                                         sendmail stand-in for sendmail_path; reads the message on stdin
  rhythm echo help [command] | version   this help, one command's, or the version

Catcher (up):
      --seal                 nothing leaves; Echo answers for every service (default)
      --observe              record everything and send it for real
      --listen <addr>        API address (default 127.0.0.1:8025)
      --smtp <addr>          SMTP sink (default 127.0.0.1:1025; "off" disables)
      --proxy <addr>         also run an HTTP(S) proxy, e.g. 127.0.0.1:8026
      --mitm                 decrypt HTTPS in the proxy (trust the CA: rhythm echo ca)
      --allow <hosts>        hosts reachable even when sealed, e.g. api.stripe.com
      --token <t>            require this token. Needed when the API, SMTP sink or proxy
                             listens beyond loopback; the SMTP sink then asks for it as
                             the password. Prefer RHYTHM_ECHO_TOKEN: other users can
                             read a flag in ps
      --private-network      listen beyond loopback without a token, on private (RFC 1918)
                             and container networks only; prints a warning
      --hostname <name>      also answer to this name without a token, e.g. *.ddev.site
                             (RHYTHM_ECHO_HOSTNAMES)
      --free-ports           when 8025 or 1025 is taken (Mailpit in DDEV, Lando, Sail), move
                             to the next free pair and save it to the config without asking
      --login <user:pass>    let viewers sign in to Echo Inbox, read only, for 8 hours
                             (with relay: they may also release an email; prefer
                             RHYTHM_ECHO_LOGIN)
      --sites <file>         a write token per site (captures only), from a hub sites.yaml
      --store <dir>          keep what Echo catches in this folder, across restarts
      --keep <n>             the most messages kept (default 10000; at most 256 MiB)
      --keep-for <d>         with --store: drop messages older than this, e.g. 14d
      --spamassassin <addr>  score each email with the SpamAssassin daemon at host:port
                             (spamd; port 783 when left out)

Install (install <stack>):
      --url <url>            Echo's API as the site reaches it (default from .echo/config.yaml)
      --token <t>            the token Echo was started with (prefer RHYTHM_ECHO_TOKEN)
      --mode <m>             wordpress: seal or observe, what to do when Echo is down
      --force                wordpress: run even where WordPress says production
      --proxy <url>          wordpress: send raw cURL through Echo's proxy
      --ca <path>            wordpress: Echo's CA certificate, as the site sees it
      --wp-cli               wordpress: install through WP-CLI (wp eval-file)
      --container <name>     wordpress: install with WP-CLI inside this Docker container
      --uninstall            wordpress: remove the connector
      --plugin               wordpress: install Echo Staging Seal, the WordPress.org plugin
      --hub <url>            wordpress: report to an agency hub, with --site and the site's
                             write token in RHYTHM_ECHO_WRITE_TOKEN
      --site <id>            wordpress: the site's ID, named in every capture
      --bin <path>           wordpress: the rhythm-echo the plugin's buttons run
      --project <dir>        wordpress: the folder with .echo/config.yaml they run in

Messages (watch, ls, clear):
      --channel <c>          only email, sms, whatsapp, webhook, chat, analytics, payment,
                             push or http
      --run <id>             only messages from this run
      --to <address>         only messages to this recipient
      --since <d>            ls, watch: messages from this long ago on, e.g. 10m

Output:
      --json --md --junit --html   report format
  -o, --output <file>        write the report to a file (format from the extension)
      --open                 open the HTML report when done
  -q, --quiet                results only
      --plain                no color, boxes or glyphs (screen readers, logs)
      --no-color             no color
  -v, --verbose              show every message
      --fail-on <level>      exit 1 at or above this level. compare: changes at broke
                             (the default), new or changed. run, snapshot, check and
                             headers: issues at error (the default), warning or
                             notice. run, snapshot and compare also exit 1 when an
                             action fails; none turns both off

Actions (snapshot, compare, run):
      --config <file>        settings (default the nearest .echo/config.yaml, here or above)
      --store <dir>          where snapshots and runs are kept (default the config's
                             folder); report and snapshots read --config and --store too
      --url <url>            where Echo runs (default http://127.0.0.1:8025; RHYTHM_ECHO_URL)
      --action <name>        run only this action (repeatable)
      --name <name>          snapshot: its name (default the date and time)
      --force                snapshot: save it even when every action failed
      --snapshot <name>      compare: which snapshot (default the latest)
      --quiet <d>            how long nothing must arrive before an action is done
                             (overrides quiet:, at most 30s); -q alone: results only
      --timeout <d>          longest wait per action (overrides timeout:, at most 1m)
      --accept <key>         compare: take this change (its key from -v) as intended (repeatable)
      --parallel <n>         run up to n actions at once (one with parallel: false runs alone)
      --against <path>       compare: with this run instead (a run folder, its run.json or
                             a .tgz from snapshot --export)
      --export <file.tgz>    snapshot: write one (--name, default the latest) to a .tgz;
                             - writes it to stdout
      --import <file.tgz>    snapshot: add one from --export and make it the latest
                             (--name renames it, --force replaces one of that name)

Replay (inbound webhooks, sent only to staging and local addresses, with Echo sealed):
      --to <url>             where to send (absolute, or relative to site:)
      --set <path=value>     override a field, e.g. data.object.amount=1999 (repeatable)
  -H, --header <h>           extra header, "Name: value" (repeatable)
      --secret-env <NAME>    env var with the signing secret (default per scheme,
                             e.g. ECHO_STRIPE_WEBHOOK_SECRET)
      --scheme <s>           for JSON files: stripe, standard, github, woocommerce, hmac
      --type <t>             for JSON files: the event type (default the payload's "type")
      --list                 list the built-in events
      --wait <d>             how long to collect what the site sends back (default 5s)

Latest (latest --to <address>; prints only the code or link, for scripts and tests):
      --to <address>         whose message: an email address or a phone number
      --channel <c,c>        only these channels, e.g. email,sms
      --action <name>        only messages from this action of a run
      --run <id>             only messages from this run
      --since <d>            only messages from this long ago on (default 1m unless --run)
      --wait <d>             how long to wait for a message (default 10s, at most 1m)
      --code                 print the one-time code (the default, else the first link)
      --link                 print the first link instead (magic, verify, reset, login or
                             unsubscribe)
      --kind <k>             with --link: only this kind of link, e.g. magic or reset
  Exits 1 when nothing arrives in time, or it has no code or link. A test that signs
  the same user in again should pass --run or a short --since, or clear --to first:
  Echo answers the newest match at once, which may be the previous one.

Relay (release <id> --to <address>; Echo Inbox's Release button does the same):
      --to <address>         who gets the email: an address on relay: allow
  up sends a release through your own SMTP account over TLS, only to the addresses
  relay: allow lists, and logs each one in the inbox. It needs Echo's token or a
  viewer --login. Keep the account's password in RHYTHM_ECHO_RELAY, not the config:
    relay:
      allow: ["*@client.example"]
    RHYTHM_ECHO_RELAY=smtp://user:pass@smtp.agency.example:587 rhythm echo up --login ann:secret

Deliverability (check, headers; never sends anything):
      --from-mail <file.eml> learn the real sender from a received email
      --provider <name>      or name it: sendgrid, mailgun, postmark, ses, resend, brevo,
                             mailchimp, mandrill, sparkpost, google, microsoft, php-mail
      --selector <s>         DKIM selector to check
      --return-path <domain> the envelope sender's domain, which SPF checks
      --smtp-host <host>     the server that hands mail out, a name or an IP
      --mta-sts-fetch        also fetch the MTA-STS policy file over HTTPS
      --resolver <host:port> DNS server to ask (default the system's)
      --check                (headers) also run the DNS checks for the sender
      --hops                 (headers) show the route the message took

Agency report (report --site <id> --store <the hub's store>; rhythm echo schedule fills it):
      --site <id>            the hub site to report on: did its update windows break anything
      --since <d>            the period, e.g. 90d (default 30d)
      --pdf                  also print it to a PDF (needs Chrome or Chromium)
      --brand-name <name>    the agency's name on the report
      --brand-logo <file>    the agency's logo (PNG, JPEG, GIF, WebP or SVG)
      --brand-color <#hex>   the report's accent color, e.g. #1F6FEB

Checks (checks; rules: and checks: in .echo/config.yaml add and tune them):
      --test                 try each of the config's rules on its fixtures; exit 1 when
                             one fails (every other command then stops with exit 2)

Exit codes (as in Tuner and Soundcheck):
  0    ok
  1    findings: --fail-on hit, an action failed, a doctor check failed, or the site
       rejected a replay
  2    usage error: a wrong command line, or .echo/config.yaml asks for what isn't there
  3    couldn't run: Echo not reachable, every action failed, the DNS resolver never
       answered, a file or network error
  130  interrupted
  sendmail exits with sendmail's codes instead (sysexits: 64 usage, 69 Echo unreachable…)

Each command takes only the flags it reads; rhythm echo <command> --help lists them.
--json writes one shape for every command: schema, kind, summary, verdict and data
(watch --json streams one message per line instead).

Examples:
  rhythm echo up                                   # seal this machine's staging sites
  rhythm echo install wordpress /var/www/staging   # add the WordPress connector
  rhythm echo snapshot --name before-update
  rhythm echo compare --fail-on broke
  code=$(rhythm echo latest --to ann@shop.test --code)
                                                   # the login code just sent, in a test
  rhythm soundcheck snapshot staging.shop.test --echo --level inspect,click,submit
                                                   # forms and flows, with what they send
`)
}
