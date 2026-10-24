// Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
// SPDX-License-Identifier: MIT

package main

import (
	"bytes"
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"io/fs"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/RhythmShahriar/echo/catcher"
	"github.com/RhythmShahriar/echo/config"
	"github.com/RhythmShahriar/echo/model"
	"github.com/RhythmShahriar/echo/report"
	"github.com/RhythmShahriar/echo/store"
)

func init() {
	os.Setenv("RHYTHM_NO_UPDATE_CHECK", "1")
	version = "1.0.0-test"
}

func runCLI(t *testing.T, args ...string) (int, string, string) {
	t.Helper()
	var out, errb bytes.Buffer
	code := run(args, strings.NewReader(""), &out, &errb)
	return code, out.String(), errb.String()
}

func TestVersionHelpUnknown(t *testing.T) {
	if code, out, _ := runCLI(t, "--version"); code != 0 || strings.TrimSpace(out) != "rhythm-echo 1.0.0-test" {
		t.Errorf("version: %d %q", code, out)
	}
	if code, out, _ := runCLI(t, "help"); code != 0 || !strings.Contains(out, "rhythm echo up") {
		t.Error("help")
	}
	if code, _, _ := runCLI(t, "frobnicate"); code != exitUsage {
		t.Error("unknown command")
	}
	if code, out, _ := runCLI(t, "update"); code != 0 || out == "" {
		t.Error("update must succeed for rhythm update")
	}
}

func TestUsageErrors(t *testing.T) {
	t.Chdir(t.TempDir())
	for _, tc := range []struct {
		args []string
		want string
	}{
		{[]string{"up", "--seal", "--observe"}, "not both"},
		{[]string{"up", "--listen", "0.0.0.0:18999"}, "--token"},
		{[]string{"up", "--listen", "127.0.0.1:0", "--smtp", "0.0.0.0:18998"}, "--smtp 0.0.0.0:18998"},
		{[]string{"up", "--listen", "127.0.0.1:0", "--smtp", "off", "--proxy", "0.0.0.0:18997"}, "open relay"},
		{[]string{"snapshot", "extra"}, "no arguments"},
		{[]string{"snapshot", "--json", "--md"}, "one output format"},
		{[]string{"snapshot", "--name", "../x"}, "--name"},
		{[]string{"snapshot", "--fail-on", "broke"}, "--fail-on"},
		{[]string{"compare"}, "no snapshot yet"},
		{[]string{"report"}, "nothing to show"},
		{[]string{"show"}, "message ID"},
		{[]string{"run", "--config", "missing.yaml"}, "--config"},
	} {
		code, _, errOut := runCLI(t, tc.args...)
		if code != exitUsage || !strings.Contains(errOut, tc.want) {
			t.Errorf("%v: exit %d, stderr %q; want 2 and %q", tc.args, code, errOut, tc.want)
		}
	}
}

func TestInitWritesLoadableTemplate(t *testing.T) {
	t.Chdir(t.TempDir())
	if code, _, e := runCLI(t, "init"); code != 0 {
		t.Fatal(e)
	}
	if _, err := config.Load(config.Path()); err != nil {
		t.Fatal(err)
	}
	if code, _, _ := runCLI(t, "init"); code != exitUsage {
		t.Error("init must not overwrite")
	}
}

