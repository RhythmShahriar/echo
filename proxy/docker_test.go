// Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
// SPDX-License-Identifier: MIT

//go:build docker

// Tests with clients in Docker. Run with: go test -tags docker ./proxy
// The containers use the host network to reach the proxy on loopback and
// map api.twilio.com to 127.0.0.1 in /etc/hosts, so a client that ignored
// the proxy would hit a closed local port, never the real host.
package proxy

import (
	"crypto/rand"
	"encoding/hex"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

const phpImage = "php:8.3-cli"

// phpClient posts with cURL. argv: url form auth cafile [putenv].
// With a fifth argument the proxy is set at runtime with putenv().
const phpClient = `<?php
[$_, $url, $form, $auth, $ca] = $argv;
if (isset($argv[5])) { putenv($argv[5]); }
$ch = curl_init($url);
curl_setopt_array($ch, [CURLOPT_POST => true, CURLOPT_POSTFIELDS => $form, CURLOPT_RETURNTRANSFER => true,
    CURLOPT_CAINFO => $ca, CURLOPT_TIMEOUT => 10,
    CURLOPT_HTTPHEADER => ['Authorization: ' . $auth, 'Content-Type: application/x-www-form-urlencoded']]);
$body = curl_exec($ch);
if ($body === false) { fwrite(STDERR, curl_error($ch) . "\n"); exit(1); }
echo curl_getinfo($ch, CURLINFO_RESPONSE_CODE), "\n", $body;
`

func needDocker(t *testing.T, image string) {
	t.Helper()
	if _, err := exec.LookPath("docker"); err != nil {
		t.Skip("docker not installed")
	}
	if exec.Command("docker", "image", "inspect", image).Run() != nil {
		t.Skipf("docker image %s not present", image)
	}
}

func runPHP(t *testing.T, h *harness, ca string, env []string, putenv string) ([]byte, error) {
	t.Helper()
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "client.php"), []byte(phpClient), 0o644); err != nil {
		t.Fatal(err)
	}
	pem, err := os.ReadFile(ca)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "ca.pem"), pem, 0o644); err != nil {
		t.Fatal(err)
	}
	b := make([]byte, 4)
	_, _ = rand.Read(b)
	args := []string{"run", "--rm", "--name", "echo-proxy-test-" + hex.EncodeToString(b), "--network", "host",
		"--add-host", twilioHost + ":127.0.0.1", "-v", dir + ":/w:ro"}
	for _, e := range env {
		args = append(args, "-e", e)
	}
	args = append(args, phpImage, "php", "/w/client.php", "https://"+twilioHost+twilioPath, twilioForm, twilioAuth, "/w/ca.pem")
	if putenv != "" {
		args = append(args, putenv)
	}
	return exec.Command("docker", args...).CombinedOutput()
}

func TestPHPCurl(t *testing.T) {
	needDocker(t, phpImage)
	runScenarios(t, func(t *testing.T, h *harness, ca string) (int, string) {
		out, err := runPHP(t, h, ca, []string{"https_proxy=" + h.proxyURL}, "")
		if err != nil {
			t.Fatalf("php: %v\n%s", err, out)
		}
		return parseOut(t, out)
	})
}

// TestSpikePHPPutenv answers: does putenv('https_proxy=…') at runtime
// reach libcurl? (libcurl reads the proxy variables when each transfer
// starts, so it should.) Also checks the uppercase name.
func TestSpikePHPPutenv(t *testing.T) {
	needDocker(t, phpImage)
	for _, name := range []string{"https_proxy", "HTTPS_PROXY"} {
		t.Run(name, func(t *testing.T) {
			h := newHarness(t, setup{mode: "seal", mitm: true})
			out, err := runPHP(t, h, h.echoCA, nil, name+"="+h.proxyURL)
			if err != nil {
				t.Fatalf("putenv(%s) did not reach libcurl: %v\n%s", name, err, out)
			}
			status, body := parseOut(t, out)
			if status != 201 || !strings.Contains(body, "queued") {
				t.Fatalf("got %d %s", status, body)
			}
			t.Logf("putenv('%s=…') at runtime: proxied, faked by Echo", name)
		})
	}
}
