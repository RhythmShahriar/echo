package main

import (
	"context"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"slices"
	"strings"
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
	ctx, cancel := context.WithTimeout(s.ctx, 60*time.Second)
	defer cancel()
	s.view.Header(version, domain)
	if sender != nil && sender.Provider != "" {
		s.view.Step("Sender", providerLabel(sender.Provider))
	}
	rep := dns.Check(ctx, domain, s.dnsOptions(sender))
	if s.o.json {
		return jsonOut(s, rep)
	}
	s.view.Step("Records", deliverySummary(rep))
	return s.issues(domain, rep.Issues)
}

// dnsOptions are the DNS check settings from --resolver and --mta-sts-fetch.
func (s *session) dnsOptions(sender *dns.Sender) dns.Options {
	opts := dns.Options{Sender: sender}
	if s.o.mtaSTSFetch {
		opts.HTTP = &http.Client{Timeout: 10 * time.Second}
	}
	if s.o.resolver != "" {
		opts.Resolver = customResolver(s.o.resolver)
	}
	return opts
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
		ctx, cancel := context.WithTimeout(s.ctx, 60*time.Second)
		rep := dns.Check(ctx, r.FromDomain, s.dnsOptions(&r.Sender))
		cancel()
		issues = append(issues, rep.Issues...)
	}
	if s.o.json {
		r.Issues = issues
		return jsonOut(s, r)
	}
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
	return s.issues(label, issues)
}

// issues prints issues as a run would and picks the exit code.
func (s *session) issues(title string, is []model.Issue) int {
	if err := s.checkFailOn(false); err != nil {
		return s.fail(err)
	}
	run := &model.Run{Schema: model.Schema, Tool: "rhythm-echo", Version: version, Mode: "check", Started: time.Now(),
		Actions: []model.Action{{ID: "check", Label: title, Issues: is}}}
	res := report.Result{Run: run}
	if s.format != "" && s.format != "json" {
		return s.finish(res, "")
	}
	s.view.Summary(res, "")
	if report.FailOn(res, s.o.failOn) {
		return exitFindings
	}
	return exitOK
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
		return s.fail(usageErr("Echo is in %s mode; replays only run sealed, so the site's reactions can't reach anyone (rhythm echo up --seal)", h.Mode))
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
	opts := replay.Options{SecretEnv: *secretEnv, Staging: s.cfg.Staging, Scan: scanConfigs()}
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
		replay.Options{SecretEnv: opts.SecretEnv, Staging: opts.Staging, Scan: opts.Scan, Run: runID, Action: action})
	if err != nil {
		return s.fail(err)
	}
	s.view.Step("Response", fmt.Sprintf("%d in %s", res.Status, res.Duration.Round(time.Millisecond)))
	msgs, _ := s.echo.Wait(s.ctx, client.Filter{Run: runID, Action: action}, 0, 1500*time.Millisecond, *wait)
	for _, m := range msgs {
		s.view.Message(m)
	}
	if s.o.json {
		return jsonOut(s, map[string]any{"replay": res, "messages": msgs})
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
func replayer(cfg config.Config) runner.Replayer {
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
			Scan: scanConfigs(), Run: r, Action: act})
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
// can refuse to run where live payment keys are configured.
func scanConfigs() map[string]string {
	out := map[string]string{}
	for _, f := range []string{".env", "wp-config.php", "../wp-config.php", config.Path()} {
		if b, err := os.ReadFile(f); err == nil { //nolint:gosec // fixed local names
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

func customResolver(addr string) dns.Resolver {
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