func TestSnapshotCompareThroughTheCLI(t *testing.T) {
	if _, err := exec.LookPath("curl"); err != nil {
		t.Skip("no curl")
	}
	t.Chdir(t.TempDir())
	srv := httptest.NewServer(catcher.NewServer(catcher.NewInbox(1000), catcher.Config{Version: "test"}))
	defer srv.Close()
	writeConfig := func(subject string) {
		capture := fmt.Sprintf(`curl -s -X POST %s/v1/capture -d "{\"v\":1,\"kind\":\"email\",\"run\":\"$RHYTHM_ECHO_RUN\",\"source\":{\"via\":\"test\"},\"email\":{\"from\":\"shop@shop.test\",\"to\":[\"admin@shop.test\"],\"subject\":\"%s\",\"text\":\"New order\"}}" >/dev/null`, srv.URL, subject)
		cfg := fmt.Sprintf("quiet: 300ms\ntimeout: 5s\nactions:\n  - name: New order\n    command: |\n      %s\n", capture)
		if err := os.MkdirAll(".echo", 0o750); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(config.Path(), []byte(cfg), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	writeConfig("Order received")
	code, out, errOut := runCLI(t, "snapshot", "--url", srv.URL, "--name", "good", "--plain")
	if code != 0 || !strings.Contains(out, `Snapshot "good" saved`) {
		t.Fatalf("snapshot: %d\n%s\n%s", code, out, errOut)
	}
	code, out, _ = runCLI(t, "compare", "--url", srv.URL, "--plain", "--fail-on", "changed")
	if code != 0 || !strings.Contains(out, "Everything is still sent the same way") {
		t.Fatalf("unchanged compare: %d\n%s", code, out)
	}
	writeConfig("ORDER #{order_number} received")
	code, out, _ = runCLI(t, "compare", "--url", srv.URL, "--json", "-o", "result.json", "--plain", "--fail-on", "broke")
	if code != exitFindings {
		t.Fatalf("broken compare: exit %d\n%s", code, out)
	}
	data, err := os.ReadFile("result.json")
	if err != nil {
		t.Fatal(err)
	}
	var c model.Comparison
	if err := json.Unmarshal(data, &c); err != nil {
		t.Fatal(err)
	}
	var got []string
	for _, ch := range c.Changes {
		got = append(got, ch.Class+": "+ch.Title)
	}
	all := strings.Join(got, "\n")
	for _, want := range []string{"broke: Unfilled placeholder {order_number} in email", "changed: Email to admin@shop.test: subject changed"} {
		if !strings.Contains(all, want) {
			t.Errorf("missing %q:\n%s", want, all)
		}
	}
	if code, out, _ := runCLI(t, "report", "--md"); code != 0 || !strings.Contains(out, "## ♪ Echo") {
		t.Errorf("report --md: %d\n%s", code, out)
	}
	if code, out, _ := runCLI(t, "snapshots"); code != 0 || !strings.Contains(out, "(latest)") {
		t.Errorf("snapshots:\n%s", out)
	}
	if code, out, _ := runCLI(t, "ls", "--url", srv.URL, "--plain"); code != 0 || !strings.Contains(out, "admin@shop.test") {
		t.Errorf("ls:\n%s", out)
	}
	if html, _ := filepath.Glob(".echo/*/*/report.html"); len(html) < 2 {
		t.Errorf("want a report.html for the snapshot and each compare, got %v", html)
	}
	assertPrivate(t, "result.json", ".echo/runs", ".echo/snapshots")
}

// assertPrivate fails for any file under paths that others can read:
// reports and runs hold the captured messages themselves.
func assertPrivate(t *testing.T, paths ...string) {
	t.Helper()
	if runtime.GOOS == "windows" {
		return // no Unix modes
	}
	seen := 0
	for _, root := range paths {
		err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
			if err != nil || d.IsDir() {
				return err
			}
			info, err := d.Info()
			if err != nil {
				return err
			}
			seen++
			if mode := info.Mode().Perm(); mode != 0o600 {
				t.Errorf("%s: mode %o, want 600", path, mode)
			}
			return nil
		})
		if err != nil {
			t.Fatal(err)
		}
	}
	if seen == 0 {
		t.Errorf("no files under %v", paths)
	}
}

func TestLiveLogPrintsEachMessageOnce(t *testing.T) {
	var out bytes.Buffer
	l := newLiveLog(report.NewPretty(&out, &out, report.PrettyOptions{Plain: true}))
	m := model.Message{ID: "m1", Channel: model.Email, To: []string{"ann@shop.test"}, Subject: "Reset", Status: model.Faked}
	l.add(m)
	l.add(m) // an update that changes nothing visible
	m.Status = model.Blocked
	l.add(m)
	got := out.String()
	if n := strings.Count(got, "Reset"); n != 1 {
		t.Errorf("message printed %d times:\n%s", n, got)
	}
	if !strings.Contains(got, "Update: ") || !strings.Contains(got, "is now blocked (was faked)") {
		t.Errorf("no marked update:\n%s", got)
	}
}

