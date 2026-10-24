// Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
// SPDX-License-Identifier: MIT

//go:build testbed

package main

import (
	"context"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/RhythmShahriar/echo/config"
	"github.com/RhythmShahriar/echo/model"
	"github.com/RhythmShahriar/echo/store"
)

// TestManifest and TestFixtureDNS need nothing running. TestGoodStateIsClean
// needs the testbed (make -C testbed up) and skips without it:
//
//	go test -tags testbed -v ./testbed/bench/

func testbedDir(t *testing.T) (string, *Manifest) {
	t.Helper()
	dir, err := Testbed()
	if err != nil {
		t.Fatal(err)
	}
	m, err := LoadManifest(filepath.Join(dir, "scenarios.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	return dir, m
}

// TestManifest checks that every scenario names an action that exists in
// the testbed's Echo config, that observe-mode scenarios only use actions
// that stay inside the testbed and that toggle.sh knows every scenario.
func TestManifest(t *testing.T) {
	dir, m := testbedDir(t)
	cfg, err := config.Load(filepath.Join(dir, ".echo", "config.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	var names []string
	for _, a := range cfg.Actions {
		names = append(names, a.Name)
	}
	// Observe mode sends for real: only these actions are known to reach
	// nothing but the testbed's CRM and Mailpit.
	local := []string{"Order webhook to the CRM", "Resend the receipt for order 1000"}
	for _, s := range m.Scenarios {
		if !slices.Contains(names, s.Action) {
			t.Errorf("%s: action %q isn't in .echo/config.yaml", s.ID, s.Action)
		}
		if s.Mode == "observe" && !slices.Contains(local, s.Action) {
			t.Errorf("%s: observe mode with action %q, which may reach real services", s.ID, s.Action)
		}
	}
	if len(m.Scenarios) != 20 {
		t.Errorf("%d scenarios, want 20", len(m.Scenarios))
	}
	for _, s := range m.Scenarios {
		if err := Toggle(dir, s.ID, "good"); err != nil {
			t.Errorf("%s: %v", s.ID, err)
		}
	}
}

// TestFixtureDNS serves the zone and checks a record in both states of s18.
func TestFixtureDNS(t *testing.T) {
	dir, _ := testbedDir(t)
	zone, err := LoadZone(filepath.Join(dir, "dns", "zone.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	state := t.TempDir()
	srv := &DNSServer{Zone: zone, State: state}
	if err := srv.Start("127.0.0.1:0"); err != nil {
		t.Fatal(err)
	}
	defer srv.Close()
	addr := srv.udp.LocalAddr().String()
	ctx := context.Background()
	spf := func() string {
		txt, err := lookupTXT(ctx, addr, "shop.example.com")
		if err != nil || len(txt) != 1 {
			t.Fatalf("TXT shop.example.com: %v %v", txt, err)
		}
		return txt[0]
	}
	if n := strings.Count(spf(), "include:"); n != 8 {
		t.Errorf("good SPF has %d includes, want 8", n)
	}
	if err := os.WriteFile(filepath.Join(state, "s18"), nil, 0o600); err != nil {
		t.Fatal(err)
	}
	if n := strings.Count(spf(), "include:"); n != 11 {
		t.Errorf("broken SPF has %d includes, want 11", n)
	}
	// A DKIM key over 255 bytes, through a CNAME.
	txt, err := lookupTXT(ctx, addr, "s1._domainkey.shop.example.com")
	if err != nil || len(txt) != 1 || !strings.HasPrefix(txt[0], "v=DKIM1;") || len(txt[0]) < 300 {
		t.Errorf("DKIM s1: %q %v", txt, err)
	}
	if txt, err := lookupTXT(ctx, addr, "nothing.example.org"); err != nil || txt != nil {
		t.Errorf("a name outside the zone: %q %v, want not found", txt, err)
	}
}

// buildEcho builds the binary under test from the module the testbed
// folder dir is in.
func buildEcho(t *testing.T, dir string) string {
	t.Helper()
	bin := filepath.Join(t.TempDir(), "rhythm-echo")
	goBin, err := exec.LookPath("go")
	if err != nil {
		goBin = "/usr/local/go/bin/go"
	}
	build := exec.Command(goBin, "build", "-o", bin, "./cmd/rhythm-echo") //nolint:noctx,gosec // building the binary under test
	build.Dir, build.Env = filepath.Dir(dir), append(os.Environ(), "GOWORK=off")
	if out, err := build.CombinedOutput(); err != nil {
		t.Fatalf("go build: %v: %s", err, out)
	}
	return bin
}

// TestReadsEchoJSON feeds readComparison and checkIssues what the binary
// under test writes with --json, so the benchmark follows its output. It
// needs no testbed: Echo runs on 127.0.0.1:22300-22302 with one command
// action that sends an email through the sendmail stand-in and stops
// sending it.
func TestReadsEchoJSON(t *testing.T) {
	if testing.Short() {
		t.Skip("builds and starts Echo")
	}
	const api, smtp, dnsAddr = "127.0.0.1:22300", "127.0.0.1:22301", "127.0.0.1:22302"
	dir, _ := testbedDir(t)
	bin := buildEcho(t, dir)
	wd := t.TempDir()
	env := append(os.Environ(), "RHYTHM_ECHO_URL=http://"+api, "RHYTHM_ECHO_TOKEN=", "RHYTHM_ECHO_SMTP=")
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()

	up := exec.CommandContext(ctx, bin, "up", "--seal", "--listen", api, "--smtp", smtp, "--plain") //nolint:gosec // the binary this test built
	up.Dir, up.Env = wd, env
	var log strings.Builder
	up.Stdout, up.Stderr = &log, &log
	if err := up.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = up.Process.Kill(); _ = up.Wait() })
	for i := 0; ; i++ {
		resp, err := http.Get("http://" + api + "/v1/health") //nolint:noctx // the Echo this test started
		if err == nil {
			_ = resp.Body.Close()
			break
		}
		if i == 100 {
			t.Fatalf("rhythm-echo up didn't answer: %v\n%s", err, log.String())
		}
		time.Sleep(100 * time.Millisecond)
	}

	config := func(cmd string) {
		t.Helper()
		yaml := "actions:\n  - name: Welcome\n    command: " + strconv.Quote(cmd) + "\n"
		if err := os.MkdirAll(filepath.Join(wd, ".echo"), 0o750); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(wd, ".echo", "config.yaml"), []byte(yaml), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	echo := func(args ...string) string {
		t.Helper()
		out, err := command(ctx, time.Minute, wd, env, bin, args...)
		if err != nil {
			t.Fatal(err)
		}
		return out
	}
	config(`printf 'From: shop@shop.example.com\nTo: ann@shop.test\nSubject: Welcome\n\nHi Ann\n' | '` + bin + `' sendmail -t`)
	echo("snapshot", "--name", "good", "--fail-on", "none", "--json", "-o", "good.json")
	good, err := store.LoadRun(filepath.Join(wd, ".echo", "snapshots", "good"))
	if err != nil || len(good.Actions) != 1 || len(good.Actions[0].Messages) != 1 {
		t.Fatalf("the good snapshot: %v %+v", err, good)
	}
	config("true")
	echo("compare", "--snapshot", "good", "--fail-on", "none", "--json", "-o", "compare.json")
	c, err := readComparison(filepath.Join(wd, "compare.json"))
	if err != nil {
		t.Fatal(err)
	}
	if c.Actions != 1 || c.Count(model.Broke) == 0 || c.After.Dir == "" {
		t.Errorf("readComparison: %d actions, %d broke, after %q", c.Actions, c.Count(model.Broke), c.After.Dir)
	}
	if _, err := store.LoadRun(under(wd, c.After.Dir)); err != nil {
		t.Errorf("the compared run: %v", err)
	}
	if fromDomains(good)[0] != "shop.example.com" {
		t.Errorf("fromDomains: %v", fromDomains(good))
	}

	// s18 broken: SPF needs more than 10 lookups, which check reports.
	zone, err := LoadZone(filepath.Join(dir, "dns", "zone.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	state := t.TempDir()
	if err := os.WriteFile(filepath.Join(state, "s18"), nil, 0o600); err != nil {
		t.Fatal(err)
	}
	srv := &DNSServer{Zone: zone, State: state}
	if err := srv.Start(dnsAddr); err != nil {
		t.Fatal(err)
	}
	defer srv.Close()
	issues, err := checkIssues([]byte(echo("check", "shop.example.com", "--fail-on", "none", "--json", "--resolver", dnsAddr)))
	if err != nil {
		t.Fatal(err)
	}
	if !slices.ContainsFunc(issues, func(i model.Issue) bool { return i.Check == "dns/spf" }) {
		t.Errorf("checkIssues: no SPF issue in %+v", issues)
	}
	if _, err := readComparison(filepath.Join(wd, "good.json")); err == nil {
		t.Error("readComparison read a run as a comparison")
	}
}

// TestGoodStateIsClean runs every action in the testbed's Echo config with
// every scenario good, through the CLI as a user would and checks that
// each sent what it should and nothing is above a notice: a caught
// scenario is caught because of its broken state alone.
func TestGoodStateIsClean(t *testing.T) {
	dir, _ := testbedDir(t)
	for _, u := range []string{"http://localhost:18850/", "http://localhost:18851/", "http://localhost:18852/", "http://localhost:18853/"} {
		resp, err := http.Get(u) //nolint:gosec,noctx // the local testbed
		if err != nil {
			t.Skipf("testbed isn't running (make -C testbed up): %v", err)
		}
		_ = resp.Body.Close()
	}
	bin := buildEcho(t, dir)
	if err := Toggle(dir, "all", "good"); err != nil {
		t.Fatal(err)
	}
	work := t.TempDir()
	e := &Echo{Bin: bin, Testbed: dir, Work: work}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()
	if err := e.Start(ctx, "seal"); err != nil {
		t.Fatal(err)
	}
	defer e.Stop()
	if _, err := e.Run(ctx, 4*time.Minute, work, "snapshot", "--config", e.Config(), "--name", "clean", "--fail-on", "none", "--json", "-o", "clean.json"); err != nil {
		t.Fatal(err)
	}
	r, err := store.LoadRun(filepath.Join(work, ".echo", "snapshots", "clean"))
	if err != nil {
		t.Fatal(err)
	}
	for _, a := range r.Actions {
		if a.Error != "" {
			t.Errorf("%s: %s", a.Label, a.Error)
		}
		if len(a.Messages) == 0 {
			t.Errorf("%s: sent nothing", a.Label)
		}
		for _, m := range a.Messages {
			if m.Status != model.Faked {
				t.Errorf("%s: %s %s is %s, want faked (sealed)", a.Label, m.Channel, m.URL+m.Subject, m.Status)
			}
		}
		for _, i := range a.Issues {
			if i.Level != model.Notice {
				t.Errorf("%s: %s %s: %s", a.Label, i.Level, i.Check, i.Title)
			}
		}
		t.Logf("%s: %d messages", a.Label, len(a.Messages))
	}
	for _, i := range r.Issues {
		if i.Level != model.Notice {
			t.Errorf("run: %s %s: %s", i.Level, i.Check, i.Title)
		}
	}
}
