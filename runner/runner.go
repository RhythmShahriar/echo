// Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
// SPDX-License-Identifier: MIT

// Package runner runs a project's actions against a running Echo: for
// each one it triggers the site (an HTTP request, a command, a replayed
// webhook), lets queued senders catch up, waits until the site falls
// quiet, and records the messages that action caused, with their issues.
package runner

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"maps"
	"net/http"
	"net/url"
	"os"
	"regexp"
	"slices"
	"strings"
	"time"

	"github.com/RhythmShahriar/echo/catcher"
	"github.com/RhythmShahriar/echo/check"
	"github.com/RhythmShahriar/echo/client"
	"github.com/RhythmShahriar/echo/config"
	"github.com/RhythmShahriar/echo/model"
	"github.com/RhythmShahriar/echo/store"
	"github.com/RhythmShahriar/echo/wire"
)

// Replayer sends a signed webhook for a replay action (package replay).
// run is the unsigned "<run>/<action>"; the replayer signs X-Echo-Run
// with Echo's token itself (replay.Options.Token).
type Replayer func(ctx context.Context, a config.ReplayAction, to, run string) error

// Options says what Run runs and against which Echo.
type Options struct {
	Config  config.Config
	Client  *client.Client
	Version string
	Name    string // snapshot name
	HTTP    *http.Client
	Replay  Replayer
	Only    []string // run only these action names
	Hooks   Hooks
	// Quiet and Timeout override the config's waits when more than 0
	// (the CLI's --quiet and --timeout). Quiet replaces every action's
	// quiet, its own included. Run refuses values Echo can't honor with
	// an error that matches ErrWaits.
	Quiet   time.Duration
	Timeout time.Duration
	// Parallel runs up to this many actions at once (compare --parallel)
	// once the site is seen tagging its mail; 0 or 1 runs them one by
	// one. See runAll.
	Parallel int
}

// Hooks report progress.
type Hooks struct {
	Action func(a *model.Action, done, total int)
	Log    func(string)
}

// Errors from Select: the configuration or the command line asks for
// something that isn't there, so they're the user's to fix.
var (
	ErrNoActions     = errors.New("no actions")
	ErrUnknownAction = errors.New("no action")
)

// ErrProduction means the config's site is a live site.
var ErrProduction = errors.New("refusing to run")

// Select returns the configured actions to run: all of them, or those
// named in only (case-insensitive, in the config's order). Every name in
// only must match an action.
func Select(cfg config.Config, only []string) ([]config.Action, error) {
	if len(cfg.Actions) == 0 {
		return nil, fmt.Errorf("%w in %s; add some (rhythm echo init writes examples), or use Soundcheck with --echo", ErrNoActions, config.Path())
	}
	if len(only) == 0 {
		return cfg.Actions, nil
	}
	var names []string
	for _, a := range cfg.Actions {
		names = append(names, a.Name)
	}
	for _, n := range only {
		if !slices.ContainsFunc(names, func(a string) bool { return strings.EqualFold(a, n) }) {
			return nil, fmt.Errorf("%w named %q; the actions are: %s", ErrUnknownAction, n, strings.Join(names, ", "))
		}
	}
	var out []config.Action
	for _, a := range cfg.Actions {
		if slices.ContainsFunc(only, func(n string) bool { return strings.EqualFold(a.Name, n) }) {
			out = append(out, a)
		}
	}
	return out, nil
}

