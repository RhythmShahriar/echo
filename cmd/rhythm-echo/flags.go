// Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
// SPDX-License-Identifier: MIT

package main

import (
	"cmp"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/RhythmShahriar/echo/client"
	"github.com/RhythmShahriar/echo/config"
	"github.com/RhythmShahriar/echo/model"
	"github.com/RhythmShahriar/echo/store"
	"github.com/RhythmShahriar/rhythm/kit/cli"
)

type options struct {
	json, md, junit, html          bool
	output                         string
	open                           bool
	quiet, plain, noColor, verbose bool
	failOn                         string

	configPath, url, token string
	name, snapshot         string
	actions                cli.RepeatFlag
	force                  bool // snapshot: save even when every action failed

	seal, observe bool
	listen, smtp  string
	proxy         string
	mitm          bool
	allow         cli.ListFlag

	channel, runID, to string
	since              time.Duration // ls and watch: also the messages from this long ago

	// check, headers and replay
	fromMail, provider, selector, returnPath, smtpHost, resolver string
	mtaSTSFetch, withDNS, hops, list                             bool
	sets, headers                                                cli.RepeatFlag
	secretEnv, scheme, typ                                       string
	wait                                                         time.Duration
}

func flagSet(o *options) *flag.FlagSet {
	fs := flag.NewFlagSet("echo", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	both := func(short, long string, f func(string)) { f(short); f(long) }
	fs.BoolVar(&o.json, "json", false, "")
	fs.BoolVar(&o.md, "md", false, "")
	fs.BoolVar(&o.junit, "junit", false, "")
	fs.BoolVar(&o.html, "html", false, "")
	both("o", "output", func(n string) { fs.StringVar(&o.output, n, "", "") })
	fs.BoolVar(&o.open, "open", false, "")
	both("q", "quiet", func(n string) { fs.BoolVar(&o.quiet, n, false, "") })
	fs.BoolVar(&o.plain, "plain", false, "")
	fs.BoolVar(&o.noColor, "no-color", false, "")
	both("v", "verbose", func(n string) { fs.BoolVar(&o.verbose, n, false, "") })
	fs.StringVar(&o.failOn, "fail-on", "", "")

	fs.StringVar(&o.configPath, "config", "", "")
	fs.StringVar(&o.url, "url", "", "")
	fs.StringVar(&o.token, "token", "", "")
	fs.StringVar(&o.name, "name", "", "")
	fs.StringVar(&o.snapshot, "snapshot", "", "")
	fs.Var(&o.actions, "action", "")
	fs.BoolVar(&o.force, "force", false, "")

	fs.BoolVar(&o.seal, "seal", false, "")
	fs.BoolVar(&o.observe, "observe", false, "")
	fs.StringVar(&o.listen, "listen", "", "")
	fs.StringVar(&o.smtp, "smtp", "", "")
	fs.StringVar(&o.proxy, "proxy", "", "")
	fs.BoolVar(&o.mitm, "mitm", false, "")
	fs.Var(&o.allow, "allow", "")

	fs.StringVar(&o.channel, "channel", "", "")
	fs.StringVar(&o.runID, "run", "", "")
	fs.StringVar(&o.to, "to", "", "")
	fs.DurationVar(&o.since, "since", 0, "")

	fs.StringVar(&o.fromMail, "from-mail", "", "")
	fs.StringVar(&o.provider, "provider", "", "")
	fs.StringVar(&o.selector, "selector", "", "")
	fs.StringVar(&o.returnPath, "return-path", "", "")
	fs.StringVar(&o.smtpHost, "smtp-host", "", "")
	fs.StringVar(&o.resolver, "resolver", "", "")
	fs.BoolVar(&o.mtaSTSFetch, "mta-sts-fetch", false, "")
	fs.BoolVar(&o.withDNS, "check", false, "")
	fs.BoolVar(&o.hops, "hops", false, "")
	fs.BoolVar(&o.list, "list", false, "")
	fs.Var(&o.sets, "set", "")
	both("H", "header", func(n string) { fs.Var(&o.headers, n, "") })
	fs.StringVar(&o.secretEnv, "secret-env", "", "")
	fs.StringVar(&o.scheme, "scheme", "", "")
	fs.StringVar(&o.typ, "type", "", "")
	fs.DurationVar(&o.wait, "wait", 5*time.Second, "")
	return fs
}

// plan is a command's validated settings.
type plan struct {
	o      options
	args   []string
	cfg    config.Config
	format string
	st     store.Store
	echo   *client.Client
}

func parse(args []string) (*plan, error) {
	p := &plan{}
	o := &p.o
	fs := flagSet(o)
	pos, err := cli.ParseInterspersed(fs, args)
	if err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return nil, err
		}
		return nil, cli.UsageError{Msg: err.Error()}
	}
	p.args = pos
	path := o.configPath
	if path == "" {
		path = config.Path()
	} else if _, err := os.Stat(path); err != nil {
		return nil, cli.Usagef("--config %s: %v", path, err)
	}
	if p.cfg, err = config.Load(path); err != nil {
		return nil, cli.UsageError{Msg: err.Error()}
	}
	switch {
	case o.seal && o.observe:
		return nil, cli.Usagef("pick --seal or --observe, not both")
	case o.seal:
		p.cfg.Mode = "seal"
	case o.observe:
		p.cfg.Mode = "observe"
	}
	if o.listen != "" {
		p.cfg.Listen = o.listen
	}
	if o.smtp != "" {
		p.cfg.SMTP = o.smtp
	}
	if o.proxy != "" {
		p.cfg.Proxy = o.proxy
	}
	if o.mitm {
		p.cfg.MITM = true
	}
	p.cfg.Allow = append(p.cfg.Allow, o.allow...)

	var chosen []string
	for name, on := range map[string]bool{"json": o.json, "md": o.md, "junit": o.junit, "html": o.html} {
		if on {
			chosen = append(chosen, name)
		}
	}
	switch {
	case len(chosen) > 1:
		slices.Sort(chosen)
		return nil, cli.Usagef("pick one output format, not --%s", strings.Join(chosen, " and --"))
	case len(chosen) == 1:
		p.format = chosen[0]
	case o.output != "":
		if p.format = formatFromExt(o.output); p.format == "" {
			return nil, cli.Usagef("can't tell the format of %s; add --json, --md, --junit or --html", o.output)
		}
	}
	if o.channel != "" && !slices.Contains(channels, model.Channel(o.channel)) {
		return nil, cli.Usagef("--channel %q: use one of %s", o.channel, joinChannels())
	}
	if o.since < 0 {
		return nil, cli.Usagef("--since %s: give how long ago, e.g. 10m", o.since)
	}
	if o.name != "" && !store.ValidName(o.name) {
		return nil, cli.Usagef("--name %q: use letters, digits, dots, dashes and underscores", o.name)
	}
	p.st = store.Store{Root: config.Dir}
	url := cmp.Or(o.url, os.Getenv("RHYTHM_ECHO_URL"), "http://"+p.cfg.Listen)
	p.echo = client.New(url, cmp.Or(o.token, os.Getenv("RHYTHM_ECHO_TOKEN")))
	return p, nil
}

