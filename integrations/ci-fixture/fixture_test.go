// Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
// SPDX-License-Identifier: MIT

package cifixture

import (
	"encoding/json"
	"errors"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
)

// The fixture's .echo/config.yaml names the site at 127.0.0.1:20510;
// Echo gets the two ports after it.
const (
	sitePort = "20510"
	apiAddr  = "127.0.0.1:20511"
	smtpAddr = "127.0.0.1:20512"
)

// TestSnapshotAndCompare runs the fixture the way CI does: a good
// snapshot, a compare that finds nothing, then the broken state, which
// compare --fail-on broke reports as one BROKE and one NEW change.
func TestSnapshotAndCompare(t *testing.T) {
	if testing.Short() {
		t.Skip("starts PHP and Echo")
	}
	php, err := exec.LookPath("php")
	if err != nil {
		t.Skip("php isn't installed")
	}
	echo := build(t)
	dir := t.TempDir()
	if err := os.CopyFS(dir, os.DirFS(".")); err != nil {
		t.Fatal(err)
	}
	_ = os.RemoveAll(filepath.Join(dir, ".echo", "snapshots"))

	start(t, echo, dir, nil, "up", "--seal", "--listen", apiAddr, "--smtp", smtpAddr, "--plain")
	wait(t, "http://"+apiAddr+"/v1/health")
	start(t, php, filepath.Join(dir, "php"), []string{"ECHO_SMTP=" + smtpAddr}, "-S", "127.0.0.1:"+sitePort, "index.php")
	waitTCP(t, "127.0.0.1:"+sitePort)

	cli := func(want int, args ...string) string {
		t.Helper()
		cmd := exec.Command(echo, append(args, "--url", "http://"+apiAddr, "--plain")...) //nolint:gosec // the binary this test built
		cmd.Dir = dir
		out, err := cmd.CombinedOutput()
		code := 0
		var exit *exec.ExitError
		if errors.As(err, &exit) {
			code = exit.ExitCode()
		} else if err != nil {
			t.Fatal(err)
		}
		if code != want {
			t.Fatalf("rhythm-echo %s: exit %d, want %d\n%s", strings.Join(args, " "), code, want, out)
		}
		return string(out)
	}

	cli(0, "snapshot", "--name", "good")
	out := cli(0, "compare", "--fail-on", "broke")
	if !strings.Contains(out, "Everything is still sent the same way") {
		t.Errorf("a good compare reports changes:\n%s", out)
	}

	if err := os.WriteFile(filepath.Join(dir, "php", "broken"), nil, 0o600); err != nil {
		t.Fatal(err)
	}
	cli(1, "compare", "--fail-on", "broke")
	var c struct {
		Changes []struct{ Class, Title string } `json:"changes"`
	}
	if err := json.Unmarshal([]byte(cli(0, "report", "--json")), &c); err != nil {
		t.Fatal(err)
	}
	classes := map[string]int{}
	for _, ch := range c.Changes {
		classes[ch.Class]++
	}
	if classes["broke"] != 1 || classes["new"] != 1 {
		t.Errorf("broken compare: %+v, want one broke and one new", c.Changes)
	}
}

// build compiles rhythm-echo from this checkout.
func build(t *testing.T) string {
	t.Helper()
	bin := filepath.Join(t.TempDir(), "rhythm-echo")
	if runtime.GOOS == "windows" {
		bin += ".exe"
	}
	cmd := exec.Command("go", "build", "-o", bin, "../../cmd/rhythm-echo")
	cmd.Env = append(os.Environ(), "CGO_ENABLED=0")
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("build: %v\n%s", err, out)
	}
	return bin
}

// start runs a server for the rest of the test.
func start(t *testing.T, bin, dir string, env []string, args ...string) {
	t.Helper()
	cmd := exec.Command(bin, args...) //nolint:gosec // php and the binary this test built
	cmd.Dir = dir
	cmd.Env = append(os.Environ(), env...)
	log := filepath.Join(t.TempDir(), "log")
	f, err := os.Create(log) //nolint:gosec // the test's own file
	if err != nil {
		t.Fatal(err)
	}
	cmd.Stdout, cmd.Stderr = f, f
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_ = cmd.Process.Kill()
		_ = cmd.Wait()
		_ = f.Close()
		if t.Failed() {
			b, _ := os.ReadFile(log) //nolint:gosec // the test's own file
			t.Logf("%s:\n%s", filepath.Base(bin), b)
		}
	})
}

func wait(t *testing.T, url string) {
	t.Helper()
	for range 100 {
		if r, err := http.Get(url); err == nil { //nolint:gosec,noctx // a loopback test server
			_ = r.Body.Close()
			if r.StatusCode == http.StatusOK {
				return
			}
		}
		time.Sleep(100 * time.Millisecond)
	}
	t.Fatalf("%s didn't answer", url)
}

func waitTCP(t *testing.T, addr string) {
	t.Helper()
	for range 100 {
		if c, err := net.DialTimeout("tcp", addr, time.Second); err == nil {
			_ = c.Close()
			return
		}
		time.Sleep(100 * time.Millisecond)
	}
	t.Fatalf("%s didn't open", addr)
}
