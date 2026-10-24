// Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
// SPDX-License-Identifier: MIT

package main

import (
	"bufio"
	"cmp"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/charmbracelet/huh"
	"github.com/charmbracelet/x/term"

	"github.com/RhythmShahriar/echo/config"
)

// DDEV, Lando and Laravel Sail all run Mailpit on 8025 and 1025, Echo's
// default ports. When up can't bind them it finds out what holds them,
// says so and offers a free pair, instead of blaming an Echo that isn't
// running.

// freeBase is where the search for a free API and SMTP pair starts:
// 18025 and 11025, then one up each time. No other mail catcher uses
// them by default. Tests move it into their own port range.
var freeBase = [2]int{18025, 11025}

// freeTries is how many pairs the search tries before giving up.
const freeTries = 100

// bindError is a port up couldn't listen on.
type bindError struct {
	what string // "API" or "SMTP"
	addr string
	err  error
}

func (e *bindError) Error() string {
	if e.what == "SMTP" {
		return fmt.Sprintf("SMTP on %s: %v", e.addr, e.err)
	}
	return fmt.Sprintf("listening on %s: %v", e.addr, e.err)
}

func (e *bindError) Unwrap() error { return e.err }

// listenPair binds the API and, unless smtp is "", the SMTP address. On a
// failure nothing stays open, and the error is a *bindError for the
// first port that failed.
func listenPair(api, smtp string) (ln, sl net.Listener, err error) {
	if ln, err = net.Listen("tcp", api); err != nil {
		return nil, nil, &bindError{"API", api, err}
	}
	if smtp != "" {
		if sl, err = net.Listen("tcp", smtp); err != nil {
			_ = ln.Close()
			return nil, nil, &bindError{"SMTP", smtp, err}
		}
	}
	return ln, sl, nil
}

// askFreePorts asks on a terminal whether up may move to the free pair.
// It's a variable so tests can answer.
var askFreePorts = func(s *session, question string) bool {
	if !s.tty || !term.IsTerminal(os.Stdin.Fd()) {
		return false
	}
	ok := true
	err := huh.NewConfirm().Title(question).Affirmative("Yes").Negative("No").Value(&ok).
		WithTheme(huh.ThemeCharm()).Run()
	return err == nil && ok
}

// moveAside handles a port up couldn't bind. When another mail catcher
// (or anything else) holds it, it names that and offers the next free
// pair; with auto (--free-ports) or a yes on the terminal it saves the
// pair to the config, prints what the site needs to point at it, binds
// it and sets listen and smtp to it. Otherwise it returns the error to
// show.
func moveAside(s *session, listen, smtp *string, failed error, auto bool) (ln, sl net.Listener, err error) {
	var be *bindError
	if !errors.As(failed, &be) || !errors.Is(be.err, syscall.EADDRINUSE) {
		return nil, nil, failed
	}
	smtpOn := *smtp != ""
	who := identify(be.addr, be.what == "SMTP")
	if who == "Echo" {
		return nil, nil, fmt.Errorf("Echo is already running on %s (use that one, or stop it first)", be.addr) //nolint:staticcheck // Echo is a name
	}
	host := hostOf(*listen)
	api, mail, found := nextFreePair(host, smtpOn)
	if !found {
		return nil, nil, failed
	}
	if !smtpOn {
		mail = ""
	}
	pair := api
	hint := "rhythm echo up --listen " + api
	if smtpOn {
		pair += " and " + mail
		hint += " --smtp " + mail
	}
	held := fmt.Sprintf("%s (another program has this port)", failed)
	if who != "" {
		held = fmt.Sprintf("%s is already on %s", who, be.addr)
	}
	path := cmp.Or(s.o.configPath, config.Path())
	if !auto && !askFreePorts(s, fmt.Sprintf("%s. Run Echo on %s instead and save them to %s?", held, pair, path)) {
		return nil, nil, fmt.Errorf("%s; run Echo next to it: %s", held, hint)
	}
	if ln, sl, err = listenPair(api, mail); err != nil {
		return nil, nil, err
	}
	*listen = api
	if smtpOn {
		*smtp = mail
	}
	fmt.Fprintf(s.stderr, "%s. Echo runs on %s instead.\n", held, pair)
	if err := setAddresses(path, api, mail); err != nil {
		fmt.Fprintf(s.stderr, "Couldn't save them to %s: %v\n", path, err)
	} else {
		fmt.Fprintf(s.stderr, "Saved to %s, so the next up uses them too.\n", path)
	}
	fmt.Fprintln(s.stderr, "\nPoint the site at Echo:")
	exe, _ := os.Executable()
	for _, l := range envLines(guessStack("."), api, mail, exe) {
		fmt.Fprintln(s.stderr, "  "+l)
	}
	fmt.Fprintln(s.stderr)
	return ln, sl, nil
}

