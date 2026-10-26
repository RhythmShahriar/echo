// Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
// SPDX-License-Identifier: MIT

package main

import (
	"fmt"
	"net"
	"net/url"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/RhythmShahriar/echo/testbed/consent/scenarios"
)

// TestProbesListed: e01 to e18 are in scenarios.yaml's environment:,
// each with a probe function and good and broken checks.
func TestProbesListed(t *testing.T) {
	m, err := scenarios.Load("..")
	if err != nil {
		t.Fatal(err)
	}
	for i := 1; i <= 18; i++ {
		id := fmt.Sprintf("e%02d", i)
		p, ok := m.FindProbe(id)
		switch {
		case !ok:
			t.Errorf("%s isn't in scenarios.yaml environment:", id)
		case probeFuncs[p.Probe] == nil:
			t.Errorf("%s names probe %q, which bench/probes.go doesn't have", id, p.Probe)
		case len(p.Good) == 0 || len(p.Broken) == 0:
			t.Errorf("%s needs a good and a broken expectation", id)
		}
	}
	if len(m.Environment) != 18 {
		t.Errorf("%d probes, want e01 to e18", len(m.Environment))
	}
	// Every probe function is used by the manifest.
	for name := range probeFuncs {
		if !slices.ContainsFunc(m.Environment, func(p scenarios.Probe) bool { return p.Probe == name }) {
			t.Errorf("probe %s isn't used by any scenario", name)
		}
	}
	// Only e16 needs Docker: its broken page asks for the metadata address.
	for _, p := range m.Environment {
		if (p.Needs == "docker") != (p.ID == "e16") {
			t.Errorf("%s needs %s", p.ID, p.Needs)
		}
	}
}

func TestVerifyProbe(t *testing.T) {
	three, f, tr := 3, false, true
	steps := []Step{{Name: "check", Exit: 3, Verdict: "error", Complete: &f, Output: "rhythm echo: https://g12.shop.test/ didn't answer\nchrome left: 0"}}
	for _, c := range []struct {
		check scenarios.Check
		want  string
	}{
		{scenarios.Check{Step: "check", Exit: &three, Verdict: "error", Complete: &f, Says: []string{"DIDN'T ANSWER", "chrome left: 0"}, Never: []string{"all clear"}}, ""},
		{scenarios.Check{Step: "other"}, "other: the probe has no such step"},
		{scenarios.Check{Step: "check", Exit: new(int)}, "check: exit 3, want 0"},
		{scenarios.Check{Step: "check", Verdict: "ok"}, `check: verdict "error", want "ok"`},
		{scenarios.Check{Step: "check", Complete: &tr}, "check: complete false, want true"},
		{scenarios.Check{Step: "check", Says: []string{"bot check"}}, `check: doesn't say "bot check"`},
		{scenarios.Check{Step: "check", Never: []string{"didn't"}}, `check: says "didn't"`},
	} {
		if got := verifyProbe([]scenarios.Check{c.check}, steps); !strings.HasPrefix(got, c.want) || (c.want == "" && got != "") {
			t.Errorf("%s: %q, want %q", c.check, got, c.want)
		}
	}
	noReport := []Step{{Name: "check"}}
	if got := verifyProbe([]scenarios.Check{{Step: "check", Complete: &f}}, noReport); got != "check: complete no report, want false" {
		t.Errorf("no report: %q", got)
	}
}

func TestEvaluateProbe(t *testing.T) {
	zero, three := 0, 3
	pr := scenarios.Probe{ID: "e01",
		Good:   []scenarios.Check{{Step: "check", Exit: &zero}},
		Broken: []scenarios.Check{{Step: "check", Exit: &three, Never: []string{"all clear"}}}}
	ok := []Step{{Name: "check", Exit: 0}}
	down := []Step{{Name: "check", Exit: 3}}
	if v := evaluateProbe(pr, ok, down); v.Outcome != Caught || v.Found != `check: exit 3, never "all clear"` {
		t.Errorf("caught = %+v", v)
	}
	if v := evaluateProbe(pr, ok, []Step{{Name: "check", Exit: 3, Output: "All clear"}}); v.Outcome != Missed {
		t.Errorf("an all clear = %+v", v)
	}
	if v := evaluateProbe(pr, down, down); v.Outcome != Failed || !strings.HasPrefix(v.Detail, "good state: ") {
		t.Errorf("a failing good state = %+v", v)
	}
	// A probe the manifest names that doesn't exist, a Docker probe in
	// Go mode.
	p := &ProbeEnv{Work: t.TempDir()}
	if out := runProbe(t.Context(), p, scenarios.Probe{ID: "e99", Probe: "nope"}); out.Echo.Outcome != Failed {
		t.Errorf("no such probe = %+v", out.Echo)
	}
	if out := runProbe(t.Context(), p, scenarios.Probe{ID: "e16", Probe: "ssrf", Needs: "docker"}); out.Echo.Outcome != NotRun {
		t.Errorf("e16 in Go mode = %+v", out.Echo)
	}
}

