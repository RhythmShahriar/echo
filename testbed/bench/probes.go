// Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
// SPDX-License-Identifier: MIT

//go:build testbed

package main

import (
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/smtp"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/RhythmShahriar/echo/client"
	"github.com/RhythmShahriar/echo/wire"
)

// The local probes. Each runs its own Echo on localAPI and, when it needs
// one, a fixture site on localSite, so it needs nothing but the binary
// under test. The testbed probes are in probes_testbed.go.
func init() {
	probes["site-down"] = siteDown
	probes["two-runs"] = twoRuns
	probes["token"] = tokenProbe
	probes["slow-queue"] = slowQueue
	probes["attachment"] = attachmentProbe
	probes["restart"] = restartProbe
	probes["old-snapshot"] = oldSnapshot
	probes["colliding-names"] = collidingNames
	probes["windows"] = windowsProbe
	probes["resolver-down"] = resolverDown
	probes["non-ascii"] = nonASCII
}

// shopSite answers POST /contact like a contact form with Echo's
// connector: it reports an email to the owner and one to the visitor,
// tagged with the request's X-Echo-Run, to the local Echo. With
// untagged, the owner's copy goes over plain SMTP with no run, as a site
// without a connector sends it. delay holds each request up first.
func shopSite(who string, delay time.Duration, untagged bool) http.Handler {
	echo := client.New("http://"+localAPI, "")
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		time.Sleep(delay)
		run := r.Header.Get(wire.RunHeader)
		if untagged {
			msg := "From: shop@shop.example.com\r\nTo: " + who + "-owner@shop.test\r\nSubject: New message\r\n\r\nA visitor wrote.\r\n"
			if err := smtp.SendMail(localSMTP, nil, "shop@shop.example.com", []string{who + "-owner@shop.test"}, []byte(msg)); err != nil {
				http.Error(w, err.Error(), http.StatusBadGateway)
				return
			}
		}
		for _, to := range []string{who + "-owner@shop.test", who + "-visitor@shop.test"} {
			if untagged && strings.HasSuffix(to, "-owner@shop.test") {
				continue
			}
			_, err := echo.Capture(r.Context(), wire.Capture{Kind: "email", Run: run, Source: wire.Source{Via: "wordpress", Hook: "wp_mail"},
				Email: &wire.Email{From: "shop@shop.example.com", To: []string{to}, Subject: "New message", Text: "A visitor wrote."}})
			if err != nil {
				http.Error(w, err.Error(), http.StatusBadGateway)
				return
			}
		}
		_, _ = fmt.Fprint(w, "ok")
	})
}

// contactConfig is a project with one HTTP action against the fixture
// site.
func contactConfig(path string) string {
	return "site: http://" + localSite + "\nquiet: 1s\ntimeout: 10s\nactions:\n  - name: Contact form\n    http:\n      method: POST\n      url: " + path + "\n"
}

// siteDown: the site doesn't answer, at the snapshot and at the compare.
// Echo must report a run error and exit 3, never "all clear".
func siteDown(ctx context.Context, p *ProbeEnv, broken bool) ([]Observation, error) {
	e, err := p.startEcho(ctx, "")
	if err != nil {
		return nil, err
	}
	defer e.Stop()
	dir := p.dir("shop")
	if err := writeConfig(dir, contactConfig("/contact")); err != nil {
		return nil, err
	}
	var obs []Observation
	site, err := startSite(shopSite("a", 0, false))
	if err != nil {
		return nil, err
	}
	if broken {
		site.Stop()
		obs = append(obs, p.step(ctx, "snapshot", dir, "", "snapshot", "--name", "down", "--fail-on", "none"))
		if site, err = startSite(shopSite("a", 0, false)); err != nil {
			return nil, err
		}
	} else {
		obs = append(obs, p.step(ctx, "snapshot", dir, "", "snapshot", "--name", "down", "--fail-on", "none"))
	}
	if o := p.step(ctx, "baseline", dir, "", "snapshot", "--name", "good", "--fail-on", "none"); o.Exit != 0 {
		site.Stop()
		return nil, fmt.Errorf("the baseline snapshot exited %d: %s", o.Exit, o.Output)
	}
	if broken {
		site.Stop()
	}
	obs = append(obs, p.step(ctx, "compare", dir, "", "compare", "--snapshot", "good", "--fail-on", "none"))
	site.Stop()
	return obs, nil
}