// hostOf is the host part of a listen address, 127.0.0.1 when it's empty.
func hostOf(addr string) string {
	h, _, err := net.SplitHostPort(addr)
	if err != nil || h == "" {
		return "127.0.0.1"
	}
	return h
}

// nextFreePair finds the first API and SMTP ports from freeBase upward
// that both can be bound on host right now.
func nextFreePair(host string, smtpOn bool) (api, smtp string, ok bool) {
	free := func(addr string) bool {
		l, err := net.Listen("tcp", addr)
		if err != nil {
			return false
		}
		_ = l.Close()
		return true
	}
	for i := range freeTries {
		api = net.JoinHostPort(host, strconv.Itoa(freeBase[0]+i))
		smtp = net.JoinHostPort(host, strconv.Itoa(freeBase[1]+i))
		if free(api) && (!smtpOn || free(smtp)) {
			return api, smtp, true
		}
	}
	return "", "", false
}

// probeTimeout bounds each look at a busy port: whatever holds it may not
// answer at all.
const probeTimeout = time.Second

// identify names what listens on addr: "Mailpit", "MailHog", "MailDev",
// "Echo", or "" for anything else. An SMTP port is known by its banner,
// an HTTP port by its API or its page.
func identify(addr string, smtp bool) string {
	addr = dialable(addr)
	if smtp {
		return fromBanner(addr)
	}
	return fromHTTP(addr)
}

// dialable turns a wildcard listen address into one a probe can reach.
func dialable(addr string) string {
	h, p, err := net.SplitHostPort(addr)
	if err != nil {
		return addr
	}
	if h == "" || h == "0.0.0.0" || h == "::" {
		h = "127.0.0.1"
	}
	return net.JoinHostPort(h, p)
}

// catchers are the mail catchers a page or banner can name.
var catchers = regexp.MustCompile(`(?i)\b(mailpit|mailhog|maildev)\b`)

func catcherName(s string) string {
	switch strings.ToLower(catchers.FindString(s)) {
	case "mailpit":
		return "Mailpit"
	case "mailhog":
		return "MailHog"
	case "maildev":
		return "MailDev"
	}
	return ""
}

// fromBanner reads the SMTP greeting: Mailpit and MailHog name
// themselves there; Echo's sink greets as echo.local.
func fromBanner(addr string) string {
	c, err := net.DialTimeout("tcp", addr, probeTimeout)
	if err != nil {
		return ""
	}
	defer func() { _ = c.Close() }()
	_ = c.SetDeadline(time.Now().Add(probeTimeout))
	line, _ := bufio.NewReader(io.LimitReader(c, 512)).ReadString('\n')
	if !strings.HasPrefix(line, "220") {
		return ""
	}
	if strings.Contains(line, "echo.local") {
		return "Echo"
	}
	return catcherName(line)
}

