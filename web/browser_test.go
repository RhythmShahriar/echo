// Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
// SPDX-License-Identifier: MIT

//go:build browser && unix

package web

import (
	"context"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"os"
	"os/exec"
	"strings"
	"syscall"
	"testing"
	"time"

	"golang.org/x/net/websocket"

	"github.com/RhythmShahriar/echo/model"
)

// TestBrowserSmoke opens Echo Inbox in headless Chrome, driven over the
// DevTools protocol and checks that the list, the detail pane and the
// HTML frame render without breaking the page's CSP. Run it with
// go test -tags browser ./web/; it skips without Chrome (or set CHROME).
func TestBrowserSmoke(t *testing.T) {
	chrome := ""
	for _, name := range []string{os.Getenv("CHROME"), "google-chrome", "google-chrome-stable", "chromium", "chromium-browser"} {
		if name == "" {
			continue
		}
		if p, err := exec.LookPath(name); err == nil {
			chrome = p
			break
		}
	}
	if chrome == "" {
		t.Skip("no Chrome found (set CHROME to its path)")
	}
	u, inbox := newUI(120)
	inbox.Add(model.Message{ID: "newest", Channel: model.Email, From: "shop@shop.test", To: []string{"ann@example.org"},
		Subject: "Welcome, {first_name}", Text: "Hi there", HTML: "<p>Hi <b>there</b></p>", Status: model.Faked})
	base := testServer(t, u)
	debug := freePort(t)

	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	// Not t.TempDir: Chrome's helpers may still write to the profile for
	// a moment after they're killed, which would fail its cleanup.
	profile, err := os.MkdirTemp("", "echo-inbox-chrome-")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = os.RemoveAll(profile) }()
	// Chrome's own background traffic is switched off and a dead proxy
	// catches anything else that isn't loopback.
	cmd := exec.CommandContext(ctx, chrome, "--headless=new", "--disable-gpu", "--no-first-run", "--no-default-browser-check",
		"--disable-background-networking", "--disable-component-update", "--disable-sync", "--disable-extensions",
		"--disable-default-apps", "--disable-domain-reliability", "--no-pings", "--proxy-server=127.0.0.1:9",
		"--user-data-dir="+profile, "--remote-debugging-address=127.0.0.1", "--remote-allow-origins=http://127.0.0.1",
		"--remote-debugging-port="+debug, "about:blank")
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true} // so its helpers go with it
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	defer func() {
		_ = syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
		_ = cmd.Wait()
	}()

	ws := devtools(t, debug)
	defer func() { _ = ws.Close() }()
	c := &cdp{t: t, ws: ws}
	// Injected by DevTools, so it runs before the page's own script and
	// isn't bound by its CSP.
	c.call("Page.addScriptToEvaluateOnNewDocument", map[string]any{"source": `window.__csp = [];
		document.addEventListener("securitypolicyviolation", e => window.__csp.push(e.violatedDirective + " " + e.blockedURI));`})
	c.call("Page.navigate", map[string]any{"url": base + "/"})

	var got struct {
		Rows   int      `json:"rows"`
		Sel    string   `json:"sel"`
		Title  string   `json:"title"`
		Count  string   `json:"count"`
		Frame  string   `json:"frame"`
		Height float64  `json:"height"`
		Inside string   `json:"inside"`
		CSP    []string `json:"csp"`
	}
	probe := `JSON.stringify({
		rows: document.querySelectorAll("#rows li").length,
		sel: (document.querySelector("#rows li.sel") || {dataset: {}}).dataset.id || "",
		title: document.getElementById("d-title").textContent,
		count: document.getElementById("count").textContent,
		frame: document.getElementById("frame").getAttribute("src") || "",
		height: document.getElementById("frame").getBoundingClientRect().height,
		inside: (() => { try { return document.getElementById("frame").contentDocument.body.textContent } catch (e) { return "blocked" } })(),
		csp: window.__csp || []
	})`
	deadline := time.Now().Add(15 * time.Second)
	for {
		// On a cold Chrome start the first probes can run before the page
		// has a DOM, or during navigation, and come back empty: not ready
		// yet rather than a failure.
		err := json.Unmarshal([]byte(c.eval(probe)), &got)
		if err == nil && got.Rows == 50 && got.Title != "" && got.Frame != "" {
			break
		}
		if time.Now().After(deadline) {
			if err != nil {
				t.Fatalf("probing the page: %v", err)
			}
			break
		}
		time.Sleep(100 * time.Millisecond)
	}
	if got.Rows != 50 || got.Sel != "newest" || got.Title != "Welcome, {first_name}" || got.Count != "121 messages" {
		t.Errorf("page: %+v", got)
	}
	if got.Frame != "/ui/messages/newest/html" || got.Height < 100 {
		t.Errorf("HTML frame src %q, %.0f px high", got.Frame, got.Height)
	}
	// The frame is sandboxed without allow-same-origin: the page can't
	// reach into it and it can't reach the page.
	if got.Inside != "blocked" && got.Inside != "" {
		t.Errorf("the page can read the message frame: %q", got.Inside)
	}
	if len(got.CSP) > 0 {
		t.Errorf("CSP violations: %q", got.CSP)
	}

	// A message arriving now shows up without a reload.
	m := inbox.Add(model.Message{ID: "live1", Channel: model.SMS, Provider: "twilio", To: []string{"+14155550101"}, Text: "Arrived live", Status: model.Faked})
	u.Notify(m)
	deadline = time.Now().Add(10 * time.Second)
	for !strings.Contains(c.eval(`document.getElementById("rows").textContent`), "Arrived live") {
		if time.Now().After(deadline) {
			t.Fatal("a new message never appeared")
		}
		time.Sleep(100 * time.Millisecond)
	}
}

