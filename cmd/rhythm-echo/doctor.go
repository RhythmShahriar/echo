// Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
// SPDX-License-Identifier: MIT

package main

import (
	"bufio"
	"cmp"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/smtp"
	"net/url"
	"os"
	"os/exec"
	"regexp"
	"runtime"
	"strings"
	"time"

	"github.com/charmbracelet/lipgloss"

	"github.com/RhythmShahriar/echo/client"
	"github.com/RhythmShahriar/echo/config"
	"github.com/RhythmShahriar/echo/connectors/wordpress"
	"github.com/RhythmShahriar/echo/model"
	"github.com/RhythmShahriar/echo/sendmail"
	"github.com/RhythmShahriar/echo/wire"
	"github.com/RhythmShahriar/rhythm/kit/ui"
)

// cmdDoctor checks every capture layer end to end: Echo is up, its SMTP
// sink and proxy answer, the connector for the project's stack is active,
// and canary messages sent through each layer arrive. Only the checks for
// the stack detectStack finds run, so a plain PHP site isn't told to
// install the WordPress connector. It never sends anything real: canaries
// go to .invalid addresses, or to Twilio's API only through a sealed
// connector, which answers in Twilio's place.
func cmdDoctor(s *session) int {
	if err := s.needConfig(); err != nil {
		return s.fail(err)
	}
	d := &doctor{s: s}
	s.view.Header(version, "doctor")

	h, err := s.echo.Health(s.ctx)
	if err != nil {
		if name := serviceAtURL(s.ctx, s.echo.URL); name != "" && name != "Echo" {
			d.fail("Echo", fmt.Sprintf("%s is %s, not Echo; start Echo on a free port (rhythm echo up --listen 127.0.0.1:18025) "+
				"and set listen: in %s to it", s.echo.URL, name, s.configName()))
		} else {
			d.fail("Echo", err.Error())
		}
		return d.end()
	}
	d.pass("Echo", fmt.Sprintf("%s mode at %s, %d providers recognized", h.Mode, s.echo.URL, len(h.Providers)))
	d.run = "doctor-" + time.Now().UTC().Format("150405")
	smtpAddr, proxyAddr, reported := s.captureAddrs()

	// SMTP sink.
	if smtpAddr != "" {
		d.smtp(smtpAddr, reported)
	}

	// Proxy.
	if proxyAddr != "" {
		if c, err := net.DialTimeout("tcp", proxyAddr, 2*time.Second); err != nil {
			d.fail("Proxy", proxyAddr+" isn't listening")
		} else {
			_ = c.Close()
			d.pass("Proxy", proxyAddr+" is listening (set HTTPS_PROXY to it for SDKs that skip the connector)")
		}
	}

	site := strings.TrimRight(s.cfg.Site, "/")
	reason, live := productionReason(s.cfg, s.cfg.Site)
	if reason != "" {
		d.fail("Site", reason)
	}
	dir := s.projectDir()
	switch stack := detectStack(dir); stack {
	case stackWordPress:
		d.wordpress(site, live, true)
	case stackLaravel:
		d.laravel(dir)
	case stackNext, stackNode:
		d.node(dir, stack, h.Mode)
	case stackPHP:
		d.note("PHP", "no connector to check: mail() reaches Echo through sendmail_path or the SMTP sink "+
			"(rhythm echo install generic shows how)")
	default:
		// The site may live elsewhere (a staging server): a WordPress
		// connector there still answers its ping.
		if site != "" && !live {
			d.wordpress(site, live, false)
		} else {
			d.note("Stack", "no WordPress, Laravel, Next.js, Node or PHP project in "+displayDir(dir)+
				", so no connector was checked; connect one: rhythm echo install")
		}
	}

	// Flush commands.
	for _, f := range s.cfg.Flush {
		out, err := shell(s.ctx, f).CombinedOutput()
		switch {
		case s.ctx.Err() != nil:
			_, _ = s.echo.Clear(context.WithoutCancel(s.ctx), client.Filter{Run: d.run})
			return s.fail(s.ctx.Err())
		case err != nil:
			d.fail("Flush", fmt.Sprintf("%q: %v %s", f, err, strings.TrimSpace(lastLine(string(out)))))
		default:
			d.pass("Flush", f)
		}
	}
	_, _ = s.echo.Clear(context.WithoutCancel(s.ctx), client.Filter{Run: d.run})
	return d.end()
}

