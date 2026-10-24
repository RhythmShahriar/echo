// Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
// SPDX-License-Identifier: MIT

package main

import (
	"context"
	"crypto/subtle"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"slices"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/RhythmShahriar/echo/catcher"
	"github.com/RhythmShahriar/echo/check"
	"github.com/RhythmShahriar/echo/config"
	"github.com/RhythmShahriar/echo/model"
	"github.com/RhythmShahriar/echo/proxy"
	"github.com/RhythmShahriar/echo/report"
	"github.com/RhythmShahriar/echo/web"
	"github.com/RhythmShahriar/echo/wire"
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
	proxyOn := cfg.Proxy != "" && cfg.Proxy != "off"
	if proxyOn && !loopback(cfg.Proxy) && token == "" {
		return s.fail(usageErr("--proxy %s is reachable from other machines; set --token (or RHYTHM_ECHO_TOKEN) so it isn't an open relay", cfg.Proxy))
	}

	// Every port is taken before anything is printed, so a port in use
	// fails with only its error. Whatever was started is stopped again
	// (in reverse) on every way out.
	var stack cleanups
	defer stack.run()
	smtpAddr := ""
	if smtpOn {
		smtpAddr = cfg.SMTP
	}
	ln, sl, err := listenPair(cfg.Listen, smtpAddr)
	if err != nil {
		// Mailpit or another catcher may hold the ports: offer a free
		// pair. --free-ports (cli lane, X18) will pass true here.
		if ln, sl, err = moveAside(s, &cfg.Listen, &smtpAddr, err, false); err != nil {
			return s.fail(err)
		}
		if smtpOn {
			cfg.SMTP = smtpAddr
		}
	}
	stack.push(func(context.Context) { _ = ln.Close() })
	if sl != nil {
		stack.push(func(context.Context) { _ = sl.Close() })
	}
	var pl net.Listener
	if proxyOn {
		if pl, err = net.Listen("tcp", cfg.Proxy); err != nil {
			return s.fail(fmt.Errorf("proxy on %s: %w", cfg.Proxy, err))
		}
		stack.push(func(context.Context) { _ = pl.Close() })
	}

	inbox := catcher.NewInbox(10000)
	srv := catcher.NewServer(inbox, catcher.Config{Mode: cfg.Mode, Allow: cfg.Allow, Token: token, Version: version})
	checks := liveChecks(cfg)
	log := newLiveLog(s.view)
	log.checks = checks
	ui := web.New(web.Options{Inbox: inbox, Mode: cfg.Mode, Checks: checks, AnyHost: token != ""})
	log.issues = ui.Issues // checked once, for the log and the page
	inbox.OnAdd(func(m model.Message) {
		ui.Notify(m)
		log.add(m)
	})
	errs := make(chan error, 3)
	api := &http.Server{Addr: cfg.Listen, Handler: upHandler(upRoutes(srv, inbox, ui, cfg.Mode, checks), token), ReadHeaderTimeout: 10 * time.Second}
	go func() { errs <- api.Serve(ln) }()
	stack.push(func(ctx context.Context) { _ = api.Shutdown(ctx) })
	stack.push(func(context.Context) { ui.Close() }) // runs first: Shutdown waits for open event streams
	smtpLine := ""
	if sl != nil {
		sink := catcher.NewSMTP(srv, cfg.SMTP)
		smtpLine = cfg.SMTP + "  (point a mailer or sendmail_path here)"
		if !loopback(cfg.SMTP) {
			sink.Backend = tokenBackend{inner: sink.Backend, token: token}
			smtpLine = cfg.SMTP + "  (log in with any username and the token as the password)"
		}
		go func() { errs <- sink.Serve(sl) }()
		stack.push(func(context.Context) { _ = sink.Close() })
	}
	proxyLine := ""
	if pl != nil {
		line, stop, err := startProxy(s, srv, pl)
		if err != nil {
			return s.fail(err)
		}
		proxyLine = line
		stack.push(stop)
	}

	s.start("this machine")
	s.view.Step("Mode", modeLine(cfg.Mode))
	apiLine := "http://" + cfg.Listen + "  (connectors post here; open it for the live view)"
	if token != "" {
		apiLine = "http://" + cfg.Listen + "  (connectors post here; open /?token=<your token> once for the live view)"
	}
	s.view.Step("API", apiLine)
	if smtpLine != "" {
		s.view.Step("SMTP", smtpLine)
	}
	if proxyLine != "" {
		s.view.Step("Proxy", proxyLine)
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
	stack.run()
	if !s.o.quiet {
		fmt.Fprintf(s.stderr, "\nStopped. Captured %s this session.\n", report.Tally(liveRun(inbox, cfg.Mode, checks)))
	}
	return exitOK
}

// cleanups is a stack of stop functions, run last first.
type cleanups []func(context.Context)

func (c *cleanups) push(f func(context.Context)) { *c = append(*c, f) }

// run stops everything pushed so far, giving graceful shutdowns 3 seconds
// in all. It's safe to call again: the stack is empty after.
func (c *cleanups) run() {
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	for i := len(*c) - 1; i >= 0; i-- {
		(*c)[i](ctx)
	}
	*c = nil
}

// upRoute is one route of up's API listener. upHandler puts every route
// behind requireToken. up_test probes each one without the token, so a
// route added here can't be left open.
type upRoute struct {
	pattern string
	probe   string // a GET path that reaches the route, for the test
	handler http.Handler
}

// upRoutes lists everything the API listener serves: the catcher's API,
// Echo Inbox at / and the session as an HTML report at /report.
func upRoutes(srv *catcher.Server, inbox *catcher.Inbox, ui *web.UI, mode string, checks check.Options) []upRoute {
	return []upRoute{
		{"/v1/", "/v1/messages", srv},
		{"/api/v1/", "/api/v1/messages", srv}, // Mailpit-compatible email API
		{"GET /{$}", "/", ui},
		{"GET /assets/", "/assets/app.js", ui},
		{"/ui/", web.PathMessages, ui},
		{"GET /report", "/report", http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { liveReport(w, r, inbox, mode, checks) })},
	}
}

