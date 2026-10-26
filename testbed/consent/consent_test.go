// Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
// SPDX-License-Identifier: MIT

//go:build browser

package consenttestbed

import (
	"crypto/rand"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io/fs"
	"net"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	kitcli "github.com/RhythmShahriar/rhythm/kit/cli"
	"gopkg.in/yaml.v3"

	"github.com/RhythmShahriar/echo/consent"
	"github.com/RhythmShahriar/echo/consent/consenttest"
	"github.com/RhythmShahriar/echo/internal/browser"
	"github.com/RhythmShahriar/echo/testbed/consent/judge"
	"github.com/RhythmShahriar/echo/testbed/consent/scenarios"
	"github.com/RhythmShahriar/echo/testbed/consent/server"
)

// The browser integration tests: the rhythm-echo built from this
// checkout, run as a user runs it, against the Go-mode testbed on free
// loopback ports. Linkage is read from that binary (this test process
// links no consent stage); waits scale with internal/testenv.

var (
	buildOnce sync.Once
	echoBin   string
	buildErr  error
	buildDir  string
)

// echoBinary builds rhythm-echo from ./cmd/rhythm-echo once.
func echoBinary(t *testing.T) string {
	t.Helper()
	buildOnce.Do(func() {
		buildDir, buildErr = os.MkdirTemp("", "consent-testbed-echo-")
		if buildErr != nil {
			return
		}
		cleanups = append(cleanups, func() { _ = os.RemoveAll(buildDir) })
		name := "rhythm-echo"
		if os.PathSeparator == '\\' {
			name += ".exe"
		}
		echoBin = filepath.Join(buildDir, name)
		cmd := exec.Command("go", "build", "-o", echoBin, "./cmd/rhythm-echo") //nolint:noctx // a build bounded by the test timeout
		cmd.Dir = filepath.Join("..", "..")
		cmd.Env = append(os.Environ(), "GOWORK=off")
		if out, err := cmd.CombinedOutput(); err != nil {
			buildErr = fmt.Errorf("go build: %w\n%s", err, out)
		}
	})
	if buildErr != nil {
		t.Fatal(buildErr)
	}
	return echoBin
}

// needLinked skips (or fails, with ECHO_CONSENT_REQUIRE_LINKED=1) until
// the built binary has the tracker catalog and the CMP rules, then
// skips when there is no Chrome. It returns the binary.
func needLinked(t *testing.T) string {
	t.Helper()
	bin := echoBinary(t)
	probeLinked(t, bin)
	if _, _, err := browser.Find(browser.Options{NoDownload: true}); err != nil {
		t.Skip("Chrome not found")
	}
	return bin
}

// liveBed is the Go-mode testbed on a free port over a private copy of
// the sites, so state switches never touch the checkout's state/.
type liveBed struct {
	srv  *server.Server
	addr string
	m    *scenarios.Manifest
}

func startBed(t *testing.T) *liveBed {
	t.Helper()
	root := filepath.Join(t.TempDir(), "consent")
	if err := os.CopyFS(filepath.Join(root, "sites"), os.DirFS("sites")); err != nil {
		t.Fatal(err)
	}
	srv, err := server.New(server.Options{Root: root, Admin: "127.0.0.1:9"})
	if err != nil {
		t.Fatal(err)
	}
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	go func() { _ = srv.Serve(l) }()
	t.Cleanup(func() { _ = l.Close() })
	m, err := scenarios.Load(".")
	if err != nil {
		t.Fatal(err)
	}
	return &liveBed{srv: srv, addr: l.Addr().String(), m: m}
}

