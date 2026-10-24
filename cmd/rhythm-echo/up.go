package main

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"
	"sort"
	"strings"
	"time"

	"github.com/RhythmShahriar/echo/catcher"
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
	inbox := catcher.NewInbox(10000)
	srv := catcher.NewServer(inbox, catcher.Config{Mode: cfg.Mode, Allow: cfg.Allow, Token: token, Version: version})
	inbox.OnAdd(s.view.Message)

	mux := http.NewServeMux()
	mux.Handle("/v1/", srv)
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
	if cfg.SMTP != "" && cfg.SMTP != "off" {
		sl, err := net.Listen("tcp", cfg.SMTP)
		if err != nil {
			return s.fail(fmt.Errorf("SMTP on %s: %w", cfg.SMTP, err))
		}
		sink := catcher.NewSMTP(srv, cfg.SMTP)
		go func() { errs <- sink.Serve(sl) }()
		stopSMTP = func() { _ = sink.Close() }
		s.view.Step("SMTP", cfg.SMTP+"  (point a mailer or sendmail_path here)")
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