// upHandler serves routes, all behind the token when one is set.
func upHandler(routes []upRoute, token string) http.Handler {
	mux := http.NewServeMux()
	for _, r := range routes {
		mux.Handle(r.pattern, r.handler)
	}
	return requireToken(token, mux)
}

// tokenCookie holds the token for a browser that opened the live view
// with ?token= once.
const tokenCookie = "echo_token"

// requireToken guards a handler when token is set. Connectors and scripts
// send X-Echo-Token. A browser can't, so it opens /?token=... once: Echo
// sets an HttpOnly, same-site cookie and redirects to the URL without the
// token, so the token doesn't stay in the address bar or the history.
func requireToken(token string, next http.Handler) http.Handler {
	if token == "" {
		return next
	}
	same := func(got string) bool { return subtle.ConstantTimeCompare([]byte(got), []byte(token)) == 1 }
	deny := func(w http.ResponseWriter) {
		w.Header().Set("Cache-Control", "no-store")
		http.Error(w, "missing or wrong "+wire.TokenHeader+" (in a browser, open /?token=<your token> once)", http.StatusUnauthorized)
	}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if q := r.URL.Query(); q.Has("token") && (r.Method == http.MethodGet || r.Method == http.MethodHead) {
			if !same(q.Get("token")) {
				deny(w)
				return
			}
			// Secure only under TLS: up serves plain HTTP on loopback, where
			// a Secure cookie would never be sent back.
			http.SetCookie(w, &http.Cookie{Name: tokenCookie, Value: token, Path: "/", HttpOnly: true, //nolint:gosec // Secure is set under TLS, see above
				SameSite: http.SameSiteStrictMode, Secure: r.TLS != nil})
			q.Del("token")
			back := url.URL{Path: r.URL.Path, RawQuery: q.Encode()}
			if !strings.HasPrefix(back.Path, "/") || strings.HasPrefix(back.Path, "//") || strings.HasPrefix(back.Path, "/\\") {
				back.Path = "/" // "//host" would redirect to another site
			}
			w.Header().Set("Cache-Control", "no-store")
			http.Redirect(w, r, back.String(), http.StatusSeeOther)
			return
		}
		if same(r.Header.Get(wire.TokenHeader)) {
			next.ServeHTTP(w, r)
			return
		}
		if c, err := r.Cookie(tokenCookie); err == nil && same(c.Value) {
			// The catcher checks the header itself: the cookie stands in
			// for it, so the browser can open /v1/messages too.
			r = r.Clone(r.Context())
			r.Header.Set(wire.TokenHeader, token)
			next.ServeHTTP(w, r)
			return
		}
		deny(w)
	})
}

// liveLog prints each captured message once, with the problems the
// content checks find in it. The inbox calls back again when a message
// changes (an observed request gets its answer, a check blocks it). A new
// status is printed as a marked update. An update that changes nothing
// visible isn't printed at all.
type liveLog struct {
	view   *report.Pretty
	checks check.Options
	issues func(model.Message) []model.Issue // when set, used instead of running the checks
	mu     sync.Mutex                        // guards seen and keeps a message's lines together
	seen   map[string]logged
}

