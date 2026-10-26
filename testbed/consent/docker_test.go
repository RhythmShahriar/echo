// Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
// SPDX-License-Identifier: MIT

//go:build docker

package consenttestbed

import (
	"bytes"
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// docker runs the docker CLI with args and returns its output.
func docker(ctx context.Context, args ...string) (string, error) {
	var out bytes.Buffer
	cmd := exec.CommandContext(ctx, "docker", args...)
	cmd.Stdout, cmd.Stderr = &out, &out
	err := cmd.Run()
	return out.String(), err
}

// composeArgs runs docker compose on this testbed.
func composeArgs(args ...string) []string {
	return append([]string{"compose", "-f", "docker-compose.yml", "--profile", "test"}, args...)
}

// needDocker skips without a Docker daemon.
func needDocker(t *testing.T) {
	t.Helper()
	if _, err := docker(t.Context(), "info", "--format", "{{.ServerVersion}}"); err != nil {
		t.Skip("no Docker daemon")
	}
}

// startSealed starts the services the seal needs (and the sealcheck
// client), building runtime/ first with make when it is missing. It
// stops them again unless the testbed was already up.
func startSealed(t *testing.T) {
	t.Helper()
	ctx, cancel := context.WithTimeout(t.Context(), 15*time.Minute)
	defer cancel()
	if _, err := os.Stat(filepath.Join("runtime", "linux", "dns")); err != nil {
		cmd := exec.CommandContext(ctx, "make", "runtime")
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("make runtime: %v\n%s", err, out)
		}
	}
	running, _ := docker(ctx, composeArgs("ps", "-q", "gate")...)
	// The cleanup goes in before up, so a failed up leaves nothing
	// running either.
	if strings.TrimSpace(running) == "" {
		t.Cleanup(func() {
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
			defer cancel()
			_, _ = docker(ctx, composeArgs("down", "-v", "--remove-orphans")...)
		})
	}
	if out, err := docker(ctx, composeArgs("up", "-d", "--wait", "dns", "gate", "shop", "tags", "echo", "sealcheck")...); err != nil {
		t.Fatalf("compose up: %v\n%s", err, out)
	}
}

// TestSealed: from a container with the runner's network and resolver,
// nothing on the internet answers and every name, real or not, resolves
// to the gate alone.
func TestSealed(t *testing.T) {
	needDocker(t)
	startSealed(t)
	ctx, cancel := context.WithTimeout(t.Context(), 2*time.Minute)
	defer cancel()
	in := func(sh string) (string, error) {
		return docker(ctx, composeArgs("exec", "-T", "sealcheck", "sh", "-c", sh)...)
	}
	// A TCP connection to a public address has no route.
	if out, err := in("nc -w 3 1.1.1.1 443 </dev/null && echo CONNECTED"); err == nil || strings.Contains(out, "CONNECTED") {
		t.Errorf("1.1.1.1:443 answered from the sealed network: %s", out)
	}
	if out, err := in("wget -q -T 5 -O /dev/null http://93.184.215.14/ && echo FETCHED"); err == nil || strings.Contains(out, "FETCHED") {
		t.Errorf("a public address answered over HTTP: %s", out)
	}
	// Every name resolves to the gate, through the system resolver.
	for _, name := range []string{"example.com", "www.google-analytics.com", "c01.shop.test", "metadata.google.internal"} {
		out, err := in("getent hosts " + name + " || nslookup " + name)
		if err != nil {
			t.Errorf("%s doesn't resolve: %v %s", name, err, out)
			continue
		}
		var addrs []string
		for _, f := range strings.Fields(out) {
			if strings.Count(f, ".") == 3 && f != "127.0.0.11" && f[0] >= '0' && f[0] <= '9' {
				addrs = append(addrs, f)
			}
		}
		if len(addrs) == 0 {
			t.Errorf("%s: no address in %q", name, out)
		}
		for _, a := range addrs {
			if a != "10.254.250.10" {
				t.Errorf("%s resolves to %s, not the gate (%q)", name, a, out)
			}
		}
	}
	// And a real host's name reaches the gate, which hands it to tags as
	// a host it doesn't serve, never to the internet.
	if out, _ := in("wget -q -S -O /dev/null http://example.com/ 2>&1"); !strings.Contains(out, "421") {
		t.Errorf("http://example.com/ from the sealed network: %s", out)
	}
}

// TestPluginsPinned: the WordPress image holds the plugins at the
// versions and sums of plugins.lock (its build checks each zip with
// sha256sum and records what passed).
func TestPluginsPinned(t *testing.T) {
	needDocker(t)
	ctx, cancel := context.WithTimeout(t.Context(), 20*time.Minute)
	defer cancel()
	if out, err := docker(ctx, composeArgs("build", "-q", "wp")...); err != nil {
		t.Fatalf("building wp: %v\n%s", err, out)
	}
	out, err := docker(ctx, "run", "--rm", "--network", "none", "--entrypoint", "cat", "echo-consent-testbed-wp:6.8.3", "/usr/src/plugins.verified")
	if err != nil {
		t.Fatalf("reading plugins.verified: %v\n%s", err, out)
	}
	var want []string
	for _, e := range readLock(t) {
		want = append(want, e.slug+" "+e.version+" "+e.sum)
	}
	if got := strings.Split(strings.TrimSpace(out), "\n"); strings.Join(got, "\n") != strings.Join(want, "\n") {
		t.Errorf("the image verified:\n%s\nplugins.lock pins:\n%s", strings.Join(got, "\n"), strings.Join(want, "\n"))
	}
	for _, e := range readLock(t) {
		ver, err := docker(ctx, "run", "--rm", "--network", "none", "--entrypoint", "sh", "echo-consent-testbed-wp:6.8.3", "-c",
			"grep -h -m1 -i '^ *\\* *Version:\\|^Stable tag:' /usr/src/wordpress/wp-content/plugins/"+e.slug+"/*.php /usr/src/wordpress/wp-content/plugins/"+e.slug+"/readme.txt")
		if err != nil || !strings.Contains(ver, e.version) {
			t.Errorf("%s in the image says %q, want %s", e.slug, strings.TrimSpace(ver), e.version)
		}
	}
}
