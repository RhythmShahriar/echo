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
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"regexp"
	"strings"
	"time"

	"github.com/RhythmShahriar/echo/check"
	"github.com/RhythmShahriar/echo/client"
	"github.com/RhythmShahriar/echo/config"
	"github.com/RhythmShahriar/echo/model"
	"github.com/RhythmShahriar/echo/store"
	"github.com/RhythmShahriar/echo/wire"
)

// Replayer sends a signed webhook for a replay action (package replay).
type Replayer func(ctx context.Context, a config.ReplayAction, to, run string) error

type Options struct {
	Config  config.Config
	Client  *client.Client
	Version string
	Name    string // snapshot name
	HTTP    *http.Client
	Replay  Replayer
	Only    []string // run only these action names
	Hooks   Hooks
}

// Hooks report progress.
type Hooks struct {
	Action func(a *model.Action, done, total int)
	Log    func(string)
}

// Run runs every action and returns the run.
func Run(ctx context.Context, o Options) (*model.Run, error) {
	start := time.Now()
	h, err := o.Client.Health(ctx)
	if err != nil {
		return nil, err
	}
	cfg := o.Config
	if len(cfg.Actions) == 0 {
		return nil, fmt.Errorf("no actions in %s; add some (rhythm echo init writes examples), or use Soundcheck with --echo", config.Path())
	}
	run := &model.Run{Schema: model.Schema, Tool: "rhythm-echo", Version: o.Version, ID: store.Stamp(start) + "-" + token(3),
		Target: cfg.Site, Name: o.Name, Mode: h.Mode, Started: start.UTC()}
	if o.HTTP == nil {
		o.HTTP = &http.Client{Timeout: cfg.Timeout.Duration, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	}
	fake := newFake()
	opts := check.Options{SiteHost: host(cfg.Site), AllowRecipients: cfg.AllowRecipients}
	actions := cfg.Actions
	if len(o.Only) > 0 {
		actions = nil
		for _, a := range cfg.Actions {
			for _, n := range o.Only {
				if strings.EqualFold(a.Name, n) {
					actions = append(actions, a)
				}
			}
		}
		if len(actions) == 0 {
			return nil, fmt.Errorf("no action named %s", strings.Join(o.Only, ", "))
		}
	}
	for i, a := range actions {
		if ctx.Err() != nil {
			break
		}
		act := runAction(ctx, o, run.ID, a, fake, opts)
		run.Actions = append(run.Actions, act)
		if o.Hooks.Action != nil {
			o.Hooks.Action(&run.Actions[len(run.Actions)-1], i+1, len(actions))
		}
	}
	run.Duration = float64(time.Since(start).Milliseconds())
	return run, ctx.Err()
}

// ActionID is the stable ID compare matches actions on.
func ActionID(name string) string {
	return "action:" + strings.Trim(nonWord.ReplaceAllString(strings.ToLower(name), "-"), "-")
}

var nonWord = regexp.MustCompile(`[^a-z0-9]+`)

func runAction(ctx context.Context, o Options, runID string, a config.Action, fake *fakeData, opts check.Options) model.Action {
	cfg := o.Config
	act := model.Action{ID: ActionID(a.Name), Label: a.Name}
	start := time.Now()
	runValue := client.RunValue(runID, act.ID)
	_ = o.Client.Begin(ctx, runID, act.ID)
	defer func() { _ = o.Client.End(context.WithoutCancel(ctx)) }()

	var err error
	switch {
	case a.HTTP != nil:
		act.Kind = "http"
		err = doHTTP(ctx, o, a, runValue, fake)
	case a.Command != "":
		act.Kind = "command"
		err = command(ctx, fake.expand(a.Command), runValue, a.Env)
	case a.Replay != nil:
		act.Kind = "replay"
		if o.Replay == nil {
			err = fmt.Errorf("replay isn't available in this build")
		} else {
			err = o.Replay(ctx, *a.Replay, resolve(cfg.Site, a.Replay.To), runValue)
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
	want := 0
	if a.Expect != nil {
		want = a.Expect.Email + a.Expect.SMS + a.Expect.Webhook + a.Expect.Chat
	}
	wctx, cancel := context.WithTimeout(ctx, cfg.Timeout.Duration)
	msgs, werr := o.Client.Wait(wctx, client.Filter{Run: runID, Action: act.ID}, want, cfg.Quiet.Duration, cfg.Timeout.Duration)
	cancel()
	if werr != nil && act.Error == "" {
		act.Error = werr.Error()
	}
	act.Messages = msgs
	for _, m := range msgs {
		act.Issues = append(act.Issues, check.Message(m, opts)...)
	}
	act.Issues = append(act.Issues, expectations(a.Expect, msgs)...)
	act.Duration = float64(time.Since(start).Milliseconds())
	return act
}

// expectations reports an action that sent fewer messages than its
// config says it must.
func expectations(e *config.Expect, msgs []model.Message) []model.Issue {
	if e == nil {
		return nil
	}
	count := map[model.Channel]int{}
	for _, m := range msgs {
		count[m.Channel]++
	}
	var out []model.Issue
	for _, w := range []struct {
		ch   model.Channel
		n    int
		noun string
	}{{model.Email, e.Email, "emails"}, {model.SMS, e.SMS, "SMS"}, {model.Webhook, e.Webhook, "webhooks"}, {model.Chat, e.Chat, "chat messages"}} {
		if w.n > 0 && count[w.ch] < w.n {
			i := model.Issue{Check: "expect/" + string(w.ch), Level: model.Error, Title: fmt.Sprintf("Sent fewer %s than expected", w.noun),
				Detail: fmt.Sprintf("expected %d, got %d", w.n, count[w.ch]),
				Fix:    "Check the settings of whatever sends them, or update expect in .echo/config.yaml if the change is intended."}
			i.Identify()
			out = append(out, i)
		}
	}
	return out
}

func doHTTP(ctx context.Context, o Options, a config.Action, runValue string, fake *fakeData) error {
	h := a.HTTP
	target := resolve(o.Config.Site, fake.expand(h.URL))
	if o.Config.IsProduction(target) {
		return fmt.Errorf("refusing to run against %s: it's listed under production", target)
	}
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
		b, err := json.Marshal(h.JSON)
		if err != nil {
			return err
		}
		body, ctype = bytes.NewReader([]byte(fake.expand(string(b)))), "application/json"
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

// command runs cmd with sh -c and RHYTHM_ECHO_RUN set, so connectors in
// CLI processes (wp-cli, artisan, scripts) tag what they send.
func command(ctx context.Context, cmd, runValue string, env map[string]string) error {
	c := exec.CommandContext(ctx, "sh", "-c", cmd) //nolint:gosec // the user's own configured command
	c.Env = append(os.Environ(), "RHYTHM_ECHO_RUN="+runValue)
	for k, v := range env {
		c.Env = append(c.Env, k+"="+v)
	}
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

func token(n int) string {
	b := make([]byte, n)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}