// Run runs every action and returns the run. A site listed under
// production is refused (ErrProduction) before anything is sent.
func Run(ctx context.Context, o Options) (*model.Run, error) {
	start := time.Now()
	cfg, err := o.waits()
	if err != nil {
		return nil, err
	}
	o.Config = cfg
	if r := cfg.ProductionReason(cfg.Site); cfg.Site != "" && r != "" {
		return nil, fmt.Errorf("%w against %s: %s; actions only run against staging and local sites", ErrProduction, cfg.Site, r)
	}
	h, err := o.Client.Health(ctx)
	if err != nil {
		return nil, err
	}
	actions, err := Select(cfg, o.Only)
	if err != nil {
		return nil, err
	}
	run := &model.Run{Schema: model.Schema, Tool: "rhythm-echo", Version: o.Version, ID: store.Stamp(start) + "-" + token(3),
		Target: cfg.Site, Name: o.Name, Mode: h.Mode, Started: start.UTC()}
	if o.HTTP == nil {
		o.HTTP = &http.Client{Timeout: cfg.Timeout.Duration, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	}
	fake := newFake()
	opts := check.Options{SiteHost: host(cfg.Site), AllowRecipients: cfg.AllowRecipients}
	done := runAll(ctx, o, run, actions, fake, opts)
	if w := ambiguityWarning(len(done.lost), done.overlapped); w != "" {
		run.Warnings = append(run.Warnings, w)
	}
	if ctx.Err() == nil {
		creditLate(ctx, o, run, actions, done.ended, opts)
	}
	run.Duration = float64(time.Since(start).Milliseconds())
	return run, ctx.Err()
}

// withWaits fills a timeout or quiet that isn't more than 0 with the
// default. Load refuses such values but a Config built in code skips
// Load. With a timeout of 0 the HTTP client would wait forever.
func withWaits(cfg config.Config) config.Config {
	d := config.Default()
	if cfg.Timeout.Duration <= 0 {
		cfg.Timeout = d.Timeout
	}
	if cfg.Quiet.Duration <= 0 {
		cfg.Quiet = d.Quiet
	}
	return cfg
}

// ActionID is the stable ID compare matches actions on. config.Validate
// makes sure every action's is unique and not empty.
func ActionID(name string) string { return config.ActionID(name) }

// runAction runs a and returns it, with the IDs of messages that arrived
// while it was open but that Echo couldn't credit to it, because another
// action was open too. An exclusive action asks Echo for a lease no
// other action may share (see begin).
func runAction(ctx context.Context, o Options, run *model.Run, a config.Action, fake *fakeData, opts check.Options, exclusive bool) (model.Action, []string) {
	cfg := o.Config
	runID := run.ID
	quiet := o.quietFor(a)
	act := model.Action{ID: ActionID(a.Name), Label: a.Name, Kind: kind(a)}
	start := time.Now()
	if err := refuse(cfg, a, run.Mode, fake); err != nil {
		act.Error = err.Error()
		return act, nil
	}
	// HTTP requests and commands carry the run signed with Echo's token,
	// so a connector can trust it. The replayer gets it unsigned, since it
	// takes the run and action apart.
	runValue := signRun(o.Client.Token, runID, act.ID)
	end := begin(ctx, o.Client, runID, act.ID, exclusive)
	defer end()

	var err error
	switch {
	case a.HTTP != nil:
		err = doHTTP(ctx, o, a, runValue, fake)
	case a.Command != "":
		err = command(ctx, fake.expand(a.Command), runValue, a.Env)
	case a.Replay != nil:
		if o.Replay == nil {
			err = fmt.Errorf("replay isn't available in this build")
		} else {
			err = o.Replay(ctx, *a.Replay, resolve(cfg.Site, a.Replay.To), client.RunValue(runID, act.ID))
		}
	}
	if err != nil {
		act.Error = err.Error()
	}
	for _, f := range cfg.Flush {
		if ferr := command(ctx, f, runValue, nil); ferr != nil && o.Hooks.Log != nil {
			o.Hooks.Log(fmt.Sprintf("flush %q: %v", f, ferr))
		}
	}
	// Echo answers with what it has once the timeout passes; the grace
	// lets that answer arrive instead of cutting the request off first.
	wctx, cancel := context.WithTimeout(ctx, cfg.Timeout.Duration+waitGrace)
	msgs, werr := wait(wctx, o.Client, client.Filter{Run: runID, Action: act.ID}, a.Expect, quiet, cfg.Timeout.Duration)
	cancel()
	if werr != nil && act.Error == "" {
		act.Error = werr.Error()
	}
	// Asked before end() so the window is exactly while this action was
	// open. Not knowing only costs the warning.
	unsure, _ := ambiguousSince(ctx, o.Client, start)
	act.Messages = msgs
	for _, m := range msgs {
		act.Issues = append(act.Issues, check.Message(m, opts)...)
	}
	act.Issues = append(act.Issues, expectations(a.Expect, msgs)...)
	act.Duration = float64(time.Since(start).Milliseconds())
	return act, unsure
}

func kind(a config.Action) string {
	switch {
	case a.HTTP != nil:
		return "http"
	case a.Command != "":
		return "command"
	case a.Replay != nil:
		return "replay"
	}
	return ""
}

// refuse stops an action that must not run, before anything is sent: one
// aimed at a site listed under production, and a replay outside seal
// mode, which would let the site's reactions (order emails, SMS, CRM
// calls) reach real people. Replays are refused the way `rhythm echo
// replay` refuses them.
func refuse(cfg config.Config, a config.Action, mode string, fake *fakeData) error {
	target := ""
	switch {
	case a.HTTP != nil:
		target = resolve(cfg.Site, fake.expand(a.HTTP.URL))
	case a.Replay != nil:
		target = resolve(cfg.Site, a.Replay.To)
	}
	if r := cfg.ProductionReason(target); target != "" && r != "" {
		return fmt.Errorf("refusing to run against %s: %s", target, r)
	}
	if a.Replay != nil && mode != catcher.Seal {
		return fmt.Errorf("Echo is in %s mode; replays only run sealed, so the site's reactions can't reach anyone (rhythm echo up --seal)", mode) //nolint:staticcheck // "Echo" is a name, so it stays capitalized
	}
	return nil
}

// expectations reports an action that sent fewer messages than its
// config says it must: one expect/unmet issue per channel short of its
// count, and per event short of its count (analytics: {purchase: 1}),
// matched on the message's event. A shortfall is named in the title so
// compare tells a missing purchase event from a missing email.
func expectations(e config.Expect, msgs []model.Message) []model.Issue {
	count := map[model.Channel]int{}
	events := map[model.Channel]map[string]int{}
	for _, m := range msgs {
		count[m.Channel]++
		if events[m.Channel] == nil {
			events[m.Channel] = map[string]int{}
		}
		events[m.Channel][m.Event]++
	}
	var out []model.Issue
	unmet := func(title, what string, want, got int) {
		i := model.Issue{Check: "expect/unmet", Level: model.Error, Title: title,
			Detail: fmt.Sprintf("%s: expected %d, got %d", what, want, got),
			Fix:    "Check the settings of whatever sends them, or update expect in .echo/config.yaml if the change is intended."}
		i.Identify()
		out = append(out, i)
	}
	for _, ch := range model.Channels {
		c := e[ch]
		if c.N > 0 && count[ch] < c.N {
			unmet(fmt.Sprintf("Sent fewer %s than expected", noun(ch)), string(ch), c.N, count[ch])
		}
		for _, ev := range slices.Sorted(maps.Keys(c.Events)) {
			if n, got := c.Events[ev], events[ch][ev]; n > 0 && got < n {
				unmet(fmt.Sprintf("Sent fewer %q %s than expected", ev, noun(ch)), string(ch)+" "+ev, n, got)
			}
		}
	}
	return out
}

// crm is the channel the roadmap adds for CRM API calls. model has no
// constant for it yet, so it is spelled here to name its messages once
// model.Channels lists it.
const crm model.Channel = "crm"

// noun names a channel's messages in a sentence.
func noun(ch model.Channel) string {
	switch ch {
	case model.Email:
		return "emails"
	case model.SMS:
		return "SMS"
	case model.WhatsApp:
		return "WhatsApp messages"
	case model.Chat:
		return "chat messages"
	case model.Webhook:
		return "webhooks"
	case model.Analytics:
		return "analytics events"
	case model.Payment:
		return "payment calls"
	case model.Push:
		return "push notifications"
	case crm:
		return "CRM calls"
	}
	return string(ch) + " messages"
}

func doHTTP(ctx context.Context, o Options, a config.Action, runValue string, fake *fakeData) error {
	h := a.HTTP
	target := resolve(o.Config.Site, fake.expand(h.URL)) // refuse checked it isn't production
	method := strings.ToUpper(h.Method)
	if method == "" {
		method = http.MethodGet
		if h.Form != nil || h.JSON != nil || h.Body != "" {
			method = http.MethodPost
		}
	}
	var body io.Reader
	ctype := ""
	switch {
	case h.Form != nil:
		v := url.Values{}
		for k, val := range h.Form {
			v.Set(k, fake.expand(val))
		}
		body, ctype = strings.NewReader(v.Encode()), "application/x-www-form-urlencoded"
	case h.JSON != nil:
		b, err := json.Marshal(fake.expandJSON(h.JSON))
		if err != nil {
			return err
		}
		body, ctype = bytes.NewReader(b), "application/json"
	case h.Body != "":
		body = strings.NewReader(fake.expand(h.Body))
	}
	req, err := http.NewRequestWithContext(ctx, method, target, body)
	if err != nil {
		return err
	}
	if ctype != "" {
		req.Header.Set("Content-Type", ctype)
	}
	for k, v := range h.Headers {
		req.Header.Set(k, fake.expand(v))
	}
	req.Header.Set(wire.RunHeader, runValue)
	req.AddCookie(&http.Cookie{Name: wire.RunHeader, Value: url.QueryEscape(runValue)}) //nolint:gosec // a request cookie we send, not one we set in a browser
	resp, err := o.HTTP.Do(req)
	if err != nil {
		return err
	}
	defer func() { _ = resp.Body.Close() }()
	_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 1<<20))
	if resp.StatusCode >= 400 {
		return fmt.Errorf("%s %s returned %s", method, target, resp.Status)
	}
	return nil
}

