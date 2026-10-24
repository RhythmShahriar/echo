package main

import (
	"bytes"
	"cmp"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/RhythmShahriar/echo/client"
	"github.com/RhythmShahriar/echo/connectors/wordpress"
	"github.com/RhythmShahriar/echo/proxy"
	"github.com/RhythmShahriar/rhythm/kit/cli"
)

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
	pos, err := cli.ParseInterspersed(fs, args)
	if err != nil || len(pos) == 0 {
		fmt.Fprintln(stderr, "rhythm echo: usage: rhythm echo install <wordpress|laravel|next|node|generic> [path] [--url URL] [--token T]")
		return exitUsage
	}
	echoURL := cmp.Or(*url, os.Getenv("RHYTHM_ECHO_URL"), client.DefaultURL)
	tok := cmp.Or(*token, os.Getenv("RHYTHM_ECHO_TOKEN"))
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

  composer require --dev rhythmsh/echo-laravel

Then in .env (staging or local only; the connector stays off in production):

  RHYTHM_ECHO_URL=%s%s
  QUEUE_CONNECTION=sync          # or flush with: php artisan queue:work --stop-when-empty

Check it: php artisan echo:status   ·   then: rhythm echo doctor
Packages that skip Laravel's mailer and Http client (raw Guzzle, SDKs) are caught
through Echo's proxy: rhythm echo up --proxy 127.0.0.1:8026 --mitm, then set
HTTPS_PROXY=http://127.0.0.1:8026 and trust: %s
`, echoURL, tokenLine(tok), caHint())
		return exitOK
	case "next", "nextjs":
		return installNext(cmp.Or(second(pos), "."), echoURL, tok, stdout, stderr)
	case "node":
		fmt.Fprintf(stdout, `Node: add the package and load it before your app.

  npm install --save-dev @rhythmsh/echo
  node --import @rhythmsh/echo/register server.js      # or: import '@rhythmsh/echo/register'

Environment (never production; the connector stays off there):

  RHYTHM_ECHO_URL=%s%s

Express/Fastify: app.use(echoMiddleware()) ties messages to the request that caused them.
`, echoURL, tokenLine(tok))
		return exitOK
	case "generic", "php":
		fmt.Fprintf(stdout, `Any stack: point mail and outbound HTTPS at Echo.

  Mail (SMTP):      host 127.0.0.1, port 1025, any username and password
  Mail (PHP):       sendmail_path = "rhythm-echo sendmail -t -i"   (php.ini or php-fpm pool)
  HTTP(S):          rhythm echo up --proxy 127.0.0.1:8026 --mitm
                    HTTPS_PROXY=http://127.0.0.1:8026  https_proxy=http://127.0.0.1:8026
                    and trust Echo's CA: %s
                    (curl, PHP cURL, Python requests and Go honor these; Node needs
                    NODE_USE_ENV_PROXY=1 and NODE_EXTRA_CA_CERTS on 22.21+/24.5+)
  Tie to actions:   send X-Echo-Run: <run>/<action> on the request that triggers the work,
                    or set RHYTHM_ECHO_RUN for scripts and workers.

For a seal that can't be bypassed, see docs/seal in the Echo repository: the app on an
internal Docker network or behind a firewall, with Echo as its only way out.
`, caHint())
		return exitOK
	}
	fmt.Fprintf(stderr, "rhythm echo: install: unknown stack %q (wordpress, laravel, next, node, generic)\n", pos[0])
	return exitUsage
}

func installWordPress(target string, s wordpress.Settings, wpcli bool, container string, uninstall bool, stdout, stderr io.Writer) int {
	fail := func(err error) int {
		fmt.Fprintln(stderr, "rhythm echo:", err)
		return exitError
	}
	switch {
	case container != "":
		script := wordpress.UninstallScript()
		if !uninstall {
			var err error
			if script, err = s2(wordpress.Script(s)); err != nil {
				return fail(err)
			}
		}
		cmd := exec.Command("docker", "exec", "-i", container, "wp", "eval-file", "-", "--allow-root") //nolint:gosec // the user's container
		cmd.Stdin, cmd.Stdout, cmd.Stderr = strings.NewReader(script), stdout, stderr
		if err := cmd.Run(); err != nil {
			return fail(fmt.Errorf("running wp-cli in %s: %w (is wp-cli installed in the container? or copy the files with docker cp)", container, err))
		}
	case wpcli:
		script := wordpress.UninstallScript()
		if !uninstall {
			var err error
			if script, err = s2(wordpress.Script(s)); err != nil {
				return fail(err)
			}
		}
		cmd := exec.Command("wp", "eval-file", "-", "--path="+target) //nolint:gosec // the user's site
		cmd.Stdin, cmd.Stdout, cmd.Stderr = strings.NewReader(script), stdout, stderr
		if err := cmd.Run(); err != nil {
			return fail(fmt.Errorf("wp eval-file: %w", err))
		}
	case uninstall:
		if err := wordpress.Uninstall(target); err != nil {
			if wordpress.IsNotInstalled(err) {
				fmt.Fprintln(stdout, "The connector isn't installed there.")
				return exitOK
			}
			return fail(err)
		}
	default:
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
	fmt.Fprintf(stdout, `Installed the Echo connector (wp-content/%s), reporting to %s.

  It stays off on production (WP_ENVIRONMENT_TYPE=production) unless forced.
  Next: rhythm echo up, then rhythm echo doctor (with site: in .echo/config.yaml)
  Flush async mail and webhooks after actions: add to .echo/config.yaml
    flush: ["wp cron event run --due-now", "wp action-scheduler run"]
`, wordpress.PluginPath, s.URL)
	if strings.Contains(s.URL, "127.0.0.1") && container != "" {
		fmt.Fprintln(stdout, "  In Docker, 127.0.0.1 is the container itself: reinstall with --url http://host.docker.internal:8025")
	}
	return exitOK
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
		}
	}
	wrote := ""
	if inst == "" {
		target := "instrumentation.ts"
		if fi, err := os.Stat(filepath.Join(dir, "src", "app")); err == nil && fi.IsDir() { //nolint:gosec // the user's own project folder
			target = "src/instrumentation.ts"
		}
		body := "export async function register() {\n  const { register } = await import('@rhythmsh/echo/next');\n  register();\n}\n"
		if err := os.WriteFile(filepath.Join(dir, target), []byte(body), 0o644); err != nil { //nolint:gosec // a project source file
			fmt.Fprintln(stderr, "rhythm echo:", err)
			return exitError
		}
		wrote = target
	}
	var b bytes.Buffer
	fmt.Fprintf(&b, "Next.js: add the package.\n\n  npm install --save-dev @rhythmsh/echo\n\n")
	if wrote != "" {
		fmt.Fprintf(&b, "Wrote %s, which starts Echo's connector (it stays off in production).\n", wrote)
	} else {
		fmt.Fprintf(&b, "You already have %s; add this inside register():\n\n  const { register: echo } = await import('@rhythmsh/echo/next');\n  echo();\n", inst)
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

func tokenLine(t string) string {
	if t == "" {
		return ""
	}
	return "\n  RHYTHM_ECHO_TOKEN=<the token you started Echo with>"
}

func caHint() string {
	if ca, err := proxy.LoadCA(""); err == nil {
		return ca.CertPath
	}
	return "the path printed by: rhythm echo ca"
}

func second(s []string) string {
	if len(s) > 1 {
		return s[1]
	}
	return ""
}

func s2(s string, err error) (string, error) { return s, err }