func TestUsageErrorsExitTwo(t *testing.T) {
	t.Chdir(t.TempDir())
	srv := httptest.NewServer(catcher.NewServer(catcher.NewInbox(10), catcher.Config{Version: "test"}))
	defer srv.Close()
	if code, _, errOut := runCLI(t, "run", "--url", srv.URL); code != exitUsage || !strings.Contains(errOut, "no actions in") {
		t.Errorf("no actions: %d %q", code, errOut)
	}
	_ = os.MkdirAll(config.Dir, 0o750)
	_ = os.WriteFile(config.Path(), []byte("actions:\n  - name: Contact form\n    command: \"true\"\n"), 0o600)
	for _, tc := range []struct {
		args []string
		want string
	}{
		{[]string{"run", "extra"}, "no arguments"},
		{[]string{"run", "--action", "nope"}, `no action named "nope"; the actions are: Contact form`},
		{[]string{"snapshot", "--action", "nope"}, `no action named "nope"`},
		{[]string{"show", "nope", "--url", srv.URL}, `no message "nope"`},
		{[]string{"report", "nope"}, `no snapshot named "nope"`},
		{[]string{"init", "extra"}, "usage"},
		{[]string{"ca", "extra"}, "usage"},
	} {
		code, out, errOut := runCLI(t, tc.args...)
		if code != exitUsage || !strings.Contains(errOut, tc.want) {
			t.Errorf("%v: exit %d, stderr %q; want 2 and %q", tc.args, code, errOut, tc.want)
		}
		if strings.Contains(out, "rhythm echo 1.0.0-test") {
			t.Errorf("%v: printed the header before the usage error:\n%s", tc.args, out)
		}
	}
}

func TestReportAndDeleteAfterSnapshots(t *testing.T) {
	if _, err := exec.LookPath("true"); err != nil {
		t.Skip("no true")
	}
	t.Chdir(t.TempDir())
	srv := httptest.NewServer(catcher.NewServer(catcher.NewInbox(100), catcher.Config{Version: "test"}))
	defer srv.Close()
	_ = os.MkdirAll(config.Dir, 0o750)
	_ = os.WriteFile(config.Path(), []byte("quiet: 100ms\ntimeout: 2s\nactions:\n  - name: Noop\n    command: \"true\"\n"), 0o600)
	for _, name := range []string{"b-first", "a-second"} {
		if code, out, errOut := runCLI(t, "snapshot", "--url", srv.URL, "--name", name, "--plain"); code != 0 {
			t.Fatalf("snapshot %s: %d\n%s\n%s", name, code, out, errOut)
		}
	}
	if _, err := os.Stat(filepath.Join(config.Dir, store.Incoming)); err == nil {
		t.Error("the incoming folder was left behind")
	}
	if code, out, errOut := runCLI(t, "report", "--plain"); code != 0 || !strings.Contains(out, "a-second") {
		t.Errorf("report after only snapshots: %d\n%s\n%s", code, out, errOut)
	}
	if code, out, _ := runCLI(t, "snapshots", "delete", "a-second"); code != 0 || !strings.Contains(out, "latest snapshot is now b-first") {
		t.Errorf("delete the latest: %d\n%s", code, out)
	}
	if code, out, _ := runCLI(t, "snapshots"); code != 0 || !strings.Contains(out, "(latest)") {
		t.Errorf("snapshots after delete:\n%s", out)
	}
	if code, _, _ := runCLI(t, "snapshots", "delete", "b-first"); code != 0 {
		t.Error("delete the last one")
	}
	if code, _, errOut := runCLI(t, "compare", "--url", srv.URL); code != exitUsage || !strings.Contains(errOut, "no snapshot yet") {
		t.Errorf("compare with none left: %d %q", code, errOut)
	}
}

