// Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
// SPDX-License-Identifier: MIT

// Package mcp is Echo's Model Context Protocol server, so coding agents
// can ask what a site sent ("what did checkout send?", "what changed since
// the snapshot?") instead of scraping a terminal. It speaks JSON-RPC 2.0
// over stdio, one message per line, with the standard library only:
// initialize (with protocol version negotiation), ping, tools/list and
// tools/call. The tools call Echo's own packages directly: the API client
// for captured messages, the runner and store for actions and snapshots,
// compare for changes and dns for deliverability.
//
// Every tool result is masked before it leaves (see mask.go); latest_code
// returns the code alone, never the message it came from.
//
//	claude mcp add echo -- rhythm-echo mcp
package mcp

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"slices"
	"sync"
	"time"

	"github.com/RhythmShahriar/echo/client"
	"github.com/RhythmShahriar/echo/config"
	"github.com/RhythmShahriar/echo/dns"
	"github.com/RhythmShahriar/echo/runner"
	"github.com/RhythmShahriar/echo/store"
)

// Versions are the MCP protocol versions this server speaks, newest
// first. A client asking for one of them gets it; any other request gets
// the newest; the client decides whether it can go on.
var Versions = []string{"2025-06-18", "2025-03-26", "2024-11-05"}

// JSON-RPC 2.0 error codes.
const (
	codeParse          = -32700
	codeInvalidRequest = -32600
	codeNoMethod       = -32601
	codeInvalidParams  = -32602
	codeInternal       = -32603
)

// maxLine caps one incoming message. Tool arguments are small; anything
// bigger is a mistake or an attack, not a request.
const maxLine = 4 << 20

// Server answers MCP requests with Echo's data. Zero fields have working
// defaults except Echo.
type Server struct {
	Echo    *client.Client   // the running Echo the message tools read
	Config  config.Config    // actions for run_action, snapshot and compare
	Store   store.Store      // where snapshots and runs are kept (default .echo)
	Replay  runner.Replayer  // replay actions; nil reports them as unavailable
	Dir     string           // the project folder actions run in; "" is the working folder
	DNS     dns.Resolver     // check_domain's resolver; nil is the system's
	Version string           // Echo's version, in serverInfo and stored runs
	Now     func() time.Time // nil: time.Now

	actions sync.Mutex // one run at a time: Echo attributes untagged mail to the action in progress
}

type request struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id,omitempty"`
	Method  string          `json:"method"`
	Params  json.RawMessage `json:"params,omitempty"`
}

type response struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id"`
	Result  any             `json:"result,omitempty"`
	Error   *rpcError       `json:"error,omitempty"`
}

type rpcError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

func (e *rpcError) Error() string { return e.Message }

func errorf(code int, format string, a ...any) *rpcError {
	return &rpcError{Code: code, Message: fmt.Sprintf(format, a...)}
}

// Serve reads requests from r and writes responses to w until r ends or
// ctx is done. Requests run concurrently, so a ping is answered while an
// action runs and a cancel notification stops the request it names.
func (s *Server) Serve(ctx context.Context, r io.Reader, w io.Writer) error {
	if s.Now == nil {
		s.Now = time.Now
	}
	if s.Store.Root == "" {
		s.Store.Root = config.Dir
	}
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	out := &writer{w: w}
	var wg sync.WaitGroup
	defer wg.Wait()
	running := &inflight{m: map[string]context.CancelFunc{}}

	sc := bufio.NewScanner(r)
	sc.Buffer(make([]byte, 64<<10), maxLine)
	for sc.Scan() {
		line := bytes.TrimSpace(sc.Bytes())
		if len(line) == 0 {
			continue
		}
		// A JSON array is a batch (protocol 2025-03-26); answer each
		// request in it and send the answers together.
		if line[0] == '[' {
			var batch []json.RawMessage
			if err := json.Unmarshal(line, &batch); err != nil || len(batch) == 0 {
				out.send(response{JSONRPC: "2.0", ID: json.RawMessage("null"), Error: errorf(codeInvalidRequest, "an empty or broken batch")})
				continue
			}
			wg.Add(1)
			go func() {
				defer wg.Done()
				var answers []response
				for _, raw := range batch {
					reqCtx, done := running.register(ctx, raw)
					resp, ok := s.handle(reqCtx, raw, running)
					done()
					if ok {
						answers = append(answers, resp)
					}
				}
				if len(answers) > 0 {
					out.send(answers)
				}
			}()
			continue
		}
		raw := slices.Clone(line)
		// Registered here, before the next line is read, so a cancel
		// notification that follows always finds the request.
		reqCtx, done := running.register(ctx, raw)
		wg.Add(1)
		go func() {
			defer wg.Done()
			defer done()
			if resp, ok := s.handle(reqCtx, raw, running); ok {
				out.send(resp)
			}
		}()
	}
	if err := sc.Err(); err != nil {
		return err
	}
	return nil
}

