// Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
// SPDX-License-Identifier: MIT

package main

import (
	"archive/tar"
	"bufio"
	"bytes"
	"compress/gzip"
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
		cfg := captureConfig(srv.URL, subject)
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
	var c struct{ Changes []model.Change } // data's actions are the run's, not a count
	if env := decodeEnvelope(t, data, kindComparison, &c); env.Summary.Broke == 0 || env.Verdict != verdictFindings {
		t.Errorf("summary %+v, verdict %q; want a BROKE and findings", env.Summary, env.Verdict)
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

// assertPrivate fails for any file or folder under paths that others
// can read: reports and runs hold the captured messages themselves.
// Files must be 0600 and folders 0700.
func assertPrivate(t *testing.T, paths ...string) {
	t.Helper()
	if runtime.GOOS == "windows" {
		return // no Unix modes
	}
	seen := 0
	for _, root := range paths {
		err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
			if err != nil {
				return err
			}
			info, err := d.Info()
			if err != nil {
				return err
			}
			want := fs.FileMode(0o600)
			if d.IsDir() {
				want = 0o700
			} else {
				seen++
			}
			if mode := info.Mode().Perm(); mode != want {
				t.Errorf("%s: mode %o, want %o", path, mode, want)
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
		{[]string{"init", "extra"}, `init takes no arguments, got "extra"`},
		{[]string{"ca", "extra"}, `ca takes no arguments, got "extra"`},
		{[]string{"ca", "--pem", "--untrust"}, "pick --pem or --untrust, not both"},
		{[]string{"ca", "--pme"}, "unknown flag --pme; did you mean --pem?"},
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
		{[]string{"init", "--stak", "x"}, "unknown flag --stak; did you mean --stack?"},
		{[]string{"install", "wordpress", "--uninstal"}, "did you mean --uninstall?"},
		{[]string{"install", "wordpress", "--contaner", "wp"}, "did you mean --container?"},
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
		{[]string{"doctor", "--md"}, "--md has no effect on doctor"},
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
	if got := strings.Join(values(pickState{}), " "); got != "up install doctor consent init" {
		t.Errorf("fresh folder: %s", got)
	}
	ps := pickState{running: true, hasConfig: true, hasActions: true, snapshot: `"before", 2 hours ago`, hasResult: true}
	if got := strings.Join(values(ps), " "); got != "watch compare snapshot run report install doctor consent" {
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
		{"consent", "", " https://staging.shop.test ", true, "consent -- https://staging.shop.test"},
		{"consent", "", "--send-hits", false, "consent -- --send-hits"},
		{"consent", "", "detect", false, "consent -- detect"},
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

// TestWatchShowsWhatArrivedWhileItStarted: a terminal that doesn't
// answer lipgloss's color query holds watch up for seconds before its
// first line. A message captured in that time, after the command began,
// still shows.
func TestWatchShowsWhatArrivedWhileItStarted(t *testing.T) {
	t.Chdir(t.TempDir())
	in := catcher.NewInbox(100)
	srv := httptest.NewServer(catcher.NewServer(in, catcher.Config{Version: "test"}))
	defer srv.Close()
	watch, _ := lookup("watch")
	p, err := parse(watch, []string{"--url", srv.URL, "--plain"})
	if err != nil {
		t.Fatal(err)
	}
	time.Sleep(10 * time.Millisecond)
	in.Add(model.Message{Channel: model.Email, To: []string{"ann@shop.test"}, Subject: "Sent while watch started", Status: model.Faked})
	out, errb := &syncBuffer{}, &syncBuffer{}
	s := p.begin(out, errb)
	ctx, cancel := context.WithCancel(context.Background())
	s.ctx = ctx
	done := make(chan int)
	go func() { done <- cmdWatch(s) }()
	defer func() {
		cancel()
		<-done
		s.stop()
	}()
	waitFor(t, func() bool { return strings.Contains(out.String(), "Sent while watch started") }, "the message sent while watch started")
}

// TestWatchShowsCapturesOfARun is the session the bug was reported from:
// a connector POSTs captures tagged with a run to /v1/capture while that
// run's action is open and watch prints each one as it arrives.
func TestWatchShowsCapturesOfARun(t *testing.T) {
	t.Chdir(t.TempDir())
	in := catcher.NewInbox(100)
	srv := httptest.NewServer(catcher.NewServer(in, catcher.Config{Version: "test"}))
	defer srv.Close()
	c := client.New(srv.URL, "")
	lease, err := c.BeginLease(context.Background(), "r1", "contact")
	if err != nil {
		t.Fatal(err)
	}
	watch, _ := lookup("watch")
	p, err := parse(watch, []string{"--url", srv.URL, "--plain"})
	if err != nil {
		t.Fatal(err)
	}
	out, errb := &syncBuffer{}, &syncBuffer{}
	s := p.begin(out, errb)
	ctx, cancel := context.WithCancel(context.Background())
	s.ctx = ctx
	done := make(chan int)
	go func() { done <- cmdWatch(s) }()
	defer func() {
		cancel()
		<-done
		s.stop()
	}()
	waitFor(t, func() bool { return strings.Contains(errb.String(), "Watching") }, "the watch to start")
	for i, subject := range []string{"New message from Jamie", "Thanks for getting in touch"} {
		body := fmt.Sprintf(`{"v":1,"run":"r1:contact","kind":"email","source":{"via":"wordpress","hook":"wp_mail"},`+
			`"email":{"from":"shop@shop.test","to":["owner%d@shop.test"],"subject":%q,"text":"Hi"}}`, i, subject)
		resp, err := http.Post(srv.URL+"/v1/capture", "application/json", strings.NewReader(body)) //nolint:noctx // test
		if err != nil {
			t.Fatal(err)
		}
		_ = resp.Body.Close()
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("capture: %s", resp.Status)
		}
		waitFor(t, func() bool { return strings.Contains(out.String(), subject) }, subject)
	}
	if err := c.EndLease(context.Background(), lease); err != nil {
		t.Fatal(err)
	}
}

func TestLRUForgetsTheOldest(t *testing.T) {
	c := newLRU[model.Status](2)
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
	if decodeEnvelope(t, []byte(out), kindMessages, &ms); code != 0 || len(ms) != 1 || ms[0].Subject != "New" {
		t.Errorf("ls --since 10m: %d %s\n%s", code, errOut, out)
	}
	if code, _, errOut := runCLI(t, "ls", "--since", "-5m"); code != exitUsage || !strings.Contains(errOut, "rhythm echo: --since -5m: give how long ago, e.g. 10m") {
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
	if decodeEnvelope(t, []byte(out), kindRun, &res); code != exitOK || len(res.Actions) != 1 || len(res.Actions[0].Messages) != 1 {
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
	if code, out, errOut := runCLI(t, "run", "--url", srv.URL, "--plain"); code != exitError || !strings.Contains(out+errOut, "under production; actions only run against staging") {
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
	t.Setenv("USERPROFILE", home) // os.UserHomeDir on Windows
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

// From a subfolder, flush and command actions run in the project's
// folder, as they do from the folder itself and the command says which
// config it uses.
func TestCommandsRunInTheProjectFolder(t *testing.T) {
	if _, err := exec.LookPath("test"); err != nil {
		t.Skip("no test")
	}
	srv := httptest.NewServer(catcher.NewServer(catcher.NewInbox(100), catcher.Config{Version: "test"}))
	defer srv.Close()
	root := t.TempDir()
	t.Setenv("HOME", root)
	proj := filepath.Join(root, "proj")
	writeConfigAt(t, proj, "smtp: \"off\"\nquiet: 100ms\ntimeout: 2s\nflush: [\"test -f marker\"]\n"+
		"actions:\n  - name: Marker\n    command: \"test -f marker\"\n")
	writeFiles(t, proj, map[string]string{"marker": "", "sub/keep": ""})
	t.Chdir(filepath.Join(proj, "sub"))
	wantNote := "rhythm echo: using " + filepath.Join(proj, config.Path()) + "; its flush and command actions run in " + proj + "\n"

	code, out, errOut := runCLI(t, "doctor", "--url", srv.URL, "--plain")
	if !strings.Contains(out, "ok    Flush: test -f marker") || errOut != wantNote {
		t.Errorf("doctor from a subfolder: %d\n%s\n%q", code, out, errOut)
	}
	if code, out, errOut := runCLI(t, "run", "--url", srv.URL, "--plain"); code != exitOK || !strings.HasPrefix(errOut, wantNote) {
		t.Errorf("run from a subfolder: %d\n%s\n%s", code, out, errOut)
	}
	if wd, _ := os.Getwd(); wd != filepath.Join(proj, "sub") {
		t.Errorf("run left the working folder at %s", wd)
	}
	// -q keeps the note out; from the project folder there is none.
	if _, _, errOut := runCLI(t, "run", "--url", srv.URL, "-q"); strings.Contains(errOut, "using ") {
		t.Errorf("run -q said %q", errOut)
	}
	// snapshot --export and --import run no actions, so they don't say
	// where those would run.
	if code, _, errOut := runCLI(t, "snapshot", "--url", srv.URL, "--name", "base", "-q"); code != exitOK {
		t.Fatalf("snapshot: %d %s", code, errOut)
	}
	if code, out, errOut := runCLI(t, "snapshot", "--export", "-"); code != exitOK || out == "" || strings.Contains(errOut, "using ") {
		t.Errorf("snapshot --export - from a subfolder: %d %q", code, errOut)
	}
	t.Chdir(proj)
	if _, _, errOut := runCLI(t, "run", "--url", srv.URL, "--plain"); strings.Contains(errOut, "using ") {
		t.Errorf("run in the project folder said %q", errOut)
	}
}

// A config in a folder above that anyone may write to isn't used: the
// command says so and runs on the defaults, so its flush never runs and
// the token isn't sent to its listen: address.
func TestPlantedConfigIsNotUsed(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("no Unix modes: Windows trusts every folder")
	}
	root := t.TempDir()
	t.Setenv("HOME", filepath.Join(root, "home"))
	shared := filepath.Join(root, "shared")
	writeConfigAt(t, shared, "listen: 127.0.0.1:20479\nflush: [\"touch "+filepath.Join(shared, "PWNED")+"\"]\n")
	if err := os.Chmod(shared, 0o777); err != nil { //nolint:gosec // the point of the test
		t.Fatal(err)
	}
	victim := filepath.Join(shared, "victim", "proj")
	if err := os.MkdirAll(victim, 0o750); err != nil {
		t.Fatal(err)
	}
	t.Chdir(victim)
	planted := filepath.Join(shared, config.Path())
	want := "rhythm echo: not using " + planted + ": its folder is writable by anyone (someone else could have put it there); " +
		"pass --config " + planted + " if you trust it\n"
	code, out, errOut := runCLI(t, "doctor", "--url", "http://127.0.0.1:1")
	if code != exitUsage || out != "" || errOut != want+"rhythm echo: no .echo/config.yaml here or above; run rhythm echo init\n" {
		t.Errorf("doctor below a planted config: %d %q %q", code, out, errOut)
	}
	if _, err := os.Stat(filepath.Join(shared, "PWNED")); err == nil {
		t.Error("the planted flush ran")
	}
	if code, _, errOut := runCLI(t, "snapshots"); code != exitOK || errOut != want {
		t.Errorf("snapshots below a planted config: %d %q", code, errOut)
	}
}

// fixtureServer serves h on 127.0.0.1:port, a port in the range these
// tests keep for their own servers, or a free one for port 0 (for a
// test that reaches it only through its URL, so a busy machine running
// the suite twice at once can't collide on it).
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
// smtp and everything else as h.
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
	mailpit := fixtureServer(t, 0, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/v1/info" {
			fmt.Fprint(w, `{"Version":"v1.21.0","Database":"/data/mailpit.db"}`)
			return
		}
		http.NotFound(w, r)
	}))
	code, out, _ := runCLI(t, "doctor", "--url", mailpit.URL, "--plain")
	if code != exitError || !strings.Contains(out, mailpit.URL+" is Mailpit, not Echo; start Echo on a free port") {
		t.Errorf("doctor against Mailpit's API: %d\n%s", code, out)
	}

	// The SMTP port comes from Echo's health and Mailpit on it is named.
	fakeMailpitSMTP(t, 20464)
	echo := fixtureServer(t, 0, echoHealth(catcher.NewServer(catcher.NewInbox(10), catcher.Config{Version: "test"}), "0.0.0.0:20464"))
	code, out, _ = runCLI(t, "doctor", "--url", echo.URL, "--plain")
	if code != exitFindings || !strings.Contains(out, "127.0.0.1:20464 is Mailpit, not Echo's sink") {
		t.Errorf("doctor with Mailpit on the SMTP port: %d\n%s", code, out)
	}
}

func TestDoctorRunsOnlyTheStacksChecks(t *testing.T) {
	echo := fixtureServer(t, 0, catcher.NewServer(catcher.NewInbox(10), catcher.Config{Version: "test"}))
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
	echo := fixtureServer(t, 0, catcher.NewServer(inbox, catcher.Config{Version: "test"}))
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

// captureConfig is a config whose one action posts an email with subject
// to the Echo at echoURL.
func captureConfig(echoURL, subject string) string {
	capture := fmt.Sprintf(`curl -s -X POST %s/v1/capture -d "{\"v\":1,\"kind\":\"email\",\"run\":\"$RHYTHM_ECHO_RUN\",\"source\":{\"via\":\"test\"},\"email\":{\"from\":\"shop@shop.test\",\"to\":[\"admin@shop.test\"],\"subject\":\"%s\",\"text\":\"New order\"}}" >/dev/null`, echoURL, subject)
	return fmt.Sprintf("quiet: 300ms\ntimeout: 5s\nactions:\n  - name: New order\n    command: |\n      %s\n", forShell(capture))
}

// forShell turns a test's sh command line into one cmd.exe runs the same
// way on Windows, where command actions go through cmd /C: %VAR% for
// $RHYTHM_ECHO_RUN and NUL for /dev/null.
func forShell(cmd string) string {
	if runtime.GOOS != "windows" {
		return cmd
	}
	return strings.NewReplacer("$RHYTHM_ECHO_RUN", "%RHYTHM_ECHO_RUN%", ">/dev/null", ">NUL").Replace(cmd)
}

func TestBaselinesTravel(t *testing.T) {
	if _, err := exec.LookPath("curl"); err != nil {
		t.Skip("no curl")
	}
	echo := fixtureServer(t, 0, catcher.NewServer(catcher.NewInbox(1000), catcher.Config{Version: "test"}))
	root := t.TempDir()
	t.Setenv("HOME", root)
	a, b := filepath.Join(root, "a"), filepath.Join(root, "b")
	writeConfigAt(t, a, captureConfig(echo.URL, "Order received"))
	writeConfigAt(t, b, captureConfig(echo.URL, "Order received"))

	t.Chdir(a)
	if code, out, errOut := runCLI(t, "snapshot", "--url", echo.URL, "--name", "base", "--plain"); code != 0 {
		t.Fatalf("snapshot: %d\n%s\n%s", code, out, errOut)
	}
	tgz := filepath.Join(root, "base.tgz")
	code, out, errOut := runCLI(t, "snapshot", "--export", tgz)
	if code != 0 || !strings.Contains(out, "Exported snapshot base to "+tgz) {
		t.Fatalf("export: %d\n%s\n%s", code, out, errOut)
	}
	assertArchive(t, tgz, "base/run.json", "base/report.html")
	runJSON := filepath.Join(a, config.Dir, "snapshots", "base", "run.json")

	// A fresh checkout imports it and compares as the first one would.
	t.Chdir(b)
	if code, out, errOut := runCLI(t, "snapshot", "--import", tgz); code != 0 || !strings.Contains(out, "Imported snapshot base") {
		t.Fatalf("import: %d\n%s\n%s", code, out, errOut)
	}
	assertPrivate(t, filepath.Join(config.Dir, "snapshots"))
	for _, args := range [][]string{{}, {"--against", tgz}, {"--against", runJSON}, {"--against", filepath.Dir(runJSON)}} {
		code, out, errOut := runCLI(t, append([]string{"compare", "--url", echo.URL, "--plain", "--fail-on", "changed"}, args...)...)
		if code != 0 || !strings.Contains(out, "Everything is still sent the same way") {
			t.Errorf("compare %v: %d\n%s\n%s", args, code, out, errOut)
		}
	}
	writeConfigAt(t, b, captureConfig(echo.URL, "Your order"))
	if code, out, _ := runCLI(t, "compare", "--url", echo.URL, "--plain", "--against", runJSON); code != 0 || !strings.Contains(out, "subject changed") {
		t.Errorf("compare --against run.json after a change: %d\n%s", code, out)
	}

	// Importing over a snapshot needs --force; --name renames.
	if code, _, errOut := runCLI(t, "snapshot", "--import", tgz); code != exitUsage || !strings.Contains(errOut, "snapshot base already exists (--force replaces it") {
		t.Errorf("import over base: %d %q", code, errOut)
	}
	if code, _, errOut := runCLI(t, "snapshot", "--import", tgz, "--name", "from-ci"); code != 0 {
		t.Errorf("import --name: %d %q", code, errOut)
	}
	if r, err := store.LoadRun(filepath.Join(config.Dir, "snapshots", "from-ci")); err != nil || r.Name != "from-ci" {
		t.Errorf("renamed import: %v %+v", err, r)
	}
	if code, _, _ := runCLI(t, "snapshot", "--import", tgz, "--force"); code != 0 {
		t.Error("import --force")
	}
	for _, tc := range []struct {
		args []string
		want string
	}{
		{[]string{"snapshot", "--export", "x.tgz", "--import", tgz}, "pick --export or --import, not both"},
		{[]string{"snapshot", "--export", "x.tgz", "--json"}, "take only --name"},
		{[]string{"compare", "--against", runJSON, "--snapshot", "base"}, "pick --snapshot or --against, not both"},
		{[]string{"compare", "--against", "missing.json"}, "--against missing.json:"},
		{[]string{"snapshot", "--export", "x.tgz", "--name", "nope"}, `no snapshot named "nope"`},
	} {
		if code, _, errOut := runCLI(t, tc.args...); code != exitUsage || !strings.Contains(errOut, tc.want) {
			t.Errorf("%v: %d %q; want 2 and %q", tc.args, code, errOut, tc.want)
		}
	}
}

// assertArchive checks the tgz holds the names and only files that
// are private to their owner.
func assertArchive(t *testing.T, tgz string, names ...string) {
	t.Helper()
	f, err := os.Open(tgz)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	gz, err := gzip.NewReader(f)
	if err != nil {
		t.Fatal(err)
	}
	tr := tar.NewReader(gz)
	seen := map[string]bool{}
	for {
		h, err := tr.Next()
		if err != nil {
			break
		}
		seen[h.Name] = true
		if h.Typeflag == tar.TypeReg && h.Mode != 0o600 {
			t.Errorf("%s is %o in the archive, want 600", h.Name, h.Mode)
		}
	}
	for _, n := range names {
		if !seen[n] {
			t.Errorf("the archive has no %s: %v", n, seen)
		}
	}
}

// evilArchive is a gzipped tar with one entry of the given header.
func evilArchive(t *testing.T, h tar.Header, body string) string {
	t.Helper()
	var buf bytes.Buffer
	gz := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gz)
	run := `{"schema":"` + model.Schema + `","tool":"rhythm-echo","name":"base","actions":[]}`
	_ = tw.WriteHeader(&tar.Header{Typeflag: tar.TypeReg, Name: "base/run.json", Mode: 0o600, Size: int64(len(run))})
	_, _ = tw.Write([]byte(run))
	if h.Typeflag == tar.TypeReg {
		h.Size = int64(len(body))
	}
	if err := tw.WriteHeader(&h); err != nil {
		t.Fatal(err)
	}
	_, _ = tw.Write([]byte(body))
	_ = tw.Close()
	_ = gz.Close()
	p := filepath.Join(t.TempDir(), "evil.tgz")
	if err := os.WriteFile(p, buf.Bytes(), 0o600); err != nil {
		t.Fatal(err)
	}
	return p
}

func TestImportRefusesArchivesThatEscape(t *testing.T) {
	root := t.TempDir()
	t.Setenv("HOME", root)
	work := filepath.Join(root, "work")
	writeConfigAt(t, work, noopConfig)
	t.Chdir(work)
	for _, tc := range []struct {
		h    tar.Header
		want string
	}{
		{tar.Header{Typeflag: tar.TypeReg, Name: "../evil", Mode: 0o600}, "points outside the snapshot"},
		{tar.Header{Typeflag: tar.TypeReg, Name: "base/../../evil", Mode: 0o600}, "points outside the snapshot"},
		{tar.Header{Typeflag: tar.TypeReg, Name: "/tmp/evil", Mode: 0o600}, "points outside the snapshot"},
		{tar.Header{Typeflag: tar.TypeSymlink, Name: "base/evil", Linkname: "/etc/passwd"}, "isn't a plain file"},
		{tar.Header{Typeflag: tar.TypeLink, Name: "base/evil", Linkname: "base/run.json"}, "isn't a plain file"},
		{tar.Header{Typeflag: tar.TypeReg, Name: "other/evil", Mode: 0o600}, "more than one snapshot"},
		{tar.Header{Typeflag: tar.TypeReg, Name: "evil", Mode: 0o600}, "isn't in a snapshot folder"},
	} {
		tgz := evilArchive(t, tc.h, "pwned")
		code, _, errOut := runCLI(t, "snapshot", "--import", tgz)
		if code != exitUsage || !strings.Contains(errOut, "not a snapshot archive: ") || !strings.Contains(errOut, tc.want) {
			t.Errorf("%s: %d %q; want 2 and %q", tc.h.Name, code, errOut, tc.want)
		}
		if code, _, errOut := runCLI(t, "compare", "--against", tgz); code != exitUsage || !strings.Contains(errOut, tc.want) {
			t.Errorf("compare --against %s: %d %q", tc.h.Name, code, errOut)
		}
	}
	for _, p := range []string{filepath.Join(root, "evil"), filepath.Join(work, "evil"), "/tmp/evil"} {
		if _, err := os.Lstat(p); err == nil {
			t.Errorf("%s was written", p)
		}
	}
	if names, _ := os.ReadDir(filepath.Join(config.Dir, "snapshots")); len(names) > 0 {
		t.Errorf("a refused import left %v behind", names)
	}
	// Only the run comes in: the archive's own report.html and other
	// files are dropped and the report is written anew.
	for _, name := range []string{"base/report.html", "base/extra/page.html"} {
		tgz := evilArchive(t, tar.Header{Typeflag: tar.TypeReg, Name: name, Mode: 0o600}, "<script>pwned()</script>")
		if code, _, errOut := runCLI(t, "snapshot", "--import", tgz, "--force"); code != exitOK {
			t.Fatalf("import with %s: %d %q", name, code, errOut)
		}
		dir := filepath.Join(config.Dir, "snapshots", "base")
		if b, err := os.ReadFile(filepath.Join(dir, "report.html")); err != nil || strings.Contains(string(b), "pwned") {
			t.Errorf("import with %s: report.html %v, holds the archive's: %t", name, err, strings.Contains(string(b), "pwned"))
		}
		if _, err := os.Stat(filepath.Join(dir, "extra")); err == nil {
			t.Errorf("import with %s kept extra/", name)
		}
	}

	notGz := filepath.Join(root, "plain.tgz")
	_ = os.WriteFile(notGz, []byte("not gzip"), 0o600)
	if code, _, errOut := runCLI(t, "snapshot", "--import", notGz); code != exitUsage || !strings.Contains(errOut, "not a snapshot archive") {
		t.Errorf("import of a non-archive: %d %q", code, errOut)
	}
}

// tarArchive is a gzipped tar of plain files, written in the order given
// as name, body pairs.
func tarArchive(t *testing.T, entries ...string) string {
	t.Helper()
	var buf bytes.Buffer
	gz := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gz)
	for i := 0; i+1 < len(entries); i += 2 {
		h := tar.Header{Typeflag: tar.TypeReg, Name: entries[i], Mode: 0o600, Size: int64(len(entries[i+1]))}
		if err := tw.WriteHeader(&h); err != nil {
			t.Fatal(err)
		}
		_, _ = tw.Write([]byte(entries[i+1]))
	}
	_ = tw.Close()
	_ = gz.Close()
	p := filepath.Join(t.TempDir(), "crafted.tgz")
	if err := os.WriteFile(p, buf.Bytes(), 0o600); err != nil {
		t.Fatal(err)
	}
	return p
}

func TestImportRefusesReservedNamesAndRepeats(t *testing.T) {
	root := t.TempDir()
	t.Setenv("HOME", root)
	work := filepath.Join(root, "work")
	writeConfigAt(t, work, noopConfig)
	t.Chdir(work)
	run := func(name string) string {
		return `{"schema":"` + model.Schema + `","tool":"rhythm-echo","name":"` + name + `","actions":[]}`
	}
	// A real snapshot first, so the latest pointer exists and must survive.
	if code, _, errOut := runCLI(t, "snapshot", "--import", tarArchive(t, "base/run.json", run("base"))); code != exitOK {
		t.Fatalf("import base: %d %q", code, errOut)
	}
	latest := filepath.Join(config.Dir, "snapshots", ".latest")
	for _, tc := range []struct {
		name string
		tgz  string
		want string
	}{
		{"LATEST", tarArchive(t, "LATEST/run.json", run("LATEST")), "isn't in a snapshot folder"},
		{"latest", tarArchive(t, "latest/run.json", run("latest")), "isn't in a snapshot folder"},
		{"run.json twice", tarArchive(t, "base/run.json", run("base"), "base/run.json", run("base")), "it holds run.json twice"},
	} {
		code, _, errOut := runCLI(t, "snapshot", "--import", tc.tgz, "--force")
		if code != exitUsage || !strings.Contains(errOut, "not a snapshot archive: ") || !strings.Contains(errOut, tc.want) {
			t.Errorf("%s: %d %q; want 2 and %q", tc.name, code, errOut, tc.want)
		}
		if fi, err := os.Lstat(latest); err != nil || !fi.Mode().IsRegular() {
			t.Fatalf("%s: the latest pointer is no longer a file: %v", tc.name, err)
		}
	}
	tgz := tarArchive(t, "base/run.json", run("base"))
	for _, args := range [][]string{
		{"snapshot", "--import", tgz, "--name", "LATEST", "--force"},
		{"snapshot", "--name", "Latest"},
	} {
		if code, _, errOut := runCLI(t, args...); code != exitUsage || !strings.Contains(errOut, "LATEST is taken") {
			t.Errorf("%v: %d %q", args, code, errOut)
		}
	}
	if b, err := os.ReadFile(latest); err != nil || strings.TrimSpace(string(b)) != "base" {
		t.Errorf("latest pointer = %q %v, want base", b, err)
	}
}

// decodeEnvelope checks out is one JSON envelope of kind, with every
// field of one and decodes its data into data.
func decodeEnvelope(t *testing.T, out []byte, kind jsonKind, data any) envelope {
	t.Helper()
	var keys map[string]json.RawMessage
	if err := json.Unmarshal(out, &keys); err != nil {
		t.Fatalf("not JSON: %v\n%s", err, out)
	}
	for _, k := range []string{"schema", "kind", "tool", "version", "complete", "summary", "verdict", "data"} {
		if _, ok := keys[k]; !ok {
			t.Errorf("the envelope has no %q:\n%s", k, out)
		}
	}
	var env envelope
	if err := json.Unmarshal(out, &env); err != nil {
		t.Fatal(err)
	}
	if env.Schema != model.Schema || env.Kind != kind || env.Tool != "rhythm-echo" || env.Version == "" {
		t.Errorf("envelope %s/%s/%s/%s; want %s, %s, rhythm-echo and a version", env.Schema, env.Kind, env.Tool, env.Version, model.Schema, kind)
	}
	switch env.Verdict {
	case verdictOK, verdictFindings, verdictError:
	default:
		t.Errorf("verdict %q", env.Verdict)
	}
	if data != nil {
		if err := json.Unmarshal(keys["data"], data); err != nil {
			t.Errorf("data: %v\n%s", err, keys["data"])
		}
	}
	return env
}

func TestEveryCommandWritesTheEnvelope(t *testing.T) {
	if _, err := exec.LookPath("curl"); err != nil {
		t.Skip("no curl")
	}
	inbox := catcher.NewInbox(1000)
	echo := fixtureServer(t, 0, catcher.NewServer(inbox, catcher.Config{Version: "test"}))
	root := t.TempDir()
	t.Setenv("HOME", root)
	t.Chdir(root)
	writeConfig(t, "smtp: \"off\"\n"+captureConfig(echo.URL, "Order received"))
	cli := func(kind jsonKind, data any, want int, args ...string) envelope {
		t.Helper()
		code, out, errOut := runCLI(t, args...)
		if code != want {
			t.Errorf("%v: exit %d, want %d\n%s", args, code, want, errOut)
		}
		return decodeEnvelope(t, []byte(out), kind, data)
	}

	var run model.Run
	env := cli(kindRun, &run, exitOK, "snapshot", "--url", echo.URL, "--name", "base", "--json")
	if env.Summary.Actions != 1 || env.Summary.Messages != 1 || env.Verdict != verdictOK || !env.Complete || run.Name != "base" {
		t.Errorf("snapshot --json: %+v %s, run %q", env.Summary, env.Verdict, run.Name)
	}
	if env := cli(kindRun, &run, exitOK, "run", "--url", echo.URL, "--json"); env.Summary.Messages != 1 {
		t.Errorf("run --json: %+v", env.Summary)
	}
	writeConfig(t, "smtp: \"off\"\n"+captureConfig(echo.URL, "Your order"))
	var cmpData struct {
		Changes []model.Change `json:"changes"`
		Actions []model.Action `json:"actions"`
	}
	env = cli(kindComparison, &cmpData, exitOK, "compare", "--url", echo.URL, "--json")
	if env.Summary.Changed == 0 || env.Summary.Actions != 1 || env.Verdict != verdictFindings || len(cmpData.Actions) != 1 || len(cmpData.Changes) == 0 {
		t.Errorf("compare --json: %+v %s, data %+v", env.Summary, env.Verdict, cmpData)
	}
	if env := cli(kindComparison, nil, exitOK, "report", "--json"); env.Summary.Changed == 0 {
		t.Errorf("report --json: %+v", env.Summary)
	}
	// -o writes the envelope too.
	if code, _, errOut := runCLI(t, "report", "-o", "out.json"); code != 0 {
		t.Fatalf("report -o: %d %s", code, errOut)
	}
	if b, err := os.ReadFile("out.json"); err != nil {
		t.Error(err)
	} else {
		decodeEnvelope(t, b, kindComparison, nil)
	}

	var ms []model.Message
	if env := cli(kindMessages, &ms, exitOK, "ls", "--url", echo.URL, "--json"); env.Summary.Messages != len(ms) || len(ms) < 3 {
		t.Errorf("ls --json: %+v, %d messages", env.Summary, len(ms))
	}
	var one []model.Message
	if cli(kindMessages, &one, exitOK, "show", ms[0].ID, "--url", echo.URL); len(one) != 1 || one[0].ID != ms[0].ID {
		t.Errorf("show: %+v", one)
	}

	var snaps []snapshotEntry
	cli(kindSnapshots, &snaps, exitOK, "snapshots", "--json")
	if len(snaps) != 1 || snaps[0].Name != "base" || !snaps[0].Latest || snaps[0].Actions != 1 || snaps[0].Messages != 1 || snaps[0].Started.IsZero() {
		t.Errorf("snapshots --json: %+v", snaps)
	}

	var checks []doctorCheck
	env = cli(kindDoctor, &checks, exitOK, "doctor", "--url", echo.URL, "--json")
	if len(checks) == 0 || checks[0].Name != "Echo" || checks[0].Status != "ok" || !env.Complete {
		t.Errorf("doctor --json: %+v", checks)
	}
	env = cli(kindDoctor, &checks, exitError, "doctor", "--url", "http://127.0.0.1:20479", "--json")
	if env.Summary.Errors != 1 || env.Verdict != verdictError || env.Complete || checks[0].Status != "error" {
		t.Errorf("doctor --json with no Echo: %+v %s %+v", env.Summary, env.Verdict, checks)
	}

	// An empty list is [], not null.
	t.Chdir(t.TempDir())
	if code, out, _ := runCLI(t, "snapshots", "--json"); code != 0 || !strings.Contains(out, `"data": []`) {
		t.Errorf("snapshots --json with none: %d\n%s", code, out)
	}
}

// TestQuietAndTimeoutFlags: --quiet <d> and --timeout <d> reach the
// runner and are checked like the config's quiet: and timeout:, while a
// bare -q still means results only.
func TestQuietAndTimeoutFlags(t *testing.T) {
	t.Chdir(t.TempDir())
	srv := httptest.NewServer(catcher.NewServer(catcher.NewInbox(100), catcher.Config{Version: "test"}))
	defer srv.Close()
	shop := shopSite(t, srv.URL, "Hi Ann, welcome.")
	action := "actions:\n  - name: Sign up\n    http: {method: POST, url: /signup, form: {email: ann@shop.test}}\n"
	writeConfig(t, "site: "+shop.URL+"\nquiet: 30s\ntimeout: 1m\n"+action+"    expect: {email: 1}\n")

	// The config's 30s quiet would hold the action for 30s; half of that
	// leaves a slow CI under -race plenty of room.
	start := time.Now()
	code, out, errOut := runCLI(t, "run", "--url", srv.URL, "--plain", "--quiet", "200ms")
	if took := time.Since(start); code != exitOK || took > 15*time.Second || !strings.Contains(out, "1 email") {
		t.Errorf("run --quiet 200ms: exit %d in %s\n%s%s", code, took, out, errOut)
	}
	// --quiet=<d> is the same; it doesn't hide the output as -q does.
	if code, out, _ := runCLI(t, "run", "--url", srv.URL, "--plain", "--quiet=200ms"); code != exitOK || !strings.Contains(out, "Echo") {
		t.Errorf("run --quiet=200ms: %d\n%s", code, out)
	}

	// Two emails are expected and one comes: only the timeout ends the
	// wait for the second; the runner then reports the expectation unmet.
	// The config's timeout is a minute.
	writeConfig(t, "site: "+shop.URL+"\nquiet: 200ms\ntimeout: 1m\n"+action+"    expect: {email: 2}\n")
	start = time.Now()
	code, out, _ = runCLI(t, "run", "--url", srv.URL, "--plain", "--timeout", "600ms")
	if took := time.Since(start); code != exitFindings || took > 30*time.Second || !strings.Contains(out, "email: expected 2, got 1") {
		t.Errorf("run --timeout 600ms: exit %d in %s\n%s", code, took, out)
	}
	writeConfig(t, "site: "+shop.URL+"\nquiet: 200ms\ntimeout: 10s\n"+action+"    expect: {email: 1}\n")

	// A bare -q before a flag or at the end is still results only.
	for _, args := range [][]string{{"-q", "--plain"}, {"--plain", "-q"}, {"--quiet", "--quiet", "300ms"}} {
		if code, _, errOut := runCLI(t, append([]string{"run", "--url", srv.URL, "--fail-on", "none"}, args...)...); code != exitOK {
			t.Errorf("run %v: %d %s", args, code, errOut)
		}
	}

	for _, tc := range []struct {
		args []string
		want string
	}{
		{[]string{"run", "--timeout", "0s"}, "--timeout 0s: must be more than 0, like 30s"},
		{[]string{"compare", "--timeout", "-5s"}, "--timeout -5s: must be more than 0"},
		{[]string{"snapshot", "--timeout", "2m"}, "--timeout 2m: over the 1m Echo waits at most"},
		{[]string{"run", "--quiet", "0s"}, "--quiet 0s: must be more than 0, like 2s"},
		{[]string{"run", "--quiet", "0"}, "--quiet 0s: must be more than 0"},
		{[]string{"run", "--quiet", "45s"}, "--quiet 45s: over the 30s Echo waits for quiet at most"},
		{[]string{"run", "--quiet", "5s", "--timeout", "1s"}, "--quiet 5s is longer than the timeout of 1s (--timeout), so no action could ever fall quiet"},
		{[]string{"run", "--quiet", "20s"}, "--quiet 20s is longer than the timeout of 10s (timeout: in " + filepath.Join(".echo", "config.yaml") + ")"},
		{[]string{"run", "--timeout", "100ms"}, "--timeout 100ms is shorter than the quiet of 200ms (quiet: in " + filepath.Join(".echo", "config.yaml") + "), so no action could ever fall quiet; lower it with --quiet"},
		{[]string{"run", "--quiet", "5"}, `--quiet "5": want a duration like 500ms, or no value for results only`},
		{[]string{"run", "--timeout", "soon"}, `--timeout "soon": want a duration like 10m or 5s`},
		{[]string{"ls", "--quiet=2s"}, "--quiet 2s: only snapshot, compare and run take a duration; -q alone means results only"},
		{[]string{"ls", "--timeout", "2s"}, "--timeout has no effect on ls"},
		{[]string{"snapshot", "--export", "x.tgz", "--timeout", "2s"}, "--export and --import run no actions"},
	} {
		code, out, errOut := runCLI(t, append(tc.args, "--url", srv.URL)...)
		if code != exitUsage || !strings.Contains(errOut, tc.want) || out != "" {
			t.Errorf("%v: exit %d, stderr %q; want 2 and %q", tc.args, code, errOut, tc.want)
		}
	}

	// ls's -q followed by an argument isn't joined: only commands that
	// run actions take a duration.
	if got := joinQuietValue([]string{"-q", "5s", "--", "--quiet", "1s"}); strings.Join(got, " ") != "-q=5s -- --quiet 1s" {
		t.Errorf("joinQuietValue: %q", got)
	}
	for _, c := range []string{"snapshot", "compare", "run"} {
		_, out, _ := runCLI(t, c, "--help")
		if !strings.Contains(out, "--timeout <d>") || !strings.Contains(out, "With a duration (--quiet 500ms)") {
			t.Errorf("%s --help doesn't explain --quiet <d> and --timeout:\n%s", c, out)
		}
	}
	if _, out, _ := runCLI(t, "ls", "--help"); strings.Contains(out, "--timeout") || strings.Contains(out, "With a duration") {
		t.Errorf("ls --help lists the quiet window:\n%s", out)
	}
}

// fakeLatest is an Echo with GET /v1/messages/latest as the catcher
// answers it: Ann's login email with a code and a magic link, Bob's SMS
// with neither and for anyone else a 404 once wait has passed. Each
// query it got is kept, to check what the flags sent.
func fakeLatest(t *testing.T, token string, queries chan<- string) http.Handler {
	t.Helper()
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/messages/latest" {
			http.NotFound(w, r)
			return
		}
		if r.Header.Get(wire.TokenHeader) != token {
			http.Error(w, "this Echo needs its token", http.StatusUnauthorized)
			return
		}
		q := r.URL.Query()
		select {
		case queries <- r.URL.RawQuery:
		default:
		}
		var ans map[string]any
		switch to := q.Get("to"); to {
		case "ann@shop.test":
			ans = map[string]any{"codes": []string{"482913"},
				"links":   []map[string]string{{"kind": "reset", "url": "https://shop.test/reset?key=r1"}, {"kind": "magic", "url": "https://shop.test/login?token=m1"}},
				"message": map[string]any{"id": "m-ann", "channel": "email", "subject": "Your code", "to": []string{to}}}
		case "+15005550006":
			ans = map[string]any{"codes": []string{}, "links": []any{},
				"message": map[string]any{"id": "m-bob", "channel": "sms", "to": []string{to}}}
		default:
			wait, _ := time.ParseDuration(q.Get("wait"))
			time.Sleep(wait)
			msg := "no message matched"
			if wait > 0 {
				msg += " within " + wait.String()
			}
			http.Error(w, msg, http.StatusNotFound)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(ans)
	})
}

func TestLatestPrintsTheCodeOrLink(t *testing.T) {
	t.Chdir(t.TempDir())
	queries := make(chan string, 1)
	echo := fixtureServer(t, 0, fakeLatest(t, "s3cret-token", queries))
	t.Setenv("RHYTHM_ECHO_URL", echo.URL)
	t.Setenv("RHYTHM_ECHO_TOKEN", "s3cret-token")
	lastQuery := func() string {
		select {
		case q := <-queries:
			return q
		default:
			return ""
		}
	}

	for _, tc := range []struct {
		args []string
		want string
	}{
		{[]string{"--code"}, "482913\n"},
		{nil, "482913\n"},
		{[]string{"--link"}, "https://shop.test/reset?key=r1\n"},
		{[]string{"--link", "--kind", "magic"}, "https://shop.test/login?token=m1\n"},
		{[]string{"--kind", "magic"}, "https://shop.test/login?token=m1\n"},
	} {
		code, out, errOut := runCLI(t, append([]string{"latest", "--to", "ann@shop.test"}, tc.args...)...)
		if code != exitOK || out != tc.want || errOut != "" {
			t.Errorf("latest %v: exit %d, %q %q; want %q", tc.args, code, out, errOut, tc.want)
		}
		_ = lastQuery()
	}

	// The flags reach the query: channels as one list, the action by its
	// ID, the run, how far back and the default 10s wait.
	code, _, errOut := runCLI(t, "latest", "--to", "ann@shop.test", "--channel", "email,sms", "--action", "Log in", "--run", "r1", "--since", "5m")
	q := lastQuery()
	for _, want := range []string{"channel=email%2Csms", "action=action%3Alog-in", "run=r1", "since=", "wait=10s", "to=ann%40shop.test"} {
		if code != exitOK || !strings.Contains(q, want) {
			t.Errorf("latest sent %q (exit %d %s); want %s in it", q, code, errOut, want)
		}
	}

	// Without --since it looks back a minute; with --run, at any age.
	if code, _, _ := runCLI(t, "latest", "--to", "ann@shop.test"); code != exitOK || !strings.Contains(lastQuery(), "since=") {
		t.Error("latest didn't send a default since")
	}
	if code, _, _ := runCLI(t, "latest", "--to", "ann@shop.test", "--run", "r1"); code != exitOK || strings.Contains(lastQuery(), "since=") {
		t.Error("latest --run sent a since")
	}

	code, out, _ := runCLI(t, "latest", "--to", "ann@shop.test", "--json")
	var data latestResult
	if env := decodeEnvelope(t, []byte(out), kindLatest, &data); code != exitOK || data.Codes[0] != "482913" || len(data.Links) != 2 ||
		data.Message.ID != "m-ann" || env.Summary.Messages != 1 || env.Verdict != verdictOK {
		t.Errorf("latest --json: %d\n%s", code, out)
	}
	_ = lastQuery()

	// Nothing arrives in time: exit 1, after about --wait.
	start := time.Now()
	code, out, errOut = runCLI(t, "latest", "--to", "nobody@shop.test", "--wait", "300ms")
	if took := time.Since(start); code != exitFindings || out != "" || took < 300*time.Millisecond || took > 5*time.Second ||
		errOut != "rhythm echo: no message to nobody@shop.test arrived within 300ms (nor in the 1m before; --since looks further back)\n" {
		t.Errorf("latest timeout: exit %d in %s, %q %q", code, took, out, errOut)
	}
	if !strings.Contains(lastQuery(), "wait=300ms") {
		t.Error("latest didn't send --wait")
	}
	// --wait 0 asks once, without waiting.
	if code, _, _ := runCLI(t, "latest", "--to", "nobody@shop.test", "--wait", "0"); code != exitFindings || strings.Contains(lastQuery(), "wait=") {
		t.Errorf("latest --wait 0: exit %d", code)
	}

	// A message without what was asked for: exit 1, naming the message.
	for _, args := range [][]string{{"--code"}, {}, {"--link"}} {
		code, out, errOut := runCLI(t, append([]string{"latest", "--to", "+15005550006"}, args...)...)
		if code != exitFindings || out != "" || !strings.Contains(errOut, "the newest message to +15005550006 has no") || !strings.Contains(errOut, "rhythm echo show m-bob") {
			t.Errorf("latest %v with no code: exit %d %q %q", args, code, out, errOut)
		}
	}
	if _, _, errOut := runCLI(t, "latest", "--to", "ann@shop.test", "--link", "--kind", "verify"); !strings.Contains(errOut, "has no verify link") {
		t.Errorf("latest --kind verify: %q", errOut)
	}

	for _, tc := range []struct {
		args []string
		want string
	}{
		{[]string{"latest"}, "latest needs --to <address or phone>"},
		{[]string{"latest", "ann@shop.test"}, "latest takes no arguments; name the recipient with --to"},
		{[]string{"latest", "--to", "a", "--code", "--link"}, "pick --code or --link, not both"},
		{[]string{"latest", "--to", "a", "--kind", "secret"}, `--kind "secret": use one of magic, verify, reset, login, unsubscribe`},
		{[]string{"latest", "--to", "a", "--channel", "email,fax"}, `--channel "fax": use one or more of email, sms`},
		{[]string{"latest", "--to", "a", "--wait", "2m"}, "--wait 2m: give 0 (don't wait) up to 1m"},
		{[]string{"latest", "--to", "a", "--action", "x", "--action", "y"}, "latest takes one --action"},
		{[]string{"latest", "--to", "a", "--fail-on", "none"}, "--fail-on has no effect on latest"},
		{[]string{"ls", "--code"}, "--code has no effect on ls"},
		{[]string{"clear", "m-1"}, "clear takes no arguments; narrow it with --run, --to or --channel"},
		{[]string{"ls", "m-1"}, "ls takes no arguments; narrow it with --run, --to, --channel or --since"},
		{[]string{"watch", "x"}, "watch takes no arguments; narrow it with --run, --to or --channel"},
		{[]string{"doctor", "x"}, "doctor takes no arguments; --config names another config"},
		{[]string{"ls", "--channel", "email,sms"}, `--channel "email,sms": use one of`},
	} {
		code, out, errOut := runCLI(t, tc.args...)
		if code != exitUsage || out != "" || !strings.Contains(errOut, tc.want) {
			t.Errorf("%v: exit %d, %q; want 2 and %q", tc.args, code, errOut, tc.want)
		}
	}

	// A wrong token, an Echo from before codes and no Echo at all.
	t.Setenv("RHYTHM_ECHO_TOKEN", "wrong")
	if code, _, errOut := runCLI(t, "latest", "--to", "ann@shop.test"); code != exitError || !strings.Contains(errOut, "401") {
		t.Errorf("latest with a wrong token: %d %q", code, errOut)
	}
	t.Setenv("RHYTHM_ECHO_TOKEN", "")
	// Echo 1.0.0 had no latest route: GET /v1/messages/{id} took "latest"
	// for an ID and answered http.NotFound.
	current := catcher.NewServer(catcher.NewInbox(10), catcher.Config{Version: "test"})
	old := fixtureServer(t, 0, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/v1/messages/latest" {
			http.NotFound(w, r)
			return
		}
		current.ServeHTTP(w, r)
	}))
	if code, _, errOut := runCLI(t, "latest", "--to", "ann@shop.test", "--url", old.URL); code != exitError ||
		!strings.Contains(errOut, "this Echo doesn't read codes and links; update it") {
		t.Errorf("latest on an older Echo: %d %q", code, errOut)
	}
	if code, _, errOut := runCLI(t, "latest", "--to", "ann@shop.test", "--url", "http://127.0.0.1:20472"); code != exitError ||
		!strings.Contains(errOut, "no Echo is running") {
		t.Errorf("latest without Echo: %d %q", code, errOut)
	}

	if code, out, _ := runCLI(t, "latest", "--help"); code != exitOK || !strings.Contains(out, "--kind <k>") || !strings.Contains(out, "default 10s") {
		t.Errorf("latest --help: %d\n%s", code, out)
	}
}

