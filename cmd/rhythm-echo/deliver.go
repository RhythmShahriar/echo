// Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
// SPDX-License-Identifier: MIT

package main

import (
	"cmp"
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/RhythmShahriar/echo/client"
	"github.com/RhythmShahriar/echo/config"
	"github.com/RhythmShahriar/echo/dns"
	"github.com/RhythmShahriar/echo/headers"
	"github.com/RhythmShahriar/echo/model"
	"github.com/RhythmShahriar/echo/replay"
	"github.com/RhythmShahriar/echo/report"
	"github.com/RhythmShahriar/echo/runner"
	"github.com/RhythmShahriar/rhythm/kit/secrets"
)

// cmdCheck runs the DNS deliverability checks for a domain. Nothing is
// sent: these are DNS lookups (plus the MTA-STS policy file with
// --mta-sts-fetch).
func cmdCheck(s *session) int {
	o := &s.o
	from, prov, sel, rp, host := &o.fromMail, &o.provider, &o.selector, &o.returnPath, &o.smtpHost
	args := s.args
	if len(args) != 1 {
		return s.fail(usageErr("usage: rhythm echo check <domain> [--from-mail file.eml] [--provider name] [--selector s]\n" +
			"  [--return-path domain] [--smtp-host host] [--mta-sts-fetch] [--resolver host:port] [--json] [--fail-on level]"))
	}
	domain := strings.TrimSuffix(strings.ToLower(strings.TrimSpace(args[0])), ".")
	if strings.Contains(domain, "/") || strings.Contains(domain, "@") {
		if i := strings.LastIndex(domain, "@"); i >= 0 {
			domain = domain[i+1:]
		} else {
			return s.fail(usageErr("check takes a domain, like shop.com"))
		}
	}
	var sender *dns.Sender
	if *from != "" {
		raw, err := os.ReadFile(*from) //nolint:gosec // the user's own file
		if err != nil {
			return s.fail(err)
		}
		r := headers.Analyze(raw)
		sender = &r.Sender
		if domain == "" {
			domain = r.FromDomain
		}
	}
	if *prov != "" {
		if _, ok := dns.ProviderByName(*prov); !ok {
			return s.fail(usageErr("--provider %q: use one of %s", *prov, providerNames()))
		}
	}
	if *prov != "" || *sel != "" || *rp != "" || *host != "" {
		if sender == nil {
			sender = &dns.Sender{}
		}
		if *prov != "" {
			sender.Provider = *prov
		}
		if *sel != "" {
			sender.DKIMSelector = *sel
		}
		if *rp != "" {
			sender.ReturnPathDomain = *rp
		}
		if *host != "" {
			sender.SMTPHost = *host
		}
	}
	if err := s.checkFailOn(false); err != nil {
		return s.fail(err)
	}
	ctx, cancel := context.WithTimeout(s.ctx, dnsBudget)
	defer cancel()
	s.view.Header(version, domain)
	if sender != nil && sender.Provider != "" {
		s.view.Step("Sender", providerLabel(sender.Provider))
	}
	opts, pr := s.dnsOptions(sender)
	rep := dns.Check(ctx, domain, opts)
	if s.ctx.Err() != nil {
		return s.fail(s.ctx.Err())
	}
	if pr.unreachable() {
		// Nothing was looked up, so the warnings each record gives
		// would only hide the one cause. This is "couldn't run", not a
		// finding, so --fail-on has no say in it.
		issue := pr.issue()
		rep.Issues = []model.Issue{issue}
		if s.format == "json" {
			if err := s.writeJSON(kindCheck, unreachableReport{Report: rep, Unreachable: true, Resolver: s.o.resolver}); err != nil {
				return s.fail(err)
			}
			return exitError
		}
		fmt.Fprintf(s.stderr, "rhythm echo: %s (%s)\n  %s\n", issue.Title, issue.Detail, issue.Fix)
		return exitError
	}
	s.view.Step("Records", deliverySummary(rep))
	return s.issues(kindCheck, domain, rep.Issues, rep)
}

