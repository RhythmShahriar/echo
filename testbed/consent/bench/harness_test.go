// Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
// SPDX-License-Identifier: MIT

package main

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/RhythmShahriar/echo/consent"
	"github.com/RhythmShahriar/echo/model"
	"github.com/RhythmShahriar/echo/testbed/consent/judge"
	"github.com/RhythmShahriar/echo/testbed/consent/scenarios"
	"github.com/RhythmShahriar/echo/testbed/consent/server"
)

// TestPageStandInsFromTheTestbed renders real scenario pages. A tag the
// CMP holds back until Accept (c08's TikTok in the good state) is listed
// too: the Accept run loads it.
// fakeBench is a Bench in Go mode over a private testbed copy, running
// this test binary as rhythm-echo.
func fakeBench(t *testing.T) (*Bench, *goBed, string, string) {
	t.Helper()
	root, rec, work := testRoot(t), t.TempDir(), t.TempDir()
	env := append(os.Environ(), fakeEnv+"=bench", fakeDir+"="+rec, fakeRoot+"="+root)
	bed, err := newGoBed(root, "127.0.0.1:0", self(t), work, env, "", "", "")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(bed.Close)
	return newBench(bed, work, time.Minute, func(s string) { t.Log(s) }), bed, root, rec
}

func TestRunScenarioEndToEnd(t *testing.T) {
	b, bed, root, rec := fakeBench(t)
	m, err := scenarios.Load(root)
	if err != nil {
		t.Fatal(err)
	}
	c01, _ := m.Find("c01")
	g01, _ := m.Find("g01")
	c08, _ := m.Find("c08")
	caught := []consent.Issue{
		issue("consent/before-choice", "ga4", model.Error, "GA4 fired before the visitor chose"),
		issue("consent/no-default", "", model.Error, "No Consent Mode default")}
	record(t, rec, "c01", "good", fakeEnvelope(true, nil))
	record(t, rec, "c01", "broken", fakeEnvelope(true, caught))
	record(t, rec, "g01", "good", fakeEnvelope(true, nil))

	out := b.runScenario(t.Context(), jobOf(c01))
	if out.Echo.Outcome != judge.Caught || out.GoodState == nil || out.GoodState.Outcome != judge.Clean {
		t.Fatalf("c01 = %+v good %+v", out.Echo, out.GoodState)
	}
	if bed.srv.Broken("c01") {
		t.Error("c01 left broken")
	}
	if out := b.runScenario(t.Context(), jobOf(g01)); out.Echo.Outcome != judge.Clean || out.GoodState != nil {
		t.Errorf("g01 = %+v", out)
	}
	// The map flags reach rhythm-echo with every stand-in host.
	args, _ := os.ReadFile(filepath.Join(rec, "c01-broken.args"))
	for _, want := range []string{"consent", "https://c01.shop.test/", "--json", "--fail-on\nnone", "--map-only", "www.googletagmanager.com="} {
		if !strings.Contains(string(args), want) {
			t.Errorf("c01 args lack %q:\n%s", want, args)
		}
	}

	// A broken hold: one hit reaches the sink, so the catch doesn't count.
	if err := os.WriteFile(filepath.Join(rec, "c01-broken.leak"), nil, 0o600); err != nil {
		t.Fatal(err)
	}
	if out := b.runScenario(t.Context(), jobOf(c01)); out.Echo.Outcome != judge.Failed || !strings.Contains(out.Echo.Detail, "1 tracker hits reached the sink") {
		t.Errorf("c01 with a leak = %+v", out.Echo)
	}
	if b.leaks != 1 {
		t.Errorf("leaks = %d", b.leaks)
	}

	// A report whose scripts don't list the page's gtag: did not load.
	record(t, rec, "c01", "broken", fakeEnvelope(true, caught, func(r *consent.Result) { r.Pages[0].Runs[0].Scripts = r.Pages[0].Runs[0].Scripts[2:] }))
	_ = os.Remove(filepath.Join(rec, "c01-broken.leak"))
	if out := b.runScenario(t.Context(), jobOf(c01)); out.Echo.Outcome != judge.NotLoaded || !strings.Contains(out.Echo.Detail, "gtag") {
		t.Errorf("c01 not loaded = %+v", out.Echo)
	}

	// A comparison scenario saves its baseline in the good state.
	record(t, rec, "c08", "good", fakeEnvelope(true, nil))
	record(t, rec, "c08", "broken", fakeEnvelope(true, nil))
	b.runScenario(t.Context(), jobOf(c08))
	goodArgs, _ := os.ReadFile(filepath.Join(rec, "c08-good.args"))
	brokenArgs, _ := os.ReadFile(filepath.Join(rec, "c08-broken.args"))
	if !strings.Contains(string(goodArgs), "--save\ngood") || strings.Contains(string(goodArgs), "--against") || !strings.Contains(string(brokenArgs), "--against\ngood") {
		t.Errorf("c08 args: good %q, broken %q", goodArgs, brokenArgs)
	}

	// Clicks counted on good states only; timing from the reports.
	rows := b.clickRows()
	if len(rows) != 2 || rows[0].CMP != "complianz" || rows[0].AcceptNeeded == 0 || rows[0].AcceptOK != rows[0].AcceptNeeded {
		t.Errorf("clicks = %+v", rows)
	}
	if len(b.pages) == 0 || b.pages[0] != 20*time.Second {
		t.Errorf("page times = %v", b.pages)
	}
}

