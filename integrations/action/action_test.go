// Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
// SPDX-License-Identifier: MIT

package action

import (
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"
)

// Ports for the Action's Echo and the fixture shop it checks, inside the
// range the CI fixture's own test leaves free.
const (
	listen   = "127.0.0.1:20513"
	smtp     = "127.0.0.1:20514"
	shopPort = "20515"
	deadEcho = "http://127.0.0.1:20519"
)

// shop is a monorepo layout: the CI fixture in sites/shop, served by
// php -S, with Echo built from this checkout on PATH in place of the
// release the Action downloads.
func shop(t *testing.T) *runner {
	t.Helper()
	needBash(t, "jq", "curl")
	php, err := exec.LookPath("php")
	if err != nil {
		t.Skip("php isn't installed")
	}
	if testing.Short() {
		t.Skip("starts PHP and Echo")
	}
	r := newRunner(t)
	r.skip = []string{"Install Echo"}
	site := filepath.Join(r.workspace, "sites", "shop")
	if err := os.CopyFS(site, os.DirFS(filepath.Join(root(t), "integrations", "ci-fixture"))); err != nil {
		t.Fatal(err)
	}
	cfg := filepath.Join(site, ".echo", "config.yaml")
	b, err := os.ReadFile(cfg) //nolint:gosec // the copy this test made
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(cfg, []byte(strings.ReplaceAll(string(b), ":20510", ":"+shopPort)), 0o600); err != nil {
		t.Fatal(err)
	}

	bin := t.TempDir()
	build := exec.Command("go", "build", "-o", filepath.Join(bin, "rhythm-echo"), "./cmd/rhythm-echo")
	build.Dir = root(t)
	build.Env = append(os.Environ(), "CGO_ENABLED=0")
	if out, err := build.CombinedOutput(); err != nil {
		t.Fatalf("build: %v\n%s", err, out)
	}
	r.path = []string{bin}

	srv := exec.Command(php, "-S", "127.0.0.1:"+shopPort, "index.php") //nolint:gosec // php from PATH
	srv.Dir = filepath.Join(site, "php")
	srv.Env = append(os.Environ(), "ECHO_SMTP="+smtp)
	if err := srv.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_ = srv.Process.Kill()
		_ = srv.Wait()
		stopEcho(r)
	})
	for i := 0; ; i++ {
		c, err := net.DialTimeout("tcp", "127.0.0.1:"+shopPort, time.Second)
		if err == nil {
			_ = c.Close()
			break
		}
		if i == 100 {
			t.Fatal("php -S didn't start")
		}
		time.Sleep(50 * time.Millisecond)
	}
	return r
}

// stopEcho kills an Echo the Action left running.
func stopEcho(r *runner) {
	b, err := os.ReadFile(filepath.Join(r.temp, "echo.pid"))
	if err != nil {
		return
	}
	if pid, err := strconv.Atoi(strings.TrimSpace(string(b))); err == nil {
		if p, err := os.FindProcess(pid); err == nil {
			_ = p.Signal(syscall.SIGTERM)
		}
	}
	// Wait until the API port is free for the next test.
	for range 50 {
		c, err := net.DialTimeout("tcp", listen, 100*time.Millisecond)
		if err != nil {
			return
		}
		_ = c.Close()
		time.Sleep(100 * time.Millisecond)
	}
}

// in returns the Action's inputs for the shop in its subfolder.
func in(kv ...string) map[string]string {
	m := map[string]string{"working-directory": "sites/shop", "listen": listen, "smtp": smtp}
	for i := 0; i+1 < len(kv); i += 2 {
		m[kv[i]] = kv[i+1]
	}
	return m
}

func (r *runner) broken(t *testing.T) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(r.workspace, "sites", "shop", "php", "broken"), nil, 0o600); err != nil {
		t.Fatal(err)
	}
}

func contains(t *testing.T, what, s string, want ...string) {
	t.Helper()
	for _, w := range want {
		if !strings.Contains(s, w) {
			t.Errorf("%s lacks %q:\n%s", what, w, s)
		}
	}
}

func lacks(t *testing.T, what, s string, private ...string) {
	t.Helper()
	for _, p := range private {
		if strings.Contains(s, p) {
			t.Errorf("%s shows %q:\n%s", what, p, s)
		}
	}
}

