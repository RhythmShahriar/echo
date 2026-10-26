// Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
// SPDX-License-Identifier: MIT

package main

import (
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/RhythmShahriar/echo/consent"
	"github.com/RhythmShahriar/echo/model"
	"github.com/RhythmShahriar/echo/report"
	"github.com/RhythmShahriar/echo/testbed/consent/judge"
)

// The test binary doubles as a fake rhythm-echo: with fakeEnv set it
// answers the benchmark's commands from recorded envelopes instead of
// running a browser, so the harness is tested end to end without one.
const (
	fakeEnv  = "BENCH_FAKE_ECHO" // the fake's mode: bench, site or interrupt
	fakeDir  = "BENCH_FAKE_DIR"  // recorded envelopes: <site>-<state>.json
	fakeRoot = "BENCH_FAKE_ROOT" // the testbed root whose state/ says the state
)

func TestMain(m *testing.M) {
	if mode := os.Getenv(fakeEnv); mode != "" {
		os.Exit(fakeEcho(mode, os.Args[1:]))
	}
	os.Exit(m.Run())
}

// fakeArgs is what the fake reads from a command line.
type fakeArgs struct {
	url, out, mapAddr string
	all               []string
}

func parseFake(args []string) fakeArgs {
	f := fakeArgs{all: args}
	for i, a := range args {
		switch {
		case strings.HasPrefix(a, "https://"):
			if f.url == "" {
				f.url = a
			}
		case a == "-o" && i+1 < len(args):
			f.out = args[i+1]
		case a == "--map-host" && i+1 < len(args) && strings.HasPrefix(args[i+1], "*.shop.test="):
			f.mapAddr = strings.TrimPrefix(args[i+1], "*.shop.test=")
		}
	}
	return f
}

// siteID is the scenario label of a testbed URL.
func (f fakeArgs) siteID() string {
	h := strings.TrimPrefix(f.url, "https://")
	h, _, _ = strings.Cut(h, "/")
	id, _, _ := strings.Cut(h, ".")
	return id
}

func fakeEcho(mode string, args []string) int {
	if len(args) > 0 && args[0] == "--version" {
		fmt.Println("rhythm-echo 9.9.9-test")
		return 0
	}
	f := parseFake(args)
	id := f.siteID()
	switch mode {
	case "site":
		// Like Echo on a site that's down: exit 3, never "all clear".
		req, _ := http.NewRequest(http.MethodGet, "http://"+f.mapAddr+"/", nil)
		req.Host = id + ".shop.test"
		resp, err := (&http.Client{Timeout: 5 * time.Second}).Do(req)
		if err != nil {
			fmt.Fprintf(os.Stderr, "rhythm echo: %s didn't answer: %v\n", f.url, err)
			return 3
		}
		_ = resp.Body.Close()
		return writeFake(f.out, fakeEnvelope(true, nil), 0)
	case "interrupt":
		// Starts the Reject run, makes a profile under TMPDIR and waits a
		// little for Ctrl-C; on it, cleans up and exits 130. It listens
		// before it writes the events file the probe waits for: on a
		// loaded machine the probe's Ctrl-C can otherwise land first and
		// kill the fake with its profile left behind.
		sig := make(chan os.Signal, 1)
		signal.Notify(sig, os.Interrupt)
		tmp := os.Getenv("TMPDIR")
		if tmp == "" {
			tmp = os.Getenv("TEMP")
		}
		profile := filepath.Join(tmp, "rod", "user-data", "p1")
		_ = os.MkdirAll(profile, 0o750)
		events := filepath.Join(".echo", "consent", "r1", "events")
		_ = os.MkdirAll(events, 0o750)
		_ = os.WriteFile(filepath.Join(events, "page-1-reject.ndjson"), []byte("{}\n"), 0o600)
		select {
		case <-sig:
			_ = os.RemoveAll(filepath.Join(tmp, "rod"))
			return writeFake(f.out, fakeEnvelope(false, nil), 130)
		case <-time.After(3 * time.Second):
			_ = os.RemoveAll(filepath.Join(tmp, "rod"))
			return writeFake(f.out, fakeEnvelope(true, nil), 0)
		}
	}
	// bench: replay the recorded envelope for the site and state.
	state := "good"
	if _, err := os.Stat(filepath.Join(os.Getenv(fakeRoot), "state", id)); err == nil {
		state = "broken"
	}
	dir := os.Getenv(fakeDir)
	base := filepath.Join(dir, id+"-"+state)
	_ = os.WriteFile(base+".args", []byte(strings.Join(args, "\n")), 0o600)
	if _, err := os.Stat(base + ".leak"); err == nil {
		// A hold that leaks: one hit reaches the sink.
		req, _ := http.NewRequest(http.MethodGet, "http://"+f.mapAddr+"/g/collect?v=2&_tbrun="+id, nil)
		req.Host = "www.google-analytics.com"
		if resp, err := http.DefaultClient.Do(req); err == nil {
			_ = resp.Body.Close()
		}
	}
	data, err := os.ReadFile(base + ".json")
	if err != nil {
		fmt.Fprintln(os.Stderr, "fake rhythm-echo: no recording for", id, state)
		return 3
	}
	if err := os.WriteFile(f.out, data, 0o600); err != nil {
		return 3
	}
	return 0
}