// twoRuns: two projects compare against one Echo at the same moment,
// as two CI jobs would. Project a's site tags all its mail; project b's
// sends the owner's copy over plain SMTP with no run. Nothing may land in
// the other run and only b, whose mail can't say where it belongs, may
// be warned that some couldn't be tied to an action.
func twoRuns(ctx context.Context, p *ProbeEnv, broken bool) ([]Observation, error) {
	e, err := p.startEcho(ctx, "")
	if err != nil {
		return nil, err
	}
	defer e.Stop()
	mux := http.NewServeMux()
	mux.Handle("/a", shopSite("a", 1500*time.Millisecond, false))
	mux.Handle("/b", shopSite("b", 1500*time.Millisecond, true))
	site, err := startSite(mux)
	if err != nil {
		return nil, err
	}
	defer site.Stop()
	dirs := map[string]string{}
	for _, who := range []string{"a", "b"} {
		dirs[who] = p.dir(who)
		if err := writeConfig(dirs[who], contactConfig("/"+who)); err != nil {
			return nil, err
		}
		if o := p.step(ctx, "baseline", dirs[who], "", "snapshot", "--name", "good", "--fail-on", "none"); o.Exit != 0 {
			return nil, fmt.Errorf("the baseline snapshot of %s exited %d: %s", who, o.Exit, o.Output)
		}
	}
	compare := func(who string) Observation {
		o := p.step(ctx, "compare "+who, dirs[who], "", "compare", "--snapshot", "good", "--fail-on", "none")
		other := map[string]string{"a": "b", "b": "a"}[who]
		for _, line := range strings.Split(o.Output, "\n") {
			if strings.HasPrefix(line, "message: ") && strings.Contains(line, " "+other+"-") {
				o.Output += "\nforeign mail: " + strings.TrimPrefix(line, "message: ")
			}
		}
		return o
	}
	if !broken {
		return []Observation{compare("a"), compare("b")}, nil
	}
	obs := make([]Observation, 2)
	var wg sync.WaitGroup
	for i, who := range []string{"a", "b"} {
		wg.Go(func() { obs[i] = compare(who) })
	}
	wg.Wait()
	return obs, nil
}

// tokenProbe: Echo on 0.0.0.0 with a token, the way the GitHub Action's
// service container runs it. A request from another container (its Host
// is the service name) must need the token for the live view and for the
// API alike. The good state is Echo on loopback, where no token is
// needed.
func tokenProbe(ctx context.Context, p *ProbeEnv, broken bool) ([]Observation, error) {
	var obs []Observation
	token, args, host := "", []string(nil), "127.0.0.1:23200"
	if broken {
		token, args, host = localToken, []string{"--listen", "0.0.0.0:23200"}, "echo:23200"
		// Without a token Echo must refuse to listen beyond loopback.
		exit, out := p.run(ctx, p.Work, p.env(""), p.Bin, "up", "--seal", "--listen", "0.0.0.0:23200", "--smtp", localSMTP, "--plain")
		obs = append(obs, Observation{Step: "up without a token", Exit: exit, Output: out})
	}
	e, err := p.startEcho(ctx, token, args...)
	if err != nil {
		return nil, err
	}
	defer e.Stop()
	var b strings.Builder
	for _, c := range []struct{ what, path string }{{"live view", "/"}, {"API", "/v1/messages"}} {
		code, err := status(ctx, c.path, host, "")
		if err != nil {
			return nil, err
		}
		fmt.Fprintf(&b, "%s without the token: %d\n", c.what, code)
		if token != "" {
			if code, err = status(ctx, c.path, host, token); err != nil {
				return nil, err
			}
			fmt.Fprintf(&b, "%s with the token: %d\n", c.what, code)
		}
	}
	return append(obs, Observation{Step: "requests", Output: b.String()}), nil
}

