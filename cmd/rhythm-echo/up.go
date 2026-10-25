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
	"github.com/RhythmShahriar/echo/hub"
	"github.com/RhythmShahriar/echo/model"
	"github.com/RhythmShahriar/echo/proxy"
	"github.com/RhythmShahriar/echo/report"
	"github.com/RhythmShahriar/echo/web"
	"github.com/RhythmShahriar/echo/wire"
)

// cmdUp runs the catcher until interrupted: the API connectors post to
// (with a live web view at /), the SMTP sink and, when asked, the proxy.
func cmdUp(s *session) int {
	if len(s.args) > 0 {
		return s.fail(usageErr("up takes no arguments; rhythm echo up --help lists its flags"))
	}
	cfg := s.cfg
	sites, err := loadSites(s)
	if err != nil {
		return s.fail(err)
	}
	token := s.echo.Token
	if sites != nil {
		token = sites.read
	}
	smtpOn := cfg.SMTP != "" && cfg.SMTP != "off"
	proxyOn := cfg.Proxy != "" && cfg.Proxy != "off"
	private := s.o.privateNetwork || privateNetworkOn()
	// Every listener beyond loopback needs the token. Without it anyone on
	// the network could post captures into a baseline, read the inbox (and
	// the login codes in it) or use the proxy as an open relay.
	// --private-network relaxes that only for an address no public
	// interface can reach (privateOnly).
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
	if err := shortToken(token, sites, []string{cfg.Listen, cfg.SMTP, cfg.Proxy}); err != nil {
		return s.fail(err)
	}
	names, err := upHostnames(s.o.hostnames)
	if err != nil {
		return s.fail(err)
	}
	login, loginFrom, err := upLogin(s.o.login)
	if err != nil {
		return s.fail(err)
	}
	if err := loginNeedsToken(login, loginFrom, token, cfg.Listen); err != nil {
		return s.fail(err)
	}
	store, err := inboxSettings(s)
	if err != nil {
		return s.fail(err)
	}
	spamd := s.o.spamassassin
	if spamd != "" && !hostPort(spamAddr(spamd)) {
		return s.fail(usageErr("--spamassassin %s: want host:port, like 127.0.0.1:783", spamd))
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
		// pair, or take one without asking with --free-ports.
		if ln, sl, err = moveAside(s, &cfg.Listen, &smtpAddr, err, s.o.freePorts); err != nil {
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

	began := time.Now()
	inbox, err := openInbox(store)
	if err != nil {
		return s.fail(err)
	}
	stack.push(func(context.Context) {
		if err := inbox.Close(); err != nil {
			fmt.Fprintln(s.stderr, "rhythm echo: keeping the inbox:", err)
		}
	})
	checks := s.checks
	ccfg := catcher.Config{Mode: cfg.Mode, Allow: cfg.Allow, Token: token, Version: version, Listen: cfg.Listen,
		Site: cfg.Site, AllowRecipients: cfg.AllowRecipients, Rules: checks.Rules, Checks: checks.Checks,
		SMTPAddr: "off", ProxyAddr: "off", Hosts: names, PrivateNetwork: private}
	if sl != nil {
		ccfg.SMTPAddr = sl.Addr().String()
	}
	if pl != nil {
		ccfg.ProxyAddr = pl.Addr().String()
	}
	if sites != nil {
		ccfg.WriteTokens = sites.write
	}
	srv := catcher.NewServer(inbox, ccfg)
	log := newLiveLog(s.view)
	log.checks = checks
	// A login guards the Inbox with a session cookie, which a rebinding
	// page can't send either, so the Host check isn't needed then.
	// Without either, --hostname and --private-network widen the Host
	// check the same way for the Inbox as for the API.
	ui := web.New(web.Options{Inbox: inbox, Mode: cfg.Mode, Checks: checks, AnyHost: token != "" || login != nil,
		Hosts: names, PrivateNetwork: private, Relay: relayAllow(cfg)})
	log.issues = ui.Issues // checked once, for the log and the page
	scorer := newSpamScorer(spamd, inbox)
	inbox.OnAdd(func(m model.Message) {
		ui.Notify(m)
		log.add(m)
		scorer.score(m)
	})
	stack.push(func(context.Context) { scorer.wait() })
	routes := upRoutes(srv, inbox, ui, cfg.Mode, checks)
	relayLine := ""
	if rel, line, err := releaseRoute(s, inbox, ui, token, login, names); err != nil {
		return s.fail(err)
	} else if rel != nil {
		routes, relayLine = append(routes, *rel), line
	}
	handler := upHandler(routes, token, login)
	if sites != nil {
		handler = writeTokens(sites.tokens(), srv, handler)
	}
	errs := make(chan error, 3)
	api := &http.Server{Addr: cfg.Listen, Handler: handler, ReadHeaderTimeout: 10 * time.Second}
	go func() { errs <- api.Serve(ln) }()
	stack.push(func(ctx context.Context) { _ = api.Shutdown(ctx) })
	stack.push(func(context.Context) { ui.Close() }) // runs first: Shutdown waits for open event streams
	smtpLine := ""
	if sl != nil {
		sink := catcher.NewSMTP(srv, cfg.SMTP)
		smtpLine = cfg.SMTP + "  (point a mailer or sendmail_path here)"
		if !loopback(cfg.SMTP) && token != "" {
			passwords, siteOf := []string{token}, map[string]string(nil)
			if sites != nil {
				passwords, siteOf = sites.tokens(), sites.write
			}
			sink.Backend = tokenBackend{inner: sink.Backend, tokens: passwords, sites: siteOf}
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
	if store.dir != "" {
		s.view.Step("Inbox", inboxLine(store, len(inbox.List(catcher.Filter{}))))
	}
	if sites != nil {
		s.view.Step("Sites", sites.line())
	}
	if login != nil {
		s.view.Step("Login", loginLine(login, token, cfg.Listen))
	}
	if relayLine != "" {
		s.view.Step("Relay", relayLine)
	}
	if spamd != "" {
		s.view.Step("Spam", "each email is scored by SpamAssassin at "+spamAddr(spamd))
	}
	if len(cfg.Allow) > 0 {
		s.view.Step("Allowed", strings.Join(cfg.Allow, ", "))
	}
	for _, w := range openWarnings(open, cfg) {
		s.view.Step("Warning", w)
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
	// Only what this session caught: a kept inbox (--store) also holds
	// what earlier sessions did.
	tally := report.Tally(liveRun(inbox, cfg.Mode, checks, catcher.Filter{Since: began}))
	stack.run()
	if !s.o.quiet {
		fmt.Fprintf(s.stderr, "\nStopped. Captured %s this session.\n", tally)
	}
	return exitOK
}

// openWarnings are the banner's warnings for the addresses up serves
// without a token (--private-network): anything on the network can post
// and read. An observing proxy there forwards for anyone.
func openWarnings(open []string, cfg config.Config) []string {
	if len(open) == 0 {
		return nil
	}
	out := []string{"no token on " + strings.Join(open, ", ") + ": anything on this private network can post to Echo and read what it caught (--private-network)"}
	if cfg.Mode == "observe" && slices.Contains(open, cfg.Proxy) {
		out = append(out, "observing, the proxy on "+cfg.Proxy+" forwards for anything on this private network to any host it reaches; seal Echo or set a token")
	}
	return out
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
	session bool // a --login session may use it; the handler checks the session itself (the relay)
}

// upRoutes lists everything the API listener serves: the catcher's API,
// Echo Inbox at / and the session as an HTML report at /report.
func upRoutes(srv *catcher.Server, inbox *catcher.Inbox, ui *web.UI, mode string, checks check.Options) []upRoute {
	return []upRoute{
		{"/v1/", "/v1/messages", srv, false, false},
		{"/api/v1/", "/api/v1/messages", srv, false, false}, // Mailpit-compatible email API
		{"GET /{$}", "/", ui, true, false},
		{"GET /assets/", "/assets/app.js", ui, true, false},
		{"/ui/", web.PathMessages, ui, true, false},
		// Through the UI's Host check: the report holds the whole session.
		{"GET /report", "/report", ui.Protect(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { liveReport(w, r, inbox, mode, checks) })), true, false},
	}
}

// upHandler serves routes, all behind the token when one is set. With a
// login, Echo Inbox's routes are also open to a signed-in viewer, read
// only (web.Login.Guard) and a session route (the relay's release) is
// handed the request to check the sign-in itself. Without a token they need the login then, while
// the API stays as open as before: connectors on this machine post to it.
// That is why cmdUp allows a login without a token on loopback only
// (loginNeedsToken).
func upHandler(routes []upRoute, token string, login *web.Login) http.Handler {
	mux := http.NewServeMux()
	viewer := http.NewServeMux()
	signed := http.NewServeMux()
	for _, r := range routes {
		mux.Handle(r.pattern, r.handler)
		if r.browser {
			viewer.Handle(r.pattern, r.handler)
		}
		if r.session {
			signed.Handle(r.pattern, r.handler)
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
		if _, p := signed.Handler(r); p != "" {
			signed.ServeHTTP(w, r)
			return true
		}
		if !viewing(r) {
			return false
		}
		guarded.ServeHTTP(w, r)
		return true
	})
}

// loginEnv sets up a viewer login (user:password), as --login does. The
// variable keeps the password out of ps.
const loginEnv = "RHYTHM_ECHO_LOGIN"

// upLogin returns the login --login (or else RHYTHM_ECHO_LOGIN) asks for,
// or nil, with where it came from for messages.
func upLogin(flag string) (*web.Login, string, error) {
	from, spec := "--login", flag
	if spec == "" {
		from, spec = loginEnv, os.Getenv(loginEnv)
	}
	if spec == "" {
		return nil, "", nil
	}
	l, err := web.ParseLogin(spec)
	if err != nil {
		return nil, from, usageErr("%s: %v", from, err)
	}
	return l, from, nil
}

// shortToken refuses a token shorter than hub.MinToken (the --sites
// file's tokens too) when one of addrs, the API, SMTP sink and proxy,
// is reachable from other machines. "" and "off" are addresses not
// served.
func shortToken(token string, sites *upSites, addrs []string) error {
	var short []string
	switch {
	case sites != nil:
		short = sites.short
	case token != "" && len(token) < hub.MinToken:
		short = []string{"--token (or RHYTHM_ECHO_TOKEN)"}
	}
	if len(short) == 0 {
		return nil
	}
	for _, addr := range addrs {
		if addr != "" && addr != "off" && !loopback(addr) {
			return usageErr("%v", hub.ShortTokenError(short, addr))
		}
	}
	return nil
}

// loginNeedsToken refuses a login without a token on a --listen address
// other machines can reach. Without a token the API (/v1/, /api/v1/)
// stays open, as connectors on this machine post to it, so anyone who can
// reach the address could read and delete what the sign-in guards. Only
// --listen matters: the SMTP sink and the proxy serve nothing to read.
// from names where the login came from (--login or RHYTHM_ECHO_LOGIN).
func loginNeedsToken(login *web.Login, from, token, listen string) error {
	if login == nil || token != "" || loopback(listen) {
		return nil
	}
	return usageErr("%s needs a token when --listen %s is reachable from other machines: without one the API answers anyone there; set --token (or RHYTHM_ECHO_TOKEN) for connectors and keep the login for viewers", from, listen)
}

// loginLine is the banner step for a login. Without a token (loopback
// only, see loginNeedsToken) the API stays open to this machine, so the
// line says the sign-in guards only Echo Inbox.
func loginLine(login *web.Login, token, listen string) string {
	line := "viewers sign in as " + login.User() + " at http://" + listen + web.PathLogin + " (read only, for 8 hours)"
	if token == "" {
		line += "; the API stays open to programs on this machine"
	}
	return line
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
	seen   *lru[logged]
}

// logged is what the log last printed for a message.
type logged struct {
	status model.Status
	marks  string
}

// newLiveLog prints to v; set checks for what content checks expect.
func newLiveLog(v *report.Pretty) *liveLog {
	return &liveLog{view: v, seen: newLRU[logged](watchMemory)}
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
	prev, seen := l.seen.put(m.ID, logged{m.Status, marks}) // twice what the inbox keeps; the oldest are forgotten
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

func modeLine(mode string) string {
	if mode == "observe" {
		return "observe: messages are recorded and sent for real"
	}
	return "seal: nothing leaves; Echo answers for every service"
}

// loopback reports whether a listen address stays on this machine.
func loopback(addr string) bool { return proxy.IsLoopbackAddr(addr) }

// privateNetworkEnv turns on --private-network from the environment, for
// a container whose arguments are hard to change.
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

// lookupIPs resolves a listen host for privateOnly, with a 5 s limit.
func lookupIPs(host string) ([]net.IP, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	return net.DefaultResolver.LookupIP(ctx, "ip", host)
}

// containerIface matches the bridges and virtual links container runtimes
// create (Docker, Podman, Kubernetes CNIs). The name alone is not trusted:
// a runtime can give a bridge any range, so its address must also be
// non-global (see sharedSpace).
var containerIface = regexp.MustCompile(`^(docker\d*|br-[0-9a-f]+|veth.+|cni\d*|podman\d*|cali.+|flannel.+|cilium.+)$`)

// sharedSpace is the carrier-grade NAT range (RFC 6598). It is not
// RFC 1918 but is never routed on the internet. Some runtimes and overlay
// networks hand it to container bridges.
var sharedSpace = &net.IPNet{IP: net.IPv4(100, 64, 0, 0).To4(), Mask: net.CIDRMask(10, 32)}

// privateOnly returns nil when addr can be reached only from loopback,
// private networks (RFC 1918, fc00::/7) or container interfaces with a
// private or shared (100.64.0.0/10) address. A
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
		return ip.IsLoopback() || ip.IsPrivate() || containerIface.MatchString(iface) && sharedSpace.Contains(ip)
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

// liveRun groups the inbox's messages that match f into actions (by run
// and action, "Live" for messages outside any action) with each
// message's content checks, so the HTML report can show it.
func liveRun(in *catcher.Inbox, mode string, checks check.Options, f catcher.Filter) *model.Run {
	r := &model.Run{Schema: model.Schema, Tool: "rhythm-echo", Version: version, Mode: mode, Started: time.Now()}
	at := map[string]int{}
	for _, m := range in.List(f) {
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
	w.Header().Set("Content-Security-Policy", "default-src 'none'; style-src 'unsafe-inline'; script-src 'unsafe-inline'; img-src * data:; frame-src 'self' data:; frame-ancestors 'none'")
	w.Header().Set("X-Frame-Options", "DENY")
	_ = report.HTML(w, report.Result{Run: liveRun(in, mode, checks, catcher.Filter{})})
}