func TestControlPassEndToEnd(t *testing.T) {
	b, _, root, rec := fakeBench(t)
	m, _ := scenarios.Load(root)
	g01, _ := m.Find("g01")
	g04, _ := m.Find("g04")
	record(t, rec, "g01", "good", fakeEnvelope(true, nil))
	record(t, rec, "g04", "good", fakeEnvelope(true, []consent.Issue{issue("consent/no-first-layer-reject", "", model.Warning, "No Reject on the first layer")}))
	got := b.controlPass(t.Context(), []scenarios.Scenario{g01, g04}, 3, 2)
	if got[0].Rounds != 3 || got[0].FalseAlarms+got[0].Flakes+got[0].Failed != 0 {
		t.Errorf("g01 = %+v", got[0])
	}
	if got[1].FalseAlarms != 3 || got[1].Flakes != 0 {
		t.Errorf("g04 = %+v", got[1])
	}
}

func TestCLIEndToEnd(t *testing.T) {
	root, rec := testRoot(t), t.TempDir()
	t.Setenv(fakeEnv, "bench")
	t.Setenv(fakeDir, rec)
	t.Setenv(fakeRoot, root)
	caught := []consent.Issue{
		issue("consent/before-choice", "ga4", model.Error, "GA4 fired before the visitor chose"),
		issue("consent/no-default", "", model.Error, "No Consent Mode default")}
	record(t, rec, "c01", "good", fakeEnvelope(true, nil))
	record(t, rec, "c01", "broken", fakeEnvelope(true, caught))
	record(t, rec, "g01", "good", fakeEnvelope(true, nil))
	out := filepath.Join(t.TempDir(), "benchmark-consent.md")
	work := filepath.Join(t.TempDir(), "work")
	var stdout, stderr bytes.Buffer
	code := cli([]string{"-bin", self(t), "-root", root, "-work", work, "-out", out, "-only", "c01,g01", "-rounds", "2",
		"-listen", "127.0.0.1:0", "-skip", "probes,detect,fleet,tools"}, &stdout, &stderr)
	if code != 0 {
		t.Fatalf("exit %d:\n%s", code, stderr.String())
	}
	md, err := os.ReadFile(out)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{GeneratedHeader, "Echo caught 1 of 1 scenarios. False alarms: 0 in 4 checks of unchanged good sites.",
		"sink count 0, must be 0", "| c01 | GA4 before consent | complianz | caught |", "| g01 | 2 | 0 | 0 | 0 |", "Echo: rhythm-echo 9.9.9-test", "## Run it"} {
		if !strings.Contains(string(md), want) {
			t.Errorf("the scoreboard lacks %q:\n%s", want, md)
		}
	}
	var r Results
	data, _ := os.ReadFile(filepath.Join(work, "results.json"))
	if err := json.Unmarshal(data, &r); err != nil || len(r.Scenarios) != 2 || !r.Partial {
		t.Fatalf("results.json: %v %+v", err, r)
	}
	if _, err := os.Stat(filepath.Join(root, "state", "c01")); err == nil {
		t.Error("c01 left broken")
	}
	// A run whose broken state isn't caught fails the benchmark (exit 1).
	record(t, rec, "c01", "broken", fakeEnvelope(true, nil))
	stderr.Reset()
	if code := cli([]string{"-bin", self(t), "-root", root, "-work", work, "-out", out, "-only", "c01", "-rounds", "0",
		"-listen", "127.0.0.1:0", "-skip", "probes,detect,fleet,tools"}, &stdout, &stderr); code != 1 || !strings.Contains(stderr.String(), "c01 missed") {
		t.Errorf("a miss: exit %d, %s", code, stderr.String())
	}
	// Usage errors.
	for _, args := range [][]string{{"-mode", "kube"}, {"-skip", "everything"}, {"-diff", "a.json"}, {"-nope"}} {
		if code := cli(args, &stdout, &stderr); code != 2 {
			t.Errorf("%v: exit %d", args, code)
		}
	}
	if code := cli([]string{"-root", root, "-bin", self(t), "-only", "zz9"}, &stdout, &stderr); code != 3 || !strings.Contains(stderr.String(), "-only zz9") {
		t.Errorf("an unknown id: exit %d %s", code, stderr.String())
	}
}