// slowQueue: the receipt goes through a queue that sends it 2.5 s later,
// while the next action runs; quiet is 1 s. Echo must credit it to
// the action that caused it with a note that it came late, not report
// it as no longer sent.
func slowQueue(ctx context.Context, p *ProbeEnv, broken bool) ([]Observation, error) {
	e, err := p.startEcho(ctx, "")
	if err != nil {
		return nil, err
	}
	defer e.Stop()
	dir := p.dir("shop")
	receipt, err := p.mailCmd(dir, "receipt", plainMail("ann@shop.test", "Your receipt", "Thanks for your order."))
	if err != nil {
		return nil, err
	}
	contact, err := p.mailCmd(dir, "contact", plainMail("owner@shop.test", "New message", "A visitor wrote."))
	if err != nil {
		return nil, err
	}
	config := func(queued bool) error {
		send := receipt
		if queued {
			send = "(sleep 2.5; " + receipt + ") >/dev/null 2>&1 &"
		}
		return writeConfig(dir, "quiet: 1s\ntimeout: 10s\nactions:\n  - name: Checkout\n    command: "+yamlQuote(send)+
			"\n  - name: Contact form\n    command: "+yamlQuote("sleep 3; "+contact)+"\n")
	}
	if err := config(false); err != nil {
		return nil, err
	}
	if o := p.step(ctx, "baseline", dir, "", "snapshot", "--name", "good", "--fail-on", "none"); o.Exit != 0 {
		return nil, fmt.Errorf("the baseline snapshot exited %d: %s", o.Exit, o.Output)
	}
	if err := config(broken); err != nil {
		return nil, err
	}
	return []Observation{p.step(ctx, "compare", dir, "", "compare", "--snapshot", "good", "--fail-on", "none")}, nil
}

// invoiceMail is a receipt with a PDF invoice attached, or without it.
func invoiceMail(attached bool) string {
	head := "From: Bean & Leaf <shop@shop.example.com>\nTo: ann@shop.test\nSubject: Your receipt for order 1042\nMIME-Version: 1.0\n"
	if !attached {
		return head + "Content-Type: text/plain; charset=utf-8\n\nThanks for your order. Your invoice is attached.\n"
	}
	return head + "Content-Type: multipart/mixed; boundary=\"inv\"\n\n" +
		"--inv\nContent-Type: text/plain; charset=utf-8\n\nThanks for your order. Your invoice is attached.\n" +
		"--inv\nContent-Type: application/pdf; name=\"invoice-1042.pdf\"\nContent-Disposition: attachment; filename=\"invoice-1042.pdf\"\n" +
		"Content-Transfer-Encoding: base64\n\nJVBERi0xLjQKJcOkw7zDtsOfCjEgMCBvYmoKPDw+PgplbmRvYmoKdHJhaWxlcgo8PD4+CiUlRU9G\n--inv--\n"
}

// attachmentProbe: the PDF invoice plugin stops attaching after an
// update. The receipt is still sent, so only the attachment says what
// broke: BROKE message/attachment-lost.
func attachmentProbe(ctx context.Context, p *ProbeEnv, broken bool) ([]Observation, error) {
	e, err := p.startEcho(ctx, "")
	if err != nil {
		return nil, err
	}
	defer e.Stop()
	dir := p.dir("shop")
	config := func(attached bool) error {
		cmd, err := p.mailCmd(dir, "receipt", invoiceMail(attached))
		if err != nil {
			return err
		}
		return writeConfig(dir, "quiet: 1s\ntimeout: 10s\nactions:\n  - name: Checkout\n    command: "+yamlQuote(cmd)+"\n")
	}
	if err := config(true); err != nil {
		return nil, err
	}
	if o := p.step(ctx, "baseline", dir, "", "snapshot", "--name", "good", "--fail-on", "none"); o.Exit != 0 {
		return nil, fmt.Errorf("the baseline snapshot exited %d: %s", o.Exit, o.Output)
	}
	if err := config(!broken); err != nil {
		return nil, err
	}
	return []Observation{p.step(ctx, "compare", dir, "", "compare", "--snapshot", "good", "--fail-on", "none")}, nil
}

