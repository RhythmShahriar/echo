// Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
// SPDX-License-Identifier: MIT

package main

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io/fs"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/RhythmShahriar/echo/catcher"
	"github.com/RhythmShahriar/echo/client"
	"github.com/RhythmShahriar/echo/config"
	"github.com/RhythmShahriar/echo/model"
	"github.com/RhythmShahriar/echo/report"
	"github.com/RhythmShahriar/echo/sendmail"
	"github.com/RhythmShahriar/echo/store"
	"github.com/RhythmShahriar/echo/wire"
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
		{[]string{"compare"}, "no .echo/config.yaml here or above; run rhythm echo init"},
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
	// The comparison has a BROKE change, which --fail-on's default fails on.
	if code, out, _ := runCLI(t, "report", "--md"); code != exitFindings || !strings.Contains(out, "## ♪ Echo") {
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
	if code, _, errOut := runCLI(t, "run", "--url", srv.URL); code != exitUsage || !strings.Contains(errOut, "no .echo/config.yaml here or above") {
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

// TestCommandHelpListsItsFlags checks each command's --help against what
// it accepts: every flag it reads is listed and every flag listed is one
// it reads.
func TestCommandHelpListsItsFlags(t *testing.T) {
	fs := flagSet(&options{})
	long := regexp.MustCompile(`--[a-z][a-z-]*`)
	for _, c := range commands {
		if c.own || c.hidden {
			continue
		}
		code, out, _ := runCLI(t, c.name, "--help")
		if code != exitOK || !strings.HasPrefix(out, "rhythm echo "+c.name+": ") {
			t.Errorf("%s --help: %d\n%s", c.name, code, out)
			continue
		}
		listed := out
		if i := strings.Index(out, "\nFlags:\n"); i >= 0 {
			listed = out[i:]
		} else if len(c.flags) > 0 {
			t.Errorf("%s --help has no Flags section", c.name)
		}
		for _, f := range c.flags {
			if fs.Lookup(f.name) == nil || flagDocs[f.name].left == "" {
				t.Errorf("%s reads --%s, which isn't a flag with help", c.name, f.name)
			}
			if !strings.Contains(listed, "--"+f.name+" ") && !strings.Contains(listed, "--"+f.name+"\n") {
				t.Errorf("%s --help doesn't list --%s", c.name, f.name)
			}
		}
		for _, f := range long.FindAllString(listed, -1) {
			if !c.accepts(strings.TrimPrefix(f, "--")) {
				t.Errorf("%s --help lists %s, which it rejects", c.name, f)
			}
		}
	}
}

func TestCompareHelpIsShort(t *testing.T) {
	code, out, _ := runCLI(t, "compare", "--help")
	if code != exitOK || strings.Count(out, "\n") > 35 || !strings.Contains(out, "--snapshot <name>") || !strings.Contains(out, "broke (the default)") {
		t.Errorf("compare --help: %d\n%s", code, out)
	}
	for _, other := range []string{"--seal", "--resolver", "--channel", "Examples:"} {
		if strings.Contains(out, other) {
			t.Errorf("compare --help lists %s:\n%s", other, out)
		}
	}
	if _, viaHelp, _ := runCLI(t, "help", "cmp"); viaHelp != out {
		t.Errorf("help cmp differs from compare --help:\n%s", viaHelp)
	}
	if code, out, _ := runCLI(t, "help", "install"); code != exitOK || !strings.Contains(out, "stacks: wordpress") {
		t.Errorf("help install: %d\n%s", code, out)
	}
}

func TestCommandAndFlagSuggestions(t *testing.T) {
	t.Chdir(t.TempDir())
	for _, tc := range []struct {
		args []string
		want string
	}{
		{[]string{"cmopare"}, `unknown command "cmopare"; did you mean compare?`},
		{[]string{"snapshto"}, "did you mean snapshot?"},
		{[]string{"frobnicate"}, `unknown command "frobnicate"` + "\n"},
		{[]string{"help", "cmopare"}, "did you mean compare?"},
		{[]string{"compare", "--snapshto", "x"}, "unknown flag --snapshto; did you mean --snapshot?"},
		{[]string{"ls", "--chanel", "sms"}, "did you mean --channel?"},
		{[]string{"compare", "--fial-on", "broke"}, "did you mean --fail-on?"},
		{[]string{"compare", "--fail-on"}, "--fail-on needs a value"},
		{[]string{"ls", "--since", "abc"}, `--since "abc": want a duration like 10m or 5s`},
		{[]string{"run", "--json=maybe"}, `--json "maybe": want true or false`},
		{[]string{"install", "laravel", "--tokn", "x"}, "unknown flag --tokn; did you mean --token?"},
	} {
		code, out, errOut := runCLI(t, tc.args...)
		if code != exitUsage || !strings.Contains(errOut, tc.want) {
			t.Errorf("%v: exit %d, stderr %q; want 2 and %q", tc.args, code, errOut, tc.want)
		}
		// A typo gets a line rather than the whole help. Flags keep
		// their two dashes.
		if strings.Contains(out+errOut, "Usage:") || regexp.MustCompile(`(^|[ :])-[a-z]{2,}`).MatchString(errOut) {
			t.Errorf("%v:\n%s%s", tc.args, out, errOut)
		}
	}
	if s := suggestCommand("xy"); s != "" {
		t.Errorf("xy is close to nothing, got %q", s)
	}
}

func TestUnusedFlagsAreRejected(t *testing.T) {
	t.Chdir(t.TempDir())
	for _, tc := range []struct {
		args []string
		want string
	}{
		{[]string{"ls", "--fail-on", "broke"}, "--fail-on has no effect on ls"},
		{[]string{"ls", "--snapshot", "foo", "--fail-on", "broke", "--wait", "3s"}, "--fail-on, --snapshot and --wait have no effect on ls"},
		{[]string{"list", "-o", "x.json"}, "--output has no effect on ls"},
		{[]string{"watch", "--action", "Sign up"}, "--action has no effect on watch"},
		{[]string{"check", "shop.test", "--url", "http://127.0.0.1:1"}, "--url has no effect on check"},
		{[]string{"report", "--url", "http://127.0.0.1:1"}, "--url has no effect on report"},
		{[]string{"up", "--fail-on", "none"}, "--fail-on has no effect on up"},
		{[]string{"doctor", "--json"}, "--json has no effect on doctor"},
	} {
		code, out, errOut := runCLI(t, tc.args...)
		if code != exitUsage || !strings.Contains(errOut, tc.want) {
			t.Errorf("%v: exit %d, stderr %q; want 2 and %q", tc.args, code, errOut, tc.want)
		}
		if out != "" {
			t.Errorf("%v printed before refusing:\n%s", tc.args, out)
		}
	}
}

func TestHelpHeaderAndHiddenUpdate(t *testing.T) {
	_, out, _ := runCLI(t, "help")
	if !strings.Contains(out, "rhythm echo and rhythm-echo are the same command") {
		t.Errorf("help doesn't say the two names are one command:\n%s", out)
	}
	if strings.Contains(out, "rhythm echo update") {
		t.Error("help lists update, which does nothing")
	}
	if code, out, _ := runCLI(t, "update"); code != exitOK || out == "" {
		t.Error("update must still succeed for rhythm update")
	}
}

// TestFailOnDefault: without --fail-on, compare fails on BROKE, the
// GitHub Action's default; none turns it off, in any case.
func TestFailOnDefault(t *testing.T) {
	t.Chdir(t.TempDir())
	srv := httptest.NewServer(catcher.NewServer(catcher.NewInbox(100), catcher.Config{Version: "test"}))
	defer srv.Close()
	echo := client.New(srv.URL, "")
	var send atomic.Bool
	send.Store(true)
	shop := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if send.Load() {
			_, _ = echo.Capture(r.Context(), wire.Capture{Kind: "email", Run: r.Header.Get(wire.RunHeader), Source: wire.Source{Via: "test"},
				Email: &wire.Email{To: []string{"owner@shop.test"}, Subject: "New order", Text: "Order 1 placed."}})
		}
	}))
	defer shop.Close()
	writeConfig(t, "site: "+shop.URL+"\nquiet: 200ms\ntimeout: 2s\nactions:\n  - name: Order\n    http: {method: POST, url: /order}\n")
	if code, out, errOut := runCLI(t, "snapshot", "--url", srv.URL, "--plain", "--name", "base"); code != exitOK {
		t.Fatalf("snapshot: %d\n%s\n%s", code, out, errOut)
	}
	if code, out, _ := runCLI(t, "compare", "--url", srv.URL, "--plain"); code != exitOK {
		t.Errorf("unchanged compare: %d\n%s", code, out)
	}
	send.Store(false) // the owner's notification stopped: BROKE
	if code, out, _ := runCLI(t, "compare", "--url", srv.URL, "--plain"); code != exitFindings || !strings.Contains(out, "BROKE") {
		t.Errorf("default compare with a BROKE change: %d\n%s", code, out)
	}
	for _, none := range []string{"none", "NONE", "None"} {
		if code, _, errOut := runCLI(t, "compare", "--url", srv.URL, "--plain", "--fail-on", none); code != exitOK {
			t.Errorf("--fail-on %s: %d %s", none, code, errOut)
		}
	}
	if code, _, _ := runCLI(t, "compare", "--url", srv.URL, "--plain", "--fail-on", "Broke"); code != exitFindings {
		t.Errorf("--fail-on Broke: %d", code)
	}
	if code, _, errOut := runCLI(t, "compare", "--url", srv.URL, "--fail-on", "error"); code != exitUsage ||
		!strings.Contains(errOut, `--fail-on "error": want none, broke, new or changed`) {
		t.Errorf("--fail-on error on compare: %d %q", code, errOut)
	}
	// The JUnit report marks the same: BROKE is a failure by default.
	if _, out, _ := runCLI(t, "report", "--junit"); !strings.Contains(out, "<failure") {
		t.Errorf("report --junit has no failure for BROKE:\n%s", out)
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
	// report shows a saved result again; it doesn't rerun the actions,
	// so it exits 1 for the BROKE changes rather than 3.
	if code, _, errOut := runCLI(t, "report", "--plain"); code != exitFindings {
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
	watch, _ := lookup("watch")
	p, err := parse(watch, append([]string{"--url", srv.URL, "--plain"}, args...))
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
	dead := "http://127.0.0.1:1"
	if _, _, errOut := runCLI(t, "ls", "--url", dead, "--token", "s3cret"); !strings.Contains(errOut, tokenFlagWarning) {
		t.Errorf("--token: no warning in %q", errOut)
	}
	if _, _, errOut := runCLI(t, "ls", "--url", dead, "--token", "s3cret", "-q"); strings.Contains(errOut, "RHYTHM_ECHO_TOKEN") {
		t.Errorf("-q must hide the warning: %q", errOut)
	}
	if _, _, errOut := runCLI(t, "install", "laravel", "--token", "s3cret"); !strings.Contains(errOut, tokenFlagWarning) {
		t.Errorf("install --token: no warning in %q", errOut)
	}
	t.Setenv("RHYTHM_ECHO_TOKEN", "s3cret")
	for _, args := range [][]string{{"ls", "--url", dead}, {"install", "laravel"}} {
		if _, _, errOut := runCLI(t, args...); strings.Contains(errOut, "RHYTHM_ECHO_TOKEN") {
			t.Errorf("%v with the env var: unexpected warning %q", args, errOut)
		}
	}
	if code, out, _ := runCLI(t, "help"); code != 0 || !strings.Contains(out, "Prefer RHYTHM_ECHO_TOKEN") {
		t.Error("help must steer --token to RHYTHM_ECHO_TOKEN")
	}
}

// Ctrl+C during doctor's flush check must return at once, even when the
// flush command started a child that keeps the output pipe open.
func TestDoctorFlushCancels(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("uses sleep")
	}
	t.Chdir(t.TempDir())
	srv := httptest.NewServer(catcher.NewServer(catcher.NewInbox(10), catcher.Config{Version: "test"}))
	defer srv.Close()
	if err := os.MkdirAll(".echo", 0o700); err != nil {
		t.Fatal(err)
	}
	// "; echo done" keeps sh from exec'ing sleep, so sleep is a child.
	if err := os.WriteFile(config.Path(), []byte("smtp: \"off\"\nflush: [\"sleep 25; echo done\"]\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	doctor, _ := lookup("doctor")
	p, err := parse(doctor, []string{"--url", srv.URL, "--plain"})
	if err != nil {
		t.Fatal(err)
	}
	var out, errb bytes.Buffer
	s := p.begin(&out, &errb)
	defer s.stop()
	ctx, cancel := context.WithCancel(s.ctx)
	s.ctx = ctx
	time.AfterFunc(200*time.Millisecond, cancel)
	start := time.Now()
	code := cmdDoctor(s)
	if took := time.Since(start); took > 3*time.Second {
		t.Errorf("doctor took %s after cancel, want under 3s", took)
	}
	if code != exitCanceled || !strings.Contains(errb.String(), "canceled") {
		t.Errorf("exit %d, stderr %q; want %d and canceled", code, errb.String(), exitCanceled)
	}
}

func TestDoctorFlushRuns(t *testing.T) {
	t.Chdir(t.TempDir())
	srv := httptest.NewServer(catcher.NewServer(catcher.NewInbox(10), catcher.Config{Version: "test"}))
	defer srv.Close()
	if err := os.MkdirAll(".echo", 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(config.Path(), []byte("smtp: \"off\"\nflush: [\"echo ok\", \"exit 3\"]\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	code, out, _ := runCLI(t, "doctor", "--url", srv.URL, "--plain")
	if code != exitFindings || !strings.Contains(out, "ok    Flush: echo ok") || !strings.Contains(out, `FAIL  Flush: "exit 3"`) {
		t.Errorf("exit %d\n%s", code, out)
	}
}

// shopSite is a staging site whose /signup sends a welcome email through
// Echo, tagged with the run the request carried, as a connector does.
// With a placeholder left in, the email has an issue to find.
func shopSite(t *testing.T, echoURL, text string) *httptest.Server {
	t.Helper()
	echo := client.New(echoURL, "")
	shop := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/signup" {
			http.NotFound(w, r)
			return
		}
		_, _ = echo.Capture(r.Context(), wire.Capture{Kind: "email", Run: r.Header.Get(wire.RunHeader), Source: wire.Source{Via: "test"},
			Email: &wire.Email{To: []string{"ann@shop.test"}, Subject: "Welcome", Text: text}})
	}))
	t.Cleanup(shop.Close)
	return shop
}

func writeConfig(t *testing.T, cfg string) {
	t.Helper()
	if err := os.MkdirAll(config.Dir, 0o750); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(config.Path(), []byte(cfg), 0o600); err != nil {
		t.Fatal(err)
	}
}

func TestRunCommand(t *testing.T) {
	t.Chdir(t.TempDir())
	srv := httptest.NewServer(catcher.NewServer(catcher.NewInbox(100), catcher.Config{Version: "test"}))
	defer srv.Close()
	good := shopSite(t, srv.URL, "Hi Ann, welcome.")
	cfg := func(site string) string {
		return "site: " + site + "\nquiet: 200ms\ntimeout: 3s\nactions:\n" +
			"  - name: Sign up\n    http: {method: POST, url: /signup, form: {email: ann@shop.test}}\n    expect: {email: 1}\n"
	}
	writeConfig(t, cfg(good.URL))
	code, out, errOut := runCLI(t, "run", "--url", srv.URL, "--plain")
	if code != exitOK || !strings.Contains(out, "Sign up") || !strings.Contains(out, "1 email") {
		t.Fatalf("run: %d\n%s\n%s", code, out, errOut)
	}
	runs, _ := filepath.Glob(filepath.Join(config.Dir, "runs", "*", "run.json"))
	if len(runs) != 1 {
		t.Errorf("run saved %d run.json files, want 1", len(runs))
	}
	code, out, _ = runCLI(t, "run", "--url", srv.URL, "--json")
	var res model.Run
	if code != exitOK || json.Unmarshal([]byte(out), &res) != nil || len(res.Actions) != 1 || len(res.Actions[0].Messages) != 1 {
		t.Errorf("run --json: %d\n%s", code, out)
	}
	md := filepath.Join("out", "run.md")
	if code, _, errOut := runCLI(t, "run", "--url", srv.URL, "-o", md, "-q"); code != exitOK {
		t.Errorf("run -o: %d %s", code, errOut)
	} else if b, err := os.ReadFile(md); err != nil || !strings.Contains(string(b), "Sign up") {
		t.Errorf("run -o wrote %q, %v", b, err)
	}

	// An unfilled placeholder is an error issue: --fail-on error exits 1,
	// --fail-on none doesn't.
	writeConfig(t, cfg(shopSite(t, srv.URL, "Hi {first_name}, welcome.").URL))
	if code, out, _ := runCLI(t, "run", "--url", srv.URL, "--plain", "--fail-on", "error"); code != exitFindings ||
		!strings.Contains(out, "Unfilled placeholder {first_name}") {
		t.Errorf("run --fail-on error: %d\n%s", code, out)
	}
	if code, _, _ := runCLI(t, "run", "--url", srv.URL, "--plain", "--fail-on", "none"); code != exitOK {
		t.Errorf("run --fail-on none: %d", code)
	}

	// A site listed under production: is refused, so nothing ran.
	writeConfig(t, cfg(good.URL)+"production: ["+good.URL+"]\n")
	if code, out, errOut := runCLI(t, "run", "--url", srv.URL, "--plain"); code != exitError || !strings.Contains(out+errOut, "listed under production") {
		t.Errorf("production site: %d\n%s\n%s", code, out, errOut)
	}

	writeConfig(t, cfg(good.URL))
	for _, args := range [][]string{{"run", "now"}, {"run", "--action", "Nope"}, {"run", "--fail-on", "broke"}} {
		if code, _, errOut := runCLI(t, append(args, "--url", srv.URL)...); code != exitUsage {
			t.Errorf("%v: exit %d %s", args, code, errOut)
		}
	}
	if code, _, errOut := runCLI(t, "run", "--url", "http://127.0.0.1:1"); code != exitError || !strings.Contains(errOut, "rhythm echo:") {
		t.Errorf("run without Echo: %d %s", code, errOut)
	}
}

func TestClearCommand(t *testing.T) {
	t.Chdir(t.TempDir())
	srv := httptest.NewServer(catcher.NewServer(catcher.NewInbox(100), catcher.Config{Version: "test"}))
	defer srv.Close()
	echo := client.New(srv.URL, "")
	for _, to := range []string{"ann@shop.test", "bob@shop.test"} {
		if _, err := echo.Capture(context.Background(), wire.Capture{Kind: "email", Source: wire.Source{Via: "test"},
			Email: &wire.Email{To: []string{to}, Subject: "Hi"}}); err != nil {
			t.Fatal(err)
		}
	}
	if code, out, _ := runCLI(t, "clear", "--url", srv.URL, "--to", "ann@shop.test"); code != exitOK || out != "Deleted 1 message\n" {
		t.Errorf("clear --to: %d %q", code, out)
	}
	if code, out, _ := runCLI(t, "clear", "--url", srv.URL); code != exitOK || out != "Deleted 1 message\n" {
		t.Errorf("clear: %d %q", code, out)
	}
	if code, _, errOut := runCLI(t, "ls", "--url", srv.URL); code != exitOK || !strings.Contains(errOut, "No messages captured yet.") {
		t.Errorf("ls after clear: %d %q", code, errOut)
	}
	if code, _, _ := runCLI(t, "clear", "--url", "http://127.0.0.1:1"); code != exitError {
		t.Errorf("clear without Echo: %d", code)
	}
}

func TestSendmailCommand(t *testing.T) {
	s := catcher.NewServer(catcher.NewInbox(100), catcher.Config{Version: "test"})
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	sink := catcher.NewSMTP(s, ln.Addr().String())
	go func() { _ = sink.Serve(ln) }()
	defer func() { _ = sink.Close() }()
	for _, k := range []string{sendmail.EnvURL, sendmail.EnvToken, sendmail.EnvForce, sendmail.EnvRun, "WP_ENVIRONMENT_TYPE", "APP_ENV", "VERCEL_ENV", "NODE_ENV"} {
		t.Setenv(k, "")
	}
	t.Setenv(sendmail.EnvSMTP, ln.Addr().String())
	t.Chdir(t.TempDir())

	// What PHP's mail() runs: sendmail -t -i, the message on stdin.
	var stdout, stderr bytes.Buffer
	msg := "To: ann@shop.test\nSubject: Order shipped\n\nYour order is on its way.\n"
	if code := run([]string{"sendmail", "-t", "-i"}, strings.NewReader(msg), &stdout, &stderr); code != 0 {
		t.Fatalf("sendmail: %d %s", code, stderr.String())
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	ms := s.Inbox.Wait(ctx, catcher.Filter{}, 1, 100*time.Millisecond)
	if len(ms) != 1 || ms[0].Subject != "Order shipped" || len(ms[0].To) != 1 || ms[0].To[0] != "ann@shop.test" {
		t.Errorf("caught %+v", ms)
	}
	if stdout.Len() != 0 {
		t.Errorf("sendmail writes nothing to stdout: %q", stdout.String())
	}
	// sendmail's own exit codes: 64 for a message with no recipients.
	stderr.Reset()
	if code := run([]string{"sendmail"}, strings.NewReader("Subject: x\n\nbody\n"), &stdout, &stderr); code != 64 ||
		!strings.Contains(stderr.String(), "no recipients") {
		t.Errorf("no recipients: %d %s", code, stderr.String())
	}
}

// noopConfig is a config with one action that sends nothing.
const noopConfig = "quiet: 100ms\ntimeout: 2s\nactions:\n  - name: Noop\n    command: \"true\"\n"

// writeConfigAt writes cfg as dir/.echo/config.yaml.
func writeConfigAt(t *testing.T, dir, cfg string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Join(dir, config.Dir), 0o750); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, config.Path()), []byte(cfg), 0o600); err != nil {
		t.Fatal(err)
	}
}