// dnsOptions are the DNS check settings from --resolver and
// --mta-sts-fetch. Lookups go through the returned probe, which limits
// each one and tells whether the resolver answered at all.
func (s *session) dnsOptions(sender *dns.Sender) (dns.Options, *probe) {
	opts := dns.Options{Sender: sender}
	if s.o.mtaSTSFetch {
		opts.HTTP = &http.Client{Timeout: 10 * time.Second}
	}
	r := dns.System
	if s.o.resolver != "" {
		r = customResolver(s.o.resolver)
	}
	pr := &probe{r: r, addr: s.o.resolver}
	opts.Resolver = pr
	return opts, pr
}

// Variables, not constants, so a test can wait on a dead resolver for
// less than the real budget.
var (
	// dnsQuery is how long one lookup may take. Go's default (5 s, two
	// tries per server) let a dead resolver hold check for a minute.
	dnsQuery = 3 * time.Second
	// dnsBudget caps a whole check: a dead resolver ends it in this
	// long, whatever the number of lookups.
	dnsBudget = 15 * time.Second
)

// unreachableReport is check --json when no lookup got an answer.
type unreachableReport struct {
	dns.Report
	Unreachable bool   `json:"unreachable"`
	Resolver    string `json:"resolver,omitempty"`
}

// probe is a Resolver that gives each lookup at most dnsQuery and counts
// the ones the server answered. An answer includes "no such name" and a
// server error: the resolver is there, the domain's records are not.
type probe struct {
	r    dns.Resolver
	addr string // --resolver, or "" for the system's

	mu              sync.Mutex
	asked, answered int
	last            error // the last lookup that got no answer
}

func (p *probe) LookupTXT(ctx context.Context, name string) ([]string, error) {
	ctx, cancel := context.WithTimeout(ctx, dnsQuery)
	defer cancel()
	v, err := p.r.LookupTXT(ctx, name)
	return v, p.note(err)
}

func (p *probe) LookupMX(ctx context.Context, name string) ([]*net.MX, error) {
	ctx, cancel := context.WithTimeout(ctx, dnsQuery)
	defer cancel()
	v, err := p.r.LookupMX(ctx, name)
	return v, p.note(err)
}

func (p *probe) LookupHost(ctx context.Context, host string) ([]string, error) {
	ctx, cancel := context.WithTimeout(ctx, dnsQuery)
	defer cancel()
	v, err := p.r.LookupHost(ctx, host)
	return v, p.note(err)
}

func (p *probe) LookupCNAME(ctx context.Context, host string) (string, error) {
	ctx, cancel := context.WithTimeout(ctx, dnsQuery)
	defer cancel()
	v, err := p.r.LookupCNAME(ctx, host)
	return v, p.note(err)
}

func (p *probe) note(err error) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.asked++
	if answered(err) {
		p.answered++
	} else {
		p.last = err
	}
	return err
}

// answered reports whether a lookup's result came from a DNS server.
// Go reports a timeout, a refused port and a dial error the same way as
// a reply, in a *net.DNSError, so the replies are picked out: no error,
// no such name, or the server's own error code.
func answered(err error) bool {
	var de *net.DNSError
	switch {
	case err == nil || dns.NotFound(err):
		return true
	case !errors.As(err, &de) || de.IsTimeout:
		return false
	}
	return de.Err == "server misbehaving"
}

// unreachable reports whether every lookup went unanswered.
func (p *probe) unreachable() bool {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.asked > 0 && p.answered == 0
}

// issue is the one error check reports when the resolver never answered.
// The fix points at the resolver: the domain was never asked about.
func (p *probe) issue() model.Issue {
	p.mu.Lock()
	defer p.mu.Unlock()
	i := model.Issue{Check: "dns/resolver", Level: model.Error, Title: "Could not reach the system resolver",
		Fix: "Check this machine's DNS settings and network connection, or pass --resolver host:port to ask another DNS server."}
	if p.addr != "" {
		i.Title = "Could not reach the resolver " + p.addr
		i.Fix = "Check that a DNS server is running at " + p.addr + " and answers queries from this machine, or leave out --resolver to use the system's."
	}
	if p.last != nil {
		i.Detail = lookupCause(p.last)
	}
	i.Identify()
	return i
}

