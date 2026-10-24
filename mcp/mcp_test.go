// Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
// SPDX-License-Identifier: MIT

package mcp

import (
	"bufio"
	"context"
	"encoding/json"
	"flag"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/RhythmShahriar/echo/catcher"
	"github.com/RhythmShahriar/echo/client"
	"github.com/RhythmShahriar/echo/config"
	"github.com/RhythmShahriar/echo/dns"
	"github.com/RhythmShahriar/echo/runner"
	"github.com/RhythmShahriar/echo/store"
	"github.com/RhythmShahriar/echo/wire"
)

var update = flag.Bool("update", false, "rewrite testdata/tools.golden.json")

// Fixture ports on loopback: Echo's API and the shop.
const (
	echoAddr = "127.0.0.1:20500"
	shopAddr = "127.0.0.1:20501"
)

// The secrets the shop's mail carries. Echo's catcher masks them when it
// stores the mail; an Echo before 1.1 did not, so the MCP results must
// mask them as well.
const (
	shopifyToken = "shpat_0123456789abcdef0123456789abcdef"
	resetKey     = "Zk3pQ9vX2mL7tR4w"
)

func listen(t *testing.T, addr string, h http.Handler) *httptest.Server {
	t.Helper()
	ln, err := net.Listen("tcp", addr)
	if err != nil {
		t.Fatalf("listen %s: %v", addr, err)
	}
	srv := httptest.NewUnstartedServer(h)
	_ = srv.Listener.Close()
	srv.Listener = ln
	srv.Start()
	t.Cleanup(srv.Close)
	return srv
}

// fixture is a running Echo (a real catcher.Server) and a one-page shop
// whose POST /order sends an order email through it, tagged with the run.
// broken switches the email to the shop owner instead of the buyer.
type fixture struct {
	echo   *catcher.Server
	broken atomic.Bool
	server *Server
}

func newFixture(t *testing.T) *fixture {
	t.Helper()
	f := &fixture{echo: catcher.NewServer(catcher.NewInbox(1000), catcher.Config{Version: "test"})}
	listen(t, echoAddr, f.echo)
	listen(t, shopAddr, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.URL.Path != "/order" {
			http.NotFound(w, r)
			return
		}
		to := r.FormValue("email")
		if f.broken.Load() {
			to = "owner@shop.test"
		}
		_, err := f.echo.Capture(wire.Capture{Version: wire.Version, Kind: "email", Run: r.Header.Get(wire.RunHeader),
			Source: wire.Source{Via: "wordpress", Host: shopAddr},
			Email: &wire.Email{From: "shop@shop.test", To: []string{to}, Subject: "Order W-1001 received",
				Text:    "Thanks for your order. Your sign-in code is 482913.\nTrack it: http://" + shopAddr + "/track?k=" + resetKey + "&page=2",
				Headers: map[string]string{"X-Shopify-Access-Token": shopifyToken, "X-Request-Id": "req-42"}}})
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		_, _ = io.WriteString(w, "ok")
	}))
	cfg := config.Default()
	cfg.Site = "http://" + shopAddr
	cfg.Quiet = config.Duration{Duration: 150 * time.Millisecond}
	cfg.Timeout = config.Duration{Duration: 5 * time.Second}
	cfg.Actions = []config.Action{{Name: "Checkout", HTTP: &config.HTTPAction{URL: "/order", Form: map[string]string{"email": "ann@example.com"}}}}
	f.server = &Server{Echo: client.New("http://"+echoAddr, ""), Config: cfg, Store: store.Store{Root: t.TempDir()}, Version: "test",
		DNS: &dns.Zone{TXT: map[string][]string{"shop.test": {"v=spf1 include:sendgrid.net -all"}, "_dmarc.shop.test": {"v=DMARC1; p=reject"}},
			MX: map[string][]*net.MX{"shop.test": {{Host: "mx.shop.test.", Pref: 10}}}}}
	return f
}