func TestConfigIsFoundAboveAndKeepsTheStore(t *testing.T) {
	if _, err := exec.LookPath("true"); err != nil {
		t.Skip("no true")
	}
	srv := httptest.NewServer(catcher.NewServer(catcher.NewInbox(100), catcher.Config{Version: "test"}))
	defer srv.Close()
	root := t.TempDir()
	t.Setenv("HOME", root)
	proj, other := filepath.Join(root, "proj"), filepath.Join(root, "other")
	writeConfigAt(t, proj, noopConfig)
	writeConfigAt(t, other, noopConfig)
	sub := filepath.Join(proj, "sub", "deeper")
	if err := os.MkdirAll(sub, 0o750); err != nil {
		t.Fatal(err)
	}

	// From a subfolder, the project's config and store are used.
	t.Chdir(sub)
	if code, out, errOut := runCLI(t, "snapshot", "--url", srv.URL, "--name", "base", "--plain"); code != 0 {
		t.Fatalf("snapshot from a subfolder: %d\n%s\n%s", code, out, errOut)
	}
	if _, err := os.Stat(filepath.Join(proj, config.Dir, "snapshots", "base", "run.json")); err != nil {
		t.Errorf("the snapshot isn't in proj/.echo: %v", err)
	}
	if _, err := os.Stat(filepath.Join(sub, config.Dir)); err == nil {
		t.Error("a .echo folder was made in the subfolder")
	}
	if code, out, errOut := runCLI(t, "compare", "--url", srv.URL, "--plain"); code != 0 {
		t.Errorf("compare from a subfolder: %d\n%s\n%s", code, out, errOut)
	}

	// --config moves the store with it.
	t.Chdir(root)
	cfg := filepath.Join("other", config.Path())
	if code, out, errOut := runCLI(t, "snapshot", "--config", cfg, "--url", srv.URL, "--name", "theirs", "--plain"); code != 0 {
		t.Fatalf("snapshot --config: %d\n%s\n%s", code, out, errOut)
	}
	if _, err := os.Stat(filepath.Join(other, config.Dir, "snapshots", "theirs", "run.json")); err != nil {
		t.Errorf("the snapshot isn't in other/.echo: %v", err)
	}
	if code, out, errOut := runCLI(t, "compare", "--config", cfg, "--url", srv.URL, "--plain"); code != 0 || !strings.Contains(out, "theirs") {
		t.Errorf("compare --config didn't read other/.echo/snapshots: %d\n%s\n%s", code, out, errOut)
	}
	if code, out, _ := runCLI(t, "snapshots", "--config", cfg); code != 0 || !strings.Contains(out, "theirs") || strings.Contains(out, "base") {
		t.Errorf("snapshots --config:\n%s", out)
	}

	// --store puts them anywhere.
	storeDir := filepath.Join(root, "ci-store")
	if code, out, errOut := runCLI(t, "snapshot", "--config", cfg, "--store", storeDir, "--url", srv.URL, "--name", "ci", "--plain"); code != 0 {
		t.Fatalf("snapshot --store: %d\n%s\n%s", code, out, errOut)
	}
	if _, err := os.Stat(filepath.Join(storeDir, "snapshots", "ci", "run.json")); err != nil {
		t.Errorf("the snapshot isn't in --store: %v", err)
	}
	if code, out, _ := runCLI(t, "report", "ci", "--store", storeDir, "--plain"); code != 0 || !strings.Contains(out, "ci") {
		t.Errorf("report --store:\n%s", out)
	}
}

