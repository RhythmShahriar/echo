// Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
// SPDX-License-Identifier: MIT

// rhythm-echo is the Echo binary, normally run as `rhythm echo`.
package main

import (
	"fmt"
	"io"
	"os"

	"github.com/charmbracelet/x/term"

	"github.com/RhythmShahriar/rhythm/kit"
)

var version = "dev"

// Exit codes, as in Tuner and Soundcheck. Every command returns one of
// these; the help text lists them under "Exit codes".
const (
	exitOK       = 0   // done, and nothing at or above --fail-on
	exitFindings = 1   // --fail-on hit, an action failed, a doctor check failed, or the site rejected a replay
	exitUsage    = 2   // wrong command line, or .echo/config.yaml asks for what isn't there
	exitError    = 3   // couldn't run: Echo not reachable, every action failed, a file or network error
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
		usage(stdout)
		return exitOK
	case "-v", "--version", "version":
		fmt.Fprintln(stdout, "rhythm-echo", version)
		return exitOK
	case "up", "start":
		return command(cmdUp, args[1:], stdout, stderr)
	case "snapshot", "snap":
		return command(cmdSnapshot, args[1:], stdout, stderr)
	case "compare", "cmp":
		return command(cmdCompare, args[1:], stdout, stderr)
	case "run":
		return command(cmdRun, args[1:], stdout, stderr)
	case "report":
		return command(cmdReport, args[1:], stdout, stderr)
	case "watch", "tail":
		return command(cmdWatch, args[1:], stdout, stderr)
	case "ls", "list":
		return command(cmdList, args[1:], stdout, stderr)
	case "show":
		return command(cmdShow, args[1:], stdout, stderr)
	case "clear":
		return command(cmdClear, args[1:], stdout, stderr)
	case "snapshots":
		return cmdSnapshots(args[1:], stdout, stderr)
	case "init":
		return cmdInit(args[1:], stdout, stderr)
	case "install":
		return cmdInstall(args[1:], stdout, stderr)
	case "doctor":
		return command(cmdDoctor, args[1:], stdout, stderr)
	case "check":
		return command(cmdCheck, args[1:], stdout, stderr)
	case "headers":
		return command(cmdHeaders, args[1:], stdout, stderr)
	case "replay":
		return command(cmdReplay, args[1:], stdout, stderr)
	case "ca":
		return cmdCA(args[1:], stdout, stderr)
	case "sendmail":
		return cmdSendmail(args[1:], stdin, stderr)
	case "update":
		return cmdUpdate(args[1:], stdout, stderr)
	}
	fmt.Fprintf(stderr, "rhythm echo: unknown command %q\n\n", args[0])
	usage(stderr)
	return exitUsage
}

func usage(w io.Writer) {
	fmt.Fprintln(w, kit.Banner("rhythm echo", version))
	fmt.Fprint(w, `Is your site still saying what it should? Capture every email, SMS, webhook
and API call a site sends, seal staging so nothing reaches real people, and
compare what each action sends before and after a change.

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
  rhythm echo snapshots [delete <name>]  list snapshots, or delete one
  rhythm echo replay <event> --to <url>  send a signed test webhook (Stripe, Standard Webhooks…)
  rhythm echo check <domain>             SPF, DKIM, DMARC and MX for the domain you send from
  rhythm echo headers <file.eml | ->     check a real received email's authentication
  rhythm echo init [--force]             write .echo/config.yaml to edit (--force replaces it)
  rhythm echo ca [--pem]                 print the proxy's CA certificate path (--pem: the certificate)
  rhythm echo sendmail [-t] [-i] [-f from] [rcpt…]
                                         sendmail stand-in for sendmail_path; reads the message on stdin
  rhythm echo update                     nothing to refresh; kept for rhythm update
  rhythm echo help | version

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
      --fail-on <level>      exit 1 on changes at or above broke, new or changed;
                             for run, snapshot, check and headers: issues at or
                             above error, warning or notice. run, snapshot and
                             compare also exit 1 when an action fails; none turns
                             both off

Actions (snapshot, compare, run):
      --config <file>        settings (default .echo/config.yaml)
      --url <url>            where Echo runs (default http://127.0.0.1:8025; RHYTHM_ECHO_URL)
      --action <name>        run only this action (repeatable)
      --name <name>          snapshot: its name (default the date and time)
      --force                snapshot: save it even when every action failed
      --snapshot <name>      compare: which snapshot (default the latest)

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

Exit codes (as in Tuner and Soundcheck):
  0    ok
  1    findings: --fail-on hit, an action failed, a doctor check failed, or the site
       rejected a replay
  2    usage error: a wrong command line, or .echo/config.yaml asks for what isn't there
  3    couldn't run: Echo not reachable, every action failed, a file or network error
  130  interrupted
  sendmail exits with sendmail's codes instead (sysexits: 64 usage, 69 Echo unreachable…)

Examples:
  rhythm echo up                                   # seal this machine's staging sites
  rhythm echo install wordpress /var/www/staging   # add the WordPress connector
  rhythm echo snapshot --name before-update
  rhythm echo compare --fail-on broke
  rhythm soundcheck snapshot staging.shop.test --echo --level inspect,click,submit
                                                   # forms and flows, with what they send
`)
}
