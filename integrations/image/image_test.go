// Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
// SPDX-License-Identifier: MIT

//go:build docker

// Builds the image from the Dockerfile and runs it. Run with
// go test -tags docker ./integrations/image. The base image must be
// present (CI pulls it first); the test skips otherwise rather than
// reaching a registry on its own.

package image

import (
	"bytes"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"io"
	"net"
	"net/http"
	"net/smtp"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
	"testing"
	"time"
)

// Host ports the containers publish, on loopback only.
const (
	apiPort   = "20515"
	smtpPort  = "20516"
	proxyPort = "20517"
)

// baseImage is the FROM line's image, digest included.
func baseImage(t *testing.T, root string) string {
	t.Helper()
	b, err := os.ReadFile(filepath.Join(root, "Dockerfile"))
	if err != nil {
		t.Fatal(err)
	}
	m := regexp.MustCompile(`(?m)^FROM\s+(\S+)`).FindSubmatch(b)
	if m == nil {
		t.Fatal("the Dockerfile has no FROM line")
	}
	return string(m[1])
}

func docker(t *testing.T, args ...string) string {
	t.Helper()
	out, err := exec.Command("docker", args...).CombinedOutput()
	if err != nil {
		t.Fatalf("docker %s: %v\n%s", strings.Join(args, " "), err, out)
	}
	return strings.TrimSpace(string(out))
}

// build compiles a static Linux binary into a GoReleaser-style context
// (linux/<arch>/rhythm-echo) and builds the image for this machine's
// architecture. It returns the image tag and the binary.
func build(t *testing.T) (string, string) {
	t.Helper()
	if _, err := exec.LookPath("docker"); err != nil {
		t.Skip("docker not installed")
	}
	if exec.Command("docker", "buildx", "version").Run() != nil {
		t.Skip("docker buildx not installed")
	}
	root := repoRoot(t)
	if base := baseImage(t, root); exec.Command("docker", "image", "inspect", base).Run() != nil {
		t.Skipf("base image not present; pull it first: docker pull %s", base)
	}
	ctx := t.TempDir()
	bin := filepath.Join(ctx, "linux", runtime.GOARCH, "rhythm-echo")
	gobuild := exec.Command("go", "build", "-trimpath", "-o", bin, "./cmd/rhythm-echo") //nolint:gosec // our own package
	gobuild.Dir = root
	gobuild.Env = append(os.Environ(), "CGO_ENABLED=0", "GOOS=linux", "GOARCH="+runtime.GOARCH)
	if out, err := gobuild.CombinedOutput(); err != nil {
		t.Fatalf("go build: %v\n%s", err, out)
	}
	tag := "rhythm-echo-test:" + random()
	docker(t, "buildx", "build", "--load", "--platform", "linux/"+runtime.GOARCH, "-f", filepath.Join(root, "Dockerfile"), "-t", tag, ctx)
	t.Cleanup(func() { _ = exec.Command("docker", "image", "rm", "-f", tag).Run() })
	return tag, bin
}

func random() string {
	b := make([]byte, 4)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}

// start runs the image detached with Echo's ports published on loopback.
// run adds docker run options (-e, -v); args, when given, replace the
// image's default flags.
func start(t *testing.T, tag string, run []string, args ...string) string {
	t.Helper()
	name := "rhythm-echo-test-" + random()
	run = append([]string{"run", "-d", "--name", name, "-p", "127.0.0.1:" + apiPort + ":8025", "-p", "127.0.0.1:" + smtpPort + ":1025"}, run...)
	docker(t, append(append(run, tag), args...)...)
	t.Cleanup(func() {
		if t.Failed() {
			out, _ := exec.Command("docker", "logs", name).CombinedOutput()
			t.Logf("container log:\n%s", out)
		}
		_ = exec.Command("docker", "rm", "-f", name).Run()
	})
	deadline := time.Now().Add(15 * time.Second)
	for {
		if c, err := net.DialTimeout("tcp", "127.0.0.1:"+smtpPort, time.Second); err == nil {
			// Docker accepts on the published port before Echo listens;
			// a greeting means Echo is there.
			_ = c.SetReadDeadline(time.Now().Add(time.Second))
			buf := make([]byte, 3)
			_, err := c.Read(buf)
			_ = c.Close()
			if err == nil && string(buf) == "220" {
				return name
			}
		}
		if state := docker(t, "inspect", "-f", "{{.State.Running}}", name); state != "true" {
			out, _ := exec.Command("docker", "logs", name).CombinedOutput()
			t.Fatalf("the container stopped:\n%s", out)
		}
		if time.Now().After(deadline) {
			t.Fatal("Echo didn't start in the container")
		}
		time.Sleep(200 * time.Millisecond)
	}
}

