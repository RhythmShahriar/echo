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
	"os"
	"regexp"
	"slices"
	"sort"
	"strconv"
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
	smtpOn := cfg.SMTP != "" && cfg.SMTP != "off"
	proxyOn := cfg.Proxy != "" && cfg.Proxy != "off"
	private := privateNetworkOn()
	var open []string // addresses served beyond loopback without a token
	for _, l := range []struct {
		on         bool
		flag, addr string
		why        string
	}{
		{true, "--listen", cfg.Listen, "so only your connectors can post"},
		{smtpOn, "--smtp", cfg.SMTP, "the sink then asks for it as the SMTP password"},
		{proxyOn, "--proxy", cfg.Proxy, "so it isn't an open relay"},
	} {
		if !l.on || loopback(l.addr) || token != "" {
			continue
		}
		if !private {
			sep := " "
			if l.flag == "--smtp" {
				sep = ": "
			}
			return s.fail(usageErr("%s %s is reachable from other machines; set --token (or RHYTHM_ECHO_TOKEN)%s%s", l.flag, l.addr, sep, l.why))
		}
		ifaces, err := listIfaces()
		if err != nil {
			return s.fail(fmt.Errorf("--private-network: listing network interfaces: %w", err))
		}
		if err := privateOnly(l.flag, l.addr, ifaces, lookupIPs); err != nil {
			return s.fail(usageErr("%v", err))
		}
		open = append(open, l.addr)
	}
	login, err := loginFromEnv()
	if err != nil {
		return s.fail(err)
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
	// A login guards the Inbox with a session cookie, which a rebinding
	// page can't send either, so the Host check isn't needed then.
	ui := web.New(web.Options{Inbox: inbox, Mode: cfg.Mode, Checks: checks, AnyHost: token != "" || login != nil})
	log.issues = ui.Issues // checked once, for the log and the page
	inbox.OnAdd(func(m model.Message) {
		ui.Notify(m)
		log.add(m)
	})
	errs := make(chan error, 3)
	api := &http.Server{Addr: cfg.Listen, Handler: upHandler(upRoutes(srv, inbox, ui, cfg.Mode, checks), token, login), ReadHeaderTimeout: 10 * time.Second}
	go func() { errs <- api.Serve(ln) }()
	stack.push(func(ctx context.Context) { _ = api.Shutdown(ctx) })
	stack.push(func(context.Context) { ui.Close() }) // runs first: Shutdown waits for open event streams
	smtpLine := ""
	if sl != nil {
		sink := catcher.NewSMTP(srv, cfg.SMTP)
		smtpLine = cfg.SMTP + "  (point a mailer or sendmail_path here)"
		if !loopback(cfg.SMTP) && token != "" {
			sink.Backend = tokenBackend{inner: sink.Backend, token: token}
			smtpLine = cfg.SMTP + "  (log in with any username and the token as the password)"
		}
		go func() { errs <- sink.Serve(sl) }()
		stack.push(func(context.Context) { _ = sink.Close() })
	}
	proxyLine := ""
	var notes []caNote
	if pl != nil {
		line, ns, stop, err := startProxy(s, srv, pl)
		if err != nil {
			return s.fail(err)
		}
		proxyLine, notes = line, ns
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
	for _, n := range notes {
		s.view.Step(n.label, n.text)
	}
	if login != nil {
		s.view.Step("Login", "viewers sign in as "+login.User()+" at http://"+cfg.Listen+web.PathLogin+" (read only, for 8 hours)")
	}
	if len(cfg.Allow) > 0 {
		s.view.Step("Allowed", strings.Join(cfg.Allow, ", "))
	}
	if len(open) > 0 {
		s.view.Step("Warning", "no token on "+strings.Join(open, ", ")+": anything on this private network can post to Echo and read what it caught (--private-network)")
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
	browser bool // part of Echo Inbox: a --login session may read it
}

// upRoutes lists everything the API listener serves: the catcher's API,
// Echo Inbox at / and the session as an HTML report at /report.
func upRoutes(srv *catcher.Server, inbox *catcher.Inbox, ui *web.UI, mode string, checks check.Options) []upRoute {
	return []upRoute{
		{"/v1/", "/v1/messages", srv, false},
		{"/api/v1/", "/api/v1/messages", srv, false}, // Mailpit-compatible email API
		{"GET /{$}", "/", ui, true},
		{"GET /assets/", "/assets/app.js", ui, true},
		{"/ui/", web.PathMessages, ui, true},
		{"GET /report", "/report", http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { liveReport(w, r, inbox, mode, checks) }), true},
	}
}

// upHandler serves routes, all behind the token when one is set. With a
// login, Echo Inbox's routes are also open to a signed-in viewer, read
// only (web.Login.Guard). Without a token they need the login then, while
// the API stays as open as before: connectors on this machine post to it.
func upHandler(routes []upRoute, token string, login *web.Login) http.Handler {
	mux := http.NewServeMux()
	viewer := http.NewServeMux()
	for _, r := range routes {
		mux.Handle(r.pattern, r.handler)
		if r.browser {
			viewer.Handle(r.pattern, r.handler)
		}
	}
	if login == nil {
		return requireToken(token, mux, nil)
	}
	viewer.Handle(web.PathLogin, http.NotFoundHandler()) // Guard answers these itself
	viewer.Handle(web.PathLogout, http.NotFoundHandler())
	guarded := login.Guard(viewer)
	viewing := func(r *http.Request) bool { _, p := viewer.Handler(r); return p != "" }
	if token == "" {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if viewing(r) {
				guarded.ServeHTTP(w, r)
				return
			}
			mux.ServeHTTP(w, r)
		})
	}
	return requireToken(token, mux, func(w http.ResponseWriter, r *http.Request) bool {
		if !viewing(r) {
			return false
		}
		guarded.ServeHTTP(w, r)
		return true
	})
}