// logged is what the log last printed for a message.
type logged struct {
	status model.Status
	marks  string
}

// newLiveLog prints to v; set checks for what content checks expect.
func newLiveLog(v *report.Pretty) *liveLog {
	return &liveLog{view: v, seen: map[string]logged{}}
}

func (l *liveLog) add(m model.Message) {
	var issues []model.Issue
	if l.issues != nil {
		issues = l.issues(m)
	} else {
		issues = check.Message(m, l.checks)
	}
	marks := markers(issues)
	l.mu.Lock()
	defer l.mu.Unlock()
	prev, seen := l.seen[m.ID]
	if len(l.seen) > 20000 { // the inbox keeps 10000; forget the oldest half's worth at once
		clear(l.seen)
	}
	l.seen[m.ID] = logged{m.Status, marks}
	switch {
	case !seen:
		l.view.Message(m)
	case prev.status != m.Status:
		what := check.Describe(m) // names the host for webhooks and requests
		if c := m.Channel; len(m.To) > 0 && (c == model.Email || c == model.SMS || c == model.WhatsApp) {
			what += " to " + strings.Join(m.To, ", ")
		}
		l.view.Step("Update", fmt.Sprintf("%s %s is now %s (was %s)",
			m.At.Local().Format("15:04:05"), what, m.Status, prev.status))
	case prev.marks == marks:
		return
	}
	if marks != "" && marks != prev.marks {
		l.view.Step("Issues", marks)
	}
}

// markers names a message's errors and warnings in a few words for the
// live log: "unfilled {first_name}, dev link localhost". Notices are left
// out, as is a failed send (the status already shows it).
func markers(issues []model.Issue) string {
	var out []string
	for _, i := range issues {
		if i.Level == model.Notice || i.Check == "send/failed" {
			continue
		}
		mark := i.Title
		switch i.Check {
		case "content/placeholder": // "Unfilled placeholder {x} in email"
			tag, _, _ := strings.Cut(strings.TrimPrefix(i.Title, "Unfilled placeholder "), " in ")
			mark = "unfilled " + tag
		case "content/dev-link": // "Email links to a development address (localhost)"
			if at := strings.LastIndex(i.Title, "("); at >= 0 {
				mark = "dev link " + strings.TrimSuffix(i.Title[at+1:], ")")
			}
		case "content/php-error":
			mark = "PHP error"
		case "content/encoding":
			mark = "garbled text"
		case "content/secret":
			mark = "credential in body"
		case "email/no-subject":
			mark = "no subject"
		case "email/empty":
			mark = "empty"
		case "email/clipped":
			mark = "clipped by Gmail"
		case "seal/real-recipient":
			mark = "real recipient"
		}
		if !slices.Contains(out, mark) {
			out = append(out, mark)
		}
	}
	return strings.Join(out, ", ")
}

// liveChecks is what live messages are checked against: links to the
// configured site are fine; allow_recipients may really be messaged.
func liveChecks(cfg config.Config) check.Options {
	o := check.Options{AllowRecipients: cfg.AllowRecipients}
	if u, err := url.Parse(cfg.Site); err == nil {
		o.SiteHost = u.Hostname()
	}
	return o
}

func modeLine(mode string) string {
	if mode == "observe" {
		return "observe: messages are recorded and sent for real"
	}
	return "seal: nothing leaves; Echo answers for every service"
}

// loopback reports whether a listen address stays on this machine.
func loopback(addr string) bool { return proxy.IsLoopbackAddr(addr) }

// liveRun groups the inbox into actions (by run and action, "Live" for
// messages outside any action) with each message's content checks, so the
// HTML report can show it.
func liveRun(in *catcher.Inbox, mode string, checks check.Options) *model.Run {
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
		// Checked here until the catcher keeps each message's issues.
		r.Actions[i].Issues = append(r.Actions[i].Issues, check.Message(m, checks)...)
		if m.At.Before(r.Started) {
			r.Started = m.At
		}
	}
	sort.SliceStable(r.Actions, func(a, b int) bool { return r.Actions[a].ID == "live" && r.Actions[b].ID != "live" })
	return r
}

// liveReport serves the session so far as the HTML report a run writes,
// for saving or sharing; Echo Inbox at / is the live view.
func liveReport(w http.ResponseWriter, _ *http.Request, in *catcher.Inbox, mode string, checks check.Options) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Content-Security-Policy", "default-src 'none'; style-src 'unsafe-inline'; script-src 'unsafe-inline'; img-src * data:; frame-src 'self' data:")
	_ = report.HTML(w, report.Result{Run: liveRun(in, mode, checks)})
}