// channels are the values --channel accepts.
var channels = []model.Channel{model.Email, model.SMS, model.WhatsApp, model.Webhook, model.Chat, model.Analytics,
	model.Payment, model.Push, model.HTTP}

func joinChannels() string {
	var names []string
	for _, c := range channels {
		names = append(names, string(c))
	}
	return strings.Join(names, ", ")
}

func formatFromExt(path string) string {
	switch strings.ToLower(filepath.Ext(path)) {
	case ".json":
		return "json"
	case ".md", ".markdown":
		return "md"
	case ".xml":
		return "junit"
	case ".html", ".htm":
		return "html"
	}
	return ""
}

// tokenFlagWarning is printed when --token is given on the command line,
// where any local user can read it with ps. The environment variable is
// only visible to the same user (and root).
const tokenFlagWarning = "rhythm echo: --token shows in ps to every user on this machine; set RHYTHM_ECHO_TOKEN instead" //nolint:gosec // a message about tokens, not one

// warnTokenFlag prints tokenFlagWarning when the token came from the flag.
func warnTokenFlag(stderr io.Writer, token string, quiet bool) {
	if token != "" && !quiet {
		fmt.Fprintln(stderr, tokenFlagWarning)
	}
}

func usageErr(format string, a ...any) error { return cli.UsageError{Msg: fmt.Sprintf(format, a...)} }