func TestFindSecrets(t *testing.T) {
	dir := t.TempDir()
	token := fakeToken()
	if !strings.HasPrefix(token, "tbtok_") || token == fakeToken() {
		t.Fatalf("fakeToken = %q", token)
	}
	email := "buyer@shop.test"
	_ = os.MkdirAll(filepath.Join(dir, ".echo", "consent", "r1"), 0o750)
	_ = os.WriteFile(filepath.Join(dir, ".echo", "consent", "r1", "consent.json"), []byte(`{"url": "dl=`+url.QueryEscape(url.QueryEscape(email))+`"}`), 0o600)
	_ = os.WriteFile(filepath.Join(dir, "report.md"), []byte("token=••••"+token[len(token)-4:]), 0o600)
	// The probe's own copy of the sites is never scanned.
	_ = os.MkdirAll(filepath.Join(dir, "testbed"), 0o750)
	_ = os.WriteFile(filepath.Join(dir, "testbed", "x"), []byte(token), 0o600)
	got := findSecrets(dir, "GET /?token="+url.QueryEscape(token), []string{token, email})
	want := []string{"secret 1 in the output", "secret 2 in .echo/consent/r1/consent.json"}
	if !slices.Equal(got, want) {
		t.Errorf("findSecrets = %q, want %q", got, want)
	}
	if got := findSecrets(dir, "masked ••••", []string{"nothing-like-this"}); len(got) != 0 {
		t.Errorf("no secret: %q", got)
	}
	// The list names the place, never the value.
	for _, g := range got {
		if strings.Contains(g, token) {
			t.Error("the value is in the note")
		}
	}
}

func TestUnmatched(t *testing.T) {
	sink := map[string]int{"www.google-analytics.com/g/collect": 2, "www.facebook.com/tr": 1}
	if n := unmatched(sink, map[string]int{"www.google-analytics.com/g/collect": 2, "www.facebook.com/tr": 1}); n != 0 {
		t.Errorf("one for one = %d", n)
	}
	reported := map[string]int{"www.google-analytics.com/g/collect": 1, "analytics.tiktok.com/api/v2/pixel": 1}
	if n := unmatched(sink, reported); n != 3 {
		t.Errorf("unmatched = %d", n)
	}
	d := describeUnmatched(sink, reported)
	for _, want := range []string{"www.facebook.com/tr sink 1 reported 0", "analytics.tiktok.com/api/v2/pixel sink 0 reported 1", "www.google-analytics.com/g/collect sink 2 reported 1"} {
		if !strings.Contains(d, want) {
			t.Errorf("describe = %q lacks %q", d, want)
		}
	}
	if total(sink) != 3 {
		t.Errorf("total = %d", total(sink))
	}
}

func TestProcessesWith(t *testing.T) {
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	n, err := processesWith(t.Context(), filepath.Base(exe))
	if err != nil {
		t.Skip("can't list processes here:", err)
	}
	if n < 1 {
		t.Errorf("this test's own process isn't listed (%s)", filepath.Base(exe))
	}
	if n, _ := processesWith(t.Context(), "no-such-process-"+fakeToken()); n != 0 {
		t.Errorf("a made-up name: %d", n)
	}
	dir := t.TempDir()
	_ = os.MkdirAll(filepath.Join(dir, "rod", "user-data", "p1"), 0o750)
	if n := entriesUnder(dir); n != 3 {
		t.Errorf("entries = %d", n)
	}
	if n := entriesUnder(filepath.Join(dir, "none")); n != 0 {
		t.Errorf("no folder = %d", n)
	}
}

// freeBase finds a free loopback port to start the probe ports from.
func freeBase(t *testing.T) int {
	t.Helper()
	for range 20 {
		l, err := net.Listen("tcp", "127.0.0.1:0")
		if err != nil {
			t.Fatal(err)
		}
		port := l.Addr().(*net.TCPAddr).Port
		_ = l.Close()
		ok := true
		for off := 1; off < 4; off++ {
			if l2, err := net.Listen("tcp", fmt.Sprintf("127.0.0.1:%d", port+off)); err != nil {
				ok = false
			} else {
				_ = l2.Close()
			}
		}
		if ok && port+4 < 65535 {
			return port
		}
	}
	t.Skip("no four free ports in a row")
	return 0
}