// session drives Serve over pipes, as an MCP client does over stdio.
type session struct {
	t     *testing.T
	in    *io.PipeWriter
	lines chan []byte
	id    int
	done  chan error
}

func start(t *testing.T, s *Server) *session {
	t.Helper()
	inR, inW := io.Pipe()
	outR, outW := io.Pipe()
	ss := &session{t: t, in: inW, lines: make(chan []byte, 16), done: make(chan error, 1)}
	go func() {
		ss.done <- s.Serve(context.Background(), inR, outW)
		_ = outW.Close()
	}()
	go func() {
		sc := bufio.NewScanner(outR)
		sc.Buffer(make([]byte, 1<<20), 16<<20)
		for sc.Scan() {
			ss.lines <- append([]byte(nil), sc.Bytes()...)
		}
		close(ss.lines)
	}()
	t.Cleanup(func() {
		_ = inW.Close()
		select {
		case err := <-ss.done:
			if err != nil {
				t.Errorf("Serve: %v", err)
			}
		case <-time.After(5 * time.Second):
			t.Error("Serve didn't end when stdin closed")
		}
	})
	return ss
}

func (ss *session) write(v any) {
	ss.t.Helper()
	b, err := json.Marshal(v)
	if err != nil {
		ss.t.Fatal(err)
	}
	if _, err := ss.in.Write(append(b, '\n')); err != nil {
		ss.t.Fatal(err)
	}
}

// read returns the next message the server sent.
func (ss *session) read() map[string]any {
	ss.t.Helper()
	select {
	case line, ok := <-ss.lines:
		if !ok {
			ss.t.Fatal("the server closed stdout")
		}
		var v map[string]any
		if err := json.Unmarshal(line, &v); err != nil {
			ss.t.Fatalf("not a JSON object: %s", line)
		}
		return v
	case <-time.After(60 * time.Second):
		ss.t.Fatal("no answer")
	}
	return nil
}

// call sends a request and returns its result, failing on an error.
func (ss *session) call(method string, params any) map[string]any {
	ss.t.Helper()
	resp := ss.request(method, params)
	if resp["error"] != nil {
		ss.t.Fatalf("%s: %v", method, resp["error"])
	}
	res, _ := resp["result"].(map[string]any)
	return res
}

func (ss *session) request(method string, params any) map[string]any {
	ss.t.Helper()
	ss.id++
	ss.write(map[string]any{"jsonrpc": "2.0", "id": ss.id, "method": method, "params": params})
	resp := ss.read()
	if id, _ := resp["id"].(float64); int(id) != ss.id {
		ss.t.Fatalf("answer to id %v, want %d: %v", resp["id"], ss.id, resp)
	}
	return resp
}

// tool calls a tool and returns its structured result and text.
func (ss *session) tool(name string, args map[string]any) (map[string]any, string, bool) {
	ss.t.Helper()
	res := ss.call("tools/call", map[string]any{"name": name, "arguments": args})
	content, _ := res["content"].([]any)
	if len(content) != 1 {
		ss.t.Fatalf("%s: content %v", name, res["content"])
	}
	text, _ := content[0].(map[string]any)["text"].(string)
	structured, _ := res["structuredContent"].(map[string]any)
	isErr, _ := res["isError"].(bool)
	return structured, text, isErr
}

