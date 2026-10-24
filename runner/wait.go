// Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
// SPDX-License-Identifier: MIT

package runner

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/RhythmShahriar/echo/check"
	"github.com/RhythmShahriar/echo/client"
	"github.com/RhythmShahriar/echo/config"
	"github.com/RhythmShahriar/echo/model"
	"github.com/RhythmShahriar/echo/wire"
)

// ErrWaits matches the error Run returns when Options.Quiet or
// Options.Timeout asks for a wait Echo can't honor. They're the user's
// to fix, like a bad flag.
var ErrWaits = errors.New("echo can't wait like that")

// waitError is an ErrWaits that reads as its own sentence.
type waitError string

func (e waitError) Error() string        { return string(e) }
func (e waitError) Is(target error) bool { return target == ErrWaits }

// settle is how long an action whose expect is met waits for anything
// more. Without expect Echo can't know the site is done, so it waits
// the whole quiet window; with it, the window only has to catch a
// message sent right behind the last expected one.
const settle = 200 * time.Millisecond

// waitGrace is how much longer than the timeout the runner gives Echo's
// wait request to answer.
const waitGrace = 2 * time.Second

// waits returns the config with Options.Quiet and Options.Timeout
// applied and checked against the limits Load applies to the file.
func (o Options) waits() (config.Config, error) {
	cfg := withWaits(o.Config)
	if o.Timeout > 0 {
		cfg.Timeout = config.Duration{Duration: o.Timeout}
	}
	if o.Quiet > 0 {
		cfg.Quiet = config.Duration{Duration: o.Quiet}
	}
	t, q := cfg.Timeout.Duration, cfg.Quiet.Duration
	switch {
	case t > config.MaxTimeout:
		return cfg, waitError(fmt.Sprintf("timeout %s is over the %s Echo waits for one action at most", short(t), short(config.MaxTimeout)))
	case q > config.MaxQuiet:
		return cfg, waitError(fmt.Sprintf("quiet %s is over the %s Echo waits for quiet at most", short(q), short(config.MaxQuiet)))
	case q > t:
		return cfg, waitError(fmt.Sprintf("quiet %s is longer than the timeout of %s, so no action could ever fall quiet", short(q), short(t)))
	}
	// An action's own quiet passed Load against the file's timeout; a
	// shorter Options.Timeout can still undercut it.
	if o.Quiet <= 0 {
		for _, a := range cfg.Actions {
			if a.Quiet != nil && a.Quiet.Duration > t {
				return cfg, waitError(fmt.Sprintf("action %q: quiet %s is longer than the timeout of %s, so it could never fall quiet", a.Name, short(a.Quiet.Duration), short(t)))
			}
		}
	}
	return cfg, nil
}

// quietFor is how long a must stay quiet: Options.Quiet, else the
// action's own quiet, else the config's. o.Config has been through
// waits, so the global value is set.
func (o Options) quietFor(a config.Action) time.Duration {
	switch {
	case o.Quiet > 0:
		return o.Quiet
	case a.Quiet != nil && a.Quiet.Duration > 0:
		return a.Quiet.Duration
	}
	return o.Config.Quiet.Duration
}

// short writes a duration the way a config would: 1m, not 1m0s.
func short(d time.Duration) string {
	s := d.String()
	if strings.HasSuffix(s, "m0s") {
		s = strings.TrimSuffix(s, "0s")
	}
	return s
}

// wait returns the messages an action caused. Without expect it waits
// for quiet. With expect it finishes as soon as every channel has what
// it expects and nothing new arrived for settle (or quiet, when that is
// shorter). Echo's wait counts messages of any channel, so a webhook
// can reach the count before the expected email: then it waits again
// for one more message, until the expectation is met or time runs out.
// Echo answers with fewer than asked only when its timeout passed.
func wait(ctx context.Context, c *client.Client, f client.Filter, e config.Expect, quiet, timeout time.Duration) ([]model.Message, error) {
	want := e.Total()
	if want == 0 {
		return c.Wait(ctx, f, 0, quiet, timeout)
	}
	fast := min(quiet, settle)
	deadline := time.Now().Add(timeout)
	for {
		msgs, err := c.Wait(ctx, f, want, fast, time.Until(deadline))
		if err != nil || len(msgs) < want || len(expectations(e, msgs)) == 0 || time.Until(deadline) <= fast || ctx.Err() != nil {
			return msgs, err
		}
		want = len(msgs) + 1
	}
}

