package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/smtp"
	"os"
	"os/exec"
	"strings"
	"time"

	"github.com/RhythmShahriar/echo/client"
	"github.com/RhythmShahriar/echo/connectors/wordpress"
	"github.com/RhythmShahriar/echo/wire"
)

// cmdDoctor checks every capture layer end to end: Echo is up, the site's
// connector is active, and canary messages sent through each layer arrive.
// It never sends anything real: canaries go to .invalid addresses.
func cmdDoctor(s *session) int {
	ok, fails := 0, 0
	pass := func(label, detail string) { ok++; s.view.Step("✓ "+label, detail) }
	fail := func(label, detail string) { fails++; s.view.Step("✗ "+label, detail) }
	note := func(label, detail string) { s.view.Step("· "+label, detail) }
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
		if err := smtp.SendMail(s.cfg.SMTP, nil, "doctor@echo.invalid", []string{"canary@echo.invalid"}, []byte(msg)); err != nil {
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

	// WordPress connector.
	site := strings.TrimRight(s.cfg.Site, "/")
	if site != "" {
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
		if s.cfg.IsProduction(site) {
			fail("Site", site+" is listed under production: Echo's actions and replays refuse it")
		}
	} else {
		note("WordPress", "no site: in .echo/config.yaml, so the site connector wasn't checked")
	}

	// Laravel connector, when run from a Laravel project.
	if _, err := os.Stat("artisan"); err == nil {
		cmd := exec.CommandContext(s.ctx, "php", "artisan", "echo:canary", "--json")
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
		cmd := exec.CommandContext(s.ctx, "sh", "-c", f) //nolint:gosec // the user's configured command
		if out, err := cmd.CombinedOutput(); err != nil {
			fail("Flush", fmt.Sprintf("%q: %v %s", f, err, strings.TrimSpace(lastLine(string(out)))))
		} else {
			pass("Flush", f)
		}
	}
	_, _ = s.echo.Clear(context.WithoutCancel(s.ctx), client.Filter{Run: run})
	return s.doctorEnd(ok, fails)
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