// command runs cmd in the shell (see Shell) with RHYTHM_ECHO_RUN set to
// runValue, signed like the header, so connectors in CLI processes
// (wp-cli, artisan, scripts) tag what they send and a script may pass it
// on to the site as X-Echo-Run.
func command(ctx context.Context, cmd, runValue string, env map[string]string) error {
	vars := []string{runEnv + "=" + runValue}
	for k, v := range env {
		vars = append(vars, k+"="+v)
	}
	c := Shell(ctx, cmd, vars)
	var stderr bytes.Buffer
	c.Stdout, c.Stderr = io.Discard, &stderr
	if err := c.Run(); err != nil {
		msg := strings.TrimSpace(stderr.String())
		if len(msg) > 300 {
			msg = msg[:300] + "…"
		}
		if msg != "" {
			return fmt.Errorf("%q: %w: %s", cmd, err, msg)
		}
		return fmt.Errorf("%q: %w", cmd, err)
	}
	return nil
}

func resolve(site, ref string) string {
	if strings.Contains(ref, "://") || site == "" {
		return ref
	}
	base, err := url.Parse(site)
	if err != nil {
		return ref
	}
	r, err := url.Parse(ref)
	if err != nil {
		return ref
	}
	return base.ResolveReference(r).String()
}

func host(site string) string {
	if u, err := url.Parse(site); err == nil {
		return u.Hostname()
	}
	return ""
}