func TestSession(t *testing.T) {
	f := newFixture(t)
	ss := start(t, f.server)

	init := ss.call("initialize", map[string]any{"protocolVersion": "2025-03-26", "capabilities": map[string]any{},
		"clientInfo": map[string]any{"name": "test", "version": "1"}})
	if init["protocolVersion"] != "2025-03-26" {
		t.Errorf("protocolVersion %v, want the client's 2025-03-26", init["protocolVersion"])
	}
	if info, _ := init["serverInfo"].(map[string]any); info["name"] != "rhythm-echo" || info["version"] != "test" {
		t.Errorf("serverInfo %v", init["serverInfo"])
	}
	ss.write(map[string]any{"jsonrpc": "2.0", "method": "notifications/initialized"}) // no answer
	if res := ss.call("ping", nil); len(res) != 0 {
		t.Errorf("ping: %v", res)
	}

	t.Run("tools/list", func(t *testing.T) {
		got, err := json.MarshalIndent(ss.call("tools/list", nil), "", "  ")
		if err != nil {
			t.Fatal(err)
		}
		golden := filepath.Join("testdata", "tools.golden.json")
		if *update {
			if err := os.WriteFile(golden, append(got, '\n'), 0o644); err != nil {
				t.Fatal(err)
			}
		}
		want, err := os.ReadFile(golden)
		if err != nil {
			t.Fatal(err)
		}
		if strings.TrimSpace(string(want)) != string(got) {
			t.Errorf("tools/list changed; if that's intended, run go test ./mcp -update\n%s", got)
		}
	})

	t.Run("snapshot", func(t *testing.T) {
		res, text, isErr := ss.tool("snapshot", map[string]any{"name": "good"})
		if isErr {
			t.Fatal(text)
		}
		acts, _ := res["actions"].([]any)
		if len(acts) != 1 || res["name"] != "good" {
			t.Fatalf("snapshot: %s", text)
		}
		if msgs := acts[0].(map[string]any)["messages"].([]any); len(msgs) != 1 {
			t.Errorf("the checkout sent %d messages, want 1: %s", len(msgs), text)
		}
		if _, err := os.Stat(filepath.Join(f.server.Store.Root, "snapshots", "good", "run.json")); err != nil {
			t.Error(err)
		}
	})

	t.Run("list_messages", func(t *testing.T) {
		res, text, isErr := ss.tool("list_messages", map[string]any{"channel": "email", "since": "1h"})
		if isErr {
			t.Fatal(text)
		}
		msgs, _ := res["messages"].([]any)
		if len(msgs) != 1 || res["total"] != 1.0 {
			t.Fatalf("list: %s", text)
		}
		m := msgs[0].(map[string]any)
		if m["subject"] != "Order W-1001 received" || m["action"] != "action:checkout" {
			t.Errorf("message %v", m)
		}
		if _, ok := m["text"]; ok {
			t.Error("the list carries message bodies; get_message is for those")
		}
		if _, text, isErr := ss.tool("list_messages", map[string]any{"channel": "fax"}); !isErr || !strings.Contains(text, `channel "fax"`) {
			t.Errorf("bad channel: %v %s", isErr, text)
		}
	})

	t.Run("get_message masks secrets", func(t *testing.T) {
		res, text, isErr := ss.tool("get_message", map[string]any{"id": "latest"})
		if isErr {
			t.Fatal(text)
		}
		for _, secret := range []string{shopifyToken, resetKey} {
			if strings.Contains(text, secret) {
				t.Errorf("get_message shows %s:\n%s", secret, text)
			}
		}
		headers, _ := res["headers"].(map[string]any)
		if headers["x-request-id"] != "req-42" {
			t.Errorf("x-request-id was masked or lost: %v", headers)
		}
		if !strings.Contains(text, "page=2") || !strings.Contains(text, "Order W-1001 received") {
			t.Errorf("masking took more than the secrets: %s", text)
		}
		// Echo stores the one-time code masked; latest_code is the way to it.
		if strings.Contains(text, "482913") {
			t.Errorf("get_message shows the one-time code: %s", text)
		}
		id, _ := res["id"].(string)
		byID, _, isErr := ss.tool("get_message", map[string]any{"id": id})
		if isErr || byID["subject"] != "Order W-1001 received" {
			t.Errorf("by ID: %v", byID)
		}
		if _, text, isErr := ss.tool("get_message", map[string]any{"id": "nope"}); !isErr || !strings.Contains(text, `no message "nope"`) {
			t.Errorf("unknown ID: %v %s", isErr, text)
		}
	})

	t.Run("latest_code", func(t *testing.T) {
		res, text, isErr := ss.tool("latest_code", map[string]any{"to": "ann@"})
		if isErr {
			t.Fatal(text)
		}
		if res["code"] != "482913" {
			t.Errorf("code %v", res["code"])
		}
		for k := range res {
			if k != "code" && k != "message_id" {
				t.Errorf("latest_code returns %s too: %s", k, text)
			}
		}
		if _, text, isErr := ss.tool("latest_code", map[string]any{"to": "nobody@"}); !isErr || !strings.Contains(text, "no one-time code") {
			t.Errorf("no match: %v %s", isErr, text)
		}
	})

	t.Run("run_action", func(t *testing.T) {
		res, text, isErr := ss.tool("run_action", map[string]any{"actions": []string{"checkout"}})
		if isErr {
			t.Fatal(text)
		}
		if acts, _ := res["actions"].([]any); len(acts) != 1 || acts[0].(map[string]any)["label"] != "Checkout" {
			t.Errorf("run: %s", text)
		}
		if _, text, isErr := ss.tool("run_action", map[string]any{"actions": []string{"refund"}}); !isErr || !strings.Contains(text, `"refund"`) {
			t.Errorf("unknown action: %v %s", isErr, text)
		}
	})

	t.Run("compare", func(t *testing.T) {
		res, text, isErr := ss.tool("compare", nil)
		if isErr {
			t.Fatal(text)
		}
		if res["verdict"] != "Nothing changed." || res["snapshot"] != "good" {
			t.Errorf("unchanged site: %s", text)
		}
		f.broken.Store(true)
		res, text, isErr = ss.tool("compare", map[string]any{"snapshot": "good"})
		if isErr {
			t.Fatal(text)
		}
		counts, _ := res["counts"].(map[string]any)
		if res["verdict"] != "Something broke." || counts["broke"] != 1.0 || counts["new"] != 1.0 {
			t.Errorf("broken site: %s", text)
		}
		if strings.Contains(text, shopifyToken) {
			t.Error("compare shows the token")
		}
		if _, text, isErr := ss.tool("compare", map[string]any{"snapshot": "missing"}); !isErr || !strings.Contains(text, "missing") {
			t.Errorf("unknown snapshot: %v %s", isErr, text)
		}
	})

	t.Run("check_domain", func(t *testing.T) {
		res, text, isErr := ss.tool("check_domain", map[string]any{"domain": "Shop.Test.", "provider": "sendgrid"})
		if isErr {
			t.Fatal(text)
		}
		if res["domain"] != "shop.test" || res["dmarc"] == nil {
			t.Errorf("report: %s", text)
		}
		if _, text, isErr := ss.tool("check_domain", map[string]any{"domain": "shop.test", "provider": "pigeon"}); !isErr || !strings.Contains(text, "pigeon") {
			t.Errorf("unknown provider: %v %s", isErr, text)
		}
	})

	t.Run("protocol errors", func(t *testing.T) {
		for _, tc := range []struct {
			method string
			params any
			code   float64
		}{
			{"tools/call", map[string]any{"name": "rm_rf"}, codeInvalidParams},
			{"tools/call", map[string]any{"name": "get_message", "arguments": map[string]any{"id": "x", "extra": 1}}, codeInvalidParams},
			{"resources/list", nil, codeNoMethod},
		} {
			resp := ss.request(tc.method, tc.params)
			e, _ := resp["error"].(map[string]any)
			if e["code"] != tc.code {
				t.Errorf("%s %v: %v, want code %v", tc.method, tc.params, resp, tc.code)
			}
		}
		ss.write("not an object")
		if e, _ := ss.read()["error"].(map[string]any); e["code"] != float64(codeParse) {
			t.Errorf("a JSON string: %v", e)
		}
		ss.write(map[string]any{"jsonrpc": "1.0", "id": 99, "method": "ping"})
		if e, _ := ss.read()["error"].(map[string]any); e["code"] != float64(codeInvalidRequest) {
			t.Errorf("jsonrpc 1.0: %v", e)
		}
	})

	t.Run("batch", func(t *testing.T) {
		ss.write([]any{map[string]any{"jsonrpc": "2.0", "id": "a", "method": "ping"},
			map[string]any{"jsonrpc": "2.0", "method": "notifications/initialized"},
			map[string]any{"jsonrpc": "2.0", "id": "b", "method": "ping"}})
		select {
		case line := <-ss.lines:
			var answers []map[string]any
			if err := json.Unmarshal(line, &answers); err != nil || len(answers) != 2 || answers[0]["id"] != "a" || answers[1]["id"] != "b" {
				t.Errorf("batch answer %s", line)
			}
		case <-time.After(5 * time.Second):
			t.Fatal("no batch answer")
		}
	})
}