// lookupCause is the part of a lookup error that says what went wrong.
// Go names the system's server ("on 127.0.0.53:53") even when a custom
// Dial asked another, so that part would point at the wrong machine.
func lookupCause(err error) string {
	var de *net.DNSError
	if errors.As(err, &de) {
		msg := de.Err
		if i := strings.LastIndex(msg, ": "); i >= 0 {
			msg = msg[i+2:]
		}
		return msg
	}
	return err.Error()
}

// deliverySummary says which mail records were found and which weren't
// (missing, or broken: two SPF records count as none).
func deliverySummary(r dns.Report) string {
	var found, missing []string
	for _, rec := range []struct {
		name string
		ok   bool
	}{{"SPF", r.SPF != nil}, {"DKIM", len(r.DKIM) > 0}, {"DMARC", r.DMARC != nil}, {"MX", len(r.MX) > 0 || r.NullMX}} {
		if rec.ok {
			found = append(found, rec.name)
		} else {
			missing = append(missing, rec.name)
		}
	}
	switch {
	case len(found) == 0:
		return "no mail records found"
	case len(missing) == 0:
		return "found " + strings.Join(found, ", ")
	}
	return "found " + strings.Join(found, ", ") + "; no usable " + strings.Join(missing, ", ")
}

func providerNames() string {
	var names []string
	for _, p := range dns.Providers {
		names = append(names, p.Name)
	}
	return strings.Join(names, ", ")
}

func providerLabel(name string) string {
	if p, ok := dns.ProviderByName(name); ok {
		return p.Label
	}
	return name
}

// cmdHeaders analyzes a received email's authentication and route.
func cmdHeaders(s *session) int {
	withDNS, hops, args := &s.o.withDNS, &s.o.hops, s.args
	var err error
	if len(args) != 1 {
		return s.fail(usageErr("usage: rhythm echo headers <file.eml | -> [--check] [--hops] [--resolver host:port] [--mta-sts-fetch] [--json] [--fail-on level]"))
	}
	var raw []byte
	if args[0] == "-" {
		raw, err = io.ReadAll(io.LimitReader(os.Stdin, 25<<20))
	} else {
		raw, err = os.ReadFile(args[0]) //nolint:gosec // the user names the file to read
	}
	if err != nil {
		return s.fail(err)
	}
	r := headers.Analyze(raw)
	issues := r.Issues
	if *withDNS && r.FromDomain != "" {
		ctx, cancel := context.WithTimeout(s.ctx, dnsBudget)
		opts, _ := s.dnsOptions(&r.Sender)
		rep := dns.Check(ctx, r.FromDomain, opts)
		cancel()
		issues = append(issues, rep.Issues...)
	}
	r.Issues = issues
	s.view.Header(version, args[0])
	s.view.Step("From", r.From)
	s.view.Step("Sent by", firstNonEmpty(providerLabel(r.Provider), "unknown service"))
	s.view.Step("Results", fmt.Sprintf("SPF %s · DKIM %s · DMARC %s", orNone(r.Auth.SPF), orNone(r.Auth.DKIM), orNone(r.Auth.DMARC)))
	if *hops {
		for i, h := range r.Hops {
			s.view.Step(fmt.Sprintf("Hop %d", i+1), fmt.Sprintf("%s → %s %s", h.From, h.By, h.With))
		}
	}
	label := filepath.Base(args[0])
	if args[0] == "-" {
		label = "the message"
	}
	return s.issues(kindHeaders, label, issues, r)
}