// probeEnv is a ProbeEnv that runs the fake rhythm-echo in mode.
func probeEnv(t *testing.T, mode string) *ProbeEnv {
	t.Helper()
	t.Setenv(fakeEnv, mode)
	return &ProbeEnv{Bin: self(t), Root: "..", Work: t.TempDir(), Timeout: time.Minute, Port: freeBase(t)}
}

// TestSiteDownProbe runs e01 against the fake: the good state reaches
// the probe's server, the broken one finds it down.
func TestSiteDownProbe(t *testing.T) {
	m, err := scenarios.Load("..")
	if err != nil {
		t.Fatal(err)
	}
	e01, _ := m.FindProbe("e01")
	out := runProbe(t.Context(), probeEnv(t, "site"), e01)
	if out.Echo.Outcome != Caught {
		t.Fatalf("e01 = %+v\ngood %+v\nbroken %+v", out.Echo, out.Good, out.Broken)
	}
	if out.Good[0].Verdict != "ok" || out.Broken[0].Exit != 3 {
		t.Errorf("steps: good %+v, broken %+v", out.Good, out.Broken)
	}
}

// TestInterruptProbe runs e05 against the fake, which starts its Reject
// run, makes a profile under its TMPDIR and exits 130 on Ctrl-C after
// removing it: on Windows through a console Ctrl-Break to its group.
func TestInterruptProbe(t *testing.T) {
	m, err := scenarios.Load("..")
	if err != nil {
		t.Fatal(err)
	}
	e05, _ := m.FindProbe("e05")
	out := runProbe(t.Context(), probeEnv(t, "interrupt"), e05)
	if out.Echo.Outcome != Caught {
		t.Fatalf("e05 = %+v\ngood %+v\nbroken %+v", out.Echo, out.Good, out.Broken)
	}
	if !strings.Contains(out.Broken[0].Output, "reject run seen before the interrupt: true") {
		t.Errorf("broken = %s", out.Broken[0].Output)
	}
}

// TestInterruptLeftovers: a child that leaves its profile behind is
// counted, so e05 can't pass on an Echo that doesn't clean up.
func TestInterruptLeftovers(t *testing.T) {
	dir := t.TempDir()
	_ = os.MkdirAll(filepath.Join(dir, "rod", "user-data", "p1"), 0o750)
	s := Step{Name: "check", Exit: 130}
	s.note("profile files left: %d", entriesUnder(dir))
	two := 130
	if why := checkFailed(scenarios.Check{Exit: &two, Says: []string{"profile files left: 0"}}, s); why == "" {
		t.Error("a profile left behind passed")
	}
}

func TestRejectStartedAndCompleteFromStore(t *testing.T) {
	store := t.TempDir()
	if rejectStarted(store) || completeFromStore(store) != nil {
		t.Error("an empty store")
	}
	ev := filepath.Join(store, "r1", "events")
	_ = os.MkdirAll(ev, 0o750)
	_ = os.WriteFile(filepath.Join(ev, "page-1-none.ndjson"), nil, 0o600)
	if rejectStarted(store) {
		t.Error("the none run is not the Reject run")
	}
	_ = os.WriteFile(filepath.Join(ev, "page-1-reject.ndjson"), nil, 0o600)
	if !rejectStarted(store) {
		t.Error("the Reject run's events file wasn't seen")
	}
	_ = os.WriteFile(filepath.Join(store, "r1", "consent.json"), []byte(`{"notes": ["Interrupted before every run finished: this result is incomplete."]}`), 0o600)
	if c := completeFromStore(store); c == nil || *c {
		t.Errorf("an interrupted result: %v", c)
	}
	_ = os.WriteFile(filepath.Join(store, "r1", "consent.json"), []byte(`{"notes": []}`), 0o600)
	if c := completeFromStore(store); c == nil || !*c {
		t.Errorf("a whole result: %v", c)
	}
}

func TestBurnCPUs(t *testing.T) {
	stop := burnCPUs()
	time.Sleep(20 * time.Millisecond)
	done := make(chan struct{})
	go func() { stop(); close(done) }()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("the burners didn't stop")
	}
	if runtime.NumGoroutine() > 50 {
		t.Errorf("%d goroutines left", runtime.NumGoroutine())
	}
}

func TestSelfSignedCert(t *testing.T) {
	c, err := selfSignedCert("localhost")
	if err != nil || len(c.Certificate) != 1 {
		t.Fatalf("cert: %v", err)
	}
	stop, err := serve("127.0.0.1:0", nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	stop()
}