// restartProbe: Echo restarts between two actions of a compare. Its
// inbox is in memory, so what the first action sent is gone and the
// compare can't be trusted: the run must say Echo restarted.
func restartProbe(ctx context.Context, p *ProbeEnv, broken bool) ([]Observation, error) {
	e, err := p.startEcho(ctx, "")
	if err != nil {
		return nil, err
	}
	defer func() { e.Stop() }()
	dir := p.dir("shop")
	flag := filepath.Join(dir, "restart-now")
	welcome, err := p.mailCmd(dir, "welcome", plainMail("ann@shop.test", "Welcome", "Thanks for signing up."))
	if err != nil {
		return nil, err
	}
	receipt, err := p.mailCmd(dir, "receipt", plainMail("ann@shop.test", "Your receipt", "Thanks for your order."))
	if err != nil {
		return nil, err
	}
	config := func(restart bool) error {
		first := welcome
		if restart {
			first += "; touch " + yamlQuote(flag)[1:len(yamlQuote(flag))-1] + "; sleep 3"
		}
		return writeConfig(dir, "quiet: 1s\ntimeout: 10s\nactions:\n  - name: Signup\n    command: "+yamlQuote(first)+
			"\n  - name: Checkout\n    command: "+yamlQuote(receipt)+"\n")
	}
	if err := config(false); err != nil {
		return nil, err
	}
	if o := p.step(ctx, "baseline", dir, "", "snapshot", "--name", "good", "--fail-on", "none"); o.Exit != 0 {
		return nil, fmt.Errorf("the baseline snapshot exited %d: %s", o.Exit, o.Output)
	}
	if err := config(broken); err != nil {
		return nil, err
	}
	var restartErr error
	done := make(chan struct{})
	if broken {
		go func() {
			defer close(done)
			for deadline := time.Now().Add(time.Minute); time.Now().Before(deadline); time.Sleep(50 * time.Millisecond) {
				if _, err := os.Stat(flag); err == nil {
					e.Stop()
					e, restartErr = p.startEcho(ctx, "")
					return
				}
			}
			restartErr = errors.New("the first action never asked for the restart")
		}()
	} else {
		close(done)
	}
	o := p.step(ctx, "compare", dir, "", "compare", "--snapshot", "good", "--fail-on", "none")
	<-done
	if restartErr != nil {
		return nil, restartErr
	}
	return []Observation{o}, nil
}

// oldSnapshot: the baseline was taken by Echo 1.0.0, before an upgrade.
// It must still load: compare runs against it instead of asking for a
// new snapshot. The fixtures in testdata/runs are real snapshots, one per
// released schema, of the same action as here.
func oldSnapshot(ctx context.Context, p *ProbeEnv, broken bool) ([]Observation, error) {
	e, err := p.startEcho(ctx, "")
	if err != nil {
		return nil, err
	}
	defer e.Stop()
	dir := p.dir("shop")
	cmd, err := p.mailCmd(dir, "welcome", plainMail("ann@shop.test", "Welcome to Bean & Leaf", "Thanks for signing up."))
	if err != nil {
		return nil, err
	}
	if err := writeConfig(dir, "quiet: 1s\ntimeout: 10s\nactions:\n  - name: Signup\n    command: "+yamlQuote(cmd)+"\n"); err != nil {
		return nil, err
	}
	var obs []Observation
	if broken {
		fixtures, err := filepath.Glob(filepath.Join(p.Testbed, "bench", "testdata", "runs", "*", "run.json"))
		if err != nil || len(fixtures) == 0 {
			return nil, fmt.Errorf("no snapshot fixtures in testbed/bench/testdata/runs: %w", err)
		}
		for _, f := range fixtures {
			version := filepath.Base(filepath.Dir(f))
			data, err := os.ReadFile(f) //nolint:gosec // the testbed's own fixture
			if err != nil {
				return nil, err
			}
			snap := filepath.Join(dir, ".echo", "snapshots", "v"+version)
			if err := os.MkdirAll(snap, 0o700); err != nil {
				return nil, err
			}
			if err := os.WriteFile(filepath.Join(snap, "run.json"), data, 0o600); err != nil { //nolint:gosec // a folder named after the testbed's own fixture
				return nil, err
			}
			o := p.step(ctx, "compare", dir, "", "compare", "--snapshot", "v"+version, "--fail-on", "none")
			o.Output = "snapshot from Echo " + version + "\n" + o.Output
			obs = append(obs, o)
		}
		return mergeSteps(obs), nil
	}
	if o := p.step(ctx, "baseline", dir, "", "snapshot", "--name", "good", "--fail-on", "none"); o.Exit != 0 {
		return nil, fmt.Errorf("the baseline snapshot exited %d: %s", o.Exit, o.Output)
	}
	return []Observation{p.step(ctx, "compare", dir, "", "compare", "--snapshot", "good", "--fail-on", "none")}, nil
}