func TestNegotiate(t *testing.T) {
	for req, want := range map[string]string{"2025-06-18": "2025-06-18", "2024-11-05": "2024-11-05", "2099-01-01": Versions[0], "": Versions[0]} {
		if got := Negotiate(req); got != want {
			t.Errorf("Negotiate(%q) = %q, want %q", req, got, want)
		}
	}
}

// TestCancel checks a cancel notification stops a running tool and that
// ping is answered while it runs.
func TestCancel(t *testing.T) {
	block := make(chan struct{})
	slow := listen(t, "127.0.0.1:20502", http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/v1/health" {
			_ = json.NewEncoder(w).Encode(map[string]any{"tool": "rhythm-echo", "mode": "seal"})
			return
		}
		select {
		case <-block:
		case <-r.Context().Done():
		}
	}))
	defer close(block)
	ss := start(t, &Server{Echo: client.New(slow.URL, ""), Store: store.Store{Root: t.TempDir()}})
	ss.write(map[string]any{"jsonrpc": "2.0", "id": 1, "method": "tools/call", "params": map[string]any{"name": "list_messages"}})
	ss.write(map[string]any{"jsonrpc": "2.0", "id": 2, "method": "ping"})
	if resp := ss.read(); resp["id"] != 2.0 {
		t.Fatalf("ping waited for the tool: %v", resp)
	}
	ss.write(map[string]any{"jsonrpc": "2.0", "method": "notifications/cancelled", "params": map[string]any{"requestId": 1}}) //nolint:misspell // the protocol's spelling
	resp := ss.read()
	if resp["id"] != 1.0 || resp["error"] == nil {
		t.Errorf("canceled call: %v", resp)
	}
}