// fakeData fills ${fake:…} in actions with values compare recognizes as
// test data, so they don't count as changes between runs.
type fakeData struct{ tag string }

func newFake() *fakeData { return &fakeData{tag: token(3)} }

var fakeRe = regexp.MustCompile(`\$\{(fake|env):([a-zA-Z0-9_]+)\}`)

func (f *fakeData) expand(s string) string {
	return fakeRe.ReplaceAllStringFunc(s, func(m string) string {
		p := fakeRe.FindStringSubmatch(m)
		if p[1] == "env" {
			return os.Getenv(p[2])
		}
		switch p[2] {
		case "email":
			return "echo-test+" + f.tag + "@example.com"
		case "name":
			return "echo-test " + f.tag
		case "phone":
			return "+1555" + fmt.Sprintf("%07d", int(f.tag[0])*1000+int(f.tag[1]))
		case "tag":
			return f.tag
		}
		return m
	})
}

// expandJSON fills placeholders in the string keys and values of a JSON
// body as YAML decoded it, before it's marshaled: expanding the encoded
// text instead let a value such as O"Brien break the JSON. YAML maps
// with non-string keys come back as map[any]any, which json.Marshal
// refuses, so their keys become strings here.
func (f *fakeData) expandJSON(v any) any {
	switch v := v.(type) {
	case string:
		return f.expand(v)
	case map[string]any:
		out := make(map[string]any, len(v))
		for k, val := range v {
			out[f.expand(k)] = f.expandJSON(val)
		}
		return out
	case map[any]any:
		out := make(map[string]any, len(v))
		for k, val := range v {
			out[f.expand(fmt.Sprint(k))] = f.expandJSON(val)
		}
		return out
	case []any:
		out := make([]any, len(v))
		for i, val := range v {
			out[i] = f.expandJSON(val)
		}
		return out
	}
	return v
}

func token(n int) string {
	b := make([]byte, n)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}
