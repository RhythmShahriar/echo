// Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
// SPDX-License-Identifier: MIT

package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/ysmood/gson"

	"github.com/RhythmShahriar/echo/internal/browser"
	"github.com/RhythmShahriar/echo/testbed/consent/scenarios"
	"github.com/RhythmShahriar/echo/testbed/consent/server"
)

// The other tools the benchmark judges on the same states, each only on
// what it claims to do (PRD 18.2). Blacklight and paid scanners are
// online services and are never run.
const (
	toolWEC         = "wec"
	toolAutoconsent = "autoconsent"
)

// toolNames are the column titles.
var toolNames = map[string]string{
	toolWEC:         "EDPS Website Evidence Collector",
	toolAutoconsent: "DuckDuckGo autoconsent",
}

// toolServer is a testbed server of the tools' own on a free loopback
// port with https, over the same state folder, so a tool's browser
// reaches the same states Echo checked and nothing else.
type toolServer struct {
	addr string
	l    net.Listener
}

// startToolServer serves root with a fresh CA in dir.
func startToolServer(root, dir string) (*toolServer, error) {
	caDir := filepath.Join(dir, "ca")
	if _, err := os.Stat(filepath.Join(caDir, "ca.pem")); err != nil {
		if err := server.MakeCA(caDir); err != nil {
			return nil, err
		}
	}
	ca, err := server.LoadCA(caDir)
	if err != nil {
		return nil, err
	}
	srv, err := server.New(server.Options{Root: root, CA: ca, Admin: "127.0.0.1:1"})
	if err != nil {
		return nil, err
	}
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return nil, err
	}
	go func() { _ = srv.Serve(l) }()
	return &toolServer{addr: l.Addr().String(), l: l}, nil
}

// resolverRule sends every name a browser asks for to the server.
func (t *toolServer) resolverRule() string { return "--host-resolver-rules=MAP * " + t.addr }

func (t *toolServer) close() { _ = t.l.Close() }

// wec runs the EDPS Website Evidence Collector (WEC_BIN) on a state
// without clicking anything and judges only "tracker before consent":
// a tracking beacon it lists is a catch on a bad scenario that expects
// consent/before-choice and a false alarm on a good state. Scenarios
// about anything else aren't claimed by it.
type wec struct {
	bin  string
	args []string // WEC_ARGS, split on spaces, for a version whose flags differ
	srv  *toolServer
	work string
}

// wecOutput is the part of WEC's JSON output the benchmark reads.
type wecOutput struct {
	Beacons []json.RawMessage `json:"beacons"`
}

// claims reports whether WEC claims to find s's bug: a tracker before
// the visitor chose.
func wecClaims(s scenarios.Scenario) bool {
	if s.Good() {
		return true
	}
	return slices.ContainsFunc(s.Expect, func(m scenarios.Match) bool { return m.Check == "consent/before-choice" })
}

// judge runs WEC on s in the state the bed is in now.
func (w *wec) judge(ctx context.Context, s scenarios.Scenario) Verdict {
	if !wecClaims(s) {
		return Verdict{Outcome: NotRun, Detail: "not something it claims to check"}
	}
	dir := filepath.Join(w.work, "wec", s.ID)
	if err := os.MkdirAll(dir, 0o750); err != nil {
		return Verdict{Outcome: Failed, Detail: err.Error()}
	}
	args := append([]string{siteURL(s.ID), "--no-output", "--json", "-q",
		"--browser-options=" + w.srv.resolverRule(), "--browser-options=--ignore-certificate-errors"}, w.args...)
	ctx, cancel := context.WithTimeout(ctx, 3*time.Minute)
	defer cancel()
	cmd := exec.CommandContext(ctx, w.bin, args...) //nolint:gosec // WEC_BIN, named by whoever runs the benchmark
	cmd.Dir = dir
	ex := run(cmd)
	var out wecOutput
	if err := json.Unmarshal([]byte(ex.Stdout), &out); err != nil {
		return Verdict{Outcome: Failed, Detail: fmt.Sprintf("no JSON on stdout (exit %d): %s", ex.Exit, tail(ex.Stderr, 160))}
	}
	return wecVerdict(s, len(out.Beacons))
}

// wecVerdict judges WEC's beacon count on s.
func wecVerdict(s scenarios.Scenario, beacons int) Verdict {
	switch {
	case s.Good() && beacons > 0:
		return Verdict{Outcome: FalseAlarm, Detail: fmt.Sprintf("%d beacons before any choice", beacons)}
	case s.Good():
		return Verdict{Outcome: Clean}
	case beacons > 0:
		return Verdict{Outcome: Caught, Found: fmt.Sprintf("%d beacons before any choice", beacons)}
	}
	return Verdict{Outcome: Missed, Detail: "no beacon before any choice"}
}

// autoconsent runs DuckDuckGo's autoconsent (AUTOCONSENT_DIR: the
// package folder with dist/autoconsent.playwright.js and
// rules/rules.json) in a plain Chrome on a replica's good state, opting
// out, as the reference column of the click table.
type autoconsent struct {
	script string
	rules  json.RawMessage
	srv    *toolServer

	once sync.Once
	b    *browser.Browser
	err  error
}