// check runs rhythm-echo consent on scenario s in one state and returns
// what the judge needs.
func (b *liveBed) check(t *testing.T, bin string, s scenarios.Scenario, broken bool, extra ...string) judge.Observed {
	t.Helper()
	if err := b.srv.SetBroken(s.ID, broken); err != nil {
		t.Fatal(err)
	}
	b.srv.ResetScenario(s.ID)
	want, err := judge.PageStandIns(b.srv.Handler(), s.ID)
	if err != nil {
		t.Fatal(err)
	}
	args := slices.Clone(s.Args)
	if i := slices.Index(args, "--against"); i >= 0 && !broken {
		args = append(args[:i], args[i+2:]...)
	}
	if len(s.Runs) > 0 {
		args = append(args, "--runs", strings.Join(s.Runs, ","))
	}
	dir := t.TempDir()
	full := slices.Concat([]string{"consent", "https://" + s.ID + server.SiteSuffix + "/", "--json", "-o", "result.json", "--fail-on", "none"},
		args, extra, server.MapArgs(b.addr))
	_, stderr, code := runEcho(t, bin, dir, echoEnv(), 4*time.Minute, full...)
	o := judge.Observed{Exit: code, Output: stderr, Sink: b.srv.Count(s.ID)}
	if e, err := judge.ReadEnvelope(filepath.Join(dir, "result.json")); err == nil {
		o.Env = e
	}
	o.Missing = judge.MissingStandIns(want, o.Env)
	return o
}

// TestScenarios runs the plan's four scenarios (c01, c02, c03, g01) and
// g12, c11, c13 and c14: a bad scenario's good state must be clean and
// its broken state caught (every expect, no forbid), a good one clean;
// the tracker sink stays empty in every run.
func TestScenarios(t *testing.T) {
	bin := needLinked(t)
	b := startBed(t)
	for _, id := range []string{"c01", "c02", "c03", "g01", "g12", "c11", "c13", "c14"} {
		t.Run(id, func(t *testing.T) {
			s, ok := b.m.Find(id)
			if !ok {
				t.Fatalf("%s isn't in scenarios.yaml", id)
			}
			good := b.check(t, bin, s, false)
			if v := judge.Good(s, good); v.Outcome != judge.Clean {
				t.Errorf("good state: %s: %s", v.Outcome, v.Detail)
			}
			if s.Good() {
				// A brochure site needs no consent: exit 0, never no-banner.
				if id == "g12" && good.Env != nil {
					noBanner := slices.ContainsFunc(good.Env.Data.Issues, func(is consent.Issue) bool { return is.Check == consent.CheckNoBanner })
					if good.Exit != 0 || noBanner {
						t.Errorf("g12: exit %d, issues %v", good.Exit, good.Env.Titles())
					}
				}
				return
			}
			broken := b.check(t, bin, s, true)
			if v := judge.Bad(s, broken); v.Outcome != judge.Caught {
				t.Errorf("broken state: %s: %s", v.Outcome, v.Detail)
			}
		})
	}
}

// TestHeldModeE08 is probe e08: in held mode no tracker hit reaches the
// sink, even from pages whose tags fire before the choice or after
// Reject.
func TestHeldModeE08(t *testing.T) {
	bin := needLinked(t)
	b := startBed(t)
	for _, id := range []string{"c01", "c02", "c09", "c13", "c14", "c22", "c23"} {
		t.Run(id, func(t *testing.T) {
			s, _ := b.m.Find(id)
			o := b.check(t, bin, s, true)
			if o.Env == nil {
				t.Fatalf("no report (exit %d): %s", o.Exit, o.Output)
			}
			if o.Sink.Hits != 0 {
				t.Errorf("%d hits reached the sink: %v", o.Sink.Hits, o.Sink.By)
			}
		})
	}
}

// fakeSecret is a random test value, assembled at run time (a literal
// that looks like a real secret would trip push protection).
func fakeSecret(prefix string) string {
	b := make([]byte, 12)
	_, _ = rand.Read(b)
	return prefix + "_" + "tb" + hex.EncodeToString(b)
}

// forms are a secret's raw, URL-encoded and double-encoded forms.
func forms(s string) []string {
	once := url.QueryEscape(s)
	return slices.Compact([]string{s, once, url.QueryEscape(once), url.PathEscape(s)})
}

