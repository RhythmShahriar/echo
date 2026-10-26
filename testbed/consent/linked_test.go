// Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
// SPDX-License-Identifier: MIT

package consenttestbed

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"os/exec"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/RhythmShahriar/echo/consent/consenttest"
	"github.com/RhythmShahriar/echo/internal/testenv"
)

// How the browser tests run rhythm-echo and what they do when a consent
// stage isn't in that build. This file has no build tag, so its helpers
// are tested on every run against a fake rhythm-echo (this test binary).

// fakeEchoEnv, set, makes this test binary a fake rhythm-echo whose
// consent stages answer the linkage probes but whose checks aren't
// built: the build the require flag must fail on.
const fakeEchoEnv = "CONSENT_TB_FAKE_ECHO"

// cleanups run after every test (consent_test.go adds its build folder).
var cleanups []func()

func TestMain(m *testing.M) {
	if os.Getenv(fakeEchoEnv) != "" {
		os.Exit(fakeEcho(os.Args[1:]))
	}
	code := m.Run()
	for _, f := range cleanups {
		f()
	}
	os.Exit(code)
}

// fakeEcho answers "consent trackers --json" and "consent cmps --json"
// as a linked build does and every other consent command as a build
// whose checks aren't there yet.
func fakeEcho(args []string) int {
	if len(args) >= 2 && args[0] == "consent" && (args[1] == "trackers" || args[1] == "cmps") {
		fmt.Println(`{"kind": "consent-` + args[1] + `", "data": []}`)
		return 0
	}
	fmt.Fprintln(os.Stderr, "rhythm echo: consent isn't built yet")
	return 3
}

// echoEnv is the environment of every rhythm-echo run: no update check,
// the CI behavior (no tips, no prompts, no color).
func echoEnv(extra ...string) []string {
	return append(os.Environ(), append([]string{"RHYTHM_OFFLINE=1", "CI=true", "NO_COLOR=1"}, extra...)...)
}

// notLinked reports whether rhythm-echo's output says a consent stage
// isn't in the build.
func notLinked(out string) bool {
	return strings.Contains(out, "not linked into this build") || strings.Contains(out, "isn't built yet")
}

// skipNotLinked skips t because a consent stage isn't in the build, or
// fails it when ECHO_CONSENT_REQUIRE_LINKED=1: the nightly integrated
// job must never go green on skips. Every not-linked path goes through
// here so no stage can slip past the flag.
func skipNotLinked(t testing.TB, format string, args ...any) {
	t.Helper()
	msg := fmt.Sprintf(format, args...)
	if os.Getenv(consenttest.RequireLinkedEnv) == "1" {
		t.Fatalf("%s (%s=1)", msg, consenttest.RequireLinkedEnv)
	}
	t.Skip(msg)
}

// probeLinked runs "consent trackers --json" and "consent cmps --json"
// on bin and skips (or fails) t when either stage isn't linked.
func probeLinked(t testing.TB, bin string) {
	t.Helper()
	for _, c := range []struct{ stage, sub string }{{"tracker", "trackers"}, {"cmp", "cmps"}} {
		stdout, stderr, code := runEcho(t, bin, t.TempDir(), echoEnv(), 30*time.Second, "consent", c.sub, "--json")
		if code != 0 {
			t.Fatalf("rhythm-echo consent %s --json: exit %d\n%s%s", c.sub, code, stdout, stderr)
		}
	}
}