// newAutoconsent reads the bundle and rules from dir.
func newAutoconsent(dir string, srv *toolServer) (*autoconsent, error) {
	script, err := os.ReadFile(filepath.Join(dir, "dist", "autoconsent.playwright.js")) //nolint:gosec // AUTOCONSENT_DIR
	if err != nil {
		return nil, err
	}
	rules, err := os.ReadFile(filepath.Join(dir, "rules", "rules.json")) //nolint:gosec // as above
	if err != nil {
		return nil, err
	}
	if !json.Valid(rules) {
		return nil, errors.New("rules/rules.json isn't JSON")
	}
	return &autoconsent{script: string(script), rules: rules, srv: srv}, nil
}

// autoconsentConfig is the config the puppeteer guide gives, opting out.
var autoconsentConfig = map[string]any{
	"enabled": true, "autoAction": "optOut", "disabledCmps": []string{}, "enablePrehide": true,
	"enableCosmeticRules": true, "enableGeneratedRules": true, "detectRetries": 20, "isMainWorld": false,
	"prehideTimeout": 2000, "enableHeuristicDetection": true, "heuristicMode": "tier2",
	"logs": map[string]bool{"errors": false},
}

// judge opts out on s's start page and reports what autoconsent said.
func (a *autoconsent) judge(ctx context.Context, s scenarios.Scenario) string {
	a.once.Do(func() {
		a.b, a.err = browser.Launch(ctx, browser.Options{NoDownload: true, Insecure: true,
			Flags: []string{a.srv.resolverRule(), "--disable-background-networking"}})
	})
	if a.err != nil {
		return "not run: " + a.err.Error()
	}
	sess, err := a.b.Session()
	if err != nil {
		return "failed: " + err.Error()
	}
	inc, err := sess.Incognito()
	if err != nil {
		return "failed: " + err.Error()
	}
	defer func() { _ = inc.Close() }()
	p, err := browser.NewTab(inc)
	if err != nil {
		return "failed: " + err.Error()
	}
	p = p.Context(ctx).Timeout(time.Minute)
	result := make(chan string, 4)
	send := func(msg any) {
		go func() {
			_, _ = p.Eval(`m => window.autoconsentReceiveMessage && window.autoconsentReceiveMessage(m)`, msg)
		}()
	}
	if _, err := p.Expose("autoconsentSendMessage", func(j gson.JSON) (any, error) {
		switch j.Get("type").String() {
		case "init":
			send(map[string]any{"type": "initResp", "config": autoconsentConfig, "rules": a.rules})
		case "eval":
			go func() {
				r, err := p.Eval(`code => eval(code)`, j.Get("code").String())
				val := any(false)
				if err == nil {
					val = r.Value.Val()
				}
				send(map[string]any{"type": "evalResp", "id": j.Get("id").Val(), "result": val})
			}()
		case "optOutResult":
			if j.Get("result").Bool() {
				result <- "opted out (" + j.Get("cmp").String() + ")"
			} else {
				result <- "opt-out failed (" + j.Get("cmp").String() + ")"
			}
		}
		return nil, nil
	}); err != nil {
		return "failed: " + err.Error()
	}
	if _, err := p.EvalOnNewDocument(a.script); err != nil {
		return "failed: " + err.Error()
	}
	if err := p.Navigate(siteURL(s.ID)); err != nil {
		return "failed: " + err.Error()
	}
	select {
	case r := <-result:
		return r
	case <-time.After(30 * time.Second):
		return "no banner found"
	case <-ctx.Done():
		return "canceled"
	}
}

// close stops autoconsent's browser.
func (a *autoconsent) close() {
	if a.b != nil {
		a.b.Close()
	}
}

// Tools is the set of other tools this run judges.
type Tools struct {
	wec   *wec
	auto  *autoconsent
	srv   *toolServer
	notes map[string]string
}

// setupTools starts what WEC_BIN and AUTOCONSENT_DIR ask for; a tool
// that isn't asked for, or can't start, is "not run" with the reason.
func setupTools(root, work string, getenv func(string) string) *Tools {
	t := &Tools{notes: map[string]string{}}
	bin, dir := getenv("WEC_BIN"), getenv("AUTOCONSENT_DIR")
	if bin == "" {
		t.notes[toolWEC] = "not run: WEC_BIN isn't set"
	}
	if dir == "" {
		t.notes[toolAutoconsent] = "not run: AUTOCONSENT_DIR isn't set"
	}
	if bin == "" && dir == "" {
		return t
	}
	srv, err := startToolServer(root, filepath.Join(work, "tools"))
	if err != nil {
		for _, n := range []string{toolWEC, toolAutoconsent} {
			if t.notes[n] == "" {
				t.notes[n] = "not run: " + err.Error()
			}
		}
		return t
	}
	t.srv = srv
	if bin != "" {
		t.wec = &wec{bin: bin, args: strings.Fields(getenv("WEC_ARGS")), srv: srv, work: work}
	}
	if dir != "" {
		a, err := newAutoconsent(dir, srv)
		if err != nil {
			t.notes[toolAutoconsent] = "not run: " + err.Error()
		} else {
			t.auto = a
		}
	}
	return t
}

// close stops the tools' browser and server.
func (t *Tools) close() {
	if t.auto != nil {
		t.auto.close()
	}
	if t.srv != nil {
		t.srv.close()
	}
}