// doctor counts and prints doctor's checks.
type doctor struct {
	s         *session
	run       string // the run ID canaries are tagged with
	ok, fails int
}

func (d *doctor) pass(label, detail string) { d.ok++; d.s.doctorLine(doctorOK, label, detail) }
func (d *doctor) fail(label, detail string) { d.fails++; d.s.doctorLine(doctorFail, label, detail) }
func (d *doctor) note(label, detail string) { d.s.doctorLine(doctorNote, label, detail) }
func (d *doctor) end() int                  { return d.s.doctorEnd(d.ok, d.fails) }

// smtp sends a canary through the SMTP sink at addr. When it doesn't
// reach Echo, it says what is on that port instead, since a Mailpit left
// on 1025 takes the mail and Echo never sees it.
func (d *doctor) smtp(addr string, reported bool) {
	s := d.s
	const action = "smtp"
	msg := "From: doctor@echo.invalid\r\nTo: canary@echo.invalid\r\nSubject: Echo doctor\r\n" + wire.RunHeader + ": " + d.run + "/" + action + "\r\n\r\ncanary\r\n"
	var auth smtp.Auth
	if s.echo.Token != "" {
		auth = sendmail.TokenAuth(s.echo.Token)
	}
	other := func() string {
		if name := serviceAtSMTP(addr); name != "" && name != "Echo" {
			return name
		}
		return ""
	}
	if err := smtp.SendMail(addr, auth, "doctor@echo.invalid", []string{"canary@echo.invalid"}, []byte(msg)); err != nil {
		switch name := other(); {
		case name != "":
			d.fail("SMTP", fmt.Sprintf("%s is %s's SMTP, not Echo's sink: %v", addr, name, err))
		case reported:
			d.fail("SMTP", fmt.Sprintf("%s: %v (Echo says its sink listens there)", addr, err))
		default:
			d.fail("SMTP", fmt.Sprintf("%s: %v (is Echo running with this config?)", addr, err))
		}
		return
	}
	if got := s.wait(d.run, action, 1); got >= 1 {
		d.pass("SMTP", addr+" receives mail")
		return
	}
	if name := other(); name != "" {
		d.fail("SMTP", fmt.Sprintf("%s is %s, not Echo's sink, so mail sent there never reaches Echo; stop %s "+
			"or start Echo with --smtp on a free port (and set smtp: to it)", addr, name, name))
		return
	}
	d.fail("SMTP", "mail was accepted but didn't arrive in Echo")
}

// wordpress pings the WordPress connector at site and sends its canaries.
// A live site gets no canaries: doctor would send them through the real
// site. local says the project's files are WordPress; when they aren't,
// a site with no connector is only a note, not advice to install one.
func (d *doctor) wordpress(site string, live, local bool) {
	s := d.s
	switch {
	case live:
		d.note("WordPress", "not checked: doctor sends canaries through the site and this one is live")
		return
	case site == "":
		d.note("WordPress", "no site: in "+s.configName()+", so the site connector wasn't checked")
		return
	}
	ping, err := s.wpPing(site)
	switch {
	case err != nil && !local:
		d.note("Site", "no Echo connector answered at "+site+"; connect the site: rhythm echo install")
	case err != nil:
		d.note("WordPress", "no connector answered at "+site+" ("+err.Error()+"); install it: rhythm echo install wordpress")
	case !ping.Active:
		d.fail("WordPress", "the connector is installed but off: "+ping.Reason)
	default:
		detail := fmt.Sprintf("active (%s), layers: %s", ping.Environment, strings.Join(ping.Layers, ", "))
		if !ping.Echo.Reachable {
			d.fail("WordPress", "active, but it can't reach Echo at "+ping.Echo.URL+" (in Docker use host.docker.internal)")
		} else {
			d.pass("WordPress", detail)
		}
		for _, w := range ping.Warnings {
			d.note("WordPress", w)
		}
		if err := s.wpCanary(site, d.run, "wordpress"); err != nil {
			d.fail("Canaries", err.Error())
		} else if got := s.wait(d.run, "wordpress", 2); got >= 2 {
			d.pass("Canaries", "wp_mail and wp_remote_post were both caught")
		} else {
			d.fail("Canaries", fmt.Sprintf("only %d of 2 canaries arrived", got))
		}
	}
}