func TestMissingConfigSaysRunInit(t *testing.T) {
	root := t.TempDir()
	t.Setenv("HOME", root)
	t.Chdir(root)
	const want = "rhythm echo: no .echo/config.yaml here or above; run rhythm echo init\n"
	for _, args := range [][]string{{"snapshot"}, {"compare"}, {"run"}, {"doctor"}} {
		code, out, errOut := runCLI(t, append(args, "--url", "http://127.0.0.1:1")...)
		if code != exitUsage || errOut != want || out != "" {
			t.Errorf("%v: exit %d, stdout %q, stderr %q; want 2 and %q", args, code, out, errOut, want)
		}
	}
	// Commands that work without actions still run on the defaults.
	if code, _, errOut := runCLI(t, "snapshots"); code != 0 {
		t.Errorf("snapshots without a config: %d %q", code, errOut)
	}
}

func TestFindConfigStopsAtTheRepoAndHome(t *testing.T) {
	root := t.TempDir()
	home := filepath.Join(root, "home")
	t.Setenv("HOME", home)
	mk := func(parts ...string) string {
		d := filepath.Join(append([]string{root}, parts...)...)
		if err := os.MkdirAll(d, 0o750); err != nil {
			t.Fatal(err)
		}
		return d
	}
	// A config above a repository's root isn't the repository's.
	writeConfigAt(t, root, noopConfig)
	repo := mk("home", "repo")
	mk("home", "repo", ".git")
	if _, err := findConfig(mk("home", "repo", "a", "b")); !errors.Is(err, errNoConfig) {
		t.Errorf("found a config above the repository: %v", err)
	}
	writeConfigAt(t, repo, noopConfig)
	if got, err := findConfig(filepath.Join(repo, "a", "b")); err != nil || got != filepath.Join(repo, config.Path()) {
		t.Errorf("findConfig in the repo = %q, %v", got, err)
	}
	// Outside a repository the search ends below $HOME.
	writeConfigAt(t, home, noopConfig)
	if _, err := findConfig(mk("home", "loose", "x")); !errors.Is(err, errNoConfig) {
		t.Errorf("found ~/.echo from a folder below it: %v", err)
	}
	if got, err := findConfig(home); err != nil || got != filepath.Join(home, config.Path()) {
		t.Errorf("findConfig($HOME) = %q, %v", got, err)
	}
}

