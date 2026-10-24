// Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
// SPDX-License-Identifier: MIT

package image

import (
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// fakeEcho writes a rhythm-echo stand-in whose up --help prints help.
func fakeEcho(t *testing.T, help string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "rhythm-echo")
	script := "#!/bin/sh\ncat <<'HELP'\n" + help + "\nHELP\n"
	if err := os.WriteFile(path, []byte(script), 0o700); err != nil { //nolint:gosec // a test script that must run
		t.Fatal(err)
	}
	return path
}

// TestCheckImageCmd checks the release gate that keeps an image whose
// default CMD names a flag its Echo lacks from shipping.
func TestCheckImageCmd(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("the check is a bash script")
	}
	if _, err := exec.LookPath("bash"); err != nil {
		t.Skip("no bash")
	}
	root := filepath.Join("..", "..")
	script := filepath.Join(root, ".github", "scripts", "check-image-cmd.sh")
	dockerfile := filepath.Join(root, "Dockerfile")
	const flags = "      --listen <addr>        API address\n      --smtp <addr>          SMTP sink\n"
	for _, tc := range []struct {
		name, help string
		ok         bool
	}{
		{"every flag", flags + "      --private-network      no token on private networks", true},
		{"no private network", flags, false},
		{"only a longer flag", flags + "      --private-network-only  something else", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			out, err := exec.Command("bash", script, fakeEcho(t, tc.help), dockerfile).CombinedOutput() //nolint:gosec // our own script
			if tc.ok != (err == nil) {
				t.Fatalf("err %v:\n%s", err, out)
			}
			if !tc.ok && !strings.Contains(string(out), "::error::the image's default CMD uses --private-network,") {
				t.Errorf("output:\n%s", out)
			}
		})
	}
}
