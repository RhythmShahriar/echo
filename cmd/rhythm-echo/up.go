// Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
// SPDX-License-Identifier: MIT

package main

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/RhythmShahriar/echo/catcher"
	"github.com/RhythmShahriar/echo/check"
	"github.com/RhythmShahriar/echo/model"
	"github.com/RhythmShahriar/echo/report"
)

// cmdUp runs the catcher until interrupted: the API connectors post to
// (with a live web view at /), the SMTP sink and, when asked, the proxy.
func cmdUp(s *session) int {
	cfg := s.cfg
	token := s.echo.Token
	if !loopback(cfg.Listen) && token == "" {
		return s.fail(usageErr("--listen %s is reachable from other machines; set --token (or RHYTHM_ECHO_TOKEN) so only your connectors can post", cfg.Listen))
	}
	smtpOn := cfg.SMTP != "" && cfg.SMTP != "off"
	if smtpOn && !loopback(cfg.SMTP) && token == "" {
		return s.fail(usageErr("--smtp %s is reachable from other machines; set --token (or RHYTHM_ECHO_TOKEN): the sink then asks for it as the SMTP password", cfg.SMTP))
	}
	if cfg.Proxy != "" && cfg.Proxy != "off" && !loopback(cfg.Proxy) && token == "" {
		return s.fail(usageErr("--proxy %s is reachable from other machines; set --token (or RHYTHM_ECHO_TOKEN) so it isn't an open relay", cfg.Proxy))
	}
	inbox := catcher.NewInbox(10000)
	srv := catcher.NewServer(inbox, catcher.Config{Mode: cfg.Mode, Allow: cfg.Allow, Token: token, Version: version})
	inbox.OnAdd(newLiveLog(s.view).add)

	mux := http.NewServeMux()
	mux.Handle("/v1/", srv)
	mux.Handle("/api/v1/", srv) // Mailpit-compatible email API
	mux.HandleFunc("GET /{$}", func(w http.ResponseWriter, r *http.Request) { live(w, r, inbox, cfg.Mode) })
	api := &http.Server{Addr: cfg.Listen, Handler: mux, ReadHeaderTimeout: 10 * time.Second}
	ln, err := net.Listen("tcp", cfg.Listen)
	if err != nil {
		return s.fail(fmt.Errorf("listening on %s: %w (is Echo already running? see: rhythm echo doctor)", cfg.Listen, err))
	}
	errs := make(chan error, 3)
	go func() { errs <- api.Serve(ln) }()

	s.start("this machine")
	s.view.Step("Mode", modeLine(cfg.Mode))
	s.view.Step("API", "http://"+cfg.Listen+"  (connectors post here; open it for the live view)")
	stopSMTP := func() {}
	if smtpOn {
		sl, err := net.Listen("tcp", cfg.SMTP)
		if err != nil {
			return s.fail(fmt.Errorf("SMTP on %s: %w", cfg.SMTP, err))
		}
		sink := catcher.NewSMTP(srv, cfg.SMTP)
		line := cfg.SMTP + "  (point a mailer or sendmail_path here)"
		if !loopback(cfg.SMTP) {
			sink.Backend = tokenBackend{inner: sink.Backend, token: token}
			line = cfg.SMTP + "  (log in with any username and the token as the password)"
		}
		go func() { errs <- sink.Serve(sl) }()
		stopSMTP = func() { _ = sink.Close() }
		s.view.Step("SMTP", line)
	}
	stopProxy, err := startProxy(s, srv)
	if err != nil {
		return s.fail(err)
	}
	if len(cfg.Allow) > 0 {
		s.view.Step("Allowed", strings.Join(cfg.Allow, ", "))
	}
	if !s.o.quiet {
		fmt.Fprintln(s.stderr, "\n  Waiting for messages. Ctrl+C to stop.")
	}

	select {
	case <-s.ctx.Done():
	case err := <-errs:
		if err != nil && !errors.Is(err, http.ErrServerClosed) {
			return s.fail(err)
		}
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	_ = api.Shutdown(ctx)
	stopSMTP()
	stopProxy()
	if !s.o.quiet {
		fmt.Fprintf(s.stderr, "\nStopped. Captured %s this session.\n", report.Tally(liveRun(inbox, cfg.Mode)))
	}
	return exitOK
}

// liveLog prints each captured message once. The inbox calls back again
// when a message changes (an observed request gets its answer, a check
// blocks it); a new status is printed as a marked update and an update
// that changes nothing visible isn't printed at all.
type liveLog struct {
	view   *report.Pretty
	mu     sync.Mutex
	status map[string]model.Status
}

func newLiveLog(v *report.Pretty) *liveLog {
	return &liveLog{view: v, status: map[string]model.Status{}}
}

func (l *liveLog) add(m model.Message) {
	l.mu.Lock()
	prev, seen := l.status[m.ID]
	if len(l.status) > 20000 { // the inbox keeps 10000; forget the oldest half's worth at once
		clear(l.status)
	}
	l.status[m.ID] = m.Status
	l.mu.Unlock()
	switch {
	case !seen:
		l.view.Message(m)
	case prev != m.Status:
		what := check.Describe(m) // names the host for webhooks and requests
		if c := m.Channel; len(m.To) > 0 && (c == model.Email || c == model.SMS || c == model.WhatsApp) {
			what += " to " + strings.Join(m.To, ", ")
		}
		l.view.Step("Update", fmt.Sprintf("%s %s is now %s (was %s)",
			m.At.Local().Format("15:04:05"), what, m.Status, prev))
	}
}

func modeLine(mode string) string {
	if mode == "observe" {
		return "observe: messages are recorded and sent for real"
	}
	return "seal: nothing leaves; Echo answers for every service"
}

func loopback(addr string) bool {
	host, _, err := net.SplitHostPort(addr)
	if err != nil {
		return false
	}
	if host == "localhost" {
		return true
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}

// liveRun groups the inbox into actions (by run and action, "Live" for
// messages outside any action) so the HTML report can show it.
func liveRun(in *catcher.Inbox, mode string) *model.Run {
	r := &model.Run{Schema: model.Schema, Tool: "rhythm-echo", Version: version, Mode: mode, Started: time.Now()}
	at := map[string]int{}
	for _, m := range in.List(catcher.Filter{}) {
		key, label := "live", "Live (not tied to an action)"
		if m.Action != "" {
			key, label = m.Run+"/"+m.Action, m.Action
		}
		i, ok := at[key]
		if !ok {
			i = len(r.Actions)
			at[key] = i
			r.Actions = append(r.Actions, model.Action{ID: key, Label: label, Kind: "live"})
		}
		r.Actions[i].Messages = append(r.Actions[i].Messages, m)
		if m.At.Before(r.Started) {
			r.Started = m.At
		}
	}
	sort.SliceStable(r.Actions, func(a, b int) bool { return r.Actions[a].ID == "live" && r.Actions[b].ID != "live" })
	return r
}

// live serves the web view: the HTML report of what's been captured,
// refreshed every few seconds.
func live(w http.ResponseWriter, _ *http.Request, in *catcher.Inbox, mode string) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Refresh", "5")
	w.Header().Set("Content-Security-Policy", "default-src 'none'; style-src 'unsafe-inline'; script-src 'unsafe-inline'; img-src * data:; frame-src 'self' data:")
	_ = report.HTML(w, report.Result{Run: liveRun(in, mode)})
}