func TestHelpListsEveryFlag(t *testing.T) {
	var b bytes.Buffer
	usage(&b)
	help := b.String()
	flagSet(&options{}).VisitAll(func(f *flag.Flag) {
		want := "--" + f.Name + " "
		if len(f.Name) == 1 {
			want = "-" + f.Name + ", "
		}
		if !strings.Contains(help, want) && !strings.Contains(help, "--"+f.Name+"\n") {
			t.Errorf("help doesn't list %s", strings.TrimSpace(want))
		}
	})
	for _, f := range []string{"--mode", "--force", "--wp-cli", "--container", "--uninstall", "--ca", "--pem", "[--force]"} {
		if !strings.Contains(help, f) {
			t.Errorf("help doesn't list %s", f)
		}
	}
	for _, code := range []string{"0    ok", "1    findings", "2    usage error", "3    couldn't run", "130  interrupted"} {
		if !strings.Contains(help, code) {
			t.Errorf("help doesn't explain exit code %q", code)
		}
	}
	if code, _, errOut := runCLI(t, "ls", "--channel", "fax"); code != exitUsage || !strings.Contains(errOut, "email, sms") {
		t.Errorf("unknown channel: %d %q", code, errOut)
	}
}

func TestPicker(t *testing.T) {
	// Not a terminal: the usage, never a prompt.
	if code, out, _ := runCLI(t); code != 0 || !strings.Contains(out, "Usage:") {
		t.Errorf("no arguments off a terminal: %d\n%s", code, out)
	}
	values := func(ps pickState) []string {
		var v []string
		for _, o := range pickOptions(ps) {
			v = append(v, o.Value)
		}
		return v
	}
	if got := strings.Join(values(pickState{}), " "); got != "up install doctor init" {
		t.Errorf("fresh folder: %s", got)
	}
	ps := pickState{running: true, hasConfig: true, hasActions: true, snapshot: `"before", 2 hours ago`, hasResult: true}
	if got := strings.Join(values(ps), " "); got != "watch compare snapshot run report install doctor" {
		t.Errorf("set up: %s", got)
	}
	for _, tc := range []struct {
		action, stack, site string
		open                bool
		want                string
	}{
		{"install", "wordpress", " /var/www/staging ", true, "install wordpress /var/www/staging"},
		{"install", "laravel", ".", true, "install laravel"},
		{"compare", "", "", true, "compare --open"},
		{"snapshot", "", "", false, "snapshot"},
		{"up", "", "", true, "up"},
	} {
		if got := strings.Join(pickArgs(tc.action, tc.stack, tc.site, tc.open), " "); got != tc.want {
			t.Errorf("pickArgs(%s) = %q, want %q", tc.action, got, tc.want)
		}
	}
}

// writeActions writes a config whose actions run these shell commands.
func writeActions(t *testing.T, commands ...string) {
	t.Helper()
	cfg := "quiet: 100ms\ntimeout: 2s\nactions:\n"
	for i, c := range commands {
		cfg += fmt.Sprintf("  - name: Step %d\n    command: %q\n", i+1, c)
	}
	if err := os.MkdirAll(config.Dir, 0o750); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(config.Path(), []byte(cfg), 0o600); err != nil {
		t.Fatal(err)
	}
}