// TestLatestAgainstTheRealCatcher: latest reads the code and the link
// from Echo's own /v1/messages/latest, as well as its "nothing came" answer.
func TestLatestAgainstTheRealCatcher(t *testing.T) {
	t.Chdir(t.TempDir())
	echo := fixtureServer(t, 0, catcher.NewServer(catcher.NewInbox(10), catcher.Config{Version: "test"}))
	t.Setenv("RHYTHM_ECHO_URL", echo.URL)
	t.Setenv("RHYTHM_ECHO_TOKEN", "")
	body := `{"v":1,"kind":"email","source":{"via":"wordpress"},"email":{"from":"shop@shop.test","to":["ann@shop.test"],` +
		`"subject":"Your login code","text":"Your verification code is 482913. Or sign in: https://shop.test/login?token=m1abc"}}`
	resp, err := http.Post(echo.URL+"/v1/capture", "application/json", strings.NewReader(body)) //nolint:noctx // a test fixture
	if err != nil {
		t.Fatal(err)
	}
	_ = resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("capture: %s", resp.Status)
	}
	if code, out, errOut := runCLI(t, "latest", "--to", "ann@shop.test", "--code"); code != exitOK || out != "482913\n" {
		t.Errorf("latest --code: exit %d, %q %q", code, out, errOut)
	}
	if code, out, errOut := runCLI(t, "latest", "--to", "ann@shop.test", "--link"); code != exitOK || out != "https://shop.test/login?token=m1abc\n" {
		t.Errorf("latest --link: exit %d, %q %q", code, out, errOut)
	}
	code, out, errOut := runCLI(t, "latest", "--to", "nobody@shop.test", "--wait", "0")
	if code != exitFindings || out != "" || !strings.Contains(errOut, "no message to nobody@shop.test arrived") {
		t.Errorf("latest with nothing: exit %d, %q %q", code, out, errOut)
	}
}