// fixtureServer serves h on 127.0.0.1:port, a port in the range these
// tests keep for their own servers.
func fixtureServer(t *testing.T, port int, h http.Handler) *httptest.Server {
	t.Helper()
	ln, err := net.Listen("tcp", fmt.Sprintf("127.0.0.1:%d", port))
	if err != nil {
		t.Fatalf("fixture port %d: %v", port, err)
	}
	srv := httptest.NewUnstartedServer(h)
	_ = srv.Listener.Close()
	srv.Listener = ln
	srv.Start()
	t.Cleanup(srv.Close)
	return srv
}

// writeFiles creates each name in dir with the given contents.
func writeFiles(t *testing.T, dir string, files map[string]string) {
	t.Helper()
	for name, body := range files {
		p := filepath.Join(dir, name)
		if err := os.MkdirAll(filepath.Dir(p), 0o750); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
	}
}

func TestDetectStack(t *testing.T) {
	for _, tc := range []struct {
		files []string
		want  string
	}{
		{[]string{"wp-config.php", "index.php"}, stackWordPress},
		{[]string{"wp-load.php"}, stackWordPress},
		{[]string{"artisan", "package.json", "index.php"}, stackLaravel},
		{[]string{"next.config.mjs", "package.json"}, stackNext},
		{[]string{"package.json"}, stackNode},
		{[]string{"index.php"}, stackPHP},
		{[]string{"README.md"}, ""},
	} {
		dir := t.TempDir()
		files := map[string]string{}
		for _, f := range tc.files {
			files[f] = ""
		}
		writeFiles(t, dir, files)
		if got := detectStack(dir); got != tc.want {
			t.Errorf("%v: detectStack = %q, want %q", tc.files, got, tc.want)
		}
	}
}