func TestPicked(t *testing.T) {
	m, err := scenarios.Parse([]byte(`scenarios:
  - {id: c01, name: x, area: cmp, cmp: complianz, needs: go, toggle: x, expect: [{check: consent/no-default}]}
  - {id: c99, name: x, area: cmp, cmp: complianz, needs: docker, toggle: x, expect: [{check: consent/no-default}]}
  - {id: g01, name: x, area: cmp, cmp: complianz, needs: go, toggle: x, allow: []}
variants:
  - {id: c01-wp, of: c01, cmp: complianz-wp, site: c01-wp, needs: docker}
environment:
  - {id: e05, name: x, probe: interrupt, needs: local, toggle: x, good: [{step: s}], broken: [{step: s}]}
  - {id: e16, name: x, probe: ssrf, needs: docker, toggle: x, good: [{step: s}], broken: [{step: s}]}
`))
	if err != nil {
		t.Fatal(err)
	}
	ids := func(jobs []job) string {
		var out []string
		for _, j := range jobs {
			out = append(out, j.S.ID+"@"+j.Site+"/"+j.State)
		}
		return strings.Join(out, " ")
	}
	jobs, probes, err := picked(m, nil, "go")
	if err != nil || ids(jobs) != "c01@c01/c01 g01@g01/g01" || len(probes) != 2 {
		t.Errorf("go mode: %s, %d probes, %v", ids(jobs), len(probes), err)
	}
	jobs, _, _ = picked(m, nil, "docker")
	if ids(jobs) != "c01@c01/c01 c99@c99/c99 g01@g01/g01 c01-wp@c01-wp/c01" {
		t.Errorf("docker mode: %s", ids(jobs))
	}
	if v := jobs[3].S; len(v.Expect) != 1 || v.CMP != "complianz-wp" || v.Needs != "docker" {
		t.Errorf("the variant keeps c01's expectations: %+v", v)
	}
	jobs, probes, _ = picked(m, []string{"c01", "e05"}, "go")
	if ids(jobs) != "c01@c01/c01" || len(probes) != 1 || probes[0].ID != "e05" {
		t.Errorf("-only c01,e05: %s, %v", ids(jobs), probes)
	}
	if jobs, _, err := picked(m, []string{"c01-wp"}, "go"); err != nil || len(jobs) != 0 {
		t.Errorf("a variant id in Go mode: %v %v", jobs, err)
	}
	if _, _, err := picked(m, []string{"zz9"}, "go"); err == nil {
		t.Error("an unknown id: no error")
	}
}