// timedLatest answers /v1/messages/latest as Echo does: the newest
// message to the recipient from since on, at once when there is one,
// else the first to arrive within wait. Each message is a code and when
// it arrived; add puts a new one in.
func timedLatest(t *testing.T) (h http.Handler, add func(code string, at time.Time)) {
	t.Helper()
	type msg struct {
		code string
		at   time.Time
	}
	var (
		mu      sync.Mutex
		msgs    []msg
		arrived = make(chan struct{})
	)
	add = func(code string, at time.Time) {
		mu.Lock()
		defer mu.Unlock()
		msgs = append(msgs, msg{code, at})
		close(arrived)
		arrived = make(chan struct{})
	}
	newest := func(since time.Time) (string, <-chan struct{}) {
		mu.Lock()
		defer mu.Unlock()
		for i := len(msgs) - 1; i >= 0; i-- {
			if !msgs[i].at.Before(since) {
				return msgs[i].code, nil
			}
		}
		return "", arrived
	}
	h = http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		q := r.URL.Query()
		var since time.Time
		if s := q.Get("since"); s != "" {
			since, _ = time.Parse(time.RFC3339Nano, s)
		}
		wait, _ := time.ParseDuration(q.Get("wait"))
		deadline := time.After(wait)
		for {
			code, next := newest(since)
			if code != "" {
				w.Header().Set("Content-Type", "application/json")
				_ = json.NewEncoder(w).Encode(map[string]any{"codes": []string{code}, "links": []any{},
					"message": map[string]any{"id": "m-" + code, "channel": "email", "to": []string{q.Get("to")}}})
				return
			}
			select {
			case <-next:
			case <-deadline:
				http.Error(w, "no message matched", http.StatusNotFound)
				return
			}
		}
	})
	return h, add
}

