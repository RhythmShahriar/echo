// Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
// SPDX-License-Identifier: MIT

package runner

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"time"

	"github.com/RhythmShahriar/echo/client"
	"github.com/RhythmShahriar/echo/wire"
)

// leaser is the lease API a client from catcher C5 on has: BeginLease
// returns the lease that EndLease takes, so ending one run's action never
// closes another run's. The runner asks for it with a type assertion so
// it builds against a client without it, where it falls back to the
// global Begin and End.
type leaser interface {
	BeginLease(ctx context.Context, run, action string) (string, error)
	EndLease(ctx context.Context, lease string) error
}

// beginner is the global begin and end every client has.
type beginner interface {
	Begin(ctx context.Context, run, action string) error
	End(ctx context.Context) error
}

// exclusiver is the exclusive lease a client from catcher C15 on has:
// Echo waits until no other action (of any run) is open, then keeps new
// ones out until it ends, so untagged mail can only be this action's.
type exclusiver interface {
	BeginExclusive(ctx context.Context, run, action string) (string, error)
}

// begin tells Echo that run/action is in progress and returns the func
// that ends exactly that action. With exclusive, it asks for an
// exclusive lease when the client has one; without, a shared one. Errors
// are ignored, as before: an Echo that can't attribute untagged mail
// still catches tagged mail.
func begin(ctx context.Context, c beginner, run, action string, exclusive bool) (end func()) {
	l, leases := c.(leaser)
	if x, ok := c.(exclusiver); ok && exclusive && leases {
		lease, _ := x.BeginExclusive(ctx, run, action)
		return func() { _ = l.EndLease(context.WithoutCancel(ctx), lease) }
	}
	if leases {
		lease, _ := l.BeginLease(ctx, run, action)
		return func() { _ = l.EndLease(context.WithoutCancel(ctx), lease) }
	}
	_ = c.Begin(ctx, run, action)
	return func() { _ = c.End(context.WithoutCancel(ctx)) }
}

// ambiguous is the attribution Echo gives a message that names no run
// while several actions are open (catcher C5): it can't tell whose it is,
// so it credits nobody.
const ambiguous = "ambiguous"

// ambiguousSince returns the IDs of messages Echo marked ambiguous since
// t. It reads the list itself because model.Message has no Attribution
// field until catcher C5 merges; an Echo without leases never sets one,
// so it finds none.
func ambiguousSince(ctx context.Context, c *client.Client, t time.Time) ([]string, error) {
	q := url.Values{"since": {t.UTC().Format(time.RFC3339Nano)}}
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
	var msgs []struct {
		ID          string `json:"id"`
		Attribution string `json:"attribution"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&msgs); err != nil {
		return nil, err
	}
	var ids []string
	for _, m := range msgs {
		if m.Attribution == ambiguous {
			ids = append(ids, m.ID)
		}
	}
	return ids, nil
}

// ambiguityWarning is the run warning for n messages no action could be
// credited with, or "" for none. overlapped says this run had actions
// open at once itself, which is then the likelier cause.
func ambiguityWarning(n int, overlapped bool) string {
	if n == 0 {
		return ""
	}
	what := "1 message"
	if n > 1 {
		what = fmt.Sprintf("%d messages", n)
	}
	if overlapped {
		return what + " could not be tied to an action because several actions were open at once; set parallel: false on actions whose mail a connector doesn't tag"
	}
	return what + " could not be tied to an action because another run was open"
}