// mergeSteps folds observations of the same step into one: the highest
// exit code, every output and every change, so a check covers them all.
func mergeSteps(obs []Observation) []Observation {
	var out []Observation
	for _, o := range obs {
		i := -1
		for j := range out {
			if out[j].Step == o.Step {
				i = j
			}
		}
		if i < 0 {
			out = append(out, o)
			continue
		}
		out[i].Exit = max(out[i].Exit, o.Exit)
		if out[i].Verdict != o.Verdict {
			out[i].Verdict += "," + o.Verdict
		}
		out[i].Output += "\n" + o.Output
		out[i].Changes = append(out[i].Changes, o.Changes...)
	}
	return out
}

// collidingNames: two actions whose names differ only in case and
// punctuation get the same ID, so compare would mix up what each sent.
// Echo must refuse the config before running anything.
func collidingNames(ctx context.Context, p *ProbeEnv, broken bool) ([]Observation, error) {
	dir := p.dir("shop")
	second := "Newsletter signup"
	if broken {
		second = "contact-form"
	}
	if err := writeConfig(dir, "quiet: 1s\nactions:\n  - name: Contact form\n    command: \"true\"\n  - name: "+second+"\n    command: \"true\"\n"); err != nil {
		return nil, err
	}
	e, err := p.startEcho(ctx, "")
	if err != nil {
		return nil, err
	}
	defer e.Stop()
	return []Observation{p.step(ctx, "snapshot", dir, "", "snapshot", "--name", "good", "--fail-on", "none")}, nil
}

// windowsProbe runs a snapshot and a compare on Windows, with a command
// action, a flush command and an HTTP action against the fixture site.
// In the broken state the command no longer sends its email.
func windowsProbe(ctx context.Context, p *ProbeEnv, broken bool) ([]Observation, error) {
	e, err := p.startEcho(ctx, "")
	if err != nil {
		return nil, err
	}
	defer e.Stop()
	site, err := startSite(shopSite("a", 0, false))
	if err != nil {
		return nil, err
	}
	defer site.Stop()
	dir := p.dir("shop")
	cmd, err := p.mailCmd(dir, "receipt", plainMail("ann@shop.test", "Your receipt", "Thanks for your order."))
	if err != nil {
		return nil, err
	}
	config := func(sends bool) error {
		c := cmd
		if !sends {
			c = `"` + p.Bin + `" version`
		}
		return writeConfig(dir, contactConfig("/contact")+"  - name: Checkout\n    command: "+yamlQuote(c)+"\nflush:\n  - "+yamlQuote(`"`+p.Bin+`" version`)+"\n")
	}
	if err := config(true); err != nil {
		return nil, err
	}
	snap := p.step(ctx, "snapshot", dir, "", "snapshot", "--name", "good", "--fail-on", "none")
	if err := config(!broken); err != nil {
		return nil, err
	}
	return []Observation{snap, p.step(ctx, "compare", dir, "", "compare", "--snapshot", "good", "--fail-on", "none")}, nil
}

