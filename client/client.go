// Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
// SPDX-License-Identifier: MIT

// Package client talks to a running Echo (`rhythm echo up`) over its HTTP
// API. Soundcheck uses it to tie form submits to the messages they send,
// and Echo's own commands use it to list, wait for and clear messages.
package client

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/RhythmShahriar/echo/model"
	"github.com/RhythmShahriar/echo/wire"
)

// DefaultURL is where `rhythm echo up` listens unless told otherwise.
const DefaultURL = "http://127.0.0.1:8025"

// Client is an Echo API client.
type Client struct {
	URL   string // e.g. http://127.0.0.1:8025
	Token string
	HTTP  *http.Client
}

// New returns a client for base (DefaultURL when empty).
func New(base, token string) *Client {
	if base == "" {
		base = DefaultURL
	}
	return &Client{URL: strings.TrimRight(base, "/"), Token: token, HTTP: &http.Client{Timeout: 70 * time.Second}}
}

// Health is what /v1/health reports. An older Echo leaves out the fields
// after Wire: they're then empty.
type Health struct {
	Tool    string `json:"tool"`
	Version string `json:"version"`
	Mode    string `json:"mode"`
	Wire    int    `json:"wire"`
	// SMTP and Proxy are where Echo's SMTP sink and proxy listen, such as
	// "127.0.0.1:1025", or "off". Empty when Echo doesn't say.
	SMTP  string `json:"smtp,omitempty"`
	Proxy string `json:"proxy,omitempty"`
	// BootID changes every time Echo starts: a different one mid-run
	// means Echo restarted and lost the messages it held in memory.
	BootID       string   `json:"boot_id,omitempty"`
	Store        string   `json:"store,omitempty"` // "memory" or "jsonl"
	Capabilities []string `json:"capabilities,omitempty"`
	Providers    []string `json:"providers"`
}

// Supports reports whether Echo has the API feature name, such as
// "leases".
func (h Health) Supports(name string) bool { return slices.Contains(h.Capabilities, name) }

// Health checks that Echo is running and returns its mode.
func (c *Client) Health(ctx context.Context) (Health, error) {
	var h Health
	err := c.do(ctx, http.MethodGet, "/v1/health", nil, nil, &h)
	if err == nil && h.Tool != "rhythm-echo" {
		err = fmt.Errorf("%s isn't Echo", c.URL)
	}
	return h, err
}

// Filter selects messages.
type Filter struct {
	Run, Action string
	Channel     model.Channel
	To          string
	Since       time.Time
}

func (f Filter) query() url.Values {
	q := url.Values{}
	for k, v := range map[string]string{"run": f.Run, "action": f.Action, "channel": string(f.Channel), "to": f.To} {
		if v != "" {
			q.Set(k, v)
		}
	}
	if !f.Since.IsZero() {
		q.Set("since", f.Since.UTC().Format(time.RFC3339Nano))
	}
	return q
}

// Messages lists captured messages.
func (c *Client) Messages(ctx context.Context, f Filter) ([]model.Message, error) {
	var out []model.Message
	return out, c.do(ctx, http.MethodGet, "/v1/messages", f.query(), nil, &out)
}

// Message returns one message by ID.
func (c *Client) Message(ctx context.Context, id string) (model.Message, error) {
	var m model.Message
	return m, c.do(ctx, http.MethodGet, "/v1/messages/"+url.PathEscape(id), nil, nil, &m)
}

// Wait returns the messages matching f once at least count arrived and
// none came for quiet, or after timeout with whatever arrived.
func (c *Client) Wait(ctx context.Context, f Filter, count int, quiet, timeout time.Duration) ([]model.Message, error) {
	q := f.query()
	q.Set("count", strconv.Itoa(count))
	q.Set("quiet", quiet.String())
	q.Set("timeout", timeout.String())
	var out []model.Message
	return out, c.do(ctx, http.MethodGet, "/v1/wait", q, nil, &out)
}

// Clear deletes the messages matching f and returns how many.
func (c *Client) Clear(ctx context.Context, f Filter) (int, error) {
	var out struct {
		Deleted int `json:"deleted"`
	}
	return out.Deleted, c.do(ctx, http.MethodDelete, "/v1/messages", f.query(), nil, &out)
}