// TestConsentMasksEverywhere runs secret-url and pii-beacons (both
// states) with a token in the page URL that the stand-ins copy into
// every hit, a basic-auth password and a bearer token in headers through
// -H env:, a session cookie through --cookie env: and every output
// (terminal with -v, --json, Markdown, JUnit, HTML, HAR). No secret, in
// any form, may be in stdout, stderr or any file of the result folder.
// A basic-auth password in the URL's userinfo is a usage error (PRD 5:
// URLs with userinfo are refused), so that case checks the refusal
// doesn't print it either.
func TestConsentMasksEverywhere(t *testing.T) {
	bin := needLinked(t)
	b := startBed(t)
	token := fakeSecret("tok")
	password := fakeSecret("pw")
	bearer := fakeSecret("bearer")
	cookie := fakeSecret("sess")
	basic := base64.StdEncoding.EncodeToString([]byte("shop:" + password))
	secrets := map[string]string{"URL token": token, "basic-auth password": password, "basic-auth header": basic,
		"bearer token": bearer, "cookie": cookie}
	for _, id := range []string{"secret-url", "pii-beacons"} {
		for _, c := range []struct {
			broken bool
			json   bool // stdout carries the --json envelope instead of the text report
		}{{false, false}, {true, false}, {true, true}} {
			broken := c.broken
			t.Run(fmt.Sprintf("%s broken=%v json=%v", id, broken, c.json), func(t *testing.T) {
				if err := b.srv.SetBroken(id, broken); err != nil {
					t.Fatal(err)
				}
				dir := t.TempDir()
				target := "https://" + id + server.SiteSuffix + "/?token=" + url.QueryEscape(token) + "&email=buyer%40shop.test"
				args := slices.Concat([]string{"consent", target, "--fail-on", "none", "-v", "--har",
					"-o", "result.json", "-o", "report.md", "-o", "report.xml", "-o", "report.html",
					"-H", "Authorization: env:RHYTHM_ECHO_TB_AUTH", "-H", "X-Api-Token: env:RHYTHM_ECHO_TB_BEARER",
					"--cookie", "env:RHYTHM_ECHO_TB_COOKIE"}, server.MapArgs(b.addr))
				if c.json {
					args = append(args, "--json")
				}
				env := echoEnv("RHYTHM_ECHO_TB_AUTH=Basic "+basic, "RHYTHM_ECHO_TB_BEARER=Bearer "+bearer, "RHYTHM_ECHO_TB_COOKIE=tb_session="+cookie)
				stdout, stderr, _ := runEcho(t, bin, dir, env, 4*time.Minute, args...)
				if _, err := os.Stat(filepath.Join(dir, "result.json")); err != nil {
					t.Fatalf("no report: %v\n%s", err, stderr)
				}
				if c.json && !strings.HasPrefix(strings.TrimSpace(stdout), "{") {
					t.Errorf("--json: stdout is not the JSON envelope:\n%.300s", stdout)
				}
				leaks := scanSecrets(t, dir, map[string]string{"stdout": stdout, "stderr": stderr}, secrets, 4)
				for _, l := range leaks {
					t.Error(l)
				}
			})
		}
	}
	t.Run("password in the URL", func(t *testing.T) {
		dir := t.TempDir()
		target := "https://shop:" + url.QueryEscape(password) + "@secret-url" + server.SiteSuffix + "/"
		args := slices.Concat([]string{"consent", target, "--fail-on", "none", "-v", "-o", "result.json"}, server.MapArgs(b.addr))
		stdout, stderr, code := runEcho(t, bin, dir, echoEnv(), time.Minute, args...)
		if code != kitcli.ExitUsage {
			t.Errorf("a URL with userinfo: exit %d, want %d (usage)\n%s", code, kitcli.ExitUsage, stderr)
		}
		for _, l := range scanSecrets(t, dir, map[string]string{"stdout": stdout, "stderr": stderr}, map[string]string{"basic-auth password": password}, 0) {
			t.Error(l)
		}
	})
}