// handle answers one message, in ctx: the caller has registered a
// request with running (see register), so a cancel can stop it. It
// reports false for a notification, which gets no answer.
func (s *Server) handle(ctx context.Context, raw json.RawMessage, running *inflight) (response, bool) {
	var req request
	if err := json.Unmarshal(raw, &req); err != nil {
		return response{JSONRPC: "2.0", ID: json.RawMessage("null"), Error: errorf(codeParse, "not JSON: %v", err)}, true
	}
	notification := len(req.ID) == 0
	if req.JSONRPC != "2.0" || req.Method == "" {
		if notification {
			return response{}, false
		}
		return response{JSONRPC: "2.0", ID: req.ID, Error: errorf(codeInvalidRequest, `want "jsonrpc": "2.0" and a method`)}, true
	}
	if notification {
		if req.Method == "notifications/cancelled" { //nolint:misspell // the protocol's spelling
			var p struct {
				RequestID json.RawMessage `json:"requestId"`
			}
			if json.Unmarshal(req.Params, &p) == nil {
				running.cancel(p.RequestID)
			}
		}
		return response{}, false // notifications/initialized and the rest need nothing
	}
	result, err := s.dispatch(ctx, req)
	resp := response{JSONRPC: "2.0", ID: req.ID, Result: result}
	if err != nil {
		var re *rpcError
		if !errors.As(err, &re) {
			re = errorf(codeInternal, "%v", err)
		}
		resp.Result, resp.Error = nil, re
	}
	return resp, true
}

func (s *Server) dispatch(ctx context.Context, req request) (any, error) {
	switch req.Method {
	case "initialize":
		var p struct {
			ProtocolVersion string `json:"protocolVersion"`
		}
		if err := json.Unmarshal(req.Params, &p); err != nil {
			return nil, errorf(codeInvalidParams, "initialize: %v", err)
		}
		return s.initialize(p.ProtocolVersion), nil
	case "ping":
		return struct{}{}, nil
	case "tools/list":
		return map[string]any{"tools": toolList()}, nil
	case "tools/call":
		var p struct {
			Name      string          `json:"name"`
			Arguments json.RawMessage `json:"arguments"`
		}
		if err := json.Unmarshal(req.Params, &p); err != nil {
			return nil, errorf(codeInvalidParams, "tools/call: %v", err)
		}
		return s.call(ctx, p.Name, p.Arguments)
	}
	return nil, errorf(codeNoMethod, "no method %q", req.Method)
}

// Negotiate returns the protocol version to answer a client that asked
// for requested: the same when this server speaks it, else the newest.
func Negotiate(requested string) string {
	if slices.Contains(Versions, requested) {
		return requested
	}
	return Versions[0]
}

func (s *Server) initialize(requested string) map[string]any {
	return map[string]any{
		"protocolVersion": Negotiate(requested),
		"capabilities":    map[string]any{"tools": map[string]any{"listChanged": false}},
		"serverInfo":      map[string]any{"name": "rhythm-echo", "title": "Echo", "version": s.Version},
		"instructions": "Echo records every email, SMS, webhook and API call a site sends. " +
			"list_messages and get_message read what was captured; latest_code gives a one-time code for a login or sign-up test; " +
			"run_action, snapshot and compare run the actions in .echo/config.yaml; check_domain checks SPF, DKIM, DMARC and MX; " +
			"check_consent loads a page with no choice, Accept and Reject on its cookie banner and reports trackers that fired before the choice or after Reject. " +
			"Credentials in results are masked.",
	}
}

// writer sends one JSON message per line; requests answer concurrently,
// so lines must not interleave.
type writer struct {
	mu sync.Mutex
	w  io.Writer
}

func (o *writer) send(v any) {
	b, err := json.Marshal(v)
	if err != nil {
		b, _ = json.Marshal(response{JSONRPC: "2.0", ID: json.RawMessage("null"), Error: errorf(codeInternal, "%v", err)})
	}
	o.mu.Lock()
	defer o.mu.Unlock()
	_, _ = o.w.Write(append(b, '\n'))
}

// inflight tracks running requests by ID, for cancel notifications.
type inflight struct {
	mu sync.Mutex
	m  map[string]context.CancelFunc
}

func (f *inflight) add(ctx context.Context, id json.RawMessage) (context.Context, func()) {
	ctx, cancel := context.WithCancel(ctx)
	key := string(id)
	f.mu.Lock()
	f.m[key] = cancel
	f.mu.Unlock()
	return ctx, func() {
		f.mu.Lock()
		delete(f.m, key)
		f.mu.Unlock()
		cancel()
	}
}

// register adds raw to the running requests when it is a request with an
// ID. It returns the context it runs in and what removes it again.
func (f *inflight) register(ctx context.Context, raw json.RawMessage) (context.Context, func()) {
	var head struct {
		ID json.RawMessage `json:"id"`
	}
	if json.Unmarshal(raw, &head) != nil || len(head.ID) == 0 || string(head.ID) == "null" {
		return ctx, func() {}
	}
	return f.add(ctx, head.ID)
}

func (f *inflight) cancel(id json.RawMessage) {
	f.mu.Lock()
	c := f.m[string(id)]
	f.mu.Unlock()
	if c != nil {
		c()
	}
}
