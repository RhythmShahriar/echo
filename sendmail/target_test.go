// Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
// SPDX-License-Identifier: MIT

package sendmail

import (
	"bytes"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/RhythmShahriar/echo/catcher"
)

// project makes a folder with .echo/config.yaml holding cfg and changes
// into a subfolder two levels down, as PHP does when it runs from public/.
func project(t *testing.T, cfg string) string {
	t.Helper()
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, ".echo"), 0o750); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(root, ".echo", "config.yaml")
	if err := os.WriteFile(path, []byte(cfg), 0o600); err != nil {
		t.Fatal(err)
	}
	sub := filepath.Join(root, "web", "public")
	if err := os.MkdirAll(sub, 0o750); err != nil {
		t.Fatal(err)
	}
	t.Chdir(sub)
	return path
}

func TestSinkFromHealth(t *testing.T) {
	s := sink(t)
	addr := os.Getenv(EnvSMTP)
	_, port, _ := strings.Cut(addr, ":")
	t.Setenv(EnvSMTP, "")
	health := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/health" {
			http.NotFound(w, r)
			return
		}
		// Listening on every interface: the shim dials the API's host.
		fmt.Fprintf(w, `{"tool":"rhythm-echo","mode":"seal","smtp":"0.0.0.0:%s"}`, port)
	}))
	defer health.Close()
	t.Setenv(EnvURL, health.URL)
	if code, errOut := run(t, phpStyle, "-t", "-i"); code != 0 {
		t.Fatalf("exit %d: %s", code, errOut)
	}
	if m := one(t, s); m.Source.Via != "smtp" || m.Subject != "Order #42" {
		t.Errorf("message = %+v", m)
	}
}

func TestSinkFromNearestConfig(t *testing.T) {
	s := sink(t)
	addr := os.Getenv(EnvSMTP)
	t.Setenv(EnvSMTP, "")
	project(t, "# staging\nsmtp: "+addr+"\nactions: [{name: x}]\n") // a bad action must not matter
	if code, errOut := run(t, phpStyle, "-t", "-i"); code != 0 {
		t.Fatalf("exit %d: %s", code, errOut)
	}
	if m := one(t, s); m.Source.Via != "smtp" {
		t.Errorf("message = %+v", m)
	}
}

func TestAPIFromConfigWhenSMTPIsOff(t *testing.T) {
	clearEnv(t)
	s := catcher.NewServer(catcher.NewInbox(100), catcher.Config{})
	api := httptest.NewServer(s)
	defer api.Close()
	project(t, "smtp: off\nlisten: "+strings.TrimPrefix(api.URL, "http://")+"\n")
	if code, errOut := run(t, phpStyle, "-t", "-i"); code != 0 {
		t.Fatalf("exit %d: %s", code, errOut)
	}
	if m := one(t, s); m.Source.Via != "sendmail" {
		t.Errorf("message = %+v", m)
	}
}

func TestFindTargetFallbacks(t *testing.T) {
	clearEnv(t)
	t.Chdir(t.TempDir())
	var stderr bytes.Buffer
	if got := findTarget(os.Getenv, &stderr); got.smtp != DefaultSMTP || got.api != "" {
		t.Errorf("nothing set: %+v", got)
	}
	path := project(t, "smtp: 127.0.0.1:2525\n")
	if got := findTarget(os.Getenv, &stderr); got.smtp != "127.0.0.1:2525" || got.from != path {
		t.Errorf("config: %+v", got)
	}
	t.Setenv(EnvSMTP, "127.0.0.1:3535")
	if got := findTarget(os.Getenv, &stderr); got.smtp != "127.0.0.1:3535" {
		t.Errorf("the environment must win over the config: %+v", got)
	}
	t.Setenv(EnvSMTP, "")
	project(t, "smtp: [not, an, address\n")
	if got := findTarget(os.Getenv, &stderr); got.smtp != DefaultSMTP || !strings.Contains(stderr.String(), "ignoring") {
		t.Errorf("broken config: %+v %q", got, stderr.String())
	}
	// Echo down at RHYTHM_ECHO_URL: the API, whose error names the URL.
	t.Setenv(EnvURL, "http://127.0.0.1:1")
	if got := findTarget(os.Getenv, &stderr); got.api != "http://127.0.0.1:1" {
		t.Errorf("unreachable URL: %+v", got)
	}
}

func TestUnreachableNamesWhereTheAddressCameFrom(t *testing.T) {
	clearEnv(t)
	path := project(t, "smtp: 127.0.0.1:1\n") // nothing listens
	code, errOut := run(t, "Subject: x\n\nhi\n", "a@example.test")
	if code != exUnavailable || !strings.Contains(errOut, "came from "+path) {
		t.Errorf("exit %d %q", code, errOut)
	}
}

func TestReachable(t *testing.T) {
	for _, tc := range []struct{ listen, base, want string }{
		{"0.0.0.0:1025", "http://host.docker.internal:8025", "host.docker.internal:1025"},
		{"[::]:1025", "http://127.0.0.1:8025", "127.0.0.1:1025"},
		{":2525", "http://10.0.0.5:8025", "10.0.0.5:2525"},
		{"127.0.0.1:2525", "http://localhost:8025", "127.0.0.1:2525"},
		{"127.0.0.1:2525", "http://host.docker.internal:8025", ""}, // Echo's loopback isn't ours
		{"10.0.0.5:2525", "http://echo.test:8025", "10.0.0.5:2525"},
		{"not an address", "http://127.0.0.1:8025", ""},
	} {
		if got := reachable(tc.listen, tc.base); got != tc.want {
			t.Errorf("reachable(%q, %q) = %q, want %q", tc.listen, tc.base, got, tc.want)
		}
	}
}
