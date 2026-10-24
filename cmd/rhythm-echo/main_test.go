package main

import (
	"bytes"
	"encoding/json"
	"flag"
	"fmt"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

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