func TestFailedActionsFailTheRun(t *testing.T) {
	if _, err := exec.LookPath("sh"); err != nil {
		t.Skip("no sh")
	}
	t.Chdir(t.TempDir())
	srv := httptest.NewServer(catcher.NewServer(catcher.NewInbox(100), catcher.Config{Version: "test"}))
	defer srv.Close()
	for _, tc := range []struct {
		name     string
		commands []string
		args     []string
		want     int
	}{
		{"all ok", []string{"true", "true"}, nil, exitOK},
		{"one of two failed", []string{"true", "exit 7"}, nil, exitFindings},
		{"one failed, --fail-on none", []string{"true", "exit 7"}, []string{"--fail-on", "none"}, exitOK},
		{"one failed, --fail-on error", []string{"true", "exit 7"}, []string{"--fail-on", "error"}, exitFindings},
		{"all failed", []string{"exit 7", "exit 8"}, nil, exitError},
		{"all failed, --fail-on none", []string{"exit 7", "exit 8"}, []string{"--fail-on", "none"}, exitError},
	} {
		writeActions(t, tc.commands...)
		code, out, errOut := runCLI(t, append([]string{"run", "--url", srv.URL, "--plain"}, tc.args...)...)
		if code != tc.want {
			t.Errorf("run, %s: exit %d, want %d\n%s\n%s", tc.name, code, tc.want, out, errOut)
		}
		if all := strings.Contains(errOut, "every action failed"); all != (tc.want == exitError) {
			t.Errorf("run, %s: stderr %q", tc.name, errOut)
		}
	}

	writeActions(t, "true")
	if code, out, errOut := runCLI(t, "snapshot", "--url", srv.URL, "--plain", "--name", "base"); code != exitOK {
		t.Fatalf("snapshot: %d\n%s\n%s", code, out, errOut)
	}
	writeActions(t, "exit 7")
	if code, out, errOut := runCLI(t, "compare", "--url", srv.URL, "--plain"); code != exitError {
		t.Errorf("compare with every action failed: exit %d\n%s\n%s", code, out, errOut)
	}
	if code, out, errOut := runCLI(t, "compare", "--url", srv.URL, "--plain", "--fail-on", "none", "--junit"); code != exitError ||
		!strings.Contains(out, "<testsuites") {
		t.Errorf("compare --fail-on none --junit: exit %d\n%s\n%s", code, out, errOut)
	}
	// report shows a saved result again; it doesn't rerun the actions.
	if code, _, errOut := runCLI(t, "report", "--plain"); code != exitOK {
		t.Errorf("report of a failed run: exit %d %s", code, errOut)
	}
}

func TestFailedSnapshotKeepsTheBaseline(t *testing.T) {
	if _, err := exec.LookPath("sh"); err != nil {
		t.Skip("no sh")
	}
	t.Chdir(t.TempDir())
	srv := httptest.NewServer(catcher.NewServer(catcher.NewInbox(100), catcher.Config{Version: "test"}))
	defer srv.Close()
	runJSON := filepath.Join(config.Dir, "snapshots", "good", "run.json")
	read := func() string {
		t.Helper()
		b, err := os.ReadFile(runJSON)
		if err != nil {
			t.Fatal(err)
		}
		return string(b)
	}
	writeActions(t, "true")
	if code, out, errOut := runCLI(t, "snapshot", "--url", srv.URL, "--plain", "--name", "good"); code != exitOK {
		t.Fatalf("good snapshot: %d\n%s\n%s", code, out, errOut)
	}
	good := read()

	writeActions(t, "exit 7")
	code, out, errOut := runCLI(t, "snapshot", "--url", srv.URL, "--plain", "--name", "good")
	if code != exitError || !strings.Contains(errOut, "not saved: every action failed; fix the site or pass --force") {
		t.Errorf("failed snapshot: exit %d\n%s\n%s", code, out, errOut)
	}
	if strings.Contains(out, "saved") {
		t.Errorf("the summary says it was saved:\n%s", out)
	}
	if read() != good {
		t.Error("a failed snapshot replaced the good one")
	}
	if code, _, _ := runCLI(t, "snapshot", "--url", srv.URL, "--plain", "--name", "other"); code != exitError {
		t.Errorf("failed snapshot under a new name: exit %d", code)
	}
	if code, out, _ := runCLI(t, "snapshots"); code != 0 || strings.Contains(out, "other") || !strings.Contains(out, "(latest)") {
		t.Errorf("a failed snapshot was listed or moved latest:\n%s", out)
	}
	if _, err := os.Stat(filepath.Join(config.Dir, store.Incoming)); err == nil {
		t.Error("the incoming folder was left behind")
	}

	code, _, errOut = runCLI(t, "snapshot", "--url", srv.URL, "--plain", "--name", "good", "--force")
	if code != exitError || !strings.Contains(errOut, "saved anyway because of --force") {
		t.Errorf("--force: exit %d %s", code, errOut)
	}
	if read() == good {
		t.Error("--force didn't replace the snapshot")
	}
}