// issues prints issues as a run would and picks the exit code. A JSON
// report is v, the command's own result, in an envelope of kind, to
// stdout or -o.
func (s *session) issues(kind jsonKind, title string, is []model.Issue, v any) int {
	if err := s.checkFailOn(false); err != nil {
		return s.fail(err)
	}
	run := &model.Run{Schema: model.Schema, Tool: "rhythm-echo", Version: version, Mode: "check", Started: time.Now(),
		Actions: []model.Action{{ID: "check", Label: title, Issues: is}}}
	res := report.Result{Run: run}
	switch s.format {
	case "":
		s.view.Summary(res, "")
	case "json":
		if err := s.writeJSON(kind, v); err != nil {
			return s.fail(err)
		}
	default:
		return s.finish(res, "")
	}
	if s.o.failOn != failOnNone && report.FailOn(res, s.o.failOn) {
		return exitFindings
	}
	return exitOK
}

// writeJSON writes v in an envelope of kind to the -o file, or to stdout.
func (s *session) writeJSON(kind jsonKind, v any) error {
	if s.o.output == "" {
		return writeEnvelope(s.stdout, kind, v)
	}
	if err := writeFile(s.o.output, func(w io.Writer) error { return writeEnvelope(w, kind, v) }); err != nil {
		return err
	}
	if !s.o.quiet {
		fmt.Fprintf(s.stderr, "Report written to %s\n", s.o.output)
	}
	return nil
}

// cmdReplay sends a signed test webhook to a staging site, with Echo
// sealed so whatever the site sends in response is captured.
func cmdReplay(s *session) int {
	o := &s.o
	to, sets, hdrs, secretEnv, scheme, typ, list, wait := &o.to, o.sets, o.headers, &o.secretEnv, &o.scheme, &o.typ, &o.list, &o.wait
	args := s.args
	if *list {
		for _, e := range replay.Events() {
			fmt.Fprintln(s.stdout, e)
		}
		return exitOK
	}
	if len(args) != 1 || *to == "" {
		return s.fail(usageErr("usage: rhythm echo replay <event | file.json> --to <url>   (see --list)"))
	}
	h, err := s.echo.Health(s.ctx)
	if err != nil {
		return s.fail(fmt.Errorf("%w; replays run with Echo up and sealed, so what the site sends back is caught", err))
	}
	if h.Mode != "seal" {
		return s.fail(usageErr("Echo is in %s; replays only run sealed, so the site's reactions can't reach anyone (rhythm echo up --seal)", runner.ModeName(h.Mode)))
	}
	ev, err := loadEvent(args[0], *scheme, *typ)
	if err != nil {
		return s.fail(usageErr("%v", err))
	}
	if err := ev.SetAll(sets); err != nil {
		return s.fail(usageErr("--set: %v", err))
	}
	headersMap := map[string]string{}
	for _, raw := range hdrs {
		name, value, ok := strings.Cut(raw, ":")
		if !ok || strings.TrimSpace(name) == "" {
			return s.fail(usageErr("bad header %q, use \"Name: value\"", raw))
		}
		headersMap[strings.TrimSpace(name)] = strings.TrimSpace(value)
	}
	opts := replay.Options{SecretEnv: *secretEnv, Staging: s.cfg.Staging, Scan: scanConfigs(s.projectDir(), s.cfgPath)}
	if err := replay.SecretReady(ev, opts); err != nil {
		return s.fail(err) // a usage error, before the header
	}
	runID := "replay-" + time.Now().UTC().Format("20060102T150405")
	action := "replay:" + ev.Type
	_ = s.echo.Begin(s.ctx, runID, action)
	defer func() { _ = s.echo.End(context.WithoutCancel(s.ctx)) }()
	target := resolveTarget(s.cfg.Site, *to)
	s.view.Header(version, secrets.Redact(target))
	s.view.Step("Event", ev.Name+" ("+ev.Scheme+")")
	res, err := replay.Send(s.ctx, ev, replay.Target{URL: target, Headers: headersMap},
		replay.Options{SecretEnv: opts.SecretEnv, Staging: opts.Staging, Scan: opts.Scan, Run: runID, Action: action, RunKey: s.runKey(h)})
	if err != nil {
		return s.fail(err)
	}
	s.view.Step("Response", fmt.Sprintf("%d in %s", res.Status, res.Duration.Round(time.Millisecond)))
	msgs, _ := s.echo.Wait(s.ctx, client.Filter{Run: runID, Action: action}, 0, 1500*time.Millisecond, *wait)
	for _, m := range msgs {
		s.view.Message(m)
	}
	if s.o.json {
		if code := jsonOut(s, kindReplay, replayData{Replay: res, Messages: msgs}); code != exitOK || res.OK() {
			return code
		}
		return exitFindings
	}
	if !s.o.quiet {
		fmt.Fprintf(s.stdout, "\n  The site answered %d and sent %s in response.\n\n", res.Status, report.Tally(&model.Run{Actions: []model.Action{{Messages: msgs}}}))
	}
	if !res.OK() {
		return exitFindings
	}
	return exitOK
}

