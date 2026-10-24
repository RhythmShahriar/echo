// Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
// SPDX-License-Identifier: MIT

package action

import (
	"crypto/sha256"
	"encoding/hex"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

const archive = "rhythm-echo_linux_amd64.tar.gz"

// release writes a fake download: the archive, checksums.txt with the
// given lines (%s is the archive's real hash) and a bundle.
func release(t *testing.T, lines ...string) string {
	t.Helper()
	dir := t.TempDir()
	data := []byte("not really a tarball")
	sum := sha256.Sum256(data)
	if lines == nil {
		lines = []string{"%s  " + archive}
	}
	sums := "0000000000000000000000000000000000000000000000000000000000000000  rhythm-echo_darwin_arm64.tar.gz\n"
	for _, l := range lines {
		sums += strings.ReplaceAll(l, "%s", hex.EncodeToString(sum[:])) + "\n"
	}
	for name, b := range map[string][]byte{archive: data, "checksums.txt": []byte(sums), "checksums.txt.sigstore.json": []byte(`{"fake":true}`)} {
		if err := os.WriteFile(filepath.Join(dir, name), b, 0o600); err != nil {
			t.Fatal(err)
		}
	}
	return dir
}

// fakeCosign puts a cosign on PATH that records its arguments and exits
// with code. It returns the PATH to use and the file the arguments go to.
func fakeCosign(t *testing.T, code string) (path, argsFile string) {
	t.Helper()
	bin := t.TempDir()
	argsFile = filepath.Join(bin, "args")
	body := "#!/bin/sh\nprintf '%s\\n' \"$@\" > " + argsFile + "\nexit " + code + "\n"
	if err := os.WriteFile(filepath.Join(bin, "cosign"), []byte(body), 0o700); err != nil { //nolint:gosec // a test executable
		t.Fatal(err)
	}
	return "PATH=" + bin + ":/usr/bin:/bin", argsFile
}

func TestVerifyRelease(t *testing.T) {
	needBash(t, "awk")
	sh := filepath.Join(root(t), ".github", "scripts", "verify-release.sh")
	signed, _ := fakeCosign(t, "0")
	forged, _ := fakeCosign(t, "1")
	cases := []struct {
		name  string
		dir   func() string
		path  string
		code  int
		error string
	}{
		{"signed and matching", func() string { return release(t) }, signed, 0, ""},
		{"bad signature", func() string { return release(t) }, forged, 1, "isn't signed by Echo's release workflow"},
		{"no cosign", func() string { return release(t) }, "PATH=/usr/bin:/bin", 1, "cosign isn't installed"},
		{"no bundle", func() string {
			d := release(t)
			_ = os.Remove(filepath.Join(d, "checksums.txt.sigstore.json"))
			return d
		}, signed, 1, "checksums.txt.sigstore.json is missing"},
		{"changed archive", func() string {
			d := release(t)
			_ = os.WriteFile(filepath.Join(d, archive), []byte("evil"), 0o600)
			return d
		}, signed, 1, "doesn't match its checksum"},
		{"no line", func() string { return release(t, "%s  other.tar.gz") }, signed, 1, "has no line for " + archive},
		{"two lines", func() string { return release(t, "%s  "+archive, "ffff  "+archive) }, signed, 1, "more than once"},
		{"binary-mode line", func() string { return release(t, "%s *"+archive) }, signed, 0, ""},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			out, _, code := script(t, sh, []string{c.path}, c.dir(), archive)
			if code != c.code {
				t.Fatalf("exit %d, want %d:\n%s", code, c.code, out)
			}
			if c.error != "" && (!strings.Contains(out, "::error::Echo's release didn't verify: ") || !strings.Contains(out, c.error)) {
				t.Errorf("output lacks %q:\n%s", c.error, out)
			}
		})
	}
}

// TestVerifyReleaseIdentity checks what cosign is asked to prove: the
// signer's workflow, issuer and repository.
func TestVerifyReleaseIdentity(t *testing.T) {
	needBash(t, "awk")
	sh := filepath.Join(root(t), ".github", "scripts", "verify-release.sh")
	path, argsFile := fakeCosign(t, "0")
	dir := release(t)
	if out, _, code := script(t, sh, []string{path}, dir, archive); code != 0 {
		t.Fatalf("exit %d:\n%s", code, out)
	}
	b, err := os.ReadFile(argsFile) //nolint:gosec // the test's own file
	if err != nil {
		t.Fatal(err)
	}
	args := strings.Split(strings.TrimSpace(string(b)), "\n")
	flag := func(name string) string {
		for i, a := range args {
			if a == name && i+1 < len(args) {
				return args[i+1]
			}
		}
		t.Fatalf("cosign wasn't given %s: %q", name, args)
		return ""
	}
	if args[0] != "verify-blob" || args[len(args)-1] != filepath.Join(dir, "checksums.txt") {
		t.Errorf("cosign args = %q", args)
	}
	if got := flag("--bundle"); got != filepath.Join(dir, "checksums.txt.sigstore.json") {
		t.Errorf("--bundle %s", got)
	}
	if got := flag("--certificate-oidc-issuer"); got != "https://token.actions.githubusercontent.com" {
		t.Errorf("issuer %s", got)
	}
	if got := flag("--certificate-github-workflow-repository"); got != "RhythmShahriar/echo" {
		t.Errorf("repository %s", got)
	}
	re := regexp.MustCompile(flag("--certificate-identity-regexp"))
	const wf = "https://github.com/RhythmShahriar/.github/.github/workflows/release-go.yml@"
	for _, id := range []string{wf + "refs/heads/master", wf + "refs/tags/v1.2.0", wf + "d296f34c9b6629211c68561504d938c404afaf74"} {
		if !re.MatchString(id) {
			t.Errorf("identity %s is refused", id)
		}
	}
	for _, id := range []string{
		"https://github.com/someone/.github/.github/workflows/release-go.yml@refs/heads/master",
		"https://github.com/RhythmShahriar/.github/.github/workflows/ci-go.yml@refs/heads/master",
		wf + "refs/heads/evil",
		wf + "refs/heads/master/../x",
	} {
		if re.MatchString(id) {
			t.Errorf("identity %s is accepted", id)
		}
	}
}
