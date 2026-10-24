// Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
// SPDX-License-Identifier: MIT

//go:build docker

package sendmail

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/RhythmShahriar/echo/catcher"
)

// TestPHPMail sends with PHP's mail() in php:8.3-cli, sendmail_path set
// to the shim, into a real catcher. Run with: go test -tags docker ./sendmail
func TestPHPMail(t *testing.T) {
	const image = "php:8.3-cli"
	if _, err := exec.LookPath("docker"); err != nil {
		t.Skip("docker not installed")
	}
	if exec.Command("docker", "image", "inspect", image).Run() != nil {
		t.Skipf("docker image %s not present", image)
	}
	s := sink(t)
	dir := t.TempDir()
	build := exec.Command("go", "build", "-o", filepath.Join(dir, "rhythm-echo-sendmail"), "./testdata/shim")
	build.Env = append(os.Environ(), "CGO_ENABLED=0", "GOWORK=off")
	if out, err := build.CombinedOutput(); err != nil {
		t.Fatalf("build shim: %v\n%s", err, out)
	}
	b := make([]byte, 4)
	_, _ = rand.Read(b)
	script := `$ok = mail('Ann <ann@example.test>', 'Your order #42', "Thanks!\n.\nSee you.", ` +
		`['From' => 'shop@example.test', 'Bcc' => 'audit@example.test', 'X-Echo-Run' => 'r7/checkout']); echo $ok ? "sent" : "failed";`
	// The sink's address in PHP's environment and in sendmail_path itself
	// the way install generic prints it (quoted, as in php.ini): PHP
	// runs it through sh -c.
	for i, args := range [][]string{
		{"-e", EnvSMTP + "=" + os.Getenv(EnvSMTP), image, "php", "-d", "sendmail_path=/echo/rhythm-echo-sendmail -t -i"},
		{image, "php", "-d", `sendmail_path="` + EnvSMTP + "=" + os.Getenv(EnvSMTP) + ` /echo/rhythm-echo-sendmail -t -i"`},
	} {
		run := append([]string{"run", "--rm", "--name", "echo-sendmail-test-" + hex.EncodeToString(b) + fmt.Sprint(i), "--network", "host",
			"-v", dir + ":/echo:ro"}, args...)
		out, err := exec.Command("docker", append(run, "-r", script)...).CombinedOutput() //nolint:gosec // fixed test command
		if err != nil || strings.TrimSpace(string(out)) != "sent" {
			t.Fatalf("php mail() %d: %v\n%s", i, err, out)
		}
		m := one(t, s)
		if m.Subject != "Your order #42" || m.From != "shop@example.test" || len(m.To) != 1 || m.To[0] != "ann@example.test" ||
			len(m.BCC) != 1 || m.BCC[0] != "audit@example.test" || m.Run != "r7" || !strings.Contains(m.Text, "See you.") {
			t.Errorf("message %d = %+v", i, m)
		}
		s.Inbox.Clear(catcher.Filter{})
	}
}