func TestInitWritesAnActionPerStack(t *testing.T) {
	for _, tc := range []struct {
		files      map[string]string
		args       []string
		action     string
		site, says string
	}{
		{map[string]string{"wp-config.php": "<?php\ndefine( 'WP_HOME', 'https://staging.shop.test/' );\n"}, nil,
			"Lost password", "https://staging.shop.test", "for a WordPress site"},
		{map[string]string{"artisan": "", ".env": "APP_URL=\"http://shop.test\"\n"}, nil, "Connector canary", "http://shop.test", "for a Laravel site"},
		{map[string]string{"next.config.ts": "", "package.json": "{}"}, nil, "Sign-up", "http://localhost:3000", "for a Next.js site"},
		{map[string]string{"package.json": "{}"}, nil, "Sign-up", "http://localhost:3000", "for a Node site"},
		{map[string]string{"index.php": ""}, nil, "Home page", "", "Set site: in it"},
		{map[string]string{}, nil, "Home page", "", "with one action to start from (Home page)"},
		{map[string]string{"index.php": ""}, []string{"--stack", "WP"}, "Lost password", "", "for a WordPress site"},
	} {
		dir := t.TempDir()
		t.Chdir(dir)
		writeFiles(t, dir, tc.files)
		code, out, errOut := runCLI(t, append([]string{"init"}, tc.args...)...)
		if code != 0 || !strings.Contains(out, tc.says) {
			t.Errorf("%v %v: init exit %d\n%s\n%s", tc.files, tc.args, code, out, errOut)
			continue
		}
		cfg, err := config.Load(config.Path())
		if err != nil {
			t.Errorf("%v: the config doesn't load: %v", tc.files, err)
			continue
		}
		if len(cfg.Actions) != 1 || cfg.Actions[0].Name != tc.action || cfg.Site != tc.site {
			t.Errorf("%v: actions %+v, site %q; want one %q and site %q", tc.files, cfg.Actions, cfg.Site, tc.action, tc.site)
		}
		b, _ := os.ReadFile(config.Path())
		if tc.action != "Lost password" && strings.Contains(string(b), "woocommerce") {
			t.Errorf("%v: a non-WordPress config has WordPress examples:\n%s", tc.files, b)
		}
	}
	t.Chdir(t.TempDir())
	if code, _, errOut := runCLI(t, "init", "--stack", "rails"); code != exitUsage || !strings.Contains(errOut, `--stack "rails": use wordpress, laravel, next, node or php`) {
		t.Errorf("init --stack rails: %d %q", code, errOut)
	}
}