func TestFleetFile(t *testing.T) {
	list := []scenarios.Scenario{{ID: "c01"}, {ID: "g01"}}
	path, err := fleetFile(t.TempDir(), list)
	if err != nil {
		t.Fatal(err)
	}
	data, _ := os.ReadFile(path)
	s := string(data)
	if strings.Count(s, "  - name: ") != 50 || !strings.Contains(s, "name: g01-repeat-24\n    url: https://g01.shop.test/?repeat=24") {
		t.Errorf("sites.yaml:\n%s", s)
	}
	if _, err := fleetFile(t.TempDir(), nil); err == nil {
		t.Error("no scenarios: no error")
	}
}

func TestBaselineSize(t *testing.T) {
	n, err := baselineSize("size-v1.1.2.txt")
	if err != nil || n < 10<<20 || n > 40<<20 {
		t.Errorf("size-v1.1.2.txt = %d, %v", n, err)
	}
	bad := filepath.Join(t.TempDir(), "s.txt")
	_ = os.WriteFile(bad, []byte("# nothing\n"), 0o600)
	if _, err := baselineSize(bad); err == nil {
		t.Error("no size: no error")
	}
}

func TestEchoVersion(t *testing.T) {
	t.Setenv(fakeEnv, "bench")
	if v := echoVersion(context.Background(), self(t)); v != "rhythm-echo 9.9.9-test" {
		t.Errorf("version = %q", v)
	}
	if v := echoVersion(context.Background(), filepath.Join(t.TempDir(), "none")); v != "unknown" {
		t.Errorf("no binary = %q", v)
	}
}

func TestDockerBedPaths(t *testing.T) {
	root := t.TempDir()
	if _, err := newDockerBed(root, filepath.Join(t.TempDir(), "elsewhere")); err == nil || !strings.Contains(err.Error(), "which the runner mounts") {
		t.Errorf("a work folder outside bench/: %v", err)
	}
	b := &dockerBed{compose: filepath.Join(root, "docker-compose.yml"), work: "/bench/work-a"}
	cmd := b.Command(t.Context(), "c01/good", []string{"consent", "https://c01.shop.test/"})
	got := strings.Join(cmd.Args, " ")
	for _, want := range []string{"compose -p echo-consent-testbed -f ", "exec -T -w /bench/work-a/c01/good runner /usr/local/bin/peakmem -o /bench/work-a/c01/good/.maxrss-kib rhythm-echo consent https://c01.shop.test/"} {
		if !strings.Contains(got, want) {
			t.Errorf("command %q lacks %q", got, want)
		}
	}
	if strings.Contains(got, "--map-host") {
		t.Error("Docker mode maps nothing: its resolver sends every name to the gate")
	}
}