// TestActionMonorepo snapshots and compares a site in a subfolder, with
// fail-on in capitals: the default report upload stays off, the summary is
// masked and the masked log is uploaded because compare found a break.
func TestActionMonorepo(t *testing.T) {
	r := shop(t)
	res := r.use(in("mode", "snapshot", "name", "good"))
	if !res.ok || res.outputs["exit-code"] != "0" {
		t.Fatalf("snapshot failed: %+v\n%s", res.outputs, res.log)
	}
	if _, err := os.Stat(filepath.Join(r.workspace, "sites", "shop", ".echo", "snapshots", "good")); err != nil {
		t.Errorf("the snapshot isn't in the working directory: %v", err)
	}
	contains(t, "snapshot summary", res.summary, `**Snapshot "good" saved; all clear.** 1 action run; 1 email sent.`)
	if slices.Contains(res.ran, "Upload reports") || slices.Contains(res.ran, "Upload the log") {
		t.Errorf("a clean snapshot uploaded something: %q", res.ran)
	}

	same := r.use(in("mode", "compare"))
	if !same.ok || same.outputs["exit-code"] != "0" {
		t.Fatalf("a good compare failed: %+v\n%s", same.outputs, same.log)
	}

	r.broken(t)
	res = r.use(in("mode", "compare", "fail-on", "BROKE"))
	if res.ok || res.outputs["exit-code"] != "1" {
		t.Fatalf("a broken compare passed: %+v\n%s", res.outputs, res.log)
	}
	contains(t, "compare summary", res.summary, "**Something broke.**", "| 1 | 1 | 0 | 0 |", "### ✗ BROKE (1)", "### ▲ NEW (1)")
	lacks(t, "compare summary", res.summary, "customer@example.com", "Order W-1001 received", "New order W-1001")
	if got, _ := os.ReadFile(res.outputs["summary"]); string(got) != res.summary {
		t.Errorf("the summary output differs from the job summary")
	}
	if _, err := os.Stat(res.outputs["report"]); err != nil {
		t.Errorf("no HTML report: %v", err)
	}
	if slices.Contains(res.ran, "Upload reports") {
		t.Error("the report was uploaded by default")
	}
	if !slices.Contains(res.ran, "Upload the log") || !slices.Contains(res.ran, "Fail on changes") {
		t.Errorf("steps = %q, want the log upload and the failure", res.ran)
	}
	log, err := os.ReadFile(filepath.Join(r.temp, "echo-log", "echo.log"))
	if err != nil {
		t.Fatal(err)
	}
	contains(t, "uploaded log", string(log), "email to ***@shop.example: *** [faked]")
	lacks(t, "uploaded log", string(log), "owner@shop.example", "New order W-1001")

	details := r.use(in("mode", "compare", "fail-on", "none", "summary-details", "true", "upload", "true", "config", ".echo/config.yaml"))
	if !details.ok || details.outputs["exit-code"] != "0" {
		t.Fatalf("fail-on none failed: %+v\n%s", details.outputs, details.log)
	}
	contains(t, "detailed summary", details.summary, "New order W-1001")
	if !slices.Contains(details.ran, "Upload reports") {
		t.Errorf("upload: true didn't upload: %q", details.ran)
	}
	if strings.Contains(details.log, "upload is on in a public repository") {
		t.Error("warned about a private repository")
	}
	r.github["event.repository.private"] = "false"
	public := r.use(in("mode", "run", "upload", "true"))
	contains(t, "public run log", public.log, "::warning::upload is on in a public repository")
}

// TestActionCouldntRun checks every way the command can stop early still
// writes a job summary that says why.
func TestActionCouldntRun(t *testing.T) {
	r := shop(t)
	cases := []struct {
		name   string
		inputs map[string]string
		code   string
		want   []string
	}{
		{"dead Echo", in("mode", "run", "args", "--url "+deadEcho), "3",
			[]string{"**Echo couldn't run (exit 3).**", "rhythm echo:", "The last 30 lines of echo.log"}},
		{"bad fail-on", in("mode", "compare", "fail-on", "sometimes"), "2",
			[]string{"(exit 2).**", `rhythm echo: fail-on "sometimes": want none, broke, new or changed when comparing`}},
		{"issue level when comparing", in("mode", "compare", "fail-on", "error"), "2", []string{"want none, broke, new or changed"}},
		{"bad flag", in("mode", "run", "args", "--no-such-flag"), "2", []string{"(exit 2).**", "no-such-flag"}},
		{"open quote in args", in("mode", "run", "args", "--action 'Contact"), "2", []string{"(exit 2).**", "rhythm echo: args: a quote isn't closed"}},
		{"missing config", in("mode", "run", "config", "missing.yaml"), "2", []string{"Echo stopped as it started (exit 2)", "missing.yaml"}},
		{"bad mode", in("mode", "later"), "2", []string{`mode must be snapshot, compare, run, up or stop, not "later"`}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			res := r.use(c.inputs)
			if res.ok || res.outputs["exit-code"] != c.code {
				t.Fatalf("ok %v exit-code %q, want a failure with %s\n%s", res.ok, res.outputs["exit-code"], c.code, res.log)
			}
			contains(t, "summary", res.summary, append(c.want, "## ♪ Echo")...)
			if res.outputs["summary"] == "" {
				t.Error("no summary output")
			}
			if !slices.Contains(res.ran, "Upload the log") {
				t.Errorf("the log wasn't uploaded: %q", res.ran)
			}
			stopEcho(r)
		})
	}
}