// echoHealth answers /v1/health as an Echo that reports its SMTP sink at
// smtp, and everything else as h.
func echoHealth(h http.Handler, smtp string) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/health" {
			h.ServeHTTP(w, r)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprintf(w, `{"tool":"rhythm-echo","version":"test","mode":"seal","wire":1,"smtp":%q,"proxy":"off","providers":[]}`, smtp)
	})
}

// fakeMailpitSMTP greets as Mailpit on port and accepts any mail.
func fakeMailpitSMTP(t *testing.T, port int) {
	t.Helper()
	ln, err := net.Listen("tcp", fmt.Sprintf("127.0.0.1:%d", port))
	if err != nil {
		t.Fatalf("fixture port %d: %v", port, err)
	}
	t.Cleanup(func() { _ = ln.Close() })
	go func() {
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			go func() {
				defer func() { _ = c.Close() }()
				_ = c.SetDeadline(time.Now().Add(5 * time.Second))
				fmt.Fprint(c, "220 mailpit Mailpit ESMTP Service ready\r\n")
				r := bufio.NewReader(c)
				data := false
				for {
					line, err := r.ReadString('\n')
					if err != nil {
						return
					}
					switch cmd := strings.ToUpper(strings.TrimSpace(line)); {
					case data:
						if cmd == "." {
							data = false
							fmt.Fprint(c, "250 OK\r\n")
						}
					case strings.HasPrefix(cmd, "EHLO"), strings.HasPrefix(cmd, "HELO"):
						fmt.Fprint(c, "250 mailpit\r\n")
					case cmd == "DATA":
						data = true
						fmt.Fprint(c, "354 go ahead\r\n")
					case cmd == "QUIT":
						fmt.Fprint(c, "221 bye\r\n")
						return
					default:
						fmt.Fprint(c, "250 OK\r\n")
					}
				}
			}()
		}
	}()
}