// resolverDown: the deliverability check of s17's and s18's From domains
// while the resolver doesn't answer. Refused or silent, `check` must exit
// 3 and name the resolver, not report the domain as fine or broken.
func resolverDown(ctx context.Context, p *ProbeEnv, broken bool) ([]Observation, error) {
	check := func(step, domain, resolver string) Observation {
		o := p.step(ctx, step, p.Work, "", "check", domain, "--resolver", resolver, "--provider", "sendgrid",
			"--return-path", "em1234.shop.example.com", "--fail-on", "none")
		return o
	}
	domains := []struct{ id, domain string }{{"s17", "beanleaf-news.example.net"}, {"s18", "shop.example.com"}}
	var obs []Observation
	if !broken {
		zone, err := LoadZone(filepath.Join(p.Testbed, "dns", "zone.yaml"))
		if err != nil {
			return nil, err
		}
		srv := &DNSServer{Zone: zone, State: p.Work}
		if err := srv.Start(localDNS); err != nil {
			return nil, err
		}
		defer srv.Close()
		for _, d := range domains {
			obs = append(obs, check(d.id, d.domain, localDNS))
		}
		return obs, nil
	}
	// Refused: nothing listens there.
	for _, d := range domains {
		obs = append(obs, check(d.id, d.domain, localDNS))
	}
	// Silent: it takes the query and never answers, as a dead resolver
	// behind a firewall does.
	stop, err := blackHole(silentDNS)
	if err != nil {
		return nil, err
	}
	defer stop()
	start := time.Now()
	o := check("silent", domains[1].domain, silentDNS)
	o.Output += fmt.Sprintf("\ntook %.0f s\n", time.Since(start).Seconds())
	if time.Since(start) > 30*time.Second {
		o.Output += "slower than 30 s\n"
	}
	return append(obs, o), nil
}

// nonASCII: right-to-left text, a CJK subject and a name with a
// right-to-left override, sent the same way at the snapshot and at the
// compare. Echo must find nothing wrong (no mojibake, no placeholder, no
// change) and print the override as an escape, never raw.
func nonASCII(ctx context.Context, p *ProbeEnv, broken bool) ([]Observation, error) {
	e, err := p.startEcho(ctx, "")
	if err != nil {
		return nil, err
	}
	defer e.Stop()
	dir := p.dir("shop")
	subject, body := "Your order from Bean & Leaf", "Thanks Ali, your order 1042 is on its way."
	if broken {
		// "Order for <RLO>Ali<PDF>: ご注文ありがとうございます", RFC 2047 encoded, and
		// Arabic and Hebrew text in the body.
		subject = "=?UTF-8?B?" + b64("Order for \u202eAli\u202c: ご注文ありがとうございます") + "?="
		body = "شكراً لطلبك رقم 1042 من Bean & Leaf.\nתודה על ההזמנה שלך.\n注文番号 1042 を発送しました。"
	}
	cmd, err := p.mailCmd(dir, "order", plainMail("ann@shop.test", subject, body))
	if err != nil {
		return nil, err
	}
	if err := writeConfig(dir, "quiet: 1s\ntimeout: 10s\nactions:\n  - name: Checkout\n    command: "+yamlQuote(cmd)+"\n"); err != nil {
		return nil, err
	}
	snap := p.step(ctx, "snapshot", dir, "", "snapshot", "--name", "good", "--fail-on", "none")
	exit, out := p.run(ctx, dir, p.env(""), p.Bin, "ls")
	plain := Observation{Step: "list", Exit: exit, Output: out}
	return []Observation{snap, plain, p.step(ctx, "compare", dir, "", "compare", "--snapshot", "good", "--fail-on", "none")}, nil
}

func b64(s string) string { return base64.StdEncoding.EncodeToString([]byte(s)) }

// blackHole listens for UDP on addr and never answers; stop closes it.
func blackHole(addr string) (stop func(), err error) {
	c, err := net.ListenPacket("udp", addr)
	if err != nil {
		return nil, err
	}
	go func() {
		buf := make([]byte, 4096)
		for {
			if _, _, err := c.ReadFrom(buf); err != nil {
				return
			}
		}
	}()
	return func() { _ = c.Close() }, nil
}
