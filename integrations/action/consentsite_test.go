// Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
// SPDX-License-Identifier: MIT

package action

import (
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// siteAddr is where TestConsentSite runs the server, away from the
// workflow's 18990 and the testbeds.
const siteAddr = "127.0.0.1:35100"

// TestConsentSite runs the unit tests of the consent site the
// action-test workflow starts (go test ./... skips testdata), then the
// built server itself: both pages, the hit counter and the refusal to
// listen beyond loopback.
func TestConsentSite(t *testing.T) {
	if testing.Short() {
		t.Skip("builds and starts the consent site")
	}
	pkg := "./testdata/consentsite"
	for _, args := range [][]string{{"vet", pkg}, {"test", "-count=1", pkg}} {
		cmd := exec.Command("go", args...) //nolint:gosec // go from PATH on this package
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("go %s: %v\n%s", strings.Join(args, " "), err, out)
		}
	}
	bin := filepath.Join(t.TempDir(), "consentsite")
	if out, err := exec.Command("go", "build", "-o", bin, pkg).CombinedOutput(); err != nil { //nolint:gosec // our own package
		t.Fatalf("build: %v\n%s", err, out)
	}

	if out, err := exec.Command(bin, "-addr", "0.0.0.0:35101").CombinedOutput(); err == nil || //nolint:gosec // the binary this test built
		!strings.Contains(string(out), "listen on 127.0.0.1 only") {
		t.Errorf("listened beyond loopback: %v\n%s", err, out)
	}

	srv := exec.Command(bin, "-addr", siteAddr) //nolint:gosec // the binary this test built
	srv.Stderr = os.Stderr
	if err := srv.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_ = srv.Process.Kill()
		_ = srv.Wait()
	})
	client := &http.Client{Timeout: 2 * time.Second}
	fetch := func(path string) (int, string) {
		t.Helper()
		var err error
		for range 100 {
			var resp *http.Response
			if resp, err = client.Get("http://" + siteAddr + path); err == nil {
				b, _ := io.ReadAll(resp.Body)
				_ = resp.Body.Close()
				return resp.StatusCode, string(b)
			}
			time.Sleep(50 * time.Millisecond)
		}
		t.Fatalf("GET %s: %v", path, err)
		return 0, ""
	}
	for path, want := range map[string]string{"/good/": "var fireAlways = false;", "/broken/": "var fireAlways = true;", "/hits": "0\n"} {
		if code, body := fetch(path); code != http.StatusOK || !strings.Contains(body, want) {
			t.Errorf("GET %s: %d, want %q in\n%s", path, code, want, body)
		}
	}
	fetch("/tr?id=1234567890&ev=PageView")
	if _, body := fetch("/hits"); body != "1\n" {
		t.Errorf("hits after one = %q", body)
	}
}