// syncBuffer is a bytes.Buffer safe to read while watch writes to it.
type syncBuffer struct {
	mu sync.Mutex
	b  bytes.Buffer
}

func (b *syncBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.b.Write(p)
}

func (b *syncBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.b.String()
}

// startWatch runs watch with args against a fresh Echo until the test ends.
func startWatch(t *testing.T, args ...string) (*catcher.Inbox, *syncBuffer) {
	t.Helper()
	t.Chdir(t.TempDir())
	in := catcher.NewInbox(100)
	srv := httptest.NewServer(catcher.NewServer(in, catcher.Config{Version: "test"}))
	t.Cleanup(srv.Close)
	p, err := parse(append([]string{"--url", srv.URL, "--plain"}, args...))
	if err != nil {
		t.Fatal(err)
	}
	out, errb := &syncBuffer{}, &syncBuffer{}
	s := p.begin(out, errb)
	ctx, cancel := context.WithCancel(context.Background())
	s.ctx = ctx
	done := make(chan int)
	go func() { done <- cmdWatch(s) }()
	t.Cleanup(func() {
		cancel()
		if code := <-done; code != exitOK {
			t.Errorf("watch exited %d: %s", code, errb.String())
		}
		s.stop()
	})
	waitFor(t, func() bool { return strings.Contains(errb.String(), "Watching") }, "the watch to start")
	return in, out
}

func waitFor(t *testing.T, ok func() bool, what string) {
	t.Helper()
	for deadline := time.Now().Add(5 * time.Second); time.Now().Before(deadline); time.Sleep(20 * time.Millisecond) {
		if ok() {
			return
		}
	}
	t.Fatalf("timed out waiting for %s", what)
}

func TestWatchJSONStreamsLines(t *testing.T) {
	in, out := startWatch(t, "--json")
	m := in.Add(model.Message{Channel: model.Email, To: []string{"ann@shop.test"}, Subject: "Hi {first_name}",
		Text: "Hello {first_name}", Status: model.Faked})
	waitFor(t, func() bool { return strings.Count(out.String(), "\n") >= 1 }, "the message line")
	in.Update(m.ID, func(m *model.Message) { m.Status = model.Blocked })
	waitFor(t, func() bool { return strings.Count(out.String(), "\n") >= 2 }, "the update line")
	var lines []watchLine
	for _, l := range strings.Split(strings.TrimSpace(out.String()), "\n") {
		var wl watchLine
		if err := json.Unmarshal([]byte(l), &wl); err != nil {
			t.Fatalf("not JSON: %q: %v", l, err)
		}
		lines = append(lines, wl)
	}
	if len(lines) != 2 || lines[0].Type != "message" || lines[0].Message.ID != m.ID ||
		lines[1].Type != "update" || lines[1].Message.Status != model.Blocked || lines[1].Was != model.Faked {
		t.Fatalf("lines = %+v", lines)
	}
	found := false
	for _, i := range lines[0].Issues {
		found = found || i.Check == "content/placeholder"
	}
	if !found {
		t.Errorf("no placeholder issue: %+v", lines[0].Issues)
	}
}