func TestDoctorNamesMailpit(t *testing.T) {
	t.Chdir(t.TempDir())
	writeConfig(t, "smtp: \"off\"\n")
	mailpit := fixtureServer(t, 20462, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/v1/info" {
			fmt.Fprint(w, `{"Version":"v1.21.0","Database":"/data/mailpit.db"}`)
			return
		}
		http.NotFound(w, r)
	}))
	code, out, _ := runCLI(t, "doctor", "--url", mailpit.URL, "--plain")
	if code != exitFindings || !strings.Contains(out, mailpit.URL+" is Mailpit, not Echo; start Echo on a free port") {
		t.Errorf("doctor against Mailpit's API: %d\n%s", code, out)
	}

	// The SMTP port comes from Echo's health, and Mailpit on it is named.
	fakeMailpitSMTP(t, 20464)
	echo := fixtureServer(t, 20463, echoHealth(catcher.NewServer(catcher.NewInbox(10), catcher.Config{Version: "test"}), "0.0.0.0:20464"))
	code, out, _ = runCLI(t, "doctor", "--url", echo.URL, "--plain")
	if code != exitFindings || !strings.Contains(out, "127.0.0.1:20464 is Mailpit, not Echo's sink") {
		t.Errorf("doctor with Mailpit on the SMTP port: %d\n%s", code, out)
	}
}