// laravel runs the Laravel connector's canary command in dir.
func (d *doctor) laravel(dir string) {
	s := d.s
	cmd := exec.CommandContext(s.ctx, "php", "artisan", "echo:canary", "--json")
	cmd.Dir = dir
	cmd.WaitDelay = shellWaitDelay
	cmd.Env = append(os.Environ(), "RHYTHM_ECHO_RUN="+d.run+"/laravel")
	if out, err := cmd.CombinedOutput(); err != nil {
		d.fail("Laravel", strings.TrimSpace(lastLine(string(out))))
	} else if got := s.wait(d.run, "laravel", 2); got >= 2 {
		d.pass("Laravel", "mail and Http canaries were both caught")
	} else {
		d.fail("Laravel", fmt.Sprintf("only %d of 2 canaries arrived", got))
	}
}

// nodeCanary is the script doctor runs with the Node connector loaded. It
// sends nothing unless the connector is on and sealed: then the request
// to Twilio's API stops at the connector, which asks Echo, and Echo
// answers in Twilio's place. The numbers are Twilio's test numbers and
// the account doesn't exist, should it ever get out.
const nodeCanary = `const s = globalThis[Symbol.for('rhythm.echo.state')];
if (!s || !s.active) {
  console.error('the connector did not switch on (VERCEL_ENV=production, or not installed in this project?)');
  process.exit(2);
}
if (s.mode !== 'seal') {
  console.error('the connector is not sealed, so the canary was not sent');
  process.exit(2);
}
const res = await fetch('https://api.twilio.com/2010-04-01/Accounts/ACechodoctor/Messages.json', {
  method: 'POST',
  headers: { 'content-type': 'application/x-www-form-urlencoded' },
  body: 'To=%2B15005550006&From=%2B15005550001&Body=Rhythm+Echo+canary',
});
const { flush } = await import('@rhythmsh/echo');
await flush();
console.log(res.status);
`

// node runs nodeCanary in dir with the connector's register hook, and
// checks Echo caught the request and answered for Twilio. A sealed
// Echo is required: in observe mode the request would really go out.
func (d *doctor) node(dir, stack, mode string) {
	s := d.s
	label := stackLabel(stack)
	if mode != "seal" {
		d.note(label, "not checked: the canary calls Twilio's API, so it runs only with Echo sealed (rhythm echo up --seal)")
		return
	}
	bin, err := exec.LookPath("node")
	if err != nil {
		d.note(label, "not checked: node isn't on PATH")
		return
	}
	const action = "node"
	cmd := exec.CommandContext(s.ctx, bin, "--import", "@rhythmsh/echo/register", "--input-type=module", "-e", nodeCanary) //nolint:gosec // a fixed script
	cmd.Dir = dir
	cmd.WaitDelay = shellWaitDelay
	cmd.Env = append(os.Environ(), "RHYTHM_ECHO_URL="+s.echo.URL, "RHYTHM_ECHO_RUN="+d.run+"/"+action)
	if s.echo.Token != "" {
		cmd.Env = append(cmd.Env, "RHYTHM_ECHO_TOKEN="+s.echo.Token)
	}
	out, err := cmd.CombinedOutput()
	if err != nil {
		why := strings.TrimSpace(lastLine(string(out)))
		if strings.Contains(string(out), "ERR_MODULE_NOT_FOUND") || strings.Contains(string(out), "Cannot find package") {
			why = "@rhythmsh/echo isn't installed in " + displayDir(dir) + "; add it: rhythm echo install " + stack
		}
		d.fail(label, why)
		return
	}
	got, _ := s.echo.Wait(s.ctx, client.Filter{Run: d.run, Action: action}, 1, 500*time.Millisecond, 8*time.Second)
	switch {
	case len(got) == 0:
		d.fail(label, "the connector is on, but the canary didn't arrive in Echo")
	case got[0].Status != model.Faked:
		d.fail(label, fmt.Sprintf("the canary arrived %s, not faked: Echo should answer for Twilio", got[0].Status))
	default:
		d.pass(label, "the connector caught a fetch to Twilio and Echo answered for it")
	}
}