// writeFake writes e to out and returns code.
func writeFake(out string, e *judge.Envelope, code int) int {
	if out != "" {
		data, _ := json.Marshal(e)
		_ = os.WriteFile(out, data, 0o600)
	}
	return code
}

// allScripts lists a script at every stand-in's address.
func allScripts() []consent.Script {
	var out []consent.Script
	for _, s := range judge.StandIns {
		out = append(out, consent.Script{Tracker: s.Name, URL: "https://" + s.Prefix + "x.js"})
	}
	return out
}

// fakeEnvelope is a consent envelope with issues and complete set, a
// default three-run page with every stand-in loaded.
func fakeEnvelope(complete bool, issues []consent.Issue, mods ...func(*consent.Result)) *judge.Envelope {
	r := consent.Result{ID: "r1", Browser: "Chrome 156.0.0.0", DurationMS: 20000, Issues: issues,
		Pages: []consent.Page{{URL: "https://x.shop.test/", CMP: consent.CMPInfo{ID: "complianz", Found: true},
			Runs: []consent.Run{
				{Kind: consent.RunNone, Status: consent.StatusOK,
					Scripts: allScripts()},
				{Kind: consent.RunAccept, Status: consent.StatusOK, Choice: &consent.Choice{How: "button", Verified: true}},
				{Kind: consent.RunReject, Status: consent.StatusOK, Choice: &consent.Choice{How: "button", Verified: true}},
			}}}}
	for _, m := range mods {
		m(&r)
	}
	e := &judge.Envelope{Schema: model.Schema, Kind: string(report.KindConsent), Version: "9.9.9", Complete: complete, Data: r}
	e.Verdict = "ok"
	if len(issues) > 0 {
		e.Verdict = "findings"
	}
	if !complete && len(issues) == 0 {
		e.Verdict = "error"
	}
	return e
}

// issue is an issue of check on tracker at level with title.
func issue(check, tracker string, level model.Level, title string) consent.Issue {
	return consent.Issue{Check: check, Tracker: tracker, Level: level, Title: title, Run: consent.RunNone,
		Key: consent.IssueKey(check, "/", consent.RunNone, tracker)}
}

// record writes e as the fake's recording for site in state.
func record(t *testing.T, dir, site, state string, e *judge.Envelope) {
	t.Helper()
	data, err := json.Marshal(e)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, site+"-"+state+".json"), data, 0o600); err != nil {
		t.Fatal(err)
	}
}

// testRoot is a private copy of the testbed (manifest and sites) with
// an empty state folder, so tests never switch the real one.
func testRoot(t *testing.T) string {
	t.Helper()
	root := filepath.Join(t.TempDir(), "consent")
	if err := os.CopyFS(filepath.Join(root, "sites"), os.DirFS(filepath.Join("..", "sites"))); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(filepath.Join("..", "scenarios.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "scenarios.yaml"), data, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(root, "bench"), 0o750); err != nil {
		t.Fatal(err)
	}
	size, err := os.ReadFile("size-v1.1.2.txt")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "bench", "size-v1.1.2.txt"), size, 0o600); err != nil {
		t.Fatal(err)
	}
	return root
}

// self is this test binary, the fake rhythm-echo.
func self(t *testing.T) string {
	t.Helper()
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	return exe
}
