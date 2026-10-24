// Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
// SPDX-License-Identifier: MIT

package main

import (
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
	"runtime"
	"strings"
	"time"

	"github.com/charmbracelet/lipgloss"

	"github.com/RhythmShahriar/echo/client"
	"github.com/RhythmShahriar/echo/config"
	"github.com/RhythmShahriar/echo/connectors/wordpress"
	"github.com/RhythmShahriar/echo/sendmail"
	"github.com/RhythmShahriar/echo/wire"
	"github.com/RhythmShahriar/rhythm/kit/ui"
)

// cmdDoctor checks every capture layer end to end: Echo is up, the site's
// connector is active, and canary messages sent through each layer arrive.
// It never sends anything real: canaries go to .invalid addresses.
func cmdDoctor(s *session) int {
	ok, fails := 0, 0
	pass := func(label, detail string) { ok++; s.doctorLine(doctorOK, label, detail) }
	fail := func(label, detail string) { fails++; s.doctorLine(doctorFail, label, detail) }
	note := func(label, detail string) { s.doctorLine(doctorNote, label, detail) }
	s.view.Header(version, "doctor")

	h, err := s.echo.Health(s.ctx)
	if err != nil {
		fail("Echo", err.Error())
		return s.doctorEnd(ok, fails)
	}
	pass("Echo", fmt.Sprintf("%s mode at %s, %d providers recognized", h.Mode, s.echo.URL, len(h.Providers)))
	run := "doctor-" + time.Now().UTC().Format("150405")

	// SMTP sink.
	if s.cfg.SMTP != "" && s.cfg.SMTP != "off" {
		action := "smtp"
		msg := "From: doctor@echo.invalid\r\nTo: canary@echo.invalid\r\nSubject: Echo doctor\r\n" + wire.RunHeader + ": " + run + "/" + action + "\r\n\r\ncanary\r\n"
		var auth smtp.Auth
		if s.echo.Token != "" {
			auth = sendmail.TokenAuth(s.echo.Token)
		}
		if err := smtp.SendMail(s.cfg.SMTP, auth, "doctor@echo.invalid", []string{"canary@echo.invalid"}, []byte(msg)); err != nil {
			fail("SMTP", fmt.Sprintf("%s: %v (is Echo running with this config?)", s.cfg.SMTP, err))
		} else if got := s.wait(run, action, 1); got >= 1 {
			pass("SMTP", s.cfg.SMTP+" receives mail")
		} else {
			fail("SMTP", "mail was accepted but didn't arrive in Echo")
		}
	}

	// Proxy.
	if s.cfg.Proxy != "" && s.cfg.Proxy != "off" {
		if c, err := net.DialTimeout("tcp", s.cfg.Proxy, 2*time.Second); err != nil {
			fail("Proxy", s.cfg.Proxy+" isn't listening")
		} else {
			_ = c.Close()
			pass("Proxy", s.cfg.Proxy+" is listening (set HTTPS_PROXY to it for SDKs that skip the connector)")
		}
	}

	// WordPress connector. A live site gets no canaries: doctor would
	// send them through the real site.
	site := strings.TrimRight(s.cfg.Site, "/")
	reason, live := productionReason(s.cfg, s.cfg.Site)
	if reason != "" {
		fail("Site", reason)
	}
	if live {
		note("WordPress", "not checked: doctor sends canaries through the site, and this one is live")
	} else if site != "" {
		ping, err := s.wpPing(site)
		switch {
		case err != nil:
			note("WordPress", "no connector answered at "+site+" ("+err.Error()+"); install it: rhythm echo install wordpress")
		case !ping.Active:
			fail("WordPress", "the connector is installed but off: "+ping.Reason)
		default:
			detail := fmt.Sprintf("active (%s), layers: %s", ping.Environment, strings.Join(ping.Layers, ", "))
			if !ping.Echo.Reachable {
				fail("WordPress", "active, but it can't reach Echo at "+ping.Echo.URL+" (in Docker use host.docker.internal)")
			} else {
				pass("WordPress", detail)
			}
			for _, w := range ping.Warnings {
				note("WordPress", w)
			}
			if err := s.wpCanary(site, run, "wordpress"); err != nil {
				fail("Canaries", err.Error())
			} else if got := s.wait(run, "wordpress", 2); got >= 2 {
				pass("Canaries", "wp_mail and wp_remote_post were both caught")
			} else {
				fail("Canaries", fmt.Sprintf("only %d of 2 canaries arrived", got))
			}
		}
	} else {
		note("WordPress", "no site: in .echo/config.yaml, so the site connector wasn't checked")
	}

	// Laravel connector, when run from a Laravel project.
	if _, err := os.Stat("artisan"); err == nil {
		cmd := exec.CommandContext(s.ctx, "php", "artisan", "echo:canary", "--json")
		cmd.WaitDelay = shellWaitDelay
		cmd.Env = append(os.Environ(), "RHYTHM_ECHO_RUN="+run+"/laravel")
		if out, err := cmd.CombinedOutput(); err != nil {
			fail("Laravel", strings.TrimSpace(lastLine(string(out))))
		} else if got := s.wait(run, "laravel", 2); got >= 2 {
			pass("Laravel", "mail and Http canaries were both caught")
		} else {
			fail("Laravel", fmt.Sprintf("only %d of 2 canaries arrived", got))
		}
	}

	// Flush commands.
	for _, f := range s.cfg.Flush {
		out, err := shell(s.ctx, f).CombinedOutput()
		switch {
		case s.ctx.Err() != nil:
			_, _ = s.echo.Clear(context.WithoutCancel(s.ctx), client.Filter{Run: run})
			return s.fail(s.ctx.Err())
		case err != nil:
			fail("Flush", fmt.Sprintf("%q: %v %s", f, err, strings.TrimSpace(lastLine(string(out)))))
		default:
			pass("Flush", f)
		}
	}
	_, _ = s.echo.Clear(context.WithoutCancel(s.ctx), client.Filter{Run: run})
	return s.doctorEnd(ok, fails)
}

// productionReason says why site counts as a live site under the
// config's production: list, or "" when it doesn't; live is false when
// an entry only catches it by accident. It names the entry
// and says only what Echo does about it today: http actions refuse a URL
// that starts with an entry exactly as written, and command actions run
// all the same. Replays never read the list; they send only to staging:
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
			return fmt.Sprintf("%s is another host, but it starts with production: entry %s, so its http actions are "+
				"refused; end the entry with a / to match only that site", site, p), false
		}
		if prefix {
			return fmt.Sprintf("%s is listed under production: (%s). http actions to it are refused, but command actions "+
				"still run against it; point site: at a staging copy", site, p), true
		}
		if same {
			return fmt.Sprintf("%s looks like production: entry %s, but actions match an entry exactly as written, "+
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
		fmt.Fprintf(s.stdout, "\n  %d checks passed, %d failed.\n\n", ok, fails)
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