// displayDir names a folder in a message: "this folder" for the working one.
func displayDir(dir string) string {
	if r := rel(dir); r != "." && r != "" {
		return r
	}
	return "this folder"
}

// captureAddrs are the SMTP sink and proxy addresses doctor tests, ""
// for one that's off. A running Echo reports them in /v1/health; one too
// old to say leaves the config's, which may not be what Echo was started
// with. reported says the SMTP address came from Echo. A sink listening
// on every interface (0.0.0.0:1025) is dialed on the host of Echo's URL.
func (s *session) captureAddrs() (smtpAddr, proxyAddr string, reported bool) {
	smtpAddr, proxyAddr = s.cfg.SMTP, s.cfg.Proxy
	var h struct {
		SMTP  *string `json:"smtp"`
		Proxy *string `json:"proxy"`
	}
	if err := s.healthJSON(&h); err == nil {
		if h.SMTP != nil {
			smtpAddr, reported = *h.SMTP, true
		}
		if h.Proxy != nil {
			proxyAddr = *h.Proxy
		}
	}
	host := ""
	if u, err := url.Parse(s.echo.URL); err == nil {
		host = u.Hostname()
	}
	return dialAddr(smtpAddr, host), dialAddr(proxyAddr, host), reported
}

// dialAddr is a listen address as a client reaches it: "" when it's off,
// and urlHost in place of a wildcard host.
func dialAddr(addr, urlHost string) string {
	if addr == "" || addr == "off" {
		return ""
	}
	h, p, err := net.SplitHostPort(addr)
	if err != nil || (h != "" && h != "0.0.0.0" && h != "::") {
		return addr
	}
	return net.JoinHostPort(cmp.Or(urlHost, "127.0.0.1"), p)
}

// healthJSON decodes Echo's /v1/health into v. client.Health has no SMTP
// or proxy fields until the catcher's are released, so doctor reads them
// itself.
func (s *session) healthJSON(v any) error {
	req, err := http.NewRequestWithContext(s.ctx, http.MethodGet, s.echo.URL+"/v1/health", nil)
	if err != nil {
		return err
	}
	if s.echo.Token != "" {
		req.Header.Set(wire.TokenHeader, s.echo.Token)
	}
	resp, err := (&http.Client{Timeout: 5 * time.Second}).Do(req)
	if err != nil {
		return err
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("health: %s", resp.Status)
	}
	return json.NewDecoder(io.LimitReader(resp.Body, 1<<20)).Decode(v)
}

// probeTimeout bounds each look at what answers on a port.
const probeTimeout = 2 * time.Second

// mailCatchers are the mail catchers a page or greeting can name.
var mailCatchers = regexp.MustCompile(`(?i)\b(mailpit|mailhog|maildev)\b`)

// catcherLabel spells the mail catcher named in s, or "" for none.
func catcherLabel(s string) string {
	switch strings.ToLower(mailCatchers.FindString(s)) {
	case "mailpit":
		return "Mailpit"
	case "mailhog":
		return "MailHog"
	case "maildev":
		return "MailDev"
	}
	return ""
}