func TestMain_(t *testing.T) {
	var out, errb strings.Builder
	if code := Main([]string{"--nope"}, strings.NewReader(""), &out, &errb); code != 2 || !strings.Contains(errb.String(), "usage: rhythm echo mcp") {
		t.Errorf("bad flag: %d %q", code, errb.String())
	}
	if out.Len() != 0 {
		t.Errorf("wrote to stdout: %q", out.String())
	}
	errb.Reset()
	if code := Main([]string{"--config", filepath.Join(t.TempDir(), "none.yaml")}, strings.NewReader(""), &out, &errb); code != 2 {
		t.Errorf("missing config: %d %q", code, errb.String())
	}
	errb.Reset()
	in := `{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2025-06-18"}}` + "\n"
	if code := Main([]string{"--url", "http://127.0.0.1:20503"}, strings.NewReader(in), &out, &errb); code != 0 {
		t.Errorf("serve: %d %q", code, errb.String())
	}
	if !strings.Contains(out.String(), `"protocolVersion":"2025-06-18"`) {
		t.Errorf("initialize answer %q", out.String())
	}
}

func TestReplayHook(t *testing.T) {
	called := false
	Replay = func(config.Config) runner.Replayer {
		called = true
		return func(context.Context, config.ReplayAction, string, string) error { return nil }
	}
	defer func() { Replay = nil }()
	var out, errb strings.Builder
	if code := Main([]string{"--url", "http://127.0.0.1:20503"}, strings.NewReader(""), &out, &errb); code != 0 || !called {
		t.Errorf("Main didn't use the replay hook: %d %v %q", code, called, errb.String())
	}
}
