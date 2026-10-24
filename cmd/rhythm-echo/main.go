// rhythm-echo is the Echo binary, normally run as `rhythm echo`.
package main

import (
	"fmt"
	"io"
	"os"

	"github.com/RhythmShahriar/rhythm/kit"
)

var version = "dev"

// Exit codes, as in Tuner and Soundcheck. Every command returns one of
// these; the help text lists them under "Exit codes".
const (
	exitOK       = 0   // done, and nothing at or above --fail-on
	exitFindings = 1   // --fail-on hit, a doctor check failed, or the site rejected a replay
	exitUsage    = 2   // wrong command line, or .echo/config.yaml asks for what isn't there
	exitError    = 3   // couldn't run: Echo not reachable, a file or network error
	exitCanceled = 130 // interrupted
)

func main() { os.Exit(run(os.Args[1:], os.Stdin, os.Stdout, os.Stderr)) }

func run(args []string, stdin io.Reader, stdout, stderr io.Writer) int {
	if len(args) == 0 {
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
  rhythm echo up [flags]                 start the catcher (API :8025, SMTP :1025)
  rhythm echo install <stack> [target]   connect a site: wordpress, laravel, next, node, generic
                                         (wordpress: [path] --url --token --proxy --ca, or
                                         --wp-cli, --container <name>, --uninstall)
  rhythm echo doctor                     check that every capture layer works
  rhythm echo watch                      show messages as they arrive
  rhythm echo ls | show <id> | clear     list, inspect or delete captured messages
  rhythm echo snapshot [--name N]        run the actions in .echo/config.yaml and save what they sent
  rhythm echo compare [--snapshot N]     run them again and show what changed
  rhythm echo run                        run the actions once, no comparison
  rhythm echo report [run]               show the last result again (--open, -o)
  rhythm echo snapshots                  list snapshots (delete <name> removes one)
  rhythm echo replay <event> --to <url>  send a signed test webhook (Stripe, Standard Webhooks…)
  rhythm echo check <domain>             SPF, DKIM, DMARC and MX for the domain you send from
  rhythm echo headers <file.eml>         check a real received email's authentication
  rhythm echo init                       write .echo/config.yaml to edit
  rhythm echo ca                         print the proxy's CA certificate path (for HTTPS capture)
  rhythm echo update                     nothing to refresh; kept for rhythm update

Catcher (up):
      --seal                 nothing leaves; Echo answers for every service (default)
      --observe              record everything and send it for real
      --listen <addr>        API address (default 127.0.0.1:8025)
      --smtp <addr>          SMTP sink (default 127.0.0.1:1025; "off" disables)
      --proxy <addr>         also run an HTTP(S) proxy, e.g. 127.0.0.1:8026
      --mitm                 decrypt HTTPS in the proxy (trust the CA: rhythm echo ca)
      --allow <hosts>        hosts reachable even when sealed, e.g. api.stripe.com
      --token <t>            require this token (needed when listening beyond loopback)

Output:
      --json --md --junit --html   report format
  -o, --output <file>        write the report to a file (format from the extension)
      --open                 open the HTML report when done
  -q, --quiet                results only
      --plain                no color, boxes or glyphs
  -v, --verbose              show every message
      --fail-on <level>      exit 1 on changes at or above broke, new or changed;
                             for run and snapshot: issues at error, warning, notice

Replay (inbound webhooks, sent only to staging and local addresses, with Echo sealed):
      --to <url>             where to send (absolute, or relative to site:)
      --set <path=value>     override a field, e.g. data.object.amount=1999 (repeatable)
      --secret-env <NAME>    env var with the signing secret (default per scheme,
                             e.g. ECHO_STRIPE_WEBHOOK_SECRET)
      --scheme <s>           for JSON files: stripe, standard, github, woocommerce, hmac
      --list                 list the built-in events
      --wait <d>             how long to collect what the site sends back (default 5s)

Deliverability (check, headers; never sends anything):
      --from-mail <file.eml> learn the real sender from a received email
      --provider <name>      or name it: sendgrid, mailgun, ses, postmark, resend…
      --selector <s>         DKIM selector to check
      --mta-sts-fetch        also fetch the MTA-STS policy file over HTTPS
      --check                (headers) also run the DNS checks for the sender
      --hops                 (headers) show the route the message took

Other:
      --config <file>        settings (default .echo/config.yaml)
      --url <url>            where Echo runs (default http://127.0.0.1:8025; RHYTHM_ECHO_URL)
      --action <name>        run only this action (repeatable)

Exit codes: 0 ok · 1 --fail-on hit · 2 usage error · 3 couldn't run · 130 interrupted

Examples:
  rhythm echo up                                   # seal this machine's staging sites
  rhythm echo install wordpress /var/www/staging   # add the WordPress connector
  rhythm echo snapshot --name before-update
  rhythm echo compare --fail-on broke
  rhythm soundcheck snapshot staging.shop.test --echo   # forms and flows, with messages
`)
}