// serviceAtSMTP names what greets on an SMTP port: Echo (whose sink
// greets as echo.local), Mailpit, MailHog or MailDev; "" when nothing
// answers or the greeting names none of them.
func serviceAtSMTP(addr string) string {
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
	return catcherLabel(line)
}

// serviceAtURL names what answers at base when it isn't Echo: Mailpit
// from its info route, or a mail catcher that names itself on its page.
// "" when nothing answers or it can't tell.
func serviceAtURL(ctx context.Context, base string) string {
	c := &http.Client{Timeout: probeTimeout, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	get := func(path string) (int, []byte) {
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, strings.TrimRight(base, "/")+path, nil)
		if err != nil {
			return 0, nil
		}
		resp, err := c.Do(req)
		if err != nil {
			return 0, nil
		}
		defer func() { _ = resp.Body.Close() }()
		b, _ := io.ReadAll(io.LimitReader(resp.Body, 64<<10))
		return resp.StatusCode, b
	}
	if code, b := get("/api/v1/info"); code == http.StatusOK {
		var info map[string]any
		if json.Unmarshal(b, &info) == nil && info["Version"] != nil && (info["Database"] != nil || info["RuntimeStats"] != nil) {
			return "Mailpit"
		}
	}
	if code, b := get("/"); code != 0 {
		return catcherLabel(string(b))
	}
	return ""
}

// productionReason says why site counts as a live site under the
// config's production: list, or "" when it doesn't; live is false when
// an entry only catches it by accident. It names the entry
// and says only what Echo does about it today: http actions refuse a URL
// that starts with an entry exactly as written; command actions run all
// the same. Replays never read the list; they send only to staging:
// and local addresses. An entry that differs only in case, port or a
// subdomain is not matched by the actions, so doctor says so rather
// than promise a refusal. A prefix of another host's URL is matched,
// which refuses a staging site's actions, so doctor names that too.
//
// This stands in for config.ProductionReason until that is released.
func productionReason(cfg config.Config, site string) (reason string, live bool) {
	if site == "" {
		return "", false
	}
	// Actions ask for site plus a path, so an entry ending in / that
	// equals site with a / added matches them too.
	withSlash := strings.TrimRight(site, "/") + "/"
	for _, p := range cfg.Production {
		if p == "" {
			continue
		}
		prefix, same := strings.HasPrefix(site, p) || strings.HasPrefix(withSlash, p), sameSite(site, p)
		if prefix && !same {
			// https://shop.example is a prefix of https://shop.example.staging.test.
			return fmt.Sprintf("%s is another host but starts with production: entry %s, so its http actions are "+
				"refused; end the entry with a / to match only that site", site, p), false
		}
		if prefix {
			return fmt.Sprintf("%s is listed under production: (%s). http actions to it are refused while command actions "+
				"still run against it; point site: at a staging copy", site, p), true
		}
		if same {
			return fmt.Sprintf("%s looks like production: entry %s. Actions match an entry exactly as written, "+
				"so they would run against it; point site: at a staging copy or write the entry as %s", site, p, site), true
		}
	}
	return "", false
}

// sameSite reports whether a and b name the same host, or a is on a
// subdomain of b, ignoring case, scheme and port.
func sameSite(a, b string) bool {
	host := func(raw string) string {
		if !strings.Contains(raw, "://") {
			raw = "https://" + raw
		}
		u, err := url.Parse(raw)
		if err != nil {
			return ""
		}
		return strings.TrimSuffix(strings.ToLower(u.Hostname()), ".")
	}
	ha, hb := host(a), host(b)
	return ha != "" && hb != "" && (ha == hb || strings.HasSuffix(ha, "."+hb))
}

// shellWaitDelay is how long a canceled command's output may stay open.
const shellWaitDelay = 2 * time.Second