func TestExecReadsTheRunnersPeakMemory(t *testing.T) {
	t.Setenv(fakeEnv, "bench")
	dir := t.TempDir()
	stale := func() { _ = os.WriteFile(filepath.Join(dir, rssFile), []byte("999999\n"), 0o600) }
	// Run locally, the mark of the process itself is read and a peak
	// file is never looked at.
	stale()
	cmd := execCommand(t.Context(), self(t), "--version")
	cmd.Dir = dir
	ex := run(cmd)
	if ex.Exit != 0 || !strings.Contains(ex.Stdout, "9.9.9-test") {
		t.Fatalf("exec = %+v", ex)
	}
	if ex.MaxRSS == 999999<<10 {
		t.Error("a stale peak memory file was read")
	}
	if runtime.GOOS == "linux" && ex.MaxRSS <= 0 {
		t.Errorf("no peak memory read for a local run: %d", ex.MaxRSS)
	}
	if ex.Wall <= 0 {
		t.Errorf("wall = %v", ex.Wall)
	}
	// Through peakmem, as in the runner: the file a previous run left
	// is removed first; the one written during the run is read after.
	stale()
	cmd = execCommand(t.Context(), self(t), peakmemBin, "-o", filepath.Join(dir, rssFile), "rhythm-echo", "--version")
	cmd.Dir = dir
	if ex = run(cmd); ex.Exit != 0 || ex.MaxRSS != 123456<<10 {
		t.Errorf("through peakmem = %+v", ex)
	}
	missing := run(execCommand(t.Context(), filepath.Join(dir, "none")))
	if missing.Exit != -1 || !strings.Contains(missing.Stderr, "bench: ") || missing.MaxRSS != 0 {
		t.Errorf("a missing binary = %+v", missing)
	}
}

// TestDockerBedSumsBothSinks: Docker mode reads the tracker hosts' sink
// (tags) and the sites' own (shop) and adds them up.
func TestDockerBedSumsBothSinks(t *testing.T) {
	var resets []string
	sink := func(c server.Count, hits []server.Hit) *httptest.Server {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			switch r.URL.Path {
			case "/_admin/health":
				_, _ = w.Write([]byte("ok\n"))
			case "/_sink/count":
				_ = json.NewEncoder(w).Encode(c)
			case "/_sink/hits":
				_ = json.NewEncoder(w).Encode(hits)
			case "/_sink/reset":
				resets = append(resets, r.URL.Query().Get("scenario"))
				w.WriteHeader(http.StatusNoContent)
			}
		}))
		t.Cleanup(srv.Close)
		return srv
	}
	t0 := time.Date(2026, 10, 26, 9, 0, 0, 0, time.UTC)
	tags := sink(server.Count{Hits: 2, By: map[string]int{"www.facebook.com/tr": 2}}, []server.Hit{{Host: "www.facebook.com", At: t0.Add(time.Second)}})
	shop := sink(server.Count{Hits: 1, Forbidden: 1, By: map[string]int{"metrics.c12.shop.test/g/collect": 1}}, []server.Hit{{Host: "metrics.c12.shop.test", At: t0}})
	b := &dockerBed{admins: []string{tags.URL, shop.URL}, client: http.DefaultClient}
	c, err := b.Count("c12")
	if err != nil || c.Hits != 3 || c.Forbidden != 1 || c.By["www.facebook.com/tr"] != 2 || c.By["metrics.c12.shop.test/g/collect"] != 1 {
		t.Errorf("count = %+v, %v", c, err)
	}
	hits, err := b.Hits("c12")
	if err != nil || len(hits) != 2 || hits[0].Host != "metrics.c12.shop.test" {
		t.Errorf("hits = %+v, %v", hits, err)
	}
	if err := b.ResetSink("c12"); err != nil || !slices.Equal(resets, []string{"c12", "c12"}) {
		t.Errorf("reset: %v %v", err, resets)
	}
	shop.Close()
	if _, err := b.Count("c12"); err == nil {
		t.Error("a sink that doesn't answer: no error")
	}
}

// TestLockState: a scenario and its Docker variant share one state file,
// so their runs never overlap.
func TestLockState(t *testing.T) {
	b := newBench(nil, t.TempDir(), time.Minute, nil)
	unlock := b.lockState("c01")
	got := make(chan bool, 1)
	go func() {
		u := b.lockState("c01")
		got <- true
		u()
	}()
	other := b.lockState("c02") // another state isn't held up
	other()
	select {
	case <-got:
		t.Fatal("c01-wp ran while c01 held the state")
	case <-time.After(50 * time.Millisecond):
	}
	unlock()
	select {
	case <-got:
	case <-time.After(5 * time.Second):
		t.Fatal("the state was never released")
	}
}