// runEcho runs bin with args in dir and returns its output and exit code
// (-1 when it didn't run to an exit). Output that says a consent stage
// isn't linked skips or fails t through skipNotLinked.
func runEcho(t testing.TB, bin, dir string, env []string, limit time.Duration, args ...string) (string, string, int) {
	t.Helper()
	ctx, cancel := context.WithTimeout(t.Context(), testenv.Timeout(limit))
	defer cancel()
	cmd := exec.CommandContext(ctx, bin, args...)
	cmd.Dir, cmd.Env = dir, env
	var out, errOut bytes.Buffer
	cmd.Stdout, cmd.Stderr = &out, &errOut
	_ = cmd.Run()
	code := -1
	if cmd.ProcessState != nil {
		code = cmd.ProcessState.ExitCode()
	}
	if code != 0 && notLinked(out.String()+errOut.String()) {
		skipNotLinked(t, "a consent stage isn't linked yet: rhythm-echo %s: %s", strings.Join(args[:min(2, len(args))], " "), strings.TrimSpace(errOut.String()))
	}
	return out.String(), errOut.String(), code
}

// recorder is a testing.TB that notes a skip or a failure and ends the
// goroutine, the way the testing package ends a test.
type recorder struct {
	testing.TB
	skipped, failed bool
	msg             string
}

func (r *recorder) Helper() {}

func (r *recorder) Skip(args ...any) {
	r.skipped, r.msg = true, fmt.Sprint(args...)
	runtime.Goexit()
}

func (r *recorder) Skipf(format string, args ...any) { r.Skip(fmt.Sprintf(format, args...)) }

func (r *recorder) Fatalf(format string, args ...any) {
	r.failed, r.msg = true, fmt.Sprintf(format, args...)
	runtime.Goexit()
}

// record runs f with a recorder in its own goroutine and returns it.
func record(t *testing.T, f func(testing.TB)) *recorder {
	r := &recorder{TB: t}
	done := make(chan struct{})
	go func() {
		defer close(done)
		f(r)
	}()
	<-done
	return r
}

// TestNotLinkedRequired: a build whose tracker and cmp stages are linked
// but whose check, report or detect isn't passes the linkage probe; the
// later "isn't built yet" then skips, or fails under the require flag.
func TestNotLinkedRequired(t *testing.T) {
	self, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	// The children inherit it; this process is past TestMain.
	t.Setenv(fakeEchoEnv, "1")
	env := echoEnv()
	cases := [][]string{
		{"consent", "https://c01.shop.test/", "--json", "--fail-on", "none"},
		{"consent", "report", "latest", "--json"},
		{"consent", "detect", "https://cmp-replicas.shop.test/onetrust/index.html", "--json"},
	}
	for _, require := range []string{"", "1"} {
		t.Setenv(consenttest.RequireLinkedEnv, require)
		probe := record(t, func(tb testing.TB) { probeLinked(tb, self) })
		if probe.skipped || probe.failed {
			t.Fatalf("require=%q: the linkage probe stopped the test: %s", require, probe.msg)
		}
		for _, args := range cases {
			r := record(t, func(tb testing.TB) {
				runEcho(tb, self, t.TempDir(), env, 30*time.Second, args...)
				tb.Fatalf("returned")
			})
			want := "skip"
			if require == "1" {
				want = "fail"
			}
			got := "neither"
			switch {
			case r.skipped:
				got = "skip"
			case r.failed && strings.Contains(r.msg, consenttest.RequireLinkedEnv):
				got = "fail"
			}
			if got != want {
				t.Errorf("require=%q %v: %s (%s), want %s", require, args[:2], got, r.msg, want)
			}
		}
	}
}

// TestSkipNotLinked: the flag has to be exactly 1.
func TestSkipNotLinked(t *testing.T) {
	for _, c := range []struct {
		env  string
		fail bool
	}{{"", false}, {"0", false}, {"true", false}, {"1", true}} {
		t.Setenv(consenttest.RequireLinkedEnv, c.env)
		r := record(t, func(tb testing.TB) { skipNotLinked(tb, "cmp not linked yet") })
		if r.failed != c.fail || r.skipped == c.fail {
			t.Errorf("%s=%q: failed %v skipped %v", consenttest.RequireLinkedEnv, c.env, r.failed, r.skipped)
		}
		if !strings.Contains(r.msg, "cmp not linked yet") {
			t.Errorf("%q: message %q", c.env, r.msg)
		}
	}
}