// shell runs line in the platform's shell: sh -c, or cmd /C on Windows,
// which has no sh. Canceling ctx kills the shell, but a child it started
// (sleep, wp cron) can live on and hold the output pipe open, so Wait
// would block until that child exits. WaitDelay closes the pipe instead,
// so Ctrl+C returns within two seconds.
//
// This stands in for runner.Shell, which kills the whole process group,
// until that is released.
func shell(ctx context.Context, line string) *exec.Cmd {
	name, flag := "sh", "-c"
	if runtime.GOOS == "windows" {
		name, flag = "cmd", "/C"
	}
	cmd := exec.CommandContext(ctx, name, flag, line) //nolint:gosec // the user's configured command
	cmd.WaitDelay = shellWaitDelay
	return cmd
}

// How a doctor check turned out: a glyph on a terminal, a word in --plain.
type doctorResult struct{ glyph, word, color string }

var (
	doctorOK   = doctorResult{"✓", "ok", ui.Ok}
	doctorFail = doctorResult{"✗", "FAIL", ui.Danger}
	doctorNote = doctorResult{"·", "note", ui.Muted}
)

func (s *session) doctorLine(r doctorResult, label, detail string) {
	if s.o.quiet && r != doctorFail {
		return
	}
	if s.plain {
		fmt.Fprintf(s.stdout, "%-4s  %s: %s\n", r.word, label, detail)
		return
	}
	mark := lipgloss.NewStyle().Foreground(lipgloss.Color(r.color)).Bold(true).Render(r.glyph)
	fmt.Fprintf(s.stdout, "  %s %s %s\n", mark, lipgloss.NewStyle().Bold(true).Width(13).Render(label), detail)
}

func (s *session) doctorEnd(ok, fails int) int {
	if !s.o.quiet {
		fmt.Fprintf(s.stdout, "\n  %s passed, %d failed.\n\n", plural(ok, "check"), fails)
	}
	if fails > 0 {
		return exitFindings
	}
	return exitOK
}

func (s *session) wait(run, action string, n int) int {
	got, _ := s.echo.Wait(s.ctx, client.Filter{Run: run, Action: action}, n, 500*time.Millisecond, 8*time.Second)
	return len(got)
}

type wpPing struct {
	Active      bool     `json:"active"`
	Reason      string   `json:"reason"`
	Environment string   `json:"environment"`
	Layers      []string `json:"layers"`
	Warnings    []string `json:"warnings"`
	Echo        struct {
		URL       string `json:"url"`
		Reachable bool   `json:"reachable"`
		Mode      string `json:"mode"`
	} `json:"echo"`
}

func (s *session) wpPing(site string) (wpPing, error) {
	var p wpPing
	body, err := s.wpRequest(http.MethodGet, site, wordpress.PingRoute, "")
	if err != nil {
		return p, err
	}
	if err := json.Unmarshal(body, &p); err != nil {
		return p, fmt.Errorf("unexpected answer from the ping route")
	}
	return p, nil
}

func (s *session) wpCanary(site, run, action string) error {
	_, err := s.wpRequest(http.MethodPost, site, wordpress.CanaryRoute, client.RunValue(run, action))
	return err
}

// wpRequest calls the connector's REST route with ?rest_route=, which works
// with plain permalinks too.
func (s *session) wpRequest(method, site, route, run string) ([]byte, error) {
	req, err := http.NewRequestWithContext(s.ctx, method, site+"/?rest_route=/"+route, nil)
	if err != nil {
		return nil, err
	}
	if s.echo.Token != "" {
		req.Header.Set(wire.TokenHeader, s.echo.Token)
	}
	if run != "" {
		req.Header.Set(wire.RunHeader, run)
	}
	resp, err := (&http.Client{Timeout: 15 * time.Second}).Do(req)
	if err != nil {
		return nil, err
	}
	defer func() { _ = resp.Body.Close() }()
	b, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if resp.StatusCode != 200 {
		return nil, fmt.Errorf("%s %s", resp.Status, strings.TrimSpace(lastLine(string(b))))
	}
	return b, nil
}

func lastLine(s string) string {
	s = strings.TrimSpace(s)
	if i := strings.LastIndex(s, "\n"); i >= 0 {
		s = s[i+1:]
	}
	if len(s) > 200 {
		s = s[:200] + "…"
	}
	return s
}