// TestActionUpStop leaves Echo running for later steps, then stops it and
// reports the run those steps made.
func TestActionUpStop(t *testing.T) {
	r := shop(t)
	up := r.use(in("mode", "up"))
	if !up.ok {
		t.Fatalf("up failed:\n%s", up.log)
	}
	if up.outputs["url"] != "http://"+listen || r.env["RHYTHM_ECHO_URL"] != "http://"+listen {
		t.Errorf("url %q, RHYTHM_ECHO_URL %q", up.outputs["url"], r.env["RHYTHM_ECHO_URL"])
	}
	contains(t, "up summary", up.summary, "Echo is running at http://"+listen, "mode: stop")
	report := up.outputs["report"]
	if report == "" {
		t.Fatal("up has no report output")
	}

	// A later step runs the site's checks against the running Echo.
	cmd := exec.Command(filepath.Join(r.path[len(r.path)-1], "rhythm-echo"), "run", "--plain") //nolint:gosec // the binary this test built
	cmd.Dir = filepath.Join(r.workspace, "sites", "shop")
	cmd.Env = append(os.Environ(), "RHYTHM_ECHO_URL="+r.env["RHYTHM_ECHO_URL"])
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("run: %v\n%s", err, out)
	}

	stop := r.use(map[string]string{"mode": "stop", "working-directory": "sites/shop"})
	if !stop.ok || stop.outputs["exit-code"] != "0" {
		t.Fatalf("stop failed: %+v\n%s", stop.outputs, stop.log)
	}
	if slices.Contains(stop.ran, "Install Echo") || slices.Contains(stop.ran, "Start Echo") {
		t.Errorf("stop installed or started Echo: %q", stop.ran)
	}
	contains(t, "stop summary", stop.summary, "**all clear.** 1 action run; 1 email sent.")
	if stop.outputs["report"] != report {
		t.Errorf("stop wrote the report to %q, up said %q", stop.outputs["report"], report)
	}
	if _, err := os.Stat(report); err != nil {
		t.Errorf("no report: %v", err)
	}
	if c, err := net.DialTimeout("tcp", listen, time.Second); err == nil {
		_ = c.Close()
		t.Error("Echo still runs after stop")
	}
}

// TestActionPRComment posts the comment on a pull request through a fake
// gh, then updates the same comment on the next run.
func TestActionPRComment(t *testing.T) {
	r := shop(t)
	gh := fakeGH(t, `[]`, "101")
	r.path = append([]string{gh.bin}, r.path...)
	r.github["event_name"] = "pull_request"
	r.github["event.pull_request.number"] = "7"

	if res := r.use(in("mode", "snapshot", "name", "good")); !res.ok {
		t.Fatalf("snapshot failed:\n%s", res.log)
	}
	if calls := gh.calls(t); len(calls) != 0 {
		t.Errorf("pr-comment is off by default yet gh ran: %q", calls)
	}
	r.broken(t)
	res := r.use(in("mode", "compare", "pr-comment", "true"))
	if res.outputs["comment-id"] != "101" {
		t.Fatalf("comment-id %q\n%s", res.outputs["comment-id"], res.log)
	}
	calls := gh.calls(t)
	// The workflow token can't ask gh api user, so the comment is
	// github-actions[bot]'s.
	if len(calls) != 3 || calls[0] != "api user --jq .login" ||
		!strings.HasPrefix(calls[1], "api --paginate repos/RhythmShahriar/echo/issues/7/comments") ||
		!strings.HasPrefix(calls[2], "api -X POST repos/RhythmShahriar/echo/issues/7/comments -f body=<!-- rhythm-echo comment: sites/shop -->") {
		t.Fatalf("gh calls: %q", calls)
	}
	contains(t, "comment", calls[2], "**Something broke.**", "**✗ BROKE (1)**", "**▲ NEW (1)**", "[this run](https://github.com/RhythmShahriar/echo/actions/runs/42)")
	lacks(t, "comment", calls[2], "customer@example.com", "New order W-1001")

	gh.reply(t, `[{"id": 5, "user": {"login": "ann"}, "body": "someone else"}, {"id": 101, "user": {"login": "github-actions[bot]"}, "body": "<!-- rhythm-echo comment: sites/shop -->\nold"}]`, "")
	r.use(in("mode", "compare", "pr-comment", "true"))
	calls = gh.calls(t)
	if len(calls) != 3 || !strings.HasPrefix(calls[2], "api -X PATCH repos/RhythmShahriar/echo/issues/comments/101 -f body=") {
		t.Fatalf("second run's gh calls: %q", calls)
	}
}