func send(t *testing.T, auth smtp.Auth, subject string) {
	t.Helper()
	err := smtp.SendMail("127.0.0.1:"+smtpPort, auth, "shop@shop.test", []string{"ann@example.com", "audit@shop.test"}, []byte(testMail(subject)))
	if err != nil {
		t.Fatalf("SMTP: %v", err)
	}
}

// privateNetwork reports whether this Echo has up --private-network,
// which the image's default flags use (wired into up separately).
func privateNetwork(t *testing.T, bin string) bool {
	t.Helper()
	if runtime.GOOS != "linux" {
		return true // can't run the Linux binary here; trust the image
	}
	out, _ := exec.Command(bin, "help").CombinedOutput() //nolint:gosec // the binary this test built
	return bytes.Contains(out, []byte("--private-network"))
}

func TestImage(t *testing.T) {
	tag, bin := build(t)

	t.Run("metadata", func(t *testing.T) {
		var cfg struct {
			User         string
			ExposedPorts map[string]any
			Entrypoint   []string
			Cmd          []string
		}
		if err := json.Unmarshal([]byte(docker(t, "image", "inspect", "-f", "{{json .Config}}", tag)), &cfg); err != nil {
			t.Fatal(err)
		}
		if cfg.User != "nonroot:nonroot" {
			t.Errorf("user %q, want nonroot:nonroot", cfg.User)
		}
		for _, p := range []string{"8025/tcp", "1025/tcp"} {
			if _, ok := cfg.ExposedPorts[p]; !ok {
				t.Errorf("port %s isn't exposed: %v", p, cfg.ExposedPorts)
			}
		}
		if got := strings.Join(cfg.Entrypoint, " "); got != "/usr/local/bin/rhythm-echo up" {
			t.Errorf("entrypoint %q", got)
		}
		if got := strings.Join(cfg.Cmd, " "); got != "--listen=0.0.0.0:8025 --smtp=0.0.0.0:1025 --private-network" {
			t.Errorf("cmd %q", got)
		}
	})

	// With a token Echo listens anywhere; the sink wants the token as the
	// SMTP password and the API wants it in X-Echo-Token.
	t.Run("token", func(t *testing.T) {
		var args []string
		if !privateNetwork(t, bin) {
			t.Log("this Echo has no up --private-network yet; running with explicit flags")
			args = []string{"--listen=0.0.0.0:8025", "--smtp=0.0.0.0:1025"}
		}
		const token = "image-test-token"
		start(t, tag, []string{"-e", "RHYTHM_ECHO_TOKEN=" + token}, args...)
		send(t, smtp.PlainAuth("", "echo", token, "127.0.0.1"), "Order W-1001 received")
		checkParity(t, "http://127.0.0.1:"+apiPort, token, "Order W-1001 received")
	})

	// A new named volume on the state folder is writable by nonroot: the
	// proxy's CA is created in it and served.
	t.Run("state volume", func(t *testing.T) {
		vol := "rhythm-echo-test-" + random()
		t.Cleanup(func() { _ = exec.Command("docker", "volume", "rm", "-f", vol).Run() })
		start(t, tag, []string{"-e", "RHYTHM_ECHO_TOKEN=tok", "-p", "127.0.0.1:" + proxyPort + ":8026",
			"-v", vol + ":/home/nonroot/.cache/rhythm/echo"},
			"--listen=0.0.0.0:8025", "--smtp=0.0.0.0:1025", "--proxy=0.0.0.0:8026", "--mitm")
		var pem []byte
		for i := 0; i < 50 && !bytes.HasPrefix(pem, []byte("-----BEGIN CERTIFICATE-----")); i++ {
			time.Sleep(100 * time.Millisecond)
			if resp, err := http.Get("http://127.0.0.1:" + proxyPort + "/ca.pem"); err == nil {
				pem, _ = io.ReadAll(resp.Body)
				_ = resp.Body.Close()
			}
		}
		if !bytes.HasPrefix(pem, []byte("-----BEGIN CERTIFICATE-----")) {
			t.Fatalf("no CA certificate at /ca.pem: %q", pem)
		}
	})

	// The default flags: no token, on Docker's private bridge network.
	t.Run("private network", func(t *testing.T) {
		if !privateNetwork(t, bin) {
			t.Skip("this Echo has no up --private-network yet")
		}
		start(t, tag, nil)
		send(t, nil, "Welcome to the shop")
		checkParity(t, "http://127.0.0.1:"+apiPort, "", "Welcome to the shop")
	})
}