// An older message to the same person doesn't stand in for the one a
// test is waiting for: latest looks back a minute unless told otherwise.
func TestLatestWaitsForTheNewMessage(t *testing.T) {
	t.Chdir(t.TempDir())
	h, add := timedLatest(t)
	echo := fixtureServer(t, 0, h)
	t.Setenv("RHYTHM_ECHO_URL", echo.URL)
	add("111111", time.Now().Add(-5*time.Minute)) // the previous test run's

	// Asked for, the old one is still there.
	if code, out, errOut := runCLI(t, "latest", "--to", "ann@shop.test", "--since", "10m", "--wait", "0"); code != exitOK || out != "111111\n" {
		t.Errorf("latest --since 10m: %d %q %q", code, out, errOut)
	}
	// By default it isn't: latest waits for the new one.
	go func() {
		time.Sleep(300 * time.Millisecond)
		add("222222", time.Now())
	}()
	if code, out, errOut := runCLI(t, "latest", "--to", "ann@shop.test", "--wait", "20s"); code != exitOK || out != "222222\n" {
		t.Errorf("latest while a new message arrives: %d %q %q", code, out, errOut)
	}
	// Nothing in time: the message says how far back it looked.
	h2, _ := timedLatest(t)
	t.Setenv("RHYTHM_ECHO_URL", fixtureServer(t, 0, h2).URL)
	code, _, errOut := runCLI(t, "latest", "--to", "ann@shop.test", "--wait", "200ms")
	if code != exitFindings || errOut != "rhythm echo: no message to ann@shop.test arrived within 200ms (nor in the 1m before; --since looks further back)\n" {
		t.Errorf("latest with nothing: %d %q", code, errOut)
	}
}

// TestUpdatePromptQuietForMachines checks that a JSON or -q run never asks
// about a new release or prints the notice, while a plain run may.
func TestUpdatePromptQuietForMachines(t *testing.T) {
	t.Chdir(t.TempDir())
	c, _ := lookup("run")
	for _, tc := range []struct {
		args  []string
		quiet bool
	}{
		{nil, false},
		{[]string{"--json"}, true},
		{[]string{"-o", "run.json"}, true},
		{[]string{"-q"}, true},
		{[]string{"--md"}, false},
	} {
		p, err := parse(c, append([]string{"--url", "http://127.0.0.1:1"}, tc.args...))
		if err != nil {
			t.Fatalf("%v: %v", tc.args, err)
		}
		s := p.begin(&bytes.Buffer{}, &bytes.Buffer{})
		got := s.promptOptions()
		s.stop()
		if got.Quiet != tc.quiet {
			t.Errorf("%v: Quiet = %v, want %v", tc.args, got.Quiet, tc.quiet)
		}
		if got.Stdout != s.stdout || got.Out != s.stderr {
			t.Errorf("%v: the prompt must use the run's own writers", tc.args)
		}
	}
}
