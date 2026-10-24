// Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
// SPDX-License-Identifier: MIT

package main

import (
	"bytes"
	"cmp"
	"errors"
	"flag"
	"fmt"
	"io"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"

	"github.com/RhythmShahriar/echo/config"
	"github.com/RhythmShahriar/echo/connectors/wordpress"
	"github.com/RhythmShahriar/echo/proxy"
	"github.com/RhythmShahriar/echo/sendmail"
	"github.com/RhythmShahriar/rhythm/kit/cli"
)

const installUsage = `usage: rhythm echo install <stack> [path] [flags]
  stacks: wordpress (wp), laravel, next (nextjs), node, generic (php)
  wordpress: [path] --url URL --token T --mode seal|observe --force --proxy URL --ca PATH
             --wp-cli | --container NAME, --uninstall
  all: --url URL --token T --config FILE`

// cmdInstall connects a site to Echo. WordPress is installed for real (a
// mu-plugin, on disk, through WP-CLI or into a container); the other
// stacks get their package and the lines to add, since those are the
// project's own files to change.
func cmdInstall(args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("install", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	url := fs.String("url", "", "")
	token := fs.String("token", "", "")
	mode := fs.String("mode", "", "")
	force := fs.Bool("force", false, "")
	wpcli := fs.Bool("wp-cli", false, "")
	container := fs.String("container", "", "")
	uninstall := fs.Bool("uninstall", false, "")
	proxyURL := fs.String("proxy", "", "")
	caPath := fs.String("ca", "", "")
	configPath := fs.String("config", "", "")
	pos, err := cli.ParseInterspersed(fs, args)
	if errors.Is(err, flag.ErrHelp) {
		fmt.Fprintln(stdout, installUsage)
		return exitOK
	}
	if err != nil || len(pos) == 0 {
		if err != nil {
			c, _ := lookup("install")
			fmt.Fprintln(stderr, "rhythm echo:", flagError(err, c))
		}
		fmt.Fprintln(stderr, installUsage)
		return exitUsage
	}
	if *mode != "" && *mode != "seal" && *mode != "observe" {
		fmt.Fprintf(stderr, "rhythm echo: --mode %q: use seal or observe\n", *mode)
		return exitUsage
	}
	// The addresses Echo runs on come from .echo/config.yaml when there is one.
	cfg, err := config.Load(cmp.Or(*configPath, config.Path()))
	if err != nil {
		fmt.Fprintln(stderr, "rhythm echo:", err)
		return exitUsage
	}
	echoURL := cmp.Or(*url, os.Getenv("RHYTHM_ECHO_URL"), "http://"+cfg.Listen)
	// install has no -q: what it prints is the result.
	warnTokenFlag(stderr, *token, false)
	tok := cmp.Or(*token, os.Getenv("RHYTHM_ECHO_TOKEN"))
	proxyAddr := cfg.Proxy
	if proxyAddr == "" || proxyAddr == "off" {
		proxyAddr = "127.0.0.1:8026"
	}
	smtpAddr := cfg.SMTP
	if smtpAddr == "" || smtpAddr == "off" {
		smtpAddr = config.Default().SMTP
	}
	switch pos[0] {
	case "wordpress", "wp":
		target := "."
		if len(pos) > 1 {
			target = pos[1]
		}
		s := wordpress.Settings{URL: echoURL, Token: tok, Mode: *mode, Force: *force, Proxy: *proxyURL, CA: *caPath}
		return installWordPress(target, s, *wpcli, *container, *uninstall, stdout, stderr)
	case "laravel":
		fmt.Fprintf(stdout, `Laravel: add the package and point it at Echo.

  composer require rhythmsh/echo-laravel

It's a regular dependency, not --dev: staging deploys usually run composer install
--no-dev, which would leave it out. It does nothing where APP_ENV is production or
testing (RHYTHM_ECHO_FORCE=true overrides that, only for a box that isn't production).

Then in .env on staging or your machine:

  RHYTHM_ECHO_URL=%s%s
  QUEUE_CONNECTION=sync          # or flush with: php artisan queue:work --stop-when-empty

Check it: php artisan echo:status, then: rhythm echo doctor
Packages that skip Laravel's mailer and Http client (raw Guzzle, SDKs) are caught
through Echo's proxy: rhythm echo up --proxy %s --mitm, then set
HTTPS_PROXY=http://%s and trust: %s
`, echoURL, tokenLine(tok), proxyAddr, proxyAddr, caHint())
		return exitOK
	case "next", "nextjs":
		return installNext(cmp.Or(second(pos), "."), echoURL, tok, stdout, stderr)
	case "node":
		fmt.Fprintf(stdout, `Node: add the package and load it before your app.

  npm install --save-dev @rhythmsh/echo   # or a regular dependency if staging installs without dev deps
  node --import @rhythmsh/echo/register server.js      # or: import '@rhythmsh/echo/register'

Environment, on staging or your machine only:

  RHYTHM_ECHO_URL=%s%s

The connector is off on VERCEL_ENV=production, and on NODE_ENV=production unless
RHYTHM_ECHO_URL is set: setting it there turns the connector on.

Express/Fastify: app.use(echoMiddleware()) ties messages to the request that caused them.
`, echoURL, tokenLine(tok))
		return exitOK
	case "generic", "php":
		smtpLogin := "any username and password"
		if !loopback(smtpAddr) {
			smtpLogin = "any username, Echo's token as the password"
		}
		smtpHost, smtpPort, err := net.SplitHostPort(smtpAddr)
		if err != nil {
			smtpHost, smtpPort = smtpAddr, "25"
		}
		fmt.Fprintf(stdout, `Any stack: point mail and outbound HTTPS at Echo.

  Mail (SMTP):      host %s, port %s, %s
%s  HTTP(S):          rhythm echo up --proxy %s --mitm
                    HTTPS_PROXY=http://%s  https_proxy=http://%s
                    and trust Echo's CA: %s
                    (curl, PHP cURL, Python requests and Go honor these; Node needs
                    NODE_USE_ENV_PROXY=1 and NODE_EXTRA_CA_CERTS on 22.21+/24.5+)
  Tie to actions:   send X-Echo-Run: <run>/<action> on the request that triggers the work,
                    or set RHYTHM_ECHO_RUN for scripts and workers.

For a seal that can't be bypassed, see docs/seal in the Echo repository: the app on an
internal Docker network or behind a firewall, with Echo as its only way out.
`, smtpHost, smtpPort, smtpLogin, sendmailLines(smtpAddr, binaryPath(), runtime.GOOS, !loopback(smtpAddr)), proxyAddr, proxyAddr, proxyAddr, caHint())
		return exitOK
	}
	fmt.Fprintf(stderr, "rhythm echo: install: unknown stack %q (wordpress, laravel, next, node, generic)\n", pos[0])
	return exitUsage
}

// sendmailLines says how to point PHP's mail() at the shim. The shim
// finds a sink on 127.0.0.1:1025 by itself. php-fpm runs sendmail_path
// with a bare environment, so any other address goes in the command: PHP runs it through sh -c, which takes the VAR=value
// prefix. Windows has no sh, so there the variable is set on its own.
func sendmailLines(smtpAddr, bin, goos string, needsToken bool) string {
	const pad = "                    "
	var b strings.Builder
	switch {
	case smtpAddr == sendmail.DefaultSMTP:
		fmt.Fprintf(&b, "  Mail (PHP):       sendmail_path = \"%s sendmail -t -i\"   (php.ini or php-fpm pool)\n", bin)
	case goos == "windows":
		fmt.Fprintf(&b, "  Mail (PHP):       sendmail_path = \"%s sendmail -t -i\"   (php.ini or php-fpm pool)\n", bin)
		fmt.Fprintf(&b, "%sand set %s=%s in the environment PHP runs in\n", pad, sendmail.EnvSMTP, smtpAddr)
	default:
		fmt.Fprintf(&b, "  Mail (PHP):       sendmail_path = \"%s=%s %s sendmail -t -i\"\n", sendmail.EnvSMTP, smtpAddr, bin)
		fmt.Fprintf(&b, "%s(php.ini or php-fpm pool; Echo's SMTP sink isn't on %s)\n", pad, sendmail.DefaultSMTP)
		fmt.Fprintf(&b, "  Mail (scripts):   %s=%s in the environment of cron jobs and scripts\n", sendmail.EnvSMTP, smtpAddr)
	}
	if needsToken {
		fmt.Fprintf(&b, "%sthe sink asks for Echo's token: set %s in the same environment\n", pad, sendmail.EnvToken)
	}
	return b.String()
}

func installWordPress(target string, s wordpress.Settings, wpcli bool, container string, uninstall bool, stdout, stderr io.Writer) int {
	fail := func(err error) int {
		fmt.Fprintln(stderr, "rhythm echo:", err)
		return exitError
	}
	script := wordpress.UninstallScript()
	if !uninstall && (wpcli || container != "") {
		var err error
		if script, err = wordpress.Script(s); err != nil {
			return fail(err)
		}
	}
	switch {
	case container != "":
		var errb bytes.Buffer
		cmd := exec.Command("docker", "exec", "-i", container, "wp", "eval-file", "-", "--allow-root") //nolint:gosec // the user's container
		cmd.Stdin, cmd.Stdout, cmd.Stderr = strings.NewReader(script), stdout, io.MultiWriter(stderr, &errb)
		if err := cmd.Run(); err != nil {
			hint := "is wp-cli installed in the container? or copy the files with docker cp"
			switch msg := errb.String(); {
			case strings.Contains(msg, "ermission denied"):
				hint = "wp-cli's user in the container can't write wp-content/mu-plugins; give that folder to it, or copy the files with docker cp"
			case strings.Contains(msg, "No such container"):
				hint = "no container named " + container + "; see: docker ps"
			}
			return fail(fmt.Errorf("running wp-cli in %s: %w (%s)", container, err, hint))
		}
	case wpcli:
		cmd := exec.Command("wp", "eval-file", "-", "--path="+target) //nolint:gosec // the user's site
		cmd.Stdin, cmd.Stdout, cmd.Stderr = strings.NewReader(script), stdout, stderr
		if err := cmd.Run(); err != nil {
			return fail(fmt.Errorf("wp eval-file: %w", err))
		}
	default:
		if _, err := wordpress.ContentDir(target); err != nil {
			fmt.Fprintln(stderr, "rhythm echo:", err)
			return exitUsage
		}
		if uninstall {
			if err := wordpress.Uninstall(target); err != nil {
				if wordpress.IsNotInstalled(err) {
					fmt.Fprintln(stdout, "The connector isn't installed there.")
					return exitOK
				}
				return fail(err)
			}
			break
		}
		if err := wordpress.Install(target, s); err != nil {
			var pe *os.PathError
			if errors.As(err, &pe) && errors.Is(pe.Err, os.ErrPermission) {
				return fail(fmt.Errorf("%w; try with --wp-cli, or as the site's user", err))
			}
			return fail(err)
		}
	}
	if uninstall {
		fmt.Fprintln(stdout, "Removed the Echo connector.")
		return exitOK
	}
	fmt.Fprintf(stdout, "Installed the Echo connector (wp-content/%s), reporting to %s.\n\n", wordpress.PluginPath, s.URL)
	switch env := wpEnvironment(target, wpcli || container != ""); {
	case s.Force:
		fmt.Fprintln(stdout, "  Forced on: it runs even where WordPress says production.")
	case env != "":
		fmt.Fprintf(stdout, "  wp-config.php sets WP_ENVIRONMENT_TYPE to %s, so the connector runs.\n", env)
	default:
		fmt.Fprint(stdout, `  It runs only where WP_ENVIRONMENT_TYPE is local, development or staging. WordPress
  treats an unset WP_ENVIRONMENT_TYPE as production, and there the connector does
  nothing. On staging, add this to wp-config.php, above "That's all, stop editing!":

    define( 'WP_ENVIRONMENT_TYPE', 'staging' );

  (or set it in the server's environment). On a box that isn't production but says
  so, reinstall with --force.
`)
	}
	fmt.Fprint(stdout, `
  Next: rhythm echo up, then rhythm echo doctor (with site: in .echo/config.yaml)
  Flush async mail and webhooks after actions: add to .echo/config.yaml
    flush: ["wp cron event run --due-now", "wp action-scheduler run"]
`)
	if container != "" && (strings.Contains(s.URL, "127.0.0.1") || strings.Contains(s.URL, "localhost")) {
		fmt.Fprintln(stdout, "  In Docker, 127.0.0.1 and localhost are the container itself: reinstall with --url http://host.docker.internal:8025")
	}
	return exitOK
}

var wpEnvDefine = regexp.MustCompile(`(?m)^\s*define\s*\(\s*['"]WP_ENVIRONMENT_TYPE['"]\s*,\s*['"](local|development|staging)['"]\s*\)`)

// wpEnvironment returns the non-production WP_ENVIRONMENT_TYPE the site's
// wp-config.php defines, if it can be read here.
func wpEnvironment(target string, remote bool) string {
	if remote {
		return ""
	}
	for _, p := range []string{filepath.Join(target, "wp-config.php"), filepath.Join(target, "..", "wp-config.php")} {
		if b, err := os.ReadFile(p); err == nil { //nolint:gosec // the user's own site
			if m := wpEnvDefine.FindSubmatch(b); m != nil {
				return string(m[1])
			}
		}
	}
	return ""
}

// installNext adds instrumentation.ts when the project has none, and says
// how to wrap next.config (the project's own file, so it's not edited).
func installNext(dir, url, token string, stdout, stderr io.Writer) int {
	if _, err := os.Stat(filepath.Join(dir, "package.json")); err != nil { //nolint:gosec // the user's own project folder
		fmt.Fprintf(stderr, "rhythm echo: %s has no package.json; run this in the Next.js project\n", dir)
		return exitUsage
	}
	inst := ""
	for _, name := range []string{"instrumentation.ts", "instrumentation.js", "src/instrumentation.ts", "src/instrumentation.js"} {
		if _, err := os.Stat(filepath.Join(dir, name)); err == nil { //nolint:gosec // the user's own project folder
			inst = name
			break
		}
	}
	var b bytes.Buffer
	fmt.Fprintf(&b, "Next.js: add the package.\n\n  npm install --save-dev @rhythmsh/echo\n\n")
	switch {
	case inst == "":
		target := "instrumentation.ts"
		if fi, err := os.Stat(filepath.Join(dir, "src", "app")); err == nil && fi.IsDir() { //nolint:gosec // the user's own project folder
			target = "src/instrumentation.ts"
		}
		body := "export async function register() {\n  const { register } = await import('@rhythmsh/echo/next');\n  await register();\n}\n"
		if err := os.WriteFile(filepath.Join(dir, target), []byte(body), 0o644); err != nil { //nolint:gosec // a project source file
			fmt.Fprintln(stderr, "rhythm echo:", err)
			return exitError
		}
		fmt.Fprintf(&b, "Wrote %s, which starts Echo's connector (it stays off in production).\n", target)
	case importsEcho(filepath.Join(dir, inst)):
		fmt.Fprintf(&b, "%s already starts Echo's connector; nothing to add there.\n", inst)
	default:
		fmt.Fprintf(&b, "You already have %s; add this inside its register(), which must be async:\n\n"+
			"  const { register: registerEcho } = await import('@rhythmsh/echo/next');\n  await registerEcho();\n", inst)
	}
	fmt.Fprintf(&b, `
Wrap your next.config so mail libraries run outside the bundle and get captured:

  import { withEcho } from '@rhythmsh/echo/next';
  export default withEcho(nextConfig);

Environment (.env.local, or the Preview environment on Vercel):

  RHYTHM_ECHO_URL=%s%s

Then: rhythm echo up, and rhythm echo watch while you use the app.
`, url, tokenLine(token))
	_, _ = stdout.Write(b.Bytes())
	return exitOK
}

// importsEcho reports whether a source file already loads the connector.
func importsEcho(path string) bool {
	b, err := os.ReadFile(path) //nolint:gosec // the user's own project file
	return err == nil && bytes.Contains(b, []byte("@rhythmsh/echo"))
}

func tokenLine(t string) string {
	if t == "" {
		return ""
	}
	return "\n  RHYTHM_ECHO_TOKEN=<the token you started Echo with>"
}

// caHint is where Echo's CA certificate is, without creating it.
func caHint() string {
	p, err := proxy.DefaultCAPath()
	if err != nil {
		return "the path printed by: rhythm echo ca"
	}
	if _, err := os.Stat(p); err != nil {
		return p + " (rhythm echo ca creates it)"
	}
	return p
}

// binaryPath is this binary's absolute path, for settings like
// sendmail_path that run without a PATH (php-fpm).
func binaryPath() string {
	if p, err := os.Executable(); err == nil {
		if r, err := filepath.EvalSymlinks(p); err == nil {
			return r
		}
		return p
	}
	return "/usr/local/bin/rhythm-echo"
}

func second(s []string) string {
	if len(s) > 1 {
		return s[1]
	}
	return ""
}
