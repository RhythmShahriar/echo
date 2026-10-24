//go:build docker

package proxy

import (
	"crypto/rand"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"
)

// TestComposeSeal brings up docs/seal/examples/docker-compose.yml (with a
// small app) and checks the seal: the app can't reach the internet, can
// reach Echo, its requests and mail are captured, and the proxy wants
// the token. Echo is played by testdata/echostub until the CLI exists.
func TestComposeSeal(t *testing.T) {
	needDocker(t, "nginx:1.27-alpine")
	needDocker(t, "alpine:3.20")
	dir := t.TempDir()
	bin := filepath.Join(dir, "rhythm-echo")
	build := exec.Command("go", "build", "-o", bin, "./testdata/echostub")
	build.Env = append(os.Environ(), "CGO_ENABLED=0", "GOWORK=off")
	if out, err := build.CombinedOutput(); err != nil {
		t.Fatalf("build stub: %v\n%s", err, out)
	}
	b := make([]byte, 4)
	_, _ = rand.Read(b)
	project, token := "echo-seal-test-"+hex.EncodeToString(b), "tok"+hex.EncodeToString(b)
	compose := func(args ...string) *exec.Cmd {
		c := exec.Command("docker", append([]string{"compose", "-p", project, "-f", "../docs/seal/examples/docker-compose.yml",
			"-f", "testdata/compose.override.yml"}, args...)...)
		c.Env = append(os.Environ(), "RHYTHM_ECHO_BIN="+bin, "RHYTHM_ECHO_TOKEN="+token,
			"RHYTHM_ECHO_PORT=18925", "RHYTHM_FRONT_PORT=18980")
		return c
	}
	t.Cleanup(func() {
		if out, err := compose("down", "-v", "--remove-orphans", "-t", "1").CombinedOutput(); err != nil {
			t.Logf("down: %v\n%s", err, out)
		}
	})
	if out, err := compose("up", "-d", "echo-ca", "app", "front").CombinedOutput(); err != nil {
		t.Fatalf("up: %v\n%s", err, out)
	}
	inApp := func(script string) (string, error) {
		out, err := compose("exec", "-T", "app", "sh", "-c", script).CombinedOutput()
		return string(out), err
	}
	// Wait for Echo to answer from inside the sealed network.
	var health string
	for i := 0; i < 30; i++ {
		out, err := inApp("env -u http_proxy wget -q -T 2 -O- --header 'X-Echo-Token: " + token + "' http://echo:8025/v1/health")
		if err == nil {
			health = out
			break
		}
		time.Sleep(500 * time.Millisecond)
	}
	if !strings.Contains(health, `"tool":"rhythm-echo"`) {
		logs, _ := compose("logs").CombinedOutput()
		t.Fatalf("app can't reach Echo: %q\n%s", health, logs)
	}

	t.Run("no route out", func(t *testing.T) {
		// The internal network has no gateway, so this fails at once:
		// nothing leaves the machine.
		if out, err := inApp("env -u http_proxy -u https_proxy wget -q -T 3 -O- http://1.1.1.1/"); err == nil {
			t.Fatalf("app reached the internet: %s", out)
		}
	})
	t.Run("ca certificate shared, key not", func(t *testing.T) {
		out, err := inApp("cat /echo-ca/ca.pem; ls /echo-ca")
		if err != nil || !strings.HasPrefix(out, "-----BEGIN CERTIFICATE-----") || strings.Contains(out, "ca-key.pem") {
			t.Fatalf("%v %s", err, out)
		}
	})
	t.Run("requests are faked through the proxy", func(t *testing.T) {
		// BusyBox wget turns POST into GET when it uses a proxy, so the
		// request is written by hand.
		auth := base64.StdEncoding.EncodeToString([]byte("echo:" + token))
		req := "POST http://" + twilioHost + twilioPath + " HTTP/1.1\\r\\nHost: " + twilioHost +
			"\\r\\nProxy-Authorization: Basic " + auth + "\\r\\nContent-Type: application/x-www-form-urlencoded" +
			"\\r\\nContent-Length: " + strconv.Itoa(len(twilioForm)) + "\\r\\nConnection: close\\r\\n\\r\\n" + strings.ReplaceAll(twilioForm, "%", "%%")
		out, err := inApp("printf '" + req + "' | nc -w 5 echo 8026")
		if err != nil || !strings.Contains(out, `"status":"queued"`) {
			t.Fatalf("%v %s", err, out)
		}
	})
	t.Run("https is intercepted with the shared CA", func(t *testing.T) {
		out, err := inApp("curl -sS -m 5 --cacert /echo-ca/ca.pem -d '" + twilioForm + "' https://" + twilioHost + twilioPath)
		if err != nil || !strings.Contains(out, `"status":"queued"`) {
			t.Fatalf("%v %s", err, out)
		}
	})
	t.Run("proxy needs the token", func(t *testing.T) {
		out, err := inApp("http_proxy=http://echo:8026 wget -q -T 5 -O- http://" + twilioHost + "/")
		if err == nil || !strings.Contains(out, "407") {
			t.Fatalf("open relay: %v %s", err, out)
		}
	})
	t.Run("mail is captured", func(t *testing.T) {
		out, err := inApp("printf 'To: ann@example.test\\nSubject: Sealed hello\\n\\nhi\\n' | sendmail -S echo:1025 -f shop@example.test -t")
		if err != nil {
			t.Fatalf("%v %s", err, out)
		}
	})
	t.Run("inbox on the host", func(t *testing.T) {
		req, _ := http.NewRequest(http.MethodGet, "http://127.0.0.1:18925/v1/wait?count=3&timeout=5s&quiet=200ms", nil)
		req.Header.Set("X-Echo-Token", token)
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		defer resp.Body.Close()
		var ms []struct{ Channel, Provider, Status, Subject string }
		if err := json.NewDecoder(resp.Body).Decode(&ms); err != nil {
			t.Fatal(err)
		}
		got := map[string]bool{}
		for _, m := range ms {
			got[m.Channel+"/"+m.Provider+"/"+m.Status+"/"+m.Subject] = true
		}
		if len(ms) != 3 || !got["sms/twilio/faked/"] || !got["email//faked/Sealed hello"] {
			t.Errorf("messages = %+v", ms)
		}
	})
	t.Run("site reachable through the front", func(t *testing.T) {
		resp, err := http.Get("http://127.0.0.1:18980/")
		if err != nil {
			t.Fatal(err)
		}
		body, _ := io.ReadAll(resp.Body)
		_ = resp.Body.Close()
		if resp.StatusCode != 200 || !strings.Contains(string(body), "nginx") {
			t.Errorf("front: %s %.80s", resp.Status, body)
		}
	})
}