// freePort returns a free port in 20400-20419 for Chrome's DevTools.
func freePort(t *testing.T) string {
	t.Helper()
	for port := 20419; port >= 20400; port-- {
		if l, err := net.Listen("tcp", fmt.Sprintf("127.0.0.1:%d", port)); err == nil {
			_ = l.Close()
			return fmt.Sprint(port)
		}
	}
	t.Skip("no free port in 20400-20419")
	return ""
}

// devtools connects to the first page target of the Chrome on port.
func devtools(t *testing.T, port string) *websocket.Conn {
	t.Helper()
	deadline := time.Now().Add(20 * time.Second)
	for {
		resp, err := http.Get("http://127.0.0.1:" + port + "/json/list") //nolint:noctx // test
		if err == nil {
			var targets []struct {
				Type string `json:"type"`
				URL  string `json:"webSocketDebuggerUrl"`
			}
			_ = json.NewDecoder(resp.Body).Decode(&targets)
			_ = resp.Body.Close()
			for _, tg := range targets {
				if tg.Type == "page" {
					ws, err := websocket.Dial(tg.URL, "", "http://127.0.0.1")
					if err != nil {
						t.Fatal(err)
					}
					return ws
				}
			}
		}
		if time.Now().After(deadline) {
			t.Fatalf("Chrome's DevTools never answered: %v", err)
		}
		time.Sleep(100 * time.Millisecond)
	}
}

// cdp sends DevTools commands one at a time and waits for each answer.
type cdp struct {
	t  *testing.T
	ws *websocket.Conn
	id int
}

func (c *cdp) call(method string, params any) json.RawMessage {
	c.t.Helper()
	c.id++
	if err := websocket.JSON.Send(c.ws, map[string]any{"id": c.id, "method": method, "params": params}); err != nil {
		c.t.Fatal(err)
	}
	_ = c.ws.SetReadDeadline(time.Now().Add(10 * time.Second))
	for {
		var msg struct {
			ID     int             `json:"id"`
			Result json.RawMessage `json:"result"`
			Error  *struct {
				Message string `json:"message"`
			} `json:"error"`
		}
		if err := websocket.JSON.Receive(c.ws, &msg); err != nil {
			c.t.Fatalf("%s: %v", method, err)
		}
		if msg.ID != c.id {
			continue // an event
		}
		if msg.Error != nil {
			c.t.Fatalf("%s: %s", method, msg.Error.Message)
		}
		return msg.Result
	}
}

func (c *cdp) eval(expr string) string {
	c.t.Helper()
	var r struct {
		Result struct {
			Value string `json:"value"`
		} `json:"result"`
	}
	_ = json.Unmarshal(c.call("Runtime.evaluate", map[string]any{"expression": expr, "returnByValue": true}), &r)
	return r.Result.Value
}