func TestWatchShowsStatusChangesAndIssues(t *testing.T) {
	in, out := startWatch(t)
	m := in.Add(model.Message{Channel: model.Email, To: []string{"ann@shop.test"}, Subject: "Hi {first_name}",
		Text: "Hello {first_name}", Status: model.Faked})
	clean := in.Add(model.Message{Channel: model.Email, To: []string{"bob@shop.test"}, Subject: "Receipt", Text: "Thanks", Status: model.Faked})
	waitFor(t, func() bool { return strings.Contains(out.String(), "Receipt") }, "both messages")
	in.Update(m.ID, func(m *model.Message) { m.Status = model.Blocked })
	in.Update(clean.ID, func(m *model.Message) { m.Subject = "Receipt" }) // no visible change
	waitFor(t, func() bool { return strings.Contains(out.String(), "is now blocked (was faked)") }, "the update")
	got := out.String()
	if strings.Count(got, "Hi {first_name}") != 1 || strings.Count(got, "Receipt") != 1 {
		t.Errorf("a message was printed twice:\n%s", got)
	}
	if !strings.Contains(got, "[ERROR]") || strings.Count(got, "Update:") != 1 {
		t.Errorf("want one issue marker and one update:\n%s", got)
	}
}

func TestStatusLRUForgetsTheOldest(t *testing.T) {
	c := newStatusLRU(2)
	c.put("a", model.Faked)
	c.put("b", model.Faked)
	if prev, seen := c.put("a", model.Blocked); !seen || prev != model.Faked {
		t.Errorf("a: %q %v", prev, seen)
	}
	c.put("c", model.Faked) // b is the least recently seen
	if _, seen := c.put("b", model.Faked); seen {
		t.Error("b should have been forgotten")
	}
	if _, seen := c.put("a", model.Blocked); seen {
		t.Error("a should have been forgotten when b came back") // c, a + b -> b, c
	}
}

func TestListSince(t *testing.T) {
	t.Chdir(t.TempDir())
	in := catcher.NewInbox(100)
	srv := httptest.NewServer(catcher.NewServer(in, catcher.Config{Version: "test"}))
	defer srv.Close()
	in.Add(model.Message{Channel: model.Email, To: []string{"old@shop.test"}, Subject: "Old", At: time.Now().Add(-time.Hour)})
	in.Add(model.Message{Channel: model.Email, To: []string{"new@shop.test"}, Subject: "New"})
	code, out, errOut := runCLI(t, "ls", "--url", srv.URL, "--json", "--since", "10m")
	var ms []model.Message
	if err := json.Unmarshal([]byte(out), &ms); code != 0 || err != nil || len(ms) != 1 || ms[0].Subject != "New" {
		t.Errorf("ls --since 10m: %d %v %s\n%s", code, err, errOut, out)
	}
	if code, _, errOut := runCLI(t, "ls", "--since", "-5m"); code != exitUsage || !strings.Contains(errOut, "--since") {
		t.Errorf("negative --since: %d %q", code, errOut)
	}
}

// A token on the command line shows in ps, so the flag earns a warning;
// the environment variable and -q do not.
func TestTokenFlagWarns(t *testing.T) {
	t.Chdir(t.TempDir())
	t.Setenv("RHYTHM_ECHO_TOKEN", "")
	if _, _, errOut := runCLI(t, "report", "--token", "s3cret"); !strings.Contains(errOut, tokenFlagWarning) {
		t.Errorf("--token: no warning in %q", errOut)
	}
	if _, _, errOut := runCLI(t, "report", "--token", "s3cret", "-q"); strings.Contains(errOut, "RHYTHM_ECHO_TOKEN") {
		t.Errorf("-q must hide the warning: %q", errOut)
	}
	if _, _, errOut := runCLI(t, "install", "laravel", "--token", "s3cret"); !strings.Contains(errOut, tokenFlagWarning) {
		t.Errorf("install --token: no warning in %q", errOut)
	}
	t.Setenv("RHYTHM_ECHO_TOKEN", "s3cret")
	for _, args := range [][]string{{"report"}, {"install", "laravel"}} {
		if _, _, errOut := runCLI(t, args...); strings.Contains(errOut, "RHYTHM_ECHO_TOKEN") {
			t.Errorf("%v with the env var: unexpected warning %q", args, errOut)
		}
	}
	if code, out, _ := runCLI(t, "help"); code != 0 || !strings.Contains(out, "Prefer RHYTHM_ECHO_TOKEN") {
		t.Error("help must steer --token to RHYTHM_ECHO_TOKEN")
	}
}