// scanSecrets lists where any form of any secret appears: in the named
// texts or in a file under dir. It names the place and the secret,
// never its value.
func scanSecrets(t *testing.T, dir string, texts map[string]string, secrets map[string]string, minFiles int) []string {
	t.Helper()
	var found []string
	scan := func(where, text string) {
		for name, s := range secrets {
			for _, f := range forms(s) {
				if strings.Contains(text, f) {
					found = append(found, fmt.Sprintf("the %s is in %s", name, where))
					break
				}
			}
		}
	}
	for where, text := range texts {
		scan(where, text)
	}
	files := 0
	err := filepath.WalkDir(dir, func(path string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		data, err := os.ReadFile(path) //nolint:gosec // the test's own result folder
		if err != nil {
			return err
		}
		files++
		rel, _ := filepath.Rel(dir, path)
		scan(filepath.ToSlash(rel), string(data))
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if files < minFiles {
		t.Errorf("only %d files in the result folder; the outputs weren't written", files)
	}
	slices.Sort(found)
	return found
}

// cmpExpect is consent/cmp/testdata/<cmp>/expect.yaml as far as a real
// browser can check it (the format is in consent/cmp/testdata/README.md).
type cmpExpect struct {
	CMP   string `yaml:"cmp"`
	Pages []struct {
		Page          string `yaml:"page"`
		Found         bool   `yaml:"found"`
		Present       bool   `yaml:"present"`
		BannerAfterMS int    `yaml:"banner_after_ms"`
		RejectLayer   int    `yaml:"reject_layer"`
	} `yaml:"pages"`
}

// TestCMPReplicaPages serves consent/cmp/testdata/<cmp>/ from the
// checkout and runs consent detect --json on every page with an
// expect.yaml row: the banner found or not, the CMP present or not and
// the layer Reject is on.
func TestCMPReplicaPages(t *testing.T) {
	root := filepath.Join("..", "..", "consent", "cmp", "testdata")
	files, _ := filepath.Glob(filepath.Join(root, "*", "expect.yaml"))
	if len(files) == 0 {
		if os.Getenv(consenttest.RequireLinkedEnv) == "1" {
			t.Fatalf("no consent/cmp/testdata/*/expect.yaml in this build (%s=1)", consenttest.RequireLinkedEnv)
		}
		t.Skip("cmp not linked yet: consent/cmp/testdata has no expect.yaml")
	}
	bin := needLinked(t)
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	// The replicas are another lane's fixtures: --map-only keeps Chrome
	// off the internet even if one ever names a real CDN.
	mapArgs := []string{"--map-host", cmpReplicaHost + "=" + l.Addr().String(), "--map-only"}
	srv := &http.Server{Handler: http.FileServer(http.Dir(root)), ReadHeaderTimeout: 10 * time.Second}
	go func() { _ = srv.Serve(l) }()
	t.Cleanup(func() { _ = srv.Close() })
	for _, f := range files {
		data, err := os.ReadFile(f) //nolint:gosec // the checkout's own fixture
		if err != nil {
			t.Fatal(err)
		}
		var exp cmpExpect
		if err := yaml.Unmarshal(data, &exp); err != nil {
			t.Fatalf("%s: %v", f, err)
		}
		folder := filepath.Base(filepath.Dir(f))
		for _, p := range exp.Pages {
			t.Run(folder+"/"+p.Page, func(t *testing.T) {
				d := detect(t, bin, "https://"+cmpReplicaHost+"/"+folder+"/"+p.Page, mapArgs...)
				if d.CMP.Found != p.Found || d.CMP.Present != p.Present {
					t.Errorf("found %v present %v, want found %v present %v (detected by %q)", d.CMP.Found, d.CMP.Present, p.Found, p.Present, d.CMP.DetectedBy)
				}
				if p.Found && p.RejectLayer > 0 {
					layer := d.CMP.RejectLayer
					if d.Reject != nil && d.Reject.Layer > 0 {
						layer = d.Reject.Layer
					}
					if layer != p.RejectLayer {
						t.Errorf("Reject on layer %d, want %d", layer, p.RejectLayer)
					}
				}
				if p.Found && p.BannerAfterMS > 0 && d.CMP.BannerAtMS < 2000 {
					t.Errorf("a late banner (%d ms) reported at %d ms", p.BannerAfterMS, d.CMP.BannerAtMS)
				}
			})
		}
	}
}

// cmpReplicaHost is the name the CMP replica pages are served under.
const cmpReplicaHost = "cmp-replicas" + server.SiteSuffix

// detect runs consent detect --json on u and returns its data.
func detect(t *testing.T, bin, u string, extra ...string) consent.DetectResult {
	t.Helper()
	stdout, stderr, _ := runEcho(t, bin, t.TempDir(), echoEnv(), 2*time.Minute, slices.Concat([]string{"consent", "detect", u, "--json"}, extra)...)
	var env struct {
		Kind string               `json:"kind"`
		Data consent.DetectResult `json:"data"`
	}
	if err := json.Unmarshal([]byte(stdout), &env); err != nil || env.Kind != "consent-detect" {
		t.Fatalf("consent detect --json: %v (kind %q)\n%s\n%s", err, env.Kind, stdout, stderr)
	}
	return env.Data
}
