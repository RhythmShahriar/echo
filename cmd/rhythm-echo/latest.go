// Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
// SPDX-License-Identifier: MIT

package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"slices"
	"strings"
	"time"

	"github.com/RhythmShahriar/echo/client"
	"github.com/RhythmShahriar/echo/model"
	"github.com/RhythmShahriar/echo/runner"
	"github.com/RhythmShahriar/echo/wire"
)

// latestWait is how long latest waits for a message unless --wait says.
const latestWait = 10 * time.Second

// linkKinds are the values --kind accepts: the kinds of link Echo reads
// from a message.
var linkKinds = []string{"magic", "verify", "reset", "login", "unsubscribe"}

// latestLink is a link Echo read from a message, by kind.
type latestLink struct {
	Kind string `json:"kind"`
	URL  string `json:"url"`
}

// latestResult is what GET /v1/messages/latest answers: the newest
// matching message, masked as stored, with the one-time codes (the most
// likely first) and links read from it before masking.
//
// It and fetchLatest stand in for client.Latest until that is released,
// with the same fields and query.
type latestResult struct {
	Codes   []string      `json:"codes"`
	Links   []latestLink  `json:"links"`
	Message model.Message `json:"message"`
}

// link is the first link of kind, or of any kind when kind is "".
func (l latestResult) link(kind string) string {
	for _, k := range l.Links {
		if kind == "" || k.Kind == kind {
			return k.URL
		}
	}
	return ""
}

// latestQuery selects the message latest prints. Zero fields match
// anything.
type latestQuery struct {
	To, Run, Action string
	Channels        []model.Channel
	Since           time.Time
	Wait            time.Duration
}

// errNoMatch is Echo answering that no message matched in time.
var errNoMatch = errors.New("no message matched")

// errNoCodes is an Echo from before /v1/messages/latest: it reads no
// codes or links.
var errNoCodes = errors.New("this Echo doesn't read codes and links; update it (rhythm update) and start it again")

// fetchLatest asks Echo for the newest message matching lq, waiting up to
// lq.Wait for one.
func fetchLatest(ctx context.Context, c *client.Client, lq latestQuery) (latestResult, error) {
	v := url.Values{}
	for k, s := range map[string]string{"run": lq.Run, "action": lq.Action, "to": lq.To} {
		if s != "" {
			v.Set(k, s)
		}
	}
	if !lq.Since.IsZero() {
		v.Set("since", lq.Since.UTC().Format(time.RFC3339Nano))
	}
	if len(lq.Channels) > 0 {
		names := make([]string, len(lq.Channels))
		for i, ch := range lq.Channels {
			names[i] = string(ch)
		}
		v.Set("channel", strings.Join(names, ","))
	}
	if lq.Wait > 0 {
		v.Set("wait", lq.Wait.String())
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.URL+"/v1/messages/latest?"+v.Encode(), nil)
	if err != nil {
		return latestResult{}, err
	}
	if c.Token != "" {
		req.Header.Set(wire.TokenHeader, c.Token)
	}
	// The client's own timeout may be shorter than a long --wait.
	hc := *c.HTTP
	hc.Timeout = lq.Wait + 10*time.Second
	resp, err := hc.Do(req)
	if err != nil {
		if ctx.Err() == nil {
			return latestResult{}, fmt.Errorf("%w at %s (start it with: rhythm echo up)", client.ErrNotRunning, c.URL)
		}
		return latestResult{}, err
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		msg, _ := io.ReadAll(io.LimitReader(resp.Body, 512))
		text := strings.TrimSpace(string(msg))
		switch {
		// The route answers its own 404 text; an older Echo takes "latest"
		// for a message ID and answers the plain one.
		case resp.StatusCode == http.StatusNotFound && strings.HasPrefix(text, "no message matched"):
			return latestResult{}, errNoMatch
		case resp.StatusCode == http.StatusNotFound:
			return latestResult{}, errNoCodes
		}
		return latestResult{}, &client.APIError{Method: http.MethodGet, Path: "/v1/messages/latest", StatusCode: resp.StatusCode,
			Status: resp.Status, Message: text}
	}
	var l latestResult
	if err := json.NewDecoder(resp.Body).Decode(&l); err != nil {
		return latestResult{}, fmt.Errorf("reading Echo's answer: %w", err)
	}
	return l, nil
}

// cmdLatest prints the one-time code or link in the newest message to
// --to, for a shell script or an end-to-end test that signs in: just the
// code or the URL and a newline. It exits 1 when no message came within
// --wait, or the message has no code or link of the kind asked for.
func cmdLatest(s *session) int {
	o := s.o
	if len(s.args) > 0 {
		return s.fail(usageErr("latest takes no arguments; name the recipient with --to"))
	}
	if o.to == "" {
		return s.fail(usageErr("latest needs --to <address or phone>: whose code or link to print"))
	}
	if o.code && (o.link || o.kind != "") {
		return s.fail(usageErr("pick --code or --link, not both"))
	}
	if o.kind != "" && !slices.Contains(linkKinds, o.kind) {
		return s.fail(usageErr("--kind %q: use one of %s", o.kind, strings.Join(linkKinds, ", ")))
	}
	if len(o.actions) > 1 {
		return s.fail(usageErr("latest takes one --action"))
	}
	wait := latestWait
	if o.waitSet {
		wait = o.wait
	}
	if wait < 0 || wait > time.Minute {
		return s.fail(usageErr("--wait %s: give 0 (don't wait) up to 1m", shortDuration(wait)))
	}
	lq := latestQuery{To: o.to, Run: o.runID, Channels: o.channels, Wait: wait}
	if len(o.actions) == 1 {
		// Messages carry the action's ID; a name is turned into it.
		lq.Action = o.actions[0]
		if !strings.HasPrefix(lq.Action, "action:") {
			lq.Action = runner.ActionID(lq.Action)
		}
	}
	if o.since > 0 {
		lq.Since = time.Now().UTC().Add(-o.since)
	}
	l, err := fetchLatest(s.ctx, s.echo, lq)
	if errors.Is(err, errNoMatch) {
		fmt.Fprintf(s.stderr, "rhythm echo: no message to %s arrived within %s\n", o.to, shortDuration(wait))
		return exitFindings
	}
	if err != nil {
		return s.fail(err)
	}
	if o.json {
		return jsonOut(s, kindLatest, l)
	}
	what, value := "one-time code", ""
	switch {
	case o.code:
		value = first(l.Codes)
	case o.link || o.kind != "":
		what, value = "link", l.link(o.kind)
		if o.kind != "" {
			what = o.kind + " link"
		}
	default:
		// Neither asked for: the code, else the first link.
		if value = first(l.Codes); value == "" {
			what, value = "one-time code or link", l.link("")
		}
	}
	if value == "" {
		fmt.Fprintf(s.stderr, "rhythm echo: the newest message to %s has no %s (rhythm echo show %s)\n", o.to, what, l.Message.ID)
		return exitFindings
	}
	fmt.Fprintln(s.stdout, value)
	return exitOK
}

// first is the first of list, or "".
func first(list []string) string {
	if len(list) == 0 {
		return ""
	}
	return list[0]
}