// listed is a message as Echo's list returns it, with the fields catcher
// C9 adds: a message with no run that arrived during a later action of
// its run, although its Date says it was written before that action
// began, is marked late for the action it belongs to. model.Message has
// no such fields until C9 merges; an Echo without it never sets them.
type listed struct {
	model.Message
	Attribution string `json:"attribution"`
	LateFor     string `json:"late_for"`
}

// late is the attribution catcher C9 gives such a message.
const late = "late"

// runMessages returns every message Echo credited to run, oldest first.
// It reads the list itself for the reason listed gives.
func runMessages(ctx context.Context, c *client.Client, run string) ([]listed, error) {
	q := url.Values{"run": {run}}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.URL+"/v1/messages?"+q.Encode(), nil)
	if err != nil {
		return nil, err
	}
	if c.Token != "" {
		req.Header.Set(wire.TokenHeader, c.Token)
	}
	hc := c.HTTP
	if hc == nil {
		hc = http.DefaultClient
	}
	resp, err := hc.Do(req)
	if err != nil {
		return nil, err
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 512))
		return nil, fmt.Errorf("GET /v1/messages: %s", resp.Status)
	}
	var msgs []listed
	return msgs, json.NewDecoder(resp.Body).Decode(&msgs)
}

// belongsTo is the action m was caused by: the one it names (the site
// tagged it, so it's certain), or the one Echo marked it late for.
func (m listed) belongsTo() string {
	if m.Attribution == late {
		return m.LateFor
	}
	return m.Action
}

// creditLate gives an action the mail it caused that arrived after its
// wait was over, sent by a queue or cron: mail the site tagged with the
// action and untagged mail Echo marked late for it. Without this the
// first kind was lost and the second went to the next action. Each one
// gets a run warning saying how late it was; the run gets one
// suggestion to make such senders send in time. The action's
// expectations are checked again with the mail it got back. Not knowing
// only costs the credit.
func creditLate(ctx context.Context, o Options, run *model.Run, actions []config.Action, ended map[string]time.Time, opts check.Options) {
	msgs, err := runMessages(ctx, o.Client, run.ID)
	if err != nil || len(msgs) == 0 {
		return
	}
	seen := map[string]bool{}
	for _, a := range run.Actions {
		for _, m := range a.Messages {
			seen[m.ID] = true
		}
	}
	expect := map[string]config.Expect{}
	for _, a := range actions {
		expect[ActionID(a.Name)] = a.Expect
	}
	credited := false
	for _, lm := range msgs {
		id := lm.belongsTo()
		if id == "" || seen[lm.ID] {
			continue
		}
		act := run.Action(id)
		if act == nil {
			continue
		}
		seen[lm.ID] = true
		m := lm.Message
		m.Action = act.ID // what it would have had, had it come in time
		act.Messages = append(act.Messages, m)
		act.Issues = append(act.Issues, check.Message(m, opts)...)
		run.Warnings = append(run.Warnings, fmt.Sprintf("%s: %s arrived %s after the action ended and is counted with it", act.Label, describe(m), after(m.At, ended[act.ID])))
		credited = true
		if e := expect[act.ID]; e.Total() > 0 {
			act.Issues = append(withoutExpectations(act.Issues), expectations(e, act.Messages)...)
		}
	}
	if !credited {
		return
	}
	if len(o.Config.Flush) == 0 {
		run.Warnings = append(run.Warnings, "Some mail arrived after its action ended; add a flush: command to .echo/config.yaml (like wp cron event run --due-now or php artisan queue:work --stop-when-empty) so queued senders send during their action")
	} else {
		run.Warnings = append(run.Warnings, "Some mail arrived after its action ended although the flush: commands ran; check they cover every queue the site uses, or raise quiet")
	}
}

// withoutExpectations drops the expect issues, to be worked out again.
func withoutExpectations(issues []model.Issue) []model.Issue {
	var out []model.Issue
	for _, i := range issues {
		if !strings.HasPrefix(i.Check, "expect/") {
			out = append(out, i)
		}
	}
	return out
}

// describe names a message in a warning: its channel and subject, event
// or URL.
func describe(m model.Message) string {
	what := string(m.Channel)
	if what == "" {
		what = "message"
	}
	for _, s := range []string{m.Subject, m.Event, m.URL} {
		if s != "" {
			return fmt.Sprintf("%s %q", what, s)
		}
	}
	return what
}

// after says how long after end a message arrived, like "1.2 s". Echo
// and the runner can sit on clocks that disagree a little; a message
// that seems to have come first still came after the action ended.
func after(at, end time.Time) string {
	d := at.Sub(end)
	if end.IsZero() || at.IsZero() || d < 0 {
		d = 0
	}
	return fmt.Sprintf("%.1f s", d.Seconds())
}