// Begin tells Echo that run/action is in progress, so messages that can't
// say which action caused them (plain SMTP, async jobs) are attributed to
// it. End closes it. Use BeginLease when other runs may share this Echo.
func (c *Client) Begin(ctx context.Context, run, action string) error {
	_, err := c.BeginLease(ctx, run, action)
	return err
}

// End closes the oldest action this machine began. EndLease closes one
// action exactly.
func (c *Client) End(ctx context.Context) error {
	return c.do(ctx, http.MethodPost, "/v1/actions/end", nil, nil, nil)
}

// BeginLease tells Echo that run/action is in progress and returns the
// lease that EndLease takes to close it. While several actions are open,
// Echo marks untagged messages ambiguous rather than guess. An Echo from
// before leases returns an empty lease; EndLease then falls back to End.
func (c *Client) BeginLease(ctx context.Context, run, action string) (string, error) {
	var out struct {
		Lease string `json:"lease"`
	}
	err := c.do(ctx, http.MethodPost, "/v1/actions/begin", nil, map[string]string{"run": run, "action": action}, &out)
	return out.Lease, err
}

// EndLease closes the action BeginLease returned lease for.
func (c *Client) EndLease(ctx context.Context, lease string) error {
	if lease == "" {
		return c.End(ctx)
	}
	return c.do(ctx, http.MethodPost, "/v1/actions/end", nil, map[string]string{"lease": lease}, nil)
}

// Capture sends a capture, as a connector would. Echo's own tools (the
// sendmail shim, tests) use it.
func (c *Client) Capture(ctx context.Context, cp wire.Capture) (wire.Decision, error) {
	cp.Version = wire.Version
	var d wire.Decision
	return d, c.do(ctx, http.MethodPost, "/v1/capture", nil, cp, &d)
}

// RunValue is the X-Echo-Run value for run and action.
func RunValue(run, action string) string { return run + "/" + action }

// ErrNotRunning means nothing answered at the URL.
var ErrNotRunning = errors.New("no Echo is running")

// APIError is an answer from Echo other than success.
type APIError struct {
	Method, Path string
	StatusCode   int
	Status       string // e.g. "404 Not Found"
	Message      string // the body, trimmed
}

func (e *APIError) Error() string {
	msg := "Echo answered " + e.Status + " to " + e.Method + " " + e.Path
	switch e.Message {
	case "", http.StatusText(e.StatusCode), "404 page not found": // nothing the status doesn't say
		return msg
	}
	return msg + ": " + e.Message
}

// IsNotFound reports whether err is Echo answering 404, e.g. for a message
// ID it doesn't have.
func IsNotFound(err error) bool {
	var ae *APIError
	return errors.As(err, &ae) && ae.StatusCode == http.StatusNotFound
}

func (c *Client) do(ctx context.Context, method, path string, q url.Values, body, out any) error {
	u := c.URL + path
	if len(q) > 0 {
		u += "?" + q.Encode()
	}
	var rd io.Reader
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			return err
		}
		rd = strings.NewReader(string(b))
	}
	req, err := http.NewRequestWithContext(ctx, method, u, rd)
	if err != nil {
		return err
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	if c.Token != "" {
		req.Header.Set(wire.TokenHeader, c.Token)
	}
	resp, err := c.HTTP.Do(req)
	if err != nil {
		if ctx.Err() == nil {
			return fmt.Errorf("%w at %s (start it with: rhythm echo up)", ErrNotRunning, c.URL)
		}
		return err
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode >= 300 {
		msg, _ := io.ReadAll(io.LimitReader(resp.Body, 512))
		return &APIError{Method: method, Path: path, StatusCode: resp.StatusCode, Status: resp.Status, Message: strings.TrimSpace(string(msg))}
	}
	// An Echo from before leases answers begin with 204 and no lease.
	if out == nil || resp.StatusCode == http.StatusNoContent {
		return nil
	}
	return json.NewDecoder(resp.Body).Decode(out)
}