// loginEnv sets up a viewer login (user:password). The --login flag
// belongs to the cli lane's flag set; until it lands, the environment is
// how to ask for one.
const loginEnv = "RHYTHM_ECHO_LOGIN"

// loginFromEnv returns the login RHYTHM_ECHO_LOGIN asks for, or nil.
func loginFromEnv() (*web.Login, error) {
	spec := os.Getenv(loginEnv)
	if spec == "" {
		return nil, nil
	}
	l, err := web.ParseLogin(spec)
	if err != nil {
		return nil, usageErr("%s: %v", loginEnv, err)
	}
	return l, nil
}

// tokenCookie holds the token for a browser that opened the live view
// with ?token= once.
const tokenCookie = "echo_token"

// requireToken guards a handler when token is set. Connectors and scripts
// send X-Echo-Token. A browser can't, so it opens /?token=... once: Echo
// sets an HttpOnly, same-site cookie and redirects to the URL without the
// token, so the token doesn't stay in the address bar or the history. A
// request without the token goes to otherwise when that is set and says
// it served it (a login's viewer); it is refused otherwise.
func requireToken(token string, next http.Handler, otherwise func(http.ResponseWriter, *http.Request) bool) http.Handler {
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
		if otherwise != nil && otherwise(w, r) {
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

// privateNetworkEnv turns on --private-network. The flag itself belongs
// to the cli lane's flag set; until it lands, the environment is how the
// Docker image asks for it.
const privateNetworkEnv = "RHYTHM_ECHO_PRIVATE_NETWORK"

// privateNetworkOn reports whether up may listen beyond loopback without
// a token, on private and container networks only.
func privateNetworkOn() bool {
	on, _ := strconv.ParseBool(os.Getenv(privateNetworkEnv))
	return on
}

// netIface is a network interface that is up, with its addresses.
type netIface struct {
	name string
	ips  []net.IP
}

// listIfaces lists this machine's interfaces; tests replace it.
var listIfaces = func() ([]netIface, error) {
	ifs, err := net.Interfaces()
	if err != nil {
		return nil, err
	}
	var out []netIface
	for _, i := range ifs {
		if i.Flags&net.FlagUp == 0 {
			continue
		}
		addrs, err := i.Addrs()
		if err != nil {
			return nil, err
		}
		n := netIface{name: i.Name}
		for _, a := range addrs {
			if ipn, ok := a.(*net.IPNet); ok {
				n.ips = append(n.ips, ipn.IP)
			}
		}
		out = append(out, n)
	}
	return out, nil
}

func lookupIPs(host string) ([]net.IP, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	return net.DefaultResolver.LookupIP(ctx, "ip", host)
}

// containerIface matches the bridges and virtual links container runtimes
// create (Docker, Podman, Kubernetes CNIs). Their addresses are usually
// private anyway; a runtime can give them any range, though.
var containerIface = regexp.MustCompile(`^(docker\d*|br-[0-9a-f]+|veth.+|cni\d*|podman\d*|cali.+|flannel.+|cilium.+)$`)

// privateOnly returns nil when addr can be reached only from loopback,
// private networks (RFC 1918, fc00::/7) or container interfaces. A
// wildcard address listens on every interface, so each must qualify;
// link-local addresses are left out, as they can't be routed.
func privateOnly(flag, addr string, ifaces []netIface, lookup func(string) ([]net.IP, error)) error {
	host, _, err := net.SplitHostPort(addr)
	if err != nil {
		return fmt.Errorf("%s %s: %w", flag, addr, err)
	}
	refuse := func(ip net.IP, iface string) error {
		where := ip.String()
		if iface != "" {
			where += " on " + iface
		}
		return fmt.Errorf("%s %s is reachable from a public address (%s); --private-network allows only private (RFC 1918) and container networks: listen on a private address, or set --token (or RHYTHM_ECHO_TOKEN)", flag, addr, where)
	}
	ok := func(ip net.IP, iface string) bool {
		return ip.IsLoopback() || ip.IsPrivate() || containerIface.MatchString(iface)
	}
	ip := net.ParseIP(host)
	if host == "" || ip != nil && ip.IsUnspecified() {
		for _, i := range ifaces {
			for _, a := range i.ips {
				if !a.IsLinkLocalUnicast() && !ok(a, i.name) {
					return refuse(a, i.name)
				}
			}
		}
		return nil
	}
	ips := []net.IP{ip}
	if ip == nil {
		if ips, err = lookup(host); err != nil {
			return fmt.Errorf("%s %s: %w", flag, addr, err)
		}
	}
	for _, a := range ips {
		if !ok(a, ifaceOf(a, ifaces)) {
			return refuse(a, ifaceOf(a, ifaces))
		}
	}
	return nil
}

// ifaceOf names the interface that holds ip, or "".
func ifaceOf(ip net.IP, ifaces []netIface) string {
	for _, i := range ifaces {
		for _, a := range i.ips {
			if a.Equal(ip) {
				return i.name
			}
		}
	}
	return ""
}

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