func TestDoctorRunsOnlyTheStacksChecks(t *testing.T) {
	echo := fixtureServer(t, 20465, catcher.NewServer(catcher.NewInbox(10), catcher.Config{Version: "test"}))
	for _, tc := range []struct {
		files      map[string]string
		want, not  string
		wantStatus int
	}{
		{map[string]string{"index.php": "<?php mail('a@b.test', 'Hi', 'Hi');"}, "note  PHP: no connector to check", "WordPress", exitOK},
		{map[string]string{"README.md": ""}, "note  Site: no Echo connector answered at http://127.0.0.1:20479", "WordPress", exitOK},
		{map[string]string{"wp-config.php": ""}, "note  WordPress: no connector answered", "PHP:", exitOK},
	} {
		dir := t.TempDir()
		t.Chdir(dir)
		writeFiles(t, dir, tc.files)
		writeConfig(t, "site: http://127.0.0.1:20479\nsmtp: \"off\"\n")
		code, out, errOut := runCLI(t, "doctor", "--url", echo.URL, "--plain")
		if code != tc.wantStatus || !strings.Contains(out, tc.want) || strings.Contains(out, tc.not) {
			t.Errorf("%v: doctor exit %d, want %d, %q and no %q\n%s%s", tc.files, code, tc.wantStatus, tc.want, tc.not, out, errOut)
		}
	}
}

// fakeNodeConnector is a stand-in @rhythmsh/echo with the parts the
// doctor's canary uses: the state the real connector keeps on
// globalThis, a fetch hook that asks Echo first and flush.
var fakeNodeConnector = map[string]string{
	"node_modules/@rhythmsh/echo/package.json": `{"name":"@rhythmsh/echo","type":"module","exports":{".":"./index.js","./register":"./register.js"}}`,
	"node_modules/@rhythmsh/echo/index.js":     "export async function flush() {}\n",
	"node_modules/@rhythmsh/echo/register.js": `globalThis[Symbol.for('rhythm.echo.state')] = { active: true, mode: 'seal' };
const orig = globalThis.fetch;
globalThis.fetch = async (url, init = {}) => {
  const r = await orig(process.env.RHYTHM_ECHO_URL + '/v1/capture', {
    method: 'POST',
    headers: { 'content-type': 'application/json' },
    body: JSON.stringify({ v: 1, kind: 'http', phase: 'before', run: process.env.RHYTHM_ECHO_RUN,
      source: { via: 'node', hook: 'fetch' }, http: { method: init.method || 'GET', url: String(url), body: String(init.body || '') } }),
  });
  const d = await r.json();
  if (d.action !== 'fake') throw new Error('Echo did not fake it: ' + JSON.stringify(d));
  return new Response(d.response?.body ?? '', { status: d.response?.status ?? 200 });
};
`,
}

func TestDoctorNodeCanary(t *testing.T) {
	if _, err := exec.LookPath("node"); err != nil {
		t.Skip("no node")
	}
	inbox := catcher.NewInbox(10)
	echo := fixtureServer(t, 20466, catcher.NewServer(inbox, catcher.Config{Version: "test"}))
	dir := t.TempDir()
	t.Chdir(dir)
	writeConfig(t, "smtp: \"off\"\n")
	writeFiles(t, dir, map[string]string{"package.json": "{}"})
	code, out, _ := runCLI(t, "doctor", "--url", echo.URL, "--plain")
	if code != exitFindings || !strings.Contains(out, "FAIL  Node: @rhythmsh/echo isn't installed in this folder; add it: rhythm echo install node") {
		t.Errorf("doctor without the connector: %d\n%s", code, out)
	}
	writeFiles(t, dir, fakeNodeConnector)
	code, out, errOut := runCLI(t, "doctor", "--url", echo.URL, "--plain")
	if code != exitOK || !strings.Contains(out, "ok    Node: the connector caught a fetch to Twilio and Echo answered for it") {
		t.Errorf("doctor with the connector: %d\n%s%s", code, out, errOut)
	}
	if strings.Contains(out, "WordPress") {
		t.Errorf("a Node project got a WordPress line:\n%s", out)
	}
}

func TestInstallNodeAndNextNeedAProject(t *testing.T) {
	t.Chdir(t.TempDir())
	for _, stack := range []string{"node", "next"} {
		code, out, errOut := runCLI(t, "install", stack)
		if code != exitUsage || out != "" || !strings.Contains(errOut, "this folder has no package.json; run this in the") {
			t.Errorf("install %s outside a project: %d %q %q", stack, code, out, errOut)
		}
	}
}
