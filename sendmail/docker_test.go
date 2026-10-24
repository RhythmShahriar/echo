//go:build docker

package sendmail

import (
	"crypto/rand"
	"encoding/hex"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
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
	out, err := exec.Command("docker", "run", "--rm", "--name", "echo-sendmail-test-"+hex.EncodeToString(b), "--network", "host",
		"-v", dir+":/echo:ro", "-e", EnvSMTP+"="+os.Getenv(EnvSMTP), image,
		"php", "-d", "sendmail_path=/echo/rhythm-echo-sendmail -t -i", "-r", script).CombinedOutput()
	if err != nil || strings.TrimSpace(string(out)) != "sent" {
		t.Fatalf("php mail(): %v\n%s", err, out)
	}
	m := one(t, s)
	if m.Subject != "Your order #42" || m.From != "shop@example.test" || len(m.To) != 1 || m.To[0] != "ann@example.test" ||
		len(m.BCC) != 1 || m.BCC[0] != "audit@example.test" || m.Run != "r7" || !strings.Contains(m.Text, "See you.") {
		t.Errorf("message = %+v", m)
	}
}
