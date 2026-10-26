// Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
// SPDX-License-Identifier: MIT

package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestPrintMap(t *testing.T) {
	var out, errOut bytes.Buffer
	if code := run([]string{"-print-map", "-listen", "127.0.0.1:23400"}, &out, &errOut); code != 0 {
		t.Fatalf("exit %d: %s", code, errOut.String())
	}
	line := strings.TrimSpace(out.String())
	for _, want := range []string{"--map-host '*.shop.test=127.0.0.1:23400'", "--map-host www.googletagmanager.com=127.0.0.1:23400", "--map-only"} {
		if !strings.Contains(line, want) {
			t.Errorf("-print-map lacks %q: %s", want, line)
		}
	}
}

func TestMakeCA(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "ca")
	var out, errOut bytes.Buffer
	if code := run([]string{"-make-ca", dir}, &out, &errOut); code != 0 {
		t.Fatalf("exit %d: %s", code, errOut.String())
	}
	if _, err := os.Stat(filepath.Join(dir, "ca.pem")); err != nil {
		t.Error(err)
	}
	if code := run([]string{"-make-ca", dir}, &out, &errOut); code != 1 || !strings.Contains(errOut.String(), "exists") {
		t.Errorf("second -make-ca: exit %d, %s", code, errOut.String())
	}
}

func TestBadRootAndFlags(t *testing.T) {
	var out, errOut bytes.Buffer
	if code := run([]string{"-root", t.TempDir()}, &out, &errOut); code != 1 || !strings.Contains(errOut.String(), "no sites folder") {
		t.Errorf("empty root: exit %d, %s", code, errOut.String())
	}
	if code := run([]string{"-nope"}, &out, &errOut); code != 2 {
		t.Errorf("unknown flag: exit %d", code)
	}
	errOut.Reset()
	if code := run([]string{"-tls", "-root", t.TempDir()}, &out, &errOut); code != 1 || !strings.Contains(errOut.String(), "make ca creates it") {
		t.Errorf("-tls without a CA: exit %d, %s", code, errOut.String())
	}
}