// fromHTTP asks the port what it is: Echo's health route, Mailpit's info
// route, then the page at / (Mailpit, MailHog and MailDev name
// themselves in its title).
func fromHTTP(addr string) string {
	c := &http.Client{Timeout: probeTimeout, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	get := func(path string) (int, []byte) {
		resp, err := c.Get("http://" + addr + path) //nolint:noctx // bounded by the client timeout
		if err != nil {
			return 0, nil
		}
		defer func() { _ = resp.Body.Close() }()
		b, _ := io.ReadAll(io.LimitReader(resp.Body, 64<<10))
		return resp.StatusCode, b
	}
	if code, b := get("/v1/health"); code == http.StatusOK && strings.Contains(string(b), `"wire"`) ||
		code == http.StatusUnauthorized && strings.Contains(string(b), "X-Echo-Token") {
		return "Echo"
	}
	if code, b := get("/api/v1/info"); code == http.StatusOK {
		var info map[string]any
		if json.Unmarshal(b, &info) == nil && info["Version"] != nil && (info["Database"] != nil || info["RuntimeStats"] != nil) {
			return "Mailpit"
		}
	}
	if code, b := get("/"); code != 0 {
		return catcherName(string(b))
	}
	return ""
}

// setAddresses writes listen and (unless it's "") smtp into the config
// file at path, creating it when there's none. Only those lines change: a
// key that's set gets the new value with its comment kept, a key that's
// commented out or missing is added. Everything else stays as it was.
func setAddresses(path, listen, smtp string) error {
	data, err := os.ReadFile(path) //nolint:gosec // the user's own project config
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	text := string(data)
	text = setKey(text, "listen", listen)
	if smtp != "" {
		text = setKey(text, "smtp", smtp)
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o750); err != nil { //nolint:gosec // the user's own --config path
		return err
	}
	if err := os.WriteFile(path, []byte(text), 0o600); err != nil { //nolint:gosec // the user's own --config path
		return err
	}
	_, err = config.Load(path) // never leave a file Echo can't read
	return err
}

// setKey sets a top-level "key: value" line in a YAML document.
func setKey(text, key, value string) string {
	set := regexp.MustCompile(`(?m)^` + key + `:([ \t]*)("[^"]*"|'[^']*'|[^\s#]*)([ \t]*)(#.*)?$`)
	if loc := set.FindStringSubmatchIndex(text); loc != nil {
		old := text[loc[4]:loc[5]]
		pad := text[loc[6]:loc[7]]
		// Keep a trailing comment in its column when the padding allows.
		if d := len(value) - len(old); loc[8] >= 0 && d != 0 {
			n := max(len(pad)-d, 1)
			pad = strings.Repeat(" ", n)
		}
		return text[:loc[4]] + value + pad + text[loc[7]:]
	}
	if text != "" && !strings.HasSuffix(text, "\n") {
		text += "\n"
	}
	return text + key + ": " + value + "\n"
}

// guessStack names the project in dir by its files: wordpress, laravel,
// next, node, php or "" for none of them.
func guessStack(dir string) string {
	has := func(pattern string) bool {
		m, _ := filepath.Glob(filepath.Join(dir, pattern))
		return len(m) > 0
	}
	switch {
	case has("wp-config.php") || has("wp-config-sample.php"):
		return "wordpress"
	case has("artisan"):
		return "laravel"
	case has("next.config.*"):
		return "next"
	case has("package.json"):
		return "node"
	case has("*.php"):
		return "php"
	}
	return ""
}

// envLines is what a site of the given stack needs to reach Echo on
// listen and smtp ("" when the sink is off). exe is the rhythm-echo
// binary, for PHP's sendmail_path.
func envLines(stack, listen, smtp, exe string) []string {
	out := []string{"RHYTHM_ECHO_URL=http://" + listen}
	if smtp == "" {
		return out
	}
	out = append(out, "RHYTHM_ECHO_SMTP="+smtp)
	host, port, _ := net.SplitHostPort(smtp)
	if exe == "" {
		exe = "rhythm-echo"
	}
	switch stack {
	case "laravel":
		out = append(out, "MAIL_MAILER=smtp", "MAIL_HOST="+host, "MAIL_PORT="+port+"   (Laravel .env)")
	case "wordpress", "php", "":
		// PHP runs sendmail_path through the shell, so the variable can
		// sit in front of the command.
		out = append(out, fmt.Sprintf(`sendmail_path = "RHYTHM_ECHO_SMTP=%s %s sendmail -t -i"   (php.ini)`, smtp, exe))
	}
	return out
}