func loadEvent(name, scheme, typ string) (replay.Event, error) {
	if strings.HasSuffix(name, ".json") {
		if _, err := os.Stat(name); err == nil {
			return replay.FromFile(name, scheme, typ)
		}
	}
	return replay.Load(name)
}

// replayer runs replay actions from .echo/config.yaml.
func replayer(cfg config.Config, dir, cfgPath, runKey string) runner.Replayer {
	return func(ctx context.Context, a config.ReplayAction, to, run string) error {
		ev, err := loadEvent(a.Event, "", "")
		if err != nil {
			return err
		}
		var sets []string
		for k, v := range a.Set {
			sets = append(sets, k+"="+v)
		}
		slices.Sort(sets)
		if err := ev.SetAll(sets); err != nil {
			return err
		}
		r, act, _ := strings.Cut(run, "/")
		res, err := replay.Send(ctx, ev, replay.Target{URL: to}, replay.Options{SecretEnv: a.Secret, Staging: cfg.Staging,
			Scan: scanConfigs(dir, cfgPath), Run: r, Action: act, RunKey: runKey})
		if err != nil {
			return err
		}
		if !res.OK() {
			return fmt.Errorf("the site answered %d", res.Status)
		}
		return nil
	}
}

// scanConfigs reads config files in reach (.env, wp-config.php) so replay
// can refuse to run where live payment keys are configured: those in the
// working folder, in the project folder dir when Echo's config was found
// above it and in Echo's config itself.
func scanConfigs(dir, cfgPath string) map[string]string {
	out := map[string]string{}
	files := []string{".env", "wp-config.php", "../wp-config.php", cmp.Or(cfgPath, config.Path())}
	if dir != "." {
		files = append(files, filepath.Join(dir, ".env"), filepath.Join(dir, "wp-config.php"), filepath.Join(dir, "..", "wp-config.php"))
	}
	for _, f := range files {
		if _, seen := out[f]; seen {
			continue
		}
		if b, err := os.ReadFile(f); err == nil { //nolint:gosec // fixed names in the project
			out[f] = string(b)
		}
	}
	return out
}

func resolveTarget(site, to string) string {
	if strings.Contains(to, "://") || site == "" {
		return to
	}
	return strings.TrimRight(site, "/") + "/" + strings.TrimLeft(to, "/")
}

// customResolver asks the DNS server at addr (port 53 when it has none).
func customResolver(addr string) dns.Resolver {
	if _, _, err := net.SplitHostPort(addr); err != nil {
		addr = net.JoinHostPort(strings.Trim(addr, "[]"), "53")
	}
	return &net.Resolver{PreferGo: true, Dial: func(ctx context.Context, network, _ string) (net.Conn, error) {
		var d net.Dialer
		return d.DialContext(ctx, network, addr)
	}}
}

func orNone(s string) string { return firstNonEmpty(s, "none") }

func firstNonEmpty(s ...string) string {
	for _, v := range s {
		if v != "" {
			return v
		}
	}
	return ""
}
